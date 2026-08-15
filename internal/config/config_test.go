package config

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "transaction-ingest", cfg.ServiceName)
	assert.Equal(t, "local", cfg.Environment)
	assert.Equal(t, ":8080", cfg.HTTPAddr)
	assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
	assert.Equal(t, "json", cfg.LogFormat)
	assert.Equal(t, 15*time.Second, cfg.ShutdownTimeout)
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("SERVICE_NAME", "ingest-canary")
	t.Setenv("ENVIRONMENT", "staging")
	t.Setenv("HTTP_ADDR", "127.0.0.1:9999")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("SHUTDOWN_TIMEOUT", "45s")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "ingest-canary", cfg.ServiceName)
	assert.Equal(t, "staging", cfg.Environment)
	assert.Equal(t, "127.0.0.1:9999", cfg.HTTPAddr)
	assert.Equal(t, slog.LevelDebug, cfg.LogLevel)
	assert.Equal(t, "text", cfg.LogFormat)
	assert.Equal(t, 45*time.Second, cfg.ShutdownTimeout)
}

// An empty environment variable is treated as unset. Container orchestrators
// routinely inject empty strings for values that were never populated, and
// falling back to the default beats failing to boot.
func TestLoadTreatsEmptyAsUnset(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("LOG_LEVEL", "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, ":8080", cfg.HTTPAddr)
	assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
}

func TestLoadRejectsBadValues(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "unparseable duration",
			env:     map[string]string{"HTTP_READ_TIMEOUT": "ten seconds"},
			wantErr: "HTTP_READ_TIMEOUT",
		},
		{
			name:    "non-positive duration",
			env:     map[string]string{"SHUTDOWN_TIMEOUT": "0s"},
			wantErr: "must be positive",
		},
		{
			name:    "unknown log level",
			env:     map[string]string{"LOG_LEVEL": "verbose"},
			wantErr: "LOG_LEVEL",
		},
		{
			name:    "unknown log format",
			env:     map[string]string{"LOG_FORMAT": "xml"},
			wantErr: "LOG_FORMAT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Load reports every problem at once. A deployment with three bad values should
// need one restart to discover all three, not three.
func TestLoadReportsAllProblemsTogether(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")
	t.Setenv("LOG_FORMAT", "xml")
	t.Setenv("SHUTDOWN_TIMEOUT", "nope")

	_, err := Load()
	require.Error(t, err)

	assert.Contains(t, err.Error(), "LOG_LEVEL")
	assert.Contains(t, err.Error(), "LOG_FORMAT")
	assert.Contains(t, err.Error(), "SHUTDOWN_TIMEOUT")
}

func TestParseLogLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"ERROR":   slog.LevelError,
		"Info":    slog.LevelInfo,
	}

	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			got, err := parseLogLevel(raw)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}
