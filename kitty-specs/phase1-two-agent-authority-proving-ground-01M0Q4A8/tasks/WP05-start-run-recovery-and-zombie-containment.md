---
work_package_id: WP05
title: Start-Run Recovery And Zombie Containment
dependencies:
- WP04
requirement_refs:
- FR-004
- FR-005
- FR-007
- FR-008
- FR-009
- FR-017
- FR-018
- FR-019
- FR-020
- NFR-003
- NFR-004
- NFR-005
- NFR-006
- NFR-007
- C-003
- C-007
- C-008
planning_base_branch: mission/phase1-authority-proving-ground
merge_target_branch: mission/phase1-authority-proving-ground
branch_strategy: Planning artifacts for this mission were generated on mission/phase1-authority-proving-ground. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into mission/phase1-authority-proving-ground unless the human explicitly redirects the landing branch.
subtasks:
- T019
- T020
- T021
- T022
- T023
history: []
agent_profile: implementer-ivan
create_intent:
- internal/execution/recovery.go
- internal/execution/recovery_test.go
- internal/workspace/quarantine.go
- internal/gitrepo/inspection.go
- internal/gitrepo/inspection_test.go
- internal/controlstore/sqlite/recovery.go
- internal/helpercmd/recovery.go
- internal/scenario/start_run_recovery_test.go
- internal/scenario/zombie_test.go
execution_mode: code_change
authoritative_surface: internal/execution/
owned_files:
- internal/execution/recovery.go
- internal/execution/recovery_test.go
- internal/workspace/quarantine.go
- internal/gitrepo/inspection.go
- internal/gitrepo/inspection_test.go
- internal/controlstore/sqlite/recovery.go
- internal/helpercmd/recovery.go
- internal/scenario/start_run_recovery_test.go
- internal/scenario/zombie_test.go
tags: []
tracker_refs: []
---

# Work Package Prompt: WP05 - Start-Run Recovery And Zombie Containment

## Objective

Kill allocation at every `SR-*` boundary and prove honest recovery. Then expire
a suspended process, allocate a new workspace, resume the stale writer, and
prove containment without pretending the filesystem can be fenced.

## Boundaries

Never transfer or silently clean a mutable workspace. Quarantine is a control
state, not a chmod/security claim. Recovery must inspect database, provider,
filesystem, and Git facts before acting.

### T019 - Implement operation-centric start recovery

- Resume by OperationID, not ActorID or path.
- Probe tracker marker, Git registration, base, dependency head, and ownership
  at each stored stage.
- Return typed decision state when facts are ambiguous or externally changed.

### T020 - Expire authority and quarantine permanently

- Expire with a generation compare-and-swap and reject all stale tokens.
- Apply an `invalidate` reconciliation by expiring affected authority and
  quarantining its workspace; `continue` must leave the old contract explicit.
- Mark the bound workspace quarantined; never rebind it.
- Allocate generation N+1 with a new WorkspaceID, branch, path, and index.

### T021 - Add safe inspection and cleanup refusal

- Diagnose registration/path/branch/head/dirty discrepancies without mutation.
- Refuse cleanup for unknown, dirty, active, or quarantined worktrees.
- Preserve exact commits for later explicit reconciliation.

### T022 - Execute the complete start-run crash matrix

- Crash or lose response at `SR-00` through `SR-09`.
- Retry the same operation and prove one lease, workspace, assignment, and
  terminal operation result.
- Ensure dependency conflict or partial preparation never reports ready.

### T023 - Execute the zombie experiment

- Suspend A at generation 17/W1; expire it; start B at generation 18/W2.
- Resume A and require it to edit and successfully commit in W1.
- Prove W2 and primary are unchanged and stale authority cannot hand off,
  review, or integrate through every currently available command seam.
- Prove revision invalidation uses the same expiry/quarantine path rather than
  inventing a second workspace-lifetime transition.

## Validation

```bash
go test ./internal/execution ./internal/workspace ./internal/gitrepo
go test ./internal/scenario -run 'StartRunRecovery|Zombie' -count=100
```

## Definition of Done

Every named start boundary yields a proven state or typed recovery decision,
and the zombie is dangerous but contained in a workspace no successor inherits.

## Reviewer Guidance

The zombie must actually write and commit after expiry. Reject tests that pass
by killing it, chmod tricks, workspace reuse, path-based resume, or destructive
cleanup of evidence.
