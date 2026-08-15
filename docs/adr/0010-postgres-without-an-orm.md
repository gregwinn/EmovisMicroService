# ADR-0010: PostgreSQL with hand-written SQL, no ORM

**Status:** Accepted · **Date:** 2026-08-15

## Context

The store has two statements that matter: an idempotent insert, and a claim-with-lock for the outbox relay. Both depend on precise database semantics.

## Decision

`pgx/v5` with hand-written SQL in a small repository package. Migrations via `goose`, embedded with `embed.FS`, applied by a separate `migrate` binary.

## Why

The correctness of billing here rests on exactly which rows the database locks:

- `ON CONFLICT ... DO NOTHING ... RETURNING id` — a returned row means we created it, no row means someone else did. That distinction is what makes check-and-insert atomic without an explicit lock.
- `FOR UPDATE SKIP LOCKED` — what lets relay replicas partition work rather than double-publish it.

An ORM would hide both behind an abstraction. When a behaviour is load-bearing, it should be visible in the file you are reading.

Migrations run as an explicit deployment step, never at service startup. A rolling deploy starts several tasks at once and they would race the same DDL; worse, auto-migration couples "an instance is starting" to "the schema changes", so a scale-up during an incident could alter the database.

## Consequences

Deploys need a migration step. The Terraform includes it as a one-off ECS task, and Compose models it as a service that runs to completion — the local setup mirrors production rather than inventing a shortcut that hides it.

Scanning rows is manual. `sqlc` is the scale-up path if the query count grows; the repository boundary means adopting it would not touch the domain.

Two domain rules are restated as `CHECK` constraints — at least one identifier, and a non-negative amount. Application validation produces the friendly message; the constraint is the guarantee.
