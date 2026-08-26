---
affected_files: []
cycle_number: 4
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T14:36:37Z'
reviewer_agent: codex
wp_id: WP05
---

# WP05 dependency correction: real catalog shapes

Real Beads v1.2.2 catalog round trips disproved fake payload assumptions.

- `create --json` returns an object, but `show` and `update` return one-element arrays.
- Acceptance criteria are in `acceptance_criteria`, not `acceptance`.
- Unknown optional/count fields must remain tolerated; metadata is an object and foreign keys must survive update.

Correct the bounded catalog decoder/verification using byte-real fixtures. Require exactly one record where the operation contract is singular; reject empty or multi-record ambiguity. Preserve lossless metadata numbers, content-derived revisions, attribution markers, and same-WorkID serialization.
