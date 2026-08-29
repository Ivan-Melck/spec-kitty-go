---
affected_files: []
cycle_number: 4
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-27T20:33:17Z'
reviewer_agent: user
wp_id: WP02
---

# WP02 arbiter remediation

Human authorization grants one surgical remediation after three review cycles.

The sole remaining acceptance gap is T007 exact graph diagnostics. `NewDefinition` must return bounded diagnostics that identify both the exact offending step (or stable definition field/index when no valid step exists) and the exact violated rule for every invalid initial, terminal, outcome, predecessor, and successor class. Add load-bearing exact-value mutants for invalid and absent initial, duplicate and absent terminal, terminal-with-successors, invalid terminal outcome, nonterminal-with-outcome, unknown predecessor, and unknown successor. Ensure the unknown-successor fixture uses a valid non-reserved key so it exercises `definition.unknown_successor`, not `unsupported_adjacent_step`.

All CAS, replay, store postcondition, privacy, static-law, lifecycle, and qualification fixes from prior cycles must remain unchanged.
