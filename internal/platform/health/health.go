// Package health tracks the liveness and readiness of a process and its
// dependencies.
//
// The distinction matters operationally: liveness answers "is this process
// wedged and in need of a restart?", readiness answers "should this instance be
// receiving traffic right now?". Restarting a healthy service because its
// database is briefly unreachable makes an outage worse, so the two never share
// an implementation.
package health

import (
	"context"
	"maps"
	"sync"
	"time"
)

// Status is the outcome of a readiness evaluation.
type Status string

const (
	// StatusUp means every registered check passed.
	StatusUp Status = "up"
	// StatusDown means at least one registered check failed.
	StatusDown Status = "down"
)

// Check reports whether a single dependency is usable. It must respect ctx and
// return promptly; probes run on a short timeout.
type Check func(ctx context.Context) error

// Result is the outcome of one named check.
type Result struct {
	Status  Status `json:"status"`
	Error   string `json:"error,omitempty"`
	Latency string `json:"latency"`
}

// Report is the aggregate readiness of the process.
type Report struct {
	Status Status            `json:"status"`
	Checks map[string]Result `json:"checks"`
}

// Checker holds the set of dependency checks for a process. The zero value is
// not usable; call New.
type Checker struct {
	mu     sync.RWMutex
	checks map[string]Check
	// timeout bounds each individual check.
	timeout time.Duration
}

// New returns a Checker that bounds every individual check by timeout.
func New(timeout time.Duration) *Checker {
	return &Checker{
		checks:  make(map[string]Check),
		timeout: timeout,
	}
}

// Register adds a named dependency check, replacing any previous check with the
// same name. It is safe to call concurrently with Ready.
func (c *Checker) Register(name string, check Check) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks[name] = check
}

// Ready runs every registered check and aggregates the results. Checks run
// concurrently: readiness latency should track the slowest dependency, not their
// sum.
func (c *Checker) Ready(ctx context.Context) Report {
	c.mu.RLock()
	checks := maps.Clone(c.checks)
	c.mu.RUnlock()

	report := Report{
		Status: StatusUp,
		Checks: make(map[string]Result, len(checks)),
	}

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for name, check := range checks {
		wg.Add(1)
		go func(name string, check Check) {
			defer wg.Done()

			checkCtx, cancel := context.WithTimeout(ctx, c.timeout)
			defer cancel()

			start := time.Now()
			err := check(checkCtx)
			result := Result{
				Status:  StatusUp,
				Latency: time.Since(start).String(),
			}
			if err != nil {
				result.Status = StatusDown
				result.Error = err.Error()
			}

			mu.Lock()
			defer mu.Unlock()
			report.Checks[name] = result
			if err != nil {
				report.Status = StatusDown
			}
		}(name, check)
	}
	wg.Wait()

	return report
}
