---
description: Run the full pipeline and report what needs fixing
---

Run `make ci` and report the result.

It runs, in order: `tidy`, `generate-check`, `lint`, `cover`, `build`, `vuln`.

If something fails, diagnose it against the usual causes before proposing a fix:

| Failure | Usual cause |
|---|---|
| go.mod not tidy | `go get` recorded a dependency as indirect. Run `make tidy`. |
| generated code stale | `api/openapi.yaml` changed. Run `make generate` and commit. |
| lint | Read it properly — it has caught real defects here, not just style. |
| coverage below 85% | Write the test. **Never** lower the threshold. |
| govulncheck | Usually the Go toolchain patch in `go.mod`. |

Do not report success unless every stage passed. If a stage was skipped —
integration tests without Docker, say — say so explicitly rather than implying
full coverage.
