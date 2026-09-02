# M2 GapDB Authority Data Model

Status: normative planning model for work-package decomposition. Persisted
schemas remain versioned contracts; Go struct layout is not part of the wire or
storage contract.

## Authority boundary

The model assigns three different kinds of ownership:

- Spec Kitty capability aggregates own product meaning, legality, identities,
  and results.
- GapDB owns record durability, conditional mutation, atomic batch revision,
  bounded reads, byte recovery, and sidecar lifecycle.
- the control host owns the disposable in-memory projection and command
  admission barrier.

Git, trackers, filesystems, processes, agents, and humans own observed external
facts. Their observations become bounded immutable event data; they never grant
Spec Kitty authority by themselves.

## Canonical identities

| Identity | Scope and law |
|---|---|
| `HostAuthorityID` | One local control authority; bounds global OperationID uniqueness. |
| `ProjectID` | Stable project namespace. |
| `MissionID` | Stable mission identity, independent of display name or directory. |
| `OperationID` | Caller-stable command/saga identity, unique in one host authority. |
| `PublicationID` | Deterministic identity of one durable OperationState transition. |
| `RuntimeEpochID` | One admitted owner lifetime; never reused after restart. |
| `AggregateCoordinate` | Versioned kind, typed scope, and opaque aggregate ID. |
| `EventID` | Immutable semantic event identity. |
| `CheckpointID` | Immutable product projection snapshot identity. |
| `WorkRevisionID` | Immutable executable-graph generation. |
| `WorkID` | Work identity inside a generation. |
| `ActorID` | Durable accountable actor; reviewer independence uses this identity. |
| `ProcessID` | One execution of an actor; never substitutes for ActorID. |
| `RunID` | One authorized work attempt. |
| `LeaseID` | Renewable grant for one run and workspace. |
| `WorkspaceID` | Mutable directory identity permanently bound to one lease. |
| `CandidateID` | Immutable handoff evidence identity. |
| `ReviewID` | One review attempt and terminal verdict. |
| `IntegrationID` | One serialized repository publication attempt. |
| `EffectID` | Stable external-effect intent and attribution identity. |

No identity is inferred from a branch, PID, path, ticket assignment, textual
label, clock, provider revision, or array position.

## Version 2 keyspace

All current production records use a closed `spk/v2/` keyspace. Components use
one canonical encoding, reject ambiguous escapes, and satisfy M1 product
budgets.

```text
spk/v2/authority/binding
spk/v2/runtime/current
spk/v2/runtime/epochs/<runtime-epoch-id>

spk/v2/operations/<operation-id>/binding
spk/v2/operations/<operation-id>/head

spk/v2/scopes/<scope>/heads/<aggregate-kind>/<aggregate-id>
spk/v2/scopes/<scope>/journal/<aggregate-kind>/<aggregate-id>/<version-hex16>

spk/v2/checkpoints/slots/<slot-id>
spk/v2/checkpoints/records/<checkpoint-id>
spk/v2/workgraphs/<mission-id>/eligibility/<shard-id>
```

`<scope>` is a canonical tagged coordinate such as host, project, mission,
repository, or work revision. Operation keys are deliberately outside mission
scope. Logical versions are fixed-width 16-character lowercase hexadecimal so
lexical and numeric order agree, although restore normally uses exact Gets.

Physical deletion of an aggregate head or event is forbidden in M2. Product
deletion is a tombstone transition. Historical retention/archival is a later
policy because deleting a live event without its checkpoint dependency would
destroy resumability.

## Common records

### Authority binding

```text
AuthorityBindingV2 {
  schema_version
  host_authority_id
  database_id
  admitted_tuple_sha256
  sidecar_sha256
  wire_protocol
  storage_protocol
  created_operation_id
}
```

This record prevents a healthy but wrong database from serving the project.
The exact M1 adoption record is verified before it can be created or accepted.

### Operation binding

```text
OperationBindingV2 {
  schema_version
  operation_id
  command_kind
  command_schema_version
  canonical_intent_sha256
  scope { kind, project_id?, mission_id?, repository_id?,
          work_revision_id?, work_id? }
  actor_id?
  process_id?
  doctrine_revision?
  profile_revision?
  policy_revision?
  originating_runtime_epoch_id
  observed_created_at
}
```

The binding is immutable and inserted with `RequireAbsent` in the first
meaningful publication. `canonical_intent_sha256` excludes the ephemeral
RuntimeEpochID, provider revisions, and retry timestamps. Those are evaluated
preconditions or provenance, not caller intent. Every other bound field is part
of collision equality.

Malformed or unauthorized transport requests fail before binding. A semantic
domain refusal may bind and resolve so retry returns the exact typed result.

### Operation state

```text
OperationStateV2 {
  schema_version
  operation_id
  logical_version
  status: accepted | in_progress | resolved | refused |
          ambiguous | reconciliation_required
  result_kind?
  result_schema_version?
  canonical_result?
  result_sha256?
  event_ids[]
  effect_ids[]
  last_publication_id
}
```

OperationState is an aggregate, not an overwrite-only receipt. Terminal result
bytes are immutable. Event/effect membership is cumulative and bounded. A
stage transition is published in the same batch as its capability effects.

### Publication identity

```text
PublicationID = SHA256(
  "spec-kitty.publication.v2" ||
  OperationID || nextOperationLogicalVersion || sealedCommitPlanDigest
)
```

The GapDB request uses PublicationID for response-ambiguity reconciliation.
Retrying never manufactures a new OperationID or publication identity.

### Event envelope

```text
EventEnvelopeV2 {
  envelope_version
  event_id
  event_type
  event_schema_version
  reducer_version
  scope
  aggregate_kind
  aggregate_id
  aggregate_logical_version
  operation_id
  operation_logical_version
  publication_id
  committed_runtime_epoch_id
  event_index
  event_count
  actor_id?
  process_id?
  work_revision_id?
  policy_revision?
  governance_revisions?
  observations?
  payload
  canonical_event_sha256
}
```

One publication emits at most one event per aggregate coordinate. That event's
logical version is exactly current head plus one. A multi-aggregate publication
uses consecutive zero-based event indices and one positive total event count.
Every record in the group receives the same GapDB commit revision. Unknown
event/envelope/reducer tuples fail restore.

### Aggregate head

```text
AggregateHeadV2 {
  schema_version
  coordinate
  logical_version
  last_event_id
  last_publication_id
  committed_runtime_epoch_id
  canonical_state_sha256
  authority_summary
  tombstone
}
```

An update requires both the exact observed GapDB record revision and expected
logical version. A create requires absence. `authority_summary` contains only
the minimal current coordinates needed for command decisions; it is not a
generic state blob.

### Sealed commit plan

```text
CommitPlanV2 {
  publication_id
  operation_binding_create?
  operation_transition
  capability_events[1..N]
  head_transitions[1..N]
  effect_intents[]
  eligibility_guard_transitions[]
  checkpoint_records[]
  provider_conditions[]
  canonical_plan_sha256
}
```

Construction validates exact event/head membership, one event per aggregate,
operation membership, expected revisions, budgets, event registry, and
canonical digests. Only a sealed plan can reach the GapDB writer. The committer
translates it to one `AtomicBatch` and understands no capability semantics.

## Capability aggregates

### Runtime host

States: `recovering`, `reconciling`, `ready`, `degraded`, `closed`.

Events include epoch admitted, readiness changed, predecessor invalidated, and
recovery refused. Startup restores and verifies first, then durably admits a
new epoch before opening the command barrier. Existing committed operation
results may be read under a new epoch; new mutation with predecessor authority
is refused.

### Mission and artifact authority

The mission aggregate owns lifecycle, action cursor, blockers, decisions, and
terminal outcome. One action publication may also update artifact or workgraph
aggregates in the same batch, but the common journal does not interpret why.

Artifact catalog and artifact revision are distinct aggregates. Publication
CASes the catalog's current revision; verification or approval addresses an
exact immutable artifact revision so later publication does not rewrite its
verdict.

### Work graph and eligibility

The workgraph aggregate owns immutable WorkRevision generations and lineage:

```text
continue   old generation remains executable under recorded rationale
supersede  successor becomes current; old running work may finish only as policy permits
invalidate old generation immediately loses later-gate eligibility
fork       both named generations remain explicit branches with separate scope
```

Finalization pins work IDs, descriptions, dependencies, acceptance criteria,
artifact/provider observations, and content digests. Reconciliation changes
generation authority; it does not mutate the frozen generation.

A fixed number of eligibility guard shards is derived from mission identity and
WorkID. Reconciliation updates only affected shard facts in bounded batches;
gated work commands CAS their shard with their own aggregates. Guard revisions
are concurrency metadata and do not fabricate domain events when semantic bytes
remain unchanged.

### Execution and workspace

The execution-slot coordinate is `(WorkRevisionID, WorkID)`. It owns run,
lease generation, fence, actor/process authority, heartbeat, expiry, and
outcome. Exactly one current lease exists per slot.

Workspace owns immutable repository, lease, epoch, and path identities plus a
monotonic preparation/quarantine lifecycle. Expiry journals both lease loss and
workspace quarantine. Replacement always creates a new WorkspaceID/path. A
zombie may still write its old POSIX directory, but no current authority gate
accepts that workspace's evidence.

### Handoff, review, acceptance, and integration

- handoff binds a candidate to repository observations, current workgraph,
  lease/workspace authority, and complete contributor ActorIDs;
- review binds criterion evidence and one terminal verdict to a candidate;
  reviewer ActorID must differ from every contributor unless explicit policy
  records an exception;
- acceptance cites exact current candidate, reviews, artifacts, workgraph,
  policy, and visible waivers; stale evidence refuses before repository fencing;
- integration-target owns the repository/target fence while
  integration-attempt owns staged Git observations, conflicts, repair
  candidates, and completion.

No `legacy-v1` eligibility exists. Missing complete eligibility is a typed
refusal.

### External effects and outbox

```text
intent_recorded -> attempt_started -> observation_recorded
                                  -> ambiguous
ambiguous -> reconciliation_required -> retry_allowed | compensation_allowed |
                                        refused | resolved
```

Intent contains expected provider revision/value/marker and intended mutation.
Compensation is legal only when a later observation still matches all three
attribution coordinates. Provider-generated child identities are EffectIDs or
provider idempotency markers unless explicitly registered as new top-level
operations.

Outbox delivery is an external effect. Delivery state is evidence; it does not
become mission authority.

## Checkpoint model

```text
CheckpointV2 {
  schema_version
  checkpoint_id
  through_record_revision
  event_registry_sha256
  reducer_registry_sha256
  canonical_codec_version
  rolling_prefix_sha256
  aggregates[] {
    coordinate
    logical_version
    last_event_id
    canonical_state
    canonical_state_sha256
    tombstone
  }
  operation_summary
  canonical_projection_sha256
  created_publication_id
  checkpoint_sha256
}
```

The checkpoint is built from a sealed projection/prefix capability maintained
only after acknowledged publication. It never replays from origin during
creation. It covers one complete provider revision and every authoritative
aggregate known at that revision.

A bounded retained ring of checkpoint candidate slots points to immutable
records. Selection sorts by watermark, validates every candidate independently,
falls back from a corrupt latest candidate, and refuses contradictory valid
candidates at the same watermark.

## Restore algorithm and invariants

With command admission closed:

1. capture store revision `R`;
2. select the newest compatible checkpoint or origin;
3. scan every aggregate head with a cursor pinned to `R`;
4. restart the entire attempt on stale cursor or observed revision mismatch;
5. compare the head census with checkpoint aggregate versions;
6. exact-Get every logical version in `(checkpointVersion,currentVersion]`
   using a bounded worker pool;
7. validate key/envelope coordinates, no expiry, known versions, revision in
   `(checkpointWatermark,R]`, and continuous aggregate versions;
8. group by OperationID and require exact OperationState event membership,
   common record revision, and complete event indices;
9. sort by `(Record.Revision, EventIndex, EventID)` and reduce purely;
10. match every terminal head and whole-product digest;
11. re-read store revision and restart if it is no longer `R`; and
12. admit a new RuntimeEpoch only from the sealed restore result.

Checkpoint aggregate absent from current heads, regressed head versions,
physical deletion, duplicate versions, partial operation groups, unknown
schema/reducer/order, over-budget suffix, or unstable traversal all fail closed.
Full reconstruction uses the same algorithm with checkpoint version zero and
is allowed only within explicit record/byte/time budgets.

## Historical-state disposition

```text
HistoricalStateDispositionV1 {
  schema_version
  control_root_sha256
  classification: empty_supported | current_supported | historical |
                  mixed | unsafe | malformed | ambiguous
  findings[] { kind, relative_path?, observed_sha256?, detail_code }
  current_mutation_count
  action: initialize | recover | offline_disposition_required
  diagnosis_code
}
```

The classifier reads bounded paths and file prefixes without loading a SQL
driver. Any classification other than empty/current supported records zero
current mutations and refuses before sidecar startup.

## Projection model

The in-memory projection contains immutable snapshots or owner-serialized maps
for mission, work, operation, effect, review, integration, and progress views.
It is published only after acknowledged/reconciled durable commit and is always
discardable. Queries and watches can observe revision, epoch, WorkRevision,
blockers, current runs/leases, operation progress, and legal next actions; they
cannot construct commit plans or mint authority.

## Global invariants

1. One writable host owner exists for each authority database.
2. Every accepted stage corresponds to one complete provider revision.
3. OperationID has exactly one immutable meaning across all capabilities.
4. Each affected aggregate advances exactly once per publication.
5. Event versions are continuous and immutable; heads are never physically deleted.
6. Stable result replay occurs before current-epoch validation; new mutation does not.
7. A replacement lease never inherits a mutable workspace.
8. Reviewer independence is ActorID-based.
9. External compensation is compare-before-compensate.
10. Playback has no adapter or durable-write capability.
11. Tracker and Git observations never grant Spec Kitty authority.
12. Historical SQLite state is diagnosed but never read as production authority.
