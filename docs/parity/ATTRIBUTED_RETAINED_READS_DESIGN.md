# Actual-response attribution: closed ownership design candidate

Status: parent synthesis **independently reviewed viable CONDITIONAL design**,
not code approval, completed census or new provider contract. Read-only planner
4662304b-c5d2-43a found the shared-kernel/private-adapter arrangement feasible.
Its recommendation wrapped at a turn limit; inspect actual source before relying
on completeness. Planner used shipped profile/doctrine + project charter fallback,
not the resolver overlays. No lifecycle/provider operations or state changes ran.

This resolves a direction to examine, not permission for generic selectors/clients.
The separate scalar footprint implementation is size-only and independent.

## Proposed arrangement, not provisional serialization API

- New provider-free child utility under `controlstore/gapdb/retainedkernel` imports
  neither SDK nor parent/sidecar package. It captures/checks retained kernel identity,
  not protocol. Private descriptor/revocation state; no dial/write/read-wire operation,
  descriptors returned, generic callbacks or request/key accessors.
- `Lifecycle` derives expected anchor from ITS PRIVATE retained state, including
  executed image identity and original socket identity, never caller provenance.
  Sidecar and adapter import the utility, so no reverse adapter->sidecar cycle.
- A dedicated owned-adapter construction path supplies immutable attribution and
  trusted database expectation internally. The high production Host proof takes
  actual opaque ACKs/context ONLY; callers don't select provenance, endpoints,
  lifetime, client, root, raw key, private epoch/op/event IDs or transport.
- Adapter owns private FIXED `status`/`get` transport, role codec, expected encoded
  map and frame serialization. No exported operation/arguments/unary callback,
  net.Conn/raw SDK port, key/index-based getter or public selector serialization.
  Kernel utility may inspect an actual Unix connection but cannot operate it.
- Private runtime-chain schema stays in production owner. Reuse EXISTING
  `authorityReader.Get(ctx,string)` owner-only port (`recovery.go:150`), with its
  attributed owned implementation. Do not introduce a new raw-key application /
  foundation port. Start fixed binding/current/head, derive identities ONLY from
  independently authenticated, strictly canonical predecessor/current rows.

Exported low-level constructors in internal infrastructure remain infrastructure,
not new authority: a kernel anchor is an identity constraint, not admission; public
Anchor construction must not become high Host input. Original constructor/owner
binding and immutable proof provenance must distinguish a genuine ready owner's
adapter from a caller-fabricated infrastructure instance. Opaque ACK alone also
lacks original producer DatabaseID: local exact readback still doesn't attest that
producer or historical original private writer.

## Actual read schedule and kernel contract

For EVERY proof get/status on its real response connection:

```text
retained endpoint/process check
  -> dial exact owned endpoint (one fresh Unix conn)
  -> actual SO_PEERCRED + kernel Getpeername + retained lifetime/image/endpoint check
  -> fixed bounded request/response exchange and strict frame/result decode
  -> repeat actual connection + retained lifetime/image/endpoint check
  -> close connection; release cancellation hook; return internally only
```

One retained lifetime on all calls/reconnects; no retries/refresh/barrier/mutations.
Original ready owner, database and revision remain bracketed S0/allreads/S1/S2.
Count ONLY S1. Projection closes afterward, matched-only immutable proof on full
success. Unknown content/count/provider diagnostics never return on failure.

Parent inspected current `observeStrictUnary` and process validator: it checks
peer/connected endpoint before exchange, process image/startticks and path before/
after, but doesn't re-query peer/kernel peername after. `peerPathOfConnection`
uses cached RemoteAddr. New proof requires real before AND after kernel queries,
not copying that omission. Hash `/proc/PID/exe` on every check (not
InspectOwnership's metadata-only fast path), bracket hashing with startticks and
retain pidfd exit/revocation where supported. If exact lifetime unprovable, refuse
membership; don't weaken existing startup fallback merely to pass proof.

Expected executable device/inode is the EXECUTED image, possibly descriptor-backed,
not original configured source path. No tuple retargeting. Connected socket FD's
sockfs inode is NOT filesystem pathname inode. Pinned provider binds via directory
FD; kernel peername `/proc/self/fd/N/...` must resolve via `/proc/PEERPID/fd/N/...`
then match original filesystem socket identity. Preserve the one-open/single
listener/no-online-reload admitted source contract. This is not a general peer
filesystem-inode attestation or proof against arbitrary same-process listeners,
transient pathname ABA, ptrace/kernel compromise or unrestricted self-exec. Reject
if those stronger properties are required or source contract no longer holds.

Stop/restart revokes attribution; do not attach old samples to a replacement owner.
Deadline is bounded by caller cancellation and existing per-RPC/time limits; close
connection on cancellation, unregister hook, close descriptors/connections on EVERY
error/normal path with clear lifecycle-vs-adapter ownership and no double-close.

## Compatibility and scope checks before implementation

- Candidate planner suggested routing all owned adapter get/status paths through
  attributed methods: Get, v2Status, ReadV2, ACK claim/reconciliation, Inspect status.
  Review exact call graph; no unauthenticated fallback or SDK ReadMany/snapshot /
  health/config credited as count/read proof. SDK mutation path stays existing.
- Generic get must preserve existing returned TTL semantics. **No-expiry is a
  membership/private-proof MATCHING requirement**, not blanket transport rejection
  silently changing all Get calls. Correct key/result/revision/schema checks still
  belong to transport. No generic API/ordinary progress performance expansion.
- Existing response-drop proxy paths and source-bound qualification contracts must
  be examined. Do not silently bypass a fault proxy, change its admission, transfer
  receipts or authenticate the wrong peer. If exact proxy-chain attribution isn't
  proved under its original contract, count evidence refuses; compatibility is
  unresolved rather than assumed from direct-daemon source checks.
- New strict transport must preserve wire-v2 error classification, not-found and
  command/read behavior and reject duplicates/missing/wrong-type fields, zero/
  foreign IDs, incorrect echo key and malformed Base64 within charged budgets.
  All uint64 values decoded exactly, not through floating point.
- Provider SDK imports remain only existing allowed adapter/sidecar roots; utility
  has none. Existing architecture guard prefix/exact-call classifications need
  source inspection/tests. No fresh utility's API may become generalized transport.
- Resource accounting covers every image hash/process lookup/endpoint/RPC/JSON /
  decode/clone step, not just matched payload. Existing ceilings preserved; optional
  census has explicit proof-work/time caps and failure rather than truncation.

## Review and acceptance checklist

Inspect existing process/socket/proxy APIs and actual pinned bind/open behavior.
Adversarially examine constructor substitutability, lifetime ambiguity/exec/PID
reuse, source-vs-executed inode, cached-peer escape, socket unlink/replacement/ABA,
private reader/key boundary, wire/error/TTL compatibility and fault-proxy behavior.
Require concrete path/line counterexample for unjustified claims; uncertainty is
not proof. Scope a smaller extraction/transport increment only AFTER review.
Read-only independent reviewer `2b731327-85ec-45c` loaded resolver Reviewer Renata /
review charter context, read both complete docs and actual requested code/guards.
It found no source-supported direct-daemon counterexample under exact image /
single listener /one open /no reload, but NOT implementation approval. Its six
prerequisites remain binding: immutable lifecycle-minted owned wiring and authentic
bootstrap dbID; actual fresh pre/post kernel peer/peername and hash/starttick bracket;
refuse polling fallback absent equivalent lifetime proof, revoke when shutdown
BEGINS even on failed/cancelled stop, context-interruptible hashing/connection cleanup;
refuse proxy census (no fault bypass) pending two-hop review; preserve strict SDK
UTF8/duplicate/base64/TTL/error/pre-admission-busy behavior and every credited direct
read; owner-private strict metadata-chain identities/revisions/cycle bounds. Current
readCurrentEpoch permissive json.Unmarshal isn't that proof. Repeated credentials
alone don't identify each writer/exec/FD transfer; the pinned source contract is
essential. Public infrastructure constructors are identity constraints, not admission.
Exact review text is cached as attributed-read-design-review-result.md. The scalar
meter is now separately scoped-reviewed, not complete proof-work budgeting.

Future code needs before/after source hashes, independent unit/race/vet/architecture
checks and source-bound live controls on unchanged adopted images. Source simulations
can't substitute for admitted executable ownership or expired-extra evidence.
Full graph/private admission/physical Git effects/progress/oracle remain unfinished.
