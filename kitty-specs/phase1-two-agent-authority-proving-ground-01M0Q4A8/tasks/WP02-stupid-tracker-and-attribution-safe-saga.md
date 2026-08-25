---
work_package_id: WP02
title: Stupid Tracker And Attribution-Safe Saga
dependencies:
- WP01
requirement_refs:
- FR-004
- FR-015
- FR-016
- FR-018
- FR-019
- NFR-004
- NFR-006
- NFR-009
- NFR-010
- C-004
- C-005
- C-008
planning_base_branch: mission/phase1-authority-proving-ground
merge_target_branch: mission/phase1-authority-proving-ground
branch_strategy: Planning artifacts for this mission were generated on mission/phase1-authority-proving-ground. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into mission/phase1-authority-proving-ground unless the human explicitly redirects the landing branch.
subtasks:
- T006
- T007
- T008
- T009
history: []
agent_profile: implementer-ivan
create_intent:
- internal/tracker/provider.go
- internal/tracker/fake/store.go
- internal/tracker/fake/provider.go
- internal/tracker/fake/provider_test.go
- internal/execution/external_effect.go
- internal/controlstore/sqlite/external_effect.go
- internal/helpercmd/tracker.go
- internal/scenario/tracker_saga_test.go
execution_mode: code_change
authoritative_surface: internal/tracker/
owned_files:
- internal/tracker/provider.go
- internal/tracker/fake/store.go
- internal/tracker/fake/provider.go
- internal/tracker/fake/provider_test.go
- internal/execution/external_effect.go
- internal/controlstore/sqlite/external_effect.go
- internal/helpercmd/tracker.go
- internal/scenario/tracker_saga_test.go
tags: []
tracker_refs: []
---

# Work Package Prompt: WP02 - Stupid Tracker And Attribution-Safe Saga

## Objective

Prove that tracker assignment is an unreliable external effect, not execution
authority, and that stale compensation cannot overwrite a later human change.

## Boundaries

Implement only the provider contract. The tracker must not know leases, lanes,
workspaces, review, mission state, Git, or integration. Its durability must be
separate from the Spec Kitty SQLite transaction.

### T006 - Define the narrow provider port and durable fake

- Support work, criteria, dependencies, assignment, comments, provider
  revision CAS, and operation markers.
- Store fake-provider state separately and expose no fake types to execution.

### T007 - Model ambiguous outcomes and idempotent observation

- Inject unavailable, timeout-before-apply, timeout-after-apply, and revision
  conflict.
- After ambiguity, inspect marker/revision before retrying.
- Persist external-effect observations in the control store.

### T008 - Implement compare-before-compensate

- Undo only when current revision, value, and marker prove attribution.
- Preserve a later human reassignment and return
  `external_attribution_lost` with recovery actions.
- Make repeated successful compensation a no-op.

### T009 - Prove saga behavior

- Test lost response after assignment, same-operation replay, later human
  reassignment, valid compensation, and provider unavailability.
- Assert tracker assignment never creates or extends a run lease.

## Validation

```bash
go test ./internal/tracker/... ./internal/execution
go test ./internal/scenario -run TrackerSaga -count=100
```

## Definition of Done

The fake provider is useful precisely because it is weak: cross-store crash
windows are observable, compensation is attribution safe, and no provider
state grants Spec Kitty authority.

## Reviewer Guidance

Reject a fake implemented inside the control transaction, unconditional clear
assignment, implicit retry after ambiguity, or any tracker-side mission/lease
semantics.
