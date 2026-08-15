# ADR-0008: `transaction_type` is runtime configuration, not a compiled enum

**Status:** Accepted · **Date:** 2026-08-15

## Context

The contract says, verbatim:

> The kind of billable event. The set of accepted values is operator-configurable at runtime, not compiled in.

The field is typed as a plain `string` with an example of `toll` and no `enum`.

## Decision

A `transaction.TypeSet` loaded from operator configuration at startup, swappable in place while requests are in flight. Not a Go `const` block, not an OpenAPI `enum`.

Matching ignores case and surrounding whitespace, and the **operator's own spelling** is what gets stored.

## Why

They said so, and the reason is sound: a new billable event type must not require a redeploy of the ingest service. Modelling it as a compiled enum would show I read the field name and skipped the description.

Storing the operator's spelling rather than the producer's keeps one concept from being recorded three ways because three producers capitalise differently.

An unrecognized type does **not** echo the accepted set back to the caller. It is operator configuration, and on an endpoint the contract declares unauthenticated that would be a configuration disclosure.

## Consequences

An unknown type is a `400`, which the contract lists among its `400` cases.

Environment configuration is the seam, not the destination. In production this would be backed by the operator's reference-data service; `TypeSet` is already swappable in place, so that change touches the loader alone.

If the set is refreshed on a timer, a stale cache briefly rejects a newly added type. That is the safe direction to fail: a rejected transaction is retried by the producer, whereas an accepted one with an unknown type is a billing question nobody can answer.
