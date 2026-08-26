---
affected_files: []
cycle_number: 3
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T14:36:24Z'
reviewer_agent: codex
wp_id: WP03
---

# WP03 dependency correction: real error-envelope placement

Real Beads v1.2.2 disproved the fake-only error-channel assumption. `bd show <missing> --json` exits nonzero with a valid schema-1 error object in the stdout envelope while stderr contains human prose. The protocol runner currently searches stderr JSON and misclassifies this as malformed protocol.

Decode documented JSON from the bounded stdout channel even on nonzero exit, retain stderr only as non-semantic diagnostics, and map the provider's not-found envelope to the typed `not_found` error. Preserve output bounds, redaction, argv-only execution, ambiguous-mutation rules, and malformed-envelope refusal. Add byte-real fixtures and a mutant proving human stderr is never parsed as state.
