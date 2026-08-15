package transaction

import (
	"maps"
	"slices"
	"strings"
	"sync"
)

// TypeSet is the set of transaction types an operator currently accepts.
//
// The contract is unusually direct about this:
//
//	The kind of billable event. The set of accepted values is
//	operator-configurable at runtime, not compiled in.
//
// So this is not a Go enum and not an OpenAPI enum. Adding a new billable event
// type must not require a redeploy. The set is loaded from operator reference
// data at startup and can be replaced while the service is serving traffic,
// which is why it carries a lock.
//
// A TypeSet must be created with NewTypeSet; the zero value rejects everything.
type TypeSet struct {
	mu sync.RWMutex
	// byKey maps the canonical matching key to the operator's own spelling.
	byKey map[string]string
}

// NewTypeSet builds a set from the operator's configured type names.
func NewTypeSet(types []string) *TypeSet {
	s := &TypeSet{}
	s.Replace(types)
	return s
}

// Replace swaps in a new set of accepted types. It is safe to call while
// requests are in flight.
func (s *TypeSet) Replace(types []string) {
	byKey := make(map[string]string, len(types))
	for _, t := range types {
		if key := typeKey(t); key != "" {
			byKey[key] = strings.TrimSpace(t)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey = byKey
}

// Canonical resolves a producer-supplied type to the operator's own spelling,
// reporting whether it is accepted.
//
// Matching ignores case and surrounding whitespace, and the operator's spelling
// is what gets stored. "TOLL" and "toll" are plainly the same intent, and
// normalising on the way in keeps one concept from being recorded three ways.
func (s *TypeSet) Canonical(t string) (string, bool) {
	key := typeKey(t)
	if key == "" {
		return "", false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	canonical, ok := s.byKey[key]
	return canonical, ok
}

// All returns the accepted types in the operator's own spelling, sorted.
func (s *TypeSet) All() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return slices.Sorted(maps.Values(s.byKey))
}

// Len reports how many types are accepted.
func (s *TypeSet) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.byKey)
}

func typeKey(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}
