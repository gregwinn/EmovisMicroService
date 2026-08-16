# Flows

The service in diagrams. Each one shows something the prose says at length, and
links to where the reasoning lives.

Rendered by GitHub directly — no images to regenerate when the code changes,
and the source is reviewable in a diff.

---

## The whole thing in one picture

One transaction, nothing going wrong. Everything below is a detail of this.

```mermaid
flowchart LR
    P["Producer<br/>roadside · vendor · peer"]
    A["Ingest API<br/>checks it"]
    DB[("Database")]
    R["Outbox relay"]
    Q(["Queue"])
    RES["Resolution pipeline<br/>downstream"]

    P -->|"1 · POST transaction"| A
    A -->|"2 · save transaction<br/>+ notify note<br/>ONE commit"| DB
    A -.->|"3 · 201 Created"| P
    DB -->|"4 · read pending notes"| R
    R -->|"5 · publish"| Q
    Q -->|"6 · consume"| RES
```

**Steps 1–3 are synchronous** — the producer waits and gets an answer.
**Steps 4–6 happen afterwards**, on their own, usually within a second or two.

The load-bearing detail is step 2: the transaction and the note telling the
pipeline about it are saved **together**. There is no moment where one exists
without the other, which is why a crash can delay a notification but never lose
one.

Step 4 is a poll, not a push — the relay asks the database for unpublished
notes every couple of seconds. Nothing has to tell it.

---

## 1. Who talks to what

Four producers, one endpoint, one downstream consumer. The resolution pipeline
is named because the contract names it, not because this service implements it.

```mermaid
flowchart LR
    LC["Roadside<br/>lane controller"]
    IR["Image-review<br/>vendor"]
    IP["Interoperability<br/>peer"]
    BL["Batch file<br/>loader"]

    API["transaction-api<br/>accept · validate · acknowledge"]
    DB[("PostgreSQL<br/>transactions + outbox_events")]
    RELAY["outbox-relay"]
    Q(["SQS"])
    RES["Resolution pipeline<br/>downstream — not in scope"]

    LC -->|"POST /ingest/v1/transactions"| API
    IR --> API
    IP --> API
    BL --> API

    API -->|"row + event, one commit"| DB
    RELAY -->|"claim, then mark published"| DB
    RELAY -->|"publish"| Q
    Q --> RES

    class RES faded
    classDef faded stroke-dasharray: 5 5
```

Two of the four producers submit **late** — image review and batch loading
happen well after the vehicle passed. Two of the four **resubmit** — retries
over unreliable links, and whole files replayed. That is why there is no
posting window and why ingest is idempotent.

→ [docs/domain.md](domain.md)

---

## 2. What happens to a request

```mermaid
sequenceDiagram
    autonumber
    participant P as Producer
    participant MW as Middleware
    participant CV as Contract validation
    participant H as Handler
    participant DB as PostgreSQL

    P->>MW: POST /ingest/v1/transactions
    MW->>CV: request id attached, access log and metrics armed

    alt payload does not match the published schema
        CV-->>P: 400 — every bad field named at once
    else schema satisfied
        CV->>H: decoded request
        H->>H: map wire to domain, apply semantic rules

        alt schema-valid but not billable
            H-->>P: 400 — names the rule that failed
        else acceptable
            H->>DB: BEGIN
            H->>DB: INSERT transactions ... ON CONFLICT DO NOTHING
            H->>DB: INSERT outbox_events
            H->>DB: COMMIT
            DB-->>H: committed together, or not at all
            H-->>P: 201 created
        end
    end
```

The handler never sees a malformed body: contract validation runs as
middleware, before it. And the transaction row and its event are one commit —
step 9 is the whole point of the outbox.

→ [docs/architecture.md](architecture.md)

---

## 3. Three layers of validation

The contract's own `400` description names failures no JSON Schema can
detect — *"no usable identifier, unrecognized transaction_type, or an
unparseable amount"* — which is a strong hint this shape is expected.

```mermaid
flowchart TD
    REQ["Inbound request"]

    L1{"Layer 1 — Contract<br/>api/openapi.yaml"}
    L2{"Layer 2 — Semantic<br/>internal/transaction"}
    L3{"Layer 3 — Referential<br/>database constraints"}
    OK(["Accepted and durable"])

    R1["400<br/>does not match the schema"]
    R2["400<br/>schema-valid, not billable"]
    R3["500<br/>a rule was bypassed in code"]

    REQ --> L1
    L1 -->|"types · required · lengths · formats"| L2
    L1 -->|fails| R1
    L2 -->|"an identifier is present · type is configured<br/>time is plausible · amount is exact"| L3
    L2 -->|fails| R2
    L3 -->|"unique idempotency key<br/>identifier CHECK · non-negative CHECK"| OK
    L3 -->|fails| R3

    class R1,R2,R3 reject
    class OK accept
    classDef reject fill:#8c1d18,stroke:#5f1410,color:#ffffff
    classDef accept fill:#1e6b3a,stroke:#124024,color:#ffffff
```

Layer 3 is not redundant. Application validation produces the *actionable
message*; the constraint is the *guarantee*. A future code path that skips the
rules still cannot write an unbillable row.

→ [ADR-0003](adr/0003-layered-validation.md)

---

## 4. 🔑 Idempotency, and the case the contract leaves open

The diagram worth the most attention. Both right-hand branches return the same
thing to the producer — and only one of them is silent.

```mermaid
flowchart TD
    IN["POST with (source, source_reference)"]
    INS{"INSERT ... ON CONFLICT DO NOTHING<br/>RETURNING id"}

    NEW["A row came back<br/>we created it"]
    EXIST["No row came back<br/>the key already existed"]

    FP{"Does the stored fingerprint match<br/>the submitted one?"}

    R201["201 · duplicate = false<br/>outbox event queued"]
    R200["200 · duplicate = true<br/>the stored record"]
    RDIV["200 · duplicate = true<br/>the stored record"]

    ALERT["WARN — both fingerprints, both amounts<br/>ingest_divergent_duplicates_total ++"]

    IN --> INS
    INS --> NEW --> R201
    INS --> EXIST --> FP
    FP -->|"same — an ordinary retry"| R200
    FP -->|"different content, same key"| RDIV
    RDIV -.-> ALERT

    class R201 accept
    class ALERT warn
    classDef accept fill:#1e6b3a,stroke:#124024,color:#ffffff
    classDef warn fill:#8a5a00,stroke:#5c3c00,color:#ffffff
```

The contract defines **no conflict status**, and a producer coded against it
has no branch for one — so a key match always answers as a duplicate. But a
differing payload under an existing key is either a producer defect or an
attempt to change an amount the contract calls immutable.

Answering `200` and dropping the difference on the floor is what a literal
reading produces, and it is the one option that is indefensible.

→ [ADR-0006](adr/0006-idempotency-divergence.md) — **the top thing to discuss**

---

## 5. An outbox event's life

```mermaid
stateDiagram-v2
    [*] --> Pending: written in the same commit as the transaction

    Pending --> Claimed: relay takes it with FOR UPDATE SKIP LOCKED

    Claimed --> Published: broker accepted it
    Claimed --> Failed: publish returned an error

    Failed --> Pending: next_attempt_at reached, backoff doubled
    Failed --> Parked: attempts exhausted

    Parked --> Pending: an operator requeues it

    Published --> [*]: row retained as the record of what we sent

    note right of Parked
        Parked 24h out, never deleted.
        Losing an undeliverable event is
        exactly what this design prevents.
    end note
```

`SKIP LOCKED` is what lets relay replicas partition the backlog instead of
double-publishing it or queueing behind each other. The cost of the whole
design is **at-least-once** delivery — consumers deduplicate on `event_id`.

→ [ADR-0007](adr/0007-transactional-outbox.md)

---

## 6. Two status axes, not one lifecycle

The sharpest piece of modelling in the contract, and the easiest to get wrong.

```mermaid
stateDiagram-v2
    direction LR
    [*] --> received
    received --> resolving
    resolving --> associated
    resolving --> exception
    note right of received
        association_status
        "whose is it?"
    end note
```

```mermaid
stateDiagram-v2
    direction LR
    [*] --> unpriced
    unpriced --> priced
    priced --> payable
    payable --> paid
    note right of unpriced
        settlement_status
        "what is its financial state?"
    end note
```

They advance **independently**. A single collapsed enum cannot represent an
unidentified vehicle with a known toll amount — and that is not an edge case,
it is video tolling, and a large share of revenue.

Every freshly ingested transaction is `received` **and** `priced`.

→ [docs/domain.md](domain.md)

---

## 7. What gets stored

```mermaid
erDiagram
    transactions {
        uuid id PK "UUIDv7 — time-ordered inserts"
        text source "idempotency key, part 1"
        text source_reference "idempotency key, part 2"
        text transaction_type "operator config, not an enum"
        timestamptz occurred_at "may be far in the past"
        timestamptz received_at "submission lag = the gap"
        text plate_number "raw read — evidence"
        text plate_number_key "canonical — for matching"
        text transponder_number "raw read"
        text transponder_number_key "canonical"
        numeric base_amount "for arithmetic"
        text base_amount_text "producer bytes — evidence"
        text currency "exponent is not always 2"
        jsonb location "passthrough"
        jsonb metadata "never interpreted"
        text association_status "whose is it"
        text settlement_status "financial state"
        char fingerprint "SHA-256 of billable content"
    }

    outbox_events {
        bigserial id PK "drain order"
        uuid event_id UK "consumers deduplicate on this"
        text event_type "transaction.received"
        uuid aggregate_id "the transaction it announces"
        jsonb payload "the published event body"
        timestamptz published_at "NULL until delivered"
        int attempts "drives the backoff"
        text last_error "why it failed"
        timestamptz next_attempt_at "when to retry"
    }

    transactions ||--o| outbox_events : "announced by"
```

Three things are stored twice on purpose:

| Stored twice | Why |
|---|---|
| Amount — `numeric` **and** the producer's text | One for arithmetic, one for the dispute |
| Plate — raw **and** canonical | One is evidence, one is a lookup key |
| Transponder — raw **and** canonical | Same, and leading zeros are deliberately preserved |

The relationship is **logical, not a foreign key**. Constraining it would
couple outbox retention to transaction retention, and they have different
lifetimes.

→ [ADR-0004](adr/0004-exact-decimal-money.md) ·
[ADR-0009](adr/0009-identifier-canonicalization.md)

---

## 8. Releasing a version

```mermaid
sequenceDiagram
    autonumber
    participant Dev as Maintainer
    participant CI as GitHub Actions
    participant REG as GHCR
    participant Task as ECS one-off task
    participant RDS as PostgreSQL
    participant Svc as ECS services

    Dev->>CI: push tag vX.Y.Z
    CI->>REG: 3 binaries × amd64/arm64, provenance + SBOM

    rect rgb(140, 90, 0)
        Note over Task,RDS: migrations run to completion FIRST
        Task->>RDS: migrate up
        RDS-->>Task: schema at target version
    end

    Dev->>Svc: terraform apply -var image_tag=vX.Y.Z
    Svc->>REG: pull the immutable tag
    Svc->>Svc: rolling deploy, circuit breaker armed
```

Migrations are **never** in the service's startup path. Rolling tasks would
race the same DDL, and a scale-up during an incident must not alter the schema.

No `latest` tag is published — a rollback needs a reference that cannot move,
and the Terraform rejects `latest` as an `image_tag` for the same reason.

→ [deploy/terraform/README.md](../deploy/terraform/README.md)

---

## 9. How changes reach `main`

Branches are created with `git flow`; they are **merged through pull requests**
rather than `git flow finish`, which merges locally and skips both CI and
review.

```mermaid
gitGraph
    commit id: "scaffold + CI"
    branch develop
    checkout develop
    branch feature/money
    commit id: "exact decimal"
    checkout develop
    merge feature/money
    branch feature/outbox
    commit id: "outbox + relay"
    checkout develop
    merge feature/outbox
    branch release/0.1.0
    commit id: "changelog"
    checkout develop
    merge release/0.1.0
    checkout main
    merge develop tag: "v0.1.0"
```

Features squash into `develop`, so it stays linear and each PR is one commit.
The release merges into `main` as a **merge commit**, so `main` carries the
whole history rather than one flattened blob.

→ [CONTRIBUTING.md](../CONTRIBUTING.md)
