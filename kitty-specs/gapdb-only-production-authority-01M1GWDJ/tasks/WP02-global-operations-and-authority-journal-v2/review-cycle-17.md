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
- bind semantic target, exact condition, aggregate-head logical version where
  applicable, and canonical observed-value digest into the plan preimage and
  final bytes; eligibility shards deliberately carry no fictional aggregate
  logical version;
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

## Independent-review blocker: permission-event semantic binding

The first candidate bound a resume only to a fresh EventID and a previously
known EffectID. That permits any unrelated fresh event in the same publication,
including the mandatory operation event, to be relabelled as effect permission.
The repair must make the permission coordinate immutable and bind it to the
exact current event draft admitted by the sealed plan:

- require the registered capability-owned tuple
  `external_effect@1@1`, owned by `internal/execution`;
- require aggregate kind `external_effect`, aggregate ID equal to the existing
  EffectID, and the exact scope carried by that event;
- admit only the semantic permission event types
  `external_effect.allow_retry` and
  `external_effect.allow_compensation`;
- bind EventID, event type, family, schema/reducer versions, aggregate
  coordinate, canonical payload, and canonical observations into a
  domain-separated semantic commitment;
- reconstruct and compare that commitment against the canonicalized event
  draft during `SealCommitPlanV2`; a caller-supplied label is not proof;
- keep capability policy and payload interpretation in `internal/execution`;
  the journal validates only the registered tuple, structural identity, exact
  event type, and commitment; and
- add positive public sealing coverage plus controlled mutants for substituting
  the mandatory operation event, an unrelated registered event, the wrong
  effect aggregate, the wrong registered tuple, or altered committed bytes.

Generic `AdvanceOperationState` must remain closed, and the resume helper must
not expose forgeable exported fields that reduce the proof back to two strings.
