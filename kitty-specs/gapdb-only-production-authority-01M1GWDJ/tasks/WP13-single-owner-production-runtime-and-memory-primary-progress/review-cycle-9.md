---
affected_files: []
cycle_number: 9
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-05T18:13:26Z'
reviewer_agent: codex
wp_id: WP13
---

# WP13 post-approval recovery defect

## Reason for reopening

WP14's restart-after-every-durable-boundary scenario exposed a product defect
after WP13 had been approved. A host restart correctly rotated the runtime
epoch, but every active execution and workspace capsule remained bound to the
predecessor epoch. The successor host therefore fenced legitimate resumable
work as stale, and the product exposed no explicit path to re-evaluate it.

Observed failure after the first work admission:

```text
bind workspace work-a: stale_authority: runtime_epoch
```

## Required correction

- An active, unexpired lease may be explicitly re-evaluated under the current
  runtime epoch.
- An unbound execution slot is rebound alone; an execution slot with a bound
  workspace must rebind the execution and workspace aggregates atomically.
- Rebinding preserves project, mission, work revision, work, run, lease,
  generation, fence, actor, process, workspace, path claim, and deadline.
- A predecessor capsule remains stale and cannot mutate current authority.
- Expired leases and quarantined workspaces cannot be rebound.
- The new reducer behavior must carry new implementation identities so old
  checkpoint compatibility cannot misrepresent the executable reducer set.

## Red and green evidence

- Red checkpoint: `e67d8732` (`TestTwoItemMissionRestartAfterEachDurableBoundary`)
- Product correction: `3426ca9a`
- Reducer identity correction: `f184ebc0`
- Green end-to-end restart scenario: uninterrupted and restart-after-every-
  boundary variants reached identical terminal semantic facts in 76.63 s.
- Focused execution, workspace, production-runtime, and real GapDB authority
  packages passed; the GapDB package completed in 170.508 s. Vet and
  staticcheck passed for the same package set.

This is a product recovery correction, not a waiver of stale-authority or
workspace-isolation laws. It requires a new independent review verdict.
