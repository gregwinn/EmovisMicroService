# API

The contract is [`api/openapi.yaml`](../api/openapi.yaml) and it is
authoritative — this page explains behaviour the schema cannot state.

Runnable examples: [`requests.http`](requests.http) (VS Code REST Client,
JetBrains HTTP Client).

## `POST /ingest/v1/transactions`

Accepts one billable transaction from a producing system. Idempotent on
`(source, source_reference)`.

### Responses

| Status | When | Body |
|---|---|---|
| `201` | New transaction recorded | `IngestResult` with `duplicate=false` |
| `200` | The idempotency key already existed | `IngestResult` with `duplicate=true` and the **existing** record |
| `400` | The request cannot be accepted | `Error` |
| `500` | This service could not do its job | `Error`, no detail |

Every fresh transaction is `association_status: received` and
`settlement_status: priced`.

### A minimal accepted request

```bash
curl -s localhost:8080/ingest/v1/transactions \
  -H 'Content-Type: application/json' \
  -d '{
    "source": "lane-controller-07",
    "source_reference": "LC07-20260814-000918",
    "transaction_type": "toll",
    "transaction_time_utc": "2026-08-14T13:45:02Z",
    "base_amount": "12.50",
    "plate": { "number": "ABC1234", "jurisdiction": "TX" }
  }'
```

```json
{
  "id": "01a0074a-8687-7f3a-b83b-1d0e66f6121e",
  "association_status": "received",
  "settlement_status": "priced",
  "duplicate": false
}
```

Send it again and you get `200` with the same `id` and `duplicate: true`.

### Errors

The contract's `Error` shape. `fields` is a single string — sorted,
de-duplicated, and stable, because producers diff it in their own logs.

```json
{
  "code": 400,
  "message": "transaction failed validation",
  "fields": "base_amount: must not be negative; transaction_type: unrecognized value \"parking\""
}
```

`message` tells you which layer rejected it:

| `message` | Layer |
|---|---|
| `request does not satisfy the API contract` | The payload does not match the published schema |
| `transaction failed validation` | Schema-valid, but not billable |

## What the schema cannot tell you

These are the rules that turn a schema-valid payload into a billable one.

### At least one identifier is required

`plate` and `transponder_number` are each optional, but a payload with neither
is rejected:

```
at least one usable identifier is required: plate or transponder_number
```

"Usable" means it survives canonicalization — `"---"` is a present field and an
absent identifier.

### `transaction_type` is operator configuration

Not an enum in the schema, and not compiled in. An unrecognized value is a
`400`. The accepted set is **not** echoed back: it is operator configuration,
and this endpoint is unauthenticated.

Default deployment set: `toll`, `violation`, `fee`.

### `transaction_time_utc`: old is fine, future is not

| Timestamp | Result |
|---|---|
| Years in the past | ✅ Accepted — batch and image-review replays are legitimate |
| Up to 5 minutes ahead | ✅ Accepted — roadside clock drift |
| More than 5 minutes ahead | ❌ `400` |

Any valid RFC 3339 offset is accepted and normalized to UTC.

### `base_amount` is stricter than "a string"

| Value | Result |
|---|---|
| `"12.50"`, `"12.5"`, `"12"`, `"0.00"` | ✅ |
| `"-1.00"` | ❌ must not be negative |
| `"1e5"`, `"$12.50"`, `"1,250.00"`, `" 12.50"` | ❌ must be a decimal string |
| `"12.505"` in USD | ❌ more decimal places than the currency allows |
| `"350.5"` in JPY | ❌ JPY has no minor unit |

Precision is checked against the **resolved** currency, not an assumed two
places.

### `currency` is optional

Omitted means the deployment default (`USD` unless configured otherwise).
Matching is case-insensitive; an unrecognized ISO-4217 code is a `400`.

### `location` and `metadata` are yours

Free-form, stored verbatim, never interpreted. Nothing about their shape is
validated beyond being a JSON object.

## Idempotency in practice

The key is the **pair** `(source, source_reference)`. Two producers may both
number their transactions from 1 without colliding.

A retry is safe by construction — that is the point. It creates no second
record, and no second downstream event.

**Same key, different content** still returns `200` with the stored record,
because the contract defines no conflict status and a producer coded against it
has no branch for one. The divergence is logged and counted rather than silently
discarded. See [ADR-0006](adr/0006-idempotency-divergence.md).

## Operational endpoints

Not part of the producer-facing contract, and deliberately outside contract
validation.

| Endpoint | Purpose |
|---|---|
| `GET /healthz` | Liveness. Never consults dependencies. |
| `GET /readyz` | Readiness. `503` when a dependency is down. |
| `GET /metrics` | Prometheus |

## What downstream receives

Each accepted transaction produces one `transaction.received` event, written in
the same database transaction as the record and published by the relay.

```json
{
  "event_id": "01a0074a-868d-705d-a145-5e7b43376694",
  "event_type": "transaction.received",
  "transaction_id": "01a0074a-8687-7f3a-b83b-1d0e66f6121e",
  "source": "lane-controller-07",
  "source_reference": "LC07-20260814-000918",
  "transaction_type": "toll",
  "transaction_time_utc": "2026-08-14T13:45:02Z",
  "received_at": "2026-08-15T21:18:39.751989Z",
  "plate_number": "ABC-1234",
  "plate_jurisdiction": "tx",
  "plate_number_key": "ABC1234",
  "base_amount": "12.50",
  "currency": "USD",
  "association_status": "received",
  "settlement_status": "priced"
}
```

**Delivery is at-least-once and unordered.** Consumers must deduplicate on
`event_id`. Amounts are decimal strings at the currency's full precision, for
the same reason the inbound contract uses them.

## Authentication

There is none. The contract declares `security: []` and this implementation
follows it — [ADR-0011](adr/0011-authentication-off-by-default.md) and
[security.md](security.md).
