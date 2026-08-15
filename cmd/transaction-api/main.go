// Command transaction-api serves the Transaction Ingest API.
//
// It accepts a single billable tolling transaction per request from a producing
// system, validates it, and durably records it. See api/openapi.yaml for the
// contract and docs/architecture.md for how the pieces fit together.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gregwinn/EmovisMicroService/internal/config"
	"github.com/gregwinn/EmovisMicroService/internal/httpapi"
	"github.com/gregwinn/EmovisMicroService/internal/platform/health"
	"github.com/gregwinn/EmovisMicroService/internal/platform/logging"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// healthCheckTimeout bounds each individual readiness check. Probes must answer
// quickly enough that an orchestrator's own probe timeout is never the thing
// that fires first.
const healthCheckTimeout = 2 * time.Second

func main() {
	if err := run(context.Background()); err != nil {
		// Startup can fail before a logger exists, so this path uses stderr
		// directly rather than assuming structured logging is available.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logger := logging.New(os.Stdout, logging.Options{
		Level:       cfg.LogLevel,
		Format:      cfg.LogFormat,
		Service:     cfg.ServiceName,
		Environment: cfg.Environment,
		Version:     version,
	})

	checker := health.New(healthCheckTimeout)

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Logger:  logger,
			Health:  checker,
			Version: version,
		}),
		ReadTimeout: cfg.ReadTimeout,
		// ReadHeaderTimeout is set explicitly to bound slow-header attacks even
		// when ReadTimeout is relaxed for large payloads.
		ReadHeaderTimeout: cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	// SIGTERM is what a container orchestrator sends first; honouring it is what
	// makes a rolling deploy lossless for in-flight transactions.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", slog.String("addr", cfg.HTTPAddr))
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil

	case <-ctx.Done():
		logger.Info("shutdown signal received, draining",
			slog.Duration("timeout", cfg.ShutdownTimeout))
	}

	// A fresh context: the signal context is already cancelled, and shutdown
	// needs its own budget to finish in-flight requests.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	logger.Info("shutdown complete")
	return nil
}
