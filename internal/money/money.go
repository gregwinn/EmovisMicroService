// Package money represents exact monetary amounts.
//
// Floating point never appears here. A tolling transaction is the atomic unit
// of revenue in a back office, and binary floating point cannot represent most
// decimal fractions exactly — 12.50 is fine, 0.10 is not. Accumulated across
// millions of transactions those errors become real money and unreconcilable
// ledgers.
//
// The contract in api/openapi.yaml sends amounts as decimal strings ("12.50")
// for exactly this reason, and says so explicitly. This package is the type
// that honours it.
//
// The usual answer for money in Go is an int64 of minor units, and it is a good
// one when the currency is fixed at compile time. Here it is not: the contract
// makes currency optional per transaction and defers the default to deployment
// configuration, so the minor-unit exponent is only known at runtime. An exact
// decimal with a runtime-resolved currency models that honestly.
package money

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/shopspring/decimal"
)

// Parsing failures, exposed as sentinels so that callers can map them onto
// their own field-level error messages without matching on strings.
var (
	// ErrEmpty is returned for an empty amount string.
	ErrEmpty = errors.New("must not be empty")

	// ErrSyntax is returned for anything that is not a plain decimal number.
	ErrSyntax = errors.New(`must be a decimal string, for example "12.50"`)

	// ErrScale is returned when an amount carries more decimal places than the
	// currency has minor units.
	ErrScale = errors.New("has more decimal places than the currency allows")
)

// decimalSyntax deliberately rejects everything decimal.NewFromString would
// otherwise accept: exponent notation ("1e5"), a leading plus, and surrounding
// whitespace. On a billing boundary, "the producer sent something unusual" is
// worth a 400 rather than a silent reinterpretation — "1e5" almost certainly
// means a broken integration, not a $100,000 toll.
var decimalSyntax = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// Amount is an exact monetary value in a specific currency.
//
// The zero value is not meaningful; construct one with Parse. Amount is
// immutable, comparable by value through Equal, and safe to copy.
type Amount struct {
	value      decimal.Decimal
	currency   Currency
	asReceived string
}

// Parse converts a producer-supplied decimal string into an Amount denominated
// in c.
//
// The original text is retained verbatim. The contract states that base_amount
// is immutable once accepted and that corrections arrive as separate
// adjustments, which makes the exact bytes the producer sent part of the
// evidentiary record: in a dispute you must be able to show what was received,
// not a value re-rendered by this service.
//
// Negative values parse successfully. Whether a negative amount is meaningful
// is a policy question that belongs to the caller — a base rate should never be
// negative, but an adjustment legitimately is.
func Parse(raw string, c Currency) (Amount, error) {
	if raw == "" {
		return Amount{}, ErrEmpty
	}

	if !decimalSyntax.MatchString(raw) {
		return Amount{}, ErrSyntax
	}

	value, err := decimal.NewFromString(raw)
	if err != nil {
		// Unreachable for anything matching decimalSyntax, but an amount that
		// silently became zero would be a billing defect, so it stays checked.
		return Amount{}, fmt.Errorf("%w: %w", ErrSyntax, err)
	}

	if scale := decimalPlaces(raw); scale > c.Exponent {
		return Amount{}, fmt.Errorf("%w: %s allows %d", ErrScale, c.Code, c.Exponent)
	}

	return Amount{value: value, currency: c, asReceived: raw}, nil
}

// decimalPlaces counts digits after the decimal point in the original text.
//
// This reads the string rather than asking the parsed decimal for its exponent
// because trailing zeros matter here: "12.500" in a 2-minor-unit currency is a
// producer sending more precision than the currency has, and should be
// rejected, even though its numeric value is representable.
func decimalPlaces(raw string) int32 {
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i] == '.' {
			return int32(len(raw) - i - 1)
		}
	}
	return 0
}

// Decimal returns the exact value.
func (a Amount) Decimal() decimal.Decimal { return a.value }

// Currency returns the currency the amount is denominated in.
func (a Amount) Currency() Currency { return a.currency }

// AsReceived returns the producer's original text, byte for byte.
func (a Amount) AsReceived() string { return a.asReceived }

// IsNegative reports whether the amount is below zero.
func (a Amount) IsNegative() bool { return a.value.IsNegative() }

// IsZero reports whether the amount is exactly zero.
func (a Amount) IsZero() bool { return a.value.IsZero() }

// Equal reports whether two amounts are the same currency and numerically
// equal. It compares values, not text: "12.5" and "12.50" are equal.
func (a Amount) Equal(b Amount) bool {
	return a.currency.Code == b.currency.Code && a.value.Equal(b.value)
}

// String returns the canonical rendering: the value at the currency's full
// minor-unit precision, followed by the currency code. Unlike AsReceived this
// is normalized, so "12.5" becomes "12.50 USD".
func (a Amount) String() string {
	return a.value.StringFixed(a.currency.Exponent) + " " + a.currency.Code
}
