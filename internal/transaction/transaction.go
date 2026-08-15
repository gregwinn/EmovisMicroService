// Package transaction is the domain: what a billable tolling transaction is,
// and what makes one acceptable.
//
// The scope of this package is set by the contract itself:
//
//	Accepting a transaction is not the same as resolving it. Who owns the
//	vehicle, what the final price is, and whether it has been collected are
//	downstream concerns. Ingest's job is to durably accept, validate, and
//	acknowledge.
//
// So there is no pricing here, no account lookup, and no attribution. There is
// a transaction, the rules that decide whether it can be accepted, and the two
// status axes it starts life on.
//
// Nothing in this package imports the HTTP layer or the generated contract
// types. The mapping from wire format to domain lives at the edge, in
// internal/httpapi, so that regenerating from a changed spec cannot ripple
// through the rules.
package transaction

import (
	"time"

	"github.com/google/uuid"

	"github.com/gregwinn/EmovisMicroService/internal/money"
)

// AssociationStatus is the "whose is it?" axis: progress toward attributing a
// transaction to a customer account.
//
// It is orthogonal to SettlementStatus, and the contract is explicit that this
// is deliberate. Collapsing the two into one status enum cannot represent an
// unidentified vehicle with a known toll amount — which is precisely the
// video-tolling case that makes up a large share of revenue.
type AssociationStatus string

const (
	// AssociationReceived is where every freshly ingested transaction starts.
	AssociationReceived AssociationStatus = "received"
	// AssociationResolving means attribution is in progress downstream.
	AssociationResolving AssociationStatus = "resolving"
	// AssociationAssociated means the transaction belongs to a known account.
	AssociationAssociated AssociationStatus = "associated"
	// AssociationException means attribution failed and needs human review.
	AssociationException AssociationStatus = "exception"
)

// SettlementStatus is the "what is its financial state?" axis.
type SettlementStatus string

const (
	// SettlementUnpriced means no rate has been established.
	SettlementUnpriced SettlementStatus = "unpriced"
	// SettlementPriced means a rate is known. Every ingested transaction starts
	// here, because the contract requires the producer to supply base_amount.
	SettlementPriced SettlementStatus = "priced"
	// SettlementPayable means the amount is owed by an identified party.
	SettlementPayable SettlementStatus = "payable"
	// SettlementPaid means the amount has been collected.
	SettlementPaid SettlementStatus = "paid"
)

// Submission is a transaction as offered by a producer, before validation.
//
// This is the domain's own input type rather than the generated request struct.
// The translation from the wire format happens once, at the HTTP edge, which
// keeps the blast radius of a contract change at the boundary instead of
// spreading through the rules — the point of treating the spec as the contract
// in the first place.
type Submission struct {
	// Source is the producing system's id. With SourceReference it forms the
	// idempotency key.
	Source string
	// SourceReference is the producer's own unique id for this transaction.
	SourceReference string
	// Type is the kind of billable event. Valid values are operator
	// configuration, not a compiled-in enum — the contract says so explicitly.
	Type string
	// OccurredAt is when the vehicle used the facility. May be well in the past
	// for a batch or image-review replay.
	OccurredAt time.Time

	// Plate is the license plate read, nil when the producer sent none.
	Plate *PlateRead
	// Transponder is the raw tag read, empty when the producer sent none.
	Transponder string

	// BaseAmount is the as-received rate, as a decimal string.
	BaseAmount string
	// Currency is the ISO-4217 code, empty when the producer omitted it, in
	// which case the deployment default applies.
	Currency string

	// Location and Metadata are producer passthrough. The contract declares both
	// free-form and says metadata is "not interpreted", so neither is modelled.
	Location map[string]any
	Metadata map[string]any
}

// PlateRead is a plate as submitted, before canonicalization.
type PlateRead struct {
	Number       string
	Jurisdiction string
}

// Transaction is a validated, accepted billable transaction.
//
// A value of this type has passed every rule in Validate. Constructing one
// outside this package is possible but skips that guarantee, so the ingest path
// always goes through Rules.Accept.
type Transaction struct {
	// ID is this system's identifier, a UUIDv7. Version 7 is time-ordered,
	// which keeps index inserts append-mostly on a table that only ever grows.
	ID uuid.UUID

	// Source and SourceReference are the producer's idempotency key.
	Source          string
	SourceReference string

	Type string

	// OccurredAt is when the vehicle used the facility, normalized to UTC.
	OccurredAt time.Time
	// ReceivedAt is when this service accepted it. The gap between the two is
	// the producer's submission lag, and is worth watching per producer.
	ReceivedAt time.Time

	// Plate and Transponder hold both the verbatim read and its canonical form.
	// At least one is always present; see Rules.Accept.
	Plate       *Plate
	Transponder *Transponder

	// BaseAmount is exact and retains the producer's original text.
	BaseAmount money.Amount

	Location map[string]any
	Metadata map[string]any

	// AssociationStatus and SettlementStatus are always received/priced on
	// ingest. Later transitions belong to the resolution pipeline.
	AssociationStatus AssociationStatus
	SettlementStatus  SettlementStatus
}

// HasPlate reports whether a usable plate read is present.
func (t Transaction) HasPlate() bool { return t.Plate != nil && !t.Plate.IsEmpty() }

// HasTransponder reports whether a usable transponder read is present.
func (t Transaction) HasTransponder() bool {
	return t.Transponder != nil && !t.Transponder.IsEmpty()
}

// SubmissionLag is how long the producer took to deliver the transaction. It is
// routinely large for batch and image-review producers and near zero for a lane
// controller, which makes it a useful per-producer health signal rather than an
// error condition.
func (t Transaction) SubmissionLag() time.Duration {
	return t.ReceivedAt.Sub(t.OccurredAt)
}
