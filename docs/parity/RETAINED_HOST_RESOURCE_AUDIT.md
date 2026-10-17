# Host observation resource closure — open audit worksheet

**Approved continuation:** [owned-incremental reporting and separate expiry-test
artifact](APPROVED_OBSERVATION_BOUNDARIES.md) replace the prior authority-needed
stops, not missing evidence. Whole-process memory/RSS is outside the accepted
reporting boundary; observer-exclusive library allocations remain owned. The
private ledger must expose incomplete coverage rather than invent a total bound.

Status: **NOT a resource/peak-memory/performance acceptance**. Companion to
RETAINED_HOST_OBSERVER_HANDOFF.md and read-only design review19690c1c-850f-41f.
Implementation slice is in progress; no qualification or authority advance.

## Independently checked pre-existing copy paths

- Core commit_plan.go Records() copies both the SealedRecordV2 slice and EVERY
  canonical value in that one plan; CanonicalValue() copies a value again.
  Expected-map construction therefore allocates up to two complete charged value
  copies across occurrences, not just final unique retained values. Overwritten
  values and transient Records slices must not disappear from allocation accounting.
- Adapter convertRecord() copies the decoded Get value. Owner-private helper then
  bytes.Clone()s it again. A small private retained schema ceiling does not prevent
  receiving/decoding a larger bounded response first.
- Fixed transport retains bounded frame bytes while the private scanner borrows
  slices. retainedDecodeBase64 allocates its decoded buffer; chunk roundtrip canonical
  checks allocate strings too. Envelope/result/error typed materialization and private
  reconstruction must be accounted, including malformed responses, not only success.
- Lifetime CheckEndpoint calls Check once; VerifyConnection calls checkLocked once.
  Existing exchange does endpoint, actual connection precheck and deferred postcheck:
  three full image checks per attempted RPC are a conservative upper bound; failed
  early paths may use fewer. Final independent lifetime Check adds one. No hash cache.
- Attribution is constant-size. Snapshot/Host.Status may rebuild the complete flat
  projection, so neither is an admissible preflight/closure shortcut in this slice.

## Image arithmetic independently verified

Restored files were not executed or altered for this worksheet:

| Original immutable image | Actual bytes |
| --- | ---: |
| admitted-original-reproduction | 5300621 |
| admitted-reproduction (WP13 regression) | 5327126 |
| recovered-m2 (qualification candidate) | 5426793 |

The design review's5426793-byte qualification-image figure is correct; it is NOT the
WP13 image size. A production session must use its own frozen, verified actual image
size, not either hard-coded fixture value. Conservative work formula:

    image_bytes * (3 * (charged_ACK_record_occurrences + 195 + 3) + 1)

Use checked arithmetic before map/key/value copying.195 reserves worst private helper
reads;3 reserves S0/S1/S2;1 reserves final lifetime check. ACK occurrences conservatively
bound final unique publication reads. A32GiB development refusal ceiling does not imply
that every4096-record map-bound input succeeds: all4294calls would need65.11GiB on the
qualification image. No cap is approved by this calculation alone.

## Still required before resource acceptance

1. Verify all calls and frame allocations mechanically consume the private observation
   budget, including owner-private Get childcontexts, failed endpoint checks, malformed
   headers/replies, copied/concurrent sessions and terminal checking. No context switch
   may bypass observation accounting; no provider/global-counter disclosure is needed.
2. Frame reservation after header and before allocation must bound cumulative payload,
   not just post-decode accepted bytes. Existing per-frame12MiB/node1024/fields64/depth16
   limits remain. Do not infer peak heap from64MiB cumulative wire alone.
3. Account caller-owned immutable ACK backing separately from incremental observer
   allocation, and account retained/transient map keys/slices/values, decoded records,
   private canonical JSON and reconstructed maps, lifecycle proc/path buffers and
   typed errors. Define reachable peak vs total allocation vs process RSS; these are
   different quantities. Unrelated caller/monitor allocations are not observer proof.
4. Cancellation/deadline semantics remain cooperative. Contexts do not make mutex
   contention, filesystem reads or kernel calls hard-wall interruptible. Fail/normalize
   late results; do not promise30s wall-clock merely because a context deadline exists.
5. A dedicated source-bound fixture may measure allocations/latency but measurement is
   not a universal bound. Independent source audit and ordinary real-path P95 remain
   necessary. Existing component zero-allocation meter and decoder controls do not
   establish full Host memory/performance closure.

## Completed read-only resource investigation — still not acceptance

Auditor d74098e3-9ddb-4ab fully read11 implementation files; parent verified all source
before/after hashes. Evidence: `host-retained-resource-review/run-20261014T175353Z/`
(audit.md/coverage.md). Large requested byte-buffer terms can be bounded by
`2V + 5.5F + 2P + 4MiB`, excluding rounding/structural/library storage; V<=32MiB
occurrence values, F<=64MiB cumulative allocated frames, P<=256KiB accepted private values.
The pinned SDK HardMaxValueBytes is64MiB, NOT4MiB: a12MiB response frame can decode almost
9MiB and clone it during SDK validation before matching/refusal. This is TRANSIENT
accounting, not a widened accepted product/private value ceiling. Envelope result/error
raw bytes borrow frame slices; do not count a nonexistent whole-large-result RawMessage copy.

Concrete unresolved geometry: publication Go map cardinality/hint<=4096 does not bound
Swiss directory/table growth on skewed hash prefixes. No pathological seed or exploit was
demonstrated. Independent planner2f228a16-bad7-4c1 inspected pinned Go1.26.7 construction /
generic insertion/compiler dispatch/splits and failed to refute the bound gap. Runtime
allocation failure isn't sanitized observation-budget refusal. Shared transport active-map
history is another separate term; cold caches, round-up sizes, goroutine/context/net objects,
borrowed input and shared pools also remain separate from incremental observer-owned storage.
AfterFunc stop does not join pending callbacks. Follow-up corrects total library families
 to<=2A (transport<=A plus pending Unix connect<=A), with timeout callbacks<=A+197 separately.

[Observer-only deterministic geometry proposal](RETAINED_STORAGE_GEOMETRY_HANDOFF.md)
uses exact-capacity occurrence/scratch arrays with merge/collapse preserving all existing
laws, and fixed intrusive active-connection nodes under the same transport mutex. It is
conditionally design-reviewed by4ea62d2c-a8b2-474 with no A/B semantic counterexample.
Parent incorporated zero/odd-pass/single-scan/exact-fill/reference-clearing and full node
unlink clarifications and authorized ONLY the seven private observer files; implementation
completed. Parent once unit/race66 top/871 outcomes each zero failures/skips, vet/arch5/14,
full hash/evidence/cache replay verified; independent FINAL source review edfd2a06-7d7d-409
APPROVED exact seven files (66/871 unit/race each, fresh6/6 overlay controls each, no scoped
defect). Parent then once unchanged-image live2 top/4 outcomes passed, zero failures/skips,
package2.256s, source/image stable; NOTresource/P95/expiry acceptance. Occurrence368/node32 logical layouts/fixedACK204800 measurements
are detailed in RETAINED_STORAGE_GEOMETRY.md. Separate source-library follow-up
 eedbc865-6a39-46b completed pinned allocator/library follow-up: rounded array/record/payload /
proc recipes are finite, but causal shared parent-context map growth, shared timer heap /
pool geometry and borrowed JSON buffer/ptrSeen history prevent all-causal/full-reachability
acceptance without authorized runtime/shared-state/boundary parameters. Parent verified
8295+358 inventoried paths and8 evidence entries; four parent docs changed separately.
See RETAINED_RESOURCE_BOUNDARIES.md: authority-needed STOP, no silent owned-only substitute. No aggregate
resource approval or layer acceptance; scoped source review and functional live pass do
not close rounded/cold/shared memory or decisive expired-extra witness.
No new provider/API/image or product cap. Parent build-environment capture is
`host-retained-resource-review-parent-go-environment.json`; detailed formulas and exact
scope remain in the handoff. No128MiB/RSS/hard-wall/P95 certificate follows.

The deterministic unchanged-image live expired-retained-extra witness stays BLOCKED;
this worksheet neither simulates it away nor authorizes a new executable/writer gate.
