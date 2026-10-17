# Private JSON allocation investigation — research only

Status: **not implemented or independently reviewed acceptance**. Existing private
map/copy and kernel-buffer leaves remain partial. No Core code, JSON policy,
admission, depth cap or provider API was changed by this investigation.

Read-only note, read ranges and source SHA manifest:
`/home/user/.cache/private-json-research-KhTWDIy3/`.
Parent follow-up/build-profile receipt:
`/home/user/.cache/spec-kitty-parity-resume-followup/private-geometry-parent-4hxso1he/json-research-parent-followup.json`.
Pinned `go list -json encoding/json` selects the old `decode.go`/`encode.go`
implementation, not the experimental JSONv2 branch. That is configuration evidence,
not allocation qualification.

## Candidate recipes to verify next

The research traces at most65 strict decoders,260 Core canonicalization entries,
66 explicit Marshal entries and129 formatted helpers for64 epochs. These are
candidate call-path ceilings including terminating failures, not runtime counters
or independently approved per-call recipes. One decoder's second Decode shares
its existing buffer. Reconstructed trusted values are additional inputs; they
must not silently inherit the accepted-provider byte sum.

For accepted input<=4096 bytes, decoder refill requests are512/1536/3584/7680,
summing13312 REQUESTED bytes per decoder, not allocator slots. Each make has an
explicit capacity: do not use its rounded physical slot as the next Go capacity.
The input guards cover bad/trailing prefixes too. Trusted binding validation has
a separately derived encoded-length candidate bound722, not an assumption that
owner metadata is an untrusted4KiB row. These claims still need source-bound
ledger tests and independent review before resource acceptance.

## A property bound is not a universal map bound

The grammar argument gives at most819 properties for a complete4KiB JSON object.
It rules out directory splitting for the specifically traced fresh, insertion-only
Core object/result/seen and trailing-decoder object maps, provided their actual
construction/prefix behavior matches the source premises. It does NOT bound every
map in JSON/library code by819.

Generic Core canonicalization can Marshal a deeply nested array/object tree.
Above depth1000 the encoder's `ptrSeen map[any]struct{}` tracks active containers,
including arrays: a tree of2045 compound levels has1045 such candidate entries,
exceeding896 occupancy in a1024-capacity Swiss table. This refutes the blanket
property-count argument. No pathological seed or running-memory exploit was
constructed. The private concrete schemas reject that nested input before
Marshal; trailing-any rejection does not Marshal it. Reachable private trees are
shallow, but fresh encoder-state costs and historically retained pooled storage
still need their own ownership/disclosure treatment.

The research's unspaced example has a minor length error: `{"":` followed by2044
`[` bytes, `0`,2044 `]` bytes and `}` is4094 bytes, not4095. Parent counted the
constructed bytes without executing Go/Core; the below4096 argument is unchanged.
The original note is preserved, not rewritten.

## Remaining verification

Round per allocation and preserve every discarded/overwritten occurrence. Close
array/span/scanner stacks (malformed open prefixes differ from valid-tree depth),
unquote/UTF8 expansion, numeric-error/formatting/reflect/interface costs, Marshal
sorting/output/append scratch, and cold/racing cache construction. Do not exclude
owned buffers merely because they are inside stdlib. Separately disclose borrowed
pool capacity and shared/runtime/stack growth; pool history is not an owned-zero
certificate. Source analysis must distinguish the concrete private path from
generic Core before importing a bound or proposing a policy change.

No B/op, actual escape, simultaneous live/peak heap, RSS, hard deadline, P95,
production-Host expiry qualification or full observer memory bound follows.
