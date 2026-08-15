// Package outbox implements the transactional outbox pattern.
//
// The contract states that "every accepted transaction is picked up
// asynchronously by a resolution pipeline". The naive way to satisfy that is to
// publish from the request handler once the database commit succeeds — and it
// has a window that cannot be closed by retrying:
//
//	commit succeeds → publish fails → the transaction exists, and the
//	resolution pipeline never hears about it
//
// That is silent revenue loss. It does not surface as an error anywhere, and it
// is only discovered when someone reconciles takings against the road.
//
// The fix is to make the event part of the same database transaction as the
// record. Either both are written or neither is. A separate relay then drains
// the table and publishes, retrying until it succeeds. No distributed
// transaction, no two-phase commit, and the failure mode moves from "lost
// event" to "late event" — which is recoverable.
//
// The cost is at-least-once delivery: the relay can publish successfully and
// then crash before marking the row, so a consumer may see an event twice.
// Every event therefore carries a stable EventID for deduplication, and this is
// stated in docs/adr/0007-transactional-outbox.md so consumers design for it.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

// EventTypeTransactionReceived is emitted for every newly accepted transaction.
//
// Named for what happened rather than for what a consumer should do with it: a
// second consumer appearing later must not have to reinterpret an event called
// "resolve_this".
const EventTypeTransactionReceived = "transaction.received"

// Event is a message queued for downstream delivery.
type Event struct {
	// ID is stable across redeliveries. Consumers deduplicate on it.
	ID uuid.UUID
	// Type identifies the event shape, e.g. transaction.received.
	Type string
	// AggregateID is the transaction the event concerns.
	AggregateID uuid.UUID
	// Payload is the serialized event body.
	Payload []byte
	// CreatedAt is when the event was written, not when it was published.
	CreatedAt time.Time
}

// Publisher delivers events to the downstream transport.
//
// Implementations must be safe for concurrent use and must treat a returned
// error as retryable unless it is clearly permanent — the relay backs off and
// tries again, which is the correct behaviour for a transient outage and
// harmless for a duplicate.
type Publisher interface {
	Publish(ctx context.Context, e Event) error
	// Name identifies the publisher in logs and startup output.
	Name() string
}

// TransactionReceived is the published body for a newly accepted transaction.
//
// It is a distinct type from transaction.Transaction on purpose. This is a
// published interface with its own compatibility obligations: renaming a field
// in the domain must not silently reshape what downstream consumers receive.
// Changing this struct is a breaking change to the event contract.
type TransactionReceived struct {
	EventID   uuid.UUID `json:"event_id"`
	EventType string    `json:"event_type"`

	TransactionID   string `json:"transaction_id"`
	Source          string `json:"source"`
	SourceReference string `json:"source_reference"`
	TransactionType string `json:"transaction_type"`

	TransactionTimeUTC time.Time `json:"transaction_time_utc"`
	ReceivedAt         time.Time `json:"received_at"`

	// Identifiers are included because resolution cannot attribute a vehicle
	// without them. Consumers of this topic are inside the trust boundary; the
	// PII constraint is about logs and aggregation, not about the pipeline that
	// exists to use these values.
	PlateNumber       string `json:"plate_number,omitempty"`
	PlateJurisdiction string `json:"plate_jurisdiction,omitempty"`
	PlateNumberKey    string `json:"plate_number_key,omitempty"`
	Transponder       string `json:"transponder_number,omitempty"`
	TransponderKey    string `json:"transponder_number_key,omitempty"`

	// Amounts cross the wire as decimal strings, for the same reason the
	// inbound contract does: JSON numbers are IEEE-754 doubles in most
	// consumers, and 12.50 is not exactly representable.
	BaseAmount string `json:"base_amount"`
	Currency   string `json:"currency"`

	Location map[string]any `json:"location,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`

	AssociationStatus string `json:"association_status"`
	SettlementStatus  string `json:"settlement_status"`
}

// NewTransactionReceived builds the event for an accepted transaction.
func NewTransactionReceived(tx transaction.Transaction, eventID uuid.UUID) (Event, error) {
	body := TransactionReceived{
		EventID:   eventID,
		EventType: EventTypeTransactionReceived,

		TransactionID:   tx.ID.String(),
		Source:          tx.Source,
		SourceReference: tx.SourceReference,
		TransactionType: tx.Type,

		TransactionTimeUTC: tx.OccurredAt.UTC(),
		ReceivedAt:         tx.ReceivedAt.UTC(),

		// Rendered at the currency's full minor-unit precision ("12.50", not
		// "12.5") so downstream consumers and reconciliation reports agree on
		// the shape of money without each applying their own formatting.
		BaseAmount: tx.BaseAmount.Decimal().StringFixed(tx.BaseAmount.Currency().Exponent),
		Currency:   tx.BaseAmount.Currency().Code,

		Location: tx.Location,
		Metadata: tx.Metadata,

		AssociationStatus: string(tx.AssociationStatus),
		SettlementStatus:  string(tx.SettlementStatus),
	}

	if tx.Plate != nil {
		body.PlateNumber = tx.Plate.Number
		body.PlateJurisdiction = tx.Plate.Jurisdiction
		body.PlateNumberKey = tx.Plate.NumberKey
	}
	if tx.Transponder != nil {
		body.Transponder = tx.Transponder.Number
		body.TransponderKey = tx.Transponder.NumberKey
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Event{}, fmt.Errorf("marshal transaction.received event: %w", err)
	}

	return Event{
		ID:          eventID,
		Type:        EventTypeTransactionReceived,
		AggregateID: tx.ID,
		Payload:     payload,
		CreatedAt:   tx.ReceivedAt.UTC(),
	}, nil
}
