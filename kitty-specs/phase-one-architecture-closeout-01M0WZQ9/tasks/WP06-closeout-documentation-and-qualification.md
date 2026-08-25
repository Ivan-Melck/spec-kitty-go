---
work_package_id: WP06
title: Closeout Documentation and Qualification
dependencies:
- WP01
- WP02
- WP04
- WP05
requirement_refs:
- C-001
- C-002
- C-003
- C-004
- C-005
- C-006
- C-007
- C-008
- C-009
- FR-009
- FR-012
- NFR-004
- NFR-008
planning_base_branch: feat/phase-one-architecture-closeout
merge_target_branch: feat/phase-one-architecture-closeout
branch_strategy: Planning artifacts for this mission were generated on feat/phase-one-architecture-closeout. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/phase-one-architecture-closeout unless the human explicitly redirects the landing branch.
subtasks:
- T027
- T028
- T029
- T030
- T031
history: []
agent_profile: curator-carla
authoritative_surface: docs/
create_intent:
- docs/ORCHESTRATOR_API_1_3_PROJECTION.md
- docs/PHASE1_ACCEPTANCE_ERRATA.md
- docs/phase1-evidence/closeout-results.json
execution_mode: code_change
model: ''
owned_files:
- .gitignore
- README.md
- docs/ORCHESTRATOR_API_1_3_PROJECTION.md
- docs/PHASE1_ACCEPTANCE_ERRATA.md
- docs/PYTHON_BEHAVIOR_INVENTORY.md
- docs/phase1-evidence/closeout-results.json
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP06 - Closeout Documentation and Qualification

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `curator-carla`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Reconcile human-facing architecture and acceptance artifacts with the shipped closeout behavior, publish an evidence-based Orchestrator API 1.3 projection, and run the aggregate qualification used for mission acceptance.

Implementation command:

```bash
spec-kitty agent action implement WP06 --agent codex
```

## Context

Documentation must describe actual code, not planned abstractions. Orchestrator API 1.3 sources are the local canonical Python Spec Kitty contract and Nano Kitty's strict consumer. The reviewed donor Go port is unlicensed at repository level and implements 1.1; it may be mentioned as conceptual context but no text/code/test body should be copied.

## Branch Strategy

This WP starts from the fully integrated dependency base in its assigned lane. Its merge target is `feat/phase-one-architecture-closeout`; no direct remote-main mutation is authorized.

### T027: Publish the Orchestrator API 1.3 projection record

**Purpose**: Make future external transport an adapter rather than a rediscovery.

**Steps**:

1. Revalidate contract version, seven-field envelope, command list, structured review addition, identities, policy metadata, and host boundary from current local canonical sources.
2. Map every command to the final redesign capabilities.
3. Classify direct, translated, deferred, partial, and blocking gaps.
4. Explain how generation-scoped workspaces strengthen legacy lane semantics.
5. Record typed error/idempotency/secret-handling requirements without implementing a transport.

**Files**: new `docs/ORCHESTRATOR_API_1_3_PROJECTION.md`.

### T028: Publish a visible erratum for the nonexistent waiver

**Purpose**: Remove a promise the product does not implement.

**Steps**:

1. Locate and cite the immutable Phase 1 acceptance clause mentioning an explicit visible waiver.
2. Create a living erratum stating that no waiver mechanism exists in Phase 1.
3. Link the erratum from README and update the behavior inventory consistently.
4. Do not modify the completed mission's historical evidence and do not add force/bypass behavior.
5. Check all remaining living-document waiver references for accurate scope.

**Files**: new `docs/PHASE1_ACCEPTANCE_ERRATA.md`, README, and `docs/PYTHON_BEHAVIOR_INVENTORY.md`.

### T029: Add binary-output hygiene and contributor commands

**Purpose**: Keep documented build instructions from dirtying the repository.

**Steps**:

1. Add `bin/` to `.gitignore` without changing unrelated ignore policy.
2. Update README quality commands to the canonical Make targets.
3. Explain short versus full campaigns and evidence expectations.
4. Ensure README storage language remains honest about current SQLite adapter and planned Gapdb adoption.

**Files**: `.gitignore`, `README.md`.

### T030: Document donor licensing and architectural non-adoption

**Purpose**: Preserve useful context without creating code-origin ambiguity.

**Steps**:

1. Add a concise design-source note or ADR section.
2. State that donor ideas were reviewed but code was independently implemented.
3. Record specifically rejected pieces: in-memory worktree ownership, SQLite-canonical work tracking, universal ledger/protobuf core, and 1.1 API adapter.
4. Avoid legal conclusions beyond the donor repository's explicit no-license instruction.

**Files**: README or projection/behavior inventory as appropriate.

### T031: Run aggregate qualification and record closeout evidence

**Purpose**: Produce acceptance-grade evidence from the integrated candidate.

**Steps**:

1. Run quality ten times and full campaign once.
2. Run race, vet, staticcheck, govulncheck, module verification, formatting, and binary builds.
3. Record exact commit, tool versions, durations, p95, test census, and terminal outcomes.
4. Record any unavailable remote-only evidence honestly rather than marking it passed.
5. Write `docs/phase1-evidence/closeout-results.json` and mark T027–T031 done.

## Definition of Done

- Every API 1.3 command has a projection classification.
- No stale waiver promise remains.
- `bin/` is ignored and README commands match shipped Make targets.
- Donor reuse boundaries are explicit.
- Aggregate qualification evidence is machine-readable and honest.

## Risks

- Documentation can race code changes; inspect the integrated candidate, not WP planning assumptions.
- Remote CI p95 cannot be inferred from local runs; label evidence source precisely.
- Editing closed mission artifacts must preserve history and clearly identify the closeout correction.

## Reviewer Guidance

Cross-check every behavior claim against final code or captured evidence. Reject copied donor prose/code, claims that Gapdb is already active, implied API implementation, hidden waiver language, or a “pass” for a gate that did not run.
