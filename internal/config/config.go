// Package config loads and validates service configuration from the environment.
//
// Configuration is resolved once at startup and then treated as immutable. Every
// problem found is reported together rather than one per restart, because a
// misconfigured deployment should tell you everything that is wrong on the first
// try.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gregwinn/EmovisMicroService/internal/money"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

// Config is the fully resolved configuration for a service process.
type Config struct {
	// Service identity, used in logs and metrics.
	ServiceName string
	Environment string

	// HTTP server.
	HTTPAddr        string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration

	// Logging.
	LogLevel  slog.Level
	LogFormat string

	// Ingest policy.

	// TransactionTypes is the set of billable event types this deployment
	// accepts. The contract states these are "operator-configurable at runtime,
	// not compiled in", so they are configuration rather than a Go enum.
	//
	// Environment configuration is the seam, not the destination: in production
	// this would be backed by the operator's reference data service so a new
	// type does not need a restart. transaction.TypeSet is already swappable in
	// place, so that change touches the loader and nothing else.
	TransactionTypes []string

	// DefaultCurrency applies when a producer omits `currency`, which the
	// contract permits.
	DefaultCurrency string

	// MaxClockSkew bounds how far ahead of now transaction_time_utc may be.
	// There is no equivalent bound on the past: batch and image-review
	// producers legitimately submit long after the vehicle passed.
	MaxClockSkew time.Duration

	// Storage.

	// DatabaseURL is the PostgreSQL connection string. When empty the service
	// falls back to the in-memory store, which keeps the quickstart runnable
	// with no infrastructure but is not durable.
	DatabaseURL string

	// DatabaseMaxConns caps the connection pool. It wants to be a little above
	// expected concurrency and well below the server's max_connections divided
	// by the number of running instances.
	DatabaseMaxConns int

	// Outbox relay.

	// OutboxBatchSize caps how many events one relay pass claims.
	OutboxBatchSize int
	// OutboxPollInterval is how long the relay waits after an empty pass. A
	// non-empty pass retries immediately, so a backlog still drains at speed.
	OutboxPollInterval time.Duration
}

// UsesDatabase reports whether a durable store is configured.
func (c Config) UsesDatabase() bool { return c.DatabaseURL != "" }

// Load reads configuration from the environment, applying defaults suitable for
// local development. It returns every validation problem it finds, joined into a
// single error.
func Load() (Config, error) {
	var errs []error

	cfg := Config{
		ServiceName:     envString("SERVICE_NAME", "transaction-ingest"),
		Environment:     envString("ENVIRONMENT", "local"),
		HTTPAddr:        envString("HTTP_ADDR", ":8080"),
		ReadTimeout:     envDuration("HTTP_READ_TIMEOUT", 5*time.Second, &errs),
		WriteTimeout:    envDuration("HTTP_WRITE_TIMEOUT", 10*time.Second, &errs),
		IdleTimeout:     envDuration("HTTP_IDLE_TIMEOUT", 120*time.Second, &errs),
		ShutdownTimeout: envDuration("SHUTDOWN_TIMEOUT", 15*time.Second, &errs),
		LogFormat:       envString("LOG_FORMAT", "json"),

		TransactionTypes: envStringSlice("TRANSACTION_TYPES", []string{"toll", "violation", "fee"}),
		DefaultCurrency:  strings.ToUpper(envString("DEFAULT_CURRENCY", "USD")),
		MaxClockSkew:     envDuration("MAX_CLOCK_SKEW", transaction.DefaultMaxClockSkew, &errs),

		DatabaseURL:      envString("DATABASE_URL", ""),
		DatabaseMaxConns: envInt("DATABASE_MAX_CONNS", 10, &errs),

		OutboxBatchSize:    envInt("OUTBOX_BATCH_SIZE", 100, &errs),
		OutboxPollInterval: envDuration("OUTBOX_POLL_INTERVAL", 2*time.Second, &errs),
	}

	if len(cfg.TransactionTypes) == 0 {
		errs = append(errs, errors.New("TRANSACTION_TYPES: must list at least one accepted type"))
	}
	if _, ok := money.Lookup(cfg.DefaultCurrency); !ok {
		errs = append(errs, fmt.Errorf("DEFAULT_CURRENCY: %q is not a recognised ISO-4217 code", cfg.DefaultCurrency))
	}

	level, err := parseLogLevel(envString("LOG_LEVEL", "info"))
	if err != nil {
		errs = append(errs, err)
	}
	cfg.LogLevel = level

	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("LOG_FORMAT: want %q or %q, got %q", "json", "text", cfg.LogFormat))
	}
	if cfg.HTTPAddr == "" {
		errs = append(errs, errors.New("HTTP_ADDR: must not be empty"))
	}

	return cfg, errors.Join(errs...)
}

func envString(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// envStringSlice reads a comma-separated list, trimming each entry and dropping
// blanks so that trailing commas and stray spaces are not a deployment hazard.
func envStringSlice(key string, fallback []string) []string {
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback
	}

	var values []string
	for part := range strings.SplitSeq(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

// envInt parses an integer, recording a problem rather than failing fast so
// that Load can report every error at once.
func envInt(key string, fallback int, errs *[]error) int {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %q is not a valid integer", key, raw))
		return fallback
	}
	if n <= 0 {
		*errs = append(*errs, fmt.Errorf("%s: must be positive, got %d", key, n))
		return fallback
	}
	return n
}

// envDuration parses a duration, recording a problem rather than failing fast so
// that Load can report every error at once.
func envDuration(key string, fallback time.Duration, errs *[]error) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %q is not a valid duration (e.g. %q)", key, raw, "5s"))
		return fallback
	}
	if d <= 0 {
		*errs = append(*errs, fmt.Errorf("%s: must be positive, got %s", key, d))
		return fallback
	}
	return d
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("LOG_LEVEL: want one of debug|info|warn|error, got %q", raw)
	}
}
