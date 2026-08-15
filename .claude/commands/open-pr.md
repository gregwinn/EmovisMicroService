---
description: Open a pull request into develop for the current branch
---

Open a pull request for the current branch into `develop`.

Before opening it:

1. Run `make ci`. Do not open a PR that you know will fail.
2. Run `git diff develop...HEAD --stat`. **If it touches more than one
   reviewable idea, stop and propose a split.** A PR a human cannot hold in
   their head gets rubber-stamped rather than reviewed.
3. Check `git status` for anything staged that does not belong to this change.

The description must cover:

- **What and why** — the problem, not the diff. The diff already says what.
- **Contract impact** — did `api/openapi.yaml` change? Is it backwards
  compatible for existing producers?
- **Judgement calls** — what you considered and rejected. If you followed the
  contract over a better design, say so and say why.
- **Verification** — name the tests and what they prove, not "added tests".
- **Notes for the reviewer** — anything you are unsure about, or deliberately
  left out.

Use `.github/PULL_REQUEST_TEMPLATE.md` as the skeleton and fill in every
section honestly. An unchecked box with an explanation is more useful than a
checked one that is not true.
