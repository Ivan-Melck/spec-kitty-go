---
work_package_id: WP02
title: Global Operations and Authority Journal v2
dependencies:
- WP01
requirement_refs:
- FR-002
- FR-004
- FR-005
- FR-006
- FR-017
- FR-022
- FR-025
- FR-033
- NFR-001
- NFR-002
- NFR-012
- NFR-013
- C-002
- C-004
- C-005
- C-013
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T006
- T007
- T008
- T009
- T010
- T011
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controljournal/v2/
create_intent:
- internal/controljournal/v2/model.go
- internal/controljournal/v2/codec.go
- internal/controljournal/v2/commit_plan.go
- internal/controljournal/v2/operation.go
- internal/controljournal/v2/recovery.go
- internal/controljournal/v2/recovery_test.go
- internal/controljournal/v2/publication.go
- internal/controljournal/v2/publication_test.go
- internal/controljournal/v2/projection.go
- internal/controljournal/v2/projection_test.go
- internal/controljournal/v2/journal_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/controljournal/v2/model.go
- internal/controljournal/v2/codec.go
- internal/controljournal/v2/commit_plan.go
- internal/controljournal/v2/operation.go
- internal/controljournal/v2/recovery.go
- internal/controljournal/v2/recovery_test.go
- internal/controljournal/v2/publication.go
- internal/controljournal/v2/publication_test.go
- internal/controljournal/v2/projection.go
- internal/controljournal/v2/projection_test.go
- internal/controljournal/v2/journal_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP02 - Global Operations and Authority Journal v2

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the
frontmatter, and behave according to its guidance before parsing the rest of
this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is available, run `spec-kitty agent profile list` and select the
closest implementation profile for the declared authoritative surface. Record
the resolved profile revision in the governed invocation before editing code.

## Objective

Create the provider-independent Authority Journal v2 kernel: canonical identity
and scope types, immutable host-global operation bindings, reconstructible
revisioned operation progress, deterministic publication identities, sealed
commit plans, registered event envelopes, provider-neutral recovery contracts,
durable acknowledgement coordination, acknowledged projection/prefix seals,
and pure reducer contracts.

Implementation command:

```bash
spec-kitty agent action implement WP02 --agent codex
```

## Branch strategy and file ownership

- Planning and merge target: `feat/gapdb-only-production-authority`.
- Use only the Spec Kitty lane worktree allocated for WP02.
- Treat `lanes.json` as the source of truth for the dependency-qualified base,
  lane branch, worktree, and integration target.
- Edit only the eleven files listed in `owned_files` and `create_intent`.
- Do not modify mission contracts, `wps.yaml`, GapDB adapters, module files, or
  any capability-owned package from this WP.
- You are not alone in the repository. Preserve unrelated edits and never
  revert, rewrite, or absorb another lane's work.

## Post-approval correction: foundation contracts required by WP04

This package was reopened because its first approved surface left recovery
types in the adapter and could not produce the acknowledgement-derived seal
required by checkpoints. Read `post-approval-defect-2.md` before editing. The
following correction is normative and belongs inside this WP.

This package is reopened again by `post-approval-defect-4.md`. The prior
projection seal retains its plan and record-set proof only in live memory, and
its operation summary exposes anonymous leaves rather than addressable
checkpoint boundaries. Preserve all earlier approved behavior while applying
the bounded correction below.

### Durable publication witness and shared proof helpers

Define canonical encode/decode/validation for `PublicationWitnessV2` and make
every sealed plan append exactly one immutable, non-expiring,
absent-conditioned witness record. The plan preimage binds witness schema
version 1 but excludes witness bytes. Derive PublicationID, finalize all
non-witness authority records, compute their record-set digest/count, then
construct the self-digested witness. Never include the witness in the digest
or count it reports.

The sealed plan's complete provider record projection and CommitObservation
count include the witness. Reserve one of the frozen 256 mutations: 255
non-witness records plus one witness pass; 256 plus one refuse during sealing
before a durable-port call. Expose a semantic recovery address for exact
witness reads by PublicationID; no raw key enters the foundation.

Own public pure `ProjectionPrefixGenesisV2` and
`AdvanceProjectionPrefixV2` helpers and use them inside the live tracker.
Prefix advancement binds prior digest, positive provider revision,
PublicationID, plan digest, and non-witness authority-record-set digest. Also
own a public canonical reducer-state digest helper so checkpoint/restore does
not duplicate the hash domain.

Replace anonymous operation leaves with immutable, defensively copied,
fixed-size `OperationBoundaryV2` values containing OperationID SHA-256 lookup
digest, logical version, and leaf digest. The leaf continues to bind exact
OperationID, last PublicationID, status/result, and event/effect membership.
Expose a pure `MatchesOperationID` method plus construction and validation from
bounded OperationState input. Sort by lookup digest, reject duplicate/colliding
coordinates, derive totals from boundaries, and exclude both variable-length
OperationIDs and terminal result bytes. Construct status counts from the source
states. Structural validation checks boundary shape/order/uniqueness, totals,
and status-count arithmetic; state-aware validation recomputes counts and
leaves from exact reconstructed states.

Define `MaxOperationSummaryBytesV2 = 2,112,000`. The final structured
checkpoint encoding of 10,000 maximum-version boundaries plus totals, all
status counts, field punctuation, and summary digest must fit it, leaving
2,082,304 bytes below the 4 MiB ceiling for aggregate entries and remaining
metadata. Do not embed the summary as base64 JSON bytes. Refuse plus-one or
over-budget input before unbounded allocation.

### Recovery vocabulary (`recovery.go`, `recovery_test.go`)

Define closed provider-neutral `StoreRevisionV2`, `RecordAddressV2`,
`ScanTargetV2`, `ReadRequestV2`, `ScanRequestV2`, `RecoveryRecordV2`, and
`RecoveryPageV2` values. Smart constructors admit only semantic targets for
aggregate heads, journal events, operation binding/head, checkpoint
slot/record, the complete aggregate-head scan, complete operation-record scan,
and checkpoint-slot scan. No type exposes or accepts a raw persistence key.

`StoreRevisionV2` binds store/database identity, positive current revision,
and durable-through revision. Recovery records preserve semantic address,
copied canonical bytes, positive record revision, and expiry. Pages preserve
observed revision, as-of time, copied records, opaque cursor, and truncation.
Closed errors distinguish not-found, revision-moved, stale-cursor, malformed,
unavailable/canceled, and budget-exceeded outcomes. All construction validates
budgets before allocation and every returned byte slice is defensive.

Expose complete immutable semantic accessors for `EventEnvelopeV2`,
`AggregateHeadV2`, `OperationBindingV2`, and `OperationStateV2`, including all
schema/reducer/provenance/result/head fields required by playback. Add a
foundation-owned canonical aggregate-coordinate comparator/byte identity so
restore never parses private wire JSON or recreates canonical order.

### Publication acknowledgement (`publication.go`, `publication_test.go`)

Define foundation-owned `CommitObservationV2`,
`AcknowledgedPublicationV2`, `DurablePublicationPortV2`, and
`PublicationCoordinatorV2`. The coordinator registers the sealed plan before
delivery, accepts only a positive durable observation with exact
PublicationID and mutation count or the same proof from complete
reconciliation, and validates the exact plan and sealed-record-set digests.
Zero values and field-copied forged acknowledgements fail.

No projection change may occur before acknowledgement. Unresolved ambiguity
remains registered and blocks checkpoint sealing. The coordinator permits
unrelated durable calls concurrently; it serializes only acknowledgement
application/checkpoint barriers. Capability packages receive this coordinator,
never the durable adapter. Architecture tests must reject direct or wrapped
capability-to-adapter commit reachability.

### Acknowledged projection (`projection.go`, `projection_test.go`)

Define `ProjectionTrackerV2`, compact `OperationSummaryV2`, and opaque
`SealedProjectionPrefixV2`. The tracker applies only acknowledged sealed plans,
uses the closed reducers to compare resulting state with every planned head,
and updates only changed projection entries. It maintains prior watermark,
registry/reducer/codec identities, rolling prefix, and a bounded
revision-keyed acknowledgement ledger admitting at least 10,000 publications
between seals.

At a quiescent barrier, sort acknowledgements by provider revision and fold
the prior prefix with provider revision, PublicationID, plan digest, and sealed
record-set digest through the shared pure helper. Reject duplicate/conflicting revisions, regressions,
unresolved in-flight attempts, and ledger overflow. Snapshotting may copy the
complete current projection but may not replay origin history. Operation
summary uses bounded counts/status counts and sorted fixed-size attributable
boundaries whose leaf binds operation identity/version/publication/result and
membership digests; it embeds neither exact OperationIDs nor terminal results.

Sealed projection/prefix values use private fields and internal digest
validation. “Unforgeable” means zero values and ordinary cross-package field
construction cannot pass; it does not claim protection from code inside the
owning package or Go `unsafe`.

## T006: Define canonical v2 identities, scopes, and key model

**Purpose**: Give every later authority adapter one unambiguous vocabulary and
one bounded canonical representation without importing provider semantics.

**Files**: `model.go`, `codec.go`, and `journal_test.go`.

**Implementation requirements**:

1. Define distinct value types for at least `HostAuthorityID`, `ProjectID`,
   `MissionID`, `OperationID`, `PublicationID`, `RuntimeEpochID`, `EventID`,
   `AggregateKind`, `AggregateID`, and positive logical versions.
2. Define `AggregateCoordinate` as versioned aggregate kind, typed scope, and
   opaque aggregate identity. Do not infer identity from paths, display names,
   provider revisions, PIDs, branch names, array positions, or clocks.
3. Define the closed scope-kind set `host`, `project`, `mission`, `repository`,
   `work_revision`, and `work`, with exact required/forbidden components for
   each kind. A structurally possible but semantically ambiguous scope refuses.
4. Implement a slash-safe, injective canonical key-component codec suitable
   for the normative `spk/v2/` keyspace. Reject non-canonical alternate
   spellings rather than normalizing them into aliases.
5. Represent logical event versions as positive integers and encode them as
   exactly 16 lowercase hexadecimal characters where storage-key material is
   required. Zero, overflow, uppercase, short, and overlong forms refuse.
6. Define typed bounded errors or diagnostics for invalid identity, scope,
   version, key component, canonical bytes, digest, and budget violations.
7. Keep raw keys and provider revisions out of capability-facing model types.
   Provider record revision may appear only as explicit observed metadata where
   the contract requires ordering, never as an aggregate version.
8. Establish named, reviewable limits for identity lengths, canonical payload
   sizes, list membership, event count, and plan size. Validate before copying
   or sorting attacker-controlled unbounded collections.

**Required tests**:

- Round-trip every identity and scope kind through canonical encoding.
- Prove two distinct valid components never share a canonical encoding across
  the bounded generated/property corpus.
- Reject slash, escape, Unicode-normalization, case, empty, zero-version,
  overlong, and scope-component ambiguity mutants.
- Prove map iteration and input insertion order cannot change canonical bytes.
- Prove errors are typed, bounded, deterministic, and contain no raw payload.

**Exit evidence**:

```bash
go test ./internal/controljournal/v2 -run 'Identity|Scope|Codec|Key|Budget' -count=10
```

## T007: Implement global immutable OperationBinding and collision rules

**Purpose**: Preserve the historical global idempotency law while allowing an
exact committed result to survive owner restart.

**Files**: `operation.go`, `codec.go`, and `journal_test.go`.

**Implementation requirements**:

1. Model `OperationBindingV2` with exactly the normative semantic coordinates:
   schema version, OperationID, command kind/schema, canonical intent digest,
   typed scope, optional ActorID/ProcessID, optional doctrine/profile/policy
   revisions, originating epoch provenance, and observed creation time.
2. Define stable intent canonicalization explicitly. It must include all
   caller-controlled command meaning and exclude RuntimeEpochID, provider
   revisions, retry timestamps, transport metadata, and response metadata.
3. Keep collision equality stricter than stable intent equality. Every bound
   field from the operation-binding contract participates except derived
   canonical bytes/digests that are recomputed during validation.
4. Require the binding storage coordinate to be host-global:
   `spk/v2/operations/<operation-id>/binding`. Do not prefix it with project,
   mission, repository, work revision, work item, or command family.
5. Provide a pure resolution decision for `absent`, `exact`, `conflict`, and
   `malformed-existing`. Exact returns replay/resume eligibility; conflict
   returns typed `IDEMPOTENCY_CONFLICT`; malformed existing state closes.
6. Preserve originating epoch in exact equality and audit representation while
   excluding it from stable request intent. This distinction must be visible
   in tests and API naming.
7. Do not perform epoch-currentness checks inside binding resolution. The
   caller must be able to resolve an existing exact binding/result first.
8. Validate the JSON-schema contract semantically in Go without making generic
   JSON maps the domain model.

**Collision matrix tests**:

- Same OperationID and byte-identical binding: exact replay/resume.
- Same ID, different intent digest: idempotency conflict.
- Same ID, different command kind or schema: conflict.
- Same ID, different scope kind or component: conflict.
- Same ID, different ActorID or ProcessID: conflict.
- Same ID, different doctrine/profile/policy revision: conflict.
- Same ID, different originating epoch provenance: conflict.
- Same semantic request retried after epoch rotation: stable intent digest is
  unchanged and the existing binding is resolved before epoch validation.
- Same OperationID in two missions, projects, or command families: conflict,
  proving host-global uniqueness.
- Malformed or unknown-version persisted binding: closed typed corruption, not
  absence and not a newly claimable ID.

**Exit evidence**:

```bash
go test ./internal/controljournal/v2 -run 'OperationBinding|Collision|Intent|Global' -count=20
```

## T008: Implement revisioned OperationState and staged transitions

**Purpose**: Represent real multi-stage commands without weakening exactly-once
identity or rewriting immutable terminal evidence.

**Files**: `model.go`, `operation.go`, and `journal_test.go`.

**Implementation requirements**:

1. Model states `accepted`, `in_progress`, `resolved`, `refused`, `ambiguous`,
   and `reconciliation_required` with positive logical versions.
2. Encode the allowed transition graph explicitly. Do not accept arbitrary
   enum changes or use ordinal comparisons as transition policy.
3. Require every transition to advance the operation logical version exactly
   by one and cite the prior version it expects.
4. Store canonical typed result bytes, result kind/schema, and SHA-256 where a
   result exists. Recompute and compare the digest during construction/read.
5. Make terminal result bytes immutable. Repeated resolution returns the exact
   original bytes and may not append different events/effects.
6. Keep event/effect membership cumulative, canonical, duplicate-free, and
   bounded. A later stage cannot silently discard earlier membership.
7. Allow a typed semantic refusal to be the first meaningful bound and
   terminal state so its retry is deterministic.
8. Model ambiguity honestly. `ambiguous` does not mean failed, retryable, or
   absent; it may transition only through the declared reconciliation path.
9. Expose pure transition validation and construction. Do not add persistence,
   clock, provider, side-effect, or current-epoch calls.
10. Define canonical operation-event payload encoding from OperationState with
    `LastPublicationID` excluded, and reconstruction from the completed event
    envelope. The sealer must reject any operation event whose payload differs
    from its declared state transition. Use the closed operation event schema
    2/reducer 2 tuple; tuple `1/1` is incompatible.

**Required tests**:

- Table-test every allowed and forbidden edge in the transition graph.
- Reject zero, skipped, repeated, and regressed logical versions.
- Reject result mutation after `resolved` or `refused`.
- Reject event/effect loss, duplication, reordering aliases, and over-budget
  growth while accepting canonical monotonic membership.
- Prove typed semantic refusal replays exactly.
- Prove ambiguous state cannot be treated as proven absence or automatic retry.
- Round-trip every operation stage through its immutable event payload and
  reconstruct the exact result and cumulative event/effect membership.
- Reject payload/state substitution, missing membership, wrong envelope
  PublicationID, and the obsolete operation tuple.

**Exit evidence**:

```bash
go test ./internal/controljournal/v2 -run 'OperationState|Transition|Terminal|Ambiguous' -count=20
```

## T009: Derive PublicationID and validate sealed CommitPlan

**Purpose**: Make an accepted operation stage a complete immutable publication
unit that WP03 can translate into exactly one conditional GapDB batch.

**Files**: `commit_plan.go`, `codec.go`, and `journal_test.go`.

**Implementation requirements**:

1. Represent a builder or unsealed input separately from the sealed plan type.
   The persistence-facing method must accept only the sealed type.
2. Canonicalize and validate the complete plan before deriving identity.
3. Derive `PublicationID` from the domain separator
   `spec-kitty.publication.v2`, OperationID, next operation logical version,
   and canonical sealed-plan digest. Avoid recursive hashing by excluding the
   derived PublicationID field from its own digest preimage.
4. Require exactly one OperationState transition and at least one meaningful
   capability/operation event per accepted publication.
5. Allow the absent immutable OperationBinding only on the first publication;
   later stages reference but never rewrite it.
6. Validate exact event/head membership, event ordering, expected logical
   versions, effect-intent membership, eligibility guards, optional checkpoint
   records, provider conditions, canonical digests, and all budgets.
7. Require every affected aggregate head to have exactly one matching event
   and every event to have exactly one matching head transition.
8. Reject duplicate aggregate coordinates, keys, EventIDs, EffectIDs,
   conditions, membership entries, and semantically equivalent aliases.
9. Deep-copy caller-owned mutable bytes/slices/maps during sealing or use
   immutable value representations. Mutation after sealing must not change
   canonical bytes, digest, PublicationID, membership, or conditions.
10. Expose a deterministic canonical serialization for adapters and evidence.
    It must not expose writable internals or depend on Go map iteration.
11. Do not translate to provider mutations in this package. GapDB-specific
    revision/condition encoding belongs to WP03.
12. Expose a closed immutable record projection derived only during sealing.
    It must cover binding, events, the canonical OperationState head, canonical
    capability heads, full effect-intent values, full eligibility-shard values,
    checkpoint records, checkpoint-slot transitions, and exactly one immutable
    publication witness. Projected members carry typed semantic targets,
    canonical bytes, value class, and explicit absent-or-positive-revision
    conditions; they expose no raw-key constructor, delete, expiry,
    condition-any, or post-seal mutation path.
13. Extend head-transition input with committed runtime epoch and bounded
    canonical authority summary, then derive final head bytes only after
    PublicationID exists. Operation coordinates project canonical
    OperationState at the host-global operation-head target; other coordinates
    project canonical AggregateHeadV2 values.
14. Replace digest-only effect and guard placeholders with persistence-complete
    typed inputs. Effect intents name a host-global EffectID and carry their
    full canonical intent. Eligibility transitions name mission and shard,
    carry the full canonical shard value, and declare its exact create/update
    condition. Checkpoint transitions pair a canonical immutable record with a
    canonical slot value and exact slot condition.
15. Validate an exact bijection between mutable projected members and declared
    conditions. Bind every target and canonical byte into the plan preimage;
    reject missing, extra, duplicate, aliased, substituted, zero-revision, and
    post-seal-mutated members before adapter access.
16. Bind witness schema version 1 into the plan preimage. After PublicationID
    and all non-witness records exist, hash that record set and construct a
    self-digested witness carrying the publication/operation coordinates, plan
    digest, and non-witness digest/count. Append it with `RequireAbsent`; never
    include it in the digest/count it reports.
17. Reserve one provider mutation for the witness. Exactly 255 non-witness
    records plus the witness fit the 256-member ceiling; 256 plus the witness
    refuse before the durable port.
18. Expose the plan and sealed record-set digests needed to validate a durable
    CommitObservation without parsing canonical bytes. Only an
    AcknowledgedPublication produced from the exact plan may enter the
    projection tracker.

**Required tests**:

- Identical semantic plans in different input orders seal to identical bytes,
  plan digest, and PublicationID.
- Any semantic change changes the digest and PublicationID.
- Caller mutation after seal leaves the plan unchanged.
- Missing/extra/duplicate event-head membership refuses.
- First/later binding misuse refuses.
- Split-publication and two-events-for-one-aggregate mutants are killed.
- Every boundary maximum passes and each maximum-plus-one refuses before an
  adapter call or excessive allocation.
- A consumer test can enumerate the sealed record projection and obtain every
  target/value/condition required for one AtomicBatch without parsing
  `CanonicalBytes`, rebuilding a head, or consulting capability state.
- Omission/substitution mutants for effect value, eligibility mission/shard/
  value/revision, head epoch/summary/value, checkpoint record/slot/value/
  revision, and projected role all fail closed.
- Witness omission, duplication, field substitution, mutable/expiring
  condition, self-inclusion in its record-set digest, wrong provider count,
  and exact 255/256 non-witness budget boundaries fail or pass as specified.

**Exit evidence**:

```bash
go test ./internal/controljournal/v2 -run 'Publication|CommitPlan|Seal|Membership' -count=20
```

## T010: Implement event registry and pure reducer contracts

**Purpose**: Establish a closed semantic event universe and mechanically pure
playback seam before capability implementations add their event payloads.

**Files**: `model.go`, `codec.go`, and `journal_test.go`.

**Implementation requirements**:

1. Model the normative `EventEnvelopeV2` fields, including event/envelope/
   reducer versions, aggregate coordinate/version, operation coordinates,
   PublicationID, committed epoch provenance, event index/count, optional
   governance/work coordinates, observations, typed payload, and event digest.
2. Enforce one transition event per aggregate coordinate in one publication.
   Capability packages must combine multiple facts about one aggregate into one
   semantic transition rather than emit several same-version events.
3. Require a positive next aggregate logical version, immutable EventID,
   consistent operation version, one PublicationID, consecutive event indices,
   and identical event count across the publication.
4. Define a closed registry API keyed by family, event schema, and reducer
   version. Duplicate registrations and unknown tuples refuse deterministically.
5. Seed or validate the exact family ownership declared by
   `event-family-registry-v2.json` without importing those owner packages and
   without creating dependency cycles.
6. Define canonical immutable registry descriptors/digests. Event compatibility
   includes registry ID/version, publication rule, exact ordering tuple, and
   sorted family/schema/reducer/owner tuples. Reducer compatibility requires a
   complete one-to-one registration and additionally binds an explicit owner
   and declared implementation digest; never derive identity from a Go function
   pointer.
7. Define reducer contracts as pure functions over prior state bytes/value plus
   validated event data. The interface must offer no context containing
   repositories, clocks, network clients, filesystems, agents, or effect ports.
8. Runtime `Apply` is stateless and retains no replay-wide observation cache.
   Determinism qualification is a separate bounded repeated-call helper. More
   than 10,000 distinct replay inputs must not hit the former 256-observation
   ceiling.
9. Require deterministic output and digest validation. Same prior state plus
   same event must produce byte-identical output across repeated qualification
   runs.
10. Treat recorded external observations as inert payload data. Reducers may
   interpret their declared schema but cannot fetch, refresh, compensate, or
   persist anything.
11. Fail unknown envelope, event schema, reducer, ordering, implementation, or
    digest versions closed before changing a projection.
12. Keep actual capability reducers out of this WP; later owner packages
    register implementations through the inward-owned contract.

**Required tests**:

- Registry accepts every declared family/version tuple exactly once.
- Duplicate, missing, unknown, and ownership-mismatched registry mutants fail.
- Mutating registry ID/version, publication rule, ordering, owner, event
  schema, reducer version, or reducer implementation digest changes the
  compatibility digest and refuses an incompatible checkpoint.
- Event index gaps, duplicates, count mismatches, cross-publication mixing, and
  multiple events for one aggregate fail.
- A deliberately nondeterministic or digest-mismatched reducer is detected.
- A compile-time/API-shape test proves reducer calls cannot receive an external
  or persistence adapter through the journal contract.
- Repeated reduction of canonical fixtures is byte-identical.
- At least 10,000 distinct stateless applications succeed without retained
  observation growth; an alternating reducer fails the separate qualification.

**Exit evidence**:

```bash
go test ./internal/controljournal/v2 -run 'Event|Registry|Reducer|Playback|Digest' -count=20
```

## T011: Prove journal invariants with property, fault, and mutant tests

**Purpose**: Attack the shared semantics before every later capability relies
on them, including the critical retry-after-restart ordering rule.

**Files**: all four focused test files, with production corrections limited to
the seven non-test WP02-owned files.

**Required campaigns**:

1. Generate bounded valid identities, scopes, bindings, states, events, heads,
   and plans; round-trip canonical bytes and verify stable SHA-256 digests.
2. Generate one-field binding changes and prove every collision-matrix field
   refuses under one reused host-global OperationID.
3. Generate legal staged operations and prove monotonic state, immutable
   terminal result, cumulative membership, and deterministic PublicationIDs.
4. Generate invalid event/head memberships and prove sealing rejects them.
5. Generate unknown schema/reducer/ordering versions and prove fail-closed
   results rather than fallback decoding.
6. Exercise maximum budgets and maximum-plus-one inputs without panic,
   unbounded allocation, truncated equality, or accidental acceptance.
7. Run all pure decisions concurrently under the race detector; shared
   registries must be immutable after construction or synchronize correctly.
8. Exercise provider-neutral recovery addresses, records, pages, revision
   coordinates, semantic accessors, canonical comparators, and closed error
   kinds without importing the GapDB adapter.
9. Exercise durable acknowledgement and projection sequencing: wrong plan,
   publication, record set, mutation count, revision, or durability cannot
   advance state; ambiguity and zero/forged seals cannot advance or snapshot.
10. Complete disjoint durable calls in reverse response order and prove the
    same provider-revision-ordered rolling prefix. A checkpoint barrier waits
    for or refuses unresolved registered calls without serializing delivery.
11. Seal at least 10,000 acknowledged publications within the ledger budget
    and reject the first over-budget observation before unbounded allocation.
12. Round-trip and corrupt every publication-witness field; prove one shared
    prefix helper yields byte-identical live and restored folds.
13. Build, reorder, duplicate, omit, collide, and regress OperationBoundary
    values; prove exact scanned OperationIDs match through their digest,
    reconstructed boundary events reproduce the leaf, and the canonical
    summary is attributable and defensively copied.
14. Prove the actual final structured checkpoint encoder fits 10,000
    maximum-version boundaries plus complete summary overhead within exactly
    2,112,000 bytes, and the first over-budget boundary refuses before
    unbounded allocation.

**Mandatory replay-before-epoch validation**:

Build a focused pure decision harness with the following observable order:

1. canonicalize and validate the retry request;
2. look up and compare the host-global OperationBinding;
3. return the exact recorded terminal result for an exact binding;
4. only when the binding is absent, evaluate current RuntimeEpochID and new
   capability preconditions.

The harness must prove that an exact operation committed under epoch 17 returns
its byte-identical result after epoch 18 is current. It must also prove that an
absent new request carrying epoch-17 authority refuses before capability reads
or effects. A mutant that moves epoch validation before existing-operation
resolution must fail deterministically.

**Required fault and mutant controls**:

- Runtime epoch included in stable intent digest.
- Operation key scoped below mission or project.
- Collision equality omits one binding field.
- Binding can be overwritten after the first stage.
- Terminal result bytes can change.
- Operation logical version can skip or repeat.
- PublicationID uses a random value, timestamp, or retry counter.
- Sealed plan aliases caller-owned bytes.
- Plan omits one head, event, effect intent, or condition.
- Two events target one aggregate in one publication.
- Event indices are non-consecutive or disagree on total count.
- Unknown reducer/version silently selects latest.
- Registry digest omits ID, publication rule, ordering, owner, schema, reducer,
  or implementation identity.
- Stateful reducer observation caching reintroduces the 256-input ceiling.
- Reducer receives an adapter-capable dependency.
- Projection advances before durable acknowledgement or after ambiguity.
- Acknowledgement accepts a wrong plan, record set, publication, mutation
  count, zero revision, or non-durable observation.
- Rolling prefix follows response completion rather than provider revision.
- Live and restore paths implement different prefix hash logic.
- Publication witness is missing, duplicated, mutable, expiring, substituted,
  self-recursive, omitted from observation count, or not revision-coincident.
- Operation summary contains anonymous, reordered, duplicate, missing,
  colliding, unmatched, regressed, variable-ID, or over-budget boundaries.
- Checkpoint seal ignores an unresolved registered publication.
- Operation event payload differs from the declared OperationState transition.
- Semantic refusal creates a naked reservation before its result.
- Existing-operation resolution occurs after current-epoch validation.

Each mutant test must demonstrate that the unmutated fixture passes and the
controlled mutation fails for the intended typed reason. Assertions on source
text alone do not count as a killed semantic mutant.

**Validation commands**:

```bash
go test ./internal/controljournal/v2 -count=20
go test -race ./internal/controljournal/v2 -count=5
go vet ./internal/controljournal/v2
staticcheck ./internal/controljournal/v2
go test ./internal/architecture -count=1
go test ./... -run '^$' -count=1
gofmt -d internal/controljournal/v2
git diff --check
```

Do not run the full M2 crown campaign from this lane. WP02 supplies deterministic
hooks and exact tests that later capability and qualification packages include
in their census.

## Definition of Done

- All eleven declared files exist and no undeclared file was modified.
- The package is provider-neutral and passes WP01 architecture guards.
- Canonical identity/scope/key encodings are bounded, injective, and stable.
- OperationID binding is host-global, immutable, and collision-complete.
- Stable intent excludes runtime epoch while binding retains epoch provenance.
- Existing committed results resolve before current-epoch admission.
- OperationState supports only the declared monotonic staged transitions.
- PublicationID and sealed CommitPlan are deterministic and immutable.
- Event/head membership enforces one event per aggregate per publication.
- Operation events reconstruct exact historical OperationState and membership.
- Foundation-owned recovery contracts expose no raw/provider key type.
- Registry/reducer descriptors and implementation digests fail unknown or
  incomplete versions closed; runtime Apply has no 256-input session limit.
- Projection and rolling-prefix state advance only from an exact durable
  acknowledgement; the bounded ledger supports at least 10,000 publications.
- Zero/forged acknowledgement and projection seals fail validation.
- Reducer APIs cannot perform persistence or external effects.
- Unit, property, fault, race, and controlled-mutant tests pass.
- All six subtasks are recorded `done` in the canonical event log.
- The WP is committed and handed to an independent reviewer; implementation
  does not mark itself approved or merge itself outside Spec Kitty.

After every subtask and all validations are genuinely complete, record status:

```bash
spec-kitty agent tasks mark-status T006 T007 T008 T009 T010 T011 \
  --status done \
  --mission gapdb-only-production-authority-01M1GWDJ \
  --json
```

## Risks and mitigations

| Risk | Required mitigation |
|---|---|
| Mission-scoped operation keys recreate a silent collision hole | Assert one host-global coordinate and cross-mission collision mutants. |
| Epoch provenance contaminates stable caller intent | Separate intent equality, binding equality, and epoch admission in names and tests. |
| A one-record receipt cannot describe sagas | Use immutable binding plus revisioned monotonic OperationState. |
| Plan digest recursively includes derived PublicationID | Specify and test the preimage independently from derived fields. |
| Sealing is cosmetic because slices/maps alias callers | Deep-copy or use immutable values; mutate all caller inputs after seal in tests. |
| Generic events hide capability laws | Require registered family/schema/reducer tuples and typed payload ownership. |
| Multiple same-aggregate events create ambiguous ordering | Enforce exactly one transition event per coordinate per publication. |
| Provider concepts leak into the kernel | Run WP01 dependency guards and reject raw provider key/revision APIs. |
| Replay can accidentally perform I/O | Keep reducer signatures capability-free and add compile-time/API mutant probes. |
| Budget checks occur after allocation/sort | Validate declared counts and byte bounds before expensive canonicalization. |

## Reviewer guidance

Review this WP as a semantic foundation, not merely a set of serializable Go
structs. Trace each public constructor to a required invariant and reject dead
or convenience APIs without a production consumer planned by WP03 or WP04.

The independent reviewer must explicitly verify:

1. the exact requirement and contract round-trip for all six subtasks;
2. global rather than mission-scoped OperationID behavior;
3. distinction between stable intent equality and full binding collision
   equality, especially RuntimeEpochID;
4. replay of a committed epoch-17 result after epoch 18 becomes current;
5. refusal of an absent epoch-17 request before any capability read/effect;
6. deterministic PublicationID preimage without recursive/self-reference;
7. sealed-plan immutability after adversarial caller mutation;
8. exact operation/event/head/effect membership and budgets;
9. one event per aggregate and consecutive publication ordering;
10. fail-closed unknown versions and capability-free reducers;
11. complete provider-neutral recovery types and semantic accessors without a
    raw-key or adapter dependency;
12. operation event payload/state equality and historical reconstruction;
13. canonical event/reducer descriptors whose digests cover every compatibility
    coordinate and declared implementation identity;
14. stateless runtime Apply beyond 10,000 distinct inputs;
15. acknowledgement-derived projection/prefix behavior, out-of-order durable
    response handling, ambiguity blocking, ledger budgets, and forged seals;
16. real mutant controls that fail semantically, not grep-only assertions;
17. no provider, SQLite/SQL, Beads, Git, filesystem, transport, or clock edge.

Reject the WP if passing tests depend on map iteration order, wall-clock values,
random identifiers, current runtime epoch in stable intent, storage provider
behavior, test-only alternate implementations, direct capability-to-adapter
reachability, optimistic projection, or implicit fallback decoding.
