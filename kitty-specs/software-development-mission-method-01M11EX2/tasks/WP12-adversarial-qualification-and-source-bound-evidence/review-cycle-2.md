---
affected_files: []
cycle_number: 2
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-28T20:00:28Z'
reviewer_agent: user
wp_id: WP12
---

# WP12 review feedback — cycle 2

**Verdict:** rejected

**Reviewer:** Reviewer Renata, independent of the implementation process

Cycle 2 substantially repaired the first review: the schema-v2 payload,
source/payload/attestation identities, checksums, race/fault/projection evidence,
supporting profile identities, and published failure records are internally
consistent. Focused repeat/race/static/compile gates pass.

## Blockers

Nine disposable publication mutants still survived the actual validator:

1. deletion of `review-cycle-1`;
2. deletion of attempt `20`;
3. deletion of attempt `21`;
4. deletion of attempt `real-23`;
5. malformed Mermaid body `Source -->`;
6. a documented nonexistent non-Make command;
7. a duplicate top-level JSON key;
8. more than 2 MiB of trailing JSON whitespace; and
9. a clean product commit after the named attestation while validating against
   the historical attestation ID.

### Required repair

- Make every published failure-history identity load-bearing and add a permanent
  deletion mutant for each one.
- Bind a clean checkout's exact `HEAD` and tree to the asserted attestation.
  Add later-product, later-doc, and later-empty-commit mutants.
- Reject duplicate JSON keys and explicitly reject evidence files over the size
  limit rather than silently truncating the decoder input.
- Validate Mermaid body syntax and every documented executable command through
  a closed rule, not only diagram declarations and Make targets.
- Wire the reusable checkout validator into a documented Make, CLI, or campaign
  invocation so it is a reproducible gate rather than dead exported quality
  code.

Because this changes the evidence validator/campaign surface, freeze and qualify
a new source candidate and publish another source → payload → attestation chain.
Preserve cycles 1 and 2 plus every failed/non-admissible setup attempt in the
new ledger.
