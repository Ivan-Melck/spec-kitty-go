# Control Journal Contract v1

## Scope

This contract fixes the pure kernel boundary for M1. It describes canonical
commands, events, checkpoints, playback, and commit batches. It has no GapDB,
filesystem, Git, tracker, clock, random-source, or process dependency.

## Canonical values

- All JSON is UTF-8 RFC 8259 JSON with lexicographically ordered object keys,
  no insignificant whitespace, integers only for numeric protocol values, and
  no duplicate keys.
- IDs are caller-supplied non-empty ASCII tokens, at most 128 bytes, matching
  `[A-Za-z0-9][A-Za-z0-9._:-]*`.
- Hashes are lowercase hexadecimal SHA-256 of the canonical bytes named by the
  field.
- Logical versions, lease generations, fence tokens, event indices/counts, and
  GapDB record revisions are distinct unsigned integers and must not wrap.
- `RuntimeEpochID` is an opaque identity for one admitted host lifetime. It is
  never a logical version, lease generation, fence token, or GapDB revision.
- Times, when evidentiary, are UTC RFC 3339 strings supplied as command inputs;
  they never affect ordering or authority.

## Canonical keyspace

Every persisted product key is exactly one of these forms:

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

No alternate prefix or compatibility alias is readable or writable.

## Command acceptance

A command contains schema version, operation ID, command kind, canonical
payload, command hash, affected aggregate keys, exact expected aggregate logical
version and observed GapDB head-record revision, current opaque RuntimeEpochID,
and actor/process/run/lease-generation/fence provenance.

Acceptance is deterministic:

1. reject missing logical version, head-record revision, RuntimeEpochID,
   malformed identity, oversize input,
   unsupported version, or payload/hash mismatch;
2. if the operation exists with the same command hash, return its stored result;
3. if the operation exists with another hash, return
   `operation_identity_collision`;
4. reduce the supplied state and command into events, affected aggregate heads,
   an optional prior-prefix checkpoint, and effect intents;
5. encode exactly one compare-and-mutate batch; and
6. publish only after acknowledged commit or same-operation resolution proves
   commitment.

The adapter must never supply omitted revisions, split a logical command, or
replace an operation ID after an ambiguous response.

The persisted operation value conforms to
`operation-record-v1.schema.json` and deliberately has no commit-revision
field. The public reader returns it with GapDB `Record.Revision` metadata; that
metadata is the operation's commit revision after acknowledgement or read
reconciliation.

On every host restart the admission barrier creates a new opaque
RuntimeEpochID and durably replaces the one `runtime/current` binding described
by `runtime-current-v1.schema.json` before commands become available. Ordinary
commands pin that ID but do not CAS the binding. Lease expiry records separate
lease-generation/authority/quarantine events and never changes RuntimeEpochID.

## Event and ordering

Event keys end in a preallocated EventID; no key or value guesses the GapDB
revision that will be assigned at commit. Events conform to
`event-envelope-v1.schema.json`. The reader obtains each event's authoritative
commit position from GapDB `Record.Revision` metadata and orders the total
stream by:

```text
(Record.Revision, event_index, EventID)
```

All events in one operation share the same returned record revision, use
consecutive zero-based `event_index` values, declare the same positive
`event_count`, and have distinct EventIDs. Missing, duplicate, mixed-revision,
or out-of-range members close recovery. Aggregate logical versions advance in
affected heads; RuntimeEpochID records host lifetime and does not advance when a
lease expires. Lease expiry is an accepted authority/quarantine transition.

GapDB watch delivery is a transient progress optimization only. It may wake a
reader, but it is never journal history, durable acceptance evidence, or a
substitute for revision-bound reads and scans.

### Frozen Go validation capability

The Go implementation exposes one explicit validation capability at the root
of `internal/controljournal`. This is a frozen architecture seam, not a naming
heuristic:

```go
package controljournal

type StoredEventV1 struct {
    RecordRevision uint64
    Envelope       EventEnvelopeV1
}

type EventVersionV1 struct {
    EventType          string
    EventSchemaVersion uint32
    ReducerVersion     string
}

type EventRegistryV1 struct { /* immutable unexported state */ }

func NewEventRegistryV1([]EventVersionV1) (EventRegistryV1, error)

type validationSealV1 struct {
    version uint8
    digest  [32]byte
}

type ValidatedJournalV1 struct {
    seal    validationSealV1
    records []StoredEventV1
}

type InvalidValidatedJournalError struct{}

func (InvalidValidatedJournalError) Error() string

func ValidateJournalV1(
    EventRegistryV1,
    []StoredEventV1,
) (ValidatedJournalV1, error)

func (ValidatedJournalV1) Len() (int, error)
func (ValidatedJournalV1) At(int) (StoredEventV1, error)
```

`ValidatedJournalV1` is the authority-bearing capability passed to playback and
reducers. Go always permits the zero value of an exported concrete type, so the
enforceable law is that only `ValidateJournalV1` may produce a *usable*
capability. A zero, manually formed, partially copied, or seal/content-mismatched
value is invalid. Every accessor and every reducer/playback entry point verifies
the unexported seal independently and returns `InvalidValidatedJournalError`
before reduction or publication; callers must not rely on check-then-use.

No other function or method below `internal/controljournal` may return a usable
capability, accept a raw
`[]StoredEventV1` for ordering or validation, or recreate the capability by
aliasing or embedding. `StoredEventV1` alone owns the direct `RecordRevision`
field; a promoted or embedded field does not satisfy that ownership rule.

Architecture enforcement recursively examines every non-test Go file below
`internal/controljournal`. Before any such production file exists, the frozen
guard passes. The first production file anywhere in that tree activates the
complete contract, including the exact root-package types and function
signatures above. No filename has special status, and decorative types or
unused comparator-shaped functions cannot satisfy or mask the authoritative
seam.

The capability representation is exact: `validationSealV1` has direct
unexported `version uint8` and `digest [32]byte` fields, while
`ValidatedJournalV1` has only direct unexported `seal validationSealV1` and
`records []StoredEventV1` fields. Successful validation sets seal version 1 and
binds the immutable canonical records with the seal digest. Accessors verify the
seal, return copied values, and never treat a zero value as an empty valid
journal. Ordinary copies of a valid value remain valid. Preventing legal Go
zero-value declarations is explicitly not an architecture invariant.

Validation canonicalizes shuffled input by
`(RecordRevision, EventIndex, EventID)`. Each operation group has one positive
record revision, one OperationID, one common positive `event_count` in the
range 1–128, distinct EventIDs, and exactly the zero-based indices
`[0,event_count)`. Duplicate or non-total positions and missing, mixed,
out-of-range, or over-budget groups refuse atomically.

The immutable registry requires an exact
`(EventType, EventSchemaVersion, ReducerVersion)` tuple and admits no wildcard
or best-effort skip. Envelope version is exactly v1. The canonical event digest
is computed from canonical envelope bytes excluding the digest field;
`RecordRevision` is external GapDB metadata and is excluded. Any ordering,
grouping, version, registry, or digest failure returns a typed refusal and no
usable `ValidatedJournalV1`.

## Atomic commit contents

One batch contains:

- one immutable operation record and canonical result;
- 1–128 ordered events;
- 1–32 affected aggregate heads, each with its own logical version and
  projection digest;
- 0–16 deferred effect intents; and
- when due, one immutable prior-prefix checkpoint blob plus one checkpoint-head
  update.

The encoded batch has at most 256 mutations and 6 MiB raw bytes. Each key,
ordinary value, checkpoint, and wire frame also obeys
`m1-product-budgets-v1.json`.

## Checkpoints and restore

Checkpoints conform to `checkpoint-v1.schema.json`. A checkpoint placed in
command N's batch covers a known already-accepted prefix ending before command
N; it never embeds N's not-yet-assigned record revision. It binds the prior
prefix through its known record revision, terminal EventID, event-prefix digest,
schema/reducer sets, and canonical projection digest. The checkpoint selector
and evidence record hash the complete canonical checkpoint bytes; the
checkpoint does not contain a recursive self-hash.

A candidate is valid only when those bindings and the externally computed
canonical-byte digest match. Restore tries candidates newest first, then a
bounded full replay. Invalid candidates remain immutable evidence.
Eligibility is limited to 16,000 events, 48 MiB canonical event bytes, 32 pages,
and two stale-snapshot restarts.

## Playback

Playback accepts immutable stored events carrying EventEnvelope plus the GapDB
Record.Revision metadata and a fixed through-position. It sorts and validates
the total tuple above, then reduces into an isolated value. It cannot write,
acquire authority, trigger effects, advance cursors, or invoke a concrete
storage adapter. Missing/corrupt event groups or exceeded bounds return typed
refusal.

## Required typed outcomes

`committed`, `already_committed`, `logical_version_conflict`,
`head_record_revision_conflict`, `runtime_epoch_conflict`,
`operation_identity_collision`, `ambiguous_response`,
`proven_absent`, `unsupported_version`, `budget_exceeded`,
`invalid_checkpoint`, `journal_corrupt`, `replay_bound_exceeded`, and
`authority_closed` are machine-readable outcome kinds. Human prose is
diagnostic only.
