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

## Additional WP08 dependency repair

Before this reopened package can return to review, also apply the
provider-neutral portion of `post-approval-defect-9.md`:

- add closed read-only authority assertions for unchanged aggregate-head and
  eligibility-guard targets;
- bind semantic target, exact condition, expected logical version, and
  canonical observed-value digest into the plan preimage and final bytes;
- reject duplicates, mutation/assertion overlap, malformed absent/present
  evidence, and combined operation/byte budget overflow before the durable
  port is called;
- expose the sealed assertion set defensively without importing GapDB;
- add one immutable, absent-conditioned workspace-path claim record keyed by a
  caller-supplied domain-separated claim digest and carrying canonical bounded
  value bytes;
- include the path claim in sealed records, witness membership, counts,
  digests, budgets, and ambiguity membership exactly once; and
- prove with controlled mutants that a same-value head mutation cannot
  substitute for a read-only assertion and that assertions never become
  records, events, revisions, expiry work, or watch events.

Preserve the existing closed journal API and all earlier post-approval repairs.
The foundation owns semantic proof shapes only; workspace policy, path
canonicalization, raw keys, provider types, and Git observations remain outside
this package.
