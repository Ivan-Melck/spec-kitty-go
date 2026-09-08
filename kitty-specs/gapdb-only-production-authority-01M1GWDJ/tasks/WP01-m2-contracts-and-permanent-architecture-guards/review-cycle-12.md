---
affected_files: []
cycle_number: 12
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-03T16:59:29Z'
reviewer_agent: user
wp_id: WP01
---

**Blocker: compact boundary guard freezes private implementation names/order.**

`validateM2CompactOperationBoundary` compares each unexported field's
identifier and index to exact Go spellings, and the sealed
`BOUNDARY-RENAMED-FIELD` mutant deliberately rejects an otherwise identical
fixed-size boundary with the same public accessors and exact-ID matcher. No
ratified artifact freezes those private Go details; WP01 requires
rename-resilient semantic inspection.

Validate the semantic private field shape as an order-independent multiset
(two foundation `Digest` values and one foundation `LogicalVersion`), plus
privacy/non-embedding, exact public accessors/matcher, no authority-bearing or
variable-width fields, and the exact summary helper/budget contracts. Replace
the renamed-field negative mutant with positive renamed and reordered
controls, while preserving all prior 139 IDs and every meaningful
compact-boundary mutant.
