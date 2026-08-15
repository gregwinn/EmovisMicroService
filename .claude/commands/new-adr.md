---
description: Record an architecture decision as a numbered ADR
argument-hint: <short title of the decision>
---

Create a new Architecture Decision Record for: **$ARGUMENTS**

1. Read `docs/adr/README.md` to find the next number in sequence.
2. Read two existing ADRs — `0006` and `0007` — to match tone and depth. They
   are the standard for this repo.
3. Create `docs/adr/NNNN-kebab-case-title.md` with:
   - `**Status:** Accepted · **Date:** <today>`
   - **Context** — the situation that forces a decision. Quote the contract in
     `api/openapi.yaml` where it is relevant; its prose is authoritative.
   - **Decision** — what was chosen, stated plainly.
   - **Why** — including what was *rejected* and why. This is the part that is
     expensive to reconstruct later, so it is the part worth writing.
   - **Consequences** — what this costs. Every decision costs something; an ADR
     with no downside section is marketing.
4. Add the ADR to the table in `docs/adr/README.md`.
5. Link it from any code whose behaviour it explains, as a `See docs/adr/...`
   comment.

Write it **before** implementing. A record written afterwards is a
justification, not a record.
