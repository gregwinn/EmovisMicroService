package transaction

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// A Fingerprint identifies the billable content of a transaction, so that a
// replay under an existing idempotency key can be told apart from a *different*
// transaction submitted under one.
//
// The contract defines ingest as idempotent on (source, source_reference) and
// says a re-sent push that matches an existing record returns 200 with
// duplicate=true. It does not say what happens when the key matches but the
// content differs — there is no 409 in the contract. That gap is closed by
// answering exactly as the contract promises while recording the divergence;
// see docs/adr/0006-idempotency-divergence.md. The fingerprint is what makes
// detecting it possible.
//
// It is computed from the accepted transaction's business fields, not from the
// raw request bytes. Two producers, or the same producer's retry after a
// library upgrade, can serialise identical data with different key ordering,
// whitespace, or timestamp offsets. Hashing the bytes would flag all of those
// as divergent and bury the real signal in false positives.
//
// Deliberately excluded:
//
//	ID          minted per call, so it would never match
//	ReceivedAt  when we saw it, not what happened
//	statuses    always received/priced at ingest
//
// Raw reads are used rather than canonical keys: a producer that changes a
// plate from "ABC1234" to "ABC-1234" has changed the evidence, and that is
// worth noticing even though both canonicalize alike.

// fingerprintFields is the canonical shape that gets hashed. It exists as a
// named type so that adding a field to Transaction cannot silently change what
// a fingerprint covers — a reviewer has to decide here.
//
// Field order is fixed by the struct, and encoding/json sorts map keys, so the
// encoding is deterministic for a given set of values.
type fingerprintFields struct {
	Source          string         `json:"source"`
	SourceReference string         `json:"source_reference"`
	Type            string         `json:"type"`
	OccurredAt      time.Time      `json:"occurred_at"`
	PlateNumber     string         `json:"plate_number,omitempty"`
	PlateJurisdic   string         `json:"plate_jurisdiction,omitempty"`
	Transponder     string         `json:"transponder,omitempty"`
	Amount          string         `json:"amount"`
	Currency        string         `json:"currency"`
	Location        map[string]any `json:"location,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

// Fingerprint returns a hex-encoded SHA-256 over the transaction's billable
// content.
func (t Transaction) Fingerprint() string {
	fields := fingerprintFields{
		Source:          t.Source,
		SourceReference: t.SourceReference,
		Type:            t.Type,
		// Normalised to UTC on accept, so the encoding is offset-independent.
		OccurredAt: t.OccurredAt.UTC(),
		// The canonical decimal rather than the received text: "12.5" and
		// "12.50" are the same money, and a producer tidying its formatting is
		// not a divergence worth alerting on.
		Amount:   t.BaseAmount.Decimal().String(),
		Currency: t.BaseAmount.Currency().Code,
		Location: t.Location,
		Metadata: t.Metadata,
	}

	if t.Plate != nil {
		fields.PlateNumber = t.Plate.Number
		fields.PlateJurisdic = t.Plate.Jurisdiction
	}
	if t.Transponder != nil {
		fields.Transponder = t.Transponder.Number
	}

	// encoding/json cannot fail on this type: every field is a string, a time,
	// or a map that arrived by decoding JSON in the first place.
	encoded, _ := json.Marshal(fields)

	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
