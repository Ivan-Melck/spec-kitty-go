# ACK-bounded aggregate-head census — proposed proof seam

Status: architectural proposal, **not implemented or qualified**. Research
identified a real evidence gap: current ACK proofs verify historical events and
current operation heads, but do not independently close non-operation head
membership. Private comparator changes cannot manufacture that missing
observation. This proposal is within the approved trace-evidence work; it does
not assert new admission or broaden public workflow/API parity.

Independent design review (`7da9e978-467e-447`) found a viable narrower seam but
**a physical-completeness blocker**: the exact admitted GapDB source's ScanPrefix
and ReadRecoverySnapshot omit expired records before cleanup changes revision.
Rejecting expiry on returned records cannot detect an expired extra head. A scan
can prove only the complete **live** head set at its pinned AsOf. It cannot prove
the full physically persisted set. No implementation may silently equate those
claims or accept the product's expiry-free write invariant as independent
absence evidence for out-of-band expired heads. Physical completeness needs a
separately justified observation/contract; the original stronger claim remains
unproved. The following design is conditional on resolving that boundary or
explicitly naming/retaining the narrower live-only proof.

**Follow-up:** [whole-owner cardinality alternative](GLOBAL_CARDINALITY_CENSUS_ALTERNATIVE.md)
is independently design-reviewed viable in principle: all closed expected records
(independent exact readback of every sealed role AND fixed private metadata),
coherent existing global retained count and actual per-RPC owner attribution.
It does not rely on expiry-free writes alone or raw global enumeration. Its
production seams are not implemented; this original live-head design is not
silently reclassified as a physically complete proof.

## Why the current progress DTO is insufficient

`MissionProgress.Items` is the projection list's complete result, not a whitelist
of terminal stages. Non-operation inclusion uses each full coordinate's scope;
operation inclusion instead uses its immutable binding scope. Mission/work scopes
include old generations and tombstones. Host-scoped workspace/shared integration
targets are excluded even when their payload mentions this mission. Host runtime/
checkpoint operation bindings are excluded; a mission-bound operation publishing
a host aggregate is still included. Matching unknown families are not omitted.
Mission blockers/decisions are mission-family aggregates, including closed facts.

ProgressItem discards coordinate scope, provider revision, last event, state
digest and tombstone. `(family, aggregate ID)` is therefore not an independent
graph address; two distinct coordinates can project ambiguously to one DTO.
The current validator closes operation membership only.

## Narrow next implementation contract

Add a named read-only Host proof operation taking **actual opaque collected
ACKs**, not arbitrary caller keys, scan targets or replacement historical
coordinates. An ACK has no DatabaseID: existing proof establishes matching
persisted evidence in the current owner database, not the ACK's original
producer database. Identical foreign ACK/evidence/revisions are indistinguishable
without additional owner-bound collection provenance. Do not promise foreign-
origin rejection from the current ACK type alone.

Use the current projection's database/revision/durability capture, initially
verify owner/store identity and revision, verify every ACK's existing binding/
witness/event proof at that same capture (including collected initial origins),
and fail rather than refresh if the store coordinate no longer matches.
ReadV2 reads latest values, not historical values. Every scan page must report
captured revision and identical pinned AsOf; chain only internally obtained
cursors. Verify store identity/revision again at end and explicitly recheck the
projection coordinate. VerifyRevisionV2 ensures unchanged database/current
revision and continued durability of R, **not** exact durable-through watermark
equality; no stronger historical-read or watermark claim is implied.

Derive the complete expected non-operation head set from the supplied sealed
ACK plans, keyed by full AggregateCoordinate. Check publication/provider revision
uniqueness, event/head bijection, complete aggregate versions from creation and
exact predecessor head record revisions. Select each coordinate's last sealed
**aggregate-head record bytes**, not a summary interpreted as reducer state.
Operation head storage has its own record role and is not part of this census.

Inside the owner, scan only existing `AggregateHeadScanTargetV2()` pages at the
captured coordinate; no foundation codec/address expansion or raw-key access.
Validate exact semantic address from the decoded head coordinate, expiry absence,
record revision, canonical bytes and every head field (event/publication/version/
epoch/digest/summary/tombstone) against the last sealed plan record. Require
bidirectional equality: every expected head appears exactly once and every
scanned head is expected. Extra records fail with a bounded typed error; never
return unrelated head content when the caller's ACK graph is incomplete.

Bound ACK processing as well as scans: count all ACKs (including operation-only),
origin-verification work, cumulative events/sealed records, semantic-address and
payload bytes, pages and owner-enforced elapsed duration. Before copy-returning
Plan.Records/Heads/Events or page/proof accessors, apply conservative closed size
bounds or add a separately reviewed safe measurement strategy; cancellation
alone is not an elapsed/work budget. Do not widen existing recovery ceilings.
Reject repeated/foreign cursors, inconsistent AsOf, empty nonterminal pages,
terminal/cursor inconsistencies and duplicate records. Check cancellation and
verify revision/projection again at end.
Return no partial proof on any refusal, and no unmatched coordinates/content or
provider diagnostics in errors/logs. Successful output is immutable/copy-only,
containing only matched heads, captured coordinate and pinned scan AsOf, explicitly
identified as live-only unless physical completeness is separately proved. No
adapter, raw scan, mutation, projection refresh or arbitrary read is exposed.

### Important scope limit

With current admitted reads this seam proves only **live head membership at
pinned AsOf and exact sealed-plan bytes**, not physical persisted completeness,
original ACK producer identity, omitted operation-only history, orphan journal
records or private admission membership. It alone does not prove capability
owner semantics or replay correctness: generic
sealed plans can carry summaries/digests that a later typed foundation correctly
rejects. Keep that second proof separate. Unknown/unqualified owners must still
fail at the graph layer; a census pass is not business-equivalence acceptance.

## Then close the private graph and public census

Once the observation seam is independently reviewed, join every current head to
its exact historical typed replay and independently reproduce global projection
digest. The digest includes non-operation host states and tombstones, excludes
operations, and does **not** commit last publication; explicit head/publication
checks are mandatory in addition to digest equality.

Apply projection coordinate-scope membership independently and require exact
multiset equality with every non-operation progress item; reject ambiguous
coordinate-to-DTO mappings. Reproduce bounded authority/observed classifications,
sorting and derived arrays/counts from the actual production rules, not an
improved business interpretation. Action-census completeness is separate.

Special domains remain explicit: mission prior/next payload wrappers, readiness
and legacy dispatch, mission fact-set digests, workgraph `current_revision`
wrapper, and legacy mission/decomposition `{}` operation summaries need their
own typed contracts. Decomposition effect intents, eligibility/claim/private
runtime records are not automatically external-effect/progress aggregates.
Recovery snapshots deliberately exclude some of these private domains.

## Required evidence before integration

Unit and live persisted controls: omitted/extra/duplicate head or ACK, missing
origin/creation, incomplete/mixed scopes, wrong revision/expiry, record bytes,
last event/publication, epoch/tombstone/digest/summary, duplicate/repeated cursors,
empty nonterminal pages, count/byte budget overflow, cancellation, stale capture,
foreign database, projection/store movement, proof-copy mutation and supported
operation-only/host-head histories. Keep unknown-owner refusal independent.

Additional review controls: shuffled ACK order, cross-operation updates to one
coordinate, skipped middle transitions, byte-valid but typed-owner-invalid
summaries, exact budget boundaries, and an expired-extra-head witness with cleanup
withheld. Select only RecordAggregateHead, never RecordOperationHead. Creation
absence/contiguity and predecessor conditions must be mechanical checks.

No production seam has yet been added. No real oracle was changed/rerun, no clean
candidate was altered, and historical reds/admission gates remain intact.
