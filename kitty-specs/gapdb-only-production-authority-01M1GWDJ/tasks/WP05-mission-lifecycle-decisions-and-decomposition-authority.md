---
work_package_id: WP05
title: Mission Lifecycle Decisions and Decomposition Authority
dependencies:
- WP04
requirement_refs:
- FR-007
- FR-017
- FR-018
- FR-023
- FR-024
- FR-025
- FR-033
- NFR-001
- NFR-004
- NFR-016
- C-004
- C-005
- C-009
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T023
- T024
- T025
- T026
- T027
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/mission/
create_intent:
- internal/controlstore/gapdb/mission_authority.go
- internal/controlstore/gapdb/mission_authority_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/mission/**
- internal/controlstore/gapdb/mission_authority.go
- internal/controlstore/gapdb/mission_authority_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP05 - Mission Lifecycle Decisions and Decomposition Authority

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the
frontmatter, and behave according to its guidance before parsing the rest of
this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If profile resolution fails, run `spec-kitty agent profile list`, resolve the
closest implementation profile, and record that governed profile invocation
before editing production code.

## Objective

Move mission creation, lifecycle, action, blocker, decision, and decomposition
authority onto capability-owned versioned events and the GapDB-only Authority
Journal v2. Preserve the qualified Phase 1–3 outcomes and refusal ordering while
replacing table-shaped persistence contracts, process-local reservation, and
provider-first decomposition with one-event-per-aggregate atomic publications.

Implementation command:

```bash
spec-kitty agent action implement WP05 --agent codex
```

## Branch, dependency, and ownership rules

- Start from the accepted WP04 revision assigned to WP05 in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- The planning and merge target is `feat/gapdb-only-production-authority`.
- Use only the Spec Kitty lane worktree; do not create a worktree manually.
- Edit only `internal/mission/**` and
  `internal/controlstore/gapdb/mission_*.go`.
- Preserve other agents' concurrent edits and never revert unrelated changes.
- Consume WP02 journal and WP03 GapDB contracts; do not modify or bypass them.
- Consume WP04 restore/playback contracts; do not add another replay engine.
- Provider-neutral tracker contracts may be called only by mission application
  services after durable effect intent. No tracker adapter enters the kernel.

Read `spec.md`, `plan.md`, `research.md`, `data-model.md`,
`contracts/authority-journal-v2.md`, and
`contracts/event-family-registry-v2.json` before changing code. Treat the
frozen behavior inventory and existing mission tests as behavioral evidence,
not as a mandate to preserve store method shapes.

## Product laws carried by this WP

1. Mission state is authoritative only when reduced from admitted journal
   events; a tracker observation, Markdown artifact, process, or projection is
   never mission authority.
2. Mission commands own semantic validation and refusal order; the shared
   journal validates publication mechanics but interprets no mission policy.
3. Each accepted stage publishes its OperationState transition, mission-family
   events, aggregate heads, effect intents, and guard conditions atomically.
4. Exact OperationID replay resolves before current-epoch validation; changed
   binding refuses before mission reads or provider calls.
5. One publication emits at most one transition event for each affected
   aggregate, with exactly one logical-version increment.
6. Decisions and blockers are explicit versioned aggregates, not string arrays
   that are silently rewritten with the mission snapshot.
7. Decomposition records meaningful requested authority plus attributable
   effect intent before a tracker mutation. A naked reservation is forbidden.
8. Tracker assignment or work existence cannot grant execution, review,
   acceptance, integration, or mission-transition authority.
9. Compensation or retry is allowed only after comparing provider revision,
   semantic value, and operation marker with the recorded intent.
10. Pure reducers accept recorded data only and perform no persistence,
    provider, Git, filesystem, agent, transport, or clock operation.

## T023: Define capability-owned mission ports, events, and reducer

**Purpose**: Replace store-shaped mission APIs with inward-owned semantic
contracts and a complete registered mission event family.

**Files**: existing or new files under `internal/mission/**` only.

**Implementation requirements**:

1. Inventory every current mission persistence seam: create, load, transition,
   action, fact/decision/blocker, finalization-facing mission facts,
   decomposition operation authority, and replay.
2. Replace broad CRUD/table assumptions with narrow command-side publication
   and query-side observation ports owned by `internal/mission`.
3. Keep commands and results typed around MissionID, ProjectID, OperationID,
   ActorID, ProcessID, governance/profile/policy revisions, exact expected
   mission version, and exact artifact/work authority where applicable.
4. Define bounded versioned events for at least mission created, lifecycle
   advanced, mission paused/resumed, mission failed/canceled/completed, action
   recorded, blocker raised/cleared, decision opened/resolved/deferred/canceled,
   decomposition requested/progressed/reconciliation-required/completed.
5. Combine multiple facts about the same mission aggregate into one semantic
   transition event per publication. Do not emit several same-version mission
   events to simulate SQL row writes.
6. Model blocker and decision coordinates as their own aggregates when their
   versions can contend independently; update the mission census/head in the
   same commit plan when the active/open set changes.
7. Register the mission family tuple required by the v2 event registry and
   reject unknown event schema/reducer versions.
8. Implement pure deterministic reducers that validate prior logical version,
   identity, event membership, timestamps as recorded facts, terminal
   monotonicity, blocker/decision census, and state digest.
9. Ensure all returned slices/bytes/maps are defensive or immutable and all
   inputs are bounded before canonicalization or allocation.
10. Keep GapDB keys, provider revisions, tracker adapters, SQLite concepts, and
    checkpoint traversal outside the domain/application contracts.

**Required tests**:

- Every event reduces from a valid prior state to the exact expected state.
- Each invalid identity/version/transition/census mutation refuses typed.
- Unknown event/reducer versions fail closed.
- Repeated reduction is byte-identical and has zero adapter calls.
- A generic `SaveMission`/raw-key/god-store mutant violates a compile-time or
  architecture assertion.

**Exit evidence**:

```bash
go test ./internal/mission -run 'Event|Reducer|Port|Registry' -count=10
```

## T024: Publish lifecycle, actions, blockers, and decisions atomically

**Purpose**: Preserve mission behavior and earliest refusal semantics while
making every accepted mutation one complete Authority Journal publication.

**Files**: `internal/mission/**`.

**Implementation requirements**:

1. Route create, lifecycle transition, pause/resume, terminal outcome, action,
   blocker, and decision commands through WP02 OperationBinding/OperationState
   resolution and sealed CommitPlan construction.
2. Resolve an exact existing operation and recorded result before validating
   the current runtime epoch. On binding conflict, refuse before capability
   reads, journal mutation, or external effects.
3. For an absent operation, validate syntax/identity/governance, current epoch,
   mission existence/version/definition, requested transition/action, exact
   artifact or work evidence, then blocker/decision policy in the frozen
   behavior-inventory order. Do not invent a new precedence to fit storage.
4. Represent every mutable fact used by acceptance as a head or eligibility
   guard condition in the same sealed plan. Read-then-write without matching CAS
   is forbidden.
5. Atomically publish the operation transition, mission event/head, any
   blocker/decision event/head, guard transition, typed result, and outbox/effect
   intent explicitly assigned to this action.
6. Preserve terminal monotonicity: failed, canceled, and completed missions
   cannot reopen or silently change terminal outcome.
7. Preserve mission-definition commitment and exact approval/revision evidence;
   later artifact publication cannot retroactively authorize an old command.
8. Persist doctrine/profile/policy revision provenance with the operation and
   action event where the command is governed.
9. On CAS loss, reread the global operation first. Return its result if the same
   operation committed; otherwise report the exact moved mission/decision/
   blocker/guard authority as a typed stale/conflict outcome.
10. Never auto-generate a new OperationID, retry a newly decided plan, or expose
    success before a durable acknowledgement or same-publication resolution.

**Required refusal-order tests**:

- Same OperationID/different binding precedes every mission read.
- Invalid governance precedes current-state mutation.
- Stale epoch for an absent operation precedes mission publication.
- Mission version/definition mismatch precedes action policy.
- Missing/stale required artifact approval precedes transition publication.
- Active blocker or open decision returns its frozen typed kind with zero
  durable/external mutation.
- Terminal mission refuses before any successor action.
- Exact replay returns byte-identical result after restart even when the
  original epoch is no longer current.

**Exit evidence**:

```bash
go test ./internal/mission -run 'Lifecycle|Action|Blocker|Decision|Refusal|Replay' -count=10
```

## T025: Make decomposition a durable requested/effect saga

**Purpose**: Remove the naked `ReserveDecompositionOperation` model and ensure
tracker work creation/update is always preceded by meaningful local authority.

**Files**: `internal/mission/decomposition*.go` and related mission tests.

**Implementation requirements**:

1. Replace reservation with a first atomic publication containing immutable
   OperationBinding, OperationState, `decomposition_requested`, the mission
   transition/progress fact, and one durable external-effect intent per planned
   provider mutation.
2. Bind the requested event to the exact mission state, approved plan revision
   and content digest, proposed semantic work, approved scope, provider
   capability observation, actor/process/governance coordinates, and ordered
   effect membership.
3. Derive each child provider identity deterministically from the parent
   OperationID, effect kind, and stable WorkID. Record it before the call and
   reuse it across timeout, retry, restart, or reconciliation.
4. Never allocate a fresh provider child OperationID during replay or after an
   ambiguous outcome. Reordering proposals must not change child identities.
5. Call the tracker only after the requested/effect-intent publication is
   durably acknowledged. Provider capability checks before intent may inspect
   declared support but may not mutate external state.
6. Record provider mutation and re-observation as immutable observations in a
   later operation stage. Compare provider ID, external ref, WorkID, canonical
   work definition/value, provider revision, and operation marker.
7. Treat unavailable, stale, contradictory, prior, missing-marker, or
   unattributable provider responses as ambiguous or
   reconciliation-required. Never infer completion from a success string.
8. Compensation may act only when revision, value, and marker still prove the
   external state belongs to this effect. A human/provider change blocks blind
   undo and records explicit reconciliation evidence.
9. Publish `decomposition_completed` only after every intended definition is
   re-observed, attributable, semantically complete, and reducible into one
   valid workgraph draft. Partial work remains explicit progress.
10. Tracker results never advance execution authority or finalize a
    WorkRevision; later workgraph authority owns those decisions.

**Required tests**:

- Crash before first publication produces no provider calls.
- Crash after requested publication and before provider call resumes with the
  same parent and child identities.
- Lost provider response re-observes and records attribution without duplicate
  mutation.
- Proposal permutation preserves request digest, EffectIDs, and child IDs.
- Provider child-ID drift mutant is killed.
- Naked reservation and provider-call-before-intent mutants are killed.
- Human reassignment/revision change blocks compensation.
- Partial decomposition survives restart without being labeled complete.

**Exit evidence**:

```bash
go test ./internal/mission -run 'Decomposition|Effect|Attribution|Compensation' -count=10
```

## T026: Implement the GapDB mission adapter and restart replay

**Purpose**: Translate sealed mission publications through WP03 without giving
the adapter mission policy or creating a second authority path.

**Files**: `internal/controlstore/gapdb/mission_*.go` and adapter-facing tests
within the owned wildcard surfaces.

**Implementation requirements**:

1. Implement the narrow mission-owned ports using WP03's v2 committer, reader,
   canonical key codec, and ambiguity classifier. Do not call raw GapDB from
   `internal/mission`.
2. Translate one sealed CommitPlan into one provider atomic batch; never split
   the operation, mission, decision/blocker, guard, or effect-intent records.
3. Enforce absent global OperationBinding on first publication and exact
   record-revision plus logical-version CAS for every affected head/guard.
4. Store immutable per-aggregate logical-version events and update their heads
   in the same batch. Physical event/head deletion is forbidden.
5. Implement mission queries from the restored owner-held projection or exact
   v2 observations specified by the port; do not expose raw keys or a database
   handle to callers.
6. Reconcile timeout/EOF/malformed acknowledgement by PublicationID and exact
   operation/event/head membership. Complete matching membership succeeds;
   proven absence permits same-plan retry; partial/contradictory membership
   closes authority.
7. After restart, use WP04 playback output and global operation state so exact
   retries return original results before new-epoch admission checks.
8. Prove current mission, decision, blocker, action, decomposition progress,
   effect intents, and results rebuild byte-identically from checkpoint plus
   suffix and from bounded origin replay.
9. Return bounded typed mission outcomes; do not leak provider error strings,
   keys, record values, socket details, or implementation types.
10. Add no SQLite/SQL import, backend selector, fallback, dual write, automatic
    import, legacy eligibility, or table-compatible API.

**Required tests**:

- One accepted mission command causes exactly one adapter batch call.
- Same-mission contenders produce one winner and typed losers with no partial
  events, heads, operations, or intents.
- Disjoint mission aggregates can publish concurrently without a global head.
- Response loss reconciles the exact committed result once.
- Restart playback preserves canonical state/result/effect membership.
- Partial publication fixture closes rather than fabricating a result.

**Exit evidence**:

```bash
go test ./internal/controlstore/gapdb -run 'Mission|Decomposition|MissionReplay' -count=10
```

## T027: Prove parity, contention, faults, dishonest providers, and mutants

**Purpose**: Demonstrate that storage replacement preserves mission product
behavior under conflict, crash, restart, ambiguity, and hostile observation.

**Files**: tests under the two WP05-owned wildcard surfaces only.

**Behavior and refusal parity**:

1. Map every frozen Exact/Strengthen mission create, transition, action,
   blocker, decision, pause/resume, terminal, and decomposition behavior to an
   executable v2 assertion.
2. Compare typed result kind, canonical result bytes, authority mutation count,
   effect call count, and refusal precedence with the qualified baseline.
3. Replace SQL-row assertions with domain state, journal membership, aggregate
   versions, and projection digests. Do not compile SQLite as a comparator.
4. Preserve complete expected-snapshot/request commitments; mutate each
   identity, version, approval, governance, blocker, decision, and terminal
   coordinate independently.

**Contention and fault campaigns**:

1. Run at least 100 incompatible same-mission operations per mutable aggregate
   family; exactly one wins and every loser is typed stale/conflict.
2. Run at least 100 disjoint-mission pairs; both may succeed without a global
   mission mutex, journal head, or operation bottleneck beyond unique IDs.
3. Inject before publication, after durable publication/before response, after
   response loss, before provider effect, after provider effect/before
   observation, during observation, and before final saga publication.
4. For every cut, assert zero partial authority, exactly one committed result,
   stable operation/effect identities, bounded recovery, and universal journal
   invariants after success and failure.
5. Restart at every durable boundary and compare projection/result digests with
   uninterrupted reference execution.

**Dishonest-provider fixtures**:

- stale provider revision with otherwise matching value;
- matching revision with changed value;
- missing, changed, or foreign operation marker;
- preexisting result on a locally fresh operation;
- success without attributable observation;
- re-observation that contradicts the mutation response;
- provider outage, timeout, partial completion, and recovery with human change.

Every dishonest response must yield explicit ambiguity/reconciliation or a
typed provider refusal. It must never grant mission/work execution authority,
retry under a new identity, clear a human change, or mark decomposition done.

**Mandatory controlled mutants**:

- generic/table-shaped mission store replaces consumer-owned ports;
- mission service imports the provider adapter;
- global OperationID key becomes mission-scoped;
- binding collision check occurs after mission/provider reads;
- current-epoch validation occurs before exact replay;
- mission head writes without operation/event/guard CAS;
- blocker or open-decision gate is skipped or reordered;
- terminal mission reopens;
- provider mutation precedes effect intent;
- decomposition uses a naked reservation;
- child provider operation identity changes on retry;
- completion accepts an unattributed or stale observation;
- compensation ignores revision, value, or marker;
- reducer calls a persistence/provider port;
- tracker assignment grants downstream authority.

Each mutant must have a passing control and fail for the intended typed reason;
grep-only source assertions do not prove semantic rejection.

**Validation commands**:

```bash
go test ./internal/mission ./internal/controlstore/gapdb -count=10
go test -race ./internal/mission ./internal/controlstore/gapdb -count=1
go vet ./internal/mission ./internal/controlstore/gapdb
staticcheck ./internal/mission ./internal/controlstore/gapdb
go test ./internal/architecture -count=1
go test ./... -run '^$' -count=1
gofmt -d internal/mission internal/controlstore/gapdb
git diff --check
```

## Definition of Done

- Mission ports, events, states, reducers, and refusal kinds are capability
  owned and provider-neutral.
- Lifecycle, action, blocker, decision, and terminal changes publish atomically.
- Exact replay precedes epoch validation and preserves byte-identical results.
- Decomposition publishes meaningful request plus effect intents before calls.
- Parent, effect, and child provider identities remain stable across restart.
- Dishonest or unattributable provider state requires explicit reconciliation.
- The GapDB mission adapter uses one sealed v2 batch with all required CAS.
- Checkpoint/suffix and origin playback reconstruct identical mission authority.
- Frozen parity, refusal, contention, fault, race, and mutant tests pass.
- No SQLite, fallback, dual-write, raw-key, god-store, or tracker-authority path
  exists in the owned production surface.
- All five subtasks are recorded `done` through the canonical task event log.
- The committed WP is handed to an independent reviewer and is not self-merged.

After implementation and validation genuinely complete, record status:

```bash
spec-kitty agent tasks mark-status T023 T024 T025 T026 T027 \
  --status done \
  --mission gapdb-only-production-authority-01M1GWDJ \
  --json
```

## Risks and required mitigations

| Risk | Mitigation |
|---|---|
| Existing interfaces pull SQL transaction shape forward | Replace them with narrow mission-owned semantic publication/query ports. |
| Blocker/decision census diverges from their aggregates | Publish census and changed aggregates in one guarded plan. |
| A process-local reservation survives under a new name | Require a meaningful requested event and effect intents in the first batch. |
| Provider retries fork child operation identity | Derive and persist child identity before the first provider call. |
| Provider lies or a human changes state during recovery | Compare revision, value, and marker; record reconciliation when attribution fails. |
| CAS conflict changes refusal precedence | Resolve same operation first, then identify the exact moved authority. |
| Mission reducer performs convenient I/O | Keep its signature data-only and kill an adapter-capability mutant. |
| Contention is serialized through a global mission head | Prove disjoint missions progress concurrently under race/stress tests. |

## Independent reviewer guidance

The reviewer must round-trip every relevant requirement and inspect production
wiring, not merely green unit tests. Reject dead ports, events, reducers, or
adapter functions without a non-test consumer in this WP or a declared direct
dependent. Verify the implementation preserves behavioral outcomes without
retaining the prior store's method/table structure.

Review the complete operation chronology for one lifecycle command and one
decomposition saga. For the latter, require visible evidence of:

1. exact replay/collision resolution;
2. current epoch and mission/plan validation;
3. atomic requested event plus effect-intent publication;
4. stable provider child identity;
5. provider call only after durable acknowledgement;
6. revision/value/marker re-observation;
7. explicit ambiguity or reconciliation when attribution is incomplete; and
8. atomic terminal operation/mission/decomposition publication.

Explicitly run or inspect mutants for replay-before-epoch, no naked reservation,
provider-call-before-intent, child-ID drift, dishonest observation, blind
compensation, blocker/decision refusal ordering, and tracker authority leakage.
Reject any design whose safety relies on process locks, event delivery order,
watch delivery, wall-clock timing, provider honesty, or SQLite comparison.
