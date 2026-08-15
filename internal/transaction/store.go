package transaction

import (
	"context"
	"errors"
)

// Store durably records accepted transactions.
//
// The interface is declared here, next to its consumer, rather than beside an
// implementation. There are two implementations — Postgres in production and an
// in-memory one for tests and local runs — and neither gets to define what the
// domain needs.
type Store interface {
	// Ingest records tx if its idempotency key is new, and otherwise returns
	// the transaction already stored under that key.
	//
	// Implementations must make the check-and-insert atomic. Two concurrent
	// pushes of the same (source, source_reference) are not a hypothetical:
	// producers retry on timeout, and the retry frequently overlaps the request
	// that was merely slow rather than lost. A read-then-write would let both
	// win and bill the customer twice.
	Ingest(ctx context.Context, tx Transaction) (IngestOutcome, error)
}

// IngestOutcome reports what a call to Store.Ingest did.
type IngestOutcome struct {
	// Transaction is the stored record: the new one when Duplicate is false,
	// the pre-existing one when it is true.
	Transaction Transaction

	// Duplicate is true when the idempotency key already existed and nothing
	// new was created. It maps directly onto the contract's `duplicate` field
	// and to 200 rather than 201.
	Duplicate bool

	// Divergent is true when the key already existed *and* the incoming
	// transaction's fingerprint differs from the stored one.
	//
	// The response is unaffected: the contract promises 200 with the existing
	// record for any key match, and a producer coded against it has no branch
	// for anything else. But a differing payload under the same idempotency key
	// is either a producer defect or an attempt to change an amount the
	// contract declares immutable. Both are revenue-integrity events, and
	// answering 200 while silently discarding the difference is the one
	// response that would be indefensible.
	//
	// Callers surface this as a metric and a warning, never as a client error.
	// See docs/adr/0006-idempotency-divergence.md.
	Divergent bool
}

// ErrNotFound is returned by lookups when no transaction matches.
var ErrNotFound = errors.New("transaction not found")

// Key is the idempotency key: the producer's own identifier for a transaction,
// scoped to the producer.
//
// The contract is explicit that source_reference is "unique within a given
// source", so the pair is the key and source alone is not. Two agencies both
// numbering their transactions from 1 must not collide.
type Key struct {
	Source          string
	SourceReference string
}

// KeyOf returns the idempotency key for a transaction.
func KeyOf(tx Transaction) Key {
	return Key{Source: tx.Source, SourceReference: tx.SourceReference}
}
