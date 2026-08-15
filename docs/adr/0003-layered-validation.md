# ADR-0003: Validation happens in three explicit layers

**Status:** Accepted · **Date:** 2026-08-15

## Context

"Accepts and validates a Transaction" is the assignment. In a billing system, *schema-valid* and *billable* are very different things.

The contract hints at this itself. Its `400` description names three failures no JSON Schema can detect: *"no usable identifier, unrecognized transaction_type, or an unparseable amount."*

## Decision

Three layers, each with a defined home:

| Layer | Where | Covers |
|---|---|---|
| **Contract** | `api/openapi.yaml`, enforced by middleware | Types, required fields, lengths, formats |
| **Semantic** | `internal/transaction/validate.go` | Rules a schema cannot express |
| **Referential** | Database constraints | The guarantee behind the friendly message |

Semantic rules are pure functions of the submission and operator configuration. No I/O; the clock and the id generator are injected.

## Why

Separating them makes the semantic rules exhaustively testable and readable by someone who knows tolling but not Go — which matters, because they encode domain policy that a domain expert should be able to check.

The database layer is not redundant. Application validation produces the actionable error message; the constraint is the guarantee. A future code path that bypasses the rules still cannot write an unbillable row.

## Consequences

Two validation passes per request, both cheap.

A rule has to be put in the right layer, and the wrong choice is a real mistake: a rule in the schema that belongs in the domain becomes a breaking contract change, and a rule in the domain that belongs in the database can be bypassed.

Failures from both layers are reported together within a layer, so a producer fixing an integration gets the whole list in one response rather than one per round trip.
