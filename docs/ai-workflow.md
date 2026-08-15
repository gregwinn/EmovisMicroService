# How AI Was Used to Build This

This service was built with heavy AI assistance — Claude Code, driving most of
the implementation, with me directing, reviewing, and making the calls.

This document is the honest account, including the parts that went wrong. It is
more useful than a list of features, and it is the thing I would actually want
to read if I were evaluating how someone works with these tools.

The companion document is [AGENTS.md](../AGENTS.md), which is the *forward*
half: the guardrails an agent must work within here.

---

## The headline

**AI is very good at producing plausible, idiomatic, well-tested code for the
wrong requirements** — and the tests pass, because they encode the same wrong
assumption.

That is not a hypothetical. It happened five times in the first hour, and it is
the single most important thing I learned worth passing on.

## The five wrong assumptions

The OpenAPI spec did not reach me with the brief; it arrived a little later. In
the meantime I planned against a provisional model built from general tolling
knowledge. When the real spec arrived, **five of my rules were wrong, and the
spec said so explicitly** — in its prose, not its schema.

| What I had | What the contract actually says |
|---|---|
| Reject transactions older than a 30-day posting window | *"May be well in the past for a batch or image-review replay"* |
| Validate plate jurisdiction against ISO 3166-2 | *"Plates arrive from anywhere, including off-network jurisdictions"* |
| `transaction_type` as a compiled Go enum | *"Operator-configurable at runtime, not compiled in"* |
| Money as `int64` minor units | *"Decimal strings ("12.50"), never floats"* — and currency is per-transaction |
| RFC 9457 Problem Details for errors | Their own `Error` schema: `{code, message, fields}` |

Three of those — the posting window, jurisdiction validation, the enum — are
what a *reasonable* engineer builds by default. They are correct in most billing
domains and wrong in this one. Every one would have shipped with green tests.

**The defence is not better prompting.** It is reading the source document
closely and treating the domain expert's prose as authoritative over your own
priors. In this spec, the `description` fields carried more design information
than the schemas did. An agent optimising for "make the types match" would have
skipped every one of those sentences.

## Where reasoning was confidently wrong

The `kin-openapi` error translation is the clearest example.

I wanted field-level rejections — `plate.jurisdiction: is required` rather than
`request body has an error`. To do that I had to walk the library's error tree,
and I reasoned about how it reported missing properties. That reasoning was
wrong twice:

1. I assumed `JSONPointer()` pointed at the *enclosing object* for a required
   failure, so I spliced the property name in from the reason text. It already
   points at the missing property. My "helpful" splice produced
   `base_amount.base_amount: is required`.
2. Same bug, nested: `plate.jurisdiction.jurisdiction: is required`.

Neither was caught by more reasoning. Both were caught in four minutes by a
throwaway test that printed what the library *actually* did for ten bad
payloads. That probe was the highest-value thing I did in that phase, and it is
the habit worth generalising: **print what the dependency does before asserting
what it does.**

A third finding from the same probe: format failures were dumping the entire
RFC 3339 validation regex into the response. Accurate, and useless to the
integrator reading it. Now rewritten as `must be a valid date-time`.

## Where the tooling earned its keep

Not everything went wrong. Being specific about what worked matters too.

**Volume with consistency.** Around 200 table-driven test cases, uniform in
shape, with the case name stating the behaviour. Writing those by hand is where
coverage quietly degrades from boredom.

**Boilerplate with a correct answer.** Graceful shutdown, the middleware chain,
the Makefile help target, CI YAML, the compose stack. Tedious, and there is a
right way to do each.

**Holding a wide surface at once.** When generated code entered the coverage
profile and dropped the total from 82% to 61%, the fix and the *opportunity* —
that excluding it meant the threshold could go **up**, not down — surfaced
together.

## Where the guardrails caught things

The pipeline is not decoration. It found real defects:

- **`gocritic`** flagged `defer stop()` after `os.Exit(1)` in `cmd/migrate`. The
  deferred call never runs. A genuine bug I had written and not noticed.
- **`govulncheck`** failed the build on five standard-library advisories,
  because the CI toolchain resolved to an older patch than the fixes. Not a
  defect in this code — the pipeline correctly reporting an out-of-date
  toolchain.
- **The coverage gate** failed twice on genuinely untested new code. Both times
  the answer was to write the test, not lower the bar.
- **A test failure I did not expect** was the best moment in the build. After
  wiring the handler, a case named `all_optionals_omitted` went red: expected
  201, got 400. It was **correct**. That fixture had no plate and no
  transponder — perfectly schema-valid, and completely unbillable. The layered
  validation had caught exactly the case the contract warns about. The fixture
  was wrong; the code was right.

## What I had to actively resist

The pull toward *impressive* rather than *appropriate*. At various points the
obvious next step was OpenTelemetry, `sqlc`, a router framework, a service mesh.
Each is defensible alone; together they would make the repo harder to read,
which is the stated evaluation criterion.

Related: the pull to build authentication because production will need it. The
contract declares `security: []` deliberately. Building a scheme on spec would
have been speculative code that gets replaced the moment I learn what they
actually use. The honest answer was to ask —
[ADR-0011](adr/0011-authentication-off-by-default.md).

## Process mistakes worth owning

Two, both mine, both about discipline rather than tooling:

1. **`git add -A` swept an unrelated file into a PR.** `identity.go` merged with
   the money change, without its tests and unmentioned in the description. Fixed
   by staging explicit paths from then on.
2. **I committed the handler onto the store PR's branch**, which would have
   doubled that PR's size. Split back out with `git branch` + `reset --hard` +
   force-push before merging.

Root cause of both: working ahead on untracked files while waiting for CI. The
fix is boring — finish one branch before starting the next.

## The guardrails that actually work

Distilled, and encoded in [AGENTS.md](../AGENTS.md):

1. **Read the source document closely.** Treat the domain expert's prose as
   authoritative over your own priors. The descriptions carry the design.
2. **Print what a dependency does before asserting what it does.** Reasoning
   about library internals is where confident wrongness lives.
3. **Write the decision record before the implementation.** Afterwards it is a
   justification.
4. **Keep gates that can fail, and let them.** Lint, coverage, vulnerability
   scanning, and contract-drift checks each caught something real here.
5. **State invariants an agent must not break**, in a file it will read. "Money
   is never a float", "never hand-edit generated code", "migrations are
   append-only" are cheap to write and expensive to discover the hard way.
6. **Prefer a clearly framed question over a silent guess.** The most useful
   output is often not code — it is
   [docs/assumptions.md](assumptions.md).

## Would I work this way again

Yes, with the caveat above. The productivity gain is real and large, and it is
concentrated in the parts of the work that are tedious rather than the parts
that are hard.

The judgement did not move. Deciding that a divergent replay must answer on
contract while still being recorded, that leading zeros must not be normalised,
that a posting window would reject real revenue — those came from reading the
contract and thinking about what the business actually loses when each rule is
wrong. The tooling made everything around those decisions faster; it did not
make them.
