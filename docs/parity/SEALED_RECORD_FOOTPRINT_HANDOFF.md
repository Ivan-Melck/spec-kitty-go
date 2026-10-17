# Allocation-free sealed record footprint — narrow implementation handoff

Status: parent-approved **size-observation-only** increment for the existing
trace-evidence scope. Not a membership proof, valid ACK assertion or authority
transfer. No RPC, key selector, ownership utility, adapter or provider changes.
The authenticated transport/interface design is being resolved separately. This
independent scalar prerequisite does not select that unresolved design or accept
planner's proposed aggregate ceilings.

## Source and boundary

`controljournal/v2/commit_plan.go` already has private frozen sealed records and
`productRawBudget` enforcing existing ceilings; Records()/CanonicalValue() clone.
AcknowledgedPublicationV2.Plan() returns the opaque sealed plan without cloning.
Existing product limits: 256 members/operations, 1KiB key charge, 256KiB ordinary,
4MiB checkpoint, 6MiB raw product batch, 12MiB canonical plan. Preserve them all.

Add a small `SealedRecordFootprintV2` value type and
`SealedCommitPlanV2.MeasureRecordFootprintV2(context.Context)` returning it/error,
in a new `record_footprint.go` in the same authority package. No changes to existing
seal/validation/publication/digest behavior or plan fields. Value has ONLY scalar
uint64 fields:
- RecordCount and CheckpointRecordCount;
- AssertionCount (not added to membership RecordCount);
- RecordValueBytes and LargestRecordValueBytes (exact len of private values);
- CanonicalPlanBytes (exact len of private p.canonical);
- RecordKeyBytesUpperBound (records * existing MaxProductKeyBytes, conservative);
- ProductRawBytesUpperBound (existing key+value charges and key+20/assertion).

No raw values, targets, addresses, key/serialization/digest strings, record slices,
role maps, callbacks, client ports, root inputs or mutable aliases are returned.
The key charge is NOT a measurement of actual encoded keys. The size observation
is NOT peak allocation/whole-store cardinality/ACK or plan semantic validation.
Future caller still validates actual ACK after a conservative preflight covering
copies/validation/codec/JSON/RPC/private rows/elapsed work. This getter alone does
not enforce that future aggregate budget or qualify any census.

## Algorithm contract

- Nil context refuses without panic; cancelled/deadline context returns its error.
  Observe cancellation before work, during bounded record/assertion loops and
  before success. Return ZERO statistics on every failure; no content-bearing
  error field/diagnostic.
- Reject zero/unsealed plan markers (publication identity/digests/nonempty private
  canonical/records) cheaply, WITHOUT validateSealedPlanIdentity/finalBytes/hash/
  canonical decoding. Existing DecodePublicationID's fixed scalar validation is
  permissible. Do not imply that these marker checks authenticate the full seal.
- Check bounded record+assertion counts and canonical plan length before loops.
  Reuse existing limits and productRawBudget charges, avoiding new ceilings.
- Count ALL ten known sealed roles. Unknown role refuses. Only checkpoint_record
  may use checkpoint class, and it MUST use checkpoint class; every other role
  must be ordinary. Unknown class refuses, not default-to-ordinary fallback.
- Nonempty record values; existing exact ordinary/checkpoint/raw-total limits.
  Checked/subtraction-before-add arithmetic; no overflowing additions/multipliers
  or partial success. Cancellation still checked through assertion charging.
- Read private lengths directly. Never call copy-returning Records, CanonicalValue,
  CanonicalBytes, Checkpoints, record digest, key codec or JSON marshal/accessors.
  Successful valid measurements must allocate ZERO heap objects. Error allocation
  for typed refusal is fine; refusal must not clone bodies before returning.
- No transport, generic selectors, recovery address/codec/version expansion,
  provider dependency or fixed private metadata schema is part of this increment.

## Implementation scope and tests

Allowed new files ONLY:
`internal/controljournal/v2/record_footprint.go` and
`internal/controljournal/v2/record_footprint_test.go`.
Existing source/tests/default budget constants stay unchanged. Use real sealed
fixturePlanInput plus workspace claim/assertions to include all ten roles and
compare exact lengths/counts with owned-copy getters OUTSIDE measurement. Existing
fixture scope is synthetic, not a real native provider witness.

Tests: exact totals for all roles and checkpoint/claims/witness; assertions charged
but not records; repeated stable calls and caller-mutated scalar result; 0 heap
allocations via AllocsPerRun on both fixture and valid large checkpoint; zero /
unsealed/nilctx/cancelled/mid-loop cancellation no partial result; unknown/misclass
roles and empty/ordinary/checkpoint/canonical-plan/count/raw-total max+1 refusal;
exact existing boundaries via clearly labelled package-local structural probes,
not manufactured valid seals or ACKs. Compare core helper raw charge where useful.
No unsafe allocations/huge slices to simulate overflow; bounds-before-work and
checked arithmetic plus focused internal arithmetic tests if needed. Don't create
new public authority constructors solely to enable fixtures.

Go1.26.7, btrfs TMPDIR; full controljournal/v2 unit/race once each + vet, plus parent
fresh focused scenario regression after source inspection. Preserve before/after
source hashes, exactly scoped patch and unique logs outside repo, all failure logs
if any. Independent scoped review before acceptance. No external provider/crown/
qualification tests, clean candidate edits, image/receipt/Op or mission changes.
