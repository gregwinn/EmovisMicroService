package money

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	usd = Currency{Code: "USD", Exponent: 2}
	jpy = Currency{Code: "JPY", Exponent: 0}
	bhd = Currency{Code: "BHD", Exponent: 3}
)

func TestParseAcceptsValidAmounts(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		currency Currency
		want     string // canonical rendering
	}{
		{name: "the contract's own example", raw: "12.50", currency: usd, want: "12.50 USD"},
		{name: "whole number", raw: "12", currency: usd, want: "12.00 USD"},
		{name: "single decimal place", raw: "12.5", currency: usd, want: "12.50 USD"},
		{name: "zero", raw: "0", currency: usd, want: "0.00 USD"},
		{name: "zero with places", raw: "0.00", currency: usd, want: "0.00 USD"},
		{name: "negative adjustment", raw: "-3.25", currency: usd, want: "-3.25 USD"},
		{name: "large toll", raw: "999999.99", currency: usd, want: "999999.99 USD"},
		{name: "leading zero", raw: "0.05", currency: usd, want: "0.05 USD"},
		{name: "zero-exponent currency", raw: "350", currency: jpy, want: "350 JPY"},
		{name: "three-exponent currency", raw: "1.234", currency: bhd, want: "1.234 BHD"},
		{name: "three-exponent under-precise", raw: "1.2", currency: bhd, want: "1.200 BHD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, err := Parse(tt.raw, tt.currency)
			require.NoError(t, err)

			assert.Equal(t, tt.want, amount.String())
			assert.Equal(t, tt.raw, amount.AsReceived(), "the producer's text is preserved verbatim")
			assert.Equal(t, tt.currency, amount.Currency())
		})
	}
}

// The contract says amounts are decimal strings. Anything else is a broken
// integration and gets a 400, not a creative reinterpretation — "1e5" almost
// certainly does not mean a $100,000 toll.
func TestParseRejectsNonDecimalSyntax(t *testing.T) {
	tests := map[string]string{
		"exponent notation":        "1e5",
		"capital exponent":         "1E5",
		"leading plus":             "+12.50",
		"leading whitespace":       " 12.50",
		"trailing whitespace":      "12.50 ",
		"internal whitespace":      "12. 50",
		"thousands separator":      "1,250.00",
		"currency symbol":          "$12.50",
		"currency code appended":   "12.50USD",
		"two decimal points":       "12.5.0",
		"trailing decimal point":   "12.",
		"leading decimal point":    ".50",
		"letters":                  "twelve",
		"not a number":             "NaN",
		"infinity":                 "Inf",
		"hex":                      "0x1f",
		"bare minus":               "-",
		"minus with no digits":     "-.5",
		"underscore separator":     "1_250",
		"null literal as a string": "null",
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(raw, usd)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrSyntax)
		})
	}
}

func TestParseRejectsEmpty(t *testing.T) {
	_, err := Parse("", usd)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmpty)
}

// More precision than the currency has is a producer defect. Accepting it means
// silently deciding how to round someone else's money.
func TestParseRejectsExcessPrecision(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		currency Currency
	}{
		{name: "three places in a two-place currency", raw: "12.505", currency: usd},
		{name: "any places in a zero-place currency", raw: "350.5", currency: jpy},
		{name: "four places in a three-place currency", raw: "1.2345", currency: bhd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.raw, tt.currency)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrScale)
			assert.Contains(t, err.Error(), tt.currency.Code, "the error should name the currency")
		})
	}
}

// Trailing zeros are numerically insignificant but contractually significant:
// "12.500" is a producer sending three decimal places to a two-decimal currency,
// which is the same defect as "12.505" even though the value happens to round
// cleanly. Counting the text rather than the parsed exponent is what catches it.
func TestParseRejectsExcessPrecisionEvenWhenTrailingZero(t *testing.T) {
	_, err := Parse("12.500", usd)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrScale)
}

// The float64 nearest to 0.1 is 0.1000000000000000055511151231257827.
// Summing it ten times in binary floating point does not give 1. This test is
// the reason this package exists.
func TestArithmeticIsExact(t *testing.T) {
	tenth, err := Parse("0.10", usd)
	require.NoError(t, err)

	sum := tenth.Decimal()
	for range 9 {
		sum = sum.Add(tenth.Decimal())
	}

	one, err := Parse("1.00", usd)
	require.NoError(t, err)

	assert.True(t, sum.Equal(one.Decimal()),
		"0.10 summed ten times must equal exactly 1.00, got %s", sum)
	assert.Equal(t, "1.00", sum.StringFixed(2))
}

func TestEqualComparesValueNotText(t *testing.T) {
	a, err := Parse("12.5", usd)
	require.NoError(t, err)
	b, err := Parse("12.50", usd)
	require.NoError(t, err)

	assert.True(t, a.Equal(b), "numerically equal amounts are equal")
	assert.NotEqual(t, a.AsReceived(), b.AsReceived(), "but the received text differs")
}

func TestEqualDistinguishesCurrency(t *testing.T) {
	dollars, err := Parse("12.50", usd)
	require.NoError(t, err)
	dinars, err := Parse("12.500", bhd)
	require.NoError(t, err)

	assert.False(t, dollars.Equal(dinars), "same number, different currency, not equal")
}

func TestPredicates(t *testing.T) {
	tests := []struct {
		raw          string
		wantNegative bool
		wantZero     bool
	}{
		{raw: "12.50", wantNegative: false, wantZero: false},
		{raw: "0.00", wantNegative: false, wantZero: true},
		{raw: "-0.01", wantNegative: true, wantZero: false},
		{raw: "-0.00", wantNegative: false, wantZero: true},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			amount, err := Parse(tt.raw, usd)
			require.NoError(t, err)

			assert.Equal(t, tt.wantNegative, amount.IsNegative())
			assert.Equal(t, tt.wantZero, amount.IsZero())
		})
	}
}

// Callers map sentinels onto their own field-level messages, so errors.Is has to
// work through the wrapping that adds currency detail.
func TestErrorsAreMatchableWithErrorsIs(t *testing.T) {
	_, err := Parse("12.505", usd)
	require.Error(t, err)

	assert.True(t, errors.Is(err, ErrScale))
	assert.False(t, errors.Is(err, ErrSyntax))
}

func TestLookup(t *testing.T) {
	tests := []struct {
		name         string
		code         string
		wantFound    bool
		wantExponent int32
	}{
		{name: "exact code", code: "USD", wantFound: true, wantExponent: 2},
		{name: "lower case", code: "usd", wantFound: true, wantExponent: 2},
		{name: "mixed case", code: "Usd", wantFound: true, wantExponent: 2},
		{name: "surrounding whitespace", code: " USD ", wantFound: true, wantExponent: 2},
		{name: "zero-exponent currency", code: "JPY", wantFound: true, wantExponent: 0},
		{name: "three-exponent currency", code: "KWD", wantFound: true, wantExponent: 3},
		{name: "unknown code", code: "XYZ", wantFound: false},
		{name: "empty", code: "", wantFound: false},
		{name: "numeric code is not supported", code: "840", wantFound: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, ok := Lookup(tt.code)

			require.Equal(t, tt.wantFound, ok)
			if tt.wantFound {
				assert.Equal(t, tt.wantExponent, c.Exponent)
				assert.Equal(t, c.Code, c.String())
			}
		})
	}
}

// Not every currency has two decimal places. A table that only contained
// exponent-2 currencies would let "everything has cents" pass unnoticed.
func TestCurrencyTableCoversNonStandardExponents(t *testing.T) {
	byExponent := map[int32][]string{}
	for _, code := range Codes() {
		c, ok := Lookup(code)
		require.True(t, ok)
		byExponent[c.Exponent] = append(byExponent[c.Exponent], code)
	}

	assert.NotEmpty(t, byExponent[0], "expected currencies with no minor unit, e.g. JPY")
	assert.NotEmpty(t, byExponent[2], "expected currencies with cents, e.g. USD")
	assert.NotEmpty(t, byExponent[3], "expected currencies with three decimal places, e.g. KWD")
}

func TestCurrencyZeroValue(t *testing.T) {
	assert.True(t, Currency{}.IsZero())
	assert.False(t, usd.IsZero())
}
