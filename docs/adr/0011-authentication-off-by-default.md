# ADR-0011: The endpoint stays unauthenticated, because the contract says so

**Status:** Accepted · **Date:** 2026-08-15

## Context

The operation declares `security: []` — explicitly no authentication. But the contract's own description says:

> In production this endpoint is authenticated, carries the legal capture images alongside the payload, and every accepted transaction is picked up asynchronously by a resolution pipeline. **None of that is specified here.**

So authentication exists in their platform and was deliberately trimmed from what I was given.

## Decision

Implement the contract as written: no authentication. Do not invent a scheme.

Document the recommendation, and keep the seam obvious — authentication is a middleware in the chain, and adding one is a contained change.

## Why

Adding required auth would make the service contradict its own published contract. Contract tests would fail, the README quickstart would return `401`, and a producer following the spec would be locked out. Guessing at a scheme they already have would also almost certainly guess wrong.

Inventing an optional-but-disabled scheme is only marginally better: it is speculative code, it needs tests and documentation, and it would be replaced the moment I learn what they actually use.

The honest engineering answer to "the spec omits authentication on purpose" is to ask what it is, not to build one on spec.

## What I would recommend

**mTLS**, not API keys. Roadside controllers and interoperability peers are known, long-lived systems rather than self-service signups. Client certificates give per-producer identity with a rotation and revocation story that shared secrets do not, and terminate cleanly at an ALB or service mesh.

Whatever the mechanism, it should **bind the credential to an allowed `source` set**. `source` is half the idempotency key and is currently self-declared: a misconfigured producer claiming another's `source` corrupts someone else's namespace.

## Consequences

The default deployment is unauthenticated. This is stated plainly in the README and in `docs/security.md` rather than left to be discovered.

Network-level controls carry the weight in the meantime: the service is not intended to be internet-facing without a gateway in front of it.

Recorded as an open question in `docs/assumptions.md` — what actually authenticates producers in production is the single answer that most changes the security posture.
