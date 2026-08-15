package transaction

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTypeSetCanonical(t *testing.T) {
	set := NewTypeSet([]string{"toll", "Violation", "  fee  "})

	tests := []struct {
		name          string
		submitted     string
		wantOK        bool
		wantCanonical string
	}{
		{name: "exact", submitted: "toll", wantOK: true, wantCanonical: "toll"},
		{name: "upper case", submitted: "TOLL", wantOK: true, wantCanonical: "toll"},
		{name: "mixed case", submitted: "ToLl", wantOK: true, wantCanonical: "toll"},
		{name: "surrounding whitespace", submitted: "  toll  ", wantOK: true, wantCanonical: "toll"},
		{
			name:      "operator's own capitalisation is what is stored",
			submitted: "violation", wantOK: true, wantCanonical: "Violation",
		},
		{name: "configured value is trimmed", submitted: "fee", wantOK: true, wantCanonical: "fee"},
		{name: "unconfigured", submitted: "parking", wantOK: false},
		{name: "empty", submitted: "", wantOK: false},
		{name: "whitespace only", submitted: "   ", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			canonical, ok := set.Canonical(tt.submitted)

			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantCanonical, canonical)
			}
		})
	}
}

// The zero value must be safe and must reject everything, so a TypeSet that was
// never configured fails closed rather than accepting anything.
func TestZeroTypeSetRejectsEverything(t *testing.T) {
	var set TypeSet

	_, ok := set.Canonical("toll")

	assert.False(t, ok)
	assert.Zero(t, set.Len())
	assert.Empty(t, set.All())
}

func TestTypeSetIgnoresBlankEntries(t *testing.T) {
	set := NewTypeSet([]string{"toll", "", "   ", "fee"})

	assert.Equal(t, 2, set.Len())
	assert.Equal(t, []string{"fee", "toll"}, set.All())
}

// Entries that differ only by case collapse to one type.
func TestTypeSetDeduplicatesByKey(t *testing.T) {
	set := NewTypeSet([]string{"toll", "TOLL", "Toll"})

	assert.Equal(t, 1, set.Len())

	_, ok := set.Canonical("toll")
	assert.True(t, ok)
}

func TestTypeSetAllIsSorted(t *testing.T) {
	set := NewTypeSet([]string{"violation", "toll", "fee"})

	assert.Equal(t, []string{"fee", "toll", "violation"}, set.All())
}

// Adding a billable event type must not require a redeploy — the contract says
// the accepted values are operator-configurable at runtime.
func TestTypeSetReplace(t *testing.T) {
	set := NewTypeSet([]string{"toll"})

	_, ok := set.Canonical("parking")
	require.False(t, ok)

	set.Replace([]string{"toll", "parking"})

	_, ok = set.Canonical("parking")
	assert.True(t, ok)

	// Replace is a swap, not a merge: a type the operator removed stops being
	// accepted.
	set.Replace([]string{"parking"})

	_, ok = set.Canonical("toll")
	assert.False(t, ok)
}

// Reference data is refreshed on a timer while requests are in flight, so the
// race detector needs to be satisfied here.
func TestTypeSetIsSafeUnderConcurrentReplaceAndRead(t *testing.T) {
	set := NewTypeSet([]string{"toll"})

	var wg sync.WaitGroup

	wg.Go(func() {
		for i := range 200 {
			set.Replace([]string{"toll", "type-" + strconv.Itoa(i)})
		}
	})

	for range 4 {
		wg.Go(func() {
			for range 200 {
				_, _ = set.Canonical("toll")
				_ = set.Len()
				_ = set.All()
			}
		})
	}

	wg.Wait()

	// "toll" is in every generation, so it must still resolve afterwards.
	_, ok := set.Canonical("toll")
	assert.True(t, ok)
}
