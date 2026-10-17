# Retained kernel buffer events — partial normal-allocator recipe

**Not full kernel-check/observer memory, peak/live heap, RSS, hard deadline,
performance or admission acceptance.** This test-only leaf covers named buffers
in `retainedkernel/lifetime.go`; the separate one-shot-open correction removes
one unbounded potential path-buffer retry sequence, not every remaining cost.

Implementation: `internal/controlstore/gapdb/retainedkernel/buffer_accounting_test.go`.
Profile: ordinary Go1.26.7/linux/amd64, without ASAN/MSAN/efence. Race runs test
correctness, not inclusion of shadow/instrumentation storage. Conditional heap
charges do not assert actual escape. Extra compiler/ABI/runtime storage remains
separately open. No product cap or public bookkeeping API is added.

## Count and work premises

`C` counts attempted `checkLocked` invocations, including failed prefixes, NOT just
successful executable hashes. A VerifyConnection contains one such attempt.
Owner/startup work is outside this observation scope. The existing observation
schedule allows at most three attempts per metered RPC plus one terminal check;
`C<=12883`. Every actual attempt must be counted even when it fails before hashing.
The arithmetic envelope also enforces verified image `0<I<=64MiB`, `C*I<=32GiB`.
Hash-byte work is never added to buffer bytes. Maximal image and maximal count are
not jointly accepted. Scalar controls are not proof of a realizable admitted call.

## Covered families per attempted check

| Named family | Conditional normal-allocator slot upper |
|---|---|
| readChunks arrays: one per proc/exe invocation, at most three | `3*32768` |
| two private bytes.Buffer objects (actual compiled size40, scan class48) | `2*48` |
| two proc-buffer backing histories, including conservative append-make temporary | `2*524288` |
| two explicit proc suffix-to-string copies, each at most64KiB | `2*65536` |
| **Subtotal of these families only** | **1278048 bytes** |

All counts include unsuccessful prefixes conservatively. `startTicks` calls
`Bytes`, which borrows its Buffer backing; it does NOT make a Buffer.String copy.
The later `string(raw[suffix:])` is a separate source conversion and is charged.
Field slice/span backing and conversion/error temporaries are different events.
Nothing here assumes ASCII proc input to bypass the Unicode FieldsFunc branch.

### Backing-history proof, not merely observed samples

`readChunks` checks the64KiB proc maximum BEFORE writing an oversize chunk. Every
accepted destination write therefore leaves total Buffer length at most `P=65536`.
The Buffer is new and write-only; off remains zero, with no read/reset/slide reuse.

Pinned `Buffer.grow/growSlice` starts capacity at least64; each later capacity
growth requests at least twice the old capacity and rounds noscan bytes upward.
An old capacity already>=P needs no growth. Otherwise a new request is at most2P;
normal allocator rounding cannot cross the page-aligned2P boundary. Thus there
are at most12 primary growth events, final capacity<=131072, and summed rounded
primary capacities<262144. This includes every discarded backing, with no GC/reuse
credit. The `append(nil, make([]byte,c)...)` source may have a separate temporary;
conservatively charge TWO backing slots per growth event, at most524288 per proc
reader. The model does not depend on compiler elision of that temporary.

This bounds the named backing arrays, not all Buffer/growSlice control state.
Defer/recover/ABI bookkeeping remains separately open. The small initial make
needs only one array; charging two in the general reserve is conservative.

The large array/copy ceilings32768/65536/131072 are runtime-page multiples with no
pointer header; their slot ceilings equal those values under this profile. Apply
header/class rounding to the Buffer object separately, not to one aggregate.
The two suffix copies are each bounded by the64KiB input, including allocator
rounding. No new product allocation ceiling is inferred from these observations.

## Verification boundary

Tests bind exact current kernel implementation, observation budget, pinned
bytes.Buffer and allocator-source hashes; assert actual compiled Buffer size;
and exercise zero/odd/maximal arithmetic, joint-max refusal and invalid/overflow
inputs with entirely zero output. Capacity-trace controls exercise one-byte and
other short reads,64KiB boundaries and oversize refusal, checking growth count,
doubling, final capacity and cumulative capacities. They are not B/op or heap
measurements; the instrumentation wrapper's own allocations are outside the model.
The algebra above, not finite samples alone, supplies the all-partition claim.

Every successful result has `fullCallCoverage=false` and missing recipes. Open
owned terms include Fields/FieldsFunc spans/header arrays, numeric parsing/errors,
SHA/FIPS objects/output, hex/formatting/pools/argument boxing, file/stat/path and
net/syscall wrappers, contexts/callbacks and extra compiler events. Finalizer/
poller/GC/stack and shared growth need their own disclosed classifications, not
blanket exclusions. Non-Unix/error branches are not silently treated as free.

The one-shot-open helper adds up to SIX liveness callbacks per attempted check;
future polling/callback recipes must include them. Read/Fstat library EINTR loops
also need source analysis; they are not declared harmless merely because this
leaf covers a bounded set of arrays. No full memory or wall-clock claim follows.

Independent source review and parent results, when recorded, are linked from
[the implementation audit](OBSERVATION_BOUNDARY_IMPLEMENTATION.md). Until then this
is an author-tested proposal; even after scoped approval, full-layer acceptance
and genuine-Host expiry qualification remain incomplete.
