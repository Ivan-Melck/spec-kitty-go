---
affected_files: []
cycle_number: 4
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-02T20:17:30Z'
reviewer_agent: wp03-cycle3-reviewer
wp_id: WP03
---

# Post-Approval Defect 2: Recovery Contract Cannot Express WP04

**Mission**: `gapdb-only-production-authority-01M1GWDJ`

**Affected work packages**: WP01, WP02, WP03, WP04

**Disposition**: reopen and reapprove WP01, then WP02, then WP03; keep WP04
blocked until all three corrected dependencies are approved

## Summary

WP04 correctly stopped rather than importing the GapDB adapter, duplicating
private wire models, or weakening the architecture guards. Review of its
T017–T022 contract found that the already-approved WP01–WP03 surfaces cannot
express the required recovery proof.

The defect is architectural, not an implementation inconvenience:

1. provider-neutral recovery addresses, records, pages, revisions, and errors
   are owned by the GapDB adapter rather than the journal foundation;
2. the frozen GapDB API has no as-of `Get` and no requested revision on the
   first `ScanPrefix` page, so the original “pinned read” wording promises an
   unavailable provider capability;
3. event, head, binding, and operation value objects hide semantic fields that
   playback and terminal validation must inspect;
4. no durable-acknowledgement-derived projection or rolling-prefix seal exists;
5. registry/reducer compatibility identities are incomplete, and the current
   reducer observation session refuses its 257th distinct reduction;
6. `spk/v2/scopes/<scope>/heads/...` cannot enumerate heads in scopes unknown
   to the checkpoint without scanning all scoped journal history; and
7. historical operation membership cannot be reconstructed because only the
   latest OperationState head is stored while the immutable operation event
   payload is not required to encode that state transition.

## Ratified correction

### Enforceable stable-revision protocol

Recovery captures a positive durable store coordinate `(DatabaseID, R)`. The
first scan page must report `R`; every continuation cursor remains bound by
GapDB to that database and revision; every returned record has a positive
revision no greater than `R`; and the final status check must report the same
DatabaseID and exactly `R`. Any disagreement discards the entire attempt.

This protocol does **not** claim that GapDB provides an as-of `Get` or accepts a
requested revision for the first scan page. GapDB revisions are monotonic and
non-reusable, so equal capture/final coordinates prove that no committed store
mutation occurred during the attempt. The intermediate latest-value reads are
therefore coherent at `R`.

### Enumerable heads and reconstructible operation history

The aggregate-head spelling becomes:

```text
spk/v2/heads/<scope>/<aggregate-kind>/<aggregate-id>
```

The old scope-first spelling is rejected rather than aliased. Immutable events
retain their per-scope, per-aggregate logical-version keys. Operation bindings
and current heads remain host-global under `spk/v2/operations/` and form a
second bounded census.

The closed operation event tuple is bumped. Its payload is the canonical
OperationState transition preimage, excluding only the PublicationID that the
sealer has not yet derived. The final event envelope supplies that
PublicationID, permitting recovery to reconstruct the exact historical
OperationState and publication membership without adding a second immutable
operation-state record or parsing a private adapter wire model.

### Ownership and acknowledgement

WP02 owns closed semantic recovery addresses and scan targets, immutable
records/pages, store revision coordinates, typed recovery errors, complete
semantic accessors, registry/reducer descriptors and digests, the durable
acknowledgement seal, publication coordinator, projection tracker, operation
summary, and sealed rolling-prefix/checkpoint view.

WP03 owns only translation between those contracts and the admitted public
GapDB API. Capability packages may reach the foundation publication
coordinator, but direct capability-to-GapDB-adapter reachability is prohibited.

Runtime reduction is stateless. Determinism qualification is a separate
bounded repeated-call test; it is not an unbounded runtime memoization cache.
The acknowledgement ledger is explicitly bounded and admits at least 10,000
publications between checkpoint seals.

## Why approvals must reopen in order

WP01 approved and froze the old head key, old operation tuple, and architecture
surface. Its guards would correctly reject the repaired WP02/WP03 design, so
WP01 must be corrected and independently reapproved first.

WP02 depends on those corrected contracts. It must then be reopened to move the
provider-neutral recovery vocabulary inward, make historical operation state
reconstructible, replace the 256-observation reducer session, and establish the
acknowledgement-derived projection/prefix capability. WP02 must be independently
reapproved before adapter work resumes.

WP03 depends on the repaired WP02 types. It must then be reopened to make the
GapDB key codec and reader implement the corrected semantic contract, return a
foundation commit observation, and enforce the capture/scan/read/final-check
protocol. WP03 must be independently reapproved after WP02.

WP04 remains blocked throughout this sequence. It must not implement against a
temporary adapter type, weaken the guards, or absorb dependency-owned code.
Only after WP01, WP02, and WP03 are reapproved may WP04 rebase and resume
T017–T022.

## Compatibility disposition

Authority Journal v2 is not deployed production state. These are corrections
to an incomplete preproduction contract. There is no dual read, compatibility
alias, or migration path for the defective key spelling or old operation event
tuple. Encountering either closes recovery as an unknown/incompatible format.

The frozen GapDB provider tuple remains unchanged.
