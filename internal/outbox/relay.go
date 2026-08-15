package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Relay drains the outbox and publishes events downstream.
//
// It runs as its own process so that publishing pressure — a slow broker, a
// retry storm — cannot slow down transaction ingest. The ingest path's only job
// is to commit; getting the event out is this component's problem.
type Relay struct {
	pool      *pgxpool.Pool
	publisher Publisher
	logger    *slog.Logger
	opts      RelayOptions
	metrics   Recorder
}

// Recorder is the slice of instrumentation the relay reports.
//
// An interface rather than the concrete metrics type so that the outbox does
// not depend on Prometheus and tests can pass a no-op.
type Recorder interface {
	OutboxPublish(result string)
	OutboxBacklog(pending int, oldest time.Duration)
}

// noopRecorder is used when no Recorder is supplied.
type noopRecorder struct{}

func (noopRecorder) OutboxPublish(string)             {}
func (noopRecorder) OutboxBacklog(int, time.Duration) {}

// RelayOptions tunes the relay's polling and retry behaviour.
type RelayOptions struct {
	// BatchSize caps how many events one pass claims. Bounded so a large
	// backlog is drained in steady chunks rather than one enormous transaction
	// that holds locks and memory.
	BatchSize int
	// PollInterval is how long to wait after an empty pass. After a non-empty
	// pass the relay immediately tries again, so a backlog drains at full speed
	// and an idle relay stays quiet.
	PollInterval time.Duration
	// BaseBackoff is the first retry delay; it doubles per attempt.
	BaseBackoff time.Duration
	// MaxBackoff caps the exponential growth.
	MaxBackoff time.Duration
	// MaxAttempts is how many times an event is retried before it is parked.
	MaxAttempts int
}

// DefaultRelayOptions are tuned for a service that must not lose events and is
// not latency-critical: the resolution pipeline is explicitly asynchronous.
func DefaultRelayOptions() RelayOptions {
	return RelayOptions{
		BatchSize:    100,
		PollInterval: 2 * time.Second,
		BaseBackoff:  time.Second,
		MaxBackoff:   5 * time.Minute,
		MaxAttempts:  10,
	}
}

// NewRelay constructs a relay. Zero-valued options fall back to the defaults.
func NewRelay(pool *pgxpool.Pool, publisher Publisher, logger *slog.Logger, opts RelayOptions) *Relay {
	defaults := DefaultRelayOptions()
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaults.BatchSize
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaults.PollInterval
	}
	if opts.BaseBackoff <= 0 {
		opts.BaseBackoff = defaults.BaseBackoff
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = defaults.MaxBackoff
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaults.MaxAttempts
	}

	return &Relay{pool: pool, publisher: publisher, logger: logger, opts: opts, metrics: noopRecorder{}}
}

// WithMetrics attaches a recorder. Returns the relay so it can be chained onto
// NewRelay at a call site.
func (r *Relay) WithMetrics(recorder Recorder) *Relay {
	if recorder != nil {
		r.metrics = recorder
	}
	return r
}

// Run drains the outbox until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) error {
	r.logger.Info("outbox relay started",
		slog.String("publisher", r.publisher.Name()),
		slog.Int("batch_size", r.opts.BatchSize),
		slog.Duration("poll_interval", r.opts.PollInterval))

	for {
		r.reportBacklog(ctx)

		published, err := r.RunOnce(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			r.logger.Info("outbox relay stopped")
			return nil
		case err != nil:
			// A failed pass is usually the database being briefly unavailable.
			// Log and keep going: the events are durable, so waiting costs
			// latency rather than data.
			r.logger.Error("outbox pass failed", slog.Any("error", err))
		}

		// A non-empty pass means there may be more waiting, so try again at
		// once. Only an empty pass sleeps.
		if published > 0 && err == nil {
			continue
		}

		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopped")
			return nil
		case <-time.After(r.opts.PollInterval):
		}
	}
}

// claimPending takes the oldest due, unpublished events.
//
// FOR UPDATE SKIP LOCKED is what allows more than one relay to run at a time:
// each claims a disjoint set instead of queueing behind the same rows. Without
// SKIP LOCKED a second replica would block on the first's batch and add no
// throughput; without FOR UPDATE both would publish the same events.
const claimPending = `
SELECT event_id, event_type, aggregate_id, payload, created_at, attempts
FROM outbox_events
WHERE published_at IS NULL
  AND next_attempt_at <= now()
ORDER BY id
LIMIT $1
FOR UPDATE SKIP LOCKED`

const markPublished = `
UPDATE outbox_events
SET published_at = now(), attempts = attempts + 1, last_error = NULL
WHERE event_id = $1`

const markFailed = `
UPDATE outbox_events
SET attempts = attempts + 1, last_error = $2, next_attempt_at = now() + $3::interval
WHERE event_id = $1`

// RunOnce performs a single drain pass and reports how many events it
// published. Exported so tests can drive the relay deterministically rather
// than racing its loop.
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	dbTx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin outbox transaction: %w", err)
	}
	defer func() { _ = dbTx.Rollback(ctx) }()

	claimed, err := r.claim(ctx, dbTx)
	if err != nil {
		return 0, err
	}
	if len(claimed) == 0 {
		return 0, nil
	}

	published := 0
	for _, c := range claimed {
		if err := r.publishOne(ctx, dbTx, c); err != nil {
			// Publishing failures are recorded against the row, not returned:
			// one broken event must not stop the rest of the batch.
			continue
		}
		published++
	}

	if err := dbTx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit outbox transaction: %w", err)
	}

	return published, nil
}

// claimed is an event plus the retry bookkeeping the relay needs.
type claimed struct {
	event    Event
	attempts int
}

func (r *Relay) claim(ctx context.Context, dbTx pgx.Tx) ([]claimed, error) {
	rows, err := dbTx.Query(ctx, claimPending, r.opts.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("claim pending events: %w", err)
	}
	defer rows.Close()

	var out []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(
			&c.event.ID, &c.event.Type, &c.event.AggregateID,
			&c.event.Payload, &c.event.CreatedAt, &c.attempts,
		); err != nil {
			return nil, fmt.Errorf("scan pending event: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read pending events: %w", err)
	}

	return out, nil
}

func (r *Relay) publishOne(ctx context.Context, dbTx pgx.Tx, c claimed) error {
	if err := r.publisher.Publish(ctx, c.event); err != nil {
		return r.recordFailure(ctx, dbTx, c, err)
	}

	if _, err := dbTx.Exec(ctx, markPublished, c.event.ID); err != nil {
		return fmt.Errorf("mark event published: %w", err)
	}

	r.metrics.OutboxPublish("published")

	// The window that makes delivery at-least-once rather than exactly-once:
	// the publish above has already happened, and this transaction may still
	// fail to commit. Redelivery is the safe direction, and consumers
	// deduplicate on event_id.
	return nil
}

func (r *Relay) recordFailure(ctx context.Context, dbTx pgx.Tx, c claimed, cause error) error {
	attempts := c.attempts + 1
	backoff := r.backoffFor(attempts)

	level := slog.LevelWarn
	if attempts >= r.opts.MaxAttempts {
		// Parked, not dropped. The row stays with its payload and last error so
		// an operator can inspect and requeue it. Deleting an event nobody
		// managed to deliver would be losing the thing this whole design exists
		// to protect.
		backoff = parkedBackoff
		level = slog.LevelError
	}

	r.logger.LogAttrs(ctx, level, "outbox publish failed",
		slog.String("event_id", c.event.ID.String()),
		slog.String("event_type", c.event.Type),
		slog.Int("attempts", attempts),
		slog.Duration("retry_in", backoff),
		slog.Bool("parked", attempts >= r.opts.MaxAttempts),
		slog.Any("error", cause))

	result := "failed"
	if attempts >= r.opts.MaxAttempts {
		result = "parked"
	}
	r.metrics.OutboxPublish(result)

	if _, err := dbTx.Exec(ctx, markFailed, c.event.ID, cause.Error(), backoff.String()); err != nil {
		return fmt.Errorf("record publish failure: %w", err)
	}

	return cause
}

// parkedBackoff is far enough out that a poison event stops consuming relay
// capacity, while remaining visible and requeueable.
const parkedBackoff = 24 * time.Hour

// backoffFor doubles the delay per attempt, capped.
func (r *Relay) backoffFor(attempts int) time.Duration {
	if attempts <= 1 {
		return r.opts.BaseBackoff
	}

	// Shift-based doubling overflows quickly; float math with a cap does not.
	multiplier := math.Pow(2, float64(attempts-1))
	backoff := time.Duration(float64(r.opts.BaseBackoff) * multiplier)

	if backoff <= 0 || backoff > r.opts.MaxBackoff {
		return r.opts.MaxBackoff
	}
	return backoff
}

// reportBacklog refreshes the depth and lag gauges. Failures are ignored: a
// missing metric sample must never stop the relay from doing its actual job.
func (r *Relay) reportBacklog(ctx context.Context) {
	pending, err := r.PendingCount(ctx)
	if err != nil {
		return
	}

	oldest, _, err := r.OldestPending(ctx)
	if err != nil {
		return
	}

	r.metrics.OutboxBacklog(pending, oldest)
}

// PendingCount reports how many events are waiting to be published. It backs
// the outbox-lag metric, which is the SLI for "is the resolution pipeline
// hearing about transactions?".
func (r *Relay) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count pending events: %w", err)
	}
	return n, nil
}

// OldestPending reports the age of the oldest unpublished event, and whether
// there is one. A growing value means the relay is falling behind.
func (r *Relay) OldestPending(ctx context.Context) (time.Duration, bool, error) {
	var oldest *time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT min(created_at) FROM outbox_events WHERE published_at IS NULL`).Scan(&oldest)
	if err != nil {
		return 0, false, fmt.Errorf("read oldest pending event: %w", err)
	}
	if oldest == nil {
		return 0, false, nil
	}
	return time.Since(*oldest), true, nil
}
