---
work_package_id: WP03
title: Portable Run Governance Context
dependencies: []
requirement_refs:
- C-002
- C-004
- C-005
- C-007
- FR-010
- NFR-006
- NFR-008
planning_base_branch: feat/phase-one-architecture-closeout
merge_target_branch: feat/phase-one-architecture-closeout
branch_strategy: Use the assigned lane worktree and merge reviewed changes to feat/phase-one-architecture-closeout.
subtasks:
- T011
- T012
- T013
- T014
- T015
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/governancecontext/
create_intent:
- internal/governancecontext/model.go
- internal/governancecontext/model_test.go
- internal/governancecontext/digest.go
- internal/governancecontext/digest_test.go
- internal/ids/governance_context.go
execution_mode: code_change
model: ''
owned_files:
- internal/governancecontext/**
- internal/ids/governance_context.go
- internal/execution/model.go
- internal/execution/lease.go
- internal/execution/lease_test.go
- internal/execution/start.go
- internal/execution/start_test.go
- internal/controlstore/store.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP03 - Portable Run Governance Context

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Add the smallest portable provenance model needed before provider-backed runs exist: an immutable ordered governance context with algorithm-qualified commitments, owned once by a run and referenced by all of its lease generations.

Implementation command:

```bash
spec-kitty agent action implement WP03 --agent codex
```

## Context

Do not add doctrine/profile columns to leases. Do not claim exact context delivery to a model. This capability records the configuration, doctrine, profile, and mission-type inputs resolved for run admission. Persistence is WP04; this package must remain free of SQL, filesystem, YAML, protobuf, provider, and CLI types.

## Branch Strategy

Use the lane worktree assigned after task finalization. Planning and merge target are `feat/phase-one-architecture-closeout`.

### T011: Specify commitment and ordered-context behavior

**Purpose**: Pin validation and semantic identity before production code.

**Steps**:

1. Test missing algorithm, malformed/blank digest, empty entries, blank ref/revision, duplicate identity, and unsupported schema.
2. Test deterministic output across one hundred identical evaluations.
3. Test that kind, ref, revision, algorithm, digest, entry count, and order affect the aggregate.
4. Confirm timestamps do not affect semantic identity.
5. Include copy/immutability tests so caller slice mutation cannot rewrite a validated context.

**Files**: model/digest tests.

### T012: Add governance-context typed identity and values

**Purpose**: Make context identity non-interchangeable with run, lease, or work IDs.

**Steps**:

1. Add `GovernanceContextID` following existing typed-ID parsing conventions.
2. Define `Commitment`, `EntryKind`, `Entry`, and `Context` values.
3. Provide constructors/accessors that validate and copy input collections.
4. Keep zero values invalid and errors typed/stable.
5. Register only the entry kinds required by the plan, with an explicit extension mechanism if needed.

**Files**: `internal/ids/governance_context.go`, `internal/governancecontext/model.go`.

### T013: Implement deterministic framed context commitments

**Purpose**: Prevent ambiguous concatenation and accidental algorithm assumptions.

**Steps**:

1. Use domain `go-kitty3/governance-context/v1`.
2. Frame every field with a fixed-width length before bytes.
3. Include schema version and entry count.
4. Initially mint SHA-256 but carry the algorithm name in every commitment.
5. Provide verification by recomputation; never trust a claimed aggregate alone.

**Files**: `digest.go`, `digest_test.go`.

### T014: Carry run-owned context through execution commands/results

**Purpose**: Make governance part of run authority at the portable application boundary.

**Steps**:

1. Add validated context input/reference to start/claim request types.
2. Add context ID and aggregate to the run capsule/result.
3. Ensure same-operation replay returns identical context identity.
4. Ensure replacement lease behavior retains the run context.
5. Update start/lease tests and test collaborators without adding duplicate lease fields.

**Files**: execution model, lease/start implementation and tests.

### T015: Extend portable control-store contracts and test collaborators

**Purpose**: Give adapters one atomic operation without exposing storage shape.

**Steps**:

1. Extend the repository contract to persist/resolve a context as part of claim.
2. Define conflict behavior for an existing ID with different content.
3. Update all fake/recording repositories to inspect and return context consistently.
4. Keep the port consumer-owned and narrow.
5. Record T011–T015 completion in the event log.

**Files**: `internal/controlstore/store.go` and owned execution test collaborators.

**Validation**:

```bash
go test ./internal/ids ./internal/governancecontext ./internal/execution ./internal/controlstore
go test -race ./internal/governancecontext ./internal/execution
```

## Definition of Done

- Portable context values are immutable, validated, and deterministically committed.
- All semantically relevant entry changes are detected.
- Run commands/results carry context identity.
- Leases inherit through run ownership and contain no duplicated policy fields.
- No adapter or external integration dependency enters the package.

## Risks

- A digest over caller-owned slices can change after validation; copy defensively.
- Sorting entries would erase intentional order; commit the supplied validated order.
- Treating “resolved” as “delivered” overstates evidence; keep names and docs precise.

## Reviewer Guidance

Reject implied algorithms, naive concatenation, mutable exported slices, SQL in portable types, context columns on leases, or migration work belonging to WP04.
