package logging

import (
	"log/slog"
	"strings"
)

// A license plate is not just a string — it identifies a person and, in a
// tolling system, their movements. Transponder ids are the same. Log
// aggregation is typically the least access-controlled and longest-retained
// place data goes, so identifiers must not reach it.
//
// The primary defence is that no code path logs them. This is the second
// defence: even if someone adds an attribute in a hurry, the handler redacts it
// on the way out. Tests assert the first property; this makes it structural, so
// the failure mode of a future mistake is a redacted log line rather than a
// disclosure.
//
// The event published to the resolution pipeline deliberately does carry these
// values — that pipeline exists to attribute vehicles and is inside the trust
// boundary. The rule is about logs, not about the domain.

// redactedValue replaces a sensitive value. It is deliberately distinctive so a
// search for it shows where identifiers were nearly logged.
const redactedValue = "[REDACTED]"

// sensitiveKeys are attribute keys whose values identify a vehicle or a person.
//
// Matched on the key's suffix after the last underscore-delimited qualifier, so
// both "plate_number" and "submitted_plate_number" are caught.
var sensitiveKeys = map[string]struct{}{
	"plate":                  {},
	"plate_number":           {},
	"plate_number_key":       {},
	"license_plate":          {},
	"transponder":            {},
	"transponder_number":     {},
	"transponder_number_key": {},
	"tag_id":                 {},
	"vehicle_id":             {},
}

// redactSensitive is a slog.HandlerOptions.ReplaceAttr function.
//
// ReplaceAttr is used rather than a custom Handler because slog already applies
// it to every attribute at every nesting depth, including inside groups. A
// hand-written Handler would have to re-implement that traversal, and any gap
// in it would be a silent leak.
func redactSensitive(_ []string, a slog.Attr) slog.Attr {
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, redactedValue)
	}
	return a
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	if _, ok := sensitiveKeys[normalized]; ok {
		return true
	}

	// Catch prefixed variants such as "stored_plate_number" or
	// "submitted_transponder_number" without matching on substrings, which
	// would be too eager.
	for sensitive := range sensitiveKeys {
		if strings.HasSuffix(normalized, "_"+sensitive) {
			return true
		}
	}

	return false
}
