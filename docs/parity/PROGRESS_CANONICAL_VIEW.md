# Single-pass owned progress canonicalization

Status: development implementation, scoped source review and controlled mutants
complete. Fresh source-bound critical race and M1 focused/fault checks passed
on clean candidate `41c20b14a31a9241833c39b0e3c4b6d652d7e136`.

## Immutable failure

Candidate `a0dbe69961cc5f00a17db4f2b93c1e9bf27f18c1`, tree
`6f261fefbc0d3e95c7a004fc8efda2caf7688d51`, passed its once-only M1
focused/fault run, but its separate critical race run failed
`TestProgressTenThousandQueriesRemainMemoryOnlyAndBelowP95Budget`: p95
2.214526 ms against the unchanged 2 ms limit. Completed critical verbose output
contains three skips; M1 contains one. Preserve both logs/result JSONs under
`/tmp/spec-kitty-clean-candidate-readonly-*`. No unchanged-source retry,
threshold relaxation or skip-to-green follows from the M1 pass.

## Structural reduction, exact historical preimage

The former progress digest marshaled an owned DTO, decoded it with `UseNumber`,
then marshaled its sorted maps. Private typed views now declare every public JSON
field in lexical tag order, including all nested progress items/actions/counts/
running authority. One standard marshal produces the same historical canonical
bytes. Opaque durable states remain base64 byte slices; no business JSON is
parsed, filtered or recursively normalized. Integer types, default escaping,
array order/multiplicity, nil/empty and `omitempty` semantics remain exact.

Views allocate bounded element headers, not payload copies. Query-owned defensive
copies, budget-before-copy classification and the whole-snapshot 16 MiB limit
remain unchanged. There is no revision cache or admission/authority mutation.
The `spec-kitty.reducer-state.v2` + NUL hash domain and 64-byte digest reserve
remain unchanged; durable 256 KiB limits and codecs are not widened.

## Controls and limits

An owned reflection-only test requires complete public/private field/type/tag
bijection and lexical tag order. Per-field conversion tests compare against the
unchanged test-only legacy `UseNumber` oracle. Zero/nil/empty/max-uint64,
escaping, opaque unknown business bytes and multidestination multiplicity retain
exact preimages. Source overlays that compile fail when a field/mapping is
removed, ordering changes, integers round or the digest domain changes. Original
code passes the existing regression subset; current unit/race/vet pass. Peer
source review found no concrete correctness blocker.

One count=1 same-fixture diagnostic benchmark (not p95 or qualification):

| Items | Legacy ns/op; B/op; allocs/op | New ns/op; B/op; allocs/op |
|---|---|---|
| 64 | 642224; 439107; 5113 | 80199; 76438; 8 |
| 10000 | 90333474; 76872602; 780197 | 11136553; 12953745; 14 |

Logs: `/tmp/progress-canonical-optimization/benchmark.log` and `controls/`.
Fresh reviewed-only candidate `41c20b14a31a9241833c39b0e3c4b6d652d7e136`,
tree `f81c9201998c7a96fdac4dbd5809fc2a3de523ee`, passed its once-only critical
race run in 34.887 seconds, including the original 2 ms p95 assertion. Its
once-only M1 focused/fault run passed in 699.697 seconds (eight × 100 attempts).
Completed verbose output establishes three critical skips (Hubnot public
acceptance/performance and compiled tracker lifecycle) and one M1 ordered-crown
opt-in skip. No timeout or retry occurred; source/tree, cleanliness, Go 1.26.7
and all three exact executable hashes remained unchanged before/after each run.
Parent independently checked the receipts against current source/image bytes.

Logs/results: `/tmp/spec-kitty-clean-candidate-canonical-{critical-race,m1}.{log,result.json}`.
These are new source-bound scoped passes, not transferred a0/5e9 evidence.
Full restart outcome equivalence, public API/workflow parity and release
qualification remain incomplete; this candidate remains unchanged.
