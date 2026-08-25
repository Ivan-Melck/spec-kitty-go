---
work_package_id: WP04
title: Governance Context Persistence and Migration
dependencies:
- WP03
requirement_refs:
- C-002
- C-004
- C-007
- FR-010
- FR-011
- NFR-006
- NFR-007
- NFR-008
planning_base_branch: feat/phase-one-architecture-closeout
merge_target_branch: feat/phase-one-architecture-closeout
branch_strategy: Planning artifacts for this mission were generated on feat/phase-one-architecture-closeout. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/phase-one-architecture-closeout unless the human explicitly redirects the landing branch.
subtasks:
- T016
- T017
- T018
- T019
- T020
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controlstore/sqlite/
create_intent:
- internal/controlstore/sqlite/governance_context.go
- internal/controlstore/sqlite/governance_context_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/controlstore/sqlite/migrations.go
- internal/controlstore/sqlite/db.go
- internal/controlstore/sqlite/authority.go
- internal/controlstore/sqlite/governance_context.go
- internal/controlstore/sqlite/governance_context_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP04 - Governance Context Persistence and Migration

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Persist WP03's portable governance context in the current SQLite proving adapter with a forward-only migration that preserves every historical authority record, represents old provenance as unknown, and makes context plus new run creation atomic.

Implementation command:

```bash
spec-kitty agent action implement WP04 --agent codex
```

## Context

SQLite remains an adapter pending Gapdb adoption. Do not move semantic validation into SQL alone and do not create a schema shape that portable callers must know. Existing schema migrations contain legacy token/run transformations; the new migration must work from every shipped version and must not rewrite history.

## Branch Strategy

This WP depends on WP03 and must use its dependency-resolved lane base. The mission merge target remains `feat/phase-one-architecture-closeout`.

### T016: Write forward-migration acceptance fixtures

**Purpose**: Define preservation before altering migrations.

**Steps**:

1. Seed representative databases at every shipped `user_version` using existing migration helpers.
2. Include operations, active/expired leases, runs, workspaces, handoffs/reviews/integration records where available.
3. Open through the current adapter and assert all prior rows/constraints remain readable.
4. Assert old runs return explicit `unknown_pre_provenance`, not a fabricated context.
5. Add rejection fixtures for broken/mismatched context rows.

**Files**: `governance_context_test.go`.

### T017: Add context tables and nullable historical run linkage

**Purpose**: Project the portable aggregate into normalized adapter state.

**Steps**:

1. Add `governance_contexts` and ordered `governance_context_entries`.
2. Add a nullable `runs.governance_context_id` foreign key for historical rows.
3. Enforce unique entry positions and unique `(context, kind, ref)`.
4. Restrict deletion and prevent identity/content rebinding.
5. Bump and verify `currentSchemaVersion` through the existing migration loop.

**Files**: `migrations.go`, possibly `db.go` version assertions.

### T018: Persist context and run authority atomically

**Purpose**: Prevent a run from existing with partially written governance.

**Steps**:

1. In the claim transaction, validate context through WP03 types.
2. Insert a new context and ordered entries or compare an existing identity before reuse.
3. Reject same ID/different content as an authority conflict.
4. Insert the run with its context foreign key in the same transaction.
5. Preserve same-operation replay and concurrent claim semantics.

**Files**: `authority.go`, new `governance_context.go`.

### T019: Read and inspect context through immutable run ownership

**Purpose**: Make provenance inspectable without lease duplication.

**Steps**:

1. Add adapter read helpers required by the portable contract.
2. Reconstruct entries in stored position order.
3. Recompute/verify aggregate before returning.
4. Resolve lease provenance through `lease.run_id -> run.context_id`.
5. Return typed corruption/conflict diagnostics for incomplete persisted state.

**Files**: `governance_context.go`, tests.

### T020: Prove legacy preservation and new-run enforcement

**Purpose**: Qualify both sides of the compatibility boundary.

**Steps**:

1. Run every-version migration fixtures.
2. Create a new run and verify valid context linkage.
3. Attempt missing context, rebound ID, reordered entries, and partial writes.
4. Reopen the database and confirm replay/recovery returns identical context.
5. Run focused race tests and record T016–T020 completion events.

**Validation**:

```bash
go test ./internal/controlstore/sqlite -run 'Governance|Migration' -count=10
go test -race ./internal/controlstore/sqlite
```

## Definition of Done

- All shipped schemas upgrade without lost or fabricated records.
- Old runs report unknown provenance honestly.
- New runs reference validated stored contexts atomically.
- Context identity cannot be rebound to different content.
- Lease inspection resolves through runs with no duplicate policy columns.

## Risks

- SQLite ALTER/FK behavior can differ across migration sequences; exercise each shipped version.
- Existing migration tests may seed exact column lists; update without weakening old assertions.
- Context verification on read must not silently normalize corrupted ordering.

## Reviewer Guidance

Reject backfills derived from current files, context writes outside the run transaction, schema-only validation without portable checks, lease provenance columns, or migration coverage from only the immediately previous version.
