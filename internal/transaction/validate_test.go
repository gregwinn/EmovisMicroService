package transaction

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gregwinn/EmovisMicroService/internal/money"
)

// fixedNow is the clock every test runs against, so "in the future" and "well
// in the past" mean something stable.
var fixedNow = time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC)

func testRules() Rules {
	usd, _ := money.Lookup("USD")
	return Rules{
		Types:           NewTypeSet([]string{"toll", "violation", "fee"}),
		DefaultCurrency: usd,
		MaxClockSkew:    DefaultMaxClockSkew,
		Now:             func() time.Time { return fixedNow },
		NewID:           func() (uuid.UUID, error) { return uuid.MustParse("0198b1f0-0000-7000-8000-000000000001"), nil },
	}
}

func validSubmission() Submission {
	return Submission{
		Source:          "lane-controller-07",
		SourceReference: "LC07-20260814-000918",
		Type:            "toll",
		OccurredAt:      fixedNow.Add(-15 * time.Minute),
		Plate:           &PlateRead{Number: "ABC1234", Jurisdiction: "TX"},
		BaseAmount:      "12.50",
	}
}

// reasonFor returns the reason reported for a field, or "" if that field was
// not faulted.
func reasonFor(problems []RuleError, field string) string {
	for _, p := range problems {
		if p.Field == field {
			return p.Reason
		}
	}
	return ""
}

func TestAcceptValidSubmission(t *testing.T) {
	tx, problems, err := testRules().Accept(validSubmission())
	require.NoError(t, err)

	require.Empty(t, problems)

	assert.Equal(t, "lane-controller-07", tx.Source)
	assert.Equal(t, "LC07-20260814-000918", tx.SourceReference)
	assert.Equal(t, "toll", tx.Type)
	assert.Equal(t, fixedNow.Add(-15*time.Minute), tx.OccurredAt)
	assert.Equal(t, fixedNow, tx.ReceivedAt)
	assert.Equal(t, "12.50 USD", tx.BaseAmount.String())
	assert.Equal(t, "12.50", tx.BaseAmount.AsReceived())

	require.NotNil(t, tx.Plate)
	assert.Equal(t, "ABC1234", tx.Plate.NumberKey)
	assert.Nil(t, tx.Transponder)

	// Every freshly ingested transaction starts received/priced.
	assert.Equal(t, AssociationReceived, tx.AssociationStatus)
	assert.Equal(t, SettlementPriced, tx.SettlementStatus)
}

// Either identifier alone is sufficient; neither is not.
func TestIdentityRule(t *testing.T) {
	tests := []struct {
		name        string
		plate       *PlateRead
		transponder string
		wantAccept  bool
	}{
		{name: "plate only", plate: &PlateRead{Number: "ABC1234", Jurisdiction: "TX"}, wantAccept: true},
		{name: "transponder only", transponder: "0180012345678", wantAccept: true},
		{
			name:        "both",
			plate:       &PlateRead{Number: "ABC1234", Jurisdiction: "TX"},
			transponder: "0180012345678",
			wantAccept:  true,
		},
		{name: "neither", wantAccept: false},
		{
			name:       "plate present but unusable",
			plate:      &PlateRead{Number: "---", Jurisdiction: "TX"},
			wantAccept: false,
		},
		{name: "transponder present but unusable", transponder: "  -- ", wantAccept: false},
		{
			name:        "unusable plate rescued by a real transponder",
			plate:       &PlateRead{Number: "---", Jurisdiction: "TX"},
			transponder: "0180012345678",
			wantAccept:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSubmission()
			s.Plate = tt.plate
			s.Transponder = tt.transponder

			_, problems, err := testRules().Accept(s)
			require.NoError(t, err)

			if tt.wantAccept {
				assert.Empty(t, problems)
				return
			}

			require.Len(t, problems, 1)
			assert.Contains(t, problems[0].Reason, "at least one usable identifier")
			assert.Empty(t, problems[0].Field, "the failure spans two fields, so it names neither")
		})
	}
}

func TestTransactionTypeRule(t *testing.T) {
	tests := []struct {
		name          string
		submitted     string
		wantAccept    bool
		wantCanonical string
	}{
		{name: "configured type", submitted: "toll", wantAccept: true, wantCanonical: "toll"},
		{name: "another configured type", submitted: "violation", wantAccept: true, wantCanonical: "violation"},
		{name: "upper case matches", submitted: "TOLL", wantAccept: true, wantCanonical: "toll"},
		{name: "mixed case matches", submitted: "Toll", wantAccept: true, wantCanonical: "toll"},
		{name: "surrounding whitespace matches", submitted: "  toll ", wantAccept: true, wantCanonical: "toll"},
		{name: "unconfigured type", submitted: "parking", wantAccept: false},
		{name: "empty", submitted: "", wantAccept: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSubmission()
			s.Type = tt.submitted

			tx, problems, err := testRules().Accept(s)
			require.NoError(t, err)

			if tt.wantAccept {
				require.Empty(t, problems)
				assert.Equal(t, tt.wantCanonical, tx.Type,
					"the operator's spelling is what gets stored")
				return
			}

			assert.Contains(t, reasonFor(problems, "transaction_type"), "unrecognized value")
		})
	}
}

// The accepted set is operator configuration. Echoing it back would turn an
// unauthenticated endpoint into a configuration disclosure.
func TestUnknownTypeDoesNotLeakTheAcceptedSet(t *testing.T) {
	s := validSubmission()
	s.Type = "parking"

	_, problems, err := testRules().Accept(s)
	require.NoError(t, err)

	reason := reasonFor(problems, "transaction_type")
	require.NotEmpty(t, reason)
	assert.NotContains(t, reason, "violation")
	assert.NotContains(t, reason, "fee")
}

// Types are runtime configuration, so replacing the set changes what the same
// submission does — without a redeploy.
func TestTypeSetIsRuntimeConfigurable(t *testing.T) {
	rules := testRules()

	s := validSubmission()
	s.Type = "parking"

	_, problems, err := rules.Accept(s)
	require.NoError(t, err)
	require.NotEmpty(t, problems, "not configured yet")

	rules.Types.Replace([]string{"toll", "parking"})

	_, problems, err = rules.Accept(s)
	require.NoError(t, err)
	assert.Empty(t, problems, "accepted after the operator added it")
}

func TestTimeRule(t *testing.T) {
	tests := []struct {
		name       string
		occurredAt time.Time
		wantAccept bool
		wantReason string
	}{
		{name: "just now", occurredAt: fixedNow, wantAccept: true},
		{name: "minutes ago", occurredAt: fixedNow.Add(-30 * time.Minute), wantAccept: true},
		// The contract says a transaction may be "well in the past" for a batch
		// or image-review replay. There is no posting window.
		{name: "days ago, image review", occurredAt: fixedNow.AddDate(0, 0, -30), wantAccept: true},
		{name: "a year ago, batch replay", occurredAt: fixedNow.AddDate(-1, 0, 0), wantAccept: true},
		{name: "a decade ago", occurredAt: fixedNow.AddDate(-10, 0, 0), wantAccept: true},
		// A small future stamp is clock drift on roadside equipment, not fraud.
		{name: "one minute ahead", occurredAt: fixedNow.Add(time.Minute), wantAccept: true},
		{name: "at the skew boundary", occurredAt: fixedNow.Add(DefaultMaxClockSkew), wantAccept: true},
		{
			name:       "beyond the skew boundary",
			occurredAt: fixedNow.Add(DefaultMaxClockSkew + time.Second),
			wantAccept: false,
			wantReason: "in the future",
		},
		{name: "far future", occurredAt: fixedNow.AddDate(1, 0, 0), wantAccept: false, wantReason: "in the future"},
		{name: "zero value", occurredAt: time.Time{}, wantAccept: false, wantReason: "is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSubmission()
			s.OccurredAt = tt.occurredAt

			_, problems, err := testRules().Accept(s)
			require.NoError(t, err)

			if tt.wantAccept {
				assert.Empty(t, problems)
				return
			}
			assert.Contains(t, reasonFor(problems, "transaction_time_utc"), tt.wantReason)
		})
	}
}

// The field name asserts UTC. Any valid offset is accepted and normalized, so
// downstream comparisons never have to think about zones.
func TestTimeIsNormalizedToUTC(t *testing.T) {
	central := time.FixedZone("CDT", -5*3600)

	s := validSubmission()
	s.OccurredAt = time.Date(2026, 8, 14, 8, 45, 2, 0, central)

	tx, problems, err := testRules().Accept(s)
	require.NoError(t, err)

	require.Empty(t, problems)
	assert.Equal(t, time.UTC, tx.OccurredAt.Location())
	assert.Equal(t, time.Date(2026, 8, 14, 13, 45, 2, 0, time.UTC), tx.OccurredAt)
}

func TestAmountRule(t *testing.T) {
	tests := []struct {
		name       string
		amount     string
		wantAccept bool
		wantReason string
	}{
		{name: "the contract's example", amount: "12.50", wantAccept: true},
		{name: "whole number", amount: "12", wantAccept: true},
		{name: "free passage", amount: "0.00", wantAccept: true},
		{name: "negative", amount: "-1.00", wantAccept: false, wantReason: "must not be negative"},
		{name: "empty", amount: "", wantAccept: false, wantReason: "is required"},
		{name: "not a number", amount: "twelve fifty", wantAccept: false, wantReason: "decimal string"},
		{name: "exponent notation", amount: "1e5", wantAccept: false, wantReason: "decimal string"},
		{name: "currency symbol", amount: "$12.50", wantAccept: false, wantReason: "decimal string"},
		{name: "too many decimal places", amount: "12.505", wantAccept: false, wantReason: "decimal places"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSubmission()
			s.BaseAmount = tt.amount

			_, problems, err := testRules().Accept(s)
			require.NoError(t, err)

			if tt.wantAccept {
				assert.Empty(t, problems)
				return
			}
			assert.Contains(t, reasonFor(problems, "base_amount"), tt.wantReason)
		})
	}
}

// A zero toll is legitimate — exempt vehicles, promotional periods, free
// segments — and must not be confused with a missing amount.
func TestZeroAmountIsAccepted(t *testing.T) {
	s := validSubmission()
	s.BaseAmount = "0.00"

	tx, problems, err := testRules().Accept(s)
	require.NoError(t, err)

	require.Empty(t, problems)
	assert.True(t, tx.BaseAmount.IsZero())
}

func TestCurrencyRule(t *testing.T) {
	tests := []struct {
		name         string
		currency     string
		wantAccept   bool
		wantResolved string
	}{
		{name: "omitted uses the deployment default", currency: "", wantAccept: true, wantResolved: "USD"},
		{name: "explicit match", currency: "USD", wantAccept: true, wantResolved: "USD"},
		{name: "explicit other currency", currency: "GBP", wantAccept: true, wantResolved: "GBP"},
		{name: "lower case", currency: "gbp", wantAccept: true, wantResolved: "GBP"},
		{name: "unknown code", currency: "XYZ", wantAccept: false},
		{name: "numeric code", currency: "840", wantAccept: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSubmission()
			s.Currency = tt.currency

			tx, problems, err := testRules().Accept(s)
			require.NoError(t, err)

			if tt.wantAccept {
				require.Empty(t, problems)
				assert.Equal(t, tt.wantResolved, tx.BaseAmount.Currency().Code)
				return
			}
			assert.Contains(t, reasonFor(problems, "currency"), "unrecognized ISO-4217 code")
		})
	}
}

// Precision is checked against the resolved currency, not a hard-coded two
// places.
func TestAmountPrecisionFollowsTheResolvedCurrency(t *testing.T) {
	s := validSubmission()
	s.Currency = "JPY"
	s.BaseAmount = "350.5"

	_, problems, err := testRules().Accept(s)
	require.NoError(t, err)

	assert.Contains(t, reasonFor(problems, "base_amount"), "0 decimal places")
	assert.Contains(t, reasonFor(problems, "base_amount"), "JPY")
}

// Reporting "wrong number of decimal places for XYZ" against a currency that
// does not exist is noise, so the amount check is skipped when the currency is
// the thing at fault.
func TestAmountIsNotCheckedAgainstAnInvalidCurrency(t *testing.T) {
	s := validSubmission()
	s.Currency = "XYZ"
	s.BaseAmount = "12.505"

	_, problems, err := testRules().Accept(s)
	require.NoError(t, err)

	require.Len(t, problems, 1)
	assert.Equal(t, "currency", problems[0].Field)
}

func TestMissingDefaultCurrencyIsAConfigurationError(t *testing.T) {
	rules := testRules()
	rules.DefaultCurrency = money.Currency{}

	s := validSubmission()
	s.Currency = ""

	_, problems, err := rules.Accept(s)
	require.NoError(t, err)

	assert.Contains(t, reasonFor(problems, "currency"), "no default currency configured")
}

// Like the contract validator, semantic validation reports everything at once.
func TestAllRuleFailuresAreReportedTogether(t *testing.T) {
	_, problems, err := testRules().Accept(Submission{
		Source:          "lane-controller-07",
		SourceReference: "LC07-1",
		Type:            "parking",                 // unrecognized
		OccurredAt:      fixedNow.AddDate(1, 0, 0), // far future
		BaseAmount:      "not-a-number",            // unparseable
		// no plate, no transponder                    // no identifier
	})
	require.NoError(t, err)

	require.Len(t, problems, 4)
	assert.NotEmpty(t, reasonFor(problems, "transaction_type"))
	assert.NotEmpty(t, reasonFor(problems, "transaction_time_utc"))
	assert.NotEmpty(t, reasonFor(problems, "base_amount"))
	assert.NotEmpty(t, reasonFor(problems, ""))
}

func TestFreeFormFieldsArePassedThroughUntouched(t *testing.T) {
	location := map[string]any{"facility": "SH-130", "plaza": "12", "lane": "3", "direction": "NB"}
	metadata := map[string]any{"vendor": map[string]any{"confidence": 0.94}, "images": []any{"a", "b"}}

	s := validSubmission()
	s.Location = location
	s.Metadata = metadata

	tx, problems, err := testRules().Accept(s)
	require.NoError(t, err)

	require.Empty(t, problems)
	assert.Equal(t, location, tx.Location, "location is producer passthrough")
	assert.Equal(t, metadata, tx.Metadata, "metadata is preserved verbatim and not interpreted")
}

func TestUnconfiguredTypeSetRejectsEverything(t *testing.T) {
	rules := testRules()
	rules.Types = nil

	_, problems, err := rules.Accept(validSubmission())
	require.NoError(t, err)

	assert.Contains(t, reasonFor(problems, "transaction_type"), "no transaction types are configured")
}

// An infrastructure fault is not a bad request. It comes back on the error
// channel, never as a rule failure, so the caller answers 500 rather than
// telling a producer to fix a payload that was never the problem.
func TestIDGenerationFailureIsAnInfrastructureError(t *testing.T) {
	entropyFailure := errors.New("entropy source unavailable")

	rules := testRules()
	rules.NewID = func() (uuid.UUID, error) { return uuid.Nil, entropyFailure }

	_, problems, err := rules.Accept(validSubmission())

	require.Error(t, err)
	assert.ErrorIs(t, err, entropyFailure)
	assert.Empty(t, problems, "the producer's payload was valid; do not blame them")
}

// The converse: a rule failure is never an infrastructure error.
func TestRuleFailuresDoNotSurfaceAsErrors(t *testing.T) {
	s := validSubmission()
	s.BaseAmount = "not-a-number"

	_, problems, err := testRules().Accept(s)

	require.NoError(t, err)
	assert.NotEmpty(t, problems)
}

// Rules with no Now or NewID injected must still work; the defaults are the
// real clock and a real UUIDv7.
func TestRulesFallBackToRealClockAndIDs(t *testing.T) {
	usd, _ := money.Lookup("USD")
	rules := Rules{
		Types:           NewTypeSet([]string{"toll"}),
		DefaultCurrency: usd,
	}

	before := time.Now().UTC()
	tx, problems, err := rules.Accept(validSubmission())
	require.NoError(t, err)

	require.Empty(t, problems)
	assert.NotEqual(t, uuid.Nil, tx.ID)
	assert.Equal(t, uuid.Version(7), tx.ID.Version())
	assert.False(t, tx.ReceivedAt.Before(before))
}

func TestRuleErrorMessage(t *testing.T) {
	assert.Equal(t, "base_amount: must not be negative",
		RuleError{Field: "base_amount", Reason: "must not be negative"}.Error())
	assert.Equal(t, "at least one identifier is required",
		RuleError{Reason: "at least one identifier is required"}.Error())
}
