package transaction

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/gregwinn/EmovisMicroService/internal/money"
)

// This is layer two of validation. Layer one is the OpenAPI contract, enforced
// as middleware before anything here runs: types, required fields, string
// lengths, date-time syntax. By the time a Submission reaches Accept it is
// known to be structurally well-formed.
//
// What remains is everything the schema cannot express — the difference between
// a payload that parses and a transaction that can be billed. The contract
// names three of these in its own 400 description ("no usable identifier",
// "unrecognized transaction_type", "an unparseable amount"), which is a strong
// hint that this layer is expected to exist.
//
// Every rule is a pure function of the submission and the operator's
// configuration. No I/O, no clock reads except through Rules.Now, no database.
// That is what makes them cheap to test exhaustively and readable by someone
// who knows tolling but not Go.

// DefaultMaxClockSkew is how far ahead of now transaction_time_utc may be.
//
// The contract says a transaction may be "well in the past" for a batch or
// image-review replay, and says nothing about the future. A future timestamp is
// a clock-skew or corruption signal, so it is bounded — but generously. A
// false rejection discards real revenue, which is worse than accepting a
// transaction stamped a few minutes ahead.
const DefaultMaxClockSkew = 5 * time.Minute

// RuleError is a semantic validation failure, in terms an integrator can act
// on.
type RuleError struct {
	// Field is the request field at fault, empty when the rule spans fields.
	Field string
	// Reason explains what is wrong.
	Reason string
}

func (e RuleError) Error() string {
	if e.Field == "" {
		return e.Reason
	}
	return e.Field + ": " + e.Reason
}

// Rules carries the operator configuration that acceptance depends on, plus the
// two sources of non-determinism, injected so tests can pin them.
type Rules struct {
	// Types is the operator's runtime set of accepted transaction types.
	Types *TypeSet
	// DefaultCurrency applies when the producer omits one, as the contract
	// permits.
	DefaultCurrency money.Currency
	// MaxClockSkew bounds how far ahead of Now a transaction may be stamped.
	MaxClockSkew time.Duration

	// Now reads the clock. Defaults to time.Now.
	Now func() time.Time
	// NewID mints transaction ids. Defaults to uuid.NewV7.
	NewID func() (uuid.UUID, error)
}

// Accept validates a submission and returns the transaction it becomes.
//
// It reports every rule failure rather than the first, for the same reason the
// contract validator does: a producer fixing an integration should get the
// whole list in one response.
//
// A non-empty error slice means nothing was accepted and the returned
// Transaction is meaningless.
func (r Rules) Accept(s Submission) (Transaction, []RuleError) {
	var problems []RuleError

	now := r.now()

	transactionType, typeProblem := r.validateType(s.Type)
	problems = appendIf(problems, typeProblem)

	occurredAt, timeProblem := r.validateTime(s.OccurredAt, now)
	problems = appendIf(problems, timeProblem)

	currency, currencyProblem := r.resolveCurrency(s.Currency)
	problems = appendIf(problems, currencyProblem)

	// Amount parsing needs a resolved currency to check precision against, so
	// it is skipped when the currency itself is bad. Reporting
	// `base_amount: has more decimal places than the currency allows` against a
	// currency that does not exist would be noise.
	var amount money.Amount
	if currencyProblem == nil {
		var amountProblem *RuleError
		amount, amountProblem = validateAmount(s.BaseAmount, currency)
		problems = appendIf(problems, amountProblem)
	}

	plate, transponder, identityProblem := validateIdentity(s)
	problems = appendIf(problems, identityProblem)

	if len(problems) > 0 {
		return Transaction{}, problems
	}

	id, err := r.newID()
	if err != nil {
		// Id generation failing is an infrastructure fault, not a bad request.
		// It surfaces as a rule error only so that Accept has one error channel;
		// the caller maps it to a 500. In practice uuid.NewV7 fails only if the
		// system entropy source is broken.
		return Transaction{}, []RuleError{{Reason: fmt.Sprintf("could not generate a transaction id: %v", err)}}
	}

	return Transaction{
		ID:              id,
		Source:          s.Source,
		SourceReference: s.SourceReference,
		Type:            transactionType,
		OccurredAt:      occurredAt,
		ReceivedAt:      now,
		Plate:           plate,
		Transponder:     transponder,
		BaseAmount:      amount,
		Location:        s.Location,
		Metadata:        s.Metadata,

		// Every freshly ingested transaction starts here. Priced, because the
		// contract requires the producer to supply a base rate; received,
		// because nothing has tried to attribute it yet.
		AssociationStatus: AssociationReceived,
		SettlementStatus:  SettlementPriced,
	}, nil
}

// validateType checks the type against the operator's runtime set, returning
// the operator's own spelling.
func (r Rules) validateType(raw string) (string, *RuleError) {
	if r.Types == nil {
		return "", &RuleError{
			Field:  "transaction_type",
			Reason: "no transaction types are configured for this deployment",
		}
	}

	canonical, ok := r.Types.Canonical(raw)
	if !ok {
		// The accepted set is deliberately not listed back to the producer: it
		// is operator configuration, it can be long, and echoing it turns an
		// unauthenticated endpoint into a configuration disclosure.
		return "", &RuleError{
			Field:  "transaction_type",
			Reason: fmt.Sprintf("unrecognized value %q", raw),
		}
	}

	return canonical, nil
}

// validateTime rejects implausibly future timestamps and normalizes to UTC.
//
// Old timestamps are explicitly fine. Two of the four producers the contract
// names — image-review vendors and batch file loaders — routinely submit
// transactions long after the vehicle passed, so a posting window would reject
// legitimate revenue.
func (r Rules) validateTime(occurredAt, now time.Time) (time.Time, *RuleError) {
	if occurredAt.IsZero() {
		return time.Time{}, &RuleError{Field: "transaction_time_utc", Reason: "is required"}
	}

	skew := r.MaxClockSkew
	if skew <= 0 {
		skew = DefaultMaxClockSkew
	}

	if occurredAt.After(now.Add(skew)) {
		return time.Time{}, &RuleError{
			Field:  "transaction_time_utc",
			Reason: fmt.Sprintf("is more than %s in the future", skew),
		}
	}

	// The field name asserts UTC and the contract means it. Accepting any valid
	// offset and normalizing is liberal on input, strict on storage — and it
	// means downstream comparisons never have to think about zones.
	return occurredAt.UTC(), nil
}

// resolveCurrency applies the deployment default when the producer omits one.
func (r Rules) resolveCurrency(raw string) (money.Currency, *RuleError) {
	if raw == "" {
		if r.DefaultCurrency.IsZero() {
			return money.Currency{}, &RuleError{
				Field:  "currency",
				Reason: "is required because this deployment has no default currency configured",
			}
		}
		return r.DefaultCurrency, nil
	}

	currency, ok := money.Lookup(raw)
	if !ok {
		return money.Currency{}, &RuleError{
			Field:  "currency",
			Reason: fmt.Sprintf("unrecognized ISO-4217 code %q", raw),
		}
	}

	return currency, nil
}

// validateAmount parses the as-received rate and rejects negatives.
func validateAmount(raw string, currency money.Currency) (money.Amount, *RuleError) {
	amount, err := money.Parse(raw, currency)
	if err != nil {
		return money.Amount{}, &RuleError{Field: "base_amount", Reason: amountReason(err, currency)}
	}

	// base_amount is an as-received rate. The contract says corrections arrive
	// as separate adjustments rather than by editing this value, so a negative
	// here is a producer defect rather than a credit.
	if amount.IsNegative() {
		return money.Amount{}, &RuleError{Field: "base_amount", Reason: "must not be negative"}
	}

	return amount, nil
}

func amountReason(err error, currency money.Currency) string {
	switch {
	case errors.Is(err, money.ErrEmpty):
		return "is required"
	case errors.Is(err, money.ErrScale):
		return fmt.Sprintf("has more than %d decimal places, which %s does not allow",
			currency.Exponent, currency.Code)
	case errors.Is(err, money.ErrSyntax):
		return `must be a decimal string, for example "12.50"`
	default:
		return "is not a valid amount"
	}
}

// validateIdentity enforces the one rule the contract states outright and the
// JSON schema cannot express: a transaction must be attributable to a vehicle.
//
// Both plate and transponder are individually optional, so no schema can
// require "at least one of". The contract spells out why it matters: "a payload
// with neither cannot ever be attributed to a customer" — it is unbillable
// revenue that would sit in an exception queue forever.
func validateIdentity(s Submission) (*Plate, *Transponder, *RuleError) {
	var plate *Plate
	if s.Plate != nil {
		p := NewPlate(s.Plate.Number, s.Plate.Jurisdiction)
		if !p.IsEmpty() {
			plate = &p
		}
	}

	var transponder *Transponder
	if s.Transponder != "" {
		t := NewTransponder(s.Transponder)
		if !t.IsEmpty() {
			transponder = &t
		}
	}

	if plate == nil && transponder == nil {
		// No field is named because the failure spans two of them, and pointing
		// at either one alone would suggest that field was the problem.
		return nil, nil, &RuleError{
			Reason: "at least one usable identifier is required: plate or transponder_number",
		}
	}

	return plate, transponder, nil
}

func (r Rules) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func (r Rules) newID() (uuid.UUID, error) {
	if r.NewID != nil {
		return r.NewID()
	}
	return uuid.NewV7()
}

func appendIf(problems []RuleError, problem *RuleError) []RuleError {
	if problem == nil {
		return problems
	}
	return append(problems, *problem)
}
