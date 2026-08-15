# ADR-0001: Go with the standard library HTTP server

**Status:** Accepted · **Date:** 2026-08-15

## Context

Ingest is a strict-schema, high-throughput, low-latency write path. Roadside producers burst and retry, and the service must stay predictable under both.

The routing surface is one business endpoint plus operational probes.

## Decision

Go 1.25, using `net/http` with Go 1.22+ pattern routing. No chi, gin, or echo.

## Why

A router dependency buys nothing for three routes and costs a reader a hop: to understand how a request reaches a handler you have to know the framework as well as the code. A ~30-line middleware chain covers what a framework's middleware stack would.

Fewer dependencies is a real property for a service that handles money. Every one is a supply-chain surface and a thing to keep patched.

Go itself fits the workload: predictable latency without a GC pause budget to tune, a static binary in a 14 MB container, and first-class concurrency for a service whose failure mode is a burst of concurrent retries.

## Consequences

If the surface grows past roughly ten routes, adopt `chi`. Handler signatures are already `http.HandlerFunc` and the middleware type is `func(http.Handler) http.Handler`, so it is a contained change rather than a rewrite.

Some conveniences are hand-written: the middleware chain, the response recorder that captures status for logging and metrics. They are about 60 lines in total and are testable in isolation.
