---
affected_files: []
cycle_number: 4
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T14:37:02Z'
reviewer_agent: codex
wp_id: WP07
---

# WP07 dependency correction: real assignment mutation shape

Real Beads v1.2.2 returns one-element arrays for `update --claim --json` and general assignee/metadata updates, not a single object. The record exposes `status:"in_progress"` and `assignee`; foreign metadata survives.

Correct singular assignment decoding/verification using byte-real fixtures and reject empty or multi-record ambiguity. Preserve the current product policy: assignment remains unguarded-only, atomic claim cannot attach the Spec Kitty operation marker in the same provider effect, and safe compensation remains unsupported unless attribution is demonstrably preserved.
