package transaction

import "strings"

// A transaction must be attributable to a vehicle, and the contract offers two
// ways to identify one: a license plate read and a transponder read. Either is
// sufficient; neither is guaranteed.
//
// Both arrive as free text from equipment that does not agree on formatting.
// The contract is explicit about this for transponders — "the same physical tag
// is legitimately reported in several textual forms by different equipment" —
// and about plates arriving "from anywhere, including off-network
// jurisdictions".
//
// So identifiers are stored twice: exactly as the producer sent them, and in a
// canonical form for downstream matching. The raw value is evidence and must
// survive untouched for disputes; the canonical value is a lookup key.

// Plate is a license plate read, as received and as canonicalized.
type Plate struct {
	// Number and Jurisdiction are verbatim, exactly as the producer sent them.
	Number       string
	Jurisdiction string

	// NumberKey and JurisdictionKey are canonical forms for matching. They are
	// never shown to a producer and never treated as authoritative.
	NumberKey       string
	JurisdictionKey string
}

// NewPlate canonicalizes a plate read.
//
// The jurisdiction is deliberately not validated against any list of known
// issuing authorities. The contract states plates arrive from anywhere,
// including off-network jurisdictions, so rejecting an unrecognized one would
// discard real revenue from exactly the interoperability traffic this endpoint
// exists to accept.
func NewPlate(number, jurisdiction string) Plate {
	return Plate{
		Number:          number,
		Jurisdiction:    jurisdiction,
		NumberKey:       canonicalize(number),
		JurisdictionKey: canonicalize(jurisdiction),
	}
}

// IsEmpty reports whether the plate carries no usable identifier. A plate whose
// number canonicalizes to nothing — punctuation only, say — cannot identify a
// vehicle even though the string was non-empty.
func (p Plate) IsEmpty() bool { return p.NumberKey == "" }

// Transponder is a transponder or tag read, as received and as canonicalized.
type Transponder struct {
	// Number is verbatim, exactly as the producer sent it.
	Number string
	// NumberKey is the canonical form for matching.
	NumberKey string
}

// NewTransponder canonicalizes a transponder read.
//
// Canonicalization stops at case and separators, and deliberately does not
// strip leading zeros or agency prefixes even though those are a known source
// of the "several textual forms" the contract mentions.
//
// Case and punctuation carry no identity: "01800-1234 5678" and
// "0180012345678" are unambiguously the same tag. Leading zeros are different —
// deciding that 0180012345678 and 180012345678 are one tag is a claim about the
// numbering plan of a specific agency, and getting it wrong merges two
// vehicles' transactions and bills the wrong customer.
//
// Resolving tag identity is the downstream pipeline's job, and it has the
// agency reference data to do it correctly. Ingest's job is to stop pushing
// avoidable string variation downstream, not to guess.
func NewTransponder(number string) Transponder {
	return Transponder{
		Number:    number,
		NumberKey: canonicalize(number),
	}
}

// IsEmpty reports whether the transponder carries no usable identifier.
func (t Transponder) IsEmpty() bool { return t.NumberKey == "" }

// canonicalize upper-cases and strips everything that is not a letter or digit.
//
// It works on bytes rather than runes because plate and transponder identifiers
// are ASCII alphanumerics in every scheme this service ingests, and any
// non-ASCII byte is noise from an OCR read rather than a meaningful character.
// Dropping it is the correct outcome either way.
func canonicalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := range len(s) {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z':
			b.WriteByte(c - ('a' - 'A'))
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
		}
	}

	return b.String()
}
