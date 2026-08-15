package outbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Backoff is unexported and worth testing directly: an overflow here would
// either hammer a struggling broker or park an event for a century.
func TestBackoffFor(t *testing.T) {
	r := &Relay{opts: RelayOptions{
		BaseBackoff: time.Second,
		MaxBackoff:  time.Minute,
	}}

	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: 1, want: time.Second},
		{attempts: 2, want: 2 * time.Second},
		{attempts: 3, want: 4 * time.Second},
		{attempts: 4, want: 8 * time.Second},
		{attempts: 5, want: 16 * time.Second},
		{attempts: 6, want: 32 * time.Second},
		// Capped from here on.
		{attempts: 7, want: time.Minute},
		{attempts: 20, want: time.Minute},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, r.backoffFor(tt.attempts),
			"attempt %d", tt.attempts)
	}
}

// Doubling with a shift or an unguarded multiply overflows into a negative
// duration at high attempt counts, which would make an event immediately due
// forever. The cap has to hold.
func TestBackoffDoesNotOverflow(t *testing.T) {
	r := &Relay{opts: RelayOptions{
		BaseBackoff: time.Hour,
		MaxBackoff:  24 * time.Hour,
	}}

	for _, attempts := range []int{60, 100, 1000, 100000} {
		got := r.backoffFor(attempts)

		assert.Positive(t, got, "attempt %d produced a non-positive backoff", attempts)
		assert.LessOrEqual(t, got, 24*time.Hour, "attempt %d exceeded the cap", attempts)
	}
}

func TestBackoffTreatsZeroAndNegativeAsFirstAttempt(t *testing.T) {
	r := &Relay{opts: RelayOptions{BaseBackoff: time.Second, MaxBackoff: time.Minute}}

	assert.Equal(t, time.Second, r.backoffFor(0))
	assert.Equal(t, time.Second, r.backoffFor(-1))
}

// Zero-valued options must fall back to the defaults rather than producing a
// relay that spins with a zero poll interval or claims zero rows per pass.
func TestNewRelayFillsInDefaults(t *testing.T) {
	r := NewRelay(nil, nil, nil, RelayOptions{})

	defaults := DefaultRelayOptions()
	assert.Equal(t, defaults.BatchSize, r.opts.BatchSize)
	assert.Equal(t, defaults.PollInterval, r.opts.PollInterval)
	assert.Equal(t, defaults.BaseBackoff, r.opts.BaseBackoff)
	assert.Equal(t, defaults.MaxBackoff, r.opts.MaxBackoff)
	assert.Equal(t, defaults.MaxAttempts, r.opts.MaxAttempts)
}

func TestNewRelayKeepsExplicitOptions(t *testing.T) {
	r := NewRelay(nil, nil, nil, RelayOptions{
		BatchSize:    7,
		PollInterval: 3 * time.Second,
		BaseBackoff:  250 * time.Millisecond,
		MaxBackoff:   time.Minute,
		MaxAttempts:  2,
	})

	assert.Equal(t, 7, r.opts.BatchSize)
	assert.Equal(t, 3*time.Second, r.opts.PollInterval)
	assert.Equal(t, 250*time.Millisecond, r.opts.BaseBackoff)
	assert.Equal(t, time.Minute, r.opts.MaxBackoff)
	assert.Equal(t, 2, r.opts.MaxAttempts)
}
