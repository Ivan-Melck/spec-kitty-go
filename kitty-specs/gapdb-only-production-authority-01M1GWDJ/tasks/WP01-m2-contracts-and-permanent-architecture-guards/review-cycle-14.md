---
affected_files: []
cycle_number: 14
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-03T20:05:19Z'
reviewer_agent: renata-gapdb-review
wp_id: WP01
---

# WP01 review cycle 14 — defect-5 guard repair

Verdict: **REQUEST CHANGES** for commit
`219126e823431847ad9bb6616d865b9da7d279e2`.

All focused, repeated, race, full architecture, compile, vet, staticcheck,
module, formatting, diff, and ownership gates passed. Those green results
expose the blocking problem: the new guard accepts fixtures that violate the
locked defect-5 semantics.

## 1. Prove refusal and digest influence, not field mention

The positive fixture's `Validate` method returns `nil` for both zero and
nonzero covered-through identities. Its `projectionSeal` only evaluates
`_ = s.coveredThroughPublicationID` before returning an unrelated existing
digest. The validator nevertheless treats an equality/inequality mention as
nonzero validation and any field reference in a Digest-returning function as
seal participation.

Repair requirements:

- make the positive fixture genuinely return a non-nil error for a zero
  covered-through PublicationID;
- make its seal digest derive from the covered-through identity;
- make the real validator prove those behaviors rather than mere AST mention;
- add execution-bound mutants for an inverted/no-op zero comparison and a
  dead `_ = field` seal reference; and
- prove both mutants reach and fail their intended semantic rules.

## 2. Activate the complete addendum on either new codec

`validateM2PublicationRecoveryActivation` currently checks only the two old
types and first eight helpers before its early return. Introducing only
`EncodeOperationSummaryV2` or only `DecodeOperationSummaryV2` therefore leaves
the defect-5 addendum dormant.

Repair requirements:

- include both new codecs and the covered-through accessor as activation
  signals;
- once any one appears, require the complete ten-helper/accessor/seal surface;
  and
- add codec-only partial-activation mutants for both directions.

## 3. Bind selection to greatest provider revision

The positive fixture contains only one ledger entry, so it cannot distinguish
greatest-revision selection from insertion/completion order. The current AST
check accepts comparison of any two `uint64` selectors and does not prove that
the selector is the ledger's provider revision or that sorting precedes
terminal selection.

Repair requirements:

- use a multi-entry positive fixture whose completion/insertion order differs
  from provider-revision order;
- bind the comparator to the exact provider-revision field;
- prove the ascending sort occurs before terminal selection; and
- add same-ledger mutants that sort by another `uint64` property and that
  select before sorting.

## 4. Require exact `[]byte`

`m2IsByteSlice` unwraps the element's underlying type, so
`[]DefinedByte` passes where `type DefinedByte byte`. The contract requires the
exact built-in `[]byte` type.

Repair requirements:

- compare type identity with `[]byte` rather than accepting an underlying byte
  element; and
- add a defined-byte mutant. A genuine alias may remain admissible because it
  is type-identical.

## Preservation requirements

- Preserve every original 154 mutant ID and all 12 defect-5 IDs already added.
- Extend and reseal the exact census for the new repair mutants.
- Keep edits within WP01's six owned architecture files.
- Preserve phased predeclaration, provider neutrality, GapDB-only production
  authority, and every prior guard.
- Rerun the complete WP01 validation ladder and report exact terminal evidence.

Anti-pattern result: synthetic-fixture test **FAIL** and locked decision
**FAIL**; all other items pass or are not applicable. Independent re-review is
required after repair.
