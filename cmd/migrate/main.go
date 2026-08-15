// Command migrate applies database migrations.
//
// It is a separate binary from the service on purpose. Migrations run as an
// explicit deployment step — a one-off task before the new revision is
// released — rather than at service startup, where several rolling-deploy tasks
// would race the same DDL.
//
// Usage:
//
//	migrate up       apply every pending migration (default)
//	migrate status   show applied and pending migrations
//	migrate down     roll back the most recent migration (local use)
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gregwinn/EmovisMicroService/internal/config"
	"github.com/gregwinn/EmovisMicroService/internal/store/postgres"
)

func main() {
	// Signal handling is set up inside run rather than here: os.Exit skips
	// deferred calls, so a `defer stop()` in main would silently never run.
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
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
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL must be set")
	}

	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "up":
		if err := postgres.Migrate(ctx, cfg.DatabaseURL); err != nil {
			return err
		}
		fmt.Println("migrations applied")
		return nil

	case "status":
		return postgres.Status(ctx, cfg.DatabaseURL)

	case "down":
		if err := postgres.Down(ctx, cfg.DatabaseURL); err != nil {
			return err
		}
		fmt.Println("rolled back one migration")
		return nil

	default:
		return fmt.Errorf("unknown command %q: want up, status, or down", command)
	}
}
