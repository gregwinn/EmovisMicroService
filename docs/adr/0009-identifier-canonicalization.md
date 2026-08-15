# ADR-0009: Identifiers are canonicalized, and the raw read is kept

**Status:** Accepted · **Date:** 2026-08-15

## Context

The contract is specific about both identifiers:

> **Transponder:** the same physical tag is legitimately reported in several textual forms by different equipment.
>
> **Plate:** plates arrive from anywhere, including off-network jurisdictions.

## Decision

Store every plate and transponder **twice**: the producer's bytes verbatim, and a canonical form for matching. Canonicalization upper-cases and strips everything that is not a letter or digit.

Reject neither. An unrecognized format is still a real read.

**Leading zeros on transponders are deliberately preserved.**

## Why

Case and punctuation carry no identity — `01800-1234 5678` and `0180012345678` are unambiguously the same tag, and normalising them stops downstream doing string archaeology.

Leading zeros are different in kind. Deciding that `0180012345678` and `180012345678` are one tag is a claim about a specific agency's numbering plan. Getting it wrong merges two vehicles' transactions and bills the wrong customer. Resolving tag identity belongs downstream, where the agency reference data lives; ingest's job is to remove *avoidable* variation, not to guess.

Jurisdictions are not validated against any list. Rejecting an unrecognized one would discard revenue from exactly the interoperability traffic this endpoint exists to accept. This was on my first-draft rule list until I read that line properly.

Keeping the raw read is evidentiary: a dispute needs what the equipment actually reported.

## Consequences

Two columns per identifier, indexed on the canonical form.

The fingerprint used for divergence detection hashes the **raw** reads, not the keys: a producer changing `ABC1234` to `ABC-1234` has changed the evidence, and that is worth noticing even though both canonicalize alike.

Normalization rules are policy and will evolve. They are pure functions with table-driven tests, so changing them is cheap and reviewable — and one test exists specifically to stop a future "helpful" change from stripping leading zeros.
