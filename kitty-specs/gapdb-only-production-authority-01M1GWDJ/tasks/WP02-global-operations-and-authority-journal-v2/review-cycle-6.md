---
affected_files: []
cycle_number: 6
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-02T18:26:47Z'
reviewer_agent: wp02-persistence-terminal-reviewer
wp_id: WP02
---

# WP02 Terminal Persistence Review — Rejected

Reviewed commit: `c3a0c6c5987c141a56053638ba089b7b4c3b8d98`

## Blocker: sealed plans are not closed over the consumer budget

The new record projection is structurally complete, but WP02 can seal record
sets that the frozen GapDB product contract must reject. A sealed plan is
therefore not yet a persistence capability.

Reproduced counterexamples:

- an admitted 262,144-byte event payload becomes a 350,165-byte projected
  ordinary event record, exceeding the 256 KiB ordinary-value ceiling;
- maximum operation-state and aggregate-head inputs pass early preflight and
  only fail during derived-value encoding;
- a plan below the current 12 MiB prospective-plan ceiling sealed with
  9,341,911 projected raw value bytes, exceeding the 6 MiB batch ceiling; and
- WP02 admits 64 effect intents although the frozen product limit is 16, and
  its head membership can exceed the frozen 32-head limit.

The current preflight counts typed source bytes. It does not conservatively or
exactly bound the final encoded ordinary records and complete projected batch
that WP03 must translate.

## Required bounded repair

1. Restore the frozen provider-neutral product cardinalities at the sealed
   boundary: at most 128 events, 32 heads, 16 effect intents, 256 total
   projected mutations, and the already declared checkpoint cardinality law.
2. Before registry lookup, cloning, sorting, or canonicalization, use a checked
   conservative encoded-size preflight that rejects any source shape which
   cannot fit its final projected value class or complete 6 MiB raw batch.
3. After deriving canonical records, verify the exact projection again:
   ordinary values at most 256 KiB, checkpoint records at most 4 MiB, total
   projected mutation count at most 256, and exact target-plus-value raw bytes
   at most 6 MiB. The provider adapter may repeat this using encoded keys, but
   must never be the first layer to discover a sealed plan is impossible.
4. Keep the 12 MiB canonical-plan/evidence ceiling distinct from the 6 MiB
   provider raw-batch ceiling. Do not describe typed input byte totals as exact
   provider raw bytes; include deterministic target-key upper bounds in early
   preflight and use canonical encoded keys for the final exact check where the
   provider-neutral codec permits it.
5. Add exact/plus-one tests at final projected ordinary value, checkpoint
   value, head/effect/projected-mutation count, and raw-batch boundaries. Kill
   the reproduced 350,165-byte event and 9,341,911-byte projection mutants.
6. Preserve every structural and historical WP02 invariant already passing.

## Passing scope

The eight closed roles, opaque typed targets, lack of raw-key/provider/delete/
expiry/condition-any injection, operation versus aggregate head values,
PublicationID binding, host-global EffectID, eligibility and checkpoint
targets, condition bijection, deterministic ordering, immutability, operation
authority, timestamps, reducer purity, and prior review regressions all pass.

`EffectIntentReference.Digest` is capability-semantic rather than a raw value
digest. WP02 correctly binds both the digest and canonical value into the plan;
WP07 owns validation of the canonical intent's semantic request digest.

Required focused, repeated, race, architecture, helper, compile, vet,
staticcheck, formatting, and diff gates passed. Temporary probes were removed.
