# ADR-0004: Money is exact decimal, and the producer's text is preserved

**Status:** Accepted · **Date:** 2026-08-15

## Context

The contract is explicit: *"Monetary values are decimal strings ("12.50"), never floats."* It also states `base_amount` is *"immutable once accepted — later changes are recorded as separate adjustments, not by editing this value."*

`currency` is optional per transaction and defaults to the deployment's configured currency.

## Decision

Parse to an exact decimal (`shopspring/decimal`) carrying a resolved currency. Persist **both** `base_amount NUMERIC(19,4)` and `base_amount_text` — the producer's bytes, verbatim. No `float64` anywhere in a value path.

Parsing is stricter than the library's: exponent notation, a leading plus, and surrounding whitespace are rejected, and precision is counted from the original text so `"12.500"` in USD fails like `"12.505"` does.

## Why

The usual right answer for money in Go is an `int64` of minor units, and it was the first thing I reached for. It assumes the minor-unit exponent is fixed at compile time — and here it is not, because currency is resolved at runtime. Hard-coding "cents" would be wrong the first time a deployment crosses a border, which for an operator running schemes in several countries is a matter of when.

Keeping the raw text is an audit requirement, not tidiness. In a dispute you must show what the producer sent, not a value this service re-rendered.

Strict parsing matters on a billing boundary: `"1e5"` is a broken integration, not a $100,000 toll, and accepting more precision than a currency has means silently deciding how to round someone else's money.

## Consequences

Two columns for one concept, deliberately. `NUMERIC` for arithmetic and reporting, text for evidence.

A curated ISO-4217 table rather than the full register, covering exponent 0, 2, and 3 currencies so the "everything has cents" assumption cannot survive. Adding a currency is a one-line change.

Negative amounts *parse*; whether one is meaningful is policy the domain decides. A base rate must not be negative; an adjustment legitimately is.
