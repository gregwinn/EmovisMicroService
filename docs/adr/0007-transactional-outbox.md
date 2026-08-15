# ADR-0007: A transactional outbox carries events to the resolution pipeline

**Status:** Accepted · **Date:** 2026-08-15

## Context

The contract states that *"every accepted transaction is picked up asynchronously by a resolution pipeline."* There is a downstream consumer, and missing an event means a billable transaction is never attributed to a customer.

The obvious implementation is to publish from the request handler once the database commit succeeds.

## The problem with that

```
commit succeeds → publish fails → the transaction exists,
                                  and the pipeline never hears about it
```

No amount of retrying closes the window, because the process can die inside it. Nothing reports an error. It surfaces when someone reconciles takings against the road — which is to say, late and expensively.

## Decision

Write the transaction row and an `outbox_events` row in **one database transaction**. A separate `outbox-relay` process claims pending events with `FOR UPDATE SKIP LOCKED`, publishes them, and marks them published.

## Why

Atomicity means either both rows exist or neither does. No distributed transaction, no two-phase commit. The failure mode moves from **lost event** to **late event**, which is recoverable and observable.

`SKIP LOCKED` is what lets the relay scale horizontally: replicas claim disjoint sets rather than double-publishing or serializing behind each other.

A separate process means publishing pressure — a slow broker, a downstream outage — cannot slow down or fail ingest. The API's only job is to commit.

## Consequences

**Delivery is at-least-once.** The relay can publish and then fail to commit the marking. Redelivery is the safe direction, and every event carries a stable `event_id` so consumers can deduplicate. This is stated up front rather than left for a consumer to discover in production.

**Ordering is not guaranteed.** If resolution needs per-vehicle or per-facility ordering, that is SQS FIFO with a `MessageGroupId` — a decision to make *before* anything consumes this. Recorded in `docs/assumptions.md`.

**Outbox lag becomes an SLI.** `outbox_pending_events` and `outbox_oldest_pending_age_seconds` are how you learn events are late.

Failure handling keeps events rather than dropping them: a failed publish backs off exponentially, one poison event does not block its batch, and an event that exhausts its attempts is parked 24 hours out with its payload and last error intact. Deleting an undeliverable event would lose exactly what this design protects.

A second deployable to run and monitor. That is the price.
