---
affected_files: []
cycle_number: 4
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T14:22:56Z'
reviewer_agent: codex
wp_id: WP04
---

# WP04 dependency correction: real Beads v1.2.2 preflight

The first real-provider qualification disproved WP04's synthetic inspection contract.

- `where --json` is usable, but the adapter does not fully cross-check its real path/database fields.
- `info --schema --json` emits human prose in v1.2.2 and must not be parsed as protocol JSON.
- `doctor --agent --json` does not provide the invented embedded/server payload.
- Schema-1 `dolt show --json` and `dolt status --json` expose the real storage and health facts.
- Read-only policy and logical server identity are locally sealed binding facts; they are not reliable provider observations.
- Child processes must force `BD_DISABLE_METRICS=1` and `BD_DISABLE_EVENT_FLUSH=1`; otherwise read-only inspection creates provider files.

Correct the typed command/preflight boundary using byte-for-byte v1.2.2 fixtures, prove obsolete commands are never invoked, cross-check workspace/database/mode/server facts, and prove inspection leaves both the workspace and user configuration unchanged. Preserve fail-before-lock/mutation behavior.
