---
affected_files: []
cycle_number: 5
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T14:36:49Z'
reviewer_agent: codex
wp_id: WP06
---

# WP06 dependency correction: real graph and discussion shapes

Real Beads v1.2.2 graph/discussion round trips disproved fake field names.

- `dep list <blocked> --json` returns an array whose blocker endpoint is `id` and edge kind is `dependency_type`; add/remove mutation receipts still use `depends_on_id`.
- `comments <id> --json` returns records with `text`, not `body`.
- `ready --json` returns a schema-1 array with optional/count fields.

Correct only provider translation and verification using byte-real fixtures. Preserve asymmetric dependency semantics, typed removal, append-only discussion, capability truth, complete-catalog readiness validation, and rejection of malformed/ambiguous records.
