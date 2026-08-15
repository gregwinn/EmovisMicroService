package outbox_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gregwinn/EmovisMicroService/internal/money"
	"github.com/gregwinn/EmovisMicroService/internal/outbox"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

var (
	eventID    = uuid.MustParse("0198b1f0-0000-7000-8000-0000000000ee")
	occurredAt = time.Date(2026, 8, 14, 13, 45, 2, 0, time.UTC)
)

func acceptedTransaction(t *testing.T, mutate func(*transaction.Submission)) transaction.Transaction {
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

	tx, problems, err := transaction.Rules{
		Types:           transaction.NewTypeSet([]string{"toll"}),
		DefaultCurrency: usd,
		Now:             func() time.Time { return occurredAt.Add(time.Minute) },
	}.Accept(s)
	require.NoError(t, err)
	require.Empty(t, problems)

	return tx
}

func decodeBody(t *testing.T, e outbox.Event) outbox.TransactionReceived {
	t.Helper()
	var body outbox.TransactionReceived
	require.NoError(t, json.Unmarshal(e.Payload, &body))
	return body
}

func TestNewTransactionReceived(t *testing.T) {
	tx := acceptedTransaction(t, func(s *transaction.Submission) {
		s.Transponder = "01800-12345678"
		s.Location = map[string]any{"facility": "SH-130"}
		s.Metadata = map[string]any{"vendor": "acme"}
	})

	event, err := outbox.NewTransactionReceived(tx, eventID)
	require.NoError(t, err)

	assert.Equal(t, eventID, event.ID)
	assert.Equal(t, outbox.EventTypeTransactionReceived, event.Type)
	assert.Equal(t, tx.ID, event.AggregateID)
	assert.Equal(t, tx.ReceivedAt.UTC(), event.CreatedAt)

	body := decodeBody(t, event)
	assert.Equal(t, eventID, body.EventID)
	assert.Equal(t, "transaction.received", body.EventType)
	assert.Equal(t, tx.ID.String(), body.TransactionID)
	assert.Equal(t, "lane-controller-07", body.Source)
	assert.Equal(t, "LC07-20260814-000918", body.SourceReference)
	assert.Equal(t, "toll", body.TransactionType)
	assert.Equal(t, occurredAt, body.TransactionTimeUTC)
	assert.Equal(t, "received", body.AssociationStatus)
	assert.Equal(t, "priced", body.SettlementStatus)

	// Both the raw read and the matching key: resolution needs the key, disputes
	// need the evidence.
	assert.Equal(t, "ABC-1234", body.PlateNumber)
	assert.Equal(t, "tx", body.PlateJurisdiction)
	assert.Equal(t, "ABC1234", body.PlateNumberKey)
	assert.Equal(t, "01800-12345678", body.Transponder)
	assert.Equal(t, "0180012345678", body.TransponderKey)

	assert.Equal(t, "SH-130", body.Location["facility"])
	assert.Equal(t, "acme", body.Metadata["vendor"])
}

// Money crosses the wire as a decimal string at the currency's full precision.
// A JSON number would be an IEEE-754 double in most consumers, and 12.50 is not
// exactly representable as one.
func TestEventAmountIsADecimalStringAtCurrencyPrecision(t *testing.T) {
	tests := []struct {
		name     string
		amount   string
		currency string
		want     string
	}{
		{name: "two decimal places", amount: "12.50", currency: "USD", want: "12.50"},
		{name: "trailing zero is restored", amount: "12.5", currency: "USD", want: "12.50"},
		{name: "whole number gains places", amount: "12", currency: "USD", want: "12.00"},
		{name: "zero-exponent currency", amount: "350", currency: "JPY", want: "350"},
		{name: "three-exponent currency", amount: "1.2", currency: "BHD", want: "1.200"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := acceptedTransaction(t, func(s *transaction.Submission) {
				s.BaseAmount = tt.amount
				s.Currency = tt.currency
			})

			event, err := outbox.NewTransactionReceived(tx, eventID)
			require.NoError(t, err)

			body := decodeBody(t, event)
			assert.Equal(t, tt.want, body.BaseAmount)
			assert.Equal(t, tt.currency, body.Currency)

			// The value must not be serialized as a JSON number.
			assert.Contains(t, string(event.Payload), `"base_amount":"`+tt.want+`"`)
		})
	}
}

func TestEventOmitsAbsentIdentifiers(t *testing.T) {
	tx := acceptedTransaction(t, func(s *transaction.Submission) {
		s.Plate = nil
		s.Transponder = "0180012345678"
	})

	event, err := outbox.NewTransactionReceived(tx, eventID)
	require.NoError(t, err)

	raw := string(event.Payload)
	assert.NotContains(t, raw, "plate_number")
	assert.Contains(t, raw, "transponder_number")
}

// The event body is a published interface. This test exists so that renaming a
// field in the domain cannot silently reshape what consumers receive.
func TestEventFieldNamesAreStable(t *testing.T) {
	tx := acceptedTransaction(t, nil)

	event, err := outbox.NewTransactionReceived(tx, eventID)
	require.NoError(t, err)

	var generic map[string]any
	require.NoError(t, json.Unmarshal(event.Payload, &generic))

	for _, field := range []string{
		"event_id", "event_type", "transaction_id", "source", "source_reference",
		"transaction_type", "transaction_time_utc", "received_at",
		"base_amount", "currency", "association_status", "settlement_status",
	} {
		assert.Contains(t, generic, field, "%s is part of the published event contract", field)
	}
}

func TestLogPublisher(t *testing.T) {
	var buf bytes.Buffer
	pub := outbox.NewLogPublisher(slog.New(slog.NewJSONHandler(&buf, nil)))

	assert.Equal(t, "log", pub.Name())

	tx := acceptedTransaction(t, nil)
	event, err := outbox.NewTransactionReceived(tx, eventID)
	require.NoError(t, err)

	require.NoError(t, pub.Publish(t.Context(), event))

	logged := buf.String()
	assert.Contains(t, logged, "outbox event published")
	assert.Contains(t, logged, eventID.String())
	assert.Contains(t, logged, "transaction.received")
}

// The payload carries plate and transponder values. Logging it would put PII
// into log aggregation, which is exactly what the redaction rule forbids.
func TestLogPublisherDoesNotLogThePayload(t *testing.T) {
	var buf bytes.Buffer
	pub := outbox.NewLogPublisher(slog.New(slog.NewJSONHandler(&buf, nil)))

	tx := acceptedTransaction(t, func(s *transaction.Submission) {
		s.Plate = &transaction.PlateRead{Number: "SECRET99", Jurisdiction: "TX"}
	})
	event, err := outbox.NewTransactionReceived(tx, eventID)
	require.NoError(t, err)

	require.NoError(t, pub.Publish(t.Context(), event))

	assert.NotContains(t, buf.String(), "SECRET99")
}

func TestDefaultRelayOptionsAreSane(t *testing.T) {
	opts := outbox.DefaultRelayOptions()

	assert.Positive(t, opts.BatchSize)
	assert.Positive(t, opts.PollInterval)
	assert.Positive(t, opts.BaseBackoff)
	assert.Positive(t, opts.MaxAttempts)
	assert.Greater(t, opts.MaxBackoff, opts.BaseBackoff)
}
