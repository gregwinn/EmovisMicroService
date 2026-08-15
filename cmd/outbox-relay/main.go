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
	"os"
	"os/signal"
	"syscall"

	"github.com/gregwinn/EmovisMicroService/internal/config"
	"github.com/gregwinn/EmovisMicroService/internal/outbox"
	"github.com/gregwinn/EmovisMicroService/internal/platform/logging"
	"github.com/gregwinn/EmovisMicroService/internal/store/postgres"
)

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

	relay := outbox.NewRelay(pool, outbox.NewLogPublisher(logger), logger, outbox.RelayOptions{
		BatchSize:    cfg.OutboxBatchSize,
		PollInterval: cfg.OutboxPollInterval,
	})

	if pending, err := relay.PendingCount(ctx); err == nil {
		logger.Info("outbox backlog at startup", slog.Int("pending", pending))
	}

	return relay.Run(ctx)
}
