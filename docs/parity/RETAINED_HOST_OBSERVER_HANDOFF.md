# Retained Host observer — next bounded integration slice

This continues the selected production-observation layer under existing constraints.
`continue` does NOT authorize a new provider image/API, qualification receipt, expiry
hold, generic key/callback port, or weakened live-evidence requirement. Architect Op
01M4V7BWB53R4CK18V28SFDD8A stays open. Existing checkpoint40 files/641 evidence entries,
images, candidate, coordination file and manifest/archive chain were reverified.

## Source-bound composition

- `productionruntime/composition.go`: keep the actual `AuthorityBindingV2` scalar value
  created from validated Config/bootstrap in an unexported productionOwner field,
  after ensureAuthorityBinding succeeds. No reading binding bytes to manufacture the
  expectation, no public setter or caller-supplied private identity.
- Host high entry takes context and a slice of genuine opaque coordinator ACKs ONLY.
  Nil/unsupported/unowned/composition substitution refuses. The Host retains its
  original concrete productionOwner, lifecycle and exact adapter instance.
- Use projection.AuthorityV2.Attribution(), NOT Snapshot(): Attribution is constant-size
  and commits the contiguous prefix including operation changes. Snapshot may allocate
  flat projection state before ACK resource preflight. Capture database/revision/durable,
  runtime epoch and rolling CursorToken; recheck the entire attribution at completion.
  No refresh, command barrier, write lock, admission, restore, retry or projection mutation.
- Require ready Host at entry and exit; recheck state, epoch, database and same owner.
  A proof is an observation at a bounded interval, never permission to keep using an owner.

## Closed adapter preparation and readback

Add provider-free opaque adapter preparation/session/result types, no key/value iterator,
serializer, selector, raw client, request constructor, generic callback or ID factory.
Preparation consumes the ACK slice plus the already captured typed StoreRevisionV2.
It reuses buildRetainedPublicationMapV2 and its reviewed scalar bounds, which preflight
ALL ACKs before Records/CanonicalValue/key copies. Reject ACK revisions/durability beyond
capture. Prepared entries never leave the adapter. Keep the preparation tied by private
pointer to the precise owned adapter/transport/lifetime; ordinary Dial cannot participate.

Every final unique encoded publication row is independently exact-read through the owned
fixed Get transport. Require echoed key, exact final provider revision, exact unchanged
canonical bytes and no expiry. All ten roles remain included. Assertions are charged
work, not retained rows or historical eligibility evidence. Immutable/predecessor laws
and no alias/deletion semantics remain unchanged. No Scan/ReadMany/snapshot can earn credit.

Preparation must enforce that the existing sealed role codecs cannot name authorityBinding,
runtimeCurrent, runtime epoch or runtime-admission namespaces. Thus publication keys and
fixed private keys are disjoint by construction; do NOT expose a raw-key collision port.
Test the namespace partition against every admitted role/codec.

## Authenticated bracket and private set

One single-use session owns this sequence:

    scalar projection capture + ALL preflight + prepared expected map
    S0 (ready, original owner/database, current R, R <= durable <= current)
    exact publication Get reads
    exact owner-private binding/current/head/predecessor-triple Get reads
    S1 (same coordinate; ONLY this count is used)
    S2 (same coordinate; S0/S2 counts are ignored)
    exact projection/Host close + terminal lifetime check

S0 must match the captured projection R; no catching up a stale projection. Current R
cannot change; durability must continue covering R and not exceed current R. Status
owner/db matching is already enforced by the fixed owned transport on each actual RPC.
Owner metadata uses EXISTING unexported authorityReader.Get and the reviewed helper with
trusted retained binding/epoch/capture, not a new callback/public-key surface. Every private
row is actually read, not credited by 3*n+3. Its bounded visited-set length feeds closure.
A lower adapter closing method may accept this expected-private-cardinality scalar but
must NOT return actual provider counts, private keys, an authority/admission proof, or a
Host proof factory. Only the private Host composition can mint the high matched result.
No caller can choose private IDs. A matched-publication count is expectation bookkeeping,
not whole-store proof. Reject counts above195 and overflow before summing with pub count.

S1.record_count must equal unique publication count plus actual visited private count.
No extra/missing rows, unknown projects/families/private records, expired retained extras,
or disconnected epochs can be silently omitted. Count mismatches/errors return zero high
proof, no actual/unknown counts, record content, identifiers, paths or provider diagnostics.
Cancellation returns only fixed context sentinels, never wrapped provider/custom diagnostics.
Single-use session refuses repeated/out-of-order use, wrong adapter, wrong coordinate and
concurrent reuse; no RPC retry or port refresh. No SDK fallback or fault-proxy bypass.

## Resource proposal requiring review, not presumed approved

Existing expected-map caps remain:256 ACKs,4096 record occurrences,32MiB total canonical
record values,32MiB canonical plans,4MiB conservative keys,8192 mutations+assertions.
Existing private bounds remain:64 epochs/195 rows,4KiB per schema,256KiB retained private
bytes,1MiB private response-reservation budget,5s private duration; no blanket TTL rejection
in ordinary Get and no widened source/product ceilings.

For this integration propose a30s total deadline, maximum4294 credited RPCs (4096+195+3),
64MiB cumulative response frame payload, and32GiB executed-image bytes hashed. Before map
copies, use the immutable lifetime's verified image-size scalar and worst planned RPC count
to charge THREE full image hashes per RPC (endpoint/pre/post), plus ONE terminal lifetime
check. Checked arithmetic rejects over-budget input before copies/RPC. This is a conservative
work ceiling, NOT acceptance of every input within the map's independent bounds. No hash cache.

The adapter's private context budget must reserve frame length immediately after the4-byte
header and BEFORE frame allocation; cumulative frame budget covers untrusted replies too.
All private Gets inherit this same budget via an observation-scoped derived context, not an
exported raw port or generic callback. Per-frame existing caps remain12MiB/1024 JSON nodes/
64 fields/16 depth, contextual scanner/base64 stages; cumulative64MiB bounds repeated decode
work. Every actual RPC consumes an attempt before image checks and no repeated attempt can
escape charging. Terminal lifetime hash is reserved separately. The observation-scoped
context is infrastructure bookkeeping, not an authority token or permission to read keys.

Do NOT label this a proven heap/peak-memory ceiling merely by adding byte counters. Expected-map
copies, response parse/materialization/base64 buffers, private reconstruction and projection
scalar reads must be accounted separately in review; optional proof should fail closed if that
closure cannot be defended. No arbitrary128MiB peak claim or aggregate-cap widening.

## Verification and permitted changes

New adapter session/budget code/tests; narrowly factor existing expected-map scalar preflight
if necessary without changing its laws; private transport frame-budget hook; immutable lifetime
image-size getter only; new productionruntime Host proof/tests plus retained binding field in
composition. No existing provider/module/image/admission/source receipt changes, address/codec
expansion, new generic key factories, oracle/qualifier runs or unrelated edits.

Controls: genuine ACK all-role exact inclusion; real final overwrite/predecessor; every fixed
private row; empty-publication/private-only store; extra visible/extra+missing/expired-hidden
count simulation clearly labelled NOT admitted-image qualification; wrong R/database/owner,
S1 torn count vs S0/S2 decoys, concurrent projection/operation-prefix/state/epoch move,
Stop-BEGIN revocation, unknown/ordinary/proxy adapter refusal, no read before preflight,
exact/+1 work bounds, cancelled/mid-read zero/nonleaking result, no mutation/refresh/retry.
Keep simulators private; no test seam becomes a public authority factory.

Design review19690c1c-850f-41f: endpoint-membership contract viable; permits a narrow
runtime implementation, but withholds budget approval, completed-layer approval and live
qualification. Freeze owner, exact adapter/transport/lifetime AND projection pointers;
shared private session state must consume copied handles too, including after failures.
Do not call Host.Status (it calls Snapshot). Adapter pre-copy preflight must also validate
captured revision/durability and conservative work, not append checks after the map copies.
All public failures normalize arbitrary context errors as well as provider/transport errors.
The proposed work caps are development refusal ceilings, not reviewed performance/heap
acceptance: qualification image5426793 bytes would require65.11GiB at4294 calls, so full
map-limit inputs may legitimately refuse the32GiB work proposal. Context deadlines are
cooperative; mutex/kernel waits have not been proven hard-wall interruptible.

Implement only that conditional slice with implementer profile/context; residual peak,
time/work qualification and the live expiry witness remain open. Preserve each
failed source/log and fresh correction. Parent and independent code reviewer inspect exact
files/patch; once-only focused unit/race/vet/architecture. Unchanged admitted-image positive
and permitted negative controls may run only after scoped code review; they do NOT replace the
still BLOCKED deterministic live expired-retained-extra witness or real performance qualification.
No layer/goal completion, promotion, mission advance or Op closure from this slice alone.
