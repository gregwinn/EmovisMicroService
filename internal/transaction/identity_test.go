package transaction

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPlateCanonicalizes(t *testing.T) {
	tests := []struct {
		name                string
		number              string
		jurisdiction        string
		wantNumberKey       string
		wantJurisdictionKey string
	}{
		{
			name: "already canonical", number: "ABC1234", jurisdiction: "TX",
			wantNumberKey: "ABC1234", wantJurisdictionKey: "TX",
		},
		{
			name: "lower case", number: "abc1234", jurisdiction: "tx",
			wantNumberKey: "ABC1234", wantJurisdictionKey: "TX",
		},
		{
			name: "embedded space", number: "ABC 1234", jurisdiction: "TX",
			wantNumberKey: "ABC1234", wantJurisdictionKey: "TX",
		},
		{
			name: "hyphenated", number: "ABC-1234", jurisdiction: "TX",
			wantNumberKey: "ABC1234", wantJurisdictionKey: "TX",
		},
		{
			name: "surrounding whitespace", number: "  ABC1234  ", jurisdiction: " TX ",
			wantNumberKey: "ABC1234", wantJurisdictionKey: "TX",
		},
		{
			name: "mixed punctuation", number: "A.B-C 12/34", jurisdiction: "TX",
			wantNumberKey: "ABC1234", wantJurisdictionKey: "TX",
		},
		{
			name: "off-network jurisdiction is accepted as-is", number: "K-9924", jurisdiction: "mex-tam",
			wantNumberKey: "K9924", wantJurisdictionKey: "MEXTAM",
		},
		{
			name: "non-ascii noise from an ocr read is dropped", number: "AB©C1234", jurisdiction: "TX",
			wantNumberKey: "ABC1234", wantJurisdictionKey: "TX",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plate := NewPlate(tt.number, tt.jurisdiction)

			assert.Equal(t, tt.wantNumberKey, plate.NumberKey)
			assert.Equal(t, tt.wantJurisdictionKey, plate.JurisdictionKey)

			// The verbatim read is evidence and must survive untouched.
			assert.Equal(t, tt.number, plate.Number)
			assert.Equal(t, tt.jurisdiction, plate.Jurisdiction)
		})
	}
}

// Different equipment formats the same plate differently. Canonicalization
// exists so that downstream matching is not doing string archaeology.
func TestPlateVariantsCanonicalizeTogether(t *testing.T) {
	variants := []string{"ABC1234", "abc1234", "ABC-1234", "ABC 1234", " abc-1234 ", "a.b.c.1234"}

	want := NewPlate(variants[0], "TX").NumberKey
	for _, v := range variants {
		assert.Equal(t, want, NewPlate(v, "TX").NumberKey, "variant %q should canonicalize alike", v)
	}
}

func TestPlateIsEmpty(t *testing.T) {
	tests := []struct {
		name   string
		number string
		want   bool
	}{
		{name: "real plate", number: "ABC1234", want: false},
		{name: "empty string", number: "", want: true},
		{name: "whitespace only", number: "   ", want: true},
		{name: "punctuation only", number: "---", want: true},
		{name: "single character is usable", number: "7", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NewPlate(tt.number, "TX").IsEmpty())
		})
	}
}

func TestNewTransponderCanonicalizes(t *testing.T) {
	tests := []struct {
		name    string
		number  string
		wantKey string
	}{
		{name: "the contract's example", number: "0180012345678", wantKey: "0180012345678"},
		{name: "hyphenated", number: "01800-12345678", wantKey: "0180012345678"},
		{name: "spaced groups", number: "01800 1234 5678", wantKey: "0180012345678"},
		{name: "lower-case alphanumeric tag", number: "ez-1a2b3c", wantKey: "EZ1A2B3C"},
		{name: "surrounding whitespace", number: " 0180012345678 ", wantKey: "0180012345678"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transponder := NewTransponder(tt.number)

			assert.Equal(t, tt.wantKey, transponder.NumberKey)
			assert.Equal(t, tt.number, transponder.Number, "the verbatim read is preserved")
		})
	}
}

// Canonicalization stops short of stripping leading zeros on purpose.
//
// Case and punctuation carry no identity, so removing them is safe. Deciding
// that 0180012345678 and 180012345678 are the same tag is a claim about a
// specific agency's numbering plan; getting it wrong merges two vehicles'
// transactions and bills the wrong customer. That resolution belongs downstream,
// where the agency reference data lives.
func TestTransponderLeadingZerosArePreserved(t *testing.T) {
	withZero := NewTransponder("0180012345678")
	withoutZero := NewTransponder("180012345678")

	assert.NotEqual(t, withZero.NumberKey, withoutZero.NumberKey,
		"ingest must not decide that these are the same physical tag")
}

func TestTransponderIsEmpty(t *testing.T) {
	tests := []struct {
		name   string
		number string
		want   bool
	}{
		{name: "real tag", number: "0180012345678", want: false},
		{name: "empty string", number: "", want: true},
		{name: "whitespace only", number: " \t ", want: true},
		{name: "separators only", number: "--  --", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NewTransponder(tt.number).IsEmpty())
		})
	}
}

func TestCanonicalizeIsIdempotent(t *testing.T) {
	inputs := []string{"ABC-1234", "abc 1234", "0180012345678", "", "---", "K9924"}

	for _, in := range inputs {
		once := canonicalize(in)
		assert.Equal(t, once, canonicalize(once), "canonicalizing %q twice should not change it", in)
	}
}
