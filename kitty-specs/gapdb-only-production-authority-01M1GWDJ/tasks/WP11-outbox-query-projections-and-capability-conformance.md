---
work_package_id: WP11
title: Outbox Query Projections and Capability Conformance
dependencies:
- WP05
- WP06
- WP07
- WP08
- WP09
- WP10
- WP18
requirement_refs:
- FR-002
- FR-019
- FR-023
- FR-024
- FR-025
- FR-034
- FR-036
- NFR-005
- NFR-010
- NFR-015
- NFR-016
- C-004
- C-005
- C-009
- C-010
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T054
- T055
- T056
- T057
- T058
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controlstore/
create_intent:
- internal/controlstore/gapdb/outbox_authority.go
- internal/controlstore/gapdb/outbox_authority_test.go
- internal/controlstore/gapdb/projection_authority.go
- internal/controlstore/gapdb/projection_authority_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/controlstore/store.go
- internal/projection/**
- internal/controlstore/gapdb/outbox_authority.go
- internal/controlstore/gapdb/outbox_authority_test.go
- internal/controlstore/gapdb/projection_authority.go
- internal/controlstore/gapdb/projection_authority_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP11 - Outbox Query Projections and Capability Conformance

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

Complete the shared control-store surface with consumer-owned outbox contracts,
GapDB-backed intent and delivery evidence, and a disposable memory-primary
projection for bounded mission, work, operation, and progress queries. Prove
that every migrated capability preserves its frozen outcomes and refusal order
without turning projections, delivery evidence, or provider observations into a
second authority.

Implementation command:

```bash
spec-kitty agent action implement WP11 --agent codex
```

## Branch, dependency, and ownership rules

- Start from the dependency-qualified WP11 lane declared in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json` only after
  WP05, WP06, WP07, WP08, WP09, WP10, and WP18 are accepted.
- The planning and merge target is `feat/gapdb-only-production-authority`.
- Use only the Spec Kitty-created lane worktree; do not create one manually.
- Edit only `internal/controlstore/*.go`, `internal/projection/**`,
  `internal/controlstore/gapdb/outbox_*.go`, and
  `internal/controlstore/gapdb/projection_*.go`.
- Preserve other agents' concurrent edits and never revert unrelated changes.
- Consume the admitted event families and ports from WP05–WP10. Do not repair a
  capability by changing its domain package from this WP.
- Reuse WP02 operation/publication semantics, WP03 GapDB codecs and committer,
  and WP04 checkpoint-plus-suffix restore. Do not add parallel mechanisms.
- Keep provider SDK types, raw keys, SQL vocabulary, filesystem state, Git
  state, and server process ownership out of projection and outbox contracts.

Before editing, read `spec.md`, `plan.md`, `research.md`, `data-model.md`, all
contracts, the frozen Phase 1–3 behavior inventory, and the public contracts of
WP02–WP10. Treat old table-oriented APIs as behavior evidence only.

## Product laws carried by this WP

1. Every external mutation is preceded by durable, attributable effect intent
   in the same authority publication as the requesting domain transition.
2. Outbox delivery is an external effect. Attempts and observations are durable
   evidence but never mission, work, review, acceptance, or integration truth.
3. A committed-but-unacknowledged publication is reconciled by its stable
   PublicationID and OperationID; ambiguity never licenses a blind new effect.
4. The in-memory projection is published only after an acknowledged or
   reconciled durable commit and is always safe to discard.
5. Playback is pure: no provider, tracker, Git, filesystem, agent, compensation,
   network, clock, or durable-write capability is reachable from reducers.
6. Query, list, and watch surfaces observe authority but cannot construct a
   CommitPlan, mint authority, dispatch effects, or mutate durable state.
7. Watches are bounded invalidation or change notifications, not a durable
   recovery stream and not authority.
8. Unknown events, inconsistent heads, incomplete operation membership, corrupt
   checkpoints, and projection digest mismatch fail closed before admission.
9. Capability conformance compares semantic outcomes, refusals, ordering,
   mutation count, and evidence rather than preserving obsolete store shapes.
10. Stable successor contracts remain capability-shaped and consumer-owned;
    there is no replacement god-store or universal repository.
11. Mission progress projects WP18's complete authorized/unavailable action
    census. It never collapses readiness back into one current phase or one
    chosen successor.

## Required cross-cutting semantics

- Exact OperationID replay is resolved before current-epoch validation.
- Same OperationID with a changed canonical binding refuses before domain reads
  beyond the operation record and before any external effect.
- One admitted semantic stage produces one sealed CommitPlan, one AtomicBatch,
  and one positive GapDB revision with exact all-or-nothing membership.
- Provider revisions are concurrency metadata, never domain logical versions.
- Refusals and diagnostics are typed, bounded, deterministic, and free of raw
  payloads, secret material, raw keys, or unbounded provider errors.
- Missing complete eligibility refuses. No `legacy-v1` selection or fallback is
  permitted through query, projection, or conformance helpers.

### Subtask T054 - Define consumer-owned outbox intent and delivery evidence

#### Purpose

Define narrow provider-neutral contracts for recording effect intent, dispatch
attempts, observations, ambiguity, reconciliation, and delivery evidence without
allowing delivery state to become product authority.

#### Steps

1. Inventory the outbox needs of mission, execution, handoff, review,
   acceptance, and integration without creating a general persistence API.
2. Define consumer-owned command and query ports in `internal/controlstore`
   using `OperationID`, `PublicationID`, `EffectID`, provider idempotency marker,
   intended mutation digest, attribution coordinates, and bounded evidence.
3. Represent the required lifecycle: intent recorded, attempt started,
   observation recorded or ambiguous, reconciliation required, and then only a
   legal retry, compensation, refusal, or resolution.
4. Require intent to contain the expected provider revision, expected value,
   attribution marker, intended mutation, actor, capability, and source
   operation needed for compare-before-compensate.
5. Make attempt identity stable across timeout and restart. Never manufacture a
   new top-level OperationID for a provider-generated child identity unless the
   registry explicitly classifies that identity as top-level.
6. Separate durable delivery evidence from the authoritative domain outcome.
   Projection helpers may report both but must not infer one from the other.
7. Define typed bounded failures for missing intent, mismatched binding,
   illegal lifecycle movement, unattributed observations, ambiguous outcome,
   and unsafe compensation.
8. Keep interfaces capability-shaped. Do not expose generic event append, raw
   journal scans, raw GapDB values, provider revisions, or SDK errors.

#### Files

- `internal/controlstore/*.go`

#### Validation

- Contract tests prove dispatch cannot start without durable intent.
- Changed payload, expected revision, marker, or attribution under the same
  OperationID/EffectID refuses before dispatch.
- Delivery evidence never changes mission or work authority in reducer tests.
- Compensation refuses unless the later observation still matches all required
  attribution coordinates.

### Subtask T055 - Implement GapDB outbox publication, replay, and recovery

#### Purpose

Back the outbox contracts with the common v2 journal and GapDB AtomicBatch while
preserving stable retry and honest ambiguity across restart.

#### Steps

1. Implement only the provider translation required by the T054 ports in
   `internal/controlstore/gapdb/outbox_*.go`.
2. Reuse canonical v2 keys, envelopes, digest validation, OperationState,
   PublicationID, aggregate heads, event indexes, and batch limits from WP02 and
   WP03. Do not create an outbox-specific transaction engine.
3. Include effect intent in the requesting command's AtomicBatch; never publish
   requested authority first and append intent in a later write.
4. Publish attempt, observation, ambiguity, reconciliation, retry permission,
   compensation permission, refusal, and resolution as registered versioned
   evidence events with exact expected heads.
5. On lost response or malformed acknowledgement, reconcile the original
   PublicationID before returning, retrying, or allowing another attempt.
6. If reconciliation proves the batch committed, return the recorded result. If
   it proves absence, permit replanning under the same stable operation. If the
   result stays ambiguous, remain unresolved and forbid blind retry.
7. Restore outbox and operation progress using the WP04 checkpoint-plus-suffix
   algorithm. Do not use Watch history or process-local queues for recovery.
8. Validate event family/version, coordinate, digest, operation membership,
   EventIndex ordering, and terminal heads before exposing recovered work.
9. Preserve exact same-binding replay without duplicate effects or events; a
   changed binding returns deterministic idempotency conflict.

#### Files

- `internal/controlstore/gapdb/outbox_*.go`

#### Validation

- Inject faults before commit, after commit before acknowledgement, during
  reconciliation, and after restart; prove no intent or outcome is fabricated.
- Prove intent plus requested domain events are all present or all absent.
- Prove ambiguous effects cannot auto-retry or compensate.
- Prove replay and restart preserve EffectID, attempt identity, attribution,
  lifecycle position, and bounded diagnostics.

### Subtask T056 - Build the disposable memory-primary projection

#### Purpose

Compose immutable snapshots or owner-serialized maps for mission, work,
operation, and progress views that rebuild deterministically from admitted
history and remain disposable.

#### Steps

1. Define a projection model covering current journal revision, runtime epoch,
   mission state, current WorkRevision and lineage, work status, blockers,
   leases/runs, operation progress, effects, reviews, acceptance, integration,
   and legal next actions.
2. Consume only validated capability events and restored checkpoint state. Never
   inspect GapDB raw keys, tracker/Git/filesystem state, or provider observations
   outside their admitted attributed events.
3. Implement pure reducers for every registered family owned by WP05–WP10 and
   fail closed if the event or reducer registry is incomplete.
4. Apply one complete provider revision atomically to a candidate snapshot.
   Publish the new snapshot only after all operation membership, event ordering,
   aggregate versions, and whole-projection digest checks pass.
5. Use immutable snapshot replacement or a single owner-serialized mutation
   discipline so readers never observe half an operation.
6. Keep durable commit on the critical path and projection publication after
   acknowledgement or definitive reconciliation. Never acknowledge authority
   from optimistic projection state.
7. Rebuild from the newest compatible checkpoint plus exact suffix through WP04
   and compare the resulting canonical projection bytes and digest.
8. Allow complete memory loss and deterministic reconstruction without writing
   projection rows or consulting Watch retention.
9. Bound retained snapshots, indexes, strings, collections, and diagnostic
   evidence according to the mission budgets.

#### Files

- `internal/projection/**`
- `internal/controlstore/gapdb/projection_*.go`

#### Validation

- Compare uninterrupted and restored canonical projection bytes for all event
  families over at least the mission-declared deterministic trials.
- Race readers with complete revision publication and prove no partial state.
- Corrupt, omit, duplicate, reorder, or version-skew an event and prove the
  candidate snapshot is never published.
- Instrument playback and prove it invokes no adapter or durable-write seam.

### Subtask T057 - Expose bounded authority-free query, list, and watch APIs

#### Purpose

Serve fast current-state observations from the memory projection without
granting callers storage access, mutation capability, or authority tokens.

#### Steps

1. Define typed query interfaces for supported mission, work, operation, effect,
   and progress views. Include revision and epoch attribution in every snapshot.
2. Define bounded list interfaces with stable canonical ordering, explicit page
   size limits, opaque projection-owned cursors, and stale-cursor behavior.
3. Define watches as bounded notifications tied to projection revision/epoch.
   They may prompt a fresh query but cannot replay durable history or recover the
   database.
4. Surface WorkRevision identity, blockers, active runs/leases, operation stage,
   unresolved effects, and legal next actions only when derivable from admitted
   projection state.
5. Make query DTOs immutable copies or read-only snapshots. Do not return maps,
   slices, pointers, builders, condition tokens, repositories, or commit seams
   that let a caller mutate shared state.
6. Reject over-budget filters, list sizes, cursors, watchers, response bytes, and
   diagnostic detail before unbounded allocation or fan-out.
7. On epoch change or subscriber lag beyond the bounded buffer, close/refuse the
   watch with a typed resync-required result; never silently skip authority.
8. Ensure Get/List/Watch calls perform no durable mutation, checkpoint creation,
   provider effect, or event append.

#### Files

- `internal/controlstore/*.go`
- `internal/projection/**`

#### Validation

- Mutation spies prove every query/list/watch path is authority-free.
- Ordering and pagination remain identical across restart and map insertion
  order; malformed or stale cursors fail deterministically.
- Slow watcher tests prove bounded memory and explicit resync behavior.
- A bounded local benchmark records Get/List p95 across at least 10,000
  observations and demonstrates the NFR-010 threshold on the reference machine.

### Subtask T058 - Prove cross-capability conformance and performance

#### Purpose

Create one capability-level qualification matrix that maps frozen behavior to
the v2 event/projection implementation and detects semantic, authority, replay,
latency, or race regressions.

#### Steps

1. Enumerate every migrated capability from WP05–WP10: mission, decision,
   blocker, artifact, WorkRevision, reconciliation, run, lease, workspace,
   governance, handoff, review, acceptance, integration, effect, and outbox.
2. Map each frozen accepted transition and refusal to its v2 command, result,
   mutation count, event family, aggregate heads, effect intent, projection
   change, and exact diagnostic kind.
3. Run accepted and refused sequences through public consumer-owned ports, never
   through raw adapter helpers or direct GapDB inspection.
4. Assert refusal precedence, no-mutation refusals, stable OperationID replay,
   changed-binding conflict, and one logical-version increment per affected
   aggregate.
5. Compare live and restored projection bytes/digests around clean and unclean
   restart, checkpoint fallback, and exact suffix replay.
6. Cover same-aggregate contention, unrelated-aggregate independence, guarded
   stale-generation races, watcher concurrency, and owner-serialized snapshot
   publication under a bounded race profile.
7. Record query latency using fixed fixtures, warmed and declared runs, sample
   counts, percentile method, reference machine data, and explicit thresholds.
8. Add mutants that must fail when projection writes become authority, delivery
   evidence changes mission state, playback invokes an adapter, a refusal moves
   later, a mutation disappears from batch membership, a list becomes
   unbounded, a watch becomes recovery, or a generic store leaks through.
9. Preserve C-004, C-005, C-009, and C-010 structural locks: pure kernels,
   consumer-owned ports, attributable external observations, and integration
   ownership fences remain visible in tests.

#### Files

- `internal/controlstore/*.go`
- `internal/projection/**`
- `internal/controlstore/gapdb/outbox_*.go`
- `internal/controlstore/gapdb/projection_*.go`

#### Validation

Run focused tests first, then the bounded qualification surface:

```bash
go test ./internal/controlstore ./internal/controlstore/gapdb ./internal/projection/...
go test -race ./internal/controlstore ./internal/controlstore/gapdb ./internal/projection/...
go test ./internal/architecturetests
go vet ./internal/controlstore/... ./internal/projection/...
staticcheck ./internal/controlstore/... ./internal/projection/...
git diff --check
```

Resolve package patterns with `go list` if the installed `go vet` or
`staticcheck` requires explicit packages. Keep stress and latency profiles
bounded and separate from ordinary unit tests. Do not substitute the known long
scenario suite for focused conformance evidence.

## Definition of Done

- T054–T058 are complete only within the declared owned files.
- Consumer-owned outbox ports represent intent, attempt, observation,
  ambiguity, reconciliation, retry/compensation permission, and resolution.
- Requested authority and effect intent publish atomically; delivery evidence
  remains attributable observation and never transfers authority.
- GapDB replay and restart preserve stable operation, publication, effect, and
  attempt identities without blind retry or duplicate effect.
- The mission/work/operation/progress projection is memory-primary, atomic to
  readers, bounded, deterministic, reconstructible, and disposable.
- Query, list, and watch interfaces are bounded and incapable of minting or
  mutating authority; Watch is not used for durable recovery.
- Every WP05–WP10 capability has explicit accepted/refused behavior mapping,
  batch-membership evidence, replay evidence, and projection expectations.
- At least the mission-declared restore comparisons produce byte-identical
  canonical projections, and corrupt/incomplete histories fail closed.
- Focused tests, architecture checks, race runs, mutation checks, `go vet`,
  `staticcheck`, and `git diff --check` pass.
- The NFR-010 query latency measurement uses 10,000 or more observations and
  records a reproducible p95 result against the declared reference machine.
- The implementation handoff records exact commands, outcomes, benchmark
  conditions, and any environment-qualified caveat.
- Completion is recorded through the task event log, never frontmatter edits:

```bash
spec-kitty agent tasks mark-status T054 T055 T056 T057 T058 \
  --status done \
  --mission gapdb-only-production-authority-01M1GWDJ
```

## Risks and mitigations

- **Authority transfer through delivery:** Treat all delivery status as evidence
  and require domain authority to come only from admitted capability events.
- **Blind ambiguity retry:** Reconcile the original PublicationID and stable
  effect identity; unresolved ambiguity remains explicit.
- **Projection second truth:** Never persist projection mutations as authority;
  discard and rebuild them from checkpoint plus admitted journal suffix.
- **Optimistic visibility:** Publish a snapshot only after acknowledged or
  reconciled durable commit and after complete-revision validation.
- **Impure playback:** Construct reducers without adapter capabilities and fail
  tests on any provider, clock, filesystem, Git, agent, or durable-write call.
- **Unbounded observations:** Cap pages, watchers, buffers, evidence, strings,
  cursors, and diagnostics before allocation or serialization.
- **Behavior drift hidden by API change:** Compare semantic outcomes, refusal
  order, mutations, events, and evidence, not legacy method signatures.
- **Misleading benchmark:** Pin fixture, machine, warmup, sample count, percentile
  method, and race/stress profile so the latency claim is reproducible.

## Reviewer guidance

Review WP11 as the final capability-composition gate before production-owner
wiring. Require direct evidence that:

1. Every provider mutation is preceded by durable attributable intent in the
   same authority publication as the requesting transition.
2. Delivery attempts and observations never grant or alter mission authority.
3. Committed-but-unacknowledged publications reconcile by stable identity and
   cannot create a duplicate dispatch, retry, or compensation.
4. The projection consumes all registered WP05–WP10 event families, applies a
   complete provider revision atomically, and rebuilds byte-identically.
5. Playback has no reachable external or durable-write capability.
6. Get/List/Watch are bounded, immutable observations with revision and epoch;
   they expose no repository, condition, CommitPlan, raw key, or SDK object.
7. Watch compaction, lag, and epoch change require explicit resync and never
   become a recovery path.
8. The conformance matrix covers every frozen accepted and refused behavior,
   including exact refusal order and zero-mutation failures.
9. Race, replay, restart, corruption, ambiguity, budget, mutation, and latency
   tests exercise public consumer-owned interfaces.
10. The diff stays within WP11 ownership and introduces no generalized store,
    provider leakage, SQLite/SQL path, raw database input, or authority fallback.

Reject if any projection or delivery row becomes authoritative; if an external
effect can run before durable intent; if ambiguity enables a blind retry; if a
query can mutate or mint authority; if Watch history is required for restore;
if playback reaches adapters; or if capability parity is asserted without
checkable accepted/refused mappings and reproducible evidence.
