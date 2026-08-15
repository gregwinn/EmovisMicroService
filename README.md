# Transaction Ingest Service

A microservice that accepts, validates, and durably records billable tolling
transactions pushed by roadside lane controllers, image-review vendors,
interoperability peers, and batch loaders.

Built against the `Transaction Ingest API` OpenAPI contract in
[`api/openapi.yaml`](api/openapi.yaml), which is the source of truth for the
wire format — not documentation alongside it.

> **Status:** in active development. See [Roadmap](#roadmap) for what is built
> and what is still landing.

---

## The problem this solves

A transaction is the atomic unit of revenue in a tolling back office. Ingest has
three obligations, and getting any of them wrong costs real money:

| Obligation | How it is met |
|---|---|
| **Never double-bill** | Idempotent on `(source, source_reference)`. Producers retry over unreliable links and replay whole files; a retry must never create a second billable record. |
| **Never lose an accepted transaction** | The transaction row and the event announcing it are committed in a single database transaction, via a transactional outbox. |
| **Always explain a rejection** | Validation happens in three explicit layers, and a `400` names the field and the reason. |

Ingest deliberately does **not** resolve who owns the vehicle, price the
transaction, or collect payment. Those are downstream concerns.

---

## Quick start

The fastest way to see the whole thing work:

```bash
git clone git@github.com:gregwinn/EmovisMicroService.git
cd EmovisMicroService
make demo
```

That builds the images, starts PostgreSQL, applies migrations, runs the API and
the outbox relay, then walks the service end to end: a new transaction, a
retry, a divergent replay, a schema rejection, a semantic rejection, the stored
row, and the event the relay published downstream.

**Requirements:** Docker for `make demo` and the integration tests. Go 1.25+ to
build and test locally.

### Running it directly

```bash
make            # list every available target
make ci         # everything CI runs: tidy, contract, lint, cover, build, vuln
make run        # start the API on :8080 (in-memory store, no database needed)
```

Then, in another shell:

```bash
BODY='{
  "source": "lane-controller-07",
  "source_reference": "LC07-20260814-000918",
  "transaction_type": "toll",
  "transaction_time_utc": "2026-08-14T13:45:02Z",
  "base_amount": "12.50",
  "plate": { "number": "ABC1234", "jurisdiction": "TX" }
}'

# 201 — a new billable transaction
curl -s localhost:8080/ingest/v1/transactions -H 'Content-Type: application/json' -d "$BODY" | jq

# 200 with duplicate=true and the same id — the retry created nothing
curl -s localhost:8080/ingest/v1/transactions -H 'Content-Type: application/json' -d "$BODY" | jq
```

> **Note:** with no `DATABASE_URL` set the service uses an in-memory store, so
> nothing survives a restart. It logs a warning saying so. Point `DATABASE_URL`
> at PostgreSQL and run `make migrate` for durable ingest.

---

## Configuration

Every setting is read from the environment at startup and validated once. A
misconfigured deployment reports **all** of its problems on the first boot
rather than one per restart.

| Variable | Default | Purpose |
|---|---|---|
| `SERVICE_NAME` | `transaction-ingest` | Identity attached to every log record |
| `ENVIRONMENT` | `local` | Deployment environment label |
| `HTTP_ADDR` | `:8080` | Listen address |
| `HTTP_READ_TIMEOUT` | `5s` | Request read timeout |
| `HTTP_WRITE_TIMEOUT` | `10s` | Response write timeout |
| `HTTP_IDLE_TIMEOUT` | `120s` | Keep-alive idle timeout |
| `SHUTDOWN_TIMEOUT` | `15s` | Grace period for draining in-flight requests |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `LOG_FORMAT` | `json` | `json` \| `text` |
| `TRANSACTION_TYPES` | `toll,violation,fee` | Accepted billable event types — operator configuration, not a compiled enum |
| `DEFAULT_CURRENCY` | `USD` | Applied when a producer omits `currency` |
| `MAX_CLOCK_SKEW` | `5m` | How far ahead of now `transaction_time_utc` may be. There is no bound on the past. |
| `DATABASE_URL` | _(unset)_ | PostgreSQL connection string. **Unset falls back to a non-durable in-memory store.** |
| `DATABASE_MAX_CONNS` | `10` | Connection pool ceiling |
| `OUTBOX_BATCH_SIZE` | `100` | Events claimed per relay pass |
| `OUTBOX_POLL_INTERVAL` | `2s` | Wait after an empty pass; a non-empty pass retries at once |
| `METRICS_ADDR` | `:9090` | Where the outbox relay serves `/metrics` |

---

## Repository layout

```
api/            OpenAPI contract — the source of truth for the wire format
cmd/            One directory per binary
  transaction-api/  the HTTP service
  migrate/          database migrations, run as a deploy step
  outbox-relay/     publishes accepted transactions downstream
internal/
  config/       Environment configuration, validated at startup
  httpapi/      Routing, middleware, and HTTP-to-domain adapters
    gen/        Generated from api/openapi.yaml — never edited by hand
  platform/     Cross-cutting concerns: logging, health, metrics
docs/           Architecture, domain notes, runbook, and ADRs
```

Business rules never live in `internal/httpapi`. That package translates between
the wire contract and the domain, and nothing else.

### The contract is executable

`api/openapi.yaml` drives two things:

1. **Types and routing** are generated from it (`make generate`). The compiler
   will not let an operation in the spec go unimplemented.
2. **Every inbound request is validated against it at runtime**, before any
   handler sees the body.

Generated output is committed so the repo builds without a codegen toolchain,
and CI runs `make generate-check` to fail the build if it has drifted from the
spec. Spec and implementation cannot silently diverge.

Validation failures return the contract's `Error` shape with a field-level
breakdown:

```json
{
  "code": 400,
  "message": "request does not satisfy the API contract",
  "fields": "base_amount: is required; plate.jurisdiction: is required"
}
```

---

## Observability

The API serves `/metrics` on its main listener; the relay serves its own on
`:9090`. The metrics are chosen to answer questions an operator actually asks,
not to instrument everything countable:

| Metric | Answers |
|---|---|
| `ingest_transactions_total{source,result}` | Is a producer failing, and which one? |
| `ingest_validation_failures_total{source,layer,field}` | **Which rule** are they breaking? |
| `ingest_divergent_duplicates_total{source}` | Is someone re-sending a changed amount under a used key? Worth alerting on. |
| `outbox_pending_events` · `outbox_oldest_pending_age_seconds` | Is the resolution pipeline hearing about transactions? |

That last pair is the SLI for the outbox. The pattern makes *lost* events
impossible by converting them into *late* ones — these are how you find out
they are late.

Logs are JSON with request-id correlation. Plate and transponder values are
**never logged**; a redacting handler enforces it structurally on top of the
code simply not logging them, so a future mistake produces `[REDACTED]` rather
than a disclosure.

## The local stack

```bash
make compose-up     # postgres + migrations + api + relay
make compose-logs   # follow everything
make compose-down   # stop and remove volumes
```

Images are **distroless static** and run as non-root — 14.4 MB, no shell, no
package manager, no libc. There is nothing in the runtime image to exploit and
nothing to patch, which matters for a service on an ingest boundary.

That leaves no `curl` for a container healthcheck, so the binary probes itself:
`/app -healthcheck`. Kubernetes and ECS talk to `/healthz` and `/readyz`
directly and need none of that.

Migrations run as their own compose service that exits on completion, mirroring
production: a one-off task before the new revision is released, never inside a
service's startup path where rolling tasks would race the same DDL.

## Development

```bash
make test             # race detector on everything
make test-short       # unit tests only — no Docker required
make test-integration # PostgreSQL integration tests via testcontainers
make cover            # coverage report, enforced against a threshold
make lint             # golangci-lint, pinned to the same version CI uses
```

Integration tests start a real PostgreSQL container with
[testcontainers](https://golang.testcontainers.org/), because the behaviour
under test *is* database behaviour — the idempotency guarantee lives in a unique
constraint and `ON CONFLICT` semantics, and a mock would only assert that the
code calls the code.

<details>
<summary><strong>Troubleshooting:</strong> <code>unable to find network with name or ID bridge</code></summary>

If Podman is installed alongside Docker Desktop, testcontainers may resolve to
the Podman socket, which has no `bridge` network. Point it at Docker explicitly:

```bash
export DOCKER_HOST="unix://$HOME/.docker/run/docker.sock"
export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE="/var/run/docker.sock"
```

Check which endpoint is active with `docker context ls`.
</details>

`make ci` runs the full pipeline locally. If it passes on your machine it passes
in GitHub Actions — the Makefile is what the workflow calls.

### Branching

This repo uses **git flow**:

| Branch | Purpose |
|---|---|
| `main` | Production. Tagged releases only. |
| `develop` | Integration branch. Features merge here first. |
| `feature/*` | One branch per unit of work. |
| `release/*` | Release stabilisation. |
| `hotfix/*` | Urgent fixes off `main`. |

Branches are created with `git flow`, but merged through **GitHub pull requests**
rather than `git flow finish`, so every change gets CI and a reviewable diff.

---

## Roadmap

- [x] Service scaffolding, configuration, health probes, structured logging
- [x] CI: lint, race-detector tests with a coverage gate, build, vulnerability
      and secret scanning
- [x] OpenAPI-generated types with spec validation enforced at runtime
- [x] Transaction domain model and semantic validation
- [x] Idempotent ingest with divergence detection
- [x] Postgres persistence with database-enforced idempotency
- [x] Transactional outbox and relay
- [x] Prometheus metrics and PII-safe logging
- [x] Docker Compose stack with a one-command end-to-end demo
- [ ] Terraform deployment
- [ ] Architecture decision records and runbook
- [ ] AI agent configuration (`AGENTS.md`)

---

## License

[MIT](LICENSE)
