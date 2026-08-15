---
description: Add a semantic validation rule to transaction ingest
argument-hint: <the rule, in plain language>
---

Add this semantic validation rule: **$ARGUMENTS**

First decide which layer it belongs in. Getting this wrong is a real mistake:

| Layer | Where | Use when |
|---|---|---|
| Contract | `api/openapi.yaml` | Types, required fields, lengths, formats |
| Semantic | `internal/transaction/validate.go` | A schema cannot express it |
| Referential | a database CHECK constraint | It must hold even if code bypasses the rules |

If it belongs in the contract, **stop and confirm first** — that is a change to
a published external contract, not an implementation detail.

For a semantic rule:

1. Add it to `internal/transaction/validate.go` as a small pure function
   returning `*RuleError`. No I/O, and no direct clock reads — use `Rules.Now`.
2. Wire it into `Rules.Accept`, appending to `problems` rather than returning
   early. Every failure is reported in one response.
3. Write the reason in terms a producer's integrator can act on. Compare
   `base_amount: has more than 2 decimal places, which USD does not allow`
   against `invalid amount`.
4. Add table-driven tests in `validate_test.go` covering accept and reject, and
   comment **why** the rule exists when the reason is not obvious from the name.
5. Add an HTTP-level case in `internal/httpapi/ingest_test.go` asserting the
   `400` and the field text.
6. Consider whether it should also be a database constraint. Application
   validation is the friendly message; the constraint is the guarantee.
7. Document it in `docs/api.md` under "What the schema cannot tell you".

Never leak operator configuration in the error message — see the
`transaction_type` rule for the pattern.

Then run `make ci`.
