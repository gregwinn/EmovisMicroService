// Package postgres is the durable implementation of transaction.Store.
//
// Queries are hand-written SQL over pgx. There is no ORM, and that is a
// decision rather than an omission: the whole store is two statements, and the
// interesting one depends on ON CONFLICT semantics that an ORM would hide
// behind an abstraction. When the correctness of billing rests on exactly which
// rows the database locks, that behaviour should be visible in the file you are
// reading. See docs/adr/0010-postgres-without-an-orm.md.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/gregwinn/EmovisMicroService/internal/money"
	"github.com/gregwinn/EmovisMicroService/internal/outbox"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

// Store is a transaction.Store backed by PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store using pool. The caller owns the pool's lifetime.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Connect opens a connection pool and verifies it can reach the database.
//
// Failing here rather than on the first request means a misconfigured database
// URL stops a deploy at startup instead of turning every inbound transaction
// into a 500.
func Connect(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	// Recycle connections so a long-lived process does not pin them across a
	// database failover or a rolling restart of the server.
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}

// Ping reports whether the database is reachable. It backs the readiness probe.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// insertTransaction relies on the database to arbitrate concurrent pushes of
// the same idempotency key.
//
// ON CONFLICT DO NOTHING makes the insert a no-op for a key that already
// exists, and the RETURNING clause then yields no row. That distinction — a row
// back means we created it, no row back means someone else did — is what makes
// this atomic without an explicit lock or a serializable transaction. A
// read-then-write would let two concurrent retries both decide the key was free
// and bill the customer twice.
const insertTransaction = `
INSERT INTO transactions (
    id, source, source_reference, transaction_type,
    occurred_at, received_at,
    plate_number, plate_jurisdiction, plate_number_key, plate_jurisdiction_key,
    transponder_number, transponder_number_key,
    base_amount, base_amount_text, currency,
    location, metadata,
    association_status, settlement_status,
    fingerprint
) VALUES (
    $1, $2, $3, $4,
    $5, $6,
    $7, $8, $9, $10,
    $11, $12,
    $13, $14, $15,
    $16, $17,
    $18, $19,
    $20
)
ON CONFLICT ON CONSTRAINT transactions_idempotency_key DO NOTHING
RETURNING id`

const selectByKey = `
SELECT
    id, source, source_reference, transaction_type,
    occurred_at, received_at,
    plate_number, plate_jurisdiction, plate_number_key, plate_jurisdiction_key,
    transponder_number, transponder_number_key,
    base_amount, base_amount_text, currency,
    location, metadata,
    association_status, settlement_status,
    fingerprint
FROM transactions
WHERE source = $1 AND source_reference = $2`

const insertOutboxEvent = `
INSERT INTO outbox_events (event_id, event_type, aggregate_id, payload, created_at)
VALUES ($1, $2, $3, $4, $5)`

// Ingest records tx, or returns the transaction already stored under its
// idempotency key.
//
// When the transaction is new, the row and its outbox event are written in a
// single database transaction. That atomicity is the entire point: a commit
// that succeeds without its event would leave a billable transaction that the
// resolution pipeline never hears about, and nothing would report an error.
// See docs/adr/0007-transactional-outbox.md.
//
// No event is written for a duplicate. The original push already produced one,
// and a retry is not a second business event.
func (s *Store) Ingest(ctx context.Context, tx transaction.Transaction) (transaction.IngestOutcome, error) {
	dbTx, err := s.pool.Begin(ctx)
	if err != nil {
		return transaction.IngestOutcome{}, fmt.Errorf("begin transaction: %w", err)
	}
	// Rollback is a no-op once Commit has succeeded, so this is safe
	// unconditionally and guarantees no connection is leaked on an early return.
	defer func() { _ = dbTx.Rollback(ctx) }()

	outcome, err := s.ingestInTx(ctx, dbTx, tx)
	if err != nil {
		return transaction.IngestOutcome{}, err
	}

	if err := dbTx.Commit(ctx); err != nil {
		return transaction.IngestOutcome{}, fmt.Errorf("commit transaction: %w", err)
	}

	return outcome, nil
}

func (s *Store) ingestInTx(
	ctx context.Context,
	dbTx pgx.Tx,
	tx transaction.Transaction,
) (transaction.IngestOutcome, error) {
	fingerprint := tx.Fingerprint()

	var inserted uuid.UUID
	err := dbTx.QueryRow(ctx, insertTransaction,
		tx.ID,
		tx.Source,
		tx.SourceReference,
		tx.Type,
		tx.OccurredAt,
		tx.ReceivedAt,
		nullable(plateNumber(tx)),
		nullable(plateJurisdiction(tx)),
		nullable(plateNumberKey(tx)),
		nullable(plateJurisdictionKey(tx)),
		nullable(transponderNumber(tx)),
		nullable(transponderNumberKey(tx)),
		tx.BaseAmount.Decimal(),
		tx.BaseAmount.AsReceived(),
		tx.BaseAmount.Currency().Code,
		tx.Location,
		tx.Metadata,
		string(tx.AssociationStatus),
		string(tx.SettlementStatus),
		fingerprint,
	).Scan(&inserted)

	switch {
	case err == nil:
		if err := s.writeOutboxEvent(ctx, dbTx, tx); err != nil {
			return transaction.IngestOutcome{}, err
		}
		return transaction.IngestOutcome{Transaction: tx, Duplicate: false}, nil

	case errors.Is(err, pgx.ErrNoRows):
		// The key already existed. Read back what is actually stored — the
		// contract requires returning the existing record, not the one just
		// rejected.
		existing, stored, readErr := s.getByKeyTx(ctx, dbTx, transaction.KeyOf(tx))
		if readErr != nil {
			return transaction.IngestOutcome{}, fmt.Errorf("read existing transaction: %w", readErr)
		}

		return transaction.IngestOutcome{
			Transaction: existing,
			Duplicate:   true,
			Divergent:   stored != fingerprint,
		}, nil

	default:
		return transaction.IngestOutcome{}, fmt.Errorf("insert transaction: %w", err)
	}
}

// writeOutboxEvent queues the downstream notification inside the caller's
// database transaction.
//
// A failure here rolls back the transaction row too, which is the correct
// outcome: a billable record the pipeline will never be told about is worse
// than a producer retry. Producers retry; unnoticed revenue loss does not
// correct itself.
func (s *Store) writeOutboxEvent(ctx context.Context, dbTx pgx.Tx, tx transaction.Transaction) error {
	eventID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate event id: %w", err)
	}

	event, err := outbox.NewTransactionReceived(tx, eventID)
	if err != nil {
		return fmt.Errorf("build outbox event: %w", err)
	}

	if _, err := dbTx.Exec(ctx, insertOutboxEvent,
		event.ID, event.Type, event.AggregateID, event.Payload, event.CreatedAt,
	); err != nil {
		return fmt.Errorf("queue outbox event: %w", err)
	}

	return nil
}

// Get returns the transaction stored under key.
func (s *Store) Get(ctx context.Context, key transaction.Key) (transaction.Transaction, error) {
	tx, _, err := s.getByKey(ctx, key)
	return tx, err
}

// querier is satisfied by both *pgxpool.Pool and pgx.Tx, so reads work the same
// whether or not they are inside an open database transaction.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// getByKey also returns the stored fingerprint, which Ingest needs and callers
// of Get do not.
func (s *Store) getByKey(ctx context.Context, key transaction.Key) (transaction.Transaction, string, error) {
	return s.getByKeyTx(ctx, s.pool, key)
}

func (s *Store) getByKeyTx(ctx context.Context, q querier, key transaction.Key) (transaction.Transaction, string, error) {
	row := q.QueryRow(ctx, selectByKey, key.Source, key.SourceReference)

	var (
		tx                                       transaction.Transaction
		plateNum, plateJur, plateNumK, plateJurK *string
		transNum, transNumK                      *string
		amount                                   decimal.Decimal
		amountText, currencyCode                 string
		association, settlement, fingerprint     string
	)

	err := row.Scan(
		&tx.ID, &tx.Source, &tx.SourceReference, &tx.Type,
		&tx.OccurredAt, &tx.ReceivedAt,
		&plateNum, &plateJur, &plateNumK, &plateJurK,
		&transNum, &transNumK,
		&amount, &amountText, &currencyCode,
		&tx.Location, &tx.Metadata,
		&association, &settlement,
		&fingerprint,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return transaction.Transaction{}, "", transaction.ErrNotFound
	}
	if err != nil {
		return transaction.Transaction{}, "", fmt.Errorf("scan transaction: %w", err)
	}

	// Timestamps come back in the session time zone; the domain works in UTC.
	tx.OccurredAt = tx.OccurredAt.UTC()
	tx.ReceivedAt = tx.ReceivedAt.UTC()

	tx.AssociationStatus = transaction.AssociationStatus(association)
	tx.SettlementStatus = transaction.SettlementStatus(settlement)

	if plateNum != nil {
		tx.Plate = &transaction.Plate{
			Number:          *plateNum,
			Jurisdiction:    deref(plateJur),
			NumberKey:       deref(plateNumK),
			JurisdictionKey: deref(plateJurK),
		}
	}
	if transNum != nil {
		tx.Transponder = &transaction.Transponder{
			Number:    *transNum,
			NumberKey: deref(transNumK),
		}
	}

	// Rebuild the amount from the text the producer sent rather than from the
	// NUMERIC column, so a round trip through the database returns byte-identical
	// evidence. The currency is looked up rather than assumed, because the
	// minor-unit exponent governs how the value renders.
	currency, ok := money.Lookup(currencyCode)
	if !ok {
		// A currency stored earlier and since removed from the table. Fail
		// loudly: silently substituting a default would misreport money.
		return transaction.Transaction{}, "", fmt.Errorf("stored currency %q is not recognised", currencyCode)
	}

	tx.BaseAmount, err = money.Parse(amountText, currency)
	if err != nil {
		return transaction.Transaction{}, "", fmt.Errorf("parse stored amount %q: %w", amountText, err)
	}

	return tx, fingerprint, nil
}

func plateNumber(tx transaction.Transaction) string {
	if tx.Plate == nil {
		return ""
	}
	return tx.Plate.Number
}

func plateJurisdiction(tx transaction.Transaction) string {
	if tx.Plate == nil {
		return ""
	}
	return tx.Plate.Jurisdiction
}

func plateNumberKey(tx transaction.Transaction) string {
	if tx.Plate == nil {
		return ""
	}
	return tx.Plate.NumberKey
}

func plateJurisdictionKey(tx transaction.Transaction) string {
	if tx.Plate == nil {
		return ""
	}
	return tx.Plate.JurisdictionKey
}

func transponderNumber(tx transaction.Transaction) string {
	if tx.Transponder == nil {
		return ""
	}
	return tx.Transponder.Number
}

func transponderNumberKey(tx transaction.Transaction) string {
	if tx.Transponder == nil {
		return ""
	}
	return tx.Transponder.NumberKey
}

// nullable maps an absent identifier to SQL NULL rather than an empty string.
// The CHECK constraint that requires at least one identifier tests for NULL, so
// storing "" would defeat it.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
