package transaction

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHasPlate(t *testing.T) {
	tests := []struct {
		name  string
		plate *Plate
		want  bool
	}{
		{name: "absent", plate: nil, want: false},
		{name: "usable", plate: ptr(NewPlate("ABC1234", "TX")), want: true},
		{name: "present but unusable", plate: ptr(NewPlate("--", "TX")), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Transaction{Plate: tt.plate}.HasPlate())
		})
	}
}

func TestHasTransponder(t *testing.T) {
	tests := []struct {
		name        string
		transponder *Transponder
		want        bool
	}{
		{name: "absent", transponder: nil, want: false},
		{name: "usable", transponder: ptr(NewTransponder("0180012345678")), want: true},
		{name: "present but unusable", transponder: ptr(NewTransponder("  ")), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Transaction{Transponder: tt.transponder}.HasTransponder())
		})
	}
}

// Submission lag is a producer health signal, not an error. A lane controller
// reports in seconds; an image-review vendor legitimately reports in days.
func TestSubmissionLag(t *testing.T) {
	occurred := time.Date(2026, 8, 14, 13, 45, 2, 0, time.UTC)

	tests := []struct {
		name     string
		received time.Time
		want     time.Duration
	}{
		{name: "lane controller, near real time", received: occurred.Add(2 * time.Second), want: 2 * time.Second},
		{name: "image review, days later", received: occurred.Add(72 * time.Hour), want: 72 * time.Hour},
		{name: "batch replay, months later", received: occurred.Add(90 * 24 * time.Hour), want: 90 * 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := Transaction{OccurredAt: occurred, ReceivedAt: tt.received}
			assert.Equal(t, tt.want, tx.SubmissionLag())
		})
	}
}

// The two axes are independent. A transaction can be priced but unattributed,
// which is the ordinary video-tolling case, or attributed but unpaid.
func TestStatusAxesAreOrthogonal(t *testing.T) {
	unattributedButPriced := Transaction{
		AssociationStatus: AssociationReceived,
		SettlementStatus:  SettlementPriced,
	}
	attributedButUnpaid := Transaction{
		AssociationStatus: AssociationAssociated,
		SettlementStatus:  SettlementPayable,
	}

	assert.NotEqual(t, unattributedButPriced.AssociationStatus, attributedButUnpaid.AssociationStatus)
	assert.NotEqual(t, unattributedButPriced.SettlementStatus, attributedButUnpaid.SettlementStatus)

	// Both are legitimate states, which is precisely why a single status enum
	// would be the wrong model.
	assert.Equal(t, AssociationReceived, unattributedButPriced.AssociationStatus)
	assert.Equal(t, SettlementPriced, unattributedButPriced.SettlementStatus)
}

// The constants must match the contract's enums exactly; a producer reads these
// strings off the wire.
func TestStatusValuesMatchTheContract(t *testing.T) {
	assert.Equal(t, AssociationStatus("received"), AssociationReceived)
	assert.Equal(t, AssociationStatus("resolving"), AssociationResolving)
	assert.Equal(t, AssociationStatus("associated"), AssociationAssociated)
	assert.Equal(t, AssociationStatus("exception"), AssociationException)

	assert.Equal(t, SettlementStatus("unpriced"), SettlementUnpriced)
	assert.Equal(t, SettlementStatus("priced"), SettlementPriced)
	assert.Equal(t, SettlementStatus("payable"), SettlementPayable)
	assert.Equal(t, SettlementStatus("paid"), SettlementPaid)
}

func ptr[T any](v T) *T { return &v }
