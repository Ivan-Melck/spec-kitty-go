---
work_package_id: WP07
title: Serialized Integration And Git-Fact Recovery
dependencies:
- WP06
requirement_refs:
- FR-007
- FR-012
- FR-013
- FR-014
- FR-017
- FR-018
- FR-019
- FR-020
- NFR-003
- NFR-004
- NFR-005
- NFR-006
- NFR-007
- NFR-008
- NFR-010
- C-003
- C-008
planning_base_branch: mission/phase1-authority-proving-ground
merge_target_branch: mission/phase1-authority-proving-ground
branch_strategy: Planning artifacts for this mission were generated on mission/phase1-authority-proving-ground. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into mission/phase1-authority-proving-ground unless the human explicitly redirects the landing branch.
subtasks:
- T028
- T029
- T030
- T031
- T032
history: []
agent_profile: implementer-ivan
create_intent:
- internal/gitrepo/integration.go
- internal/gitrepo/integration_test.go
- internal/controlstore/sqlite/integration.go
- internal/controlstore/sqlite/integration_test.go
- internal/helpercmd/integration.go
- internal/scenario/integration_test.go
execution_mode: code_change
authoritative_surface: internal/integration/
owned_files:
- internal/integration/**
- internal/gitrepo/integration.go
- internal/gitrepo/integration_test.go
- internal/controlstore/sqlite/integration.go
- internal/controlstore/sqlite/integration_test.go
- internal/helpercmd/integration.go
- internal/scenario/integration_test.go
tags: []
tracker_refs: []
---

# Work Package Prompt: WP07 - Serialized Integration And Git-Fact Recovery

## Objective

Serialize accepted candidates by repository plus target ref and recover every
Git/control-store crash window without duplicate commits, lost candidates, or
false done state.

## Boundaries

The exclusion key is not MissionID. Git is authoritative for ref movement and
ancestry; SQLite is authoritative for mutation authority and bookkeeping.
Cached saga phase never substitutes for probing Git.

### T028 - Implement target-ref mutation authority

- Acquire one bounded mutation lease for `(RepositoryID, TargetRef)`.
- Record candidate and preflight target SHAs before Git mutation.
- Revalidate approval, revision eligibility (including supersede/invalidate),
  and target immediately before use.

### T029 - Integrate in an isolated managed workspace

- Create a detached integration workspace outside the primary checkout.
- Apply the exact candidate and compare/update the explicit target ref.
- Never build a Git command through a shell string.

### T030 - Recover by Git fact

- At restart, inspect target SHA, ancestry, intended result, and candidate
  reachability before advancing saga phase.
- If target moved successfully, record the result and done exactly once.
- If fact is ambiguous or target changed differently, return typed repair state.

### T031 - Preserve conflict lines

- Leave target and all candidate commits reachable.
- Persist conflict/base/target/candidate evidence and an explicit repair action.
- Refuse automatic cleanup of dirty or conflict workspaces.

### T032 - Execute the integration race and crash matrix

- Race candidates from two lanes and from two missions against one target ref.
- Exercise `IG-00` through `IG-09`, including before/after ref movement.
- Assert no overlapping mutation authority, revalidation after target advance,
  no duplicate target commit, and one done record.
- Assert superseded and invalidated generations cannot acquire integration
  authority while an explicit `continue` remains eligible under its old digest.

## Validation

```bash
go test ./internal/integration ./internal/gitrepo ./internal/controlstore/sqlite
go test ./internal/scenario -run Integration -count=100
```

## Definition of Done

Convergence has one recoverable path across missions. A crash can delay the
record, but cannot duplicate or hide what Git actually did.

## Reviewer Guidance

Reject mission-scoped locks, primary-checkout merges, cached-phase recovery,
force-moving refs without expected-old comparison, unreachable candidates, or
cleanup that destroys conflict evidence.
