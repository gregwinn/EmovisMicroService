// Package memory holds transactions in process memory.
//
// It exists for two reasons: it lets the service run end to end with no
// infrastructure, which keeps the quickstart in the README honest, and it gives
// the HTTP layer a store to test against without a container.
//
// It is not a production store — nothing survives a restart. The Postgres
// implementation is the real one; this exists so that "does the endpoint behave
// correctly" and "does the database behave correctly" can be tested separately.
package memory

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

// Store is an in-memory transaction.Store.
//
// The zero value is not usable; call New.
type Store struct {
	mu sync.Mutex
	// byKey is keyed by the producer's idempotency key.
	byKey map[transaction.Key]transaction.Transaction
	// fingerprints records what each stored key was originally accepted with,
	// so a divergent replay can be detected without recomputing from the
	// stored transaction on every request.
	fingerprints map[transaction.Key]string
}

// New returns an empty in-memory store.
func New() *Store {
	return &Store{
		byKey:        make(map[transaction.Key]transaction.Transaction),
		fingerprints: make(map[transaction.Key]string),
	}
}

// Ingest records tx unless its idempotency key is already present.
//
// A plain Mutex rather than an RWMutex: the whole operation is a
// check-then-insert that must be atomic, so a read lock could never be taken
// alone. Making the exclusive lock explicit is more honest than an RWMutex that
// is only ever write-locked.
func (s *Store) Ingest(_ context.Context, tx transaction.Transaction) (transaction.IngestOutcome, error) {
	key := transaction.KeyOf(tx)
	fingerprint := tx.Fingerprint()

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.byKey[key]; ok {
		return transaction.IngestOutcome{
			Transaction: existing,
			Duplicate:   true,
			Divergent:   s.fingerprints[key] != fingerprint,
		}, nil
	}

	s.byKey[key] = tx
	s.fingerprints[key] = fingerprint

	return transaction.IngestOutcome{Transaction: tx, Duplicate: false}, nil
}

// Get returns the transaction stored under key. It is a test and debugging
// affordance, not part of transaction.Store.
func (s *Store) Get(key transaction.Key) (transaction.Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, ok := s.byKey[key]
	if !ok {
		return transaction.Transaction{}, transaction.ErrNotFound
	}
	return tx, nil
}

// Len reports how many transactions are stored.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.byKey)
}

// All returns every stored transaction, ordered by when the producer says the
// vehicle passed. Intended for tests and local inspection.
func (s *Store) All() []transaction.Transaction {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := slices.Collect(maps.Values(s.byKey))
	slices.SortFunc(all, func(a, b transaction.Transaction) int {
		return a.OccurredAt.Compare(b.OccurredAt)
	})
	return all
}
