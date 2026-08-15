package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gregwinn/EmovisMicroService/internal/httpapi/gen"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

func decodeResult(t *testing.T, rec *httptest.ResponseRecorder) gen.IngestResult {
	t.Helper()

	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var result gen.IngestResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result),
		"success responses must match the IngestResult schema")
	return result
}

// A new transaction is 201 with duplicate=false, received, and priced —
// exactly as the contract's operation description states.
func TestNewTransactionIsCreated(t *testing.T) {
	h := newHarness(t, noChecks())

	rec := postJSON(t, h, validPayload())

	require.Equal(t, http.StatusCreated, rec.Code)

	result := decodeResult(t, rec)
	assert.False(t, result.Duplicate)
	assert.Equal(t, gen.AssociationStatus("received"), result.AssociationStatus)
	assert.Equal(t, gen.SettlementStatus("priced"), result.SettlementStatus)

	id, err := uuid.Parse(result.Id)
	require.NoError(t, err, "the id should be a UUID")
	assert.Equal(t, uuid.Version(7), id.Version(), "time-ordered ids keep inserts append-mostly")

	assert.Equal(t, 1, h.Store.Len())
}

// The heart of the contract: a producer retrying over an unreliable link gets
// 200 with the original record, and no second billable transaction exists.
func TestReplayReturnsTheExistingTransaction(t *testing.T) {
	h := newHarness(t, noChecks())

	first := decodeResult(t, postJSON(t, h, validPayload()))

	rec := postJSON(t, h, validPayload())
	require.Equal(t, http.StatusOK, rec.Code, "a matched idempotency key is 200, not 201")

	second := decodeResult(t, rec)
	assert.True(t, second.Duplicate)
	assert.Equal(t, first.Id, second.Id, "the original record is returned")
	assert.Equal(t, 1, h.Store.Len(), "no second billable record")
}

func TestReplayIsStableAcrossManyRetries(t *testing.T) {
	h := newHarness(t, noChecks())

	first := decodeResult(t, postJSON(t, h, validPayload()))

	for range 10 {
		rec := postJSON(t, h, validPayload())
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, first.Id, decodeResult(t, rec).Id)
	}

	assert.Equal(t, 1, h.Store.Len())
}

// source_reference is unique within a source, so the same reference from a
// different producer is a different transaction.
func TestDifferentProducersDoNotCollide(t *testing.T) {
	h := newHarness(t, noChecks())

	a := validPayload()
	a["source"] = "agency-a"
	a["source_reference"] = "1"

	b := validPayload()
	b["source"] = "agency-b"
	b["source_reference"] = "1"

	require.Equal(t, http.StatusCreated, postJSON(t, h, a).Code)
	require.Equal(t, http.StatusCreated, postJSON(t, h, b).Code)

	assert.Equal(t, 2, h.Store.Len())
}

// 🔑 The gap the contract leaves open. A key match with different content still
// answers exactly as the contract promises — 200, duplicate, stored record —
// because producers have no branch for anything else. The difference is logged
// for alerting rather than silently discarded. See ADR-0006.
func TestDivergentReplayIsAnsweredOnContractAndLogged(t *testing.T) {
	h := newHarness(t, noChecks())

	original := decodeResult(t, postJSON(t, h, validPayload()))

	mutated := validPayload()
	mutated["base_amount"] = "99.00"

	rec := postJSON(t, h, mutated)

	require.Equal(t, http.StatusOK, rec.Code, "the contract defines no conflict status")

	result := decodeResult(t, rec)
	assert.True(t, result.Duplicate)
	assert.Equal(t, original.Id, result.Id)

	logs := h.Logs.String()
	assert.Contains(t, logs, "duplicate transaction diverges from the stored record")
	assert.Contains(t, logs, `"stored_amount":"12.50 USD"`)
	assert.Contains(t, logs, `"submitted_amount":"99.00 USD"`)

	stored, err := h.Store.Get(transaction.Key{Source: "lane-controller-07", SourceReference: "LC07-20260814-000918"})
	require.NoError(t, err)
	assert.Equal(t, "12.50", stored.BaseAmount.AsReceived(), "base_amount is immutable once accepted")
}

// An ordinary replay must not trip the divergence alarm, or the metric becomes
// noise and stops being actionable.
func TestOrdinaryReplayIsNotLoggedAsDivergent(t *testing.T) {
	h := newHarness(t, noChecks())

	postJSON(t, h, validPayload())
	postJSON(t, h, validPayload())

	assert.NotContains(t, h.Logs.String(), "diverges from the stored record")
}

// The rule the JSON schema cannot express. This payload is contract-valid and
// still unbillable.
func TestContractValidPayloadWithNoIdentifierIsRejected(t *testing.T) {
	h := newHarness(t, noChecks())

	payload := validPayload()
	delete(payload, "plate")

	rec := postJSON(t, h, payload)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, errorFields(t, rec), "at least one usable identifier")
	assert.Zero(t, h.Store.Len(), "a rejected transaction is never stored")
}

// Semantic failures the schema lets through, each returning the contract's
// Error shape.
func TestSemanticRejections(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(map[string]any)
		wantFields string
	}{
		{
			name:       "unrecognized transaction type",
			mutate:     func(p map[string]any) { p["transaction_type"] = "parking" },
			wantFields: `transaction_type: unrecognized value "parking"`,
		},
		{
			name:       "timestamp too far in the future",
			mutate:     func(p map[string]any) { p["transaction_time_utc"] = "2027-08-14T13:45:02Z" },
			wantFields: "transaction_time_utc: is more than 5m0s in the future",
		},
		{
			name:       "amount is not a decimal string",
			mutate:     func(p map[string]any) { p["base_amount"] = "twelve fifty" },
			wantFields: "base_amount: must be a decimal string",
		},
		{
			name:       "negative amount",
			mutate:     func(p map[string]any) { p["base_amount"] = "-12.50" },
			wantFields: "base_amount: must not be negative",
		},
		{
			name:       "more precision than the currency allows",
			mutate:     func(p map[string]any) { p["base_amount"] = "12.505" },
			wantFields: "base_amount: has more than 2 decimal places, which USD does not allow",
		},
		{
			name:       "unknown currency",
			mutate:     func(p map[string]any) { p["currency"] = "XYZ" },
			wantFields: `currency: unrecognized ISO-4217 code "XYZ"`,
		},
		{
			name: "identifiers present but unusable",
			mutate: func(p map[string]any) {
				p["plate"] = map[string]any{"number": "--", "jurisdiction": "TX"}
				p["transponder_number"] = "  "
			},
			wantFields: "at least one usable identifier",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, noChecks())

			payload := validPayload()
			tt.mutate(payload)

			rec := postJSON(t, h, payload)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, errorFields(t, rec), tt.wantFields)
			assert.Zero(t, h.Store.Len())
		})
	}
}

// Old timestamps are explicitly fine: batch and image-review producers submit
// long after the vehicle passed, and a posting window would reject real
// revenue.
func TestBackdatedTransactionsAreAccepted(t *testing.T) {
	for _, occurred := range []string{
		"2026-08-01T00:00:00Z", // two weeks earlier
		"2025-08-14T13:45:02Z", // a year earlier
		"2019-01-01T00:00:00Z", // a batch replay from the archive
	} {
		t.Run(occurred, func(t *testing.T) {
			h := newHarness(t, noChecks())

			payload := validPayload()
			payload["transaction_time_utc"] = occurred

			assert.Equal(t, http.StatusCreated, postJSON(t, h, payload).Code)
		})
	}
}

// Semantic failures are reported together, like schema failures.
func TestAllSemanticFailuresAreReportedTogether(t *testing.T) {
	h := newHarness(t, noChecks())

	payload := validPayload()
	payload["transaction_type"] = "parking"
	payload["base_amount"] = "-1.00"
	delete(payload, "plate")

	rec := postJSON(t, h, payload)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	fields := errorFields(t, rec)
	assert.Contains(t, fields, "transaction_type")
	assert.Contains(t, fields, "base_amount")
	assert.Contains(t, fields, "at least one usable identifier")
}

// Plate and transponder values are PII and must not reach log aggregation.
func TestIdentifiersAreNotLogged(t *testing.T) {
	h := newHarness(t, noChecks())

	payload := validPayload()
	payload["plate"] = map[string]any{"number": "SECRET99", "jurisdiction": "TX"}
	payload["transponder_number"] = "0180099999999"

	// Both an accepted request and a rejected one.
	postJSON(t, h, payload)

	rejected := validPayload()
	rejected["plate"] = map[string]any{"number": "SECRET99", "jurisdiction": "TX"}
	rejected["base_amount"] = "-1.00"
	rejected["source_reference"] = "other"
	postJSON(t, h, rejected)

	logs := h.Logs.String()
	assert.NotContains(t, logs, "SECRET99", "plate numbers are PII")
	assert.NotContains(t, logs, "0180099999999", "transponder ids are PII")
}

// Free-form producer data survives the round trip uninterpreted.
func TestFreeFormFieldsAreStoredVerbatim(t *testing.T) {
	h := newHarness(t, noChecks())

	payload := validPayload()
	payload["location"] = map[string]any{"facility": "SH-130", "plaza": "12", "lane": "3", "direction": "NB"}
	payload["metadata"] = map[string]any{"vendor": "acme", "confidence": 0.94}

	require.Equal(t, http.StatusCreated, postJSON(t, h, payload).Code)

	stored, err := h.Store.Get(transaction.Key{Source: "lane-controller-07", SourceReference: "LC07-20260814-000918"})
	require.NoError(t, err)

	assert.Equal(t, "SH-130", stored.Location["facility"])
	assert.Equal(t, "NB", stored.Location["direction"])
	assert.Equal(t, "acme", stored.Metadata["vendor"])
	assert.InEpsilon(t, 0.94, stored.Metadata["confidence"], 0.0001)
}

// A producer timing out and retrying while the first request is still in flight
// must not produce two billable records.
func TestConcurrentReplaysCreateOneTransaction(t *testing.T) {
	const attempts = 25

	h := newHarness(t, noChecks())

	var (
		mu      sync.Mutex
		created int
		ids     = map[string]struct{}{}
		wg      sync.WaitGroup
	)

	for range attempts {
		wg.Go(func() {
			rec := postJSON(t, h, validPayload())

			mu.Lock()
			defer mu.Unlock()
			if rec.Code == http.StatusCreated {
				created++
			}
			var result gen.IngestResult
			if json.Unmarshal(rec.Body.Bytes(), &result) == nil {
				ids[result.Id] = struct{}{}
			}
		})
	}
	wg.Wait()

	assert.Equal(t, 1, created, "exactly one request may create the record")
	assert.Len(t, ids, 1, "every caller is told about the same transaction")
	assert.Equal(t, 1, h.Store.Len())
}

func TestDistinctTransactionsAllPersist(t *testing.T) {
	const count = 20

	h := newHarness(t, noChecks())

	for i := range count {
		payload := validPayload()
		payload["source_reference"] = "LC07-" + strconv.Itoa(i)

		require.Equal(t, http.StatusCreated, postJSON(t, h, payload).Code)
	}

	assert.Equal(t, count, h.Store.Len())
}

// A storage failure is the service's fault, not the producer's: 500, no detail
// leaked, and the error logged for an operator.
func TestStoreFailureIsReportedAsServerError(t *testing.T) {
	h := newHarness(t, noChecks())

	failing := &failingStore{err: assert.AnError}
	router, err := NewRouter(Deps{
		Logger:  h.Logger(),
		Health:  noChecks(),
		Version: "test",
		Rules:   testRules(),
		Store:   failing,
		Metrics: h.Metrics,
	})
	require.NoError(t, err)

	rec := postJSON(t, router, validPayload())

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	body := decodeError(t, rec)
	assert.Nil(t, body.Fields, "storage errors have no field to blame")
	assert.NotContains(t, *body.Message, assert.AnError.Error(), "internal detail must not leak")

	assert.Contains(t, h.Logs.String(), "could not record transaction")
}

type failingStore struct{ err error }

func (s *failingStore) Ingest(context.Context, transaction.Transaction) (transaction.IngestOutcome, error) {
	return transaction.IngestOutcome{}, s.err
}

// Metrics are how an operator sees producer behaviour without reading logs.
func TestIngestRecordsMetrics(t *testing.T) {
	h := newHarness(t, noChecks())

	postJSON(t, h, validPayload()) // created
	postJSON(t, h, validPayload()) // duplicate

	divergent := validPayload()
	divergent["base_amount"] = "99.00"
	postJSON(t, h, divergent) // divergent duplicate

	rejected := validPayload()
	rejected["source_reference"] = "other"
	rejected["transaction_type"] = "parking"
	postJSON(t, h, rejected) // rejected

	exported := gatherMetrics(t, h)

	assert.Contains(t, exported, `ingest_transactions_total{result="created",source="lane-controller-07"} 1`)
	assert.Contains(t, exported, `ingest_transactions_total{result="duplicate",source="lane-controller-07"} 2`)
	assert.Contains(t, exported, `ingest_transactions_total{result="rejected",source="lane-controller-07"} 1`)
	assert.Contains(t, exported, `ingest_divergent_duplicates_total{source="lane-controller-07"} 1`)

	// The breakdown that turns "producer X is broken" into a dashboard.
	assert.Contains(t, exported,
		`ingest_validation_failures_total{field="transaction_type",layer="semantic",source="lane-controller-07"} 1`)
}

// Contract-layer rejections are counted too, attributed to an unknown producer:
// the body failed validation, so its `source` cannot be trusted.
func TestContractRejectionsAreCounted(t *testing.T) {
	h := newHarness(t, noChecks())

	postJSON(t, h, map[string]any{})

	exported := gatherMetrics(t, h)
	assert.Contains(t, exported, `layer="contract"`)
	assert.Contains(t, exported, `source="unknown"`)
}

// The route label is the registered pattern, never the raw path — a label taken
// from user input has unbounded cardinality.
func TestHTTPMetricsUseTheRoutePatternNotThePath(t *testing.T) {
	h := newHarness(t, noChecks())

	postJSON(t, h, validPayload())
	// A path that matches nothing must not create its own time series.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/nope/"+uuid.NewString(), nil))

	exported := gatherMetrics(t, h)
	assert.Contains(t, exported, `route="POST /ingest/v1/transactions"`)
	assert.Contains(t, exported, `route="unmatched"`)
	assert.NotContains(t, exported, "/nope/")
}

func TestMetricsEndpointIsServed(t *testing.T) {
	rec := get(t, testRouter(t, noChecks()), "/metrics")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "go_goroutines")
}

func gatherMetrics(t *testing.T, h *harness) string {
	t.Helper()

	families, err := h.Metrics.Registry().Gather()
	require.NoError(t, err)

	var b strings.Builder
	enc := expfmt.NewEncoder(&b, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, f := range families {
		require.NoError(t, enc.Encode(f))
	}
	return b.String()
}
