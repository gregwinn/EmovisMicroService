package metrics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gregwinn/EmovisMicroService/internal/platform/metrics"
)

func TestIngestResult(t *testing.T) {
	m := metrics.New()

	m.IngestResult("lane-controller-07", metrics.ResultCreated, 5*time.Millisecond)
	m.IngestResult("lane-controller-07", metrics.ResultCreated, 7*time.Millisecond)
	m.IngestResult("lane-controller-07", metrics.ResultDuplicate, 2*time.Millisecond)
	m.IngestResult("image-review", metrics.ResultRejected, time.Millisecond)

	exported := gather(t, m)

	assert.Contains(t, exported,
		`ingest_transactions_total{result="created",source="lane-controller-07"} 2`)
	assert.Contains(t, exported,
		`ingest_transactions_total{result="duplicate",source="lane-controller-07"} 1`)
	assert.Contains(t, exported,
		`ingest_transactions_total{result="rejected",source="image-review"} 1`)

	// The histogram must have observed every request, not just the created ones.
	assert.Contains(t, exported, `ingest_duration_seconds_count{result="created"} 2`)
}

// The metric an operator actually wants: which producer is breaking which rule.
func TestValidationFailureIsBrokenDownByReason(t *testing.T) {
	m := metrics.New()

	m.ValidationFailure("lane-controller-07", "semantic", "base_amount")
	m.ValidationFailure("lane-controller-07", "semantic", "base_amount")
	m.ValidationFailure("lane-controller-07", "semantic", "transaction_type")
	m.ValidationFailure("image-review", "contract", "plate.jurisdiction")

	exported := gather(t, m)

	assert.Contains(t, exported, `ingest_validation_failures_total{field="base_amount",layer="semantic",source="lane-controller-07"} 2`)
	assert.Contains(t, exported, `ingest_validation_failures_total{field="transaction_type",layer="semantic",source="lane-controller-07"} 1`)
	assert.Contains(t, exported, `field="plate.jurisdiction"`)
}

// A rule that spans fields reports no field name. It still needs a label value,
// or the series would be dropped.
func TestValidationFailureWithoutAFieldIsLabelled(t *testing.T) {
	m := metrics.New()

	m.ValidationFailure("lane-controller-07", "semantic", "")

	assert.Contains(t, gather(t, m), `field="request"`)
}

func TestDivergentReplay(t *testing.T) {
	m := metrics.New()

	m.DivergentReplay("lane-controller-07")
	m.DivergentReplay("lane-controller-07")

	assert.Contains(t, gather(t, m),
		`ingest_divergent_duplicates_total{source="lane-controller-07"} 2`)
}

// Outbox depth and lag are the SLI for ADR-0007: the outbox converts lost
// events into late ones, and this is how you find out they are late.
func TestOutboxBacklog(t *testing.T) {
	m := metrics.New()

	m.OutboxBacklog(12, 90*time.Second)

	exported := gather(t, m)
	assert.Contains(t, exported, "outbox_pending_events 12")
	assert.Contains(t, exported, "outbox_oldest_pending_age_seconds 90")

	// A gauge, not a counter: a drained backlog must be able to go back down.
	m.OutboxBacklog(0, 0)

	exported = gather(t, m)
	assert.Contains(t, exported, "outbox_pending_events 0")
	assert.Contains(t, exported, "outbox_oldest_pending_age_seconds 0")
}

func TestOutboxPublish(t *testing.T) {
	m := metrics.New()

	m.OutboxPublish("published")
	m.OutboxPublish("published")
	m.OutboxPublish("failed")
	m.OutboxPublish("parked")

	exported := gather(t, m)
	assert.Contains(t, exported, `outbox_publish_total{result="published"} 2`)
	assert.Contains(t, exported, `outbox_publish_total{result="failed"} 1`)
	assert.Contains(t, exported, `outbox_publish_total{result="parked"} 1`)
}

func TestHTTPRequest(t *testing.T) {
	m := metrics.New()

	m.HTTPRequest("POST", "POST /ingest/v1/transactions", 201)
	m.HTTPRequest("POST", "POST /ingest/v1/transactions", 400)

	exported := gather(t, m)
	assert.Contains(t, exported, `status="201"`)
	assert.Contains(t, exported, `status="400"`)
}

// Runtime and process collectors are the first thing anyone looks at when the
// service is behaving strangely.
func TestRuntimeCollectorsAreRegistered(t *testing.T) {
	exported := gather(t, metrics.New())

	assert.Contains(t, exported, "go_goroutines")
	assert.Contains(t, exported, "go_memstats_alloc_bytes")
}

// Each Metrics owns its registry rather than using the global default, so tests
// are independent and nothing imposes a global registry on its importers.
func TestRegistriesAreIndependent(t *testing.T) {
	first, second := metrics.New(), metrics.New()

	first.DivergentReplay("lane-controller-07")

	assert.Contains(t, gather(t, first), "ingest_divergent_duplicates_total")
	assert.NotContains(t, gather(t, second), `ingest_divergent_duplicates_total{source=`)
}

func gather(t *testing.T, m *metrics.Metrics) string {
	t.Helper()

	families, err := m.Registry().Gather()
	require.NoError(t, err)

	var b strings.Builder
	enc := expfmt.NewEncoder(&b, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, f := range families {
		require.NoError(t, enc.Encode(f))
	}
	return b.String()
}
