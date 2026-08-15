# ADR-0005: Error responses follow the contract's schema, not RFC 9457

**Status:** Accepted · **Date:** 2026-08-15

## Context

The contract defines errors as `{code: int32, message: string, fields: string}`.

My default is RFC 9457 Problem Details, and it is strictly better: typed URIs, a structured array of per-field failures, `application/problem+json` content negotiation, an IETF standard consumers already understand.

## Decision

**Follow the contract's schema.** Propose RFC 9457 for a future contract version rather than shipping it unilaterally.

Within the schema as given:

- `code` — the HTTP status. The spec does not say; documented as an assumption.
- `message` — a human-readable summary.
- `fields` — `"field: reason; field: reason"`, sorted and de-duplicated.

## Why

The contract is the deliverable. A producer already coded against this spec breaks if the error shape changes because the implementer preferred another one.

Silently "improving" someone else's published contract on an integration boundary is the wrong instinct. You propose it; you do not ship it and let them find out.

The sorting and de-duplication are not cosmetic: producers diff these strings in their own logs, so the same invalid payload must always produce byte-identical output.

## Consequences

Errors are less machine-readable than they could be. `fields` being a single string means multi-field failures are flattened into prose.

Real effort went into making that string useful anyway. `kin-openapi`'s error tree is walked into field-level detail, so a rejection reads `plate.jurisdiction: is required` rather than `request body has an error`, and format failures are rewritten from an inline validation regex into `must be a valid date-time`.

Two open questions for the team, recorded in `docs/assumptions.md`: whether `code` is something producers switch on (if so it needs a published registry, and mirroring the HTTP status wastes the field), and whether `fields` can become an array.
