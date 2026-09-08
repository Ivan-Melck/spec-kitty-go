# Post-Approval Defect 4: Durable Publication Evidence and Operation Boundaries

**Mission**: `gapdb-only-production-authority-01M1GWDJ`

**Affected work packages**: WP01, WP02, WP03, WP04

**Disposition**: keep WP04 blocked; reopen and independently reapprove WP01,
then WP02, then WP03; resume WP04 only after all corrected dependencies are
approved

## Summary

WP04 correctly stopped before source edits when its checkpoint/restore design
could not reconstruct two facts from durable GapDB state:

1. the acknowledged projection prefix folds the sealed plan and record-set
   digests, but those digests exist only in the live process's bounded
   acknowledgement ledger; and
2. the checkpoint operation summary retains anonymous leaf digests, but not
   the `OperationID -> LogicalVersion` boundary needed to address an operation
   event suffix after restart.

Neither gap may be repaired by importing the GapDB adapter into restore,
walking all history from origin after a nonzero checkpoint, parsing private
adapter bytes, trusting an optimistic projection, or weakening the prefix to
discard already-ratified record-set provenance.

## Ratified correction

### Immutable publication witness

Every sealed publication includes exactly one immutable ordinary record:

```text
spk/v2/publications/<publication-id>/witness
```

Its canonical value is:

```text
PublicationWitnessV2 {
  schema_version: 1
  publication_id
  operation_id
  operation_logical_version
  plan_sha256
  authority_record_set_sha256
  authority_record_count
  witness_sha256
}
```

`witness_sha256` is a domain-separated digest of the preceding fields. The
GapDB record revision is observed metadata and is not copied into the value.
The witness contains no member manifest: operation events discover
publications, while the plan and record-set digests preserve the exact
acknowledgement evidence needed by the prefix. A future publication manifest
requires its own evidence and version; it is not pre-built here.

Construction is deliberately acyclic:

1. validate the semantic plan;
2. hash its preimage, which includes the mandatory witness contract version
   but not witness bytes;
3. derive PublicationID from OperationID, next operation logical version, and
   that plan digest;
4. finalize events, heads, and OperationState with PublicationID;
5. derive and hash all non-witness authority records;
6. construct the self-digested witness from the resulting identities and
   counts; and
7. append it as one `RequireAbsent`, non-expiring record in the same atomic
   batch.

The authoritative record-set digest and count exclude the witness. The sealed
plan's provider mutation set and the provider observation count include it.
Consequently:

```text
provider_mutation_count = authority_record_count + 1
```

The frozen 256-mutation product limit therefore permits at most 255
non-witness records. Budget refusal occurs during sealing, before adapter or
provider access.

### Attributable operation boundaries

The checkpoint operation summary contains canonical, fixed-size boundaries
rather than anonymous leaf digests or full variable-length OperationIDs:

```text
OperationBoundaryV2 {
  operation_id_sha256
  logical_version
  leaf_sha256
}
```

`leaf_sha256` retains the existing domain-separated commitment to exact
OperationID, logical version, last PublicationID, status, result digest, and
event/effect membership digests. The explicit identity digest is the bounded
lookup coordinate; it is not a replacement authority identity.

Totals and the summary digest are derived from the boundaries. Status counts
are constructed from the original OperationStates; during restore they are
recomputed from exact event-reconstructed states authenticated by the
boundaries. Structural validation checks order, uniqueness, nonzero fields,
totals, and status-count arithmetic, while state-aware validation proves the
counts and leaf commitments. Boundaries sort by identity digest, reject
duplicate digest coordinates, and remain fixed size. Terminal result bytes and
variable-length OperationIDs remain excluded. During recovery the mandatory
operation-head scan supplies exact OperationIDs and current states. Restore
hashes each exact ID through the boundary's pure `MatchesOperationID` method,
joins it to the checkpoint boundary, exact-reads the operation event at the
boundary logical version, reconstructs the full prior OperationState, and
recomputes the leaf. A new operation has no boundary and starts at one; a
missing, extra, duplicate, colliding, unmatched, or regressed coordinate
closes readiness.

The compact boundary is required by the frozen 4 MiB checkpoint ceiling. Ten
thousand worst-case full OperationIDs plus their committed fields cannot fit
in one checkpoint. `MaxOperationSummaryBytesV2` is exactly 2,112,000 bytes.
The final structured checkpoint encoder, not an isolated boundary estimator,
must prove 10,000 maximum-version boundaries plus totals, all status counts,
field punctuation, and summary digest fit that budget. This leaves 2,082,304
bytes of the 4 MiB ceiling for aggregate entries and remaining checkpoint
metadata. The summary is embedded structurally, never as base64 JSON bytes.

### One shared pure proof implementation

WP02 owns public provider-neutral helpers for witness encoding/validation,
operation-summary construction/validation, canonical reducer-state digests,
projection-prefix genesis, and one prefix-advance function. Both the live
projection tracker and WP04 recovery use the same prefix implementation:

```text
next = H(domain, prior, providerRevision, publicationID,
         planSHA256, authorityRecordSetSHA256)
```

Playback remains structurally incapable of adapter access. The journal can
prove that playback packages have no I/O imports or ports and that supplied
reducers receive only recorded facts. Reducer-owner conformance tests remain
responsible for proving the behavior of arbitrary reducer implementations;
the contract does not claim Go can inspect a closure for hidden I/O.

## Dependency repair order

WP01 must first ratify the new key family, foundation-owned witness/boundary
types, dependency rules, and deletion/substitution/look-alike mutants.

WP02 must then make the witness mandatory in sealing, reserve its mutation
budget, expose the pure helpers and compact attributable boundaries, prove the
10,000-entry boundary budget, and use the same prefix function as recovery.

WP03 must then translate the witness semantic address to its exact key, include
the immutable absent-conditioned put in every batch and reconciliation proof,
and preserve the common provider revision across the witness and every other
publication member.

WP04 may finally exact-read one witness per distinct suffix PublicationID,
match it to the operation event and record revision, join exact scanned
OperationIDs to compact operation boundaries, and reconstruct the identical
rolling prefix.

## Mandatory adversarial evidence

The repair must reject a missing, duplicate, mutable, expiring,
revision-conditioned, wrong-revision, or separately committed witness;
substitution of every witness field; inclusion of the witness in its own
record-set digest; omission of the witness schema coordinate from the plan
preimage; provider counts that exclude the witness; complete-looking
reconciliation without the witness; divergent live/restore prefix logic;
completion-order folding; malformed, reordered, duplicate, missing, extra,
colliding, unmatched, or regressed operation boundaries; full OperationID or
terminal result leakage into checkpoint boundaries; over-budget 10,000-entry
summaries; and adapter-owned witness/boundary look-alike types crossing the
foundation boundary.

Exact budget evidence must prove 255 non-witness records plus the witness can
seal and 256 non-witness records plus the witness refuse before provider
access.

## Compatibility disposition

Authority Journal v2 remains preproduction. A v2 publication without the
mandatory witness contract is incompatible and refuses recovery. No dual
read, compatibility alias, importer, or best-effort prefix is admitted. The
frozen GapDB product tuple is unchanged.
