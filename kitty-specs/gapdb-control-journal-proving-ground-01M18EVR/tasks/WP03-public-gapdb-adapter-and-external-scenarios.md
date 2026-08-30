---
work_package_id: WP03
title: Public GapDB Adapter and External Scenarios
dependencies:
- WP02
requirement_refs:
- C-003
- C-006
- C-007
- C-008
- FR-006
- FR-011
- FR-012
- FR-013
- FR-014
- FR-039
- NFR-009
- NFR-011
- NFR-013
planning_base_branch: kitty/mission-gapdb-control-journal-proving-ground-01M18EVR
merge_target_branch: kitty/mission-gapdb-control-journal-proving-ground-01M18EVR
branch_strategy: Planning artifacts for this mission were generated on kitty/mission-gapdb-control-journal-proving-ground-01M18EVR. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into the mission branch; only the accepted mission may merge to main.
subtasks:
- T012
- T013
- T014
- T015
- T016
- T017
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controlstore/gapdb/
create_intent:
- internal/controlstore/gapdb/port.go
- internal/controlstore/gapdb/port_test.go
- internal/controlstore/gapdb/adapter.go
- internal/controlstore/gapdb/adapter_test.go
- internal/controlstore/gapdb/external_scenarios_test.go
- internal/controlstore/gapdb/admission_test.go
execution_mode: code_change
model: ''
owned_files:
- go.mod
- go.sum
- internal/controlstore/gapdb/port.go
- internal/controlstore/gapdb/port_test.go
- internal/controlstore/gapdb/adapter.go
- internal/controlstore/gapdb/adapter_test.go
- internal/controlstore/gapdb/external_scenarios_test.go
- internal/controlstore/gapdb/admission_test.go
role: implementer
tags:
- m1
- gapdb
- adapter
tracker_refs: []
---

# Work Package Prompt: WP03 - Public GapDB Adapter and External Scenarios

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Implement the narrow external adapter over the exact admitted public GapDB
module and prove it with the fourteen named scenarios. This WP is the sole owner
of module changes. It does not vendor GapDB, embed its server, copy internals,
select SQLite, manage the sidecar process, or implement product command policy.

## Admitted tuple and boundaries

- Go module version: `v0.0.0-20260824033134-ff3dd478f87c`.
- Sidecar source SHA and admission digest must match the ratified contracts.
- Only this package and `internal/sidecar/gapdb` may import the public module.
- `internal/server` imports are forbidden.
- All adapter methods accept explicit contexts and return typed ambiguity.
- Watch is an observation convenience, never the durable journal.
- Server revisions remain adapter metadata, not current-batch product values.

## Branch and ownership

Begin from accepted WP02. Edit only the files listed in `owned_files`; this is
the only WP permitted to edit `go.mod` and `go.sum`. Do not edit SQLite or any
existing control-store implementation. Integrate to the mission branch.

## T012: Implement the admitted public GapDB port

**Files**: Create `port.go` and `port_test.go`; update module files.

**Implementation requirements**:

1. Pin the exact GapDB pseudo-version; do not use a branch, replace directive,
   local checkout, or unreviewed transitive version.
2. Define the smallest application-facing port for Get, conditional mutation,
   atomic batch, sorted prefix scan, watch, inspect, and authority operations.
3. Translate public GapDB results to storage-neutral typed outcomes.
4. Preserve committed revision as returned metadata only after acceptance.
5. Bound contexts, request sizes, pages, and diagnostics at the adapter edge.
6. Make capability absence and version skew explicit typed refusals.

**Tests**:

- Compile-time interface assertions bind only public APIs.
- Context cancellation and malformed responses remain distinguishable.
- No test imports or instantiates an internal GapDB server.

**Exit evidence**: `go test ./internal/controlstore/gapdb -run Port`.

## T013: Implement atomic batch, CAS, scan, and watch semantics

**Files**: Create `adapter.go` and `adapter_test.go`.

**Implementation requirements**:

1. Translate one product batch into one GapDB atomic batch call.
2. Support 1-128 events, 1-32 aggregate heads, 0-16 intents, optional
   prior-prefix checkpoint publication, and at most 256 mutations.
3. Enforce six-MiB raw and twelve-MiB wire budgets before publication.
4. Preserve independent per-aggregate CAS expectations.
5. Page scans by both record count and bytes with stable prefix ordering.
6. Register watches before relying on notification ordering, then reconcile by
   ordinary reads; never reconstruct the journal from watch delivery.
7. Return conflicts without partial product publication.

**Tests**:

- Boundary and boundary-plus-one cases for every count/size.
- Competing CAS updates yield exactly one accepted head transition.
- Scan ordering and pagination neither skip nor duplicate records.
- Watch loss cannot hide a committed record from ordinary read/scan.

**Exit evidence**: `go test ./internal/controlstore/gapdb -run 'Batch|CAS|Scan|Watch'`.

## T014: Expose inspect, lease, review, workflow, and integration ports

**Files**: Extend `port.go`, `adapter.go`, and tests.

**Implementation requirements**:

1. Map only admitted public authority primitives required by the fourteen-case
   compatibility suite.
2. Keep product authority validation above the adapter; the adapter executes
   explicit expected-value/revision operations but invents no mission policy.
3. Keep lease, review, workflow, and integration records distinct by namespace
   and codec; never let one record satisfy another authority predicate.
4. Preserve opaque server-side tokens as observations.
5. Surface inspect/config provenance needed by the sidecar lifecycle.
6. Return unavailable/skew/malformed/ambiguous/conflict distinctly.

**Tests**:

- Namespace crossover is refused.
- Stale conditional authority operations fail without publication.
- Inspect values are attributed to the connected endpoint.

**Exit evidence**: `go test ./internal/controlstore/gapdb -run 'Authority|Inspect'`.

## T015: Preserve ambiguous outcomes and bounded diagnostics

**Files**: Extend adapter tests.

**Implementation requirements**:

1. Treat connection loss after request delivery as `ambiguous`, not failure.
2. Include OperationID and safe recovery action in the typed diagnostic.
3. Never retry a mutating request with a newly generated OperationID.
4. Do not claim provider exactly-once semantics.
5. Redact values, credentials, full socket paths when sensitive, and daemon
   transcripts from stable errors.
6. Bound diagnostic payloads independently from product values.

**Tests**:

- Before-send loss is distinguishable from after-send ambiguity.
- Retry guidance retains OperationID.
- Seeded secrets and oversized daemon output do not appear in errors.

**Exit evidence**: `go test ./internal/controlstore/gapdb -run 'Ambigu|Diagnostic'`.

## T016: Run the exact fourteen external-adapter scenarios

**Files**: Create `external_scenarios_test.go`.

**Required scenario IDs**:

1. `data/get-put`
2. `conditional/cas-delete`
3. `batch/atomic`
4. `scan/sorted-prefix`
5. `watch/registered-order`
6. `expiry/release-authority`
7. `ack/memory-visible`
8. `ack/durable-restart`
9. `inspect/authority`
10. `authority/lease`
11. `authority/review`
12. `authority/workflow`
13. `authority/integration`
14. `response-loss/reconcile`

**Implementation requirements**:

- Execute every case against the external admitted daemon, not an in-process
  substitute. Give each terminal result its exact stable ID.
- Assert universal local authority invariants after every case.
- Exercise slow, unavailable, stale, and dishonest observations where relevant.
- Keep scenario count separate from fault, repetition, and mutant counts.
- Skip only with an explicit environmental precondition reason in ordinary test
  mode; qualification mode must record incomplete and fail closed.

**Exit evidence**: exact fourteen-case test output with no aliases or extras.

## T017: Prove dependency, budget, and API admission

**Files**: Create `admission_test.go`.

**Validation**:

1. Assert module version, public import path, sidecar provenance inputs, and
   product-budget compatibility against pinned contract revisions.
2. Run `go mod verify` and `go list -deps` checks.
3. Run adapter tests, race detector, vet, and `git diff --check`.
4. Run WP01 architecture guards after the module is present.
5. Confirm no `replace`, internal server, SQLite fallback, PATH daemon lookup,
   or current-batch revision encoding exists.

## Review guidance

Review with the real external daemon and inspect imports directly. Reject an
adapter whose fake is more capable than its real path, whose watch becomes the
journal, whose ambiguity is auto-retried unsafely, or whose fourteen scenarios
are counted without actually starting and terminating.

## Completion criteria

- Exact public dependency is pinned and verified.
- Fourteen external scenarios are independently identifiable and terminal.
- The adapter is bounded and preserves ambiguity.
- No production or adoption claim is made.
