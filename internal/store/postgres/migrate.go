package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/gregwinn/EmovisMicroService/db"
)

// Migrations are applied by an explicit step, never automatically at service
// startup.
//
// Auto-migrating on boot is convenient in a demo and wrong in production: a
// rolling deploy starts several tasks at once and they would race the same DDL.
// Worse, it couples "this instance is starting" to "the schema changes", so a
// scale-up event during an incident can alter the database. Deployment runs
// `migrate` as a one-off task before the new revision is released.

// Migrate applies every pending migration.
func Migrate(ctx context.Context, databaseURL string) error {
	sqlDB, err := goose.OpenDBWithDriver("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database for migration: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	return runGoose(ctx, sqlDB, func() error {
		return goose.UpContext(ctx, sqlDB, "migrations")
	})
}

// MigrateWithPool applies pending migrations over an existing pool, which is
// what the integration tests use so they do not open a second connection.
func MigrateWithPool(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()

	return runGoose(ctx, sqlDB, func() error {
		return goose.UpContext(ctx, sqlDB, "migrations")
	})
}

// Status writes the applied and pending migrations to goose's logger.
func Status(ctx context.Context, databaseURL string) error {
	sqlDB, err := goose.OpenDBWithDriver("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database for migration: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	return runGoose(ctx, sqlDB, func() error {
		return goose.StatusContext(ctx, sqlDB, "migrations")
	})
}

// Down rolls back the most recent migration. Present for local iteration; a
// production rollback is a forward migration, not a reversal.
func Down(ctx context.Context, databaseURL string) error {
	sqlDB, err := goose.OpenDBWithDriver("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database for migration: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	return runGoose(ctx, sqlDB, func() error {
		return goose.DownContext(ctx, sqlDB, "migrations")
	})
}

func runGoose(_ context.Context, _ *sql.DB, run func() error) error {
	goose.SetBaseFS(db.Migrations)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}

	if err := run(); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
