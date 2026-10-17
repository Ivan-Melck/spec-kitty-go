# Private JSON events — conditional, partial source ledger

Status: **author-tested; independent review pending**. No full observer memory,
actual heap-placement, live/peak/RSS, P95, Host expiry or admission acceptance.
No product code/cap/API changes. Scope is ONLY JSON work reached from the private
metadata helper, not transport/status JSON or generic Core callers.

Implementation: `internal/controlstore/gapdb/retained_json_accounting_test.go`;
actual-schema/prefix companion:
`internal/productionruntime/retained_private_json_accounting_test.go`.
Normal Go1.26.7/linux/amd64 allocator and classic encoding/json, no ASAN/MSAN/efence.
Race runs establish correctness, not inclusion of shadow/runtime instrumentation.
The prior reviewed allocator leaf supplies per-event class/header/tiny rounding.

## Source entry counts and input premises

For E<=64, strict decoders: current plus E wrappers, at most65. Second Decode
uses that SAME decoder. Core canonicalization: binding, current input/current
reconstruction/head, then wrapper input/reconstruction/operation/event per epoch:
`4+4E<=260`. Failures terminate this sequence; count every entry already reached.

Untrusted strict inputs passed `read` and decode's4096-byte guards; their cumulative
length is<=256KiB (a subset of accepted private rows). Reconstructed inputs do NOT
inherit this budget: they are separately bounded by concrete validated schemas.
Observe validates database/Host/operation identities before binding.canonical;
its digest/protocol/schema checks occur before Marshal. ASCII identities<=128,
fixed64-hex digests, two20-digit protocols and fixed JSON grammar give binding722.
Validated current/wrapper have maxima737/768; formatted head/operation/event
97/164/187. All are<4096. Size witnesses and absence of custom JSON/Text hooks
supplement these source premises; witnesses alone do not prove the bounds.
Binding.canonical in arbitrary other callers is NOT independently bounded here.

The scalar ledger lists lengths at actual strict/Core ENTRIES. It conservatively
reserves a decoder, reader, Valid scanner and fast-copy candidate at EVERY Core
entry, even when that branch creates none. It does not claim these branches or
maxima execute simultaneously, nor that every scalar input is a realizable Host
call. Core fallback outputs can expand beyond4096 and are NOT covered by this
input/fast-copy recipe. Marshal outputs/scratch remain explicit open costs.

## EOF-safe decoder backing history

Pinned refill uses `make([]byte,len,2*cap+512)`. Its Go capacities are exactly
512,1536,3584,7680—not the physical allocator classes. Every buffered byte came
from the one bounded bytes.Reader; compaction can only reduce the buffered length.
Once capacity>=N+512, free space>=512 at every refill and no further growth occurs.
This holds across Decode/Token calls, consumed-prefix slides, malformed data and
EOF. Critically a one-byte scalar or whitespace prefix can need a SECOND buffer
at EOF: the minRead test occurs BEFORE the reader reports EOF. N<=512 is not a
proof that only512 bytes were requested.

| Input length N | Conservative request history | Requested sum | Slot sum |
|---|---|---:|---:|
| 1..1024 | 512,1536 | 2048 | 2048 |
| 1025..3072 | above +3584 | 5632 | 6144 |
| 3073..4096 | above +7680 | 13312 | 14336 |

Round each discarded backing independently. The3584 request occupies a4096 slot;
its Go capacity stays3584, so the next request is7680, not8704. The Slot sum is
not simultaneous retained storage or empirical B/op. The derivation covers the
make backing events, not extra compiler/ABI temporaries.

## Other named families

- One conditional Decoder object304 bytes -> scanned320 slot, and one bytes.Reader
  object40 -> scanned48, per reserved decoder. Reader input bytes are borrowed
  references to copies already charged elsewhere, not a new payload clone.
- One conditional pooled Valid scanner header64 -> scanned64 per Core entry.
  This is separate from the two scanner headers embedded in Decoder304. Scanner
  stacks/pointees, pool misses/growth/retained backing and runtime bookkeeping are
  additional costs, not hidden in64 or treated as free stdlib infrastructure.
- One append-from-nil byte backing per Core entry for its canonical FAST-path copy,
  using actual nil-append capacity/per-event rounding. Fallback Marshal is a
  different branch whose outputs are still uncovered; reserving a fast copy on
  fallback is conservative but does not pay for those outputs.

At65 strict lengths3073 and260 Core lengths4096 (a conservative scalar reserve,
not a real call), the four-family subtotal is5860400 conditional slot-upper bytes:
4659200 decoder backings +119600 decoder/reader objects +16640 scanner headers
+1064960 possible fast copies. All successful ledgers have fullCallCoverage=false
and immutable nonempty missing-recipes text. Invalid lengths/counts/accepted-byte
sums, including MaxUint64, return an entirely zero ledger. Bounded domains prove
all internal sums are below6MiB; no unbounded arithmetic is multiplied first.

## Controls and limits

Actual Decoder controls trace public Reader windows and InputOffset during refill;
unread delivered bytes plus read-window length equals the current Go capacity.
No unsafe private-buffer introspection or heap measurement. Whitespace EOF controls
reach the request ceilings; Decode/Token, trailing/duplicate/escaped/malformed
corpora corroborate compaction/event limits. Source hashes and actual compiled
object sizes bind the current implementation; active GoFiles confirm classic JSON.
Unsupported profile skips cannot qualify this leaf. Command/build settings and
review source custody must accompany results; drift tripwires alone are not proof
against arbitrary overlays, altered toolchains or unlisted allocation sites.

A real193-row source-valid fake-reader prefix exercises canonical case aliases:
the last wrapper's typed decode and Core input canonicalization succeed, then
reconstruction differs and refuses. Its escaped alias takes the fallback path;
reconstruction also falls back. Therefore healthy-path fallback counts must not
silently replace failed-prefix counts. No actual fallback-count telemetry, native
provider receipt or allocation measurement is claimed by this fixture.

Still open: schema/target/error objects; scanner/token/span/array/map backings;
unquote/UTF8/folding/numbers/strings/reflection/boxing; Marshal state, buffer/output,
sorting and append scratch; formatting/keys; caches/pools, context/connection/kernel
and additional compiler/library events. Shared/runtime/stack costs require separate
classification. The generic deep-tree cycle-map issue in
[the investigation](PRIVATE_JSON_ALLOCATION_INVESTIGATION.md) is not imported as a
private-path bound or fixed by this leaf. Complete owned accounting stays open.
