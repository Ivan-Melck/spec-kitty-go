---
work_package_id: WP04
title: Lease-Scoped Git Workspaces And Concurrent Start
dependencies:
- WP01
- WP02
- WP03
requirement_refs:
- FR-004
- FR-005
- FR-006
- FR-007
- FR-008
- FR-009
- FR-018
- FR-019
- FR-020
- NFR-003
- NFR-004
- NFR-005
- NFR-006
- NFR-007
- NFR-009
- NFR-010
- C-003
- C-007
- C-008
planning_base_branch: mission/phase1-authority-proving-ground
merge_target_branch: mission/phase1-authority-proving-ground
branch_strategy: Planning artifacts for this mission were generated on mission/phase1-authority-proving-ground. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into mission/phase1-authority-proving-ground unless the human explicitly redirects the landing branch.
subtasks:
- T014
- T015
- T016
- T017
- T018
history: []
agent_profile: implementer-ivan
create_intent:
- internal/gitrepo/runner.go
- internal/gitrepo/repository.go
- internal/gitrepo/workspace.go
- internal/gitrepo/workspace_test.go
- internal/workspace/model.go
- internal/workspace/service.go
- internal/workspace/service_test.go
- internal/execution/start.go
- internal/execution/start_test.go
- internal/controlstore/sqlite/start_run.go
- internal/helpercmd/start_run.go
- internal/scenario/workspace_concurrency_test.go
execution_mode: code_change
authoritative_surface: internal/workspace/
owned_files:
- internal/gitrepo/runner.go
- internal/gitrepo/repository.go
- internal/gitrepo/workspace.go
- internal/gitrepo/workspace_test.go
- internal/workspace/model.go
- internal/workspace/service.go
- internal/workspace/service_test.go
- internal/execution/start.go
- internal/execution/start_test.go
- internal/controlstore/sqlite/start_run.go
- internal/helpercmd/start_run.go
- internal/scenario/workspace_concurrency_test.go
tags: []
tracker_refs: []
---

# Work Package Prompt: WP04 - Lease-Scoped Git Workspaces And Concurrent Start

## Objective

Deliver the crown-jewel happy path: two independent processes start distinct
work concurrently in distinct real Git worktrees while the primary checkout
remains byte-for-byte unchanged.

## Boundaries

Lane lineage and mutable workspace identity must remain separate. Execute Git
as argv with explicit canonical paths. A directory or branch existing does not
mean a workspace is ready. Replacement behavior is completed in WP05, but this
package must make rebinding structurally impossible.

### T014 - Bind the real repository safely

- Resolve the Git common directory and canonical repository identity once.
- Create a managed workspace root outside candidate worktrees.
- Refuse path/symlink escape and keep control/tracker stores out of Git.
- Record primary HEAD, index, and tree for isolation assertions.

### T015 - Implement the staged workspace state machine

- Persist reservation before `git worktree add`.
- Separately verify registration, pinned base, dependency-prepared head, owned
  branch, and final readiness.
- Make Workspace-to-Lease binding immutable and paths/branches unique.

### T016 - Compose the idempotent start saga

- Resume the WP01 operation through tracker assignment and workspace stages.
- Re-read/probe each external effect before advancing.
- Publish a Run capsule only after the exact base and ownership are verified.

### T017 - Prepare dependencies atomically

- Apply pinned accepted dependency candidates in deterministic order.
- Never publish a partial/conflicting dependency head as ready.
- Return typed target/base movement and conflict diagnostics.

### T018 - Prove two-agent concurrency

- Start two OS processes on disjoint ready items at a barrier.
- Make both edit and commit with overlapping active intervals.
- Assert distinct runs, tokens, paths, branches, indexes, and commits and zero
  primary-checkout change.

## Validation

```bash
go test ./internal/gitrepo ./internal/workspace ./internal/execution
go test ./internal/scenario -run WorkspaceConcurrency -count=100
```

## Definition of Done

Two agents really work at once, but never in the same mutable checkout. The
Run capsule is returned only for a verified ready workspace.

## Reviewer Guidance

Reject ambient CWD discovery during mutations, shell command strings, a shared
index, existence-as-readiness, mutable lease binding, or a concurrency test
whose active intervals do not actually overlap.
