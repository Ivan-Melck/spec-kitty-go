# WP03 Cycle 2 Review — Changes Requested

Reviewed commit: `721890a3e8c0f55cb53d475cf3a32c297dc4cf36`

## High: the closed codec admits mission-scoped operation aliases

`V2AggregateHeadKey` and `V2JournalEventKey` accept operation coordinates in
scopes that the host-global operation identity law forbids. Their parser
branches accept the same aliases. Adversarial probes reproduced both a scoped
operation head and a mission-scoped operation journal event.

Required repair:

1. Reject the `operation` kind from the generic aggregate-head constructor and
   parser family.
2. Require host scope for operation journal-event construction and parsing.
3. Add host, project, and mission mutant coverage proving there is one
   canonical spelling and no scoped operation alias.

Evidence: `internal/controlstore/gapdb/v2_codec.go`, constructors near lines
109 and 121 and parser branches near lines 350–365 in the reviewed commit.

## High: partial same-publication membership can reopen as conflict

`ReconcileV2` counts an exact mutable head from the sealed publication as a
shared precondition match. When all siblings are absent or unchanged, the
current decision returns ordinary conflict. A reproduced probe supplied the
exact sealed capability-head bytes at revision 71 and `NotFound` for every
other member; the result was conflict rather than partial-publication
corruption.

Once any present record carries exact sealed bytes for the publication, an
incomplete member set is partial durable membership. It must fail closed as
malformed/corrupt authority, not authorize retry or normal conflict.

Required repair:

1. Distinguish present exact sealed members from unchanged preconditions before
   the conflict decision.
2. Classify any incomplete mixture containing an exact sealed publication
   member as `OutcomeMalformedResponse` (or the contract's equivalent terminal
   corruption outcome).
3. Add singleton partial-membership tests for every mutable role, especially
   aggregate/capability heads, eligibility guards, and checkpoint slots.

Evidence: `internal/controlstore/gapdb/v2_ambiguity.go`, classification near
line 43 and the conflict branch near lines 92–93 in the reviewed commit.

## Medium: scan validation admits a non-progress truncated page

`convertV2ScanPage` accepts zero records with `Truncated=true` and a nonempty
cursor. GapDB cannot produce that response: a continuation cursor derives from
the final returned record, while an oversized first record is an error.

Required repair:

1. Reject every truncated page containing zero records, even if a cursor is
   present.
2. Preserve a valid empty terminal page (`Truncated=false`, no cursor).
3. Add both cases as regression tests.

Evidence: `internal/controlstore/gapdb/v2_reader.go` near line 133 in the
reviewed commit.

## Passing scope to preserve

The remainder of the five-file WP03 surface passed review: one sealed record
maps to one mutation; validation finishes before the sole durable
`AtomicBatch`; product count/raw/wire and value-class budgets are enforced;
conditions, acknowledgements, and delivery classifications are closed; watches
are notification-only; provider types stay behind the adapter; and no SQLite,
backend selector, or subprocess path was introduced.

Focused, repeated, race, full-package, compile, vet, staticcheck,
architecture/helper, formatting, and diff gates passed. External sidecar
qualification remained unavailable because its explicit binary and data-root
preconditions were absent; no external qualification is claimed. Temporary
review probes were removed.
