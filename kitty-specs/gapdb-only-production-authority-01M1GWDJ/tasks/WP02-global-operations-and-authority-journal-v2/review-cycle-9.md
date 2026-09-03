---
affected_files: []
cycle_number: 9
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-02T22:56:12Z'
reviewer_agent: user
wp_id: WP02
---

# WP02 Review Feedback — Cycle 9

**Verdict:** Request changes

**Reviewed head:** `5beb9a09a4ccbded6e2f1af39ecbc98829fc948b`

## Blocking findings

1. The reducer/projector seam cannot reproduce the exact durable operation
   head. The operation event payload correctly excludes `LastPublicationID`,
   but the reducer receives only prior state and payload. Projection stores that
   payload-derived result while the durable operation head contains the complete
   `OperationStateV2`, including the enclosing event's `PublicationID`. Change
   the pure reducer/projector input so it receives sufficient validated event
   semantics, and add a test comparing the acknowledged operation projection
   byte-for-byte with the sealed durable operation-head value. Do not use a
   test-only identity reducer to define this production contract.

2. `SealedProjectionPrefixV2` is not yet a complete WP04 checkpoint input.
   Projection entries discard terminal `EventID` and tombstone state, so WP04
   cannot construct its required checkpoint entries without replaying origin
   history. Operation projection can also retain full terminal result bytes,
   contrary to the compact checkpoint contract. Preserve and seal the terminal
   head metadata needed by WP04, keep operation checkpoint representation
   compact, and add a consumer-shape test proving the complete checkpoint census
   can be constructed solely from the valid sealed projection/prefix.

## Evidence

The normative repeated, race, architecture, compile, vet, staticcheck,
formatting, ownership, and 121-mutant campaigns passed. An independent transient
operation-projection/durable-head equivalence probe failed and was removed. All
other new recovery, acknowledgement, ambiguity, reverse-completion, ledger, and
opaque-seal probes passed.
