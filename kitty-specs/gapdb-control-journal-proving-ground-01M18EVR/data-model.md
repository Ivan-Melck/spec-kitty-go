# M1 control-journal data model

Status: proposed proving-ground model derived from research. Names and encodings
are planning inputs, not shipped API commitments.

## Model boundary

The model separates three responsibilities:

- GapDB owns bounded records, conditional mutation, one-revision atomic
  batches, durable acknowledgement, byte recovery, prefix scans, and watches.
- Spec Kitty owns identifiers, event and reducer meaning, idempotency,
  authority, checkpoints, playback, effect attribution, and readiness.
- Git and external providers own observed effects; their observations are
  recorded as events but are never reconstructed by reducer side effects.

The in-memory projection is the fast active view. It is disposable. The
durably accepted journal batch is the authority from which the view is rebuilt.

## Canonical identities

All identifiers are opaque, immutable, bounded strings encoded canonically.

| Identity | Purpose |
| --- | --- |
| `ProjectID` | Namespace for one proving-ground project. |
| `MissionID` | Stable mission identity. |
| `WorkRevisionID` | Generation of the finalized executable graph. |
| `WorkID` | Work item inside a work revision. |
| `ActorID` | Durable accountable actor identity. |
| `ProcessID` | One execution of an actor. |
| `RuntimeEpochID` | One admitted host lifetime; always new after restart. |
| `OperationID` | Caller-stable idempotency identity. |
| `RunID` | One authorized attempt to execute work. |
| `LeaseID` | Renewable authority grant for a run. |
| `WorkspaceID` | Mutable workspace permanently bound to one lease/epoch. |
| `CandidateID` | Immutable handoff candidate produced by a workspace. |
| `ReviewID` | Independent review attempt. |
| `IntegrationID` | Serialized target-ref integration attempt. |
| `EventID` | Preallocated immutable journal-record identity. |
| `CheckpointID` | Projection snapshot tied to a journal prefix. |
| `EffectID` | Stable external-effect intent identity. |

No identifier is inferred from a display name, PID, branch name, filesystem
path, tracker assignment, or GapDB revision.

## Keyspace

Keys are ASCII, versioned, and scoped by canonical ProjectID and MissionID.
Every M1 product key begins with exactly
`spk/v1/<project-id>/<mission-id>/`. Mission scope prevents collision and
bounds this proving slice; no leading-slash, `/v1/projects`, project-only, or
compatibility alias is valid. The codec rejects ambiguous escaping and enforces
a product key budget below GapDB's 4-KiB default.

```text
spk/v1/<project-id>/<mission-id>/binding
spk/v1/<project-id>/<mission-id>/runtime/epochs/<runtime-epoch-id>
spk/v1/<project-id>/<mission-id>/runtime/current
spk/v1/<project-id>/<mission-id>/operations/<operation-id>
spk/v1/<project-id>/<mission-id>/journal/<event-id>
spk/v1/<project-id>/<mission-id>/heads/<aggregate-kind>/<aggregate-id>
spk/v1/<project-id>/<mission-id>/checkpoint-head
spk/v1/<project-id>/<mission-id>/checkpoints/<checkpoint-id>
spk/v1/<project-id>/<mission-id>/effects/<effect-id>/intent
spk/v1/<project-id>/<mission-id>/effects/<effect-id>/attempts/<attempt-id>
spk/v1/<project-id>/<mission-id>/effects/<effect-id>/observations/<observation-id>
```

Journal keys contain preallocated `EventID`s, not commit revisions. GapDB
allocates the batch revision only after the request has been formed; trying to
place it in a value or key would require a second write and violate atomicity.
Each scanned journal record carries the authoritative `Record.Revision` supplied
by GapDB. Replay order is the total tuple:

```text
(gapdb_record_revision, event_index, event_id)
```

All events emitted by one operation have the same record revision, consecutive
zero-based event indices, the same positive event count, and distinct IDs.
Missing, duplicate, or out-of-range indices close recovery.

## Canonical records

All persisted values use a deterministic, versioned binary or canonical JSON
codec. Unknown fields are not silently accepted into a digest. Every record
type has a maximum encoded size.

### Database binding

```text
DatabaseBinding {
  schema_version
  project_id
  database_id
  admitted_sidecar_sha256
  wire_schema_version
  storage_schema_version
  created_operation_id
}
```

The binding prevents a correctly running but wrong database from serving the
project. Executable/process provenance remains host evidence because the daemon
does not attest it in this record.

### Runtime epoch

```text
RuntimeEpoch {
  schema_version
  runtime_epoch_id
  predecessor_epoch_id?
  host_instance_id
  started_at_observation
  sidecar_path
  sidecar_sha256
  database_id
  owner_pid_observation
  owner_started_at_observation
  status: recovering | reconciling | ready | degraded | closed
  refusal_reason?
}
```

A new epoch is inserted durably after journal restoration and before command
admission on every host restart. The single owning host closes its admission
barrier while changing epochs. Ordinary commands pin the current epoch in their
request, operation record, event envelopes, and aggregate heads. M1 does not
touch a single global epoch key in every command batch, which would serialize
otherwise independent aggregates. Instead, the one-owner admission barrier and
the conditional aggregate/lease heads make the epoch check race-free inside the
product boundary. Direct GapDB access by agents is forbidden.

### Operation record

```text
OperationRecord {
  schema_version
  operation_id
  command_type
  request_digest
  runtime_epoch_id
  expected_preconditions_digest
  status: accepted
  result_type
  result_schema_version
  canonical_result
  event_ids[]
  effect_ids[]
}
```

The record is inserted with `RequireAbsent`. Its GapDB `Record.Revision` is the
commit revision; that number is not embedded in the value because it is unknown
before commit. On retry, equal request digest returns the typed stored result,
augmented with the record metadata if a commit revision is part of the response.
A different digest returns an idempotency conflict and performs no mutation.

There is no durable `pending` operation state before the atomic batch. Before
publication, absence means no accepted operation. After durable acceptance, the
complete accepted result exists with all events and heads.

### Event envelope

```text
EventEnvelope {
  envelope_version
  event_id
  event_type
  event_schema_version
  reducer_version
  project_id
  aggregate_kind
  aggregate_id
  operation_id
  runtime_epoch_id
  event_index
  event_count
  work_revision_id?
  policy_revision?
  recorded_observations?
  payload
  canonical_event_digest
}
```

Events are append-only and inserted with `RequireAbsent`. Event payloads carry
all observations required for deterministic reduction. A reducer cannot ask the
clock, filesystem, Git, tracker, policy service, or any other adapter for facts.

### Stored and validated journal values

The pure Go journal boundary distinguishes product bytes from storage metadata:

```text
StoredEventV1 {
  record_revision       // positive GapDB Record.Revision metadata
  envelope              // EventEnvelopeV1 product value
}

EventVersionV1 {
  event_type
  event_schema_version
  reducer_version
}

EventRegistryV1 {
  immutable_exact_version_tuples
}

ValidatedJournalV1 {
  unexported_canonical_stored_events
}
```

Construction of `EventRegistryV1` validates and defensively copies its exact
tuples. `ValidateJournalV1` is the sole constructor of
`ValidatedJournalV1`; reducers and playback accept that capability instead of
unvalidated stored-event slices. Its accessors return values without exposing
mutable internal collections. This makes successful validation a structural
precondition rather than a convention callers can bypass.

`RecordRevision` belongs only to `StoredEventV1`; it is neither serialized in
the event envelope nor included in the canonical event digest. Canonical order
is `(record_revision, event_index, event_id)`. A validation failure produces a
stable typed refusal and no usable validated capability.

### Aggregate head

```text
AggregateHead {
  schema_version
  aggregate_kind
  aggregate_id
  logical_version
  runtime_epoch_id
  last_event_id
  canonical_state_digest
  authority_summary
}
```

An existing head is replaced with CAS against its observed GapDB record
revision; a new head requires absence. `authority_summary` contains the minimal
lease/fence/generation/candidate facts required by the command. Full projected
state can remain memory-resident or in a bounded projection record, but the
head's digest must match reduction of the accepted prefix.

### Projection digests

Each affected AggregateHead carries its own canonical state/projection digest
and is CASed independently. M1 stores no global projection head in ordinary
command batches: such a head would make unrelated aggregates conflict. Restore,
playback, and evidence compute the whole-product canonical projection digest by
a stable ordered fold of all aggregate heads/reduced states. That derived global
digest is comparison evidence, not a shared mutation precondition.

### Checkpoint

```text
CheckpointRecord {
  schema_version
  checkpoint_id
  through_gapdb_revision
  through_event_id
  event_prefix_digest
  canonical_projection_digest
  reducer_set_digest
  event_schema_set_digest
  uncompressed_size
  codec
  projection_bytes
}
```

```text
CheckpointHead {
  schema_version
  checkpoint_id
  checkpoint_record_digest
  through_gapdb_revision
  through_event_id
}
```

When a checkpoint is due, the next command's single batch carries a checkpoint
of the known already-accepted prefix preceding that command. Its through
revision comes from prior Record metadata; it never guesses the revision GapDB
will assign to the forming batch. The selector/evidence hashes the complete
canonical checkpoint bytes, so the checkpoint contains no recursive self-hash.
A checkpoint exceeding the revision-pinned budget is refused before publication
rather than split across writes. Large-artifact indirection is outside M1.

### External-effect records

```text
EffectIntent {
  schema_version
  effect_id
  operation_id
  effect_type
  provider_binding
  canonical_request
  request_digest
  attribution_marker
  expected_provider_revision?
  compensation_policy
}

EffectAttempt {
  schema_version
  effect_id
  attempt_id
  runtime_epoch_id
  started_observation
  request_digest
}

EffectObservation {
  schema_version
  effect_id
  observation_id
  outcome: applied | not_applied | ambiguous | conflict | compensated
  provider_revision?
  observed_value_digest?
  attribution_marker?
  bounded_evidence
}
```

The intent is inserted in the command batch. Attempts and observations are
subsequent commands with their own operations and events. Recovery reads intent
and observations before dispatch. Compensation may undo only state whose
revision, value, and operation marker still prove attribution to the intent.

## One-batch command transaction

For a new command, the host canonicalizes and hashes the request, validates the
current runtime epoch and typed preconditions, reads required heads, and runs a
pure reducer. It then sends one `AckDurable` `AtomicBatch` containing:

| Mutation | Condition |
| --- | --- |
| Operation record | key absent |
| Every event envelope | key absent |
| Every affected aggregate head | CAS observed revision, or absent for create |
| Checkpoint record/head when due | record absent; head CAS/absent |
| Every new effect intent | key absent |

GapDB rejects duplicate keys in a batch, so the codec must ensure each logical
record maps to one key. The whole encoded request must remain below the product
batch-operation and byte budgets, both stricter than GapDB's defaults of 1,024
operations and 16 MiB.

After a durable response, or after read reconciliation proves the same operation
record exists, the host may publish the events to the in-memory projection and
watch stream. It may then schedule external effects. Neither publication nor
effect dispatch can convert an unacknowledged/unknown outcome into success.

## Authority state machines

### Run, lease, and workspace

```text
Run:       allocated -> active -> handed_off -> terminal
                         |             |
                         +-> expired --+

Lease:     current -> expiring -> expired
              |                       |
              +---------> revoked ----+

Workspace: allocated -> bound -> mutable -> candidate_created -> quarantined
                                      |                    |
                                      +---- lease ends ----+
```

Invariant: `(WorkspaceID, LeaseID, RuntimeEpochID)` is immutable after binding.
An expired or predecessor-epoch lease makes its workspace quarantined; no later
lease can reactivate or inherit it. Replacement authorization creates a fresh
`LeaseID` and `WorkspaceID`. A stale process can still write files, but its
capsule cannot satisfy current head preconditions for handoff, review, or
integration.

Lease expiry advances a lease generation and journals authority/quarantine
transitions; it does not create or advance RuntimeEpochID. RuntimeEpochID changes
only at the mandatory host-restart admission barrier. Lease generation, fence
token, aggregate logical version, GapDB Record.Revision, and RuntimeEpochID are
distinct capsule fields.

### Candidate, review, and integration

```text
Candidate: created -> handed_off -> under_review -> approved | rejected
Review:    opened -> verdict_recorded
Integration: queued -> lock_held -> integrated | conflicted | refused
```

A candidate contains immutable commit/tree evidence and the creating lease,
workspace, epoch, actor, and process. Reviewer independence is evaluated in
ActorID terms under mission policy. Integration conditionally owns the target
ref and revalidates candidate reachability and current approval while holding
the serialized integration authority.

## Restore algorithm and precedence

1. Recover GapDB bytes through its public owner lifecycle.
2. Read and validate the complete immutable journal record set, including event
   digests, index/count groups, reducer/event versions, and record revisions.
3. Enumerate checkpoint heads newest first. Select the newest checkpoint whose
   record, prefix digest, version sets, through-position, state digest, and
   revision-pinned suffix budget all validate.
4. Reduce only its suffix. If no checkpoint validates, reduce the full journal
   only if it fits the same declared bound.
5. If neither path is bounded, enter `degraded`; if journal integrity or reducer
   compatibility cannot be established, enter `closed`.
6. Stable-fold the restored aggregate heads/states into the global canonical
   projection digest and compare it with playback/evidence expectations; no
   global projection record is CASed by ordinary commands.
7. Create the new runtime epoch durably; quarantine every predecessor lease and
   workspace through product events.
8. Reconcile outstanding effect intents by observation, then publish progress
   and change the epoch to `ready`.

There is no force-ready override. A missing checkpoint never authorizes skipping
journal records, and GapDB watch history is never used as the durable journal.

Sorted prefix scans are limited to 1,000 records/16 MiB by default. The public
cursor pins `ObservedRevision`; GapDB returns `SCAN_STALE` if the database
changes between pages. The host admission barrier keeps writers closed during
restore, traverses every cursor page, checks a constant observed revision, and
restarts the complete scan on a stale cursor. M1 must exercise multi-page
traversal and prove no omission or duplication.

## Playback model

Playback input is:

```text
PlaybackInput {
  database_binding
  selected_checkpoint?
  ordered_event_suffix
  reducer_registry_exact_version
}
```

Playback output is:

```text
PlaybackResult {
  through_gapdb_revision
  through_event_id
  event_count
  canonical_projection_digest
  adapter_call_count: 0
}
```

The playback process receives no adapter implementations; tests additionally
install panic/counting sentinels for tracker, Git, filesystem, clock,
qualification, artifacts, outbox dispatch, and compensation. Equality is over
canonical projection bytes/digest, not log text, timestamps of the playback
run, or map iteration order.

## Limits and validation rules

| GapDB default | Product rule for M1 |
| --- | --- |
| Key 4 KiB | Define and test a smaller canonical-key limit. |
| Value 8 MiB | Bound every record type; checkpoint has an explicit lower budget. |
| Frame/batch 16 MiB | Preflight the entire encoded atomic batch below a declared lower budget. |
| Batch 1,024 operations | Bound emitted events/effects and reject before publication. |
| Scan 1,000 records / 16 MiB | Page deterministically behind the restore admission barrier. |
| Watch buffer 256; history 100,000 / 64 MiB | Treat watch as notification only; rebuild from journal on loss. |
| 256 clients | M1 uses one owner and a small bounded client set. |

Canonical validation additionally requires:

- one event/reducer registry digest for the admitted build;
- exactly one operation record per `OperationID`;
- one request digest per operation identity for all time;
- all records from an operation at one GapDB revision;
- consecutive event indices and matching event count;
- conditional heads consistent with the reducer input;
- stored head/checkpoint/playback digests equal to canonical reduction;
- no external-effect observation without an existing intent;
- no compensation without current provider attribution;
- no current lease from a predecessor runtime epoch; and
- no workspace bound to more than one lease or epoch.

## Unresolved design questions for the plan

1. The external adapter must demonstrate the documented revision-pinned cursor
   contract against `gapdbd`, including multi-page success and deterministic
   restart on `SCAN_STALE`; restore cannot depend on one 1,000-record response.
2. The daemon readiness/status protocol does not attest source or executable
   digest. The plan needs a concrete host provenance record and a fail-closed
   branch if orphan identity cannot be proven.
3. Signal-based shutdown needs an exact same-process predicate resistant to PID
   reuse; if the host cannot prove it, it must leave the process alone.
4. The checkpoint product byte budget and cadence must be selected before test
   generation so checkpoint eligibility is deterministic and revision-pinned.
5. M1 resolves projection storage by keeping the full fast view in memory and
   per-aggregate digests in independently CASed aggregate heads. The global
   digest is a deterministic restore/playback/evidence fold, never a shared
   command-batch CAS.
6. Journal compaction and destructive retention are deferred. The proving
   database retains all journal events and checkpoints needed for audit.
