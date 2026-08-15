# Architecture

How the pieces fit, and why they are arranged this way.

## The shape of the problem

A producing system pushes one billable tolling transaction. The service has
three obligations, and every structural decision traces back to one of them:

| Obligation | Mechanism |
|---|---|
| **Never double-bill** | Idempotency on `(source, source_reference)`, enforced by a unique constraint |
| **Never lose an accepted transaction** | The record and its event commit together — [ADR-0007](adr/0007-transactional-outbox.md) |
| **Always explain a rejection** | Three validation layers, each naming the field and the reason |

## Request path

```
  producer
     │  POST /ingest/v1/transactions
     ▼
┌─────────────────────────────────────────────────────┐
│ transaction-api                                     │
│                                                     │
│  RequestID → Logger → Metrics → Recover             │  middleware
│                                                     │
│  1  contract validation      api/openapi.yaml       │  layer 1
│  2  wire → domain mapping    internal/httpapi       │
│  3  semantic rules           internal/transaction   │  layer 2
│                                                     │
│  4  ┌── one database transaction ──────────┐        │
│     │  INSERT transactions   (ON CONFLICT) │        │  layer 3
│     │  INSERT outbox_events                │        │
│     └──────────────────────────────────────┘        │
│                                                     │
│  201 created │ 200 duplicate │ 400 rejected         │
└─────────────────────────────────────────────────────┘
     │
     ▼  outbox_events
┌─────────────────────────────────────────────────────┐
│ outbox-relay                                        │
│   claim FOR UPDATE SKIP LOCKED → publish → mark     │
└─────────────────────────────────────────────────────┘
     │
     ▼  resolution pipeline (downstream, not in scope)
```

## Packages

```
api/openapi.yaml          the contract — source of truth
cmd/
  transaction-api/        the HTTP service
  migrate/                migrations, run as a deploy step
  outbox-relay/           drains the outbox
internal/
  config/                 environment configuration, validated at startup
  money/                  exact decimal amounts, ISO-4217 currencies
  transaction/            the domain: entity, rules, store interface
  store/memory/           non-durable, for tests and the quickstart
  store/postgres/         the real one
  httpapi/                routing, middleware, wire↔domain mapping
    gen/                  generated from the spec — never edited
  outbox/                 event contract, relay, publishers
  platform/               logging, health, metrics
db/migrations/            goose migrations, embedded
```

### The one rule about dependencies

`internal/transaction` does not import `internal/httpapi` or the generated
types. The domain declares its own `Submission` input, and the translation from
the wire format happens in exactly one file: `internal/httpapi/mapping.go`.

That file is the whole blast radius of a contract change. If a future spec
renames a field, the compiler points there and nowhere else. This is the
practical payoff of [ADR-0002](adr/0002-spec-as-enforced-contract.md) — without
it, "the spec is the source of truth" would mean generated types leaking into
the rules.

## Why the domain is the way it is

**Two orthogonal status axes**, not one. `association_status` answers *"whose is
it?"*; `settlement_status` answers *"what is its financial state?"*. Collapsing
them cannot represent an unidentified vehicle with a known toll amount — the
ordinary video-tolling case. The contract models it this way and explains why;
following it is the whole point.

**Semantic rules are pure functions.** No I/O, and the clock and id generator
are injected. That makes them exhaustively testable and readable by someone who
knows tolling but not Go — which matters, because they encode policy a domain
expert should be able to check.

**Identifiers are stored twice** — raw as evidence, canonical as a matching key.
See [ADR-0009](adr/0009-identifier-canonicalization.md).

**Money is stored three ways** — exact numeric for arithmetic, the producer's
text for the evidentiary record, and the currency because the minor-unit
exponent is not always 2. See [ADR-0004](adr/0004-exact-decimal-money.md).

## Why the outbox

The contract says the resolution pipeline picks transactions up asynchronously.
Publishing after the commit has a window that cannot be retried away: the commit
succeeds, the publish fails, and a billable transaction exists that nothing
downstream will ever see. No error is reported. It surfaces during
reconciliation, late and expensively.

Committing the event with the record removes the window. The failure mode
becomes a *late* event, which is recoverable and observable —
`outbox_oldest_pending_age_seconds` is the SLI.

The cost is at-least-once delivery, stated up front so consumers deduplicate on
`event_id` by design rather than discovering the need in production.

## Failure behaviour

| Failure | Response | Why |
|---|---|---|
| Malformed or schema-invalid body | `400` with field detail | The producer can fix it |
| Contract-valid but unbillable | `400` with field detail | Same, and it names the rule |
| Idempotency key already used | `200` with the stored record | The contract requires it |
| Same key, different content | `200`, plus a `WARN` and a counter | On contract, never silently discarded — [ADR-0006](adr/0006-idempotency-divergence.md) |
| Database unavailable | `500`, no detail leaked | Retry is safe; idempotency makes it safe |
| Panic in a handler | `500`, stack logged | One request must not take down a process serving others |
| Broker unavailable | Ingest unaffected | The relay backs off; events are durable |

**Liveness and readiness never share an implementation.** A database blip
should drain an instance from the load balancer, not have the orchestrator
restart a process that is otherwise fine.

## What is deliberately not here

Resolution, pricing, and collection. The contract is explicit: *"Accepting a
transaction is not the same as resolving it... Ingest's job is to durably
accept, validate, and acknowledge."*

Also absent, and noted rather than forgotten: capture-image handling
(out of contract), authentication ([ADR-0011](adr/0011-authentication-off-by-default.md)),
OpenTelemetry tracing, and a batch ingest endpoint — see
[assumptions.md](assumptions.md).
