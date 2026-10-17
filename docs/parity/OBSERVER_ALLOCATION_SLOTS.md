# Observer allocation slots — pinned, partial leaf recipes

**INCOMPLETE observer accounting. Not a whole-call bound, memory acceptance,
production qualification, peak/live heap, performance/P95, or RSS claim.**
This extends, and does not replace, the [logical ledger](OBSERVER_OWNED_ACCOUNTING.md)
and [approved reporting boundary](APPROVED_OBSERVATION_BOUNDARIES.md).

Implementation: `internal/controlstore/gapdb/retained_allocator_accounting_test.go`.
Only private test code and documentation change. No product cap, admission,
provider image, authority record, runtime API or observation result is changed.

## Profile, units and coverage

The model describes **cumulative covered source-event allocation-slot upper
bytes**, including in-slot headers/internal slack, for the inspected ordinary
Go1.26.7 linux/amd64 allocator. One conditional heap instance is charged per
named explicit source event; compiler-eliminated/stack-placed objects can cost
zero actual heap. This is NOT proof that the fixed ACK/order arrays both escape,
nor a complete count of compiler-generated temporaries, ABI copies or escapes.
Additional object/pointee allocations remain separate open events.

ASAN/MSAN redzones and debug.efence are outside this profile. Race execution is
a correctness check, not measurement or inclusion of shadow/instrumentation
storage. Runtime pages are 8192 bytes, not OS pages. Spans, GC metadata, stacks,
shared/cache expansion and kernel storage require separately disclosed
classification; per-call owned library objects are NOT shared exclusions.
Applying a model to an actual Host still requires binding its build/runtime
profile and completing the event registry. Arithmetic-only source-specific tests
are not an admission or executable-configuration certificate.

Every successful leaf ledger keeps `fullCallCoverage=false` and a nonempty,
immutable missing-recipes disclosure. Remaining owned sites include key/codec/
scanner/private maps, state/context/connection/callback/kernel-check objects and
compiler-generated extra events; see the original source-site registry. The new
leaf covers only the sites named below, not an entire function or observer call.

The helper's 64 MiB per-request arithmetic domain covers the enumerated bounded
source objects. It is **not** a new product ceiling or authority to widen one.
Invalid/out-of-domain inputs return entirely zero results. Checked products/sums
are used; envelope bounds precede the remaining small integer operations.

## Ordinary allocation and append recipes

Let `B(q,scan)` be the conservative ordinary allocation-slot charge for a request
of `q` bytes; zero requests charge zero. For `q<=32760`, use the pinned small-size
classes and add an eight-byte header exactly when `scan && q>512` before class
selection. For `q>32760`, round to an 8192-byte page, without an inline header.
Noscan requests `1..15` conservatively charge a whole 16-byte tiny block PER event,
even if actual tiny packing/reuse reduces aggregate allocations.

**Growth payload is not that slot charge.** `roundupsize(1,noscan)` exposes eight
payload bytes, whereas this conservative tiny slot charge is sixteen. A scanned
513-byte request exposes 568 bytes and occupies a 576-byte slot. A scanned
8192-byte order array occupies a 9472-byte slot if heap allocated.

For one bulk append from nil (not repeated incremental append), first round
`count*elementSize` to the runtime's available growth payload; floor by element
size to obtain capacity; then round the actual `capacity*elementSize` allocation
request again. A 256-record append with 360-byte scanned elements obtains capacity
273, requests 98280 bytes and occupies a 98304-byte slot. `make` instead allocates
its specified capacity directly; it does not expose spare class capacity.

For all requests in this profile, `B(q,scan)<=q+8191`. For noscan requests at most
4096 bytes, the tighter slack is 639 bytes. Tests enumerate all small requests,
both scan kinds, generated-table indexed reference behavior, threshold/page
boundaries, refusal cases and actual pinned-runtime append capacities. Zero-byte
backings, non-power-of-two elements and tiny allocation/growth differences are
explicit controls.

## Covered builder and registry events

Write `m_i` for each plan's record count, `v_j` for every occurrence's canonical
length, `r=sum(m_i)`, `V=sum(v_j)`, and `A` for attempted RPCs. Every plan is counted
separately; overwritten occurrences remain present. `R(m)` and `C(v)` denote the
append-from-nil slot recipe for 360-byte scanned records and noscan bytes.

```
covered builder/node slots = B(204800,scan) + B(8192,scan)
                          + 2*B(368*r,scan)
                          + sum_i R(m_i) + 2*sum_j C(v_j) + 32*A
```

This covers the fixed ACK snapshot/order source objects, two occurrence arrays,
per-plan `Records()` backings, both defensive canonical-byte copies and fixed
intrusive connection nodes. Node pointees are NOT included. It is not a claim
that those are all builder or connection allocations. Borrowed immutable ACK
referents are not copied by header storage.

A distribution-independent upper for these same events is:

```
214272 + 736*r + 360*r + 2*V + 32*A
       + 8191 * (n + 2*r + (2 if r>0 else 0))
```

It sums rounding slack PER event, not by rounding aggregate `V`/record storage.
The scalar envelope is the existing `n<=256`, `r<=4096`, `V<=32MiB`,
`A<=r+198`, with nonempty per-plan counts and one nonzero bounded value length per
occurrence. This is structural arithmetic, not ACK validation or realizability
of every tuple. Genuine coordinator-minted overwrite history separately checks
that final-row compaction cannot discard earlier copy charges.

At the numeric control `n=16, m_i=256, r=4096, v_j=8192, A=4294`, covered slots
are 72048064 bytes. **This is not a full observer/builder heap bound.** No provider
workload or performance acceptance is inferred from that scalar control.

## Covered large transport buffers

`F` is the existing never-refunded charged response length, including malformed
or oversized declared lengths. Each metered exchange reads at most one frame;
its get path creates at most one decoded backing and one SDK validation clone.
A refused length can allocate nothing; using `F` overstates rather than refunds
it. Bound active nonempty responses by `N=min(A,F)` (byte units are integral).

Decoded backing lengths, INCLUDING buffers allocated before terminal decode
failure, sum to `D<=floor(3F/4)`. Do not infer `D` only from successful returned
values. This new envelope uses the upper derived from `F`, not an unverified
post-success measurement. All single allocations fit the leaf profile.

Base64 flushes use at most 4096 characters each. Total flush count
`C<=floor(F/4096)+N`; escapes only enlarge raw JSON relative to canonical character
count. `EncodeToString` creates an encoded byte buffer and returned string;
`string(part)` is conservatively charged as a third buffer even if optimized
away. Each request is at most 4096 bytes, so 639 bytes/event covers slot slack.
A decoder's fixed 4096-byte chunk is conservatively charged once as a conditional
heap object; stack placement can reduce actual heap.

```
frame payload slots       <= F + 8191*N
Base64 decoded slots      <= floor(3F/4) + 8191*N
SDK validation clone slots<= floor(3F/4) + 8191*N
roundtrip slots           <= 3*F + 3*639*C
conditional chunk slots   <= 4096*N
```

These five terms replace, not add again to, their corresponding logical-length
terms. Decoder closures/scalars, escape parsing, scanner/JSON nodes, reflection,
SDK error objects, requests, contexts and connection graphs remain open owned
costs. A 32-byte registry node is not a connection resource bound.

For the numeric control `F=64MiB, r=4096, A=4294`, this large-buffer subtotal is
at most 531843164 slot bytes, including conservative per-event reserves. It is
cumulative scoped accounting, NOT simultaneous live storage or a memory cap.
`F=0` produces zero for these large-buffer families even with attempted RPCs;
connections and other failed-dial costs remain outside this leaf, not zero.

## Source binding and evidence policy

The pinned-source test verifies SHA256 of allocator sizing, malloc paths, large
allocation page rounding, slice growth, GC constants/generated class tables and
Base64 encoding source. It compares the model with an independently indexed
reference to the actual generated class tables. Hashes are custody/drift guards,
not a claim to have proved every runtime path. The prior logical-ledger source
checks also bind actual current copy expressions and the resolved pinned SDK
clone implementation. The complete package compiles; no source-list exclusions
or provider execution are needed for this slice.

Runtime-capacity/source witnesses explicitly skip on a different compiler/target;
that result cannot qualify this pinned profile. Required qualification commands
use `GOTOOLCHAIN=go1.26.7`, `TMPDIR=/home/user/.cache/spktmp`, `-count=1`, and must
have zero skips. Unit/race run the existing `TestRetainedResourceAccounting` and
new `TestRetainedAllocator` tests; vet remains package-scoped. No B/op inequality,
full observation performance result, new image execution or receipt transfer is
inferred. The unchanged provider scenarios need not be rerun for test-only math.

Independent source review and final parent execution evidence are recorded
separately; until recorded, this addition is a tested author model, not a reviewed
or accepted memory deliverable. Full-call coverage remains false in every case.
