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

**Requirements:** Go 1.25+. Docker is needed for the integration tests and the
full local stack.

```bash
git clone git@github.com:gregwinn/EmovisMicroService.git
cd EmovisMicroService

make            # list every available target
make ci         # tidy, lint, test with coverage, build — everything CI runs
make run        # start the API on :8080
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

> **Note:** the default store is in-memory, so nothing survives a restart.
> Postgres replaces it behind the same interface.

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

---

## Repository layout

```
api/            OpenAPI contract — the source of truth for the wire format
cmd/            One directory per binary
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

## Development

```bash
make test         # race detector on everything
make cover        # coverage report, enforced against a threshold
make lint         # golangci-lint, pinned to the same version CI uses
```

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
- [ ] Postgres persistence (in-memory store today)
- [ ] Transactional outbox and publisher
- [ ] Metrics and PII-safe logging
- [ ] Docker Compose stack and Terraform deployment
- [ ] Architecture decision records and runbook
- [ ] AI agent configuration (`AGENTS.md`)

---

## License

[MIT](LICENSE)
