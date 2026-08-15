// Package db embeds the SQL migrations so they travel with the binary.
//
// Embedding means the container image cannot ship a binary and a stale set of
// migration files, and the integration tests apply exactly the schema that
// production will.
package db

import "embed"

// Migrations holds the goose migration files in db/migrations.
//
// Migrations are append-only. Editing one that has already been applied
// anywhere means environments silently disagree about their schema; the fix for
// a bad migration is another migration.
//
//go:embed migrations/*.sql
var Migrations embed.FS
