# Post-approval defect 5: acyclic checkpoint identity and summary ownership

## Finding

WP04 exposed two dependency-contract defects before creating production code.

First, the approved `CheckpointV2` model named the PublicationID of the
publication creating the checkpoint. That is not constructible: the checkpoint
bytes participate in the sealed-plan preimage from which PublicationID is
derived, while the checkpoint would also have to contain that PublicationID.
This requires a cryptographic fixed point and cannot be an implementation
invariant.

Second, WP04 must persist and recover `OperationSummaryV2`, but WP02 exposed
validation without a public strict canonical codec. Reproducing the private
wire model in WP04 would create a second authority representation.

## Locked correction

`CheckpointV2.created_publication_id` is replaced by
`CheckpointV2.covered_through_publication_id`.

For every persisted checkpoint:

- `through_record_revision = R` is positive;
- `covered_through_publication_id = P` is the unique acknowledged publication
  at revision `R` and the last publication folded into the checkpoint rolling
  prefix;
- `P` comes only from `SealedProjectionPrefixV2`, never from a caller;
- the checkpoint is the complete authoritative projection immediately after
  `P`;
- the checkpoint record and retained-ring slot are committed atomically by a
  later checkpoint-writing publication at provider revision `C`, where
  `C > R`; and
- the checkpoint-writing publication is post-checkpoint suffix and is not
  represented as covered by that checkpoint.

Origin mode has neither a checkpoint nor a covered-through PublicationID.
Persisted checkpoints do not admit a zero revision or absent covered-through
identity.

Checkpoint candidate validation exact-reads the immutable publication witness
for `P` and requires its PublicationID to equal `P`, its record revision to
equal `R`, and its bytes, self-digest, non-expiring class, and
absent-conditioned publication provenance to validate. The checkpoint record
and slot must share revision `C`, and `C` must be greater than `R`.

## Foundation ownership

WP02 extends `SealedProjectionPrefixV2` with an opaque accessor for `P`. Sealing
selects `P` from the greatest provider revision in the revision-sorted
acknowledgement ledger and binds it into the seal. Completion order is never a
selection rule.

WP02 also owns these exact public strict codecs:

```go
EncodeOperationSummaryV2(OperationSummaryV2) ([]byte, error)
DecodeOperationSummaryV2([]byte) (OperationSummaryV2, error)
```

They enforce the existing canonical field order, bounded input and output,
duplicate/ordering rules, status arithmetic, summary digest, and 10,000-entry
limit. WP04 embeds the returned canonical JSON structurally and decodes through
the same foundation codec. It must not define an operation-summary wire model
or reproduce foundation-private boundary fields.

## Work-package routing

- **WP01** reopens only to ratify the covered-through seal/accessor and the two
  summary codecs in the architecture guard. Mutants cover missing,
  substituted, zero, completion-order-selected, seal-omitted, old-field-name,
  and codec-deleted/substituted behavior. Adapter- or WP04-owned summary wire
  models remain forbidden.
- **WP02** reopens to expose the terminal revision-sorted acknowledged
  PublicationID through the sealed prefix and to implement the strict summary
  codecs. Existing witness, prefix, and summary behavior remains intact.
- **WP03** remains approved. Its exact-read witness and common-revision
  behavior already supplies the required durable evidence.
- **WP04** consumes only the corrected foundation surface. It records `P`,
  validates the witness at `R`, proves the checkpoint/slot publication at
  `C > R`, and treats that writer publication as suffix.

## Falsifiers

The correction is incomplete if any of these are possible:

- checkpoint construction accepts a caller-selected covered-through identity;
- a seal chooses acknowledgement completion order instead of greatest provider
  revision;
- the old `created_publication_id` field survives in a normative checkpoint
  model;
- the checkpoint writer's PublicationID is embedded in its own checkpoint;
- the covered-through witness is absent, mutable, expiring, malformed, or at a
  revision other than `R`;
- checkpoint record and slot revisions differ, or their revision is not
  greater than `R`; or
- WP04 defines or accepts an alternate operation-summary wire representation.

This correction changes no provider API, keyspace, WP03 behavior, backend
choice, or mission capability semantics.
