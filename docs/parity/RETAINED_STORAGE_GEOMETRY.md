# Deterministic observer storage — scoped-reviewed and functionally exercised

Status: **Parent controls, independent scoped code review and unchanged-image functional
regression passed.** Not aggregate
resource, RSS, P95, hard-wall, expiry-witness or production-observation acceptance.

## Scope and preserved semantics

Only seven gapdb files from RETAINED_STORAGE_GEOMETRY_HANDOFF.md changed. Public APIs,
sealed codecs/core authority, Host/private helper, kernel, budgets, product/private limits,
provider module/images/admissions and clean qualification candidate are untouched.

Publication storage is now private dense occurrences. Two exact-capacity r arrays are
filled by index, sorted by bottom-up merge (encoded key, unsigned provider revision), then
validated/compacted in one grouping scan. Sorted backing becomes final rows; no odd-pass
copy, third backing, map growth, append growth or payload recopy. Discarded tail and full
other array are cleared. First absent/zero, full role/target identity, strictly increasing
revision, six immutable singleton roles and four mutable exact previous-collected-actual
revision laws remain. Every materialized occurrence retains its codec/witness/class/value /
namespace checks; every final row still receives actual attributed Get. S1-only count,
private actual visited count, S0/S2, frozen Host/projection close and terminal hash unchanged.
Both entrypoints keep fixed opaque ACK-value snapshot BEFORE context callbacks and all
scalar/capture/work checks before variable copies. No partial output on refusal/cancellation.

Transport active connections use stable private intrusive nodes under the same mutex.
Actual post-dial registration/precheck/deferred postcheck order remains. Close shuts every
registered socket, clears each node/head, and detached deferred unlink is harmless. Local
actual conn remains captured by cancellation; no concurrency/admission cap or descriptor /
authority port is added. Lifecycle still owns pidfd/revocation. No retry/fallback.

## Logical ledger, not peak certificate

Pinned Go1.26.7 linux/amd64 test measurements:

- Occurrence368 bytes; two backings736r, <=3,014,656 bytes for r<=4096.
- ACK snapshot204800 bytes, ONE heap allocation; order metadata8192 logical bytes separately.
- Registry node32 bytes, cumulative <=32A=137408 bytes for A<=4294 per invocation.
  Concurrent invocation sums remain separate. Follow-up corrects TOTALlibrary AfterFunc
  graph to<=2A (transportA plus pending Unix connectA), plus timeout callbacks<=A+197;
  stopping doesn't join any pending family.
- L=ceil(log2(max(1,r)))<=12, B1024. Merge comparisons/writes<=rL; conservative logical
  key examinations<=2BrL. One lexical comparison per decision. Collapse comparisons
  <=max(0,r-1), exams<=2B*max(0,r-1); compaction/tail/other clears<=2r writes.
- Materialization writes<=r separately. Canonical payload-copy work remains2V including
  overwritten values. Records backing/codec temporaries/allocator rounding/cold/shared
  objects and existing near9MiB decoded response transient remain separate terms.

These bounds remove two map-geometry terms but DO NOT establish peak heap/RSS, arbitrary
128MiB acceptance, global concurrency memory, hard-wall deadlines or real-path P95.

## Artifact-backed controls and preserved failures

Implementer evidence:
`retained-storage-foundation/run-20261014T183402477126Z/`.
Final seven-file patch45411 bytes SHA256
`35dbd41d0a6b6265404bbbcdfad3ad6fefe4ad8d2a2f963b00b82c4ec837af63`,
`controls-20261014T185348145660Z/exact-seven-file.patch`.

Implementer FINAL once unit/race66 top/871 outcomes each, zero failures/skips, vet and
architecture5 top/14 outcomes. Fresh cache-only R1 replacements3 outcomes unit/race each;
historical map/source/probe overlays remain unmodified and were not incorrectly replayed
against incompatible private representation. Test-only independent chronological Go map
oracle does not call production collapse to derive expected bytes.

Preserved rounds: initial long-prefix fixture exceeded existing identity128 limit, corrected
fixture only; subsequent race process-wide allocation sample204816 versus204800 (no data
race), all failed source/logs retained. Final measurement retains FIVE process-wide samples,
requires minimum EXACT204800 (no increased tolerance), plus isolated AllocsPerRun==1.
No noisy TotalAlloc sample is a peak-memory guarantee.

Parent evidence `retained-storage-parent-9l4ynaw_/`: verified seven hashes, all evidence
manifest members, exact cache-only patch replay and1619 unchanged other entry files; only
four parent-owned docs differed separately. Once FINAL unit/race66 top/871 outcomes each,
zero failures/skips, vet and architecture5/14 passed. Main/clean/images/coordination/
replacement refs unchanged. NO changed-source live provider ran.

New controls cover genuine4096-occurrence builder acceptance/+1 refusal, zero/one/odd /
nonpower-of-two/reverse/long-prefix geometry, ties/full aliases/immutable/predecessor
negatives, cancellation throughout copying/merge/collapse/clearing, backing/payload-pointer
reuse, exact registry bidirectional integrity/head/middle/tail/concurrent cleanup/Close and
actual socket closure. Owned maximum input can still legitimately refuse existing32GiB
image-work reservation; no cap changed to make tests pass.

Independent final code reviewer edfd2a06-7d7d-409 APPROVED exact seven-file source slice,
no scoped defect. Once unit/race66 top/871 outcomes each, vet/architecture5/14; fresh overlay
6/6 per mode (three adapted R1, unsigned genuine-ACK chronology, late invalid-chain no
partial, fixed-node allocation), zero failures/skips. All28 inventoried files unchanged,
all170 implementer evidence entries verified, independent fuzz0 byte replay passed; all80
independent snapshot samples EXACT204800 alongside one-allocation assertions. Supporting
contract partial read boundaries are explicitly recorded, not full resource qualification.
Review: `retained-storage-source-review/run-20261014T190135637748Z/REVIEW.md`.

Parent THEN once unchanged admitted WP13 functional regression (same binary5327126 bytes,
SHA947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad):
2 top/4 outcomes passed, zero skips/failures, package2.256s. Real private-only/actualACK/final
row-overwrite fixture and persisted-ACK/restart regression, not expiry/P95 acceptance.
Evidence `retained-storage-live-parent-corrected-r_1csusx/`; source/image unchanged pre/post.
First parent custody preflight incorrectly included later parent-owned proof doc hash in
blanket reviewer-current equality; it failed BEFORE any Go/provider execution. Failure
`retained-storage-live-parent-3pknd2w0/` retained; correction records the ONE changed parent
doc and verifies all actual reviewed Go/module bytes plus immutable review evidence. Never
calls the failed audit green or rewrites its log.

Separate resource follow-up eedbc865-6a39-46b COMPLETED source-only rounding/cold/library /
borrowed/shared investigation, no numeric qualification. Parent verified8295 project/Go
and358 SDK/x/sys inventory paths and8 evidence entries; exactly four concurrent parent docs
recorded. It closed allocator/backing/proc recipes but identified causal shared parent
children-map/timer-heap/pool history and additional Unix connect callbacks. Full causal
resource reporting needs authorized runtime/shared-state/boundary parameters; exact finite
library-event enumeration still unclosed. See RETAINED_RESOURCE_BOUNDARIES.md for evidence,
limits and specific authority-needed stop. NO pending agent result or qualification assumed. Attempt to resume old resource agent was refused
(agent cleaned up); fresh self-contained research agent launched without editing old audit.

Keep Op01M4V7BWB53R4CK18V28SFDD8A OPEN. Decisive unchanged-image expired-retained-extra witness
remains BLOCKED without authorized compatible hold/scope revision. No new image, admission,
promotion, release or selected-layer completion follows from component greens.
