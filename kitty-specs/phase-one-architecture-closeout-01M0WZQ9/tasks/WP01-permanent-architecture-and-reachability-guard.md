---
work_package_id: WP01
title: Permanent Architecture and Reachability Guard
dependencies: []
requirement_refs:
- C-001
- C-003
- C-004
- C-005
- C-006
- FR-001
- FR-002
- FR-003
- FR-004
- FR-005
- NFR-003
- NFR-008
planning_base_branch: feat/phase-one-architecture-closeout
merge_target_branch: feat/phase-one-architecture-closeout
branch_strategy: Planning artifacts for this mission were generated on feat/phase-one-architecture-closeout. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/phase-one-architecture-closeout unless the human explicitly redirects the landing branch.
base_branch: kitty/mission-phase-one-architecture-closeout-01M0WZQ9
base_commit: 06a683544fe6bccb756b0e2ace9d073c12987700
created_at: '2026-08-25T18:01:30.846825+00:00'
subtasks:
- T001
- T002
- T003
- T004
- T005
- T006
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/architecture/
create_intent:
- internal/architecture/policy.go
- internal/architecture/policy_test.go
- internal/architecture/graph.go
- internal/architecture/reachability.go
- internal/architecture/reachability_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/architecture/**
- internal/scenario/repetition_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP01 - Permanent Architecture and Reachability Guard

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Replace Phase 1's temporary flat source allowlist with a Go-native, directional capability policy that permits future provider adapters while mechanically preventing provider semantics from entering the authority kernel. Add a curated production-reachability check so tested-but-unwired authority guards cannot masquerade as shipped behavior.

Implementation command:

```bash
spec-kitty agent action implement WP01 --agent codex
```

## Context

The current guard in `internal/scenario/repetition_test.go` rejects Beads, APIs, and new production directories everywhere. Deleting it in Phase 2 would discard a distinctive advantage. The replacement must classify capability packages, inspect actual Go imports, accept a provider dependency from a synthetic adapter fixture, and reject the same dependency from execution/workspace/review/integration/workgraph/handoff/control-store contracts.

This is an independent implementation inspired by review findings and donor concepts. Do not copy donor scripts or source.

## Branch Strategy

Work only in the lane worktree supplied by Spec Kitty. The planning and merge target is `feat/phase-one-architecture-closeout`. Do not edit the primary checkout, create a second worktree manually, or push directly to `main`.

### T001: Specify accepting and rejecting architecture fixtures

**Purpose**: Establish observable policy behavior before production implementation.

**Steps**:

1. Write table-driven tests for the current repository graph and small synthetic graphs.
2. Include acceptance of an external provider import from a classified adapter.
3. Include rejection of kernel-to-adapter, kernel-to-provider, unknown-root, ambiguous-tier, and removed-load-bearing-rule cases.
4. Require errors to include stable rule ID, source package, and target package.
5. Keep fixtures independent of Beads SDK availability; use an inert example module path.

**Files**: `internal/architecture/policy_test.go`.

**Validation**: Each bad fixture fails only for its intended edge; the adapter fixture passes.

### T002: Implement closed capability-tier classification

**Purpose**: Make architecture policy reviewable data rather than scattered special cases.

**Steps**:

1. Define tier and rule value types with validation.
2. Classify exact packages or slash-bounded prefixes; substring matching is forbidden.
3. Reject zero, multiple, and unknown classifications for production packages.
4. Encode the current capability-oriented structure without moving packages into horizontal layer directories.
5. Keep provider SDK allowances confined to adapter tiers.

**Files**: `internal/architecture/policy.go`.

**Validation**: Unit tests cover nested paths, look-alike prefixes, overlap, and deterministic diagnostics.

### T003: Load and evaluate the production import graph

**Purpose**: Inspect real dependency edges rather than source prose.

**Steps**:

1. Load packages through Go tooling with structured output.
2. Distinguish module-local, external, test-only, and standard-library imports.
3. Evaluate every direct production edge against the closed policy.
4. Sort packages and findings for deterministic output.
5. Bound subprocess output and return typed failures for load errors.

**Files**: `internal/architecture/graph.go` plus focused tests.

**Validation**: The current repository graph passes; an injected local package cannot disappear through an unknown-root exemption.

### T004: Add curated production reachability checks

**Purpose**: Detect authority-critical implementations or guards that have only test callers.

**Steps**:

1. Define a small explicit registry/marker for reachability targets.
2. Parse production Go files and build symbol call references without counting `_test.go`.
3. Support external-package and same-package production caller policies.
4. Add a fabricated guard used only by a test and prove rejection names it.
5. Add a real registered target and prove removing its only production call site reddens the fixture.

**Files**: `internal/architecture/reachability.go`, `reachability_test.go`.

**Validation**: Both accepting and rejecting fixtures demonstrate the census is not decorative.

### T005: Replace the Phase 1 flat scope guard without weakening it

**Purpose**: Retire the temporary all-name prohibition only after directional protection exists.

**Steps**:

1. Refactor `TestPhase1SourceScope` to invoke the architecture policy.
2. Remove flat production-directory and provider-name bans that would reject valid adapters.
3. Retain explicit prohibitions on Python runtime, Markdown authority parsing, HTTP/API production surfaces, Team Kitty, and MCP until their tiers exist.
4. Preserve fake tracker and SQLite as classified adapters, not kernel packages.
5. Add a regression proving `internal/tracker/beads` could be classified without allowing any kernel import of it.

**Files**: `internal/scenario/repetition_test.go`.

**Validation**: Existing Phase 1 scope tests remain meaningful and the Phase 2 adapter shape is accepted.

### T006: Prove guard non-vacuity through repeated mutants

**Purpose**: Demonstrate every policy axis is load-bearing and stable.

**Steps**:

1. Run each mutant ten times without mutating the real checkout.
2. Verify the implicated rule/symbol, not merely non-zero exit.
3. Ensure temporary fixtures use isolated module/cache/tmp paths.
4. Record useful duration diagnostics without turning duration into a fragile lower bound.
5. Mark T001–T006 done through the event-sourced task command.

**Validation**:

```bash
go test ./internal/architecture -count=10
go test ./internal/scenario -run 'Scope|Architecture' -count=10
```

## Definition of Done

- Current production packages are completely and unambiguously classified.
- A synthetic provider adapter is accepted.
- Kernel-to-adapter/provider and unknown-root mutations fail with exact diagnostics.
- A test-only shadow implementation fails reachability.
- No Python runtime or copied donor implementation enters production.
- All six subtask completion events are recorded.

## Risks

- A broad prefix can hide unknown packages; use slash-bounded matching and overlap checks.
- A reachability census can overfit implementation; keep the registry limited to authority-critical seams.
- Replacing the old guard too early creates a protection gap; keep acceptance and rejection tests green in the same change.

## Reviewer Guidance

Reject textual provider-name grep as the main mechanism, exemptions for unknown packages, mutants that fail for compilation accidents, or a new horizontal architecture directory structure. Verify the acceptance fixture is as strong as the rejection fixtures.
