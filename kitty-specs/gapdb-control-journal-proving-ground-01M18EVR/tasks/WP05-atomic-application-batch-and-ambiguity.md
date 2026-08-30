---
work_package_id: WP05
title: Atomic Application Batch and Ambiguity Reconciliation
dependencies:
- WP02
- WP03
- WP04
requirement_refs:
- C-009
- FR-007
- FR-008
- FR-009
- FR-010
- FR-011
- FR-012
- FR-013
- FR-018
- FR-022
- FR-026
- FR-027
- FR-028
- FR-029
- NFR-001
- NFR-002
- NFR-007
planning_base_branch: main
merge_target_branch: main
branch_strategy: Planning artifacts for this mission were generated on main. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into main unless the human explicitly redirects the landing branch.
subtasks:
- T024
- T025
- T026
- T027
- T028
- T029
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controljournal/application/
create_intent:
- internal/controljournal/application/service.go
- internal/controljournal/application/service_test.go
- internal/controljournal/application/batch.go
- internal/controljournal/application/batch_test.go
- internal/controljournal/application/reconcile.go
- internal/controljournal/application/reconcile_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/controljournal/application/service.go
- internal/controljournal/application/service_test.go
- internal/controljournal/application/batch.go
- internal/controljournal/application/batch_test.go
- internal/controljournal/application/reconcile.go
- internal/controljournal/application/reconcile_test.go
role: implementer
tags:
- m1
- application
- atomicity
- ambiguity
tracker_refs: []
---

# Work Package Prompt: WP05 - Atomic Application Batch and Ambiguity Reconciliation

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Implement the product application service that validates a command, constructs
one complete durable publication batch, submits it once through the GapDB port,
and reconciles response ambiguity by stable OperationID and ordinary reads. This
is the single point where product semantics become an admitted storage batch.

## Non-negotiable laws

1. A product command publishes atomically or leaves operation-attributable state
   unchanged while preserving every preexisting revision and digest exactly.
2. One command produces one GapDB batch; split publication is forbidden.
3. Retries reuse OperationID and canonical command digest.
4. Current-batch revisions are returned metadata, never prefilled values/keys.
5. Per-aggregate heads are the only ordinary concurrency CAS coordinates.
6. RuntimeEpochID is pinned on commands but changes only at owner restart.
7. Lost response means ambiguous until read-side reconciliation.
8. Storage idempotency does not imply exactly-once external effects.

## Branch and ownership

Begin from accepted WP02–WP04. Edit only the six application files. Consume the
pure model, public adapter port, and lifecycle endpoints; do not edit them. Do
not edit SQLite, module files, or existing domain packages.

## T024: Build one atomic application publication batch

**Files**: Create `batch.go` and `batch_test.go`.

**Implementation requirements**:

1. Accept a validated command envelope, expected aggregate heads, ordered
   events, effect intents, and optional checkpoint from the prior prefix only.
2. Construct one operation record, 1-128 events, 1-32 head mutations, 0-16
   intents, and optional checkpoint blob/head in one batch.
3. Enforce at most 256 mutations, six-MiB raw, and twelve-MiB wire before call.
4. Canonically encode and hash the command so retry equivalence is exact.
5. Refuse duplicate aggregate/event/intent identities and inconsistent heads.
6. Never include a current-batch committed revision in any mutation value/key.
7. Make the batch immutable after validation.

**Tests**:

- Minimum, maximum, and each boundary-plus-one case.
- Mutation/value ordering does not affect canonical command digest.
- Optional checkpoint can cover only a proven prior prefix.
- A split-batch mutant is impossible or rejected.

**Exit evidence**: `go test ./internal/controljournal/application -run Batch`.

## T025: Implement stable operation identity and retry reconciliation

**Files**: Create `service.go` and `service_test.go`.

**Implementation requirements**:

1. Require caller-supplied OperationID for every mutating command.
2. Submit the exact validated batch once through a narrow port.
3. If an operation record already exists, compare canonical digest and return
   its durable outcome; refuse same ID/different command.
4. Preserve OperationID across timeout, process restart, and explicit retry.
5. Return applied/conflict/refused/ambiguous through typed outcomes.
6. Attach committed revision only to accepted/read-back outcomes.
7. Do not silently retry ambiguous mutation inside the service.

**Tests**:

- Same ID/same digest reconciles without double publication.
- Same ID/different digest fails deterministically.
- A retry-new-operation-id mutant is killed.
- Adapter ambiguity remains visible to caller.

**Exit evidence**: `go test ./internal/controljournal/application -run Service`.

## T026: Implement per-aggregate CAS without global serialization

**Files**: Extend batch/service files and tests.

**Implementation requirements**:

1. Compare expected head for every touched aggregate in the same batch.
2. Advance each accepted aggregate to its explicit next semantic head.
3. Permit disjoint aggregate batches to proceed concurrently.
4. Refuse one stale touched head with no partial operation publication.
5. Compute global projection digest only after committed history is read.
6. Never compare-and-set a global projection digest or runtime epoch for an
   ordinary command.

**Tests**:

- Concurrent disjoint commands both succeed.
- Competing same-aggregate commands yield one accepted publication.
- A shared-head/global-CAS mutant fails qualification.

**Exit evidence**: `go test ./internal/controljournal/application -run 'Aggregate|Concurrent'`.

## T027: Admit a new runtime epoch only at owner restart

**Files**: Extend `service.go` and tests.

**Implementation requirements**:

1. Accept an explicitly admitted current RuntimeEpochID from owner startup.
2. Pin command and capsule validation to it without making it an ordinary CAS.
3. Expose a startup-only admission operation that creates a fresh opaque epoch
   after owner host/process restart.
4. Never change epoch on lease expiry, workspace replacement, command retry, or
   normal sidecar reconnect.
5. Preserve durable revisions across epoch transition.
6. Refuse predecessor-epoch capsules after new epoch admission.

**Tests**:

- Every simulated owner restart creates a distinct epoch.
- Lease expiry alone preserves epoch.
- Ordinary commands cannot mutate the current binding.
- Runtime-coordinate-conflation mutant is killed.

**Exit evidence**: `go test ./internal/controljournal/application -run Epoch`.

## T028: Reconcile lost responses by ordinary reads

**Files**: Create `reconcile.go` and `reconcile_test.go`.

**Implementation requirements**:

1. On ambiguity, read the operation record by stable OperationID.
2. Verify canonical digest, expected identity, committed outcome, and any
   aggregate heads/events required to establish product publication.
3. Return applied only if the complete batch is observable and consistent.
4. Return absent/indeterminate honestly when ordinary reads cannot decide.
5. Do not rely on watch delivery or resubmit with a new identity.
6. Bound reconciliation attempts/deadline and report operator recovery action.

**Tests**:

- Real GJ-02 proxy ambiguity reconciles to exactly one applied outcome.
- Absent operation remains non-applied.
- Partial/corrupt observation fails closed.
- Watch suppression does not affect reconciliation.

**Exit evidence**: `go test ./internal/controljournal/application -run Reconcile`.

## T029: Prove GJ-01 and GJ-02 application invariants

**Fault requirements**:

1. GJ-01 injects before request publication and asserts no new or changed
   operation-attributable records plus exact preservation of every preexisting
   revision and digest; it does not require a globally empty store.
2. GJ-02 uses WP04's real response-drop proxy after full daemon response.
3. Each cut must start, terminate, and record an honest typed outcome.
4. Repeat atomic-publication and lost-response campaigns at least 100 times in
   the later campaign seam; provide deterministic callable hooks here.
5. Assert universal journal/authority invariants on both pass and failure paths.

**Validation**:

- Run tests repeatedly and with race detector.
- Run WP01 guards, vet, and `git diff --check`.
- Confirm no split batch, auto-generated retry ID, watch journal, or global CAS.

## Review guidance

Review the wire-level call count and before/after record census. Reject any GJ-01
test that passes only because the store began empty, or GJ-02 test that injects a
fake timeout. Verify same-ID/different-command cannot be mistaken for a retry.

## Completion criteria

- One command is one durable atomic product batch.
- Ambiguity is real, visible, and safely reconcilable.
- Epoch and revision coordinates remain separate.
- GJ-01/GJ-02 hooks are suitable for the combined trial.
