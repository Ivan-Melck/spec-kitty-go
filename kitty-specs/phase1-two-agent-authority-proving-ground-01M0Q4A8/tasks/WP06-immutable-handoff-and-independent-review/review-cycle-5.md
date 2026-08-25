---
affected_files: []
cycle_number: 5
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command:
reviewed_at: '2026-08-25T04:03:38Z'
reviewer_agent: user
wp_id: WP06
---

# WP06 re-opened by Phase 1 proving-ground evidence

## Blocker: branch-movement refusal does not preserve both Git facts

WP08's P1-12 scenario exposed a product-owned recovery gap. When an owned
workspace branch is force-moved after candidate `C` is recorded, handoff
correctly refuses with a typed, decision-required candidate mismatch, but the
product does not create durable evidence refs that preserve both `C` and the
observed moved branch head `B`. The current WP08 draft can prove reachability
only by creating an `evidence/...` ref in the test fixture before the fault.
That is not product recovery and cannot support the Phase 1 verdict.

Remediate in WP06's handoff/Git verification surface, coordinating with the
existing gitrepo adapter only as necessary:

1. Create candidate `C`, then force-move the owned branch to `B` without any
   fixture-created evidence ref.
2. Submit a handoff for `C` and return the existing contract-compatible
   `candidate_mismatch` diagnostic with `decision_required`, including exact
   expected candidate `C`, observed branch head `B`, relevant identities, and
   a stable branch-movement subkind/field or equivalent recovery context.
3. Accept no handoff or review and create no execution/integration authority.
4. Product recovery must create durable refs/evidence preserving both `C` and
   `B`. After reflog expiration and `git gc --prune=now`, both commits must
   remain reachable from product-owned refs.
5. Read-only inspection must report the exact divergence and explicit repair
   action. The primary checkout must remain unchanged.
6. Same-operation replay must be idempotent: one evidence record/ref set, no
   duplicate handoff, and the same typed refusal.

Do not invent a new top-level `branch_moved` error kind unless required. The
locked command contract permits `candidate_mismatch`; the missing invariant is
product-owned evidence preservation and complete observed/expected context.

Run focused red/green tests, handoff/review fault tests, full tests, race, vet,
staticcheck, formatting/diff checks, and a GC-survival repetition appropriate
to the risk. Preserve schema v8/v9/v10 ordering and do not alter WP07's
repository-first integration fence.
