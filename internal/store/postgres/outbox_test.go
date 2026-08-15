package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gregwinn/EmovisMicroService/internal/outbox"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

// These tests are the evidence for ADR-0007. The claim is that an accepted
// transaction and the event announcing it are written atomically, and that the
// relay eventually delivers every event. Both halves are only provable against
// a real database, because both rest on transaction and locking semantics.

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// recordingPublisher captures what it was asked to publish and can be told to
// fail.
type recordingPublisher struct {
	mu        sync.Mutex
	published []outbox.Event
	failWith  error
	failCount int
}

func (p *recordingPublisher) Name() string { return "recording" }

func (p *recordingPublisher) Publish(_ context.Context, e outbox.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.failWith != nil && p.failCount != 0 {
		if p.failCount > 0 {
			p.failCount--
		}
		return p.failWith
	}

	p.published = append(p.published, e)
	return nil
}

func (p *recordingPublisher) events() []outbox.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]outbox.Event(nil), p.published...)
}

func newRelay(t *testing.T, pub outbox.Publisher) *outbox.Relay {
	t.Helper()
	return outbox.NewRelay(testPool, pub, discardLogger(), outbox.RelayOptions{
		BatchSize:   50,
		BaseBackoff: time.Millisecond,
		MaxAttempts: 3,
	})
}

// resetOutbox clears both tables so each test starts from empty.
func resetOutbox(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: requires Docker")
	}
	_, err := testPool.Exec(t.Context(), "TRUNCATE transactions, outbox_events")
	require.NoError(t, err)
}

func pendingCount(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(t.Context(),
		"SELECT count(*) FROM outbox_events WHERE published_at IS NULL").Scan(&n))
	return n
}

func outboxCount(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(t.Context(),
		"SELECT count(*) FROM outbox_events").Scan(&n))
	return n
}

// 🔑 Accepting a transaction queues exactly one event, in the same commit.
func TestAcceptedTransactionQueuesAnEvent(t *testing.T) {
	resetOutbox(t)
	store := newStoreNoTruncate()

	tx := build(t, nil)
	_, err := store.Ingest(t.Context(), tx)
	require.NoError(t, err)

	require.Equal(t, 1, outboxCount(t))

	var (
		eventType   string
		aggregateID uuid.UUID
		payload     []byte
		publishedAt *time.Time
	)
	require.NoError(t, testPool.QueryRow(t.Context(),
		`SELECT event_type, aggregate_id, payload, published_at FROM outbox_events`).
		Scan(&eventType, &aggregateID, &payload, &publishedAt))

	assert.Equal(t, outbox.EventTypeTransactionReceived, eventType)
	assert.Equal(t, tx.ID, aggregateID)
	assert.Nil(t, publishedAt, "the relay has not run yet")

	var body outbox.TransactionReceived
	require.NoError(t, json.Unmarshal(payload, &body))

	assert.Equal(t, tx.ID.String(), body.TransactionID)
	assert.Equal(t, "lane-controller-07", body.Source)
	assert.Equal(t, "toll", body.TransactionType)
	assert.Equal(t, "received", body.AssociationStatus)
	assert.Equal(t, "priced", body.SettlementStatus)

	// Amounts cross the wire as decimal strings for the same reason the inbound
	// contract uses them: JSON numbers are doubles in most consumers.
	assert.Equal(t, "12.50", body.BaseAmount)
	assert.Equal(t, "USD", body.Currency)

	// Resolution cannot attribute a vehicle without the identifiers, so both
	// the raw read and the matching key travel with the event.
	assert.Equal(t, "ABC-1234", body.PlateNumber)
	assert.Equal(t, "ABC1234", body.PlateNumberKey)
}

// A retry is not a second business event. Publishing one would make the
// resolution pipeline process the same transaction twice.
func TestDuplicateDoesNotQueueASecondEvent(t *testing.T) {
	resetOutbox(t)
	store := newStoreNoTruncate()

	_, err := store.Ingest(t.Context(), build(t, nil))
	require.NoError(t, err)

	for range 5 {
		outcome, err := store.Ingest(t.Context(), build(t, nil))
		require.NoError(t, err)
		require.True(t, outcome.Duplicate)
	}

	assert.Equal(t, 1, outboxCount(t), "one transaction, one event")
}

// 🔑 The atomicity claim, tested from the other side: if the event cannot be
// written, the transaction must not exist either. A billable record the
// pipeline will never hear about is the exact failure the outbox prevents.
func TestTransactionAndEventCommitTogether(t *testing.T) {
	resetOutbox(t)
	store := newStoreNoTruncate()

	// Break the outbox insert without touching the transactions table.
	_, err := testPool.Exec(t.Context(),
		`ALTER TABLE outbox_events ADD CONSTRAINT tmp_reject_all CHECK (false) NOT VALID`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS tmp_reject_all`)
	})

	tx := build(t, nil)
	_, err = store.Ingest(t.Context(), tx)

	require.Error(t, err, "ingest must fail when the event cannot be queued")

	var transactions int
	require.NoError(t, testPool.QueryRow(t.Context(),
		"SELECT count(*) FROM transactions").Scan(&transactions))

	assert.Zero(t, transactions,
		"the transaction must roll back with its event: no silent revenue loss")
	assert.Zero(t, outboxCount(t))
}

func TestRelayPublishesPendingEvents(t *testing.T) {
	resetOutbox(t)
	store := newStoreNoTruncate()

	const count = 5
	for i := range count {
		_, err := store.Ingest(t.Context(), build(t, func(s *transaction.Submission) {
			s.SourceReference = "ref-" + strconv.Itoa(i)
		}))
		require.NoError(t, err)
	}
	require.Equal(t, count, pendingCount(t))

	pub := &recordingPublisher{}
	published, err := newRelay(t, pub).RunOnce(t.Context())

	require.NoError(t, err)
	assert.Equal(t, count, published)
	assert.Len(t, pub.events(), count)
	assert.Zero(t, pendingCount(t), "every event should be marked published")
}

// Published rows are kept, not deleted: they are the record of what this
// service told downstream and when.
func TestPublishedEventsAreRetained(t *testing.T) {
	resetOutbox(t)
	store := newStoreNoTruncate()

	_, err := store.Ingest(t.Context(), build(t, nil))
	require.NoError(t, err)

	_, err = newRelay(t, &recordingPublisher{}).RunOnce(t.Context())
	require.NoError(t, err)

	assert.Equal(t, 1, outboxCount(t), "the row is retained as an audit record")
	assert.Zero(t, pendingCount(t))
}

func TestRelayIsANoOpWhenTheOutboxIsEmpty(t *testing.T) {
	resetOutbox(t)

	pub := &recordingPublisher{}
	published, err := newRelay(t, pub).RunOnce(t.Context())

	require.NoError(t, err)
	assert.Zero(t, published)
	assert.Empty(t, pub.events())
}

// A publish failure must not lose the event. It stays pending, records the
// error, and is retried later.
func TestFailedPublishIsRetriedNotLost(t *testing.T) {
	resetOutbox(t)
	store := newStoreNoTruncate()

	_, err := store.Ingest(t.Context(), build(t, nil))
	require.NoError(t, err)

	// Fail the first attempt only.
	pub := &recordingPublisher{failWith: errors.New("broker unreachable"), failCount: 1}
	relay := newRelay(t, pub)

	published, err := relay.RunOnce(t.Context())
	require.NoError(t, err)
	assert.Zero(t, published)
	assert.Equal(t, 1, pendingCount(t), "the event survives a failed publish")

	var (
		attempts  int
		lastError *string
	)
	require.NoError(t, testPool.QueryRow(t.Context(),
		"SELECT attempts, last_error FROM outbox_events").Scan(&attempts, &lastError))
	assert.Equal(t, 1, attempts)
	require.NotNil(t, lastError)
	assert.Contains(t, *lastError, "broker unreachable")

	// The backoff is a millisecond in these options, so the retry is due.
	time.Sleep(10 * time.Millisecond)

	published, err = relay.RunOnce(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, published)
	assert.Zero(t, pendingCount(t))
}

// Backoff means a just-failed event is not retried immediately, or a broker
// outage becomes a hot loop.
func TestFailedEventIsNotRetriedBeforeItsBackoff(t *testing.T) {
	resetOutbox(t)
	store := newStoreNoTruncate()

	_, err := store.Ingest(t.Context(), build(t, nil))
	require.NoError(t, err)

	pub := &recordingPublisher{failWith: errors.New("down"), failCount: -1}
	relay := outbox.NewRelay(testPool, pub, discardLogger(), outbox.RelayOptions{
		BatchSize:   10,
		BaseBackoff: time.Hour,
		MaxAttempts: 5,
	})

	_, err = relay.RunOnce(t.Context())
	require.NoError(t, err)

	// Immediately again: the event is not yet due.
	published, err := relay.RunOnce(t.Context())
	require.NoError(t, err)
	assert.Zero(t, published)

	var attempts int
	require.NoError(t, testPool.QueryRow(t.Context(),
		"SELECT attempts FROM outbox_events").Scan(&attempts))
	assert.Equal(t, 1, attempts, "the second pass should not have touched it")
}

// One poison event must not block the rest of the batch.
func TestOneFailingEventDoesNotBlockTheBatch(t *testing.T) {
	resetOutbox(t)
	store := newStoreNoTruncate()

	for i := range 4 {
		_, err := store.Ingest(t.Context(), build(t, func(s *transaction.Submission) {
			s.SourceReference = "ref-" + strconv.Itoa(i)
		}))
		require.NoError(t, err)
	}

	// Fail only the first publish call of the pass.
	pub := &recordingPublisher{failWith: errors.New("transient"), failCount: 1}

	published, err := newRelay(t, pub).RunOnce(t.Context())

	require.NoError(t, err)
	assert.Equal(t, 3, published, "the other three still go out")
	assert.Equal(t, 1, pendingCount(t), "the failed one remains pending")
}

// An event that keeps failing is parked far in the future rather than deleted:
// losing an undeliverable event is exactly what this design exists to prevent.
func TestExhaustedEventIsParkedNotDropped(t *testing.T) {
	resetOutbox(t)
	store := newStoreNoTruncate()

	_, err := store.Ingest(t.Context(), build(t, nil))
	require.NoError(t, err)

	pub := &recordingPublisher{failWith: errors.New("permanently broken"), failCount: -1}
	relay := outbox.NewRelay(testPool, pub, discardLogger(), outbox.RelayOptions{
		BatchSize:   10,
		BaseBackoff: time.Millisecond,
		MaxAttempts: 3,
	})

	for range 3 {
		_, err := relay.RunOnce(t.Context())
		require.NoError(t, err)
		time.Sleep(5 * time.Millisecond)
	}

	var (
		attempts      int
		nextAttemptAt time.Time
	)
	require.NoError(t, testPool.QueryRow(t.Context(),
		"SELECT attempts, next_attempt_at FROM outbox_events").Scan(&attempts, &nextAttemptAt))

	assert.GreaterOrEqual(t, attempts, 3)
	assert.True(t, nextAttemptAt.After(time.Now().Add(time.Hour)),
		"an exhausted event is parked for inspection, not retried in a hot loop")
	assert.Equal(t, 1, outboxCount(t), "and it is still there")
}

// 🔑 SKIP LOCKED is what lets the relay scale horizontally. Concurrent relays
// must partition the work rather than double-publish it or serialize on it.
func TestConcurrentRelaysDoNotDoublePublish(t *testing.T) {
	resetOutbox(t)
	store := newStoreNoTruncate()

	const events = 40
	for i := range events {
		_, err := store.Ingest(t.Context(), build(t, func(s *transaction.Submission) {
			s.SourceReference = "ref-" + strconv.Itoa(i)
		}))
		require.NoError(t, err)
	}

	publishers := make([]*recordingPublisher, 4)
	var wg sync.WaitGroup

	for i := range publishers {
		pub := &recordingPublisher{}
		publishers[i] = pub

		wg.Go(func() {
			relay := newRelay(t, pub)
			// Several passes each, so the relays genuinely contend.
			for range 5 {
				_, err := relay.RunOnce(t.Context())
				assert.NoError(t, err)
			}
		})
	}
	wg.Wait()

	seen := map[uuid.UUID]int{}
	for _, pub := range publishers {
		for _, e := range pub.events() {
			seen[e.ID]++
		}
	}

	assert.Len(t, seen, events, "every event published exactly once, across all relays")
	for id, count := range seen {
		assert.Equal(t, 1, count, "event %s was published more than once", id)
	}
	assert.Zero(t, pendingCount(t))
}

func TestPendingCountAndOldestPending(t *testing.T) {
	resetOutbox(t)
	store := newStoreNoTruncate()

	relay := newRelay(t, &recordingPublisher{})

	n, err := relay.PendingCount(t.Context())
	require.NoError(t, err)
	assert.Zero(t, n)

	_, found, err := relay.OldestPending(t.Context())
	require.NoError(t, err)
	assert.False(t, found, "nothing pending means no age to report")

	_, err = store.Ingest(t.Context(), build(t, nil))
	require.NoError(t, err)

	n, err = relay.PendingCount(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	age, found, err := relay.OldestPending(t.Context())
	require.NoError(t, err)
	assert.True(t, found)
	assert.Positive(t, age)

	_, err = relay.RunOnce(t.Context())
	require.NoError(t, err)

	_, found, err = relay.OldestPending(t.Context())
	require.NoError(t, err)
	assert.False(t, found, "the backlog is clear")
}
