# Observer-only deterministic storage geometry — proposed next slice

Status: **Conditionally design-reviewed; parent permits only the exact seven-file
implementation slice after the clarifications below. NOT resource/layer acceptance.** Production observation remains incomplete. Preserve architect Op
01M4V7BWB53R4CK18V28SFDD8A, main/clean/source/module/images/receipts/coordination authority.
No provider/API/image change, new raw key/selector/callback/ID factory, product-limit
change, promotion/admission or weakened expiry witness is authorized.

## Evidence and premise

Host observer and ACK snapshot correction are scoped-reviewed; parent unchanged-image
functional controls pass2 top/4 outcomes, zero skips. Latest exclusive functional
checkpoint is `host-observation-functional-checkpoint/manifest.json` (51 source/docs,
1117 evidence), not selected-layer qualification.

Resource auditor d74098e3-9ddb-4ab fully read11 invocation implementation files. Audit and
coverage live in `host-retained-resource-review/run-20261014T175353Z/`; parent verified
all before/after source hashes. The audit identifies:

1. Decoded response allocation can approach9MiB inside a12MiB frame: pinned SDK
   HardMaxValueBytes is64MiB, then NewPutMutation validation clones decoded bytes. This
   is transient accounting, NOT widening accepted4MiB checkpoint/private ceilings.
2. Expected publication Go map can split its full1024-slot tables and repeatedly grow
   its directory on skewed hash prefixes. Cardinality<=4096 and make occurrence hint
   do NOT establish deterministic directory/table storage. No pathological seed,
   lawful triggering fixture or exploit was demonstrated; this is a bound gap.

Planner2f228a16-bad7-4c1 independently attempted refutation by reading pinned Go1.26.7
construction/generic insertion/compiler dispatch/splitting/installDirectory paths.
Refutation failed: r=4096 hint gives8 initial1024-slot tables, each896-entry growth
allowance; same successive hash-prefix bits can repeatedly split/retry without increasing
row count. Closed codecs/opaque ACKs do not promise hash distribution. Runtime OOM isn't
an observation budget refusal. Planner recommends bounded batch merge/collapse.

## Desired closed geometry A: publication occurrences and final dense rows

Change ONLY private observer storage, not the sealed codecs/ACKs or high/lower public API.

- Preserve the fixed[256] opaque ACK VALUE snapshot BEFORE any context callbacks and
  ALL existing scalar/capture/image-work preflight before variable materialization.
- Materialize at most r charged occurrences into one exact-capacity array, plus a
  same-capacity scratch array; fill by index and verify exact final fill count. No
  append growth, third final-row backing or standard map on this path.
  Each occurrence stores encoded key, exact owned canonical bytes, provider revision,
  sealed role/full target, condition kind and actual observed predecessor.
- Keep every existing source codec/provider-condition, value-class/key/value ceiling,
  witness publication-ID and ALL-ten-role private namespace checks unchanged. Values
  are copied exactly, never normalized/re-encoded. Charge every occurrence including
  overwritten values; assertions stay work, not retained rows.
- Implement private bottom-up mergesort by (encoded key, provider revision), with ONE
  lexical key comparison per merge decision. Compact whichever buffer contains sorted
  records: no odd-pass final copy (which would add r assignments). No generic comparator
  callback or new caller surface. Check cancellation before comparisons and
  block copies; do not introduce uncancellable unbounded runtime insertion loops.
- For each equal-key chain: exact same role AND full target; strictly increasing
  revisions (ties refuse); first conditionAbsent/predecessor0; immutable roles exactly
  one occurrence; mutable successors conditionRecordRevision exactly equal previous
  collected occurrence's actual revision. Unsupported conditions, aliases, uncollected /
  wrong-component / wrong-revision / decreased predecessors refuse. Sorting doesn't
  license immutable recreation or collapse aliases/deletions. Per-key grouping preserves
  the existing global chronological law because no law consumes another key's value.
- Use ONE equal-key boundary scan and reuse grouping decisions for validation and
  compaction; a second scan would add max(0,r-1) comparisons. Validate ALL chains before
  publishing (internal compaction may proceed, but errors return no partial storage).
  Compact each chain's final occurrence into dense owned final rows; clear discarded
  tail AND the entire other/scratch array's references. On cancellation /
  refusal return no partial storage. Exact all-role membership isn't historical writer /
  producer-DB/eligibility proof.
- Session ReadPublications ranges EVERY dense final row through original owned transport;
  cardinality is actual final-row length. maxProviderRevision remains largest collected
  immutable witness revision, NOT whole database R. S0/private helper/S1-only count/S2,
  frozen Host/projection close and terminal image check remain byte-for-byte semantic
  contracts. No refresh/retry/SDK fallback/barrier/scan/public factory.

### Checkable storage/work formulas, not a heap acceptance cap

r<=4096, V<=32MiB occurrence canonical values, K<=4MiB key charge,
B=MaxKeyBytes1024, L=ceil(log2(max(1,r)))<=12, S=sizeof occurrence.

- Two arrays exact capacity r:2rS; fixed ACK snapshot204800 once plus fixed order metadata.
- Merge comparisons<=rL; key-byte examinations<=2BrL; assignments<=rL.
- Collapse comparisons<=max(0,r-1), examinations<=2B*max(0,r-1); compaction plus
  clearing discarded tail and entire other buffer<=2r assignments.
- Payload-copy work remains2V (Records clone then CanonicalValue clone), not2*final values.
  Sorting/collapse only copies fixed records/string/slice headers, not payload bytes.
- Record slice backing and codec temporaries need separate pinned allocator rounding
  terms. No128MiB peak/RSS/hard-wall claim and no mutually guaranteed work-cap acceptance.

Keep a test-only independent Go map oracle OUTSIDE observer allocation scope. Adapt
private-type assertions honestly; never make oracle call the production collapse law.
Historical R1 source/cache overlays/red logs remain untouched even if old private-type
probes become incompatible. Fresh controls preserve genuine Core ACK factory authority.

## Desired closed geometry B: active connection bookkeeping

The planner flags the existing shared transport.active Go map as another potentially
large/history-dependent term. Address it in the same bounded-geometry slice without
changing concurrency/admission/transport semantics or choosing a new arbitrary cap:

- Replace private map[*net.UnixConn]struct{} with private intrusive doubly-linked active
  nodes under the existing transport mutex. One fixed node per actual registered
  connection, no hash/directory/slice growth or historical-capacity retention.
- Registration still checks closed, after real dial and before actual RPC/precheck.
  Unlink that exact node once in deferred cleanup, including decode/cancel/attribution
  failure. Maintain list integrity entirely under the mutex; no raw node/conn escapes.
- Close BEGIN remains terminal: set closed and close every currently registered actual
  connection. Clear EACH node's linked/prev/next state, not only head; later deferred
  unlink of detached nodes must be harmless. Double Close is harmless. Lifetime and
  actual connection inspection remain OUTSIDE transport.mu; preserve postcheck ordering.
- Context AfterFunc cancellation still closes the actual connection; no attribution
  check, cancellation/failure cleanup, Stop-BEGIN revocation or RPC-budget attempt may
  be weakened. SDK writes/unattributed snapshot delegation untouched.
- No concurrency cap is added: A budgeted attempts imply at most A allocated registry
  nodes per invocation. Concurrent invocations' nodes sum; shared transport locking
  still affects latency. This is not a global process-memory or command-admission limit.
  AfterFunc stopping doesn't join pending callbacks; keep their separate <=A ledger.

Tests: concurrent registration/unlink/Close, double/failed/cancelled Close, no leaked
registered node/socket, wrong-owner/dead/revoked/closed transport refusals, attempts charged
before endpoint, private child-context frame charging, actual fresh peer pre/post controls.
Adapt current private len(active) assertion to exact registry state, not weaker cleanup.

## Exact allowed implementation scope, subject to design review

- internal/controlstore/gapdb/retained_publication_map.go
- internal/controlstore/gapdb/retained_observation.go
- internal/controlstore/gapdb/retained_publication_map_test.go
- internal/controlstore/gapdb/retained_observation_test.go
- internal/controlstore/gapdb/retained_observation_budget_test.go
- internal/controlstore/gapdb/retained_transport.go
- internal/controlstore/gapdb/retained_transport_test.go

Independent design review4ea62d2c-a8b2-474 fully read all seven allowed files/tests,
confirmed15 inventoried Go files/handoff unchanged, and found no A/B semantic counterexample.
Required clarifications are incorporated above: zero comparison boundary, exact indexed
arrays/fill count, one lexical merge comparison, no odd-pass final copy, single grouping
scan, full tail/scratch clearing, complete node detachment/idempotent deferred unlink and
lifetime inspections outside transport.mu. Exact registry-integrity and actual socket
closure tests must not weaken the old len(active)==0 assertion. Review evidence:
`retained-storage-design-review/run-20261014T182304446366Z/REVIEW.md`.

No other production/test/docs/provider/runtime module files change. Parent owns docs.
Source snapshot/patch/evidence must precede edits; previous positive/red/reviews remain
separate. Need broader scope? Stop with actual source conflict, not silent expansion.

Tests must preserve every previous role/namespace/predecessor/capture/ACK-snapshot /
coherent count/frozen composition/nonleaking budget control. New exact4096/+1 capacity,
zero/one/non-power-of-two, reverse/long-prefix/tie/alias/immutable/predecessor controls;
reachable cancellation during materialization/merge/collapse; original canonical bytes
and input ACK order unchanged; no variable clones before preflight, no growth after exact
array allocation. Genuine4096-occurrence pure builder accepted within existing caps;
actual image-work refusal may still prevent full-map owned session. No new ceiling.

Independent source review before any changed live fixture invocation. Once-only focused
unit/race/vet/architecture and exact hash/custody checks; preserve failed rounds/corrections.
No layer-complete status from these formulas: complete rounded/cold/shared allocator,
ordinary real-path P95 and decisive unchanged-image expiry witness still remain OPEN.
