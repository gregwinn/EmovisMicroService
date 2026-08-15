# Changelog

All notable changes to this service are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Because this service publishes an external contract, **the contract's
compatibility is what the version number tracks.** A change that breaks a
producer coded against `api/openapi.yaml` is a major version, regardless of how
small the diff is.

## [Unreleased]

## [0.1.0] — 2026-08-15

First release. Implements the `Transaction Ingest API` contract end to end.

### Added

**The contract, made executable**
- `api/openapi.yaml` as the source of truth, byte-identical to the spec as
  provided.
- Types and the server interface generated from it, so an unimplemented
  operation is a compile error. Generated output is committed and CI fails if
  it has drifted.
- Every inbound request validated against the embedded spec before any handler
  runs, reporting all schema violations at once.

**Ingest**
- `POST /ingest/v1/transactions` — `201` for a new transaction, `200` for a
  replay, `400` for anything a producer got wrong.
- Idempotent on `(source, source_reference)`, enforced by a unique constraint
  and `ON CONFLICT`, so concurrent retries cannot both create a record.
- Semantic validation for the rules a schema cannot express: at least one
  usable identifier, a known transaction type, a plausible timestamp, and an
  exact non-negative amount whose precision fits its currency.
- A divergent replay answers on contract and is logged and counted rather than
  silently discarded.

**Durability**
- PostgreSQL persistence with the producer's original amount text and raw
  identifier reads retained as evidence alongside their canonical forms.
- A transactional outbox: the transaction row and the event announcing it
  commit together, so the resolution pipeline cannot miss a billable
  transaction.
- An `outbox-relay` process draining the outbox with `FOR UPDATE SKIP LOCKED`,
  exponential backoff, and parking rather than dropping undeliverable events.
- Migrations embedded in the binary and applied by an explicit `migrate` step,
  never at service startup.

**Operations**
- Prometheus metrics covering ingest results, validation failures by reason,
  divergent replays, and outbox depth and lag.
- Structured JSON logs with request-id correlation and PII redaction enforced
  by the log handler.
- Separate liveness and readiness probes, and graceful shutdown on `SIGTERM`.
- Distroless non-root container images, a Compose stack, and `make demo` — one
  command that walks the whole service end to end.
- Terraform for AWS ECS Fargate, validated in CI and never applied.

**Documentation**
- Twelve ADRs, an architecture guide, a domain primer for developers new to
  tolling, an API guide with runnable requests, a runbook, security notes, and
  a list of every assumption made without access to the team.
- `AGENTS.md` and `.claude/` configuration for AI coding agents, plus an honest
  account of how the service was built with AI — including what went wrong.

### Known limitations

Each is a deliberate scope call, documented in
[docs/assumptions.md](docs/assumptions.md):

- **No authentication.** The contract declares `security: []`; no scheme was
  invented. mTLS is the recommendation.
- **No batch endpoint**, despite the contract naming a file-based batch loader
  as a producer. Written up as a proposal rather than built.
- **At-least-once, unordered delivery** downstream. Consumers must deduplicate
  on `event_id`.
- **No OpenTelemetry tracing.**
- **Nothing deployed.** The Terraform is validated, not applied.

[Unreleased]: https://github.com/gregwinn/EmovisMicroService/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/gregwinn/EmovisMicroService/releases/tag/v0.1.0
