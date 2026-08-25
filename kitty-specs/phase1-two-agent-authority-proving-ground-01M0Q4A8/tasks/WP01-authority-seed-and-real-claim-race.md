---
work_package_id: WP01
title: Authority Seed And Real Claim Race
dependencies: []
requirement_refs:
- FR-001
- FR-003
- FR-004
- FR-007
- FR-018
- FR-019
- NFR-001
- NFR-003
- NFR-004
- NFR-006
- NFR-008
- NFR-010
- C-001
- C-002
- C-008
planning_base_branch: mission/phase1-authority-proving-ground
merge_target_branch: mission/phase1-authority-proving-ground
branch_strategy: Planning artifacts for this mission were generated on mission/phase1-authority-proving-ground. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into mission/phase1-authority-proving-ground unless the human explicitly redirects the landing branch.
base_branch: kitty/mission-phase1-two-agent-authority-proving-ground-01M0Q4A8
base_commit: 94fd5596489998e79b68974a320e6cdd203cf2a5
created_at: '2026-08-23T11:28:10.155392+00:00'
subtasks:
- T001
- T002
- T003
- T004
- T005
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/execution/
create_intent:
- go.mod
- go.sum
- cmd/spk-phase1-helper/main.go
- internal/helpercmd/root.go
- internal/controlstore/store.go
- internal/controlstore/sqlite/db.go
- internal/controlstore/sqlite/migrations.go
- internal/controlstore/sqlite/authority.go
- internal/controlstore/sqlite/authority_test.go
- internal/execution/model.go
- internal/execution/lease.go
- internal/execution/lease_test.go
- internal/scenario/harness.go
- internal/scenario/claim_race_test.go
execution_mode: code_change
owned_files:
- go.mod
- go.sum
- cmd/spk-phase1-helper/main.go
- internal/helpercmd/root.go
- internal/ids/**
- internal/faults/**
- internal/controlstore/store.go
- internal/controlstore/sqlite/db.go
- internal/controlstore/sqlite/migrations.go
- internal/controlstore/sqlite/authority.go
- internal/controlstore/sqlite/authority_test.go
- internal/execution/model.go
- internal/execution/lease.go
- internal/execution/lease_test.go
- internal/scenario/harness.go
- internal/scenario/claim_race_test.go
tags: []
tracker_refs: []
---

# Work Package Prompt: WP01 - Authority Seed And Real Claim Race

## Objective

Deliver the smallest real vertical experiment: independent OS processes contend
for one work generation through a real SQLite WAL store, and exactly one obtains
a valid lease. This package must end with a running scenario, not reusable
scaffolding awaiting a later package.

## Context and Boundaries

Use the data model and command contract. Pin `modernc.org/sqlite v1.57.0` and
verify WAL, foreign keys, and busy timeout at startup. Domain types must not
import SQLite or CLI details. The helper delegates to the same command handlers
used in-process. Do not add Git worktrees or tracker behavior yet.

### T001 - Establish the Go module and typed identities

- Create the Go module, pin the driver, and define non-interchangeable typed IDs.
- Add deterministic ID/clock injection for tests; production defaults may use
  randomness and wall clock behind narrow interfaces.
- Prove parse/round-trip and type boundaries with tests.

### T002 - Implement the minimal control-store migration and policy

- Create versioned forward-only migrations for projects, work revisions, work,
  actors/processes, operations, runs, leases, events, and idempotency.
- Verify effective pragmas on acquired connections.
- Use immediate short write transactions and translate exhausted contention to
  `control_store_busy`.

### T003 - Implement atomic lease claim and replay

- Create a finalized seed work generation through a constrained seed API.
- Implement `StartRun` only through lease grant: operation idempotency, one
  active lease, monotonically increasing generation, opaque token, typed
  conflict, and same-operation replay.
- Recheck token/generation in heartbeat and expiry mutations.

### T004 - Add named fault and independent-process plumbing

- Implement the no-op `Faults` contract plus process-safe block/crash controls.
- Keep scenario branching outside business code.
- Make the helper accept structured input/output and invoke the same lease
  service as tests.

### T005 - Prove the real claim race

- Race at least two helper processes at a barrier against one SQLite database.
- Assert exactly one valid lease, one generation, one idempotency result per
  operation, and complete diagnostics for losers.
- Add a 32-contender test and demonstrate that replay does not duplicate state.

## Validation

```bash
go test ./internal/ids ./internal/controlstore/sqlite ./internal/execution
go test ./internal/scenario -run ClaimRace -count=100
go test -race ./...
```

## Definition of Done

One real multi-process race proves the first authority law. SQLite settings are
observed rather than assumed, and no package exists without being consumed by
that scenario.

## Reviewer Guidance

Reject goroutine-only evidence, deferred transactions whose upgrade race is
hidden by retries, stringly typed IDs, or a helper with alternate business
logic. Confirm that one valid lease—not merely one success response—is the
asserted invariant.
