package transaction

import (
	"maps"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// accepted runs a submission through the real rules so fingerprint tests
// operate on transactions built the way production builds them.
func accepted(t *testing.T, mutate func(*Submission)) Transaction {
	t.Helper()

	s := validSubmission()
	if mutate != nil {
		mutate(&s)
	}

	tx, problems := testRules().Accept(s)
	require.Empty(t, problems)
	return tx
}

func TestFingerprintIsStable(t *testing.T) {
	first := accepted(t, nil).Fingerprint()

	for range 5 {
		assert.Equal(t, first, accepted(t, nil).Fingerprint(),
			"the same content must always fingerprint alike")
	}
}

func TestFingerprintIsHexSHA256(t *testing.T) {
	assert.Len(t, accepted(t, nil).Fingerprint(), 64)
	assert.Regexp(t, "^[0-9a-f]{64}$", accepted(t, nil).Fingerprint())
}

// Fields that differ per call must not participate, or every replay would look
// divergent.
func TestFingerprintIgnoresPerCallFields(t *testing.T) {
	base := accepted(t, nil)

	variants := map[string]func(Transaction) Transaction{
		"different id": func(tx Transaction) Transaction {
			tx.ID = uuid.MustParse("0198b1f0-0000-7000-8000-00000000dead")
			return tx
		},
		"different received time": func(tx Transaction) Transaction {
			tx.ReceivedAt = tx.ReceivedAt.Add(72 * time.Hour)
			return tx
		},
		"advanced association status": func(tx Transaction) Transaction {
			tx.AssociationStatus = AssociationAssociated
			return tx
		},
		"advanced settlement status": func(tx Transaction) Transaction {
			tx.SettlementStatus = SettlementPaid
			return tx
		},
	}

	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, base.Fingerprint(), mutate(base).Fingerprint())
		})
	}
}

// Any change to what is being billed must change the fingerprint. These are the
// differences a divergent replay is supposed to catch.
func TestFingerprintChangesWithBillableContent(t *testing.T) {
	base := accepted(t, nil).Fingerprint()

	variants := map[string]func(*Submission){
		"different amount":       func(s *Submission) { s.BaseAmount = "13.50" },
		"different currency":     func(s *Submission) { s.Currency = "GBP" },
		"different type":         func(s *Submission) { s.Type = "violation" },
		"different plate":        func(s *Submission) { s.Plate = &PlateRead{Number: "XYZ9876", Jurisdiction: "TX"} },
		"different jurisdiction": func(s *Submission) { s.Plate = &PlateRead{Number: "ABC1234", Jurisdiction: "OK"} },
		"added transponder":      func(s *Submission) { s.Transponder = "0180012345678" },
		"different occurrence":   func(s *Submission) { s.OccurredAt = fixedNow.Add(-16 * time.Minute) },
		"different source":       func(s *Submission) { s.Source = "lane-controller-08" },
		"different reference":    func(s *Submission) { s.SourceReference = "LC07-20260814-000919" },
		"added location":         func(s *Submission) { s.Location = map[string]any{"facility": "SH-130"} },
		"added metadata":         func(s *Submission) { s.Metadata = map[string]any{"lane_confidence": 0.9} },
	}

	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			assert.NotEqual(t, base, accepted(t, mutate).Fingerprint(),
				"a change to billable content must be detectable")
		})
	}
}

// A producer that tidies its serialisation between the original push and the
// retry has not changed the transaction. Hashing the raw request bytes would
// call all of these divergent and bury the real signal in false positives.
func TestFingerprintIgnoresCosmeticDifferences(t *testing.T) {
	base := accepted(t, nil).Fingerprint()

	variants := map[string]func(*Submission){
		// "12.5" and "12.50" are the same money. The fingerprint hashes the
		// canonical decimal, not the received text, so a producer tidying its
		// formatting does not read as a change.
		"amount without the trailing zero":                 func(s *Submission) { s.BaseAmount = "12.5" },
		"currency stated explicitly rather than defaulted": func(s *Submission) { s.Currency = "USD" },
		"currency in lower case":                           func(s *Submission) { s.Currency = "usd" },
		"type in a different case":                         func(s *Submission) { s.Type = "TOLL" },
		"timestamp in a different zone": func(s *Submission) {
			s.OccurredAt = s.OccurredAt.In(time.FixedZone("CDT", -5*3600))
		},
	}

	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, base, accepted(t, mutate).Fingerprint(),
				"a cosmetic difference must not read as a divergent replay")
		})
	}
}

// Map iteration order is randomised in Go, so free-form passthrough fields have
// to hash deterministically or the fingerprint would be useless.
func TestFingerprintIsDeterministicForFreeFormMaps(t *testing.T) {
	location := map[string]any{"facility": "SH-130", "plaza": "12", "lane": "3", "direction": "NB"}
	metadata := map[string]any{"z": 1, "a": 2, "m": map[string]any{"nested": true, "another": "value"}}

	mutate := func(s *Submission) {
		// Fresh maps each call, so insertion order differs between runs.
		s.Location = maps.Clone(location)
		s.Metadata = maps.Clone(metadata)
	}

	first := accepted(t, mutate).Fingerprint()
	for range 20 {
		assert.Equal(t, first, accepted(t, mutate).Fingerprint())
	}
}

// The canonical key and the raw read differ here: both plates canonicalize to
// ABC1234, but the evidence a producer sent has changed, and that is worth
// noticing.
func TestFingerprintUsesRawReadsNotCanonicalKeys(t *testing.T) {
	withHyphen := accepted(t, func(s *Submission) {
		s.Plate = &PlateRead{Number: "ABC-1234", Jurisdiction: "TX"}
	})
	withoutHyphen := accepted(t, func(s *Submission) {
		s.Plate = &PlateRead{Number: "ABC1234", Jurisdiction: "TX"}
	})

	require.Equal(t, withHyphen.Plate.NumberKey, withoutHyphen.Plate.NumberKey,
		"precondition: these canonicalize alike")
	assert.NotEqual(t, withHyphen.Fingerprint(), withoutHyphen.Fingerprint(),
		"but the evidence differs, so the fingerprint should too")
}
