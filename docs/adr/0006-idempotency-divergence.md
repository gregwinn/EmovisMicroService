# ADR-0006: A divergent replay answers on contract and is recorded

**Status:** Accepted · **Date:** 2026-08-15

## Context

Ingest is idempotent on `(source, source_reference)`. The contract specifies: a new push returns `201` with `duplicate=false`; a re-sent push that matches an existing key returns `200` with the existing record and `duplicate=true`.

It does **not** say what happens when the key matches but the payload differs — a different amount, a different plate. There is no `409` in the contract.

The brief said the spec *"leaves plenty unspecified on purpose"*. This is the most interesting gap in it.

## Options

| | Behaviour | Problem |
|---|---|---|
| A | `200`, silently discard the new payload | It is what a literal reading says. Loses a possible correction, and a producer defect goes unseen indefinitely. |
| B | `409 Conflict` | Off-contract. A buggy producer's retry storm becomes hard failures, and they have no code path for a status the spec never mentions. |
| C | Answer on contract, record the divergence | More moving parts. |

## Decision

**Option C.** Return `200` with the existing record and `duplicate=true`, exactly as the contract promises. Additionally:

- Compare a fingerprint of the billable content against the stored one.
- On divergence, log at `WARN` with both fingerprints and both amounts.
- Increment `ingest_divergent_duplicates_total{source}`, which is worth alerting on.

## Why

The response is what the producer's contract promises, so no integration sees a surprise status.

But a differing payload under an existing idempotency key is either a producer defect or an attempt to change an amount the contract declares immutable. Both are revenue-integrity events. Answering `200` and dropping the difference on the floor is the one option that is genuinely indefensible — and it is the one a literal reading of the spec produces.

The fingerprint hashes the transaction's **billable content**, not the raw bytes. Two producers, or the same producer after a library upgrade, can serialise identical data with different key ordering, whitespace, or timestamp offsets. Hashing bytes would flag all of those and bury the real signal in false positives.

## Consequences

Detection requires a stored fingerprint per transaction. It is one column.

**This is the top item to take to the team.** It is a contract question, not an implementation one: is a divergent replay a producer defect you want surfaced loudly, or an expected correction path that deserves a real amendment endpoint? The answer decides whether this stays a monitoring signal or becomes a feature.
