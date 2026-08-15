# Assumptions and Open Questions

The brief said: *"if you hit a decision you'd normally take to the team, make a
call, note it, and keep moving."* This is that list.

Every item here has a working default behind it. None of them is a blocker; all
of them are things I would rather hear your answer on than keep guessing at.

---

## Questions I would take to the team

Ordered by how much the answer would change the implementation.

### 1. A key match with a divergent payload

Same `(source, source_reference)`, different `base_amount`. The contract defines
no conflict status and says a match returns `200` with `duplicate=true`.

**Current behaviour:** contract-faithful `200`, and the divergence is logged at
`WARN` and counted. See [ADR-0006](adr/0006-idempotency-divergence.md).

**What I'd ask:** is this a producer defect you want surfaced loudly, or an
expected correction path that deserves a real amendment endpoint? That decides
whether the divergence signal stays a monitoring artifact or becomes a feature.

### 2. What authenticates producers in production?

The operation declares `security: []`, but the description says the endpoint is
authenticated in production.

**Current behaviour:** implemented as specified — no authentication. No scheme
invented. See [ADR-0011](adr/0011-authentication-off-by-default.md).

**What I'd ask:** mTLS, API keys, or network isolation? This is the single
answer that most changes the security posture. Related: `source` is currently
self-declared and is half the idempotency key, so whatever the mechanism, it
should bind a credential to an allowed `source` set.

### 3. What is `Error.code`?

Typed `int32` with no enum.

**Current behaviour:** the HTTP status.

**What I'd ask:** do producers switch on it? If so it needs a published registry
of codes, and mirroring the HTTP status wastes the field.

### 4. `Error.fields` is a string, not an array

Multi-field failures are flattened into `"field: reason; field: reason"`.

**What I'd ask:** is that shape load-bearing for existing producers, or can it
become an array? I would also propose RFC 9457 Problem Details for a future
contract version — happy to be told no.

### 5. How much clock drift do roadside controllers have?

**Current behaviour:** `transaction_time_utc` is rejected beyond **5 minutes**
in the future, and accepted at any age in the past.

**What I'd ask:** what is the real clock discipline on lane equipment? If
controllers can drift fifteen minutes, my threshold rejects real revenue — which
is much worse than accepting a slightly-future timestamp.

### 6. 📦 Batch ingest — one request per transaction does not fit a file loader

The contract says a producer *"pushes one transaction at a time"* and names **a
file-based batch loader** as a producer. Those two statements are in tension.

A nightly reciprocity file can carry hundreds of thousands of transactions. At
one request each that is hundreds of thousands of TLS handshakes and round trips
to move one file, where per-request overhead dwarfs the actual work. It is the
most obvious scaling cliff in the contract.

**Current behaviour:** implemented exactly as specified. No batch endpoint
invented.

**What I'd propose:** an additive `POST /ingest/v1/transactions:batch` taking an
array, with **per-item results** rather than all-or-nothing:

- the same idempotency key per item, so a replayed *file* is as safe as a
  replayed *request* — the whole point for this producer;
- per-item outcomes, because one malformed row in 200,000 must not reject the
  other 199,999;
- a bounded page size, so one request cannot monopolise a connection;
- **the same validation and outbox path per item** — batch is a transport
  optimisation, not a second code path. Two divergent ingest implementations is
  how billing bugs happen.

Being additive, existing single-push producers are untouched.

**What I'd ask:** do file loaders fan out to single pushes today, or does a
batch endpoint already exist that this trimmed spec omitted? And what is the
largest file? That number decides whether this is a real bottleneck or a
theoretical one.

### 7. Does the resolution pipeline need ordering?

**Current behaviour:** at-least-once, unordered. Consumers must be idempotent on
`event_id`.

**What I'd ask:** per-vehicle or per-facility ordering? If yes, that is SQS FIFO
with a `MessageGroupId`, and it is a decision to make *before* anything consumes
this rather than after.

### 8. Is a deployment ever multi-currency?

**Current behaviour:** one configured default currency per deployment, applied
when a producer omits `currency`.

**What I'd ask:** Emovis runs schemes in several countries. If one deployment
ever spans currencies, the default belongs on the facility or the producer, not
the deployment.

---

## Assumptions made and moved on from

| # | Assumption | Basis | Risk if wrong |
|---|---|---|---|
| A1 | Validation failures are `400`, not `422` | The spec defines only 200/201/400 | Low — following the contract |
| A2 | `id` is a UUIDv7 string | Spec says `type: string`, no format | Low — opaque to producers |
| A3 | Unknown plate jurisdictions are accepted | Spec: *"plates arrive from anywhere"* | Low — explicitly stated |
| A4 | A negative `base_amount` is invalid | It is an as-received *rate*; corrections are separate adjustments per the spec | **Medium** — wrong if credits arrive via ingest |
| A5 | `location` and `metadata` are stored verbatim, uninterpreted | Spec: *"shape varies"*, *"not interpreted"* | Low |
| A6 | Amount precision must fit the currency exponent | Standard billing hygiene | **Medium** — a producer sending `"12.500"` gets a 400 |
| A7 | A non-UTC offset is accepted and normalized | Liberal in, strict out | Low |
| A8 | AWS is the target cloud | Terraform targets ECS/Fargate | **Medium** — see below |
| A9 | One transaction per request | Spec: *"one transaction at a time"* | Low — but see question 6 |
| A10 | Capture images are out of scope | Spec: *"none of that is specified here"* | Low |
| A11 | `transaction_type` matching is case-insensitive | Producers are inconsistent; rejecting `TOLL` would be pedantry | Low |

---

## Context I built without

**I did not have the job description or the notes from our first conversation
while building.** The brief asked me to align with your stack and problem space.

I aligned with the **problem space** — the spec is detailed enough to do that
well, and most of the decisions here come directly from reading it closely. I
made my own call on the **stack**.

If you are a Java or .NET shop, Go is a divergence I would rather own than
defend. The architecture — spec-as-contract, layered validation, the outbox,
the idempotency semantics — ports to any of those runtimes essentially
unchanged, which is the argument I would make for it.

---

## Deliberate cuts

Not oversights. Each is a scope call against the stated few-hours constraint.

- **No resolution or pricing logic** — explicitly downstream per the contract.
- **No capture-image handling** — explicitly out of contract.
- **No authentication** — [ADR-0011](adr/0011-authentication-off-by-default.md).
- **No OpenTelemetry tracing** — metrics and structured logs cover a single
  service; tracing earns its place once the resolution pipeline is a real
  consumer.
- **No live AWS deployment** — the Terraform is written and validates in CI, but
  nothing is applied and no credentials exist in a public repo.
- **No batch endpoint** — question 6 above.
- **No admin API for transaction types** — seeded from configuration; a real
  operator reference-data service is the production answer, and the loader is
  the only thing that would change.
