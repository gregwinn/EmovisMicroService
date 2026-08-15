# ADR-0002: The OpenAPI spec is the enforced contract, not documentation

**Status:** Accepted · **Date:** 2026-08-15

## Context

Emovis supplied a real contract from their platform. External producers — roadside controllers, image-review vendors, interoperability peers — are coded against it.

In most services the spec is a sidecar: generated once, then hand-maintained. It drifts within a sprint, and the drift is discovered by a producer in production.

## Decision

`api/openapi.yaml` is the source of truth, and it is executable in two ways:

1. **Code generation.** `oapi-codegen` produces request/response models and a server interface. The compiler refuses to build if an operation in the spec has no implementation.
2. **Runtime validation.** `kin-openapi` middleware validates every inbound request against the embedded spec before any handler runs.

Generated code is committed. CI runs `make generate-check` and fails if it has drifted from the spec.

## Why

Drift between a published contract and its implementation is the most common integration failure with external producers, and it is entirely preventable. Making the spec executable removes the failure mode rather than documenting it.

Committing generated output means a reader can clone and build with no codegen toolchain — and the drift check means committing it cannot become a way to hide a stale spec.

`oapi-codegen` is pinned as a Go tool dependency in `go.mod`, so every machine and CI generate byte-identical output with nothing extra to install.

## Consequences

Changing behaviour means changing the spec first. That is the point, and it makes every contract change visible in review.

The spec is embedded in the binary rather than read from disk, so a running service cannot disagree with the contract it was built from, and there is no file to forget to ship in the image.

Validation costs a schema pass per request. It is negligible next to a database round trip, and it is what lets handlers assume a well-formed body.
