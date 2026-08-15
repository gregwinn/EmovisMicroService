package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testOptions(format string, level slog.Level) Options {
	return Options{
		Level:       level,
		Format:      format,
		Service:     "transaction-ingest",
		Environment: "test",
		Version:     "1.2.3",
	}
}

// Every record carries service, environment, and version so that logs pooled
// from several deployments stay attributable.
func TestNewJSONAttachesServiceIdentity(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, testOptions("json", slog.LevelInfo)).Info("ingested", slog.String("source", "lane-controller-07"))

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))

	assert.Equal(t, "transaction-ingest", record["service"])
	assert.Equal(t, "test", record["env"])
	assert.Equal(t, "1.2.3", record["version"])
	assert.Equal(t, "ingested", record["msg"])
	assert.Equal(t, "lane-controller-07", record["source"])
}

func TestNewTextFormat(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, testOptions("text", slog.LevelInfo)).Info("ingested")

	out := buf.String()
	assert.Contains(t, out, "service=transaction-ingest")
	assert.Contains(t, out, "msg=ingested")
	assert.False(t, json.Valid(bytes.TrimSpace(buf.Bytes())), "text format should not emit JSON")
}

// An unrecognized format must not silently disable logging. config.Load already
// rejects bad values, so this is defence in depth.
func TestNewUnknownFormatFallsBackToJSON(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, testOptions("yaml", slog.LevelInfo)).Info("ingested")

	assert.True(t, json.Valid(bytes.TrimSpace(buf.Bytes())))
}

func TestNewRespectsLevel(t *testing.T) {
	tests := []struct {
		name      string
		level     slog.Level
		wantDebug bool
	}{
		{name: "info suppresses debug", level: slog.LevelInfo, wantDebug: false},
		{name: "debug emits debug", level: slog.LevelDebug, wantDebug: true},
		{name: "error suppresses info and debug", level: slog.LevelError, wantDebug: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			New(&buf, testOptions("json", tt.level)).Debug("noisy detail")

			assert.Equal(t, tt.wantDebug, strings.Contains(buf.String(), "noisy detail"))
		})
	}
}
