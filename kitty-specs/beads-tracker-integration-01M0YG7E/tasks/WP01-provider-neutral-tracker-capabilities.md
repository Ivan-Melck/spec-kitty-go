---
work_package_id: WP01
title: Provider-Neutral Tracker Capabilities
dependencies: []
requirement_refs:
- C-001
- C-004
- C-007
- FR-001
- FR-006
- FR-013
- FR-014
- FR-015
- FR-017
- NFR-014
planning_base_branch: feat/beads-tracker-integration
merge_target_branch: feat/beads-tracker-integration
branch_strategy: Planning artifacts for this mission were generated on feat/beads-tracker-integration. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/beads-tracker-integration unless the human explicitly redirects the landing branch.
base_branch: kitty/mission-beads-tracker-integration-01M0YG7E
base_commit: 785e49262ab809e85804d64052093eab482dd8d9
created_at: '2026-08-26T09:02:02.948003+00:00'
subtasks:
- T001
- T002
- T003
- T004
- T005
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/tracker/
create_intent:
- internal/tracker/capabilities.go
- internal/tracker/capabilities_test.go
- internal/tracker/fake/conformance_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/tracker/provider.go
- internal/tracker/capabilities.go
- internal/tracker/capabilities_test.go
- internal/tracker/fake/store.go
- internal/tracker/fake/provider.go
- internal/tracker/fake/provider_test.go
- internal/tracker/fake/conformance_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP01 - Provider-Neutral Tracker Capabilities

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Establish the provider-neutral tracker vocabulary that every later Phase 2 adapter
and application service can depend on. The package must describe what a provider
can actually do, expose narrow interfaces for the capabilities an application
uses, and make unsupported safety guarantees fail before mutation.

The completed increment must preserve the current tracker saga and its durable
fake provider while making their guarantees explicit. It must not introduce a
Beads dependency, a command runner, provider-specific field names, or a new
source of execution authority.

Implementation command:

```bash
spec-kitty agent action implement WP01 --agent codex
```

## Context

Phase 1 deliberately used a small durable fake tracker. Its current
`tracker.Provider` interface combines reading, assignment, comments, and guarded
compensation. Its method names and implementation express strong compare-and-set
semantics that the fake really provides, but a future provider may honestly
provide weaker guarantees or no safe mutation at all.

This work package creates the portable contract before the Beads adapter exists.
The governing separation remains:

```text
tracker provider: mutable work facts and visible coordination
Spec Kitty:       run, lease, workspace, review, and integration authority
Git:              repository history and candidate truth
```

The fake provider is the reference contract, not a simulation of Beads. It must
remain runnable with only the Go toolchain: no `bd`, Dolt, network, provider
environment variables, shell scripts, or Git hooks.

### Required design boundaries

1. Capability declarations are provider-neutral values, not provider-name
   switches and not optimistic booleans that erase guarantee levels.
2. `Revision` remains an opaque token to callers. Existing sequential fake
   revisions are valid provider-native observations; consumers may compare for
   equality but must not infer ordering, freshness, or CAS support from arithmetic.
3. A provider may offer catalog reading while refusing catalog mutation, or
   assignment observation while refusing compensation. These combinations are
   valid and must be representable.
4. Capability absence is an ordinary typed result. It is not a panic, a missing
   method discovered after partial work, or permission to emulate safety with a
   local read-then-write sequence.
5. The compatibility `Provider` interface must continue compiling for existing
   execution services and test collaborators. Decompose by embedding narrow
   interfaces with the same method signatures; do not require unrelated callers
   to implement speculative Phase 2 operations.
6. The fake may declare strong guarantees only where its file-locked mutation
   actually checks the expected revision, expected value, and operation marker
   in the same critical section as the write.
7. No tracker value or comment can acquire, extend, or recreate a Spec Kitty
   execution lease.

## Branch Strategy

Work only in the Spec Kitty lane worktree assigned for WP01. Planning artifacts
were created from `feat/beads-tracker-integration`, and completed changes must
merge back into `feat/beads-tracker-integration`.

Do not edit the primary checkout, create an additional worktree, merge to
`main`, or push directly to the protected branch. This WP has no dependencies,
so its implementation lane begins from the finalized mission planning base.

## T001: Define capability levels and provider-neutral values

**Purpose**: Make provider behavior explicit enough that application code can
branch on guarantees without importing or naming a provider.

**Files**:

- Create `internal/tracker/capabilities.go`.
- Add value-level tests in `internal/tracker/capabilities_test.go`.

**Implementation requirements**:

1. Define validated, typed capability levels for each distinction in the data
   model:
   - catalog read and create support;
   - catalog update as unsupported, unguarded, or guarded;
   - blocking graph support;
   - discussion as unsupported, append-only, or observably idempotent;
   - assignment as unsupported, unguarded, or atomic claim;
   - observation revision as unsupported, content-derived, or provider-native;
   - safe compensation as unsupported or conditionally guarded;
   - concurrent writers as none, adapter-serialized, or provider-native;
   - portable export and restorable backup support.
2. Prefer closed domain types and named constants over booleans. A zero value may
   represent unsupported only when that choice is explicit, documented, and
   validated consistently. Do not permit arbitrary strings to become valid
   capability claims.
3. Define `ProviderCapabilities` as a copyable value with deterministic
   validation. Validation must reject unknown enum values and contradictory
   claims, including safe compensation without a guarded mutation primitive.
4. Model the provider-neutral identity and observation values needed by later
   capability ports. At minimum, make stable external work references,
   structured criteria, provider observations, health/capability reports, and
   diagnostics expressible without adapter structs.
5. Preserve internal `WorkID` semantics by keeping external issue keys as
   mappings. Do not rename an external key into the authoritative internal work
   identity and do not introduce a provider-specific ID type in this package.
6. Keep criteria structured: stable criterion ID, text, verifier kind, required
   flag, and optional evidence hint. A tracker-rendered checklist is a
   projection, never an acceptance verdict.
7. Make collections defensively copied or expose construction/access patterns
   that prevent caller mutation from silently changing a validated capability
   or observation value.
8. Provide useful `String` or validation diagnostics for logging and tests, but
   do not serialize implementation-specific numeric enum values as the public
   contract.

**Tests**:

- Every declared level round-trips through its stable textual form.
- Unknown values and contradictory combinations fail deterministically.
- A minimal read-only provider capability set is valid.
- A provider can truthfully declare assignment observation with no guarded
  compensation.
- Mutating caller-owned slices or maps after construction does not alter the
  validated portable value.
- Test names and fixtures contain no provider-specific workflow vocabulary.

**Exit evidence**: `go test ./internal/tracker -run 'Capability|Criterion|ExternalWork|Observation'`.

## T002: Extend typed errors and observation-revision semantics

**Purpose**: Give callers stable, safe diagnostics for every capability refusal
and make clear that an observed revision does not itself prove a conditional
mutation primitive.

**Files**:

- Update `internal/tracker/provider.go`.
- Extend `internal/tracker/capabilities_test.go` and existing fake tests where
  necessary.

**Implementation requirements**:

1. Retain all existing `ErrorKind` values and add the portable vocabulary needed
   by this mission: timeout, version skew, schema skew, malformed protocol,
   workspace mismatch, lock contention, safe-compensation unsupported, and any
   separately named guarded-update refusal required by the contract.
2. Extend `tracker.Error` compatibly with stable recovery-code and diagnostic
   fields when useful. Existing struct literals and `errors.Is`/`errors.As`
   behavior must continue to compile and behave as before.
3. Keep provider stderr, command transcripts, credentials, environment values,
   and comment bodies out of the stable message and branching fields. A wrapped
   cause may retain local debugging context, but callers branch only on typed
   fields.
4. Document `Revision` as opaque observation identity. Equality and zero-value
   validation are allowed; ordering, incrementing, or timestamp inference by
   consumers is forbidden.
5. Provide an observation value that can retain the portable revision and, when
   available, a full canonical digest or provider-native token for diagnostics.
   The 64-bit `Revision` stays compatible with existing execution/control-store
   records; this WP must not migrate those records.
6. Distinguish:
   - a changed observation (`revision_conflict`);
   - a state no longer attributable to an operation (`attribution_lost`);
   - inability to enforce the required guard (`safe_compensation_unsupported`);
   - unknown apply status (`ambiguous`);
   - provider absence (`unavailable`).
7. Ensure the unsupported-safety error can name the requested capability and
   recovery action without requiring a provider-specific error type.

**Tests**:

- `IsKind` recognizes old and new kinds through wrapped errors.
- Stable error strings remain useful when optional fields are absent.
- Diagnostic formatting does not disclose a seeded secret or comment body.
- Equal revisions mean equal observed tokens only; tests do not assert numeric
  recency.
- Unsupported guarded compensation is distinguishable from attribution loss
  and ambiguity.

**Exit evidence**: `go test ./internal/tracker -run 'Error|Revision|Diagnostic'`.

## T003: Preserve compatibility while decomposing the current Provider contract

**Purpose**: Let consumers request the smallest capability they need without
breaking the Phase 1 saga or forcing all test doubles to implement future ports.

**Files**:

- Update `internal/tracker/provider.go`.
- Add compile-time interface tests to `internal/tracker/capabilities_test.go`.

**Implementation requirements**:

1. Extract narrow consumer-facing interfaces around the existing exact method
   signatures:
   - work observation/read;
   - assignment projection;
   - discussion append;
   - guarded compensation.
2. Express the legacy `Provider` as a compatibility composition of those narrow
   interfaces. The method set must remain source-compatible with
   `execution.TrackerSaga` and existing in-repository provider test doubles.
3. Define capability inspection/reporting as a separate narrow interface so
   adding it does not silently expand the legacy `Provider` method set.
4. Define future catalog, graph, discussion-list, assignment-observation,
   inspector, and exporter ports only to the extent their request/result values
   are stable in the mission contract. Do not add placeholder methods that
   cannot yet be given precise precondition and mutation semantics.
5. Keep interfaces owned by the consumer package. Do not add a universal
   provider base class, registration singleton, adapter switch, reflection
   dispatch, generic connector substrate, protobuf layer, or service process.
6. Verify at compile time that the old composite is satisfied by a type
   implementing the old four methods and that each narrow consumer can accept a
   collaborator implementing only its one required method set.
7. Do not change execution, helper-command, control-store, or scenario files in
   this WP. If the proposed method set requires those edits, the decomposition
   is not source-compatible and must be redesigned.

**Tests**:

- A legacy four-method fake satisfies `tracker.Provider` unchanged.
- A read-only collaborator satisfies only the read interface and is not forced
  to implement mutation.
- Capability reporting can be queried independently from legacy operations.
- No interface contains a provider-specific type or provider-name method.
- `go test ./internal/execution ./internal/scenario` compiles unchanged as a
  regression check, even though those paths are not owned by this WP.

**Exit evidence**: repository compilation proves the compatibility seam is real,
not asserted only by comments.

## T004: Upgrade the durable fake provider to declare and honor capabilities

**Purpose**: Keep the provider-neutral fake as an honest, durable reference that
advertises only guarantees enforced by its implementation.

**Files**:

- Update `internal/tracker/fake/store.go`.
- Update `internal/tracker/fake/provider.go`.
- Extend `internal/tracker/fake/provider_test.go`.

**Implementation requirements**:

1. Add capability reporting to `fake.Provider` without adding provider-specific
   configuration or changing the legacy `tracker.Provider` compatibility seam.
2. Declare the capabilities supported by the current durable store. Guarded
   assignment and guarded compensation may be strong because the expected
   revision, current value, and marker are tested under the same exclusive file
   lock as the write.
3. Do not claim catalog create/update, graph mutation, export, backup, or other
   operations merely because tests can seed or inspect the store. Test setup
   helpers are not production provider capabilities.
4. Keep `Seed`, `Inject`, `HumanAssign`, and `History` as test-control surfaces;
   do not let them satisfy production ports accidentally.
5. Ensure unsupported capability requests are rejected before entering
   `Store.update`, consuming an injected failure, appending history, advancing a
   revision, or rewriting the durable file.
6. If a generic unsupported-operation helper is introduced, make its error kind,
   capability name, and recovery code stable and provider-neutral.
7. Preserve current idempotent assignment, comment, and compensation replay
   behavior, including the invariant that a historical replay returns one
   coherent historical provider state rather than splicing it with current
   human changes.
8. Preserve durable compatibility with existing fake store version 1 unless a
   schema change is demonstrably required. If a version change is unavoidable,
   add a forward reader/migration test rather than silently rejecting existing
   fixtures.
9. Continue cloning criteria, dependencies, and comments across the storage
   boundary so callers cannot mutate durable state without a provider operation.

**Tests**:

- Declared fake capabilities validate and match compile-time interfaces.
- Every declared strong mutation is backed by an atomic critical-section test.
- Human reassignment still prevents compensation and remains untouched.
- Replay after process reopen remains idempotent and historically coherent.
- Unsupported operations leave the file bytes, history length, revision, and
  failure queue unchanged.
- The tests run with an empty provider-related environment and no executable on
  PATH beyond the normal Go test requirements.

**Exit evidence**: `go test ./internal/tracker/fake -count=10`.

## T005: Add provider-neutral and fake conformance tests

**Purpose**: Turn the capability vocabulary into an executable portable contract
whose unsupported paths are as rigorously tested as its supported ones.

**Files**:

- Create `internal/tracker/fake/conformance_test.go`.
- Complete `internal/tracker/capabilities_test.go` and
  `internal/tracker/fake/provider_test.go` as needed.

**Implementation requirements**:

1. Build table-driven assertions around declared capability levels. Supported
   operations exercise their full contract; unsupported operations must return
   the precise typed refusal before mutation.
2. Include a mutation sentinel stronger than checking only the returned error.
   Snapshot durable file content, work revision, history, and queued injected
   fault before a forbidden call, then prove all remain identical afterward.
3. Test capability validation independently from the fake so later adapters can
   reuse the vocabulary without inheriting fake storage assumptions.
4. Test old-provider compatibility and narrow-interface substitutability with
   small compile-time/test-local collaborators.
5. Exercise the fake's declared read, assignment, discussion, and compensation
   guarantees across reopen. Include same-operation replay, marker reuse with
   different semantic input, stale revision, timeout-before-apply,
   timeout-after-apply, unavailable, and direct human change.
6. Assert that an observation revision is treated opaquely in shared test logic.
   Conformance must compare tokens for equality/change and must not require a
   provider to use consecutive integers.
7. Every skipped operation must correspond to an explicit unsupported
   capability declaration. A missing method, panic, build tag, unavailable
   external executable, or unclassified error is a test failure, not a skip.
8. Avoid importing `internal/tracker/beads`, using Beads-shaped fixture names,
   or reading environment configuration. The conformance suite must remain
   provider-neutral and offline.
9. Run tests repeatedly and under the race detector. File-backed tests should
   use `t.TempDir()` and must not share mutable package-global state.

**Required adversarial cases**:

- unsupported guarded operation refuses before a failure injection is consumed;
- a stale observation cannot authorize a fake mutation;
- an operation marker cannot be reused for different semantic input;
- an ambiguous-after-apply result is resolved by observation, not blind replay;
- a human assignment invalidates operation attribution;
- safe compensation requires revision, assignee, and marker together;
- provider unavailability does not alter durable state;
- an unknown capability enum cannot be normalized into supported.

**Exit evidence**:

```bash
go test ./internal/tracker ./internal/tracker/fake -count=10
go test -race ./internal/tracker ./internal/tracker/fake
```

## Test Strategy

Run focused tests during development:

```bash
go test ./internal/tracker
go test ./internal/tracker/fake
```

Run compatibility checks before handoff:

```bash
go test ./internal/execution ./internal/helpercmd ./internal/scenario
go test ./internal/tracker ./internal/tracker/fake -count=10
go test -race ./internal/tracker ./internal/tracker/fake ./internal/execution
go vet ./internal/tracker/... ./internal/execution/...
```

If the full scenario package is slow, compilation-only evidence is not enough
for the focused tracker packages. The tracker and fake conformance commands above
must complete and pass. Do not weaken or skip a test because Beads is absent;
this WP is specifically required to operate without it.

## Definition of Done

- All capability levels from the data model are representable and validated.
- Portable values contain no provider-specific or authority-bearing semantics.
- The existing `tracker.Provider` remains source-compatible while narrow
  consumer-owned interfaces are available.
- Revision documentation and tests forbid treating observation as ordering or
  proof of provider-side CAS.
- Every portable error kind required by this WP has a stable typed identity and
  recovery classification.
- The fake declares only capabilities its durable critical sections enforce.
- Unsupported safety requests fail before lock-backed mutation, failure
  consumption, history append, revision advance, or durable rewrite.
- Existing assignment, comment, compensation, ambiguity, human-change, and
  replay behavior remains green.
- Fake conformance runs offline, repeatedly, and under `go test -race`.
- No Beads code, executable dependency, environment dependency, or workflow
  semantic enters `internal/tracker` or `internal/tracker/fake`.
- Only files listed in `owned_files` are changed.
- Record all subtask completion through the event-sourced status command:

```bash
spec-kitty agent tasks mark-status T001 T002 T003 T004 T005 --status done --mission beads-tracker-integration-01M0YG7E
```

## Risks

- **False capability promotion**: retry, serialization, or a file lock can look
  like a stronger provider guarantee. Declare only the primitive enforced at the
  external mutation boundary.
- **Compatibility break**: adding capability reporting directly to the legacy
  composite would force edits across execution and scenario test doubles. Keep
  reporting separate and compose narrowly.
- **Fat-interface gravity**: putting all future operations on one interface
  makes unsupported providers dishonest. Consumers should request one small
  port at a time.
- **Revision overclaim**: the fake's integer happens to increase, but portable
  callers must not rely on that property.
- **Test-helper leakage**: `Seed`, `Inject`, `HumanAssign`, and `History` are
  powerful but are not provider capabilities.
- **Decorative refusal tests**: checking an error without checking durable state
  can miss a mutation performed before refusal.
- **Provider vocabulary leakage**: convenient names from the first production
  adapter can turn a portable contract into a disguised adapter API.
- **Secret leakage**: expanded diagnostics can accidentally include work or
  comment bodies. Stable diagnostics must remain bounded and redacted.

## Reviewer Guidance

Reject this work package if capability support is represented only as booleans,
if provider names drive application behavior, or if unsupported safety is
emulated through read-then-write. Verify that the exact guarantee distinctions
in `data-model.md` survive into typed values and validation.

Inspect the `Provider` method set carefully. Existing execution collaborators
must still compile without implementing capability reporting or speculative
catalog/graph/export methods. Confirm that new narrow interfaces are
consumer-owned and do not import adapter code.

For the fake, follow every declared strong capability to the critical section
that enforces it. Confirm revision, value, and marker checks occur under the same
exclusive lock as compensation. Run the unsupported-before-mutation test with a
queued failure and verify the queue is still present afterward; this mutant
control proves the refusal happened before touching the provider.

Finally, run the offline and race commands exactly as documented. Reject skips
conditioned on Beads installation, environment variables, or network access.
The fake is the portable reference and must stay intentionally less
provider-shaped than the production adapter that will follow.
