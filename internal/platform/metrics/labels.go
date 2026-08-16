package metrics

import "sync"

// `source` is producer-supplied and reaches Prometheus as a label value. On an
// endpoint the contract declares unauthenticated, that is an unbounded
// cardinality hazard: every distinct value creates a new time series that lives
// for the process's lifetime, in this service's memory and again in whatever
// scrapes it.
//
// Measured before this existed: 300 requests carrying 300 distinct `source`
// values produced 302 series. A few million requests would exhaust the
// service and take the monitoring backend with it — and monitoring is the
// thing you need most while under attack.
//
// A real deployment has a handful of producers. Once the tracked set is full,
// further values collapse into a single bucket: the aggregate stays correct,
// per-producer breakdown is preserved for the producers that matter, and the
// series count has a ceiling.

const (
	// maxTrackedSources is the ceiling on distinct `source` label values.
	// Generous next to any plausible producer count, small enough that the
	// series count can never become a memory problem.
	maxTrackedSources = 200

	// maxSourceLabelLength truncates an over-long value. The contract caps
	// `source` at 64 characters, but the label must stay bounded even if a
	// caller reaches this code before contract validation has run.
	maxSourceLabelLength = 64

	// sourceOverflow is where values beyond the cap are counted.
	sourceOverflow = "other"

	// sourceUnknown is used when the producer is not yet known — a request
	// rejected by contract validation has no trustworthy `source`.
	sourceUnknown = "unknown"
)

// sourceLabels bounds the set of `source` values that become label values.
type sourceLabels struct {
	mu    sync.RWMutex
	seen  map[string]struct{}
	limit int
}

func newSourceLabels(limit int) *sourceLabels {
	return &sourceLabels{seen: make(map[string]struct{}), limit: limit}
}

// label returns the label value to use for a producer-supplied source.
func (s *sourceLabels) label(source string) string {
	if source == "" {
		return sourceUnknown
	}
	if len(source) > maxSourceLabelLength {
		source = source[:maxSourceLabelLength]
	}

	// Fast path: already tracked. This is every request in a healthy
	// deployment, so it takes a read lock only.
	s.mu.RLock()
	_, known := s.seen[source]
	full := len(s.seen) >= s.limit
	s.mu.RUnlock()

	if known {
		return source
	}
	if full {
		return sourceOverflow
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Re-check under the write lock: another goroutine may have added this
	// value, or filled the last slot, between the two locks.
	if _, known := s.seen[source]; known {
		return source
	}
	if len(s.seen) >= s.limit {
		return sourceOverflow
	}

	s.seen[source] = struct{}{}
	return source
}

// tracked reports how many distinct sources are being labelled individually.
func (s *sourceLabels) tracked() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.seen)
}
