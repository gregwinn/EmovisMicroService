# What and why

<!-- What changes, and what problem it solves. Link the ADR if this changes a
     documented decision. -->

## Contract impact

<!-- Does this change api/openapi.yaml? If so, is it backwards compatible for
     existing producers? Delete this section if the contract is untouched. -->

- [ ] `api/openapi.yaml` unchanged, or the change is backwards compatible
- [ ] Generated code regenerated (`make generate`) and committed

## Verification

<!-- How you know this works. Name the tests, not just "tested". -->

- [ ] `make ci` passes locally
- [ ] New behaviour is covered by tests
- [ ] Migrations (if any) are append-only and safe to run against a live table

## Notes for the reviewer

<!-- Anything worth reading first, decisions you are unsure about, or things you
     deliberately left out. -->
