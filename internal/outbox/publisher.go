package outbox

import (
	"context"
	"log/slog"
)

// LogPublisher writes events to the logger instead of a message broker.
//
// It is the default so the service runs end to end with no queue
// infrastructure, which keeps the quickstart honest. It is also what the
// integration tests use: the relay's retry, backoff, and ordering behaviour is
// what those tests are about, and a real broker would only add flakiness to
// them.
//
// It is not a production publisher. Nothing downstream consumes a log line.
type LogPublisher struct {
	logger *slog.Logger
}

// NewLogPublisher returns a Publisher that logs each event at Info.
func NewLogPublisher(logger *slog.Logger) *LogPublisher {
	return &LogPublisher{logger: logger}
}

// Name identifies the publisher in startup output.
func (p *LogPublisher) Name() string { return "log" }

// Publish records the event.
//
// The payload is deliberately not logged: it carries plate and transponder
// values, which are PII and must not reach log aggregation. The identifiers
// logged here are our own.
func (p *LogPublisher) Publish(ctx context.Context, e Event) error {
	p.logger.LogAttrs(ctx, slog.LevelInfo, "outbox event published",
		slog.String("event_id", e.ID.String()),
		slog.String("event_type", e.Type),
		slog.String("aggregate_id", e.AggregateID.String()),
		slog.Int("payload_bytes", len(e.Payload)),
	)
	return nil
}
