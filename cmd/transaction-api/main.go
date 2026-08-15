// Command transaction-api serves the Transaction Ingest API.
//
// It accepts a single billable tolling transaction per request from a producing
// system, validates it, and durably records it. See api/openapi.yaml for the
// contract and docs/architecture.md for how the pieces fit together.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gregwinn/EmovisMicroService/internal/config"
	"github.com/gregwinn/EmovisMicroService/internal/httpapi"
	"github.com/gregwinn/EmovisMicroService/internal/money"
	"github.com/gregwinn/EmovisMicroService/internal/platform/health"
	"github.com/gregwinn/EmovisMicroService/internal/platform/logging"
	"github.com/gregwinn/EmovisMicroService/internal/platform/metrics"
	"github.com/gregwinn/EmovisMicroService/internal/store/memory"
	"github.com/gregwinn/EmovisMicroService/internal/store/postgres"
	"github.com/gregwinn/EmovisMicroService/internal/transaction"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// healthCheckTimeout bounds each individual readiness check. Probes must answer
// quickly enough that an orchestrator's own probe timeout is never the thing
// that fires first.
const healthCheckTimeout = 2 * time.Second

func main() {
	// A self-probe mode for container healthchecks: the distroless runtime image
	// has no shell and no curl. See healthcheck.go.
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz endpoint and exit")
	flag.Parse()

	if *healthcheck {
		addr := os.Getenv("HTTP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		if err := runHealthcheck(addr); err != nil {
			fmt.Fprintf(os.Stderr, "unhealthy: %v\n", err)
			os.Exit(1)
		}
		return
	}

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
	recorder := metrics.New()

	// config.Load has already verified the currency is recognised.
	defaultCurrency, _ := money.Lookup(cfg.DefaultCurrency)

	rules := transaction.Rules{
		Types:           transaction.NewTypeSet(cfg.TransactionTypes),
		DefaultCurrency: defaultCurrency,
		MaxClockSkew:    cfg.MaxClockSkew,
	}

	store, storeName, closeStore, err := openStore(ctx, cfg, checker)
	if err != nil {
		return err
	}
	defer closeStore()

	logger.Info("ingest policy loaded",
		slog.Any("transaction_types", rules.Types.All()),
		slog.String("default_currency", defaultCurrency.Code),
		slog.Duration("max_clock_skew", cfg.MaxClockSkew),
		slog.String("store", storeName))

	if !cfg.UsesDatabase() {
		logger.Warn("running without a database: accepted transactions are held in memory and lost on restart",
			slog.String("remedy", "set DATABASE_URL to use PostgreSQL"))
	}

	// Building the router loads and parses the embedded OpenAPI contract. A
	// malformed contract is a build-time mistake that must stop the process
	// here rather than surface as a runtime failure on the first request.
	router, err := httpapi.NewRouter(httpapi.Deps{
		Logger:  logger,
		Health:  checker,
		Version: version,
		Rules:   rules,
		Store:   store,
		Metrics: recorder,
	})
	if err != nil {
		return fmt.Errorf("build http router: %w", err)
	}

	srv := &http.Server{
		Addr:        cfg.HTTPAddr,
		Handler:     router,
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

// openStore selects the durable store when a database is configured and the
// in-memory one otherwise, registering a readiness check for whichever it
// returns.
//
// Falling back rather than refusing to start is what keeps the README
// quickstart honest: a reader can clone the repo and exercise the endpoint
// without provisioning anything. The trade is that a deployment which *meant*
// to have a database but lost the variable starts up looking healthy, so the
// fallback is logged loudly at Warn.
func openStore(
	ctx context.Context,
	cfg config.Config,
	checker *health.Checker,
) (transaction.Store, string, func(), error) {
	if !cfg.UsesDatabase() {
		return memory.New(), "memory (not durable)", func() {}, nil
	}

	//nolint:gosec // DatabaseMaxConns is validated positive by config.Load.
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, int32(cfg.DatabaseMaxConns))
	if err != nil {
		return nil, "", nil, fmt.Errorf("connect to database: %w", err)
	}

	store := postgres.New(pool)

	// Readiness, not liveness: an unreachable database should drain this
	// instance from the load balancer, not have the orchestrator restart a
	// process that is otherwise fine.
	checker.Register("database", store.Ping)

	return store, "postgres", pool.Close, nil
}
