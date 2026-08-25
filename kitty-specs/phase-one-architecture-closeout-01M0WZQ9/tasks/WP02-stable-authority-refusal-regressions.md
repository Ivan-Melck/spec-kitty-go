---
work_package_id: WP02
title: Stable Authority Refusal Regressions
dependencies: []
requirement_refs:
- C-004
- FR-007
- FR-008
- NFR-002
- NFR-003
- NFR-004
- NFR-005
planning_base_branch: feat/phase-one-architecture-closeout
merge_target_branch: feat/phase-one-architecture-closeout
branch_strategy: Use the assigned lane worktree and merge reviewed changes to feat/phase-one-architecture-closeout.
subtasks:
- T007
- T008
- T009
- T010
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controlstore/sqlite/
create_intent:
- internal/controlstore/sqlite/integration_refusal_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/controlstore/sqlite/authority_test.go
- internal/controlstore/sqlite/integration.go
- internal/controlstore/sqlite/integration_test.go
- internal/controlstore/sqlite/integration_refusal_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP02 - Stable Authority Refusal Regressions

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Close two concrete gaps before CI is introduced: prove an already-inadmissible integration request is refused before repository-fence acquisition, and repair the isolated SQLite contention test so correct scheduling variance cannot make it flaky.

Implementation command:

```bash
spec-kitty agent action implement WP02 --agent codex
```

## Context

Removing the early check in `integration.go` currently preserves the final error and can escape tests even though repository authority was unnecessarily acquired. Separately, the busy-timeout test fails frequently in isolation because it expects a nominal minimum elapsed time that the driver does not guarantee.

## Branch Strategy

Use only the Spec Kitty lane worktree. Planning and merge target are `feat/phase-one-architecture-closeout`; the eventual remote PR targets `main`.

### T007: Observe and assert pre-fence integration refusal

**Purpose**: Test side-effect ordering, not just returned diagnostics.

**Steps**:

1. Identify the earliest purely local admissibility check in integration.
2. Add a narrow test seam/observer around repository-fence acquisition without duplicating business decisions.
3. Submit an invalid candidate/request that can be rejected before any repository mutation authority is needed.
4. Assert the typed rejection and an acquisition count of zero.
5. Keep the observer test-only or injected through an existing narrow collaborator.

**Files**: `integration.go`, new `integration_refusal_test.go`.

**Validation**: The result includes the expected diagnostic and no fence acquisition observation.

### T008: Add an early-refusal mutant control

**Purpose**: Prove T007 fails if the early check is removed or moved after acquisition.

**Steps**:

1. Introduce a controlled test mutation or alternative seam that executes late-refusal order.
2. Require the test to fail because acquisition occurred.
3. Ensure the diagnostic remains otherwise identical so the mutant isolates ordering.
4. Avoid repository-copy shell mutation if a function seam can express the bad order more directly.

**Validation**: Good path passes; mutant reports the forbidden acquisition.

### T009: Repair the isolated SQLite contention assertion

**Purpose**: Assert the product contract instead of scheduler folklore.

**Steps**:

1. Reproduce the test alone and record the actual diagnostic.
2. Remove the unstable minimum-duration assertion.
3. Assert the typed busy/timeout kind, relevant retry semantics, and a conservative upper bound that detects hangs.
4. Preserve real contention; do not mock away the SQLite behavior.
5. Keep error assertions independent of human message wording.

**Files**: `authority_test.go`.

**Validation**: Twenty isolated runs pass without sibling tests.

### T010: Qualify authority regressions repeatedly and under race

**Purpose**: Establish stability before these tests become required CI contexts.

**Steps**:

1. Run the contention test twenty times in isolation.
2. Run the early-refusal accepting and mutant cases ten times.
3. Run the affected packages under the race detector.
4. Confirm existing serialized integration/recovery tests still pass.
5. Record T007–T010 through `spec-kitty agent tasks mark-status`.

**Validation**:

```bash
go test ./internal/controlstore/sqlite -run 'Busy|Contention' -count=20
go test ./internal/controlstore/sqlite -run 'Refus|Fence' -count=10
go test -race ./internal/controlstore/sqlite
```

## Definition of Done

- Inadmissible work is observably rejected before fence acquisition.
- The ordering mutant is killed for the intended reason.
- The contention test has no minimum elapsed-time requirement.
- Twenty isolated runs and focused race tests pass.
- Existing integration authority behavior is unchanged.

## Risks

- Exposing a test hook can become a second decision path; observe rather than reimplement.
- An enormous upper bound would hide hangs; choose from measured distribution with reasonable scheduling margin.
- Test transaction setup can accidentally rely on global suite state; create all contention locally.

## Reviewer Guidance

Require proof of zero acquisition, not only “no Git mutation.” Reject sleeps, lowered-but-still-arbitrary duration floors, mocked SQLite contention, or tests whose mutant fails from a different error.
