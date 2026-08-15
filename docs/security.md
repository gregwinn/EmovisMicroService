# Security

What this service protects, what it deliberately does not, and what I would
change before it took production traffic.

## ⚠️ Read this first

**The endpoint is unauthenticated.** The contract declares `security: []`, and
this implementation follows it rather than inventing a scheme —
[ADR-0011](adr/0011-authentication-off-by-default.md).

It is not intended to face the internet without a gateway in front of it. This
is stated here rather than left to be discovered.

## Data this service holds

| Data | Sensitivity | Handling |
|---|---|---|
| License plate number | **PII** — identifies a person and their movements | Stored raw (evidence) and canonical (matching). Never logged. |
| Plate jurisdiction | Low on its own | Stored as read; not validated against any list |
| Transponder id | **PII** — tied to a customer account | Stored raw and canonical. Never logged. |
| `location` | Low; combined with a plate it is a movement record | Stored verbatim as JSONB |
| `metadata` | **Unknown** — producer passthrough | Stored verbatim, never interpreted or logged |
| `base_amount` | Commercial | Stored exactly, plus the producer's original text |

`metadata` deserves a note. It is free-form and *"not interpreted"*, which means
a producer can put anything in it, including data nobody agreed to collect. It
is stored and never logged, but it is worth an explicit conversation with each
producer about what they actually send.

## PII in logs

Plate and transponder values must never reach log aggregation — typically the
least access-controlled and longest-retained place data goes.

Two layers enforce it:

1. **No code path logs them.** Rejections log field *names* and reasons, never
   values. Tests assert this for both accepted and rejected requests.
2. **A redacting `slog` handler** replaces sensitive attribute values at every
   nesting depth, including inside groups.

The second exists because the first depends on everyone remembering. If someone
adds `slog.String("plate_number", …)` in a hurry, the output is `[REDACTED]`
rather than a disclosure.

**The known limitation:** a value interpolated into a message string is not
caught. There is a test documenting exactly that, so it is a known boundary
rather than a false sense of safety.

The event published to the resolution pipeline **does** carry identifiers. That
pipeline exists to attribute vehicles and is inside the trust boundary. The rule
is about logs, not about the domain.

## Input handling

The service accepts data from systems it does not control.

| Concern | Mitigation |
|---|---|
| Malformed or hostile JSON | Validated against the OpenAPI contract before any handler runs |
| Oversized fields | `maxLength` from the contract, enforced by the validator |
| SQL injection | Parameterised queries throughout; no string-built SQL |
| Slowloris | `ReadHeaderTimeout` set explicitly, independent of `ReadTimeout` |
| Panic as denial of service | Recovery middleware; one request cannot take down a process serving others |
| Attacker-controlled log content | Inbound `X-Request-Id` is length-capped before it reaches a log record |
| Metrics cardinality attack | Route labels use the registered mux pattern; unmatched paths collapse to `unmatched`, so random URLs cannot create unbounded series |
| Error responses leaking internals | Storage and panic errors return a status and nothing else; detail goes to logs |

## Trust and identity

`source` is **self-declared**. It is also half the idempotency key.

A misconfigured — or malicious — producer claiming another's `source` can
collide with that producer's namespace: their transaction would be reported as a
duplicate of ours, or ours would suppress theirs. With no authentication there
is nothing preventing this.

Whatever authentication is added, it should **bind the credential to an allowed
`source` set** and reject mismatches. That closes the namespace problem, not
just the access one.

## Supply chain

- `govulncheck` runs in CI and fails the build on a known vulnerability in
  anything reachable from this code. It has already caught an out-of-date Go
  toolchain.
- The Go toolchain version is pinned in `go.mod`, so a security patch is a
  one-line bump that CI verifies.
- `gitleaks` scans history for committed secrets on every run.
- Dependabot proposes updates weekly, targeting `develop`.
- Runtime images are **distroless static, non-root**: no shell, no package
  manager, no libc. Nothing to exploit interactively and very little to patch.
- Dependencies are few and deliberate — see
  [ADR-0001](adr/0001-go-and-stdlib-http.md).

## What I would change before production

In priority order.

1. **Authenticate producers, with mTLS.** They are known, long-lived systems
   rather than self-service signups. Client certificates give per-producer
   identity with rotation and revocation that shared secrets do not, and
   terminate cleanly at an ALB or service mesh. Bind each certificate to an
   allowed `source` set.
2. **Rate-limit per producer.** A looping lane controller should degrade its own
   throughput, not everyone's.
3. **Encrypt at rest and restrict access.** RDS encryption is in the Terraform;
   what is missing is a policy on who can read the `transactions` table, because
   that table is a movement database.
4. **Define retention.** Plate reads should not be held forever. Tolling has
   statutory retention periods and they vary by jurisdiction — this needs a
   legal answer, not an engineering one.
5. **Audit reads, not just writes.** The write path is fully auditable today.
   Who *queried* a plate is not recorded, and for movement data that is usually
   the question an investigation actually asks.

## Reporting a vulnerability

This is an interview exercise, not a deployed system. If you find something in
it, please mention it directly — I would rather hear about it.
