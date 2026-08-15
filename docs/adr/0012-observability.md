# ADR-0012: Metrics answer operator questions; logs never carry identifiers

**Status:** Accepted · **Date:** 2026-08-15

## Context

Ingest runs unattended and at volume. Two things must be true: an operator can tell what producers are doing, and none of the data used to tell them is personally identifying.

License plates and transponder ids identify people and, in a tolling system, their movements. Log aggregation is typically the least access-controlled and longest-retained place data goes.

## Decision

**Prometheus metrics**, chosen to answer specific questions rather than to instrument everything countable:

| Metric | Answers |
|---|---|
| `ingest_transactions_total{source,result}` | Is a producer failing, and which one? |
| `ingest_validation_failures_total{source,layer,field}` | Which rule are they breaking? |
| `ingest_divergent_duplicates_total{source}` | Is content changing under a used idempotency key? |
| `outbox_pending_events`, `outbox_oldest_pending_age_seconds` | Is the resolution pipeline hearing about transactions? |

**Structured JSON logs** with request-id correlation, and a redacting `slog` handler that replaces sensitive attribute values at every nesting depth.

## Why

The validation breakdown is the one worth arguing for: it turns "producer X is broken" from a support ticket into a dashboard, and the `layer` label distinguishes their bug from our configuration.

Outbox depth and lag are the SLI for ADR-0007. That pattern makes lost events impossible by converting them into late ones — these are how you find out they are late.

Redaction uses slog's `ReplaceAttr` rather than a custom `Handler` because slog already applies it to every attribute at every depth, including inside groups. A hand-written handler would have to re-implement that traversal, and any gap would be a silent leak.

## Consequences

**Cardinality is a budget.** The route label is the registered mux pattern, never the raw path; anything unmatched collapses to `unmatched`. Field *names* are labels; field *values* never are — they are both PII and unbounded.

Redaction is defence in depth, not the primary control. A value interpolated into a message string is not caught, which is why no code path logs identifiers in the first place and tests assert it. A test documents that limitation explicitly.

Some debugging needs database access rather than logs. That is the correct trade for PII.

**OpenTelemetry tracing is the gap.** Metrics and logs cover a single service well; once the resolution pipeline is a real consumer, correlating a transaction across the outbox boundary wants traces. Noted, not built.
