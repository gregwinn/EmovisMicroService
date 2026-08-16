package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gregwinn/EmovisMicroService/internal/httpapi/middleware"
)

// These are regression tests for issues found in a security pass over the
// running service. Each one failed before its fix.

// Before the body limit, a 61 MB request was accepted with a 201, cost roughly
// twice that in heap, and stored its payload permanently.
func TestOversizedBodyIsRejected(t *testing.T) {
	h := newHarness(t, noChecks())

	payload := validPayload()
	payload["metadata"] = map[string]any{"pad": strings.Repeat("A", int(middleware.DefaultMaxBodyBytes)+1024)}

	rec := postJSON(t, h, payload)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code,
		"an oversized body must be refused, not buffered and stored")

	body := decodeError(t, rec)
	require.NotNil(t, body.Fields)
	assert.Contains(t, *body.Fields, "body:")
	assert.Contains(t, *body.Fields, strconv.FormatInt(middleware.DefaultMaxBodyBytes, 10),
		"the producer should be told the limit so they can fix their integration")

	assert.Zero(t, h.Store.Len(), "nothing oversized reaches the store")
}

// A payload comfortably inside the limit is unaffected. The limit must not be
// so tight that a verbose but legitimate producer is refused.
func TestGenerouslySizedLegitimatePayloadIsAccepted(t *testing.T) {
	h := newHarness(t, noChecks())

	payload := validPayload()
	// 64 KB of producer passthrough — far beyond anything realistic, far
	// inside the 1 MiB ceiling.
	payload["metadata"] = map[string]any{"pad": strings.Repeat("A", 64*1024)}

	assert.Equal(t, http.StatusCreated, postJSON(t, h, payload).Code)
}

// 🔑 `source` is producer-supplied and became a Prometheus label. Before the
// cap, 300 distinct values produced 302 series — on an endpoint the contract
// declares unauthenticated, that is a memory-exhaustion vector against this
// service and against whatever scrapes it.
func TestProducerCannotCreateUnboundedMetricSeries(t *testing.T) {
	h := newHarness(t, noChecks())

	const attempts = 400
	for i := range attempts {
		payload := validPayload()
		payload["source"] = "evil-" + strconv.Itoa(i)
		payload["source_reference"] = "ref-" + strconv.Itoa(i)
		postJSON(t, h, payload)
	}

	exported := gatherMetrics(t, h)

	series := strings.Count(exported, "\ningest_transactions_total{")
	assert.Less(t, series, attempts,
		"series count must not grow one-for-one with distinct producer values")
	assert.Contains(t, exported, `source="other"`,
		"values beyond the ceiling collapse into a single bucket")

	// The aggregate is still correct even though the breakdown is capped.
	assert.Contains(t, exported, "ingest_source_labels_tracked")
}

// The cap must not damage the normal case: a handful of real producers keep
// their own series.
func TestKnownProducersKeepTheirOwnSeries(t *testing.T) {
	h := newHarness(t, noChecks())

	for _, source := range []string{"lane-controller-07", "image-review-vendor", "interop-peer"} {
		payload := validPayload()
		payload["source"] = source
		payload["source_reference"] = "ref-" + source
		require.Equal(t, http.StatusCreated, postJSON(t, h, payload).Code)
	}

	exported := gatherMetrics(t, h)
	for _, source := range []string{"lane-controller-07", "image-review-vendor", "interop-peer"} {
		assert.Contains(t, exported, `source="`+source+`"`)
	}
	assert.NotContains(t, exported, `source="other"`)
}

// The contract puts no maxLength on transaction_type, and the rejection echoed
// it verbatim: a 4 MB request produced a 4 MB error response.
func TestRejectionDoesNotEchoAnUnboundedValue(t *testing.T) {
	h := newHarness(t, noChecks())

	payload := validPayload()
	payload["transaction_type"] = strings.Repeat("Z", 200_000)

	rec := postJSON(t, h, payload)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Less(t, rec.Body.Len(), 1024,
		"the error must not reflect the producer's payload back at them")
	assert.Contains(t, errorFields(t, rec), "…", "the echoed value is truncated")
}

// Readiness is unauthenticated. A driver error names the database host, port,
// user, and database — in production, the RDS endpoint.
func TestReadinessDoesNotLeakInfrastructureDetail(t *testing.T) {
	const secretish = "internal-db-prod-01.corp.example.com:5432"

	checker := noChecks()
	checker.Register("database", func(context.Context) error {
		return errors.New("failed to connect to `user=ingest database=ingest`: " + secretish)
	})

	h := newHarness(t, checker)
	rec := get(t, h, "/readyz")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	assert.NotContains(t, rec.Body.String(), secretish,
		"the probe must not publish infrastructure topology")
	assert.NotContains(t, rec.Body.String(), "user=ingest")
	assert.Contains(t, rec.Body.String(), "unavailable", "the caller still learns it is down")

	// The operator still gets the real error, where access is controlled.
	assert.Contains(t, h.Logs.String(), secretish)
	assert.Contains(t, h.Logs.String(), "readiness check failed")
}

// Producer input reaches the error body. It must be escaped, and the response
// must not be interpretable as anything but JSON.
func TestReflectedInputIsEscapedAndTypedAsJSON(t *testing.T) {
	h := newHarness(t, noChecks())

	payload := validPayload()
	payload["transaction_type"] = `<script>alert(1)</script>`

	rec := postJSON(t, h, payload)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.NotContains(t, rec.Body.String(), "<script>", "must be escaped, not raw")

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
}

// Deeply nested free-form JSON must not exhaust the stack.
func TestDeeplyNestedPayloadIsRejectedWithoutCrashing(t *testing.T) {
	h := newHarness(t, noChecks())

	const depth = 50_000
	raw := `{"source":"a","source_reference":"b","transaction_type":"toll",` +
		`"transaction_time_utc":"2026-08-14T13:45:02Z","base_amount":"12.50",` +
		`"plate":{"number":"A","jurisdiction":"TX"},"metadata":{"x":` +
		strings.Repeat("[", depth) + strings.Repeat("]", depth) + `}}`

	require.NotPanics(t, func() {
		rec := postJSON(t, h, raw)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	// Still serving afterwards.
	assert.Equal(t, http.StatusOK, get(t, h, "/healthz").Code)
}
