# Observer-owned incremental accounting — partial source ledger

## Status and boundary

**Full-call coverage is INCOMPLETE. No full observer bound, process ceiling,
peak-heap certificate, hard deadline, or production qualification is claimed.**
The approved boundary is observer-owned incremental resources, with borrowed and
shared resources disclosed separately. It does **not** excuse per-call owned
library buffers. In particular, the SDK validation clone and Base64 roundtrip
buffers are owned costs delegated to libraries, not shared exclusions.

Newest independently bounded-reviewed [qualification Join source-request recipe](EXPIRY_JOIN_SOURCE_ACCOUNTING.md)
adds exact nil-filtering/interface-backing semantics and a48-site source inventory,
not event multiplicity, physical allocations, caller/ABI or whole-call coverage.

Logical implementation: `internal/controlstore/gapdb/retained_resource_accounting_test.go`.
The separately reviewed [pinned allocation-slot leaf](OBSERVER_ALLOCATION_SLOTS.md)
adds `retained_allocator_accounting_test.go` for named per-event rounding/capacity
recipes. Neither model has full-call coverage. They add only private test
bookkeeping, source checks, genuine-ACK controls and a builder benchmark. It does not add a production API, counter, cap, admission
rule, provider import, hook, or runtime mutation. Approval/boundary/expiry
artifacts maintained separately are not replaced by this ledger.

Additional independently reviewed partial leaves now cover [private direct maps
and copies](PRIVATE_OBSERVER_ALLOCATION_EVENTS.md) and [four kernel buffer
families](RETAINED_KERNEL_BUFFER_EVENTS.md). Their conditional per-event slot
uppers are NOT added to this document's logical totals or relabeled whole-call
memory. The kernel's separately reviewed one-shot-open product correction removes
one uncapped path-allocation retry loop; it does not certify remaining library
loops or full memory/time. [JSON investigation](PRIVATE_JSON_ALLOCATION_INVESTIGATION.md)
is research only. Current review/parent evidence is in
[the implementation record](OBSERVATION_BOUNDARY_IMPLEMENTATION.md).
The separately scoped-approved [private JSON leaf](PRIVATE_JSON_ALLOCATION_EVENTS.md)
adds EOF-safe decoder backing histories, decoder/reader/conditional Valid scanner
headers and possible canonical fast copies. It is a branch-superset conditional
slot recipe, not full JSON or a term to add to this logical-length total. Remaining
Marshal/map/stack/string/reflection/context/library costs are still owned/open.

Newest [tagged S1 closure](RETAINED_EXPIRY_CLOSURE_SLICE.md) preserves ordinary
136-byte state geometry; E state152 bytes/component16 plus canonical handle escape
and one additional bounded pidfd-liveness guard. Actual diagnostic compile confirms
Arm's receiver leaks via stored canonical pointer; this does NOT certify event
multiplicity/full heap. Poll/context/mutex/wrapper costs remain OWNED/UNQUALIFIED.
Current ordinary/tagged correctness and native unarmed controls pass, not memory
acceptance. No ordinary partial model or historical timing transfers to E.

Prior source research is `owned-event-registry-f8vszzJ4/registry.json` under the
follow-up cache:36-family INCOMPLETE worklist with exact read/hash limitations,
not full causal coverage or new allocation approval. Parent derives kernel
liveness/poll callback multiplicity including one-byte short reads and six extra
one-shot-open checks, but that cache investigation is not an implemented/reviewed
recipe. [Actual small original-Host timing](OBSERVATION_PERFORMANCE_RESULTS.md)
measures latency only; it contributes NO B/op, peak/RSS or exclusive-owned bound.
The active [four-deliverable audit](OBSERVATION_QUALIFICATION_PLAN.md) still requires
closing EVERY owned family and accurately disclosing shared/runtime/borrowed costs.

The scope is one retained-observation sequence: preparation, publication reads,
owner-private reads, closing status/lifetime checks, and terminal failure where
applicable. The standalone expected-map builder has the same covered preparation
recipes, but is **not** an observation or provider-I/O benchmark. ACK creation,
sealing, coordinator projection, owner startup, and fixture construction precede
the measured builder scope.

## Units and accepted arithmetic envelope

All byte recipes below mean **logical source lengths or conservative sums of
logical lengths**. They are neither allocator-rounded bytes nor simultaneous
live bytes. They sum contributions over a call, including temporary copies and
occurrences later overwritten. Logical array element storage includes the
compiler's struct padding; append backing capacity, size-class rounding, and
other allocator costs are not supplied by this logical ledger. The separate
pinned leaf covers named normal-allocator source events; unlisted event counts,
compiler-generated storage and the remaining owned sites are still unresolved.
Do not relabel this document's logical subtotals as allocated slots.

| Symbol | Meaning | Envelope |
|---|---|---|
| `n` | Input opaque ACK count | `0 <= n <= 256` |
| `r` | All sealed record occurrences, not final unique rows | `0 <= r <= 4096` |
| `V` | Sum of canonical value lengths across **all** occurrences | `0 <= V <= 32 MiB` |
| `F` | Sum of charged response payload lengths (not the four-byte frame headers) | `0 <= F <= 64 MiB` |
| `D` | Sum of decoded backing lengths, before trimming Base64 padding | `0 <= D <= floor(3F/4)` |
| `P` | Accepted private value-byte prefix | `0 <= P <= 256 KiB` |
| `A` | Attempted observation RPCs, including failures | `0 <= A <= r+198` |
| `H` | Complete executable hash invocations charged/planned | `0 <= H <= 3A+1` |
| `I` | Verified executed-image bytes | `0 < I <= 64 MiB`, `I*H <= 32 GiB` |

`F` also charges oversized/malformed declared lengths before refusal; their
payload need not actually be allocated. Thus using `F` can overstate actual
backings. `D` is a structural input bounded by `F`, not a prediction that all
frames carry values. Work `I*H` is reported separately, **never added to retained
byte terms**. The terminal hash is the `+1`. An image of zero is invalid even for
the zero-cardinality control; that control uses `I=1` and `H=0`.

This envelope checks arithmetic consistency, not ACK validity or realizability
of every scalar tuple. Production additionally meters canonical-plan/key bytes,
assertions, per-record classes, per-frame lengths, cancellation, ownership, and
phase transitions. Tests use genuine coordinator-minted ACKs for integration;
scalar maximum/overflow controls do not forge authority. In particular, an ACK
count of zero with a positive scalar `r` is not evidence of a real workload.
All independent maxima are **not jointly accepted**: `I=64 MiB, A=4294,
H=12883` fails the hash-work envelope. For those maximal counts the image must
be at most `floor(32 GiB/12883)`.

Additions, products, and sums use checked unsigned arithmetic. Invalid inputs
return a completely zero ledger, not a partial result. Odd `F` uses integer
floor division, without floating point.

## Covered recipes

The layout control asserts actual `unsafe.Sizeof` results against the audited
geometry using the current compiler. The model computes with those actual types,
not merely the numbers printed here. Audited geometry is 64-bit; another target
or type layout must fail the assertion and be re-audited, not inherit these
constants. Compiler version and target are recorded by the focused tests.

| Term | Source and ownership | Logical recipe |
|---|---|---|
| Frozen ACK array | `buildRetainedPublicationMapV2` **or** `PrepareRetainedObservationV2`, owned opaque header snapshot | `[256]AcknowledgedPublicationV2 = 204800` |
| Preflight order array | `preflightRetainedPublicationsV2`, owned scalar metadata | `[256]retainedPublicationOrderV2 = 8192` |
| Two occurrence backings | `copyRetainedPublicationMapV2`, owned `rows` and `scratch` | `2 * sizeof(retainedPublicationOccurrenceV2) * r = 2*368*r` |
| Per-plan `Records()` backings | `SealedCommitPlanV2.Records`, delegated to Core, still observer-owned incremental copies | `sizeof(SealedRecordV2)*r = 360*r`, summed over **every plan** |
| First value copies | `Records()` clones every record's canonical slice | `V` |
| Second value copies | `SealedRecordV2.CanonicalValue` during map construction | `V` |
| Received payload backing | `retainedReadFrameContext`, owned | `<= F` |
| Base64 decoded backing | `retainedDecodeBase64`, owned; `DecodedLen(count)` before trimming | `D <= floor(3F/4)` |
| SDK validation clone | `decodeContext -> gapdb.NewPutMutation -> cloneOpaqueBytes`, **delegated-owned** | `<= D` |
| Base64 roundtrip | `flush -> EncodeToString` byte buffer and returned string, plus `string(part)` comparison conversion, **delegated-owned / owned** | conservative `<= 3F` |
| Adapter/private value copies | `convertRecord` append plus `retainedPrivateWork.read` `bytes.Clone`; one terminal rejected response can be copied first | `<= 2P + 4 MiB` |
| Intrusive registry nodes | `registerConnection`, owned; one fixed node per actually registered dial, no map geometry | `<= sizeof(retainedConnectionNode)*A = 32A` |

The frozen ACK and order arrays are counted **once per entrypoint**, not once
per ACK and not as both entrypoints in a single sequence. Preparation calls the
copy helper, not the standalone builder. Their sum is source-logical geometry;
it does **not** assert that both arrays reside on the heap. Existing empirical
snapshot escape/allocation tests are separate evidence, not a heap-placement
premise of this model. By-value copies, ABI temporaries, stack frames and stack
growth are not silently included in this geometry recipe.

`Records()` uses append-backed defensive copies. `360r` describes element
storage by length, **not** exact allocated capacity. The same distinction applies
to append-backed value clones. Two occurrence arrays keep their original `r`
capacity after compaction; a smaller final unique-row count must not reduce the
charge. Sorting swaps backings and copies headers, not payload bytes; tail and
other-buffer clearing do not undo allocations already charged. Strings reached
through copied headers are not deep-copied by that operation; newly encoded keys
are a different, currently open owned cost.

The adapter/private recipe counts the accepted prefix's two direct copies plus
one worst `convertRecord` copy before private schema rejection. That copy is
limited by the existing 4 MiB checkpoint ceiling, **not** the private 4 KiB schema
limit. Publication comparisons use transport reads directly and do not incur
this adapter conversion. The fixed 4 MiB reserve is conservative even for a
zero-attempt scalar control; it is not evidence of an actual allocation.
Private JSON/canonicalization buffers are additional open costs, not hidden in
`2P`.

### Large transport buffers: include library ownership without double counting

The named subtotal is:

```
transport-large-buffers = F + D + SDK-validation-clone + Base64-roundtrip
                       <= F + D + D + 3F
                       = 4F + 2D
                       <= floor(11F/2)       (5.5F)
```

This is a conservative logical-length envelope for **these four terms only**.
It is not a complete transport-memory recipe. The `5.5F` envelope replaces the
four terms in the covered subtotal; it is not added again. In fact the model
uses `4F+2D` and checks `<= floor(11F/2)`.

Base64 decoding consumes each canonical quartet once, with 4096-character
chunks. The sum of chunk character lengths is at most the received JSON bytes,
including escaped spellings. The audited standard-library `EncodeToString`
creates an encoded byte buffer and a returned string. Conservatively charging
`string(part)` as a third buffer, even when compiler optimization removes it,
gives `3F` without needing a perfect allocation attribution experiment. The SDK
constructor clones `record.Value` even though this caller immediately invokes
`Validate` rather than persisting a mutation. Treating either library operation
as merely shared infrastructure would be wrong. Early decode/validation failures
can reduce actual use; this is not a promise that all copies coexist.

The test ledger's `builderCoveredSourceLogical` is exactly:

```
204800 + 8192 + 2*368*r + 360*r + 2V
```

Its `coveredSourceLogicalUpper` adds **once** the transport subtotal,
`2P+4 MiB`, and `32A`. Both names are deliberately qualified. Neither is a
complete total, a process ceiling, or an empirical `B/op` predictor.
`fullCallCoverage` remains `false`, with nonempty `missingRecipes` on every
successful result. The latter is defensively copied to prevent a consumer from
silently editing subsequent coverage disclosures.

## Source-site registry and unresolved terms

Paths below are relative to the repository unless qualified. Each row indicates
what the current recipes do and do not cover. **Open means not yet quantified,
not zero and not safely excluded.** Existing finite production limits do not by
themselves supply an allocator/multiplicity recipe.

| Source site | Classification | Covered / open obligation |
|---|---|---|
| `internal/controlstore/gapdb/retained_observation.go`: preparation snapshot, state, context derivation, budget | Snapshot owned; child context/budget/state owned or delegated-owned | Snapshot geometry covered. State object, mutex/atomic bookkeeping, context values, deadlines/timers, closures, cancellation propagation and phase-related temporaries **open**. Parent context baseline is borrowed; incremental child state is not. |
| `internal/controlstore/gapdb/retained_publication_map.go`: preflight order, rows/scratch, merge/collapse | Owned | Exact element geometry and value-copy lengths covered. Caller ACK backing is borrowed. Scalar stack/ABI copies and complete escape/capacity accounting **open**. |
| `internal/controljournal/v2/commit_plan.go`: `Records`, `CanonicalValue`, target/scalar accessors | Delegated-owned copies; borrowed immutable referents | `360r+2V` by length covered here. The separate pinned leaf covers per-event append capacity/slot upper for these clones; complete compiler/ABI multiplicity remains **open**. ACK-reachable canonical plan/event/state bytes preexist this observation; no full-plan clone is charged because these entrypoints do not make one. Any future getter introducing a clone requires a new owned term. |
| `internal/controlstore/gapdb/v2_committer.go`, `v2_codec.go` and key helpers: `v2KeyForSealedRecord`, `parseV2Key`, validation and encoding | Owned/delegated-owned | Encoded key strings, parsing splits, Base64/path encoding, concatenation, formatting and validation scratch **open**; the current key-byte preflight charge is not a complete key-allocation recipe. |
| `internal/controlstore/gapdb/retained_transport.go`: framing, result/envelope slices | Owned payload; borrowed aliases | Payload backing covered by `F`. Scanner `raw`, envelope `Result`/`Error`, and node slices alias it; do not recharge their referenced bytes as new payload. Slice/header/object storage and four-byte header escape **open**. |
| Same: `retainedScanner.value`, `retainedParse`, `retainedShape` | Owned/delegated-owned | Nodes, field maps, array slice growth, decoded field-name strings, reflection/type-tag split and temporary maps **open**. Node/depth/field caps are not exact map geometry. |
| Same: `retainedDecodeBase64` and `flush` | Owned and delegated-owned | Decoded backing and three roundtrip logical buffers covered here. Separate pinned leaf adds their slot slack and a conditional 4096-byte chunk charge. Closures, scratch/scalar stack storage, strconv escape parsing and other allocation events remain **open**. |
| Same: `decodeContext`, `retainedTyped`, `retainedEnvelopeFields`, `retainedUTC`, `retainedRemoteError`, `retainedFields` | Owned/delegated-owned | SDK validation value clone covered. Small JSON decoder state, status/error structures, key/metadata/time strings, timestamp marshal, raw-message copies, error-evidence maps/slices, definitions and `remote.Clone` **open**, including malformed/error paths. |
| Same: `exchange`, `requestID`, `retainedRequest` | Owned/delegated-owned | Request ID formatting, request marshaling output and encoder scratch, timeout context, cancellation closures, deferred state and error wrapping **open**. Response `F` does not pay for requests. |
| Same: `registerConnection`, unlink/close | Owned | Fixed node geometry `32A` covered; node pointers do not pay for pointees. Lock/cancellation interactions do not prove a concurrency ceiling. |
| `internal/controlstore/gapdb/adapter.go`: `Get`, `convertRecord`, `requestContext`, error translation | Owned/delegated-owned | Direct value append covered in private recipe. Cloned expiry/time objects, contexts, typed/wrapped errors and non-value response fields **open**. |
| `internal/productionruntime/retained_private_records.go`: `read`, `observe`, `decode`, canonical helpers | Owned/delegated-owned | Direct copies plus one terminal adapter-copy reserve now have a separate slot leaf. Direct195/64/64 no-delete row/epoch/operation map objects/growth are separately covered under their pinned layout. Separate JSON leaf now covers named decoder/reader/Valid-header/backing histories and possible fast-copy slots, including EOF/failed prefixes. Generated keys, target/schema/error objects, strings, map/span/scanner/array stacks, Marshal/reflection/formatting/cache and other private events remain **open**. No full JSON or whole-call bound. |
| `internal/controlstore/gapdb/retainedkernel/lifetime.go`: `Check`, `VerifyConnection`, `checkLocked`, `startTicks`, `readChunks`, socket/path checks | Owned/delegated-owned and kernel-owned | `I*H` is hash-byte work only. Separate conditional slot leaf covers at most3 chunk arrays,2 Buffer objects,2 proc backing histories (including possible append-make temporary) and2 suffix-string payloads per attempted check. SHA/output, Fields/FieldsFunc, formatting/hex, file/stat/path/poll/syscall/peer objects, callbacks and other kernel/library/compiler events remain **open**. One-shot opens remove the old path-copy retry loop and add up to6 liveness callbacks/check; they do not prove full check/heap/time costs. |
| Pinned SDK `gapdb/types.go`: `NewPutMutation`, `cloneOpaqueBytes`, `Validate` | Delegated-owned | Validation value clone length covered by `D`; other validation errors/metadata/expiry allocations **open**. Provider writes/publication receipts are not benchmarked or inferred. |
| Toolchain `src/encoding/base64/base64.go`: `EncodeToString` | Delegated-owned | Encoded buffer/string logical lengths covered here; separate pinned leaf covers the named <=4096-byte roundtrip event slots. Other library/event overhead remains **open**. |
| Toolchain `encoding/json`, `reflect`, `bytes`, `strings`, `fmt`, `context`, `net`, `os`, crypto and syscall wrappers, when called in this sequence | Delegated-owned per-call work; some shared caches | Per-call buffers, objects, closures and temporary copies remain owned costs even if the library uses pools. Most await recipes as identified above. **No blanket standard-library exclusion.** |
| `net.Dialer.DialContext`, `net.UnixConn`, `context.AfterFunc`, deadlines and file-descriptor lifecycle | Delegated-owned / kernel-owned | Connection/FD/netpoll wrappers, socket buffers and kernel objects, cancellation task/goroutine state and concurrent cleanup **open**. One registry node is not the whole connection resource. |
| Go runtime allocation/GC/stack machinery | Runtime | Separate pinned leaf covers normal page/size-class rounding and append capacity for named source events only. Unlisted event rounding, stack placement/growth, compiler/ABI multiplicity, GC metadata, allocator spans and concurrency effects remain **open**, not converted to a process/heap ceiling. |
| Preexisting adapter, transport, lifetime/pidfd, SDK client, owner/projection state | Borrowed or shared baseline | Disclosed separately as preexisting for this accounting boundary. Per-call checks, wrappers and new descriptors are **not** baseline exclusions. Input ACK/immutable referents must remain alive for actual use; their baseline lifetime is not free process memory. |
| Type/JSON metadata caches, immutable schema tables, reusable pool baseline, runtime/netpoll infrastructure | Shared/runtime baseline | Not an observer-exclusive baseline allocation recipe. Newly allocated or expanded per-call state, pool borrowing and return, and attributable growth need separate accounting; cache/pool membership alone does not exclude ownership. |
| Provider process and system-wide filesystem/page caches | External/shared | Not claimed as observer-owned heap. Socket/file objects created by the observer remain owned/kernel-owned; image-read work may affect shared caches, whose residency is not bounded here. |

There is no asserted upper bound on aggregate process memory, goroutines,
concurrent observations, system cache residency, kernel socket memory, or other
callers. Existing count/work refusal ceilings are not newly introduced limits
and are not a peak-memory proof.

## Verification and evidence interpretation

The focused tests cover compiler geometry, zero/odd/maximal accepted arithmetic,
independent plus-one/invalid inputs, joint-max refusal, overflow helpers and all
input fields at `MaxUint64`, monotonicity, genuine overwrite chains and copy
isolation, and source-site drift. Assertions ensure historical occurrences and
both value copies are charged even when final rows are fewer. A second fixture
control exercises the same genuinely sealed/coordinator-published ACK used by
the benchmark. Fixture-port revision responses are not native provider receipts.

Source anchors cover the principal geometry/copy expressions, the current SDK
module pin (replacement refused), its actual resolved clone source, and the
active toolchain's `EncodeToString`. Their hashes are logged. Anchors are drift
tripwires, not a proof of all possible control flow or unlisted allocation sites;
a source change still requires review and fresh evidence.

`BenchmarkRetainedResourceAccountingGenuineACKBuilder` constructs its real ACK,
preflight and ledger **outside timing**. Timed iterations call the actual builder
and retain its result. The only custom byte metric is
`covered-source-logical-B/op`, using the builder covered terms, not transport or
private reserves. Go's normal `B/op` and `allocs/op` are **empirical cumulative
allocation** observations for that benchmark/toolchain, not peak heap, full-call
observation allocation, an acceptance threshold, or an allocation attribution
proof. A difference from the logical metric is expected: open key/codec costs,
capacity and allocator rounding, and stack/escape placement are distinct.

Focused commands (unit run once with `-count=1`; benchmark fixture is validated by
that run):

```
go test -json -count=1 -run '^TestRetainedResourceAccounting' ./internal/controlstore/gapdb
go test -json -run '^$' -bench '^BenchmarkRetainedResourceAccountingGenuineACKBuilder$' -benchtime=20x -count=1 ./internal/controlstore/gapdb
go vet ./internal/controlstore/gapdb
```

### Recorded focused run

Evidence directory:
`/home/user/.cache/spec-kitty-parity-resume-followup/approved-owned-accounting-mzi9xntd/`.

The initial canonical package unit/vet commands both failed to build because the
concurrently added, separately owned `retained_expiry_evidence_test.go:148` supplied
an SDK client where the private `provider` interface was required (`Watch`
return-type mismatch). **Zero accounting tests executed in that attempt.** No
change to that file was made for this ledger.

The accounting tests then executed **once** against the package's explicit Go
source list, excluding only that unrelated expiry test. Exact file selection is
recorded in `focused-source-selection.json`; full argv in
`focused-unit.command.json`, `focused-vet.command.json`, and
`benchmark.command.json`. This is focused accounting evidence, **not a clean
whole-package claim**. Results on `go1.27.2-X:nodwarf5 linux/amd64`:

- 8 top-level tests plus 11 subtests passed; 0 failed, 0 skipped.
- Focused source-list vet passed.
- The genuine-ACK builder benchmark passed at 20 iterations:
  `221196 covered-source-logical-B/op`, empirical `218156 B/op`, `78 allocs/op`.
  These are different quantities, not a certified inequality.

Logs are `unit.stdout` / `unit.stderr` / `unit.exit` and matching `vet.*` for the
initial build blocker, then `focused-unit.*`, `focused-vet.*`, and `benchmark.*`
for the successful focused checks. `focused-test-counts.json`, source/artifact
hashes, and preexisting-file preservation observations accompany them. Other
parallel work may change separately owned files; such changes are recorded, not
reverted or incorporated into a false custody claim. In particular,
`retained_observation.go` changed after the focused run. Results bind the source
hashes logged by that run; final verification of the parent's combined changes
is still required (`post-test-source-drift.json`).

### Parent combined pinned-toolchain follow-up

After independent scoped source approval, the parent compiled the FULL combined
package and ran the accounting/count-reason/observation selection on Go1.26.7:
unit/race each26 top-level /162 outcomes, zero failures/skips, plus vet. Source-list
exclusion and Go1.27.2 are limitations of the preserved earlier author run, not this
new parent result. Evidence:
`approved-observation-parent-TnMdOgsw/{unit,race}.jsonl`, `vet.*`.
Pinned builder benchmark20 iterations: `221196 covered-source-logical-B/op`,
empirical `217878 B/op`, `78 allocs/op`; fixture-specific, not an allocation bound.
See [the current audit](OBSERVATION_BOUNDARY_IMPLEMENTATION.md). This paragraph is
parent-owned documentation added after the independent ledger review; model code
is unchanged from that reviewed source.

### Scoped allocation-slot follow-up

[The new leaf](OBSERVER_ALLOCATION_SLOTS.md) received independent scoped approval;
parent and reviewer pinned unit/race each pass15 top-level /35 outcomes, no failures
or skips, and package vet. Exact reviewed source/doc hashes and evidence are in
[the current audit](OBSERVATION_BOUNDARY_IMPLEMENTATION.md). The new model converts
named builder/node and large transport-buffer events, not all the registry's
owned sites. Old logical tests/model and historical evidence remain unchanged.
No provider scenario or benchmark was rerun for these test-only changes.

No full observer bound can be promoted from these results until the open
owned/delegated-owned/kernel and runtime terms have defensible recipes and
verification. No provider-dependent qualification, process ceiling, or automatic
expiry hook is introduced by this accounting file.
