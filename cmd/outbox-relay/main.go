// Command outbox-relay publishes accepted transactions to the resolution
// pipeline.
//
// It is a separate process from the API on purpose. Publishing pressure — a
// slow broker, a retry storm, a downstream outage — must not slow down or fail
// transaction ingest. The API's job is to commit the transaction and its event
// atomically; getting the event out is this process's job, and it can fall
// behind without anything being lost.
//
// Several replicas can run concurrently: the relay claims work with
// FOR UPDATE SKIP LOCKED, so each takes a disjoint set of events.
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

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/gregwinn/EmovisMicroService/internal/config"
	"github.com/gregwinn/EmovisMicroService/internal/outbox"
	"github.com/gregwinn/EmovisMicroService/internal/platform/logging"
	"github.com/gregwinn/EmovisMicroService/internal/platform/metrics"
	"github.com/gregwinn/EmovisMicroService/internal/store/postgres"
)

// metricsMux serves the relay's collectors plus a liveness probe.
func metricsMux(recorder *metrics.Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(recorder.Registry(), promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"up"}`))
	})
	return mux
}

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// Signal handling lives in run: os.Exit skips deferred calls, so a defer
	// here would silently never run.
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logger := logging.New(os.Stdout, logging.Options{
		Level:       cfg.LogLevel,
		Format:      cfg.LogFormat,
		Service:     cfg.ServiceName + "-outbox-relay",
		Environment: cfg.Environment,
		Version:     version,
	})

	// The relay has nothing to do without a durable outbox. Refusing to start
	// beats running as a no-op that looks healthy.
	if !cfg.UsesDatabase() {
		return errors.New("DATABASE_URL must be set: the outbox relay requires a durable store")
	}

	//nolint:gosec // DatabaseMaxConns is validated positive by config.Load.
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, int32(cfg.DatabaseMaxConns))
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	recorder := metrics.New()

	relay := outbox.NewRelay(pool, outbox.NewLogPublisher(logger), logger, outbox.RelayOptions{
		BatchSize:    cfg.OutboxBatchSize,
		PollInterval: cfg.OutboxPollInterval,
	}).WithMetrics(recorder)

	// The relay serves its own /metrics: outbox depth and lag are the SLI for
	// whether the resolution pipeline is hearing about transactions, and they
	// are only observable from the process doing the draining.
	metricsServer := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           metricsMux(recorder),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("relay metrics listening", slog.String("addr", cfg.MetricsAddr))
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("relay metrics server stopped", slog.Any("error", err))
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = metricsServer.Shutdown(shutdownCtx)
	}()

	if pending, err := relay.PendingCount(ctx); err == nil {
		logger.Info("outbox backlog at startup", slog.Int("pending", pending))
	}

	return relay.Run(ctx)
}
