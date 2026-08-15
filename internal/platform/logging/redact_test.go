package logging

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func redactingLogger(buf *bytes.Buffer) *slog.Logger {
	return New(buf, Options{Level: slog.LevelDebug, Format: "json", Service: "test"})
}

// A license plate identifies a person and, in a tolling system, their
// movements. Log aggregation is the least access-controlled and
// longest-retained place data goes.
func TestSensitiveAttributesAreRedacted(t *testing.T) {
	tests := []struct {
		name string
		attr slog.Attr
	}{
		{name: "plate", attr: slog.String("plate", "ABC1234")},
		{name: "plate_number", attr: slog.String("plate_number", "ABC1234")},
		{name: "plate_number_key", attr: slog.String("plate_number_key", "ABC1234")},
		{name: "license_plate", attr: slog.String("license_plate", "ABC1234")},
		{name: "transponder", attr: slog.String("transponder", "ABC1234")},
		{name: "transponder_number", attr: slog.String("transponder_number", "ABC1234")},
		{name: "tag_id", attr: slog.String("tag_id", "ABC1234")},
		{name: "vehicle_id", attr: slog.String("vehicle_id", "ABC1234")},
		{name: "upper case key", attr: slog.String("Plate_Number", "ABC1234")},
		{name: "prefixed variant", attr: slog.String("stored_plate_number", "ABC1234")},
		{name: "another prefixed variant", attr: slog.String("submitted_transponder_number", "ABC1234")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			redactingLogger(&buf).LogAttrs(t.Context(), slog.LevelInfo, "ingested", tt.attr)

			out := buf.String()
			assert.NotContains(t, out, "ABC1234", "the identifier must not reach the log")
			assert.Contains(t, out, redactedValue)
		})
	}
}

// slog applies ReplaceAttr at every nesting depth. Using it rather than a
// hand-written Handler is why groups are covered without extra code — and this
// test is what proves the claim.
func TestRedactionAppliesInsideGroups(t *testing.T) {
	var buf bytes.Buffer

	redactingLogger(&buf).LogAttrs(t.Context(), slog.LevelInfo, "ingested",
		slog.Group("vehicle",
			slog.String("plate_number", "ABC1234"),
			slog.String("jurisdiction", "TX"),
		),
	)

	out := buf.String()
	assert.NotContains(t, out, "ABC1234")
	assert.Contains(t, out, redactedValue)
	assert.Contains(t, out, "TX", "non-sensitive fields survive")
}

// Redaction must not be so eager that it hides operational detail.
func TestNonSensitiveAttributesSurvive(t *testing.T) {
	var buf bytes.Buffer

	redactingLogger(&buf).LogAttrs(t.Context(), slog.LevelInfo, "ingested",
		slog.String("source", "lane-controller-07"),
		slog.String("source_reference", "LC07-000918"),
		slog.String("transaction_type", "toll"),
		slog.String("jurisdiction", "TX"),
		slog.String("facility", "SH-130"),
		slog.String("amount", "12.50 USD"),
		slog.Int("status", 201),
	)

	out := buf.String()
	assert.NotContains(t, out, redactedValue)
	for _, want := range []string{"lane-controller-07", "LC07-000918", "toll", "TX", "SH-130", "12.50 USD"} {
		assert.Contains(t, out, want)
	}
}

func TestIsSensitiveKey(t *testing.T) {
	sensitive := []string{
		"plate", "plate_number", "PLATE_NUMBER", "transponder_number",
		"stored_plate_number", "submitted_plate_number", "tag_id",
	}
	for _, key := range sensitive {
		assert.True(t, isSensitiveKey(key), "%q should be redacted", key)
	}

	// Deliberately not redacted: substring matching would be too eager and
	// would hide fields an operator needs.
	safe := []string{
		"source", "source_reference", "plateau", "number", "jurisdiction",
		"transaction_type", "id", "request_id", "aggregate_id",
	}
	for _, key := range safe {
		assert.False(t, isSensitiveKey(key), "%q should not be redacted", key)
	}
}

// Redaction is defence in depth, not the primary control. The service still
// must not put identifiers into log attributes in the first place — a value
// interpolated into the message string would not be caught here.
func TestRedactionDoesNotCoverMessageStrings(t *testing.T) {
	var buf bytes.Buffer
	redactingLogger(&buf).Info("plate ABC1234 rejected")

	require.Contains(t, buf.String(), "ABC1234",
		"this documents the limitation: interpolated values are not redacted, "+
			"which is why the handler tests assert identifiers stay out of logs entirely")
}
