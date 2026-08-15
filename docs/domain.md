# The Domain

What a tolling transaction is, and which parts of that matter to this service.
Written for a developer who knows Go but has never worked on tolling.

## What a transaction is

> The transaction is the atomic unit of revenue in a tolling back office: it
> records that a vehicle used a facility at a point in time, identified by a
> transponder read and/or a license plate, at an as-received rate.
>
> — the contract, `api/openapi.yaml`

And the line that fences this service's scope:

> Accepting a transaction is not the same as resolving it. Who owns the vehicle,
> what the final price is, and whether it has been collected are downstream
> concerns. **Ingest's job is to durably accept, validate, and acknowledge.**

There is no pricing here, no account lookup, no attribution. Anything moving in
that direction belongs downstream.

## Who sends us transactions

The contract names four producers, and they fail in different ways. Most of the
design follows from this table.

| Producer | What it is | Characteristic that matters |
|---|---|---|
| **Roadside lane controller** | Equipment at the gantry | Real time, unreliable link, aggressive retries |
| **Image-review vendor** | Humans and OCR reading plate photos | **Late** — reads come back hours or days after the event |
| **Interoperability peer** | Another agency's back office | Foreign plate jurisdictions; bulk reciprocity files |
| **File-based batch loader** | Ingests a file of transactions | **Replays** — the same file loaded twice is a real operational event |

Two of the four routinely submit events with old timestamps. Two of the four
routinely resubmit. That is *why* the contract is idempotent, and *why*
rejecting old timestamps would be wrong.

## The two status axes

The sharpest piece of modelling in the contract, and the easiest to get wrong.

**`association_status`** — the *"whose is it?"* axis:

```
received → resolving → associated
                    ↘ exception
```

**`settlement_status`** — the *"what is its financial state?"* axis:

```
unpriced → priced → payable → paid
```

They are **orthogonal**, not one lifecycle. The contract says why: *"a
transaction can be priced but unattributed, or attributed and unpaid."*

A single collapsed `status` enum cannot represent **an unidentified vehicle with
a known toll amount** — and that is not an edge case. It is video tolling: a
camera read a plate, we know exactly what the toll is, and we do not yet know
whose it is. It is a large share of revenue.

Every freshly ingested transaction is `received` + `priced`. Priced on arrival
because the producer always supplies `base_amount`.

## Identifying a vehicle

A transaction must be attributable. The contract requires **at least one** of a
plate read or a transponder read:

> A payload with neither cannot ever be attributed to a customer.

Both fields are individually optional in the JSON Schema, so this rule cannot be
expressed there. It is the flagship example of why semantic validation is a
separate layer.

### Transponders

> The same physical tag is legitimately reported in several textual forms by
> different equipment.

`01800-1234 5678` and `0180012345678` are one tag. Canonicalization strips case
and separators so downstream matching is not doing string archaeology.

**Leading zeros are kept.** Deciding that `0180012345678` and `180012345678` are
the same tag is a claim about one agency's numbering plan, and getting it wrong
bills the wrong customer. That belongs downstream, with the reference data.

### Plates

> Plates arrive from anywhere, including off-network jurisdictions.

So jurisdictions are **not** validated. Rejecting an unrecognized one would
discard revenue from exactly the interoperability traffic the endpoint exists to
accept.

Every identifier is stored twice: raw as evidence for disputes, canonical as a
matching key.

## Time

> Always UTC — the `_utc` suffix is part of the field name on purpose. May be
> well in the past for a batch or image-review replay.

- **Old is fine.** There is no posting window. A transaction from last year is
  an ordinary batch replay.
- **Future is not.** A timestamp meaningfully ahead of now is clock skew or
  corruption. Bounded at 5 minutes — generously, because a false rejection
  discards real revenue.
- Any valid offset is accepted and normalized to UTC on storage.

## Money

> Monetary values are decimal strings ("12.50"), never floats.

Correct, and non-negotiable. Binary floating point cannot represent most decimal
fractions exactly; across millions of transactions those errors become real
money and unreconcilable ledgers.

`base_amount` is the **as-received rate** and is immutable once accepted.
Corrections arrive as separate adjustments, never by editing the value — which
makes the producer's original bytes part of the evidentiary record.

Currency is optional per transaction and defaults to the deployment's configured
currency. Its minor-unit exponent is **not always 2**: JPY has none, KWD has
three.

## Idempotency

> Ingest is idempotent on (source, source_reference). Producers retry — over
> unreliable links, after timeouts, and during file replays — and a retry must
> never create a second billable record.

- New key → `201`, `duplicate=false`
- Matched key → `200`, the existing record, `duplicate=true`

`source_reference` is unique **within** a source, so the key is the pair. Two
agencies both numbering from 1 must not collide.

The interesting case — a matched key with *different content* — is the gap the
contract leaves open. See
[ADR-0006](adr/0006-idempotency-divergence.md).

## Free-form fields

`location` and `metadata` are both `additionalProperties: true`. The contract
says *"shape varies by producer"* and *"preserved verbatim… not interpreted"*.

They are stored as JSONB and never modelled into columns. Over-modelling them
would be a mistake — the contract is saying that producers disagree about their
shape, and the next one will disagree differently.

## Glossary

| Term | Meaning |
|---|---|
| **AVI** | Automatic Vehicle Identification — a transponder read |
| **Video tolling** | Billing from a plate image when no transponder was read |
| **Gantry / plaza / lane** | Physical structure, toll point, and individual lane |
| **Interoperability** | Agencies honouring each other's transponders and exchanging transactions |
| **Reciprocity file** | A bulk exchange of transactions between agencies |
| **Association** | Attributing a transaction to a customer account |
| **Settlement** | Moving a transaction toward being paid |
| **Exception** | A transaction that could not be attributed and needs human review |
