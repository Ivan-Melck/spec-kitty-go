---
affected_files: []
cycle_number: 3
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-28T13:50:26Z'
reviewer_agent: reviewer-renata
wp_id: WP11
---

# WP11 cycle-2 reviewer feedback

Verdict: rejected.

T060 refusal evidence still snapshots only `MissionSnapshot` plus row counts.
That cannot detect same-count mutations, including legal changes to an existing
outbox envelope's delivery status, attempts, claim, or timing. It also omits the
derived mission-method observation and projection availability.

Minimum repair:

- Canonically snapshot or hash ordered row contents for `phase3_operations`,
  `phase3_work_revisions_v2`, `phase3_drift_reports`,
  `phase3_mixed_reconciliations`, `phase3_events`, and every outbox identity and
  mutable delivery field.
- Capture exact pre/post `ObserveMissionMethod` data or the same typed
  unavailability, plus exact projection bytes/digest or the same typed
  projection-unavailable result.
- Add a load-bearing same-row-count mutant that changes existing outbox
  delivery state and prove the refusal snapshot catches it.
- Add mission-level wrong-WorkID and between-observation external-mapping
  substitution cases through BoundAdapter + FinalizationService. Each must
  refuse with zero authority change and then succeed only after a fresh clean
  retry.

Preserve all cycle-2 repairs and passing focused/race/static/architecture and
pinned-provider evidence. The historical WP08 `revision_ineligible` red remains
non-attributable and is not T060 evidence.
