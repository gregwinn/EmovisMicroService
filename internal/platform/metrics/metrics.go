// Package metrics defines what this service reports about itself.
//
// The metrics here are chosen to answer questions an operator actually asks at
// three in the morning, not to instrument everything that could be counted:
//
//   - Is a producer sending us rubbish, and which producer? — ingest results
//     and validation failures, broken down by reason.
//   - Are we double-billing, or is someone trying to? — duplicate and divergent
//     replay counts.
//   - Is the resolution pipeline hearing about transactions? — outbox backlog
//     and the age of the oldest unpublished event.
//
// That last one is the SLI for ADR-0007. The outbox makes lost events
// impossible; it converts them into *late* events, and this is how you find out
// they are late.
package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Label values for the ingest outcome, kept as constants so a typo cannot
// silently create a new time series.
const (
	ResultCreated   = "created"
	ResultDuplicate = "duplicate"
	ResultRejected  = "rejected"
	ResultError     = "error"
)

// Metrics is the service's instrumentation.
//
// It is passed explicitly rather than registered globally: package-level
// metrics make tests order-dependent and force a global registry on anything
// that imports them.
type Metrics struct {
	registry *prometheus.Registry

	ingestTotal       *prometheus.CounterVec
	ingestDuration    *prometheus.HistogramVec
	validationFailure *prometheus.CounterVec
	divergentReplay   *prometheus.CounterVec

	outboxPending   prometheus.Gauge
	outboxOldestAge prometheus.Gauge
	outboxPublished *prometheus.CounterVec

	httpRequests *prometheus.CounterVec
}

// New builds the metric set and its registry.
func New() *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		registry: registry,

		ingestTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ingest_transactions_total",
			Help: "Transaction pushes by producing system and outcome.",
		}, []string{"source", "result"}),

		ingestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ingest_duration_seconds",
			Help: "End-to-end handling time for a transaction push.",
			// Tuned for a write path that does one database round trip: most
			// requests land in single-digit milliseconds, and the buckets need
			// resolution there rather than at the second mark.
			Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		}, []string{"result"}),

		// The metric an operator actually wants. "Producer X started failing"
		// becomes a dashboard rather than a support ticket, and the reason
		// label says whether it is their bug or our configuration.
		validationFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ingest_validation_failures_total",
			Help: "Rejected fields by producing system, validation layer, and field.",
		}, []string{"source", "layer", "field"}),

		// Worth alerting on. A non-zero rate means a producer is sending
		// different content under an idempotency key it has already used —
		// either their defect or an attempt to change an immutable amount.
		divergentReplay: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ingest_divergent_duplicates_total",
			Help: "Replays whose idempotency key matched but whose content differed.",
		}, []string{"source"}),

		outboxPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "outbox_pending_events",
			Help: "Events written but not yet published downstream.",
		}),

		outboxOldestAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "outbox_oldest_pending_age_seconds",
			Help: "Age of the oldest unpublished event. The SLI for outbox lag.",
		}),

		outboxPublished: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbox_publish_total",
			Help: "Publish attempts by outcome.",
		}, []string{"result"}),

		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests by method, route, and status.",
		}, []string{"method", "route", "status"}),
	}

	registry.MustRegister(
		m.ingestTotal,
		m.ingestDuration,
		m.validationFailure,
		m.divergentReplay,
		m.outboxPending,
		m.outboxOldestAge,
		m.outboxPublished,
		m.httpRequests,
		// Go runtime and process metrics: cheap, and the first thing anyone
		// looks at when the service is behaving strangely.
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return m
}

// Registry exposes the collector set for the /metrics handler.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// IngestResult records the outcome of a transaction push.
func (m *Metrics) IngestResult(source, result string, took time.Duration) {
	m.ingestTotal.WithLabelValues(source, result).Inc()
	m.ingestDuration.WithLabelValues(result).Observe(took.Seconds())
}

// ValidationFailure records one rejected field.
//
// The field name is a label; the field *value* never is. Plate numbers are PII
// and would also blow the cardinality budget — a label whose values are
// unbounded turns one metric into millions of time series.
func (m *Metrics) ValidationFailure(source, layer, field string) {
	if field == "" {
		field = "request"
	}
	m.validationFailure.WithLabelValues(source, layer, field).Inc()
}

// DivergentReplay records a duplicate whose content differed from the stored
// transaction.
func (m *Metrics) DivergentReplay(source string) {
	m.divergentReplay.WithLabelValues(source).Inc()
}

// OutboxBacklog records the current unpublished depth and the age of the oldest
// waiting event.
func (m *Metrics) OutboxBacklog(pending int, oldest time.Duration) {
	m.outboxPending.Set(float64(pending))
	m.outboxOldestAge.Set(oldest.Seconds())
}

// OutboxPublish records the outcome of a publish attempt.
func (m *Metrics) OutboxPublish(result string) {
	m.outboxPublished.WithLabelValues(result).Inc()
}

// HTTPRequest records a served request.
//
// The route is the registered pattern, never the raw path: a label taken from
// user input is an unbounded-cardinality hazard, and here it would also be an
// open invitation to fill the metrics store with junk.
func (m *Metrics) HTTPRequest(method, route string, status int) {
	m.httpRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
}
