---
work_package_id: WP02
title: Pure Journal Model Codec and Reducer
dependencies:
- WP01
requirement_refs:
- C-008
- FR-007
- FR-008
- FR-009
- FR-010
- FR-011
- FR-012
- FR-013
- FR-015
- FR-016
- FR-017
- FR-018
- NFR-001
- NFR-002
- NFR-006
- NFR-010
planning_base_branch: main
merge_target_branch: main
branch_strategy: Planning artifacts for this mission were generated on main. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into main unless the human explicitly redirects the landing branch.
subtasks:
- T006
- T007
- T008
- T009
- T010
- T011
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controljournal/
create_intent:
- internal/controljournal/model.go
- internal/controljournal/model_test.go
- internal/controljournal/codec.go
- internal/controljournal/codec_test.go
- internal/controljournal/operation.go
- internal/controljournal/operation_test.go
- internal/controljournal/reducer/reducer.go
- internal/controljournal/reducer/reducer_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/controljournal/model.go
- internal/controljournal/model_test.go
- internal/controljournal/codec.go
- internal/controljournal/codec_test.go
- internal/controljournal/operation.go
- internal/controljournal/operation_test.go
- internal/controljournal/reducer/reducer.go
- internal/controljournal/reducer/reducer_test.go
role: implementer
tags:
- m1
- journal
- reducer
tracker_refs: []
---

# Work Package Prompt: WP02 - Pure Journal Model, Codec, and Reducer

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Implement the pure, deterministic semantic core for M1. It owns versioned
domain envelopes, bounded codecs, operation identity, aggregate-local heads,
effect intents, reducers, and stable projection digests. It must be usable with
an in-memory test history and must know nothing about GapDB, sockets, Git,
trackers, worktrees, SQLite, or evidence-file layout.

## Required boundaries

1. Storage assigns committed revisions; product values never predict them.
2. A command contains one to 128 ordered events and zero to sixteen effects.
3. Event order is `(Record.Revision, EventIndex, EventID)`.
4. Aggregate heads advance independently; no global projection head exists.
5. RuntimeEpochID and durable revision remain different coordinates.
6. Reducers are total for supported versions and fail closed for unknown ones.
7. Stable digests derive from canonical product state, not Go map iteration.
8. Pure reducers perform no clock, random, filesystem, network, or store calls.

## Branch and ownership

Begin from accepted WP01 in the assigned lane. Edit only the eight owned files.
Do not add module dependencies. Do not create adapter or application-service
code. Integrate to the mission branch, never directly to `main`.

## T006: Define journal identities, records, events, and heads

**Files**: Create `model.go` and `model_test.go`.

**Implementation requirements**:

1. Define validated opaque IDs for run, mission, work revision, actor, process,
   workspace, runtime epoch, operation, record, event, aggregate, intent, and
   candidate identity where the approved contracts require them.
2. Define immutable-style values for a committed record, event envelope,
   expected aggregate head, new aggregate head, effect intent, and operation
   outcome without embedding GapDB keys or server revision fields.
3. Preserve exactly two implementation ActorIDs in the trial vocabulary;
   Process C is a later process for Actor A, not a third implementation actor.
4. Represent version and event index explicitly with validated ranges.
5. Defensively copy byte slices and collections at construction/access seams.
6. Make zero/invalid values rejectable with typed, stable errors.

**Tests**:

- Invalid IDs, versions, indexes, and duplicate event IDs fail.
- Caller mutation cannot change constructed values.
- No model field is named for a storage key, current revision, or SQLite.

**Exit evidence**: `go test ./internal/controljournal -run Model`.

## T007: Implement bounded versioned codecs

**Files**: Create `codec.go` and `codec_test.go`.

**Implementation requirements**:

1. Encode every durable product value with an explicit schema version.
2. Produce deterministic bytes for equal values independent of map insertion.
3. Reject unknown versions, trailing data, duplicate fields, invalid UTF-8,
   overlong identifiers, and values above the product budget.
4. Keep ordinary values at or below 256 KiB and leave checkpoint payloads to
   WP06's distinct four-MiB contract.
5. Do not serialize a current-batch committed revision into event values or keys.
6. Return typed diagnostics without echoing arbitrary payload bytes.

**Tests**:

- Golden round trips cover every supported value kind.
- Byte equality holds across randomized construction order.
- Unknown versions and boundary-plus-one payloads fail before publication.
- A golden scan proves no current-batch revision fill-in.

**Exit evidence**: `go test ./internal/controljournal -run Codec`.

## T008: Implement deterministic reducers and stable digests

**Files**: Create `reducer/reducer.go` and `reducer/reducer_test.go`.

**Implementation requirements**:

1. Define a registry keyed by aggregate/event kind and supported version.
2. Apply events to canonical aggregate state without external side effects.
3. Reject missing reducers, unsupported versions, and invalid transitions.
4. Compute per-aggregate digests from canonical encoded state.
5. Compute the global projection digest by sorting aggregate identity and folding
   stable pairs; never use it as an authority compare-and-set target.
6. Ensure a reducer failure returns no partially published state to its caller.

**Tests**:

- Repeated histories produce byte-identical state and digest.
- Different map iteration orders produce the same global digest.
- Unknown events fail at the exact event without mutating prior state.
- A test ensures digest computation cannot grant authority.

**Exit evidence**: `go test ./internal/controljournal/reducer`.

## T009: Enforce event order and aggregate-local revisions

**Files**: Extend model, codec, and reducer tests.

**Implementation requirements**:

1. Provide a comparator for the exact three-part committed event order.
2. Reject duplicate order tuples and non-monotonic committed record sequences.
3. Validate event indexes are contiguous within each record from zero.
4. Validate expected/new aggregate heads per touched aggregate.
5. Permit unrelated aggregates to progress concurrently.
6. Do not introduce a shared projection-head record or global CAS token.

**Tests**:

- Shuffled input restores to canonical order.
- Duplicate EventID and duplicate tuple fail.
- Two aggregates can advance from different expected heads independently.
- A synthetic shared-head mutant is rejected.

**Exit evidence**: `go test ./internal/controljournal/... -run 'Order|Head'`.

## T010: Model operation outcomes and effect intents

**Files**: Create `operation.go` and `operation_test.go`.

**Implementation requirements**:

1. Model stable OperationID reuse across timeout, restart, and retry.
2. Distinguish proposed, durably applied, refused, conflict, ambiguous, and
   reconciled observations without claiming exactly-once provider behavior.
3. Model effect intent, provider observation, expected revision/value, and
   operation marker sufficient for compare-before-compensate.
4. Keep storage acceptance distinct from response delivery.
5. Keep byte recovery distinct from product recovery readiness.
6. Provide deterministic validation and branching kinds.

**Tests**:

- Retry with changed OperationID is visibly a different operation.
- Ambiguous can reconcile to applied or refused through later observation.
- Unattributed provider state cannot become safely compensable.

**Exit evidence**: `go test ./internal/controljournal -run Operation`.

## T011: Qualify pure-model invariants and mutants

**Validation and mutants**:

1. Run package tests with `-count=20` and the race detector.
2. Add negative controls for revision fill-in, invalid event order,
   global-authority CAS, shared projection head, and coordinate conflation.
3. Add property/fuzz coverage for codec round trips and reducer determinism with
   explicit corpus size/time bounds suitable for CI.
4. Run `go vet ./internal/controljournal/...` and `git diff --check`.
5. Confirm imports contain no database, filesystem, network, clock, or random
   packages except deterministic test-only generation.

## Review guidance

Review from the perspective of a second storage implementation. It should be
possible to feed committed records into reducers without understanding GapDB.
Reject if the codec relies on Go map order, if a reducer performs I/O, if epoch
identity is treated as a durable sequence, or if a storage revision appears in
the value before the batch is accepted.

## Completion criteria

- All pure types and codecs are versioned and bounded.
- Reducer replay is deterministic and side-effect free.
- Aggregate concurrency is not collapsed into a global head.
- Every assigned mutant has a live negative control.

