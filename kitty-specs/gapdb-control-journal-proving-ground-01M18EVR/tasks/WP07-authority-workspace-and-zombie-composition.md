---
work_package_id: WP07
title: Authority Workspace and Zombie Composition
dependencies:
- WP05
- WP06
requirement_refs:
- C-012
- C-013
- FR-023
- FR-026
- FR-027
- FR-028
- FR-029
- FR-030
- FR-031
- FR-032
- FR-033
- FR-034
- NFR-003
- NFR-007
- NFR-008
- NFR-017
planning_base_branch: kitty/mission-gapdb-control-journal-proving-ground-01M18EVR
merge_target_branch: kitty/mission-gapdb-control-journal-proving-ground-01M18EVR
branch_strategy: Planning artifacts for this mission were generated on kitty/mission-gapdb-control-journal-proving-ground-01M18EVR. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into the mission branch; only the accepted mission may merge to main.
subtasks:
- T036
- T037
- T038
- T039
- T040
- T041
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/provingground/authority.go
create_intent:
- internal/provingground/authority.go
- internal/provingground/authority_test.go
- internal/provingground/workspace.go
- internal/provingground/workspace_test.go
- internal/provingground/zombie_test.go
- internal/provingground/workspace_fault_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/provingground/authority.go
- internal/provingground/authority_test.go
- internal/provingground/workspace.go
- internal/provingground/workspace_test.go
- internal/provingground/zombie_test.go
- internal/provingground/workspace_fault_test.go
role: implementer
tags:
- m1
- authority
- workspace
- zombie
tracker_refs: []
---

# Work Package Prompt: WP07 - Authority, Workspace, and Zombie Composition

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Compose the new journal/application/recovery slice with existing authority and
workspace services to prove Spec Kitty's crown-jewel concurrency behavior. Two
implementation ActorIDs work concurrently. When Actor A's Process A becomes a
zombie, its W1 is quarantined and Actor A's replacement Process C receives fresh
W3. Actor B/Process B retains W2. No fencing-token claim may pretend to prevent
direct POSIX writes.

## Product laws

1. Exactly one valid execution authority exists per WorkRevision.
2. Lease/fence protects Spec Kitty operations, not arbitrary filesystem writes.
3. A mutable workspace is lease-scoped and never transferred after expiry.
4. W1 quarantine is logical/authority isolation; a zombie may still write W1.
5. Replacement Process C retains ActorID A and receives W3.
6. Actor B/Process B/W2 continues independently.
7. Runtime epoch changes only on owner restart, not replacement lease issuance.
8. Durable binding, capsule delivery, and physical workspace facts reconcile.

## Branch and ownership

Begin from accepted WP05/WP06. Implement composition in new proving-ground
files only. Use existing execution/workspace/gitrepo APIs; do not edit those
packages in this WP. Do not modify SQLite or create an alternative workspace
manager. Keep WP08's effect/review/integration files untouched.

## T036: Compose two implementation actors and one authority per revision

**Files**: Create `authority.go` and `authority_test.go`.

**Implementation requirements**:

1. Seed two runnable work items in one immutable WorkRevision.
2. Claim concurrently as Actor A/Process A and Actor B/Process B.
3. Journal operation, authority decision, lease, fence, actor, process, work,
   revision, and runtime epoch in the single product batch shape.
4. Require exactly one successful current authority per work item.
5. Reject stale work revision, lease, fence, epoch, actor/process mismatch, and
   duplicate competing claim with typed outcomes.
6. Preserve concurrency across different work items/aggregates.

**Tests**:

- Concurrent claims for distinct items both succeed.
- High-contention same-item claims produce one authority.
- Every terminal path runs universal authority assertions.

**Exit evidence**: `go test ./internal/provingground -run Authority`.

## T037: Bind W1/W2 durably and inject GJ-03/GJ-04

**Files**: Create `workspace.go`, `workspace_test.go`, `workspace_fault_test.go`.

**Implementation requirements**:

1. Create a physical workspace only after valid claim authority.
2. GJ-03 cuts after physical creation and before durable workspace binding.
3. Reconcile an orphan by Git/filesystem facts without granting it authority.
4. Bind workspace identity/path, lease, fence, WorkRevision, actor/process, and
   epoch durably before returning a run capsule.
5. GJ-04 cuts after durable binding and before capsule response.
6. Reconcile retry to the same valid binding only while its lease is current.
7. Never infer binding from directory existence alone.

**Tests**:

- GJ-03 leaves no authoritative orphan and preserves attributable artifacts.
- GJ-04 retry reads the binding without creating a duplicate workspace.
- W1/W2 paths and branches are distinct.

**Exit evidence**: `go test ./internal/provingground -run 'Workspace|GJ03|GJ04'`.

## T038: Quarantine W1 and issue fresh W3 to Process C for Actor A

**Files**: Extend workspace/authority files and tests.

**Implementation requirements**:

1. Stop heartbeats for Process A while leaving it alive as a zombie fixture.
2. Expire its lease according to authoritative time and record quarantine W1.
3. Refuse any reuse or transfer of W1 under a replacement lease.
4. Claim replacement authority with a new lease/fence for Actor A/Process C.
5. Allocate and durably bind fresh W3 before capsule delivery.
6. Preserve exactly two implementation ActorIDs; do not invent Actor C.
7. Preserve Actor B/Process B/W2 and its valid authority.

**Tests**:

- Identity tuple is exactly A/A/W1, B/B/W2, A/C/W3.
- W1 != W3 physically and durably.
- Workspace-reuse mutant is killed.

**Exit evidence**: `go test ./internal/provingground -run Replacement`.

## T039: Refuse zombie and predecessor-epoch authority attempts

**Files**: Create/extend `zombie_test.go` and authority code.

**Implementation requirements**:

1. Allow zombie Process A to resume direct writes in W1 as the adversarial fact.
2. Demonstrate Spec Kitty cannot and does not fence those POSIX writes.
3. Refuse every zombie attempt to heartbeat, hand off, review, integrate, or
   otherwise mutate governed state with the expired capsule.
4. Restart the owner within the umbrella RunID, admit a fresh RuntimeEpochID,
   and refuse capsules from the predecessor epoch.
5. Keep lease expiry and owner restart as separate events.
6. Count zombie authority successes and require exactly zero.

**Tests**:

- W1 file mutation can occur but never becomes candidate authority.
- Old lease/fence and old epoch each independently refuse.
- New Process C capsule proceeds after restore/readiness.

**Exit evidence**: `go test ./internal/provingground -run Zombie`.

## T040: Preserve W2 and reconcile orphaned physical workspaces

**Files**: Extend workspace tests.

**Implementation requirements**:

1. Maintain Actor B's lease, W2 binding, and work across Actor A replacement.
2. Inspect branch HEAD, worktree registration, path identity, and durable binding
   when reconciling physical artifacts.
3. Classify active-bound, quarantined, orphan-unbound, foreign, and indeterminate.
4. Never delete or adopt foreign/indeterminate state automatically.
5. Return bounded operator actions for quarantined/orphaned work.
6. Preserve useful W1 evidence for later human reconciliation.

**Tests**:

- W2 candidate remains attributable and unchanged.
- Manual branch movement and missing registration fail closed.
- Reconciliation cannot turn W1 into W3.

**Exit evidence**: `go test ./internal/provingground -run ReconcileWorkspace`.

## T041: Qualify concurrent claims and zombie isolation

**Validation**:

1. Provide callable focused campaigns for concurrent claims and zombie isolation
   that later run at least 100 repetitions under one campaign RunID.
2. Kill global-authority-CAS, workspace-reuse, runtime-coordinate-conflation, and
   stale-capsule acceptance mutants.
3. Run tests repeatedly, race detector, vet, and `git diff --check`.
4. Assert universal invariants after every injected cut, including failures.
5. Confirm no test claims fencing tokens stop filesystem writes.

## Review guidance

The reviewer should let the zombie actually write W1. Reject the WP if safety
depends on chmod, process death, token-aware editors, or workspace handoff. Also
reject if Process C is represented as a third actor or if Actor B pauses merely
because Actor A is replaced.

## Completion criteria

- Two-agent concurrency remains genuine.
- W1 is quarantined and W3 is fresh.
- Zombie writes cannot become governed authority.
- Restart epoch and lease replacement remain distinct.
