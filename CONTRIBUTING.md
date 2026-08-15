# Contributing

## Getting set up

```bash
make            # every available target
make test-short # unit tests — no Docker needed
make ci         # everything the pipeline runs
```

`make ci` is the gate. It runs tidy, contract drift, lint, race-detector tests
behind a coverage threshold, build, and a vulnerability scan. If it passes
locally it passes in GitHub Actions, because the workflow calls the same
targets.

Docker is needed for the integration tests and `make demo`. If testcontainers
reports `unable to find network with name or ID bridge`, see the troubleshooting
note in the [README](README.md#development).

## Branching

We use **git flow**, with one deliberate deviation.

| Branch | Purpose |
|---|---|
| `main` | Production. Tagged releases only. |
| `develop` | Integration. Features merge here first. |
| `feature/*` | One unit of work. |
| `release/*` | Release stabilisation. |
| `hotfix/*` | Urgent fixes off `main`. |

```bash
git flow feature start my-change
# ... work ...
git push -u origin feature/my-change
gh pr create --base develop
```

**The deviation:** branches are *created* with `git flow`, but merged through
**GitHub pull requests** rather than `git flow feature finish`. `finish` merges
locally, which skips CI and leaves no reviewable diff. Both protected branches
require passing checks, so the PR route is the only one that actually works —
and it is what you would want anyway.

## Pull requests

**Keep them small.** One reviewable idea each. If a change touches a value type,
the rules that use it, and the handler that calls those, that is three PRs.

A PR is a communication artifact, not just a merge mechanism. If a reviewer
cannot hold the diff in their head, review degrades into rubber-stamping.

The description should explain **why**. The diff already says what. If you made
a judgement call, say what you considered and rejected — that is the part which
is expensive to reconstruct in six months.

Stage explicit paths. `git add -A` sweeps unrelated work into the wrong PR.

### Commits

Conventional Commits: `feat(scope):`, `fix(scope):`, `chore(scope):`,
`docs(scope):`. The body explains the reasoning.

## Things that will fail CI

| Check | Fix |
|---|---|
| `go.mod` not tidy | `make tidy` and commit the result |
| Generated code stale | `make generate` and commit — the spec changed |
| Lint | `make lint`. It has caught real defects, not just style. |
| Coverage below 85% | Write the test. Do not lower the threshold. |
| `govulncheck` | Usually a Go toolchain bump — one line in `go.mod` |
| `gitleaks` | A secret reached a commit. Rotate it, then rewrite history. |

## Rules that are not negotiable

The full list is in [AGENTS.md](AGENTS.md) — it applies to humans as much as to
agents. The short version:

1. **`api/openapi.yaml` is the source of truth.** Never hand-edit
   `internal/httpapi/gen/`. Changing the spec changes an external contract.
2. **Money is never a float.** Use `internal/money`.
3. **Migrations are append-only.** The fix for a bad migration is another
   migration.
4. **Plate and transponder values are never logged.** Tests assert it.
5. **Semantic rules stay pure.** No I/O; the clock and id generator are
   injected.

## Decisions

Anything non-obvious gets an ADR in [`docs/adr/`](docs/adr/). One page: context,
decision, consequences. Write it *before* the implementation — a decision record
written afterwards is a justification, not a record.

Anything you had to assume goes in
[`docs/assumptions.md`](docs/assumptions.md), phrased as the question you would
have asked.

## Working with AI agents

This repo is set up for them: [`AGENTS.md`](AGENTS.md) states the invariants and
conventions, and `.claude/` carries the tooling configuration.

The guardrails exist because the failure mode of AI-assisted development is not
bad code — it is plausible, idiomatic, well-tested code built for the wrong
requirements, where the tests encode the same wrong assumption. See
[`docs/ai-workflow.md`](docs/ai-workflow.md) for what that looked like in
practice here, including the parts that went wrong.
