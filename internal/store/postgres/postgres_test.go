package postgres_test

import (
	"context"
	"flag"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gregwinn/EmovisMicroService/internal/money"
	"github.com/gregwinn/EmovisMicroService/internal/store/postgres"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

// These tests run against a real PostgreSQL instance, because the behaviour
// under test *is* database behaviour. The idempotency guarantee lives in a
// unique constraint and ON CONFLICT semantics; a mock would only assert that
// the code calls the code.
//
// One container is shared across the package and each test truncates, which
// keeps the suite to a few seconds. `make test-short` skips them entirely so
// the fast path needs no Docker.

var (
	testPool *pgxpool.Pool
	testURL  string
)

func TestMain(m *testing.M) {
	// testing.Short() reads a flag, and TestMain runs before the testing
	// package parses them. Without this the call panics.
	flag.Parse()

	if testing.Short() {
		// Skip the container entirely: `make test-short` must work with no
		// Docker. Individual tests skip themselves too, via newStore.
		os.Exit(m.Run())
	}

	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("ingest_test"),
		tcpostgres.WithUsername("ingest"),
		tcpostgres.WithPassword("ingest"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		panic("start postgres container: " + err.Error())
	}

	testURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic("container connection string: " + err.Error())
	}

	testPool, err = postgres.Connect(ctx, testURL, 20)
	if err != nil {
		panic("connect to test database: " + err.Error())
	}

	// The tests run against exactly the schema production gets.
	if err := postgres.MigrateWithPool(ctx, testPool); err != nil {
		panic("apply migrations: " + err.Error())
	}

	code := m.Run()

	testPool.Close()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

// newStore returns a store over an empty transactions table.
func newStore(t *testing.T) *postgres.Store {
	t.Helper()

	if testing.Short() {
		t.Skip("integration test: requires Docker")
	}

	_, err := testPool.Exec(t.Context(), "TRUNCATE transactions")
	require.NoError(t, err)

	return postgres.New(testPool)
}

var occurredAt = time.Date(2026, 8, 14, 13, 45, 2, 0, time.UTC)

func build(t *testing.T, mutate func(*transaction.Submission)) transaction.Transaction {
	t.Helper()

	usd, ok := money.Lookup("USD")
	require.True(t, ok)

	s := transaction.Submission{
		Source:          "lane-controller-07",
		SourceReference: "LC07-20260814-000918",
		Type:            "toll",
		OccurredAt:      occurredAt,
		Plate:           &transaction.PlateRead{Number: "ABC-1234", Jurisdiction: "tx"},
		BaseAmount:      "12.50",
	}
	if mutate != nil {
		mutate(&s)
	}

	rules := transaction.Rules{
		Types:           transaction.NewTypeSet([]string{"toll", "violation"}),
		DefaultCurrency: usd,
		Now:             func() time.Time { return occurredAt.Add(time.Minute) },
	}

	tx, problems, err := rules.Accept(s)
	require.NoError(t, err)
	require.Empty(t, problems)
	return tx
}

func countRows(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(t.Context(), "SELECT count(*) FROM transactions").Scan(&n))
	return n
}

func TestIngestStoresAndReadsBackFaithfully(t *testing.T) {
	store := newStore(t)

	tx := build(t, func(s *transaction.Submission) {
		s.Transponder = "01800-12345678"
		s.Location = map[string]any{"facility": "SH-130", "plaza": "12", "lane": "3", "direction": "NB"}
		s.Metadata = map[string]any{"vendor": "acme", "confidence": 0.94}
	})

	outcome, err := store.Ingest(t.Context(), tx)
	require.NoError(t, err)
	require.False(t, outcome.Duplicate)

	stored, err := store.Get(t.Context(), transaction.KeyOf(tx))
	require.NoError(t, err)

	assert.Equal(t, tx.ID, stored.ID)
	assert.Equal(t, tx.Source, stored.Source)
	assert.Equal(t, tx.SourceReference, stored.SourceReference)
	assert.Equal(t, "toll", stored.Type)
	assert.True(t, tx.OccurredAt.Equal(stored.OccurredAt))
	assert.Equal(t, transaction.AssociationReceived, stored.AssociationStatus)
	assert.Equal(t, transaction.SettlementPriced, stored.SettlementStatus)

	// Identifiers survive as evidence *and* as matching keys.
	require.NotNil(t, stored.Plate)
	assert.Equal(t, "ABC-1234", stored.Plate.Number, "the raw read is preserved verbatim")
	assert.Equal(t, "ABC1234", stored.Plate.NumberKey)
	assert.Equal(t, "tx", stored.Plate.Jurisdiction)
	assert.Equal(t, "TX", stored.Plate.JurisdictionKey)

	require.NotNil(t, stored.Transponder)
	assert.Equal(t, "01800-12345678", stored.Transponder.Number)
	assert.Equal(t, "0180012345678", stored.Transponder.NumberKey)

	// Money round-trips exactly, including the producer's original text.
	assert.Equal(t, "12.50", stored.BaseAmount.AsReceived())
	assert.Equal(t, "12.50 USD", stored.BaseAmount.String())
	assert.True(t, tx.BaseAmount.Equal(stored.BaseAmount))

	// Free-form producer data is uninterpreted but intact.
	assert.Equal(t, "SH-130", stored.Location["facility"])
	assert.Equal(t, "acme", stored.Metadata["vendor"])
	assert.InEpsilon(t, 0.94, stored.Metadata["confidence"], 0.0001)

	// A round trip must not change the fingerprint, or every replay of a
	// restarted service would look divergent.
	assert.Equal(t, tx.Fingerprint(), stored.Fingerprint())
}

func TestIngestIsIdempotent(t *testing.T) {
	store := newStore(t)

	first, err := store.Ingest(t.Context(), build(t, nil))
	require.NoError(t, err)
	require.False(t, first.Duplicate)

	retry := build(t, nil)
	require.NotEqual(t, first.Transaction.ID, retry.ID)

	second, err := store.Ingest(t.Context(), retry)
	require.NoError(t, err)

	assert.True(t, second.Duplicate)
	assert.False(t, second.Divergent)
	assert.Equal(t, first.Transaction.ID, second.Transaction.ID)
	assert.Equal(t, 1, countRows(t))
}

func TestDivergentReplayIsDetected(t *testing.T) {
	store := newStore(t)

	original, err := store.Ingest(t.Context(), build(t, nil))
	require.NoError(t, err)

	outcome, err := store.Ingest(t.Context(), build(t, func(s *transaction.Submission) {
		s.BaseAmount = "99.00"
	}))
	require.NoError(t, err)

	assert.True(t, outcome.Duplicate)
	assert.True(t, outcome.Divergent)
	assert.Equal(t, original.Transaction.ID, outcome.Transaction.ID)
	assert.Equal(t, "12.50", outcome.Transaction.BaseAmount.AsReceived(),
		"the stored amount is immutable")
	assert.Equal(t, 1, countRows(t))
}

func TestCosmeticReplayIsNotDivergent(t *testing.T) {
	store := newStore(t)

	_, err := store.Ingest(t.Context(), build(t, nil))
	require.NoError(t, err)

	// "12.5" is the same money as "12.50"; the fingerprint hashes the canonical
	// decimal, so a producer tidying its formatting is not a divergence.
	outcome, err := store.Ingest(t.Context(), build(t, func(s *transaction.Submission) {
		s.BaseAmount = "12.5"
	}))
	require.NoError(t, err)

	assert.True(t, outcome.Duplicate)
	assert.False(t, outcome.Divergent)
}

// 🔑 The guarantee the whole design rests on, proven against a real database.
//
// A producer times out and retries while the original request is still in
// flight. The unique constraint plus ON CONFLICT DO NOTHING means exactly one
// writer can win; a read-then-write would let both decide the key was free.
func TestConcurrentIngestOfOneKeyCreatesOneRow(t *testing.T) {
	const attempts = 30

	store := newStore(t)

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		created   int
		duplicate int
		ids       = map[uuid.UUID]struct{}{}
	)

	// Every goroutine waits on the same gate so the writes genuinely overlap
	// rather than trickling in one at a time.
	gate := make(chan struct{})

	for range attempts {
		wg.Go(func() {
			<-gate

			outcome, err := store.Ingest(t.Context(), build(t, nil))
			assert.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()
			if outcome.Duplicate {
				duplicate++
			} else {
				created++
			}
			ids[outcome.Transaction.ID] = struct{}{}
		})
	}

	close(gate)
	wg.Wait()

	assert.Equal(t, 1, created, "exactly one writer may create the billable record")
	assert.Equal(t, attempts-1, duplicate)
	assert.Len(t, ids, 1, "every caller must be told about the same transaction")
	assert.Equal(t, 1, countRows(t), "no second billable record")
}

func TestConcurrentDistinctKeysAllPersist(t *testing.T) {
	const count = 50

	store := newStore(t)

	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			_, err := store.Ingest(t.Context(), build(t, func(s *transaction.Submission) {
				s.SourceReference = "LC07-" + strconv.Itoa(i)
			}))
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	assert.Equal(t, count, countRows(t))
}

// source_reference is unique within a source, so the key must be the pair.
func TestKeyIsScopedToSource(t *testing.T) {
	store := newStore(t)

	for _, source := range []string{"agency-a", "agency-b"} {
		outcome, err := store.Ingest(t.Context(), build(t, func(s *transaction.Submission) {
			s.Source = source
			s.SourceReference = "1"
		}))
		require.NoError(t, err)
		assert.False(t, outcome.Duplicate)
	}

	assert.Equal(t, 2, countRows(t))
}

func TestGetUnknownKeyReturnsNotFound(t *testing.T) {
	store := newStore(t)

	_, err := store.Get(t.Context(), transaction.Key{Source: "nobody", SourceReference: "nothing"})
	assert.ErrorIs(t, err, transaction.ErrNotFound)
}

// Application validation produces the friendly error; the constraint is the
// guarantee. This asserts the database would refuse the write even if the
// domain rule were bypassed.
func TestIdentifierConstraintIsEnforcedByTheDatabase(t *testing.T) {
	newStore(t)

	_, err := testPool.Exec(t.Context(), `
		INSERT INTO transactions (
			id, source, source_reference, transaction_type,
			occurred_at, received_at,
			base_amount, base_amount_text, currency,
			association_status, settlement_status, fingerprint
		) VALUES (
			$1, 'rogue', 'no-identifier', 'toll',
			now(), now(),
			12.50, '12.50', 'USD',
			'received', 'priced', repeat('a', 64)
		)`, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "transactions_requires_identifier")
}

func TestNegativeAmountConstraintIsEnforcedByTheDatabase(t *testing.T) {
	newStore(t)

	_, err := testPool.Exec(t.Context(), `
		INSERT INTO transactions (
			id, source, source_reference, transaction_type,
			occurred_at, received_at,
			plate_number, plate_number_key,
			base_amount, base_amount_text, currency,
			association_status, settlement_status, fingerprint
		) VALUES (
			$1, 'rogue', 'negative', 'toll',
			now(), now(),
			'ABC1234', 'ABC1234',
			-1.00, '-1.00', 'USD',
			'received', 'priced', repeat('a', 64)
		)`, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "transactions_amount_not_negative")
}

// Migrations must be safe to re-run: a deploy that retries its migration task
// should be a no-op, not a failure.
func TestMigrationsAreIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires Docker")
	}

	require.NoError(t, postgres.MigrateWithPool(t.Context(), testPool))
	require.NoError(t, postgres.MigrateWithPool(t.Context(), testPool))
}

func TestPing(t *testing.T) {
	store := newStore(t)
	assert.NoError(t, store.Ping(t.Context()))
}

// The migrate binary drives these by URL rather than by pool.
func TestMigrateCommandsOverAURL(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires Docker")
	}

	require.NoError(t, postgres.Migrate(t.Context(), testURL))
	require.NoError(t, postgres.Status(t.Context(), testURL))

	// Down then up must leave the schema where it started, or a rollback during
	// an incident would be a one-way door.
	require.NoError(t, postgres.Down(t.Context(), testURL))
	require.NoError(t, postgres.Migrate(t.Context(), testURL))

	_, err := testPool.Exec(t.Context(), "SELECT 1 FROM transactions LIMIT 1")
	assert.NoError(t, err, "the table should exist again after down/up")
}

func TestMigrateRejectsAnUnreachableDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires Docker")
	}

	err := postgres.Migrate(t.Context(), "postgres://nobody:nobody@127.0.0.1:1/nowhere?sslmode=disable&connect_timeout=1")
	assert.Error(t, err)
}

func TestConnectRejectsAMalformedURL(t *testing.T) {
	_, err := postgres.Connect(t.Context(), "://not-a-url", 5)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse database url")
}
