---
affected_files: []
cycle_number: 17
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T08:10:42Z'
reviewer_agent: codex
wp_id: WP02
---

# WP02 post-approval defect: explicit effect reconciliation resume

WP07 exposed a closed-state-machine contradiction in the approved Authority Journal v2 contract. `OperationState` admits `ambiguous -> reconciliation_required`, but no nonterminal transition can represent a capability-owned `retry_allowed` or `compensation_allowed` decision that is durably published before the next provider call under the same `OperationID`.

The bounded repair must preserve the ordinary transition matrix and add one explicit foundation operation for `reconciliation_required -> in_progress`, authorized by a newly appended permission event for an already-bound EffectID. Generic `AdvanceOperationState` must continue rejecting that edge. Missing, reused, or unbound permission coordinates must refuse, and controlled tests must prove the direct/widened mutant stays dead. No new status, OperationID, provider vocabulary, or capability-policy interpretation belongs in the journal.
