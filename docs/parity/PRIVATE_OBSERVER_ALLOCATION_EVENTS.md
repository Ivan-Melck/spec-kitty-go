# Private observer allocation events — scoped, incomplete

This adds private test-only map/value-copy recipes to the
[pinned slot leaf](OBSERVER_ALLOCATION_SLOTS.md). **Not complete observer memory,
heap placement, peak/RSS, performance, Host qualification or admission.**
No product code, schema, cap, image, lifecycle or public provider API changes.

New files:

- `internal/controlstore/gapdb/retained_private_accounting_test.go`: checked
  per-event accounting using the existing reviewed normal allocator helpers.
- `internal/productionruntime/retained_private_geometry_test.go`: actual compiled
  private-record/key layout, source-bound runtime-layout mirrors, successful and
  failed-prefix controls using existing source-valid fake-reader fixtures.

The profile remains ordinary Go1.26.7/linux/amd64, without ASAN/MSAN/efence.
Race is a correctness check; instrumentation/runtime storage is separately
classified, not measured by the slot model. Unsupported compiler/target source
and geometry witnesses explicitly skip; such runs cannot qualify this profile.
Required pinned unit/race qualification must have zero skips.

## Three direct private maps

`retained_private_records.go` creates unhinted maps for rows, epochs and operations.
One invocation inserts at most195,64,64 unique entries, respectively. There are no
deletes, clears, clones or external map mutations during construction. Operations
are bounded by the pre-insertion epoch-loop guard; they cannot outnumber epochs.
Every source change requires re-audit, not implicit inheritance of this recipe.

The old4096-publication-map directory problem does **not** apply to these maps.
Pinned Swiss growth is occupancy-triggered. A small map holds eight entries; the
ninth creates a16-slot table and one-entry directory. With no tombstones, tables
admit7/8 capacity before growing. Collision probing cannot trigger earlier growth;
quadratic probing visits every group. Rows therefore reach at most256 slots;
epoch/operation maps at most128. Directory splitting requires growing past1024
slots, which these insert counts cannot reach, independently of hash seed.

Compiler layout is `{ctrl uint64; slots [8]struct{key;elem}}`. Record fields
string/slice/uint64 have offsets0/16/40 and size48. Row slots are64 bytes; groups520.
Named-string/bool slots pad to24 bytes, groups200. Keys/elements fit the128-byte
inline ceilings: no per-slot indirect key/value allocation is hidden here.
Map header48, table32 and directory pointer8 follow the pinned compiler/runtime
source. `abi.MapType` is shared type metadata, not a second per-map header.

| Event family | Rows | Epochs | Operations |
|---|---|---|---|
| Group-array requests | `520*{1,2,4,8,16,32}` | `200*{1,2,4,8,16}` | same as epochs |
| Replaced/new table requests | `5*32` | `4*32` | `4*32` |
| Conditional header | `48` | `48` | `48` |
| One-pointer directory | `8` | `8` | `8` |

Apply the existing scanned allocator recipe separately to EACH event, including
inline headers/size-class slack. Do not round45744 aggregate requested bytes as
one allocation. All discarded growth arrays/tables remain charged; no GC/reuse
credit is taken. Full envelope: **at most35 named direct objects,45744 requested
bytes,50920 covered slot-upper bytes**. These are cumulative source-event upper
charges, not an observation of35 heap allocations. Compiler stack placement or
elision can reduce actual heap; extra compiler/ABI storage is still uncovered.
Empty-prefix controls conservatively reserve all three conditional headers even
when a failed invocation never creates every map.

The companion test checks actual private-record and named-key types; runtime
map header/table checks are explicitly source-bound mirrors, not unsafe inspection
of live opaque map internals. Hash guards bind the private implementation,
compiler group/layout construction, runtime growth/fast-string paths, ABI inline
constants, current adapter and Core key type. The combined qualification must run
both packages' tests: neither only arithmetic nor only mirror sizes is enough.
These guards support the inspected functions, not proof of unread runtime paths.

## Direct value copies, including failed prefixes

`lengths` means every row passing `read`'s size/ownership/revision guards, INCLUDING
a final row whose later schema/canonical validation fails. It is not inferred only
from successfully returned complete chains. Let `X<=195` be that count and
`P<=256KiB` its aggregate bytes, each nonzero length at most4096.

Each such row has two direct defensive byte copies: adapter `convertRecord`
append and work `bytes.Clone`. Both are append-from-zero-capacity operations;
the normal byte append helper accounts for capacity/tiny/slot rounding per copy.
The pinned bytes.Clone source is authenticated, not called shared infrastructure.

A pending failing response can first incur ONE adapter conversion before private
read guards reject it. `convertRecord` rejects values above the existing4MiB
checkpoint ceiling BEFORE copying; private4KiB is not that allocation ceiling.
Reserve the maximum byte-append slot over0..4MiB, which is4MiB. Do not impose a new
cap, or charge a second private clone for that rejected response. Cancellation or
schema failure after an accepted read is covered by the prefix copies instead.
The reserve is conservative even at the zero-prefix scalar control; it does not
assert that a response was attempted or those bytes actually allocated.

```
covered-copy-slots = 2*sum(byteAppendSlot(length_j)) + 4MiB
                 <= 2*P + 2*639*X + 4MiB
                 <= 4967802 bytes
```

The639-byte slack applies per <=4096-byte noscan copy; tiny blocks count separately.
Maximal cardinality and aggregate bytes must be satisfied together, not195
independent4KiB values. This subtotal is separate from transport decode/SDK copies
and map storage; do not add old logical versions again. Expiry objects, key bytes,
JSON/canonicalization, contexts/errors and other fields are NOT paid for here.

## Evidence and remaining work

Arithmetic tests cover occupancy boundaries0/1/8/9/14/15/28/29/56/57/112/113/195,
all maxima, monotone prefixes, invalid/overflow/refusal with zero output, per-copy
tiny/slack and terminal reserve boundaries. Source-valid fake-reader fixtures
exercise1/2/64 epochs plus early and late refusal prefixes; they are not provider
I/O, real publication receipts or native Host-admission evidence.

Both ledgers always report `fullCallCoverage=false` with named missing recipes.
Key/string backing, JSON/canonicalization/decoder storage, errors, contexts/timers,
hashing setup, callbacks/connections/kernel checks, other library events and extra
compiler/ABI storage remain open. Shared/runtime baselines are separately
disclosed; no blanket standard-library exclusion is introduced.

Independent scoped review and parent results, when available, are recorded in
[the current implementation audit](OBSERVATION_BOUNDARY_IMPLEMENTATION.md).
Until recorded these additions are author-tested models, not reviewed acceptance.
Even after a scoped approval, the full resource/Host/negative/performance/P95 and
layer acceptance rows remain incomplete. Prior receipts/reviews/images stay intact.
