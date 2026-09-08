# Authority Journal v2 Contract

Status: normative M2 planning contract.

## Purpose

Authority Journal v2 generalizes the accepted M1 event/CAS model to every
production authority capability while repairing three boundaries that M1 did
not claim to solve:

1. OperationID uniqueness is host-global, not mission-local.
2. staged operations have immutable binding plus revisioned progress.
3. checkpoint suffixes are directly addressable without a provider
   revision-range scan or compactable watch history.

## Key law

```text
operation binding:
  spk/v2/operations/<operation-id>/binding

operation head:
  spk/v2/operations/<operation-id>/head

immutable effect intent:
  spk/v2/effects/<effect-id>/intent

aggregate head:
  spk/v2/heads/<scope>/<aggregate-kind>/<aggregate-id>

immutable event:
  spk/v2/scopes/<scope>/journal/<aggregate-kind>/<aggregate-id>/<version-hex16>
```

Every key component uses the canonical bounded codec. Logical version is a
positive 16-character lowercase hexadecimal integer. An event key is inserted
with absence required and is never updated or deleted in M2. Aggregate deletion
is a tombstone transition with a retained head.

`spk/v2/heads/` is the only aggregate-head spelling. It permits a complete
census before the set of scopes is known. The former
`spk/v2/scopes/<scope>/heads/...` spelling is rejected and is not a read or
migration alias. Operation binding/head records remain host-global under
`spk/v2/operations/` and form a second bounded recovery census.

## Operation resolution order

1. authenticate and syntactically validate the request;
2. canonicalize stable intent and read the global OperationBinding;
3. on exact binding, return or resume OperationState even if the runtime epoch
   has since changed;
4. on binding mismatch, return `IDEMPOTENCY_CONFLICT` before capability reads;
5. on absence, evaluate current epoch and capability preconditions;
6. publish the first meaningful transition and binding atomically.

The stable intent digest excludes RuntimeEpochID. The binding preserves the
epoch that evaluated the command as provenance. A request proven absent under a
predecessor epoch is not executed in the replacement epoch without explicit
re-evaluation.

## Publication law

One sealed CommitPlan becomes exactly one GapDB AtomicBatch and one positive
GapDB revision. It contains:

- absent OperationBinding on the first publication;
- exactly one OperationState event/head transition;
- zero or more capability event/head transitions;
- zero or more effect intents and eligibility-guard transitions; and
- due checkpoint records/slot update when explicitly planned; and
- exactly one immutable publication witness.

The sealed plan also exposes one closed, immutable, provider-neutral record
projection derived by the sealer from those typed members. Each projected
record names one admitted semantic role and target, carries its exact canonical
value, value class, and create-or-revision condition, and has no public
constructor. It is not a generic mutation API: callers cannot supply a raw key,
arbitrary role, provider mutation, delete, expiry, or condition-any. The GapDB
adapter maps these closed targets to keys and conditions without rebuilding
domain values or inventing membership.

The admitted roles are operation binding, immutable journal event, operation
head, aggregate head, immutable effect intent, eligibility guard, immutable
checkpoint record, checkpoint slot, and immutable publication witness.
EffectID is unique within one host authority, matching the host-global
operation membership that names it.

Every publication writes the witness at
`spk/v2/publications/<publication-id>/witness` with `RequireAbsent` and no
expiry. Its canonical schema-1 value binds PublicationID, OperationID,
operation logical version, sealed plan digest, the non-witness authority
record-set digest/count, and a domain-separated witness digest. The witness
does not contain a member manifest. Its provider record revision is the atomic
publication revision and is never copied into the value.

Witness construction is acyclic. The plan preimage includes the mandatory
witness contract version but excludes witness bytes. PublicationID is derived
from that plan digest; final non-witness records are derived next; their record
set is hashed; and only then is the witness constructed and appended. The
authority record-set digest/count exclude the witness. The sealed plan's
complete provider mutation set and CommitObservation mutation count include
it, so the 256-mutation ceiling reserves one member for the witness and permits
at most 255 non-witness records.

Head-transition input includes the committed runtime epoch and bounded
canonical authority summary needed to derive the final AggregateHead value.
After PublicationID derivation, sealing constructs each final head value. The
operation head stores canonical OperationState; other aggregate heads store
canonical AggregateHeadV2. Eligibility transitions name mission and shard and
carry their canonical value plus exact observed revision. A checkpoint member
names both its immutable record and mutable ring slot, with canonical values
and the slot's create-or-revision condition.

At most one event addresses an aggregate coordinate in a publication. Its
logical version is exactly the prior head version plus one. Every event has the
same OperationID, PublicationID, event count, provider revision, and distinct
consecutive event index. Publication success is not exposed before durable
acknowledgement or same-identity reconciliation.

The closed operation event uses event schema 2 and reducer version 2. Its
payload is exactly the canonical OperationState transition preimage, including
status, result identity/bytes, and cumulative event/effect membership but
excluding the not-yet-derived PublicationID. The sealer rejects a mismatched
payload. The final envelope PublicationID reconstructs the exact historical
OperationState. Operation event tuple `1/1` is incompatible and refuses.

The event registry exposes canonical descriptor bytes and a digest over its
registry ID/version, publication rule, ordering tuple, and sorted
family/schema/reducer/owner census. The complete reducer registry exposes a
separate descriptor/digest that also binds each declared implementation
digest. Runtime reducer application is stateless. Bounded repeated-call
determinism qualification is separate and cannot impose a replay-wide
observation limit.

The common journal owns the durable publication port and coordinator. A
CommitObservation names PublicationID, positive provider revision,
durable-through revision, and exact mutation count. Only exact validation
against the sealed plan and record-set digest creates an opaque
AcknowledgedPublication. Projection state and its rolling prefix advance only
from that acknowledgement, never from optimistic delivery or unresolved
ambiguity.

The coordinator registers every in-flight plan before delivery, allows
unrelated provider commits concurrently, and retains a bounded revision-keyed
acknowledgement ledger supporting at least 10,000 publications between seals.
A quiescent checkpoint barrier orders acknowledgements by provider revision and
folds the prior prefix with revision, PublicationID, plan digest, and sealed
record-set digest. One public pure prefix-advance helper performs this fold for
both live acknowledgement and restore. Unresolved ambiguity blocks sealing.
Capability packages may reach the coordinator but must not reach the GapDB
adapter directly.

## Ambiguity law

PublicationID is deterministically derived from OperationID, next operation
logical version, and sealed CommitPlan digest. On EOF, timeout, cancellation,
or malformed acknowledgement, reconciliation reads the global binding,
OperationState, events, and heads:

- complete matching membership returns the exact recorded result/progress;
- proven absence permits retry of the identical sealed plan and preconditions;
- partial or contradictory membership is corruption and closes authority.

No ambiguity path allocates a new OperationID, calls an external adapter, or
publishes a projection optimistically.

Reconciliation enumerates the complete sealed provider mutation projection,
including the mandatory witness, rather than reconstructing a second
membership model. Complete means every projected key has the exact canonical
value and one common positive commit revision. Proven absence means every
projected key is absent. Application records without the witness are partial,
not complete. Any partial, foreign, mixed-revision, or contradictory
observation closes authority.

## Restore law

Provider-neutral recovery addresses, scan targets, records, pages, store
revision coordinates, and typed recovery errors belong to the common journal.
They expose semantic identities and copied canonical bytes, never raw GapDB
keys or provider types. The adapter alone translates those types to the frozen
public GapDB client.

Every exported v2 value classified as a direct or nested durable record has
one recursively canonical final-byte representation. Persistence-facing
encoding, `RecoveryRecordV2` admission, and strict decoding agree on those
exact bytes. `RecoveryRecordV2` rejects noncanonical provider evidence and
never rewrites it. Existing semantic digest preimages remain independently
defined; canonicalizing final durable bytes does not implicitly change an
event, plan, witness, registry, reducer, or summary digest domain.

Watch is notification only. Restore captures a positive durable
`(DatabaseID, R)`, selects a compatible complete checkpoint, scans all current
aggregate heads and operation records, and exact-reads every new logical
version for each advanced/new aggregate. Each initial scan page must report
`R`; continuation cursors remain provider-pinned to it; each record revision
must be positive and no greater than `R`; and final status must report the same
DatabaseID and exactly `R`.

This is an attempt-bound stable-revision proof. The frozen GapDB API offers no
as-of `Get` and accepts no requested revision on the first prefix-scan page, so
the contract makes neither claim. Monotonic non-reusable revisions plus equal
capture/final coordinates prove that retained latest-value reads are coherent
at `R`.

Restore reconstructs historical OperationState from the closed operation
event payload. For every distinct suffix PublicationID it exact-reads the
immutable publication witness, validates its operation coordinate and common
provider revision, and advances the prefix with the foundation-owned helper.
It validates cumulative publication membership and sorts events by
`(Record.Revision, EventIndex, EventID)` before pure reduction.

Checkpoint operation summaries expose canonical, fixed-size
`OperationBoundaryV2` values containing an OperationID SHA-256 lookup digest,
logical version, and the operation-summary leaf digest. The leaf remains the
commitment to exact OperationID, version, last PublicationID, status/result,
and event/effect membership. Boundaries sort by lookup digest; duplicate or
colliding coordinates refuse. Totals and summary digest are derived from those
boundaries. Status counts are constructed from source OperationStates and, on
restore, recomputed from exact boundary-event states. Structural validation
checks shape/order/uniqueness and arithmetic; state-aware validation proves
counts and leaf commitments. Variable-length OperationIDs and terminal result
bytes are excluded.

The foundation owns strict canonical `OperationSummaryV2` encode and decode
operations. Checkpoint and restore code consumes those operations and may not
declare a second summary/boundary wire representation.

A persisted checkpoint identifies the last publication it already covers, not
the later publication that writes it. For checkpoint watermark `R`, the opaque
sealed projection supplies the terminal revision-sorted acknowledged
PublicationID `P`; the checkpoint records `P` as its
`covered_through_publication_id`. Candidate validation proves the immutable
publication witness for `P` at exactly `R`. The checkpoint record and retained
slot are atomically written at a common revision `C > R`, and that writer
publication remains part of the suffix. Origin mode has no checkpoint or
covered-through identity.

The complete operation-head scan supplies exact OperationIDs during restore.
Each boundary matches an exact ID through a foundation-owned pure method;
restore exact-reads the operation event at the boundary version, reconstructs
the full state, and verifies the leaf before suffix use. Existing operation
suffixes begin after the matched boundary, new operations begin at version one,
and missing, extra, unmatched, or regressed operations close readiness. The
final structured checkpoint encoding of 10,000 maximum-version boundaries plus
outer summary overhead must remain within
`MaxOperationSummaryBytesV2 = 2,112,000` bytes. This leaves 2,082,304 bytes of
the 4 MiB ceiling for aggregate projection entries and remaining checkpoint
metadata. The summary is embedded structurally, not as base64 JSON bytes.

A stale cursor or any database/revision movement restarts the entire bounded
attempt and discards all collected material. Missing versions, physical head
deletion, mismatched terminal heads, partial operation membership, unknown
version tuples, or exceeded budgets fail readiness. Only a sealed restore
result may admit the replacement runtime epoch.

Restore's elapsed-attempt budget is an orchestration capability. The exact
`internal/controljournal/v2/restore` package may import the Go
standard-library `time` package only to validate a bounded duration and enforce
per-attempt context deadlines. The v2 foundation, checkpoint, and playback
packages remain time-free. Wall-clock values MUST NOT enter durable records,
ordering, revision selection, digest preimages, or acceptance decisions. The
architecture exception MUST be exact-source/exact-target and MUST NOT widen to
the `internal/controljournal/v2` prefix. WP01 owns structural import and object-
use confinement, including refusal of ambient-clock APIs and escaping deadline
functions. WP04 owns executable validation, derivation, placement, propagation,
cancellation, retry, and parent-deadline behavior. WP17 binds those gates and
their overlay mutants to one source identity. No partial static control-flow
model is authoritative. See `post-approval-defect-7.md` and
`post-approval-defect-8.md`.

## Capability ownership law

The common journal owns publication mechanics and canonical validation only.
Mission, artifact, workgraph, execution, workspace, handoff, review,
acceptance, integration, effect, and outbox packages own their commands,
events, states, decisions, reducers, and narrow repository ports. Neither the
common journal nor the GapDB adapter interprets capability policy.

## Refusal law

Semantic refusal order is capability-defined and behavior-inventory traced.
All mutable facts used by an accepted command are represented by provider
conditions in the same batch. On CAS loss, resolution first checks whether the
same operation committed, then identifies the moved authority/eligibility
coordinate. It never retries a newly decided batch automatically.

## Structural law

No production package, dependency, binary, installation, configuration,
fallback, importer, build tag, generated edge, reflection path, or subprocess
may select or reach SQLite. Historical evidence is permitted only by exact
path-scoped architecture rules and is never executable production input.
