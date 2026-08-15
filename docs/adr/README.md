# Architecture Decision Records

Short records of the decisions behind this service, and the reasoning that
produced them. Each is one page: context, the decision, and what it costs.

They exist because the *why* is the part that is expensive to recover later. The
code says what the service does; these say what else was considered and why it
was not chosen.

| ADR | Decision |
|---|---|
| [0001](0001-go-and-stdlib-http.md) | Go with the standard library HTTP server |
| [0002](0002-spec-as-enforced-contract.md) | The OpenAPI spec is the enforced contract, not documentation |
| [0003](0003-layered-validation.md) | Validation happens in three explicit layers |
| [0004](0004-exact-decimal-money.md) | Money is exact decimal, and the producer's text is preserved |
| [0005](0005-error-contract-fidelity.md) | Error responses follow the contract's schema, not RFC 9457 |
| [0006](0006-idempotency-divergence.md) | **A divergent replay answers on contract and is recorded** |
| [0007](0007-transactional-outbox.md) | **A transactional outbox carries events to the resolution pipeline** |
| [0008](0008-runtime-configurable-transaction-types.md) | `transaction_type` is runtime configuration, not a compiled enum |
| [0009](0009-identifier-canonicalization.md) | Identifiers are canonicalized, and the raw read is kept |
| [0010](0010-postgres-without-an-orm.md) | PostgreSQL with hand-written SQL, no ORM |
| [0011](0011-authentication-off-by-default.md) | The endpoint stays unauthenticated, because the contract says so |
| [0012](0012-observability.md) | Metrics answer operator questions; logs never carry identifiers |

**If you only read two:** ADR-0006 is the gap the contract deliberately leaves
open and how it is closed. ADR-0007 is why the resolution pipeline cannot miss
a billable transaction.
