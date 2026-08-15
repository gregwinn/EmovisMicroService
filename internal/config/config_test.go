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

func TestIngestPolicyDefaults(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, []string{"toll", "violation", "fee"}, cfg.TransactionTypes)
	assert.Equal(t, "USD", cfg.DefaultCurrency)
	assert.Equal(t, 5*time.Minute, cfg.MaxClockSkew)
	assert.Equal(t, 10, cfg.DatabaseMaxConns)
	assert.False(t, cfg.UsesDatabase(), "no database configured by default")
}

// Transaction types are operator configuration. Trailing commas and stray
// spaces are a deployment hazard, not a reason to fail.
func TestTransactionTypesParsing(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "single", raw: "toll", want: []string{"toll"}},
		{name: "several", raw: "toll,violation,fee", want: []string{"toll", "violation", "fee"}},
		{name: "spaces around entries", raw: "toll, violation , fee", want: []string{"toll", "violation", "fee"}},
		{name: "trailing comma", raw: "toll,violation,", want: []string{"toll", "violation"}},
		{name: "repeated commas", raw: "toll,,violation", want: []string{"toll", "violation"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TRANSACTION_TYPES", tt.raw)

			cfg, err := Load()
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.TransactionTypes)
		})
	}
}

func TestTransactionTypesCannotBeEmpty(t *testing.T) {
	t.Setenv("TRANSACTION_TYPES", " , , ")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one accepted type")
}

// A currency typo should fail the boot, not every request.
func TestDefaultCurrencyIsValidated(t *testing.T) {
	t.Setenv("DEFAULT_CURRENCY", "XYZ")

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a recognised ISO-4217 code")
}

func TestDefaultCurrencyIsUpperCased(t *testing.T) {
	t.Setenv("DEFAULT_CURRENCY", "gbp")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, "GBP", cfg.DefaultCurrency)
}

func TestDatabaseConfiguration(t *testing.T) {
	const url = "postgres://ingest:secret@db:5432/ingest?sslmode=require"

	t.Setenv("DATABASE_URL", url)
	t.Setenv("DATABASE_MAX_CONNS", "25")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, url, cfg.DatabaseURL)
	assert.Equal(t, 25, cfg.DatabaseMaxConns)
	assert.True(t, cfg.UsesDatabase())
}

func TestDatabaseMaxConnsIsValidated(t *testing.T) {
	tests := map[string]string{
		"not a number": "many",
		"zero":         "0",
		"negative":     "-5",
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DATABASE_MAX_CONNS", raw)

			_, err := Load()

			require.Error(t, err)
			assert.Contains(t, err.Error(), "DATABASE_MAX_CONNS")
		})
	}
}

func TestMaxClockSkewIsConfigurable(t *testing.T) {
	t.Setenv("MAX_CLOCK_SKEW", "15m")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, cfg.MaxClockSkew)
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
