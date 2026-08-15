package memory_test

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gregwinn/EmovisMicroService/internal/money"
	"github.com/gregwinn/EmovisMicroService/internal/store/memory"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

var fixedNow = time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC)

// build produces an accepted transaction through the real rules, so the store
// is exercised against the values production would hand it.
func build(t *testing.T, mutate func(*transaction.Submission)) transaction.Transaction {
	t.Helper()

	usd, ok := money.Lookup("USD")
	require.True(t, ok)

	s := transaction.Submission{
		Source:          "lane-controller-07",
		SourceReference: "LC07-20260814-000918",
		Type:            "toll",
		OccurredAt:      fixedNow.Add(-15 * time.Minute),
		Plate:           &transaction.PlateRead{Number: "ABC1234", Jurisdiction: "TX"},
		BaseAmount:      "12.50",
	}
	if mutate != nil {
		mutate(&s)
	}

	rules := transaction.Rules{
		Types:           transaction.NewTypeSet([]string{"toll", "violation"}),
		DefaultCurrency: usd,
		Now:             func() time.Time { return fixedNow },
	}

	tx, problems := rules.Accept(s)
	require.Empty(t, problems)
	return tx
}

func TestIngestStoresANewTransaction(t *testing.T) {
	store := memory.New()
	tx := build(t, nil)

	outcome, err := store.Ingest(t.Context(), tx)

	require.NoError(t, err)
	assert.False(t, outcome.Duplicate)
	assert.False(t, outcome.Divergent)
	assert.Equal(t, tx.ID, outcome.Transaction.ID)
	assert.Equal(t, 1, store.Len())
}

// A retry over an unreliable link must never create a second billable record.
func TestIngestIsIdempotentOnTheProducerKey(t *testing.T) {
	store := memory.New()

	first, err := store.Ingest(t.Context(), build(t, nil))
	require.NoError(t, err)

	// A retry mints a new id, exactly as a real second request would.
	retry := build(t, nil)
	require.NotEqual(t, first.Transaction.ID, retry.ID, "precondition: the retry has its own id")

	second, err := store.Ingest(t.Context(), retry)
	require.NoError(t, err)

	assert.True(t, second.Duplicate)
	assert.False(t, second.Divergent)
	assert.Equal(t, first.Transaction.ID, second.Transaction.ID,
		"the original record is returned, not the retry")
	assert.Equal(t, 1, store.Len(), "no second billable record")
}

// source_reference is unique *within* a source. Two agencies both numbering
// from 1 must not collide.
func TestKeyIsScopedToTheSource(t *testing.T) {
	store := memory.New()

	_, err := store.Ingest(t.Context(), build(t, func(s *transaction.Submission) {
		s.Source = "agency-a"
		s.SourceReference = "1"
	}))
	require.NoError(t, err)

	outcome, err := store.Ingest(t.Context(), build(t, func(s *transaction.Submission) {
		s.Source = "agency-b"
		s.SourceReference = "1"
	}))
	require.NoError(t, err)

	assert.False(t, outcome.Duplicate, "same reference, different producer, different transaction")
	assert.Equal(t, 2, store.Len())
}

// The contract has no 409, so a divergent replay still answers 200 with the
// stored record. It is flagged rather than rejected, and never silently
// discarded. See ADR-0006.
func TestDivergentReplayIsFlaggedButStillADuplicate(t *testing.T) {
	store := memory.New()

	original, err := store.Ingest(t.Context(), build(t, nil))
	require.NoError(t, err)

	// Same idempotency key, different amount.
	outcome, err := store.Ingest(t.Context(), build(t, func(s *transaction.Submission) {
		s.BaseAmount = "99.00"
	}))
	require.NoError(t, err)

	assert.True(t, outcome.Duplicate, "the contract promises a duplicate response for any key match")
	assert.True(t, outcome.Divergent, "but the difference must not go unrecorded")
	assert.Equal(t, original.Transaction.ID, outcome.Transaction.ID)
	assert.Equal(t, "12.50", outcome.Transaction.BaseAmount.AsReceived(),
		"base_amount is immutable once accepted")
	assert.Equal(t, 1, store.Len())
}

func TestDivergenceDetectsAnyBillableChange(t *testing.T) {
	tests := map[string]func(*transaction.Submission){
		"amount": func(s *transaction.Submission) { s.BaseAmount = "99.00" },
		"plate": func(s *transaction.Submission) {
			s.Plate = &transaction.PlateRead{Number: "XYZ9876", Jurisdiction: "TX"}
		},
		"type":        func(s *transaction.Submission) { s.Type = "violation" },
		"occurrence":  func(s *transaction.Submission) { s.OccurredAt = fixedNow.Add(-time.Hour) },
		"transponder": func(s *transaction.Submission) { s.Transponder = "0180012345678" },
		"currency":    func(s *transaction.Submission) { s.Currency = "GBP" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			store := memory.New()

			_, err := store.Ingest(t.Context(), build(t, nil))
			require.NoError(t, err)

			outcome, err := store.Ingest(t.Context(), build(t, mutate))
			require.NoError(t, err)

			assert.True(t, outcome.Duplicate)
			assert.True(t, outcome.Divergent, "a changed %s should register as divergent", name)
		})
	}
}

// A producer that re-serialises the same transaction is not diverging. False
// positives here would make the divergence metric unactionable.
func TestCosmeticDifferencesAreNotDivergent(t *testing.T) {
	tests := map[string]func(*transaction.Submission){
		"amount formatting":   func(s *transaction.Submission) { s.BaseAmount = "12.5" },
		"explicit currency":   func(s *transaction.Submission) { s.Currency = "USD" },
		"type capitalisation": func(s *transaction.Submission) { s.Type = "TOLL" },
		"timestamp offset": func(s *transaction.Submission) {
			s.OccurredAt = s.OccurredAt.In(time.FixedZone("CDT", -5*3600))
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			store := memory.New()

			_, err := store.Ingest(t.Context(), build(t, nil))
			require.NoError(t, err)

			outcome, err := store.Ingest(t.Context(), build(t, mutate))
			require.NoError(t, err)

			assert.True(t, outcome.Duplicate)
			assert.False(t, outcome.Divergent, "%s is not a change to the transaction", name)
		})
	}
}

// The case the whole design exists for: a producer times out and retries while
// the original request is still in flight. A read-then-write would let both
// win and bill the customer twice.
func TestConcurrentSubmissionsOfTheSameKeyProduceOneRecord(t *testing.T) {
	const attempts = 50

	store := memory.New()

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		created   int
		duplicate int
		ids       = map[uuid.UUID]struct{}{}
	)

	for range attempts {
		wg.Go(func() {
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
	wg.Wait()

	assert.Equal(t, 1, created, "exactly one caller may create the record")
	assert.Equal(t, attempts-1, duplicate)
	assert.Len(t, ids, 1, "every caller must be told about the same transaction")
	assert.Equal(t, 1, store.Len())
}

func TestConcurrentDistinctKeysAllPersist(t *testing.T) {
	const count = 100

	store := memory.New()

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

	assert.Equal(t, count, store.Len())
}

func TestGet(t *testing.T) {
	store := memory.New()
	tx := build(t, nil)

	_, err := store.Ingest(t.Context(), tx)
	require.NoError(t, err)

	found, err := store.Get(transaction.KeyOf(tx))
	require.NoError(t, err)
	assert.Equal(t, tx.ID, found.ID)

	_, err = store.Get(transaction.Key{Source: "nobody", SourceReference: "nothing"})
	assert.ErrorIs(t, err, transaction.ErrNotFound)
}

func TestAllIsOrderedByOccurrence(t *testing.T) {
	store := memory.New()

	for i, offset := range []time.Duration{-time.Hour, -3 * time.Hour, -2 * time.Hour} {
		_, err := store.Ingest(t.Context(), build(t, func(s *transaction.Submission) {
			s.SourceReference = "ref-" + strconv.Itoa(i)
			s.OccurredAt = fixedNow.Add(offset)
		}))
		require.NoError(t, err)
	}

	all := store.All()
	require.Len(t, all, 3)
	for i := 1; i < len(all); i++ {
		assert.False(t, all[i].OccurredAt.Before(all[i-1].OccurredAt), "expected ascending order")
	}
}

func TestEmptyStore(t *testing.T) {
	store := memory.New()

	assert.Zero(t, store.Len())
	assert.Empty(t, store.All())
}
