# AGENTS.md

Instructions for AI coding agents working in this repository.

Humans should read [README.md](README.md) first; this file assumes it. Everything here is also true for humans — it is written for agents because agents need it stated rather than inferred.

---

## What this service is

A microservice that accepts billable tolling transactions pushed by roadside lane controllers, image-review vendors, interoperability peers, and batch file loaders.

It has **three obligations**, and every design decision in the repo traces to one of them:

1. **Never double-bill.** Ingest is idempotent on `(source, source_reference)`.
2. **Never lose an accepted transaction.** The record and the event announcing it commit together.
3. **Always explain a rejection.** A `400` names the field and the reason.

It deliberately does **not** resolve vehicle ownership, price transactions, or collect payment. Those are downstream. If a change starts moving in that direction, stop and ask.

---

## 🛑 Invariants

These are not style preferences. Breaking one is a defect, and in most cases a defect that costs money and is invisible until reconciliation.

### 1. `api/openapi.yaml` is the source of truth

It is the published contract that external producers are coded against.

- **Never** hand-edit `internal/httpapi/gen/` — it is generated. Change the spec, then run `make generate`.
- Changing the spec changes an external contract. Treat any change as breaking until proven otherwise, and say so in the PR.
- CI fails if committed generated code has drifted (`make generate-check`).

### 2. Money is never a float

`float64` must not appear anywhere in a value path. Use `internal/money`.

The contract sends amounts as decimal strings for this reason. `base_amount_text` stores the producer's original bytes and must never be regenerated from the numeric column — it is evidence in a billing dispute.

### 3. Migrations are append-only

Never edit a migration that has been applied anywhere. Environments then silently disagree about their schema. The fix for a bad migration is another migration.

Migrations never run at service startup. A rolling deploy would race them.

### 4. Identifiers are PII

License plate numbers and transponder ids identify people and their movements.

- Never log them, at any level. Log field *names* and reasons, never values.
- `internal/httpapi/ingest_test.go` asserts they stay out of logs. If that test fails, the change is wrong — do not adjust the test.

### 5. Validation has three layers; put a rule in the right one

| Layer | Where | For |
|---|---|---|
| Contract | `api/openapi.yaml`, enforced by middleware | Types, required fields, lengths, formats |
| Semantic | `internal/transaction/validate.go` | Rules a schema cannot express |
| Referential | Database constraints | The guarantee behind the friendly message |

Semantic rules must stay **pure functions** — no I/O, no direct clock reads. `Rules.Now` and `Rules.NewID` are injected so tests can pin them.

### 6. `transaction_type` is runtime configuration

The contract states the accepted values are *"operator-configurable at runtime, not compiled in."* Do not turn it into a Go `const` block or an OpenAPI `enum`, however tempting. Use `transaction.TypeSet`.

### 7. Error responses follow the contract's `Error` schema

`{code, message, fields}`. RFC 9457 Problem Details is a better shape and is deliberately not used — see [ADR-0005](docs/adr/0005-error-contract-fidelity.md). Do not "improve" it.

### 8. A key match always answers as a duplicate

The contract defines no conflict status. A divergent replay returns `200` with the stored record and is **logged**, never rejected and never silently discarded. See [ADR-0006](docs/adr/0006-idempotency-divergence.md).

---

## Layout

```
api/openapi.yaml          the contract — source of truth
cmd/                      one directory per binary
  transaction-api/        the HTTP service
  migrate/                database migrations, run as a deploy step
  outbox-relay/           publishes accepted transactions downstream
internal/
  config/                 environment configuration, validated at startup
  money/                  exact decimal amounts and ISO-4217 currencies
  transaction/            the domain: entity, rules, store interface
  store/
    memory/               non-durable, for tests and the quickstart
    postgres/             the real one
  httpapi/                routing, middleware, wire↔domain mapping
    gen/                  GENERATED — never edit
  outbox/                 transactional outbox and publishers
  platform/               logging, health, metrics
db/migrations/            goose migrations, embedded
docs/adr/                 why things are the way they are
```

**Dependency rule:** `internal/transaction` must not import `internal/httpapi` or `internal/httpapi/gen`. The domain owns its own input type; the mapping lives in `internal/httpapi/mapping.go` and nowhere else. That one file is the entire blast radius of a contract change — keep it that way.

---

## Commands

```bash
make                  # list every target
make ci               # everything CI runs: tidy, generate-check, lint, cover, build, vuln
make test-short       # unit tests only — no Docker needed
make test             # everything, including PostgreSQL integration tests
make generate         # regenerate from api/openapi.yaml
make migrate          # apply migrations (needs DATABASE_URL)
```

**Run `make ci` before opening a PR.** Not `make test` — the full target catches the tidy, drift, lint, coverage, and vulnerability failures that would otherwise fail in the pipeline.

---

## Working conventions

### Tests

- Table-driven, with the case name saying what behaviour is being asserted.
- Comment **why** a test exists when the reason is not obvious from its name. `TestTransponderLeadingZerosArePreserved` has a comment explaining that merging two tags bills the wrong customer; that comment is the point of the test.
- Integration tests use real PostgreSQL through testcontainers. Do not replace them with mocks — the behaviour under test *is* database behaviour.
- Coverage is gated at 80%. If a change drops it, write the test. Do not lower the threshold.

### Commits and pull requests

- Conventional Commits: `feat(scope):`, `fix(scope):`, `chore(scope):`, `docs(scope):`.
- The body explains **why**, not what. The diff already says what.
- **Keep PRs small** — one reviewable idea each. If a change touches a value type, the rules that use it, and the handler that calls those, that is three PRs.
- Stage explicit paths. `git add -A` sweeps unrelated work into the wrong PR.

### Branching

git flow, with branches created by `git flow feature start <name>` but merged through **GitHub pull requests** rather than `git flow finish`, so every change gets CI and a reviewable diff.

`main` is production and tagged; `develop` is the integration branch; features branch from and merge to `develop`. Both protected branches require passing checks.

---

## When you hit an unspecified case

The spec leaves things open on purpose, and this repo's answer is never to guess silently.

1. **Follow the contract** where it speaks. Contract fidelity beats your preferred design on an integration boundary.
2. **Where it is silent, make a defensible call** and write it down as an ADR in `docs/adr/`.
3. **Never silently discard data.** If something cannot be represented in the response, record it — a log, a metric, a table.
4. **Say what you assumed** in the PR description, and add it to `docs/assumptions.md`.

The most useful thing an agent can produce here is not code but a clearly framed question with a working default behind it.

---

## Definition of done

- [ ] `make ci` passes
- [ ] New behaviour has tests, and the test names say what behaviour
- [ ] No new `float64` in a money path, no identifiers in logs
- [ ] `api/openapi.yaml` unchanged, or the change is documented as contract-affecting
- [ ] Generated code regenerated and committed if the spec changed
- [ ] Any new decision recorded as an ADR
- [ ] Any assumption recorded in `docs/assumptions.md`
- [ ] PR description explains **why**, and is small enough for a human to read in one sitting
