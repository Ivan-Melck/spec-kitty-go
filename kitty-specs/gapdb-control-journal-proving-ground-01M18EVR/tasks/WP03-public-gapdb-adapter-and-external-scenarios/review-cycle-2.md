---
affected_files:
- internal/controlstore/gapdb/adapter.go
- internal/controlstore/gapdb/adapter_test.go
cycle_number: 2
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command: go test ./internal/controlstore/gapdb -count=1
reviewed_at: '2026-08-30T13:07:04Z'
reviewer_agent: renata-wp03-cycle2
wp_id: WP03
---

# WP03 review feedback — cycle 2

Verdict: changes requested.

Reviewer: `renata-wp03-cycle2` (independent of implementer `codex-wp03`)

Implementation reviewed: `7258aa2`

## Blocking finding

### 1. The committed wire-budget test does not protect the publication path

Severity: high  
Location: `internal/controlstore/gapdb/adapter.go:214-226`, `adapter_test.go:77-90`, `adapter_test.go:165-208`

Cycle one explicitly required committed exact/plus-one evidence for the 12 MiB
wire budget before any provider call. `TestProviderBatchAndWireCeilingsAreInclusive`
now invokes `validateWireFrameSize` directly, but no test sends an encoded batch
through `Adapter.AtomicBatch` at that boundary or otherwise proves that the
adapter actually invokes the guard.

The required deletion test reproduced the gap:

1. Temporarily replace the `validateWireFrameSize(len(encoded))` branch in
   `AtomicBatch` with `_ = encoded`.
2. Run `go test ./internal/controlstore/gapdb -count=1`.
3. The complete package remains green.

The temporary change was restored. This is not merely a missing assertion on a
test helper: FR-014 and NFR-011 require 100% of over-budget commands to refuse
before publication, while the committed suite cannot detect removal of that
refusal from the real adapter path.

Resolve the mismatch explicitly. Provide a behavioral or mutant control that
fails when `AtomicBatch` no longer enforces the wire budget, and exercise the
same encoding/validation seam used for publication at exact and plus one. The
current 6 MiB raw limit appears to cap reachable JSON frames at roughly 8.4 MiB,
so if 12 MiB is intentionally unreachable for the admitted codec, preserve a
proof of that implication and route the exact/plus-one guard through a testable
publication-boundary seam rather than treating a standalone helper test as
adapter evidence. If the ratified exact-boundary requirement is impossible by
design, return that inconsistency to planning instead of silently weakening the
test.

## Prior findings independently rechecked and closed

- Non-durable atomic acknowledgement: refused as malformed after exactly one
  provider observation; no success result escapes.
- Single-mutation `MutationCount=0` and `MutationCount=2`: refused across put,
  put-if-absent, compare-and-swap, and delete.
- Nested, prefixed, suffixed, and pairwise-crossed authority keys: refused before
  any provider call for all four authority namespaces.
- Scan accounting: exact provider key/value + 32-byte record envelope + optional
  8-byte expiry overhead passes; plus one repages/refuses correctly.
- External scan: the admitted daemon produced a real stale cursor after revision
  advancement; restart from an empty cursor returned the exact sorted keyset at
  one observed revision.

## Evidence that passed

- Exact admitted daemon SHA-256
  `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`
  and size `5300621`.
- Real external qualification: exact 14/14 passed normally and under the race
  detector with fresh absolute data roots and `qualification=true`; all daemons,
  sockets, proxies, and data roots terminated cleanly.
- Acknowledgement/durable-through mismatch probes, 256/257 validator bounds,
  every mutating transport route, one-call behavior, original OperationID, and
  recovery action passed.
- Package tests, race detector, bounded fuzz (167,790 executions), full
  architecture suite and mutant controls, `go mod verify`, dependency/import
  census, vet, staticcheck, gofmt, and `git diff --check` passed.
- The public module resolves from VCS hash
  `ff3dd478f87c6b4cbb1dcc168562f99879ba8c25` with the admitted module and
  go.mod sums; only `github.com/spec-kitty/gapdb/gapdb` enters this package's
  non-standard dependency closure.

## Anti-pattern checklist

1. Dead code: N/A — this deliberately staged adapter is the declared dependency
   of later M1 work and is exercised through the real public client/daemon.
2. Synthetic-fixture test: FAIL for the wire refusal evidence; the standalone
   helper test survives deletion of the production call site.
3. Silent empty return: PASS.
4. FR coverage: FAIL for FR-014/NFR-011 regression protection at the wire
   publication boundary.
5. Frozen surface: PASS.
6. Locked decision: PASS in current behavior; the committed evidence does not
   protect the decision from deletion.
7. Shared-file ownership: PASS; module files remain within WP03's sole ownership.
8. Production fragility: N/A; no new fail-open panic path was found.
