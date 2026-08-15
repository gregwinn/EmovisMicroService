# Runbook

For whoever is on call. Each section is an alert, what it means, and what to do
about it — in that order, because at 3am you want the action, not the theory.

## At a glance

| Endpoint | Purpose |
|---|---|
| `GET /healthz` | Liveness. Answers if the process can serve HTTP. **Never** consults dependencies. |
| `GET /readyz` | Readiness. Runs every dependency check; `503` when any is down. |
| `GET /metrics` | Prometheus, on the API listener |
| `GET /metrics` on `:9090` | Prometheus, on the relay |

**Liveness and readiness are different on purpose.** A database blip should
drain an instance from the load balancer, not have the orchestrator restart a
process that is otherwise fine. If you find yourself pointing a liveness probe
at `/readyz`, stop: you have built a way to turn a brief database outage into a
restart storm.

---

## 🔴 `outbox_oldest_pending_age_seconds` is climbing

**Means:** accepted transactions are not reaching the resolution pipeline. They
are **not lost** — that is what the outbox guarantees — but revenue is not being
attributed while this continues.

**Suggested alert:** warn above 300s, page above 1800s.

1. Is the relay running at all?
   ```bash
   kubectl get pods -l app=outbox-relay      # or: aws ecs list-tasks --service outbox-relay
   ```
2. Check its logs for `outbox publish failed`. The `error` field names the
   cause.
3. Look at the split:
   ```sql
   SELECT count(*) FILTER (WHERE published_at IS NULL)          AS pending,
          count(*) FILTER (WHERE attempts >= 10)                AS parked,
          max(attempts)                                         AS worst,
          min(created_at)                                       AS oldest
   FROM outbox_events WHERE published_at IS NULL;
   ```

| What you see | Do |
|---|---|
| Relay not running | Start it. The backlog drains on its own; a non-empty pass retries immediately. |
| Broker unreachable | Fix the broker. Nothing is lost; back-pressure is the design working. |
| Lots of `parked` rows | See below. |
| Relay running, backlog flat, no errors | Suspect throughput. Raise `OUTBOX_BATCH_SIZE`, or run more replicas — `SKIP LOCKED` means they partition the work. |

**Do not delete rows from `outbox_events` to clear an alert.** Losing an
undeliverable event is precisely what this design exists to prevent.

## 🟠 Events are parked (`outbox_publish_total{result="parked"}` rising)

**Means:** an event exhausted its retries and is scheduled 24 hours out. It is
intact, not dropped.

```sql
SELECT event_id, event_type, aggregate_id, attempts, last_error, created_at
FROM outbox_events
WHERE published_at IS NULL AND attempts >= 10
ORDER BY created_at LIMIT 20;
```

Read `last_error`. Once the cause is fixed, requeue:

```sql
UPDATE outbox_events
SET next_attempt_at = now(), attempts = 0, last_error = NULL
WHERE event_id = '<event_id>';
```

The relay picks it up on its next pass. Requeueing is safe: delivery is
at-least-once and consumers deduplicate on `event_id`.

## 🟠 `ingest_divergent_duplicates_total` is rising

**Means:** a producer is sending **different content** under an idempotency key
it has already used. Either their defect, or an attempt to change an amount the
contract declares immutable.

**This never affects the response** — the producer got a normal `200` duplicate.
It is a revenue-integrity signal, not an outage.

1. Which producer, and how often?
   ```
   sum by (source) (rate(ingest_divergent_duplicates_total[1h]))
   ```
2. Find the detail. The `WARN` log carries both fingerprints and both amounts:
   ```
   msg="duplicate transaction diverges from the stored record"
   ```
3. If amounts differ, treat it as a revenue issue and involve the producer's
   integrator. If only formatting differs, that is a bug in the fingerprint —
   raise it, since cosmetic differences are supposed to be ignored.

Context: [ADR-0006](adr/0006-idempotency-divergence.md).

## 🟠 A producer's rejection rate jumped

**Means:** usually a producer deployed a change. Occasionally we did.

```
sum by (source, layer, field) (rate(ingest_validation_failures_total[15m]))
```

The `layer` label tells you whose problem it is:

| `layer` | Meaning |
|---|---|
| `contract` | The payload does not match the published schema. Their bug. |
| `semantic` | Schema-valid but unbillable. Look at `field`. |

A spike in `semantic` on `transaction_type` usually means **our** configuration:
the operator added a new billable event type and this deployment's
`TRANSACTION_TYPES` has not caught up. Fix the configuration, not the producer.

A spike on `base_amount` or the identifier rule is almost always theirs.

## 🔴 `/readyz` returning 503

Check which dependency:

```bash
curl -s localhost:8080/readyz | jq
```

The response names each check, its status, and its latency. Today that is the
database. Ingest returns `500` while it is down — producers retry, and
idempotency makes retrying safe.

## 🔴 Ingest returning 500s

```
sum by (source) (rate(ingest_transactions_total{result="error"}[5m]))
```

Look for `could not record transaction` in the logs. The `error` field carries
the cause; the client is told nothing, deliberately, because storage errors leak
infrastructure shape.

Almost always the database. Check `/readyz`, connection pool saturation
(`DATABASE_MAX_CONNS` against the server's `max_connections` divided by
instances), and disk.

---

## Routine operations

### Deploying

Migrations run as an explicit step **before** the new revision is released,
never at service startup — concurrent rolling tasks would race the same DDL.

```bash
migrate up          # one-off task, must succeed first
# then release the new revision
```

Migrations are append-only. The fix for a bad migration is another migration.

### Adding a transaction type

Update `TRANSACTION_TYPES` and restart, or update the operator reference data if
that is wired. No code change and no redeploy of the image:
[ADR-0008](adr/0008-runtime-configurable-transaction-types.md).

### Investigating one transaction

```sql
SELECT * FROM transactions
WHERE source = 'lane-controller-07' AND source_reference = 'LC07-20260814-000918';

-- what we told downstream about it, and when
SELECT event_type, created_at, published_at, attempts, last_error
FROM outbox_events WHERE aggregate_id = '<transaction id>';
```

`base_amount_text` is the producer's bytes verbatim; `plate_number` is the raw
read and `plate_number_key` is the canonical form. In a dispute, quote the raw
values.

### Draining an instance

`SIGTERM` triggers graceful shutdown: the listener stops accepting, in-flight
requests finish within `SHUTDOWN_TIMEOUT` (default 15s), then the process exits.
A rolling deploy is lossless as long as the grace period exceeds that.

---

## Things that look like incidents and are not

| Symptom | Reality |
|---|---|
| High `result="duplicate"` rate | Normal. Producers retry, and batch loaders replay whole files. That is the contract working. |
| Transactions with `transaction_time_utc` days old | Normal. Image-review vendors and batch loaders legitimately submit late. There is no posting window. |
| Plate values missing from logs | Intentional. They are PII and are redacted. Use the database. |
| `outbox_events` growing forever | Intentional. Published rows are retained as the record of what was sent and when. Archive on a schedule if the table gets large; do not delete unpublished rows. |
