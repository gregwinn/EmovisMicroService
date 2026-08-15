package money

import "strings"

// Currency is an ISO-4217 code together with its minor-unit exponent — the
// number of decimal places the currency actually has.
//
// The exponent is not always 2. Japanese yen has none, and several Gulf and
// North African currencies have three. Assuming "cents" is a bug that only
// shows up once a deployment crosses a border, which for a tolling operator
// running schemes in several countries is a matter of when, not if.
type Currency struct {
	// Code is the upper-case ISO-4217 alphabetic code, e.g. "USD".
	Code string
	// Exponent is the number of minor-unit decimal places, e.g. 2 for USD.
	Exponent int32
}

// String returns the currency code.
func (c Currency) String() string { return c.Code }

// IsZero reports whether the currency is unset.
func (c Currency) IsZero() bool { return c.Code == "" }

// currencies is the set of ISO-4217 currencies this service recognises.
//
// It is deliberately a curated table rather than the full ISO register: it
// covers the markets a tolling operator plausibly runs schemes in, plus the
// exponent-0 and exponent-3 currencies that exist specifically to break the
// "everything has cents" assumption. Adding a currency is a one-line change.
//
// Unlike transaction types, this is not operator policy — ISO-4217 is a
// standard, so it belongs in code rather than in runtime configuration. Which
// currency a deployment *defaults* to is configuration; see internal/config.
var currencies = map[string]Currency{
	// North America
	"USD": {Code: "USD", Exponent: 2},
	"CAD": {Code: "CAD", Exponent: 2},
	"MXN": {Code: "MXN", Exponent: 2},

	// Europe
	"EUR": {Code: "EUR", Exponent: 2},
	"GBP": {Code: "GBP", Exponent: 2},
	"SEK": {Code: "SEK", Exponent: 2},
	"NOK": {Code: "NOK", Exponent: 2},
	"DKK": {Code: "DKK", Exponent: 2},
	"CHF": {Code: "CHF", Exponent: 2},
	"PLN": {Code: "PLN", Exponent: 2},
	"CZK": {Code: "CZK", Exponent: 2},
	"ISK": {Code: "ISK", Exponent: 0},

	// Middle East
	"AED": {Code: "AED", Exponent: 2},
	"SAR": {Code: "SAR", Exponent: 2},
	"QAR": {Code: "QAR", Exponent: 2},
	"ILS": {Code: "ILS", Exponent: 2},
	"BHD": {Code: "BHD", Exponent: 3},
	"KWD": {Code: "KWD", Exponent: 3},
	"OMR": {Code: "OMR", Exponent: 3},
	"JOD": {Code: "JOD", Exponent: 3},
	"TND": {Code: "TND", Exponent: 3},

	// Asia-Pacific
	"AUD": {Code: "AUD", Exponent: 2},
	"NZD": {Code: "NZD", Exponent: 2},
	"SGD": {Code: "SGD", Exponent: 2},
	"INR": {Code: "INR", Exponent: 2},
	"JPY": {Code: "JPY", Exponent: 0},
	"KRW": {Code: "KRW", Exponent: 0},

	// South America
	"BRL": {Code: "BRL", Exponent: 2},
	"CLP": {Code: "CLP", Exponent: 0},
}

// Lookup resolves an ISO-4217 code, case-insensitively.
//
// Codes are matched case-insensitively because producers are inconsistent about
// it and rejecting "usd" would be pedantry rather than validation.
func Lookup(code string) (Currency, bool) {
	c, ok := currencies[strings.ToUpper(strings.TrimSpace(code))]
	return c, ok
}

// Codes returns every recognised currency code. Order is unspecified.
func Codes() []string {
	codes := make([]string, 0, len(currencies))
	for code := range currencies {
		codes = append(codes, code)
	}
	return codes
}
