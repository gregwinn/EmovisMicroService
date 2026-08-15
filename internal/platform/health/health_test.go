package health

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadyWithNoChecksIsUp(t *testing.T) {
	report := New(time.Second).Ready(t.Context())

	assert.Equal(t, StatusUp, report.Status)
	assert.Empty(t, report.Checks)
}

func TestReadyAllChecksPass(t *testing.T) {
	c := New(time.Second)
	c.Register("database", func(context.Context) error { return nil })
	c.Register("queue", func(context.Context) error { return nil })

	report := c.Ready(t.Context())

	assert.Equal(t, StatusUp, report.Status)
	require.Len(t, report.Checks, 2)
	assert.Equal(t, StatusUp, report.Checks["database"].Status)
	assert.Empty(t, report.Checks["database"].Error)
}

func TestReadyOneFailingCheckFailsTheReport(t *testing.T) {
	c := New(time.Second)
	c.Register("database", func(context.Context) error { return nil })
	c.Register("queue", func(context.Context) error { return errors.New("connection refused") })

	report := c.Ready(t.Context())

	assert.Equal(t, StatusDown, report.Status)
	assert.Equal(t, StatusUp, report.Checks["database"].Status)
	assert.Equal(t, StatusDown, report.Checks["queue"].Status)
	assert.Equal(t, "connection refused", report.Checks["queue"].Error)
}

// A dependency that hangs must not hang the probe. Without a per-check timeout,
// one stuck connection pool takes the whole instance out of rotation slowly
// instead of quickly.
func TestReadyBoundsEachCheckByTimeout(t *testing.T) {
	c := New(20 * time.Millisecond)
	c.Register("slow", func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return nil
		}
	})

	start := time.Now()
	report := c.Ready(t.Context())

	assert.Equal(t, StatusDown, report.Status)
	assert.Less(t, time.Since(start), time.Second, "probe should return as soon as the check times out")
	assert.Contains(t, report.Checks["slow"].Error, "deadline exceeded")
}

// Checks run concurrently, so total probe latency tracks the slowest dependency
// rather than the sum of all of them.
func TestReadyRunsChecksConcurrently(t *testing.T) {
	const (
		checks    = 8
		checkTime = 40 * time.Millisecond
	)

	c := New(time.Second)
	for i := range checks {
		c.Register("dep-"+strconv.Itoa(i), func(context.Context) error {
			time.Sleep(checkTime)
			return nil
		})
	}

	start := time.Now()
	report := c.Ready(t.Context())
	elapsed := time.Since(start)

	assert.Equal(t, StatusUp, report.Status)
	assert.Len(t, report.Checks, checks)
	assert.Less(t, elapsed, checkTime*checks/2, "checks should overlap, not run in series")
}

func TestRegisterReplacesByName(t *testing.T) {
	c := New(time.Second)
	c.Register("database", func(context.Context) error { return errors.New("stale") })
	c.Register("database", func(context.Context) error { return nil })

	report := c.Ready(t.Context())

	assert.Equal(t, StatusUp, report.Status)
	assert.Len(t, report.Checks, 1)
}

// Registration happens at startup but can also happen while probes are in
// flight; the race detector makes this test meaningful.
func TestRegisterIsSafeDuringReady(t *testing.T) {
	c := New(time.Second)
	var calls atomic.Int64

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 50 {
			c.Register("dep-"+strconv.Itoa(i), func(context.Context) error {
				calls.Add(1)
				return nil
			})
		}
	}()

	for range 50 {
		_ = c.Ready(t.Context())
	}
	<-done

	assert.Equal(t, StatusUp, c.Ready(t.Context()).Status)
}
