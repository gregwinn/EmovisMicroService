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
	"strings"
	"time"
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
}

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
