# Approved boundary implementation — scoped evidence, not layer completion

Status: **Expiry test mechanism implemented and scoped-qualified; owned-memory
reporting boundary implemented with a reviewed PARTIAL ledger. Full-call memory
accounting and production-Host qualification remain incomplete.**

Approval: [APPROVED_OBSERVATION_BOUNDARIES.md](APPROVED_OBSERVATION_BOUNDARIES.md).
No production admission, source promotion, original-image proof transfer, Op
closure or thread-goal completion is claimed.

## Changes

- `scripts/parity/expiry_evidence/`: prepares an isolated copy of the exact pinned
  provider, authenticates actual module ZIP h1 against go.sum, verifies every
  original source byte, changes one call site and adds three build-tag files.
  The first nonempty genuinely due expiry pauses before execution outside read
  and admin locks. An inherited socketpair acknowledges hold/release; errors,
  disconnect and timeout release. No control listener/provider API is added.
- Original descriptor safety: the hook duplicates FD3 during package init before
  database ownership opens, closes only its own copies, and reserves the one
  inherited slot until process exit. Invalid configuration cannot close a reused
  database lock. No concurrent snapshot/backup is allowed in the held fixture.
- `retained_expiry_evidence_test.go`: real daemon, actual durable adapter commits
  and coordinator ACKs, independently exact-read publication rows, Get-hidden /
  Scan-omitted expired extra, real retained count mismatch, stable attributed
  owner/database/revision, explicit release, EOF release and default-build control.
  This is adapter infrastructure, NOT the production Host/private-chain proof.
- `retained_observation.go`: one private boolean records count mismatch ONLY
  after successful S1 decoding/attribution and every coordinate check. The fixture
  cannot pass merely because an RPC failed. No count, diagnostic, API or authority
  escapes; public refusal behavior is unchanged. Five negative classification
  controls accompany it. The publication test helper now accepts the existing
  publication-port interface so actual adapter commits can mint the test ACKs.
- `retained_resource_accounting_test.go`: checked, test-only logical-length model
  for known geometry/value/large-buffer terms. Includes delegated-owned SDK/Base64
  copies, counts historical overwritten occurrences, refuses overflow/invalid
  inputs with zero output, and always exposes incomplete full-call coverage.
  [Owned accounting](OBSERVER_OWNED_ACCOUNTING.md) enumerates unclosed terms.

## Review and corrections

Initial gate review rejected closing borrowed FD3 on conversion failure and a
module-ZIP trust gap. Initial live-fixture review rejected the missing SDK bridge
and the possibility that an S1 read failure could masquerade as count rejection.
Rejected sources/logs remain under the follow-up cache in
`approved-expiry-rejected-r1-ep_wguq6/` and
`approved-expiry-live-rejected-r1-bm5nsvu7/`.

Corrected full scoped reviews approved build/hook, fixture/private classification,
and partial ledger respectively:

- `/home/user/.cache/approved-expiry-hook-review-r2-zghhym69/REVIEW.md`
- `/home/user/.cache/approved-expiry-live-review-zN5R8jE9/REVIEW.md`
- `/home/user/.cache/spec-kitty-parity-resume-followup/approved-owned-ledger-review-u_58v00s/review.md`

No review claimed full-layer or production admission. Fixture directory removal
and every descriptor-close result are not universally asserted; process shutdown
signal/wait errors and timeout/kill fallback do fail the live test.

## Parent execution evidence

Evidence root:
`/home/user/.cache/spec-kitty-parity-resume-followup/approved-observation-parent-TnMdOgsw/`.
Explicit `GOTOOLCHAIN=go1.26.7`, provider data under btrfs
`TMPDIR=/home/user/.cache/spktmp`. Named logs preserve commands and exit status.

| Requirement | Actual evidence | Result/scope |
|---|---|---|
| Fresh source/delta and exact test image | `expiry-image/preparation.json`, `build.json`, per-step logs | Original provider source unchanged; two identical TEST builds; no old receipt reused. |
| Hook release/error/FD controls | `expiry-image/gate-unit.log`, `gate-race.log` | Each 2 top-level /7 total outcomes, no failures/skips. Timeout/malformed/unread ACK are helper-level controls. |
| Build-tag absence | `build-tag-absence.json`, both `.metadata` files | Hook symbols/env literal absent from no-tag control; present in TEST image. No artifact was executed for this inspection. |
| Real expired-retained-extra witness | `live.jsonl`, `live-race.jsonl` | Each 1 top-level /4 total outcomes, no failures/skips. Explicit hold/release, EOF release, default-build ignore; adapter-only scope. |
| Accounting and count-reason regressions | `unit.jsonl`, `race.jsonl` | Each 26 top-level /162 total outcomes, no failures/skips, full combined package compiled. |
| Static validation | `vet.*`, `architecture.jsonl` | Vet passed; 5 selected guards /14 outcomes passed, no failures/skips. Not the full architecture suite. |
| Original admitted-image regression | `original-image.jsonl` | Unchanged WP13 image, 2 top-level /4 outcomes passed, no failures/skips; real Host private-only/final-overwrite and ACK/restart controls. No expiry proof transferred to that image. |
| Empirical builder measurement | `benchmark.jsonl` | 20 iterations; 221196 covered-source-logical-B/op, empirical 217878 B/op and 78 allocs/op. Different units; NOT a bound on whole-call heap or performance/P95 acceptance. |
| Preservation | source/review hashes and final custody receipt | Main/clean/coordination identities, original image bytes, historical receipts/rejections preserved. No commit or admission advance. |

TEST image SHA256:
`83d5db39db928f62c9725253c53fdb8d1264f1b6e317c94db33aa11944f5905b`.
No-hook control SHA256:
`59508c1885d1ee02b4218376d7a6a772771627f3f9530074f4124613b76cd8c9`.
Neither is an adopted production image. `qualification.json` binds this scoped
result to exact reviewed Go/code-asset hashes, source inventories and log digests.
The first final-custody preflight failed because a blanket hash comparison included
the parent-updated README; `failed-first-final-custody.json` preserves it. The
corrected verifier checks all code exactly and records the one post-review README
change separately. Abbreviated command labels have an additive exact-argv receipt
in `expanded-commands.json`; original labels/logs were not rewritten.

Exclusive preservation snapshot:
`/home/user/.cache/spec-kitty-parity-resume-followup/approved-observation-boundaries-checkpoint/manifest.json`.
It chains to the earlier resource checkpoint and preserves source/docs, new
execution/review evidence and rejected rounds. Preservation is not qualification.

## Pinned allocation-slot continuation

The reviewed PARTIAL logical ledger now has a separately reviewed
[pinned slot leaf](OBSERVER_ALLOCATION_SLOTS.md):
`internal/controlstore/gapdb/retained_allocator_accounting_test.go`. It supplies
normal Go1.26.7 linux/amd64 size-class/header/page/tiny conversion, actual nil-append
capacity/double rounding, per-plan/per-occurrence builder/node accounting, and
large frame/decoded/SDK/Base64/chunk buffer bounds. Both slot ledgers retain
`fullCallCoverage=false` and named missing recipes. These are cumulative covered
source-event upper bytes, NOT a full-call, live/peak heap, RSS, performance or
memory-acceptance result. Compiler-generated extra events and other owned sites
remain open; no blanket library/shared exclusion is used.

Independent scoped review APPROVE, no blocking finding:
`/home/user/.cache/spec-kitty-parity-resume-followup/allocator-leaf-review-0is0Z8Gk/REVIEW.md`.
Exact reviewed hashes, unchanged through parent verification:

- Go leaf: `4074fd2d744bb2e9261aa9ffe5c7d99875a6870f676aa6f536df9c4464cc98ea`.
- Slot document: `281455ec3c5c3cbc5269fe92a2fdbffb8717a4368096b936ed8868b33ca2c4de`.

Parent evidence:
`/home/user/.cache/spec-kitty-parity-resume-followup/allocator-leaf-parent-z3eu3t2f/`.
Parent and reviewer each ran pinned unit/race: **15 top-level /35 outcomes per
mode, zero failures/skips**, and package vet. Parent unit is `extended-unit.jsonl`
(the exact current code); race/vet are separately named. The earlier smaller
initial author source/unit are retained, not rewritten. Source witnesses verify
seven pinned runtime/Base64 files and independently indexed generated classes;
actual capacity controls execute on the pinned compiler. Genuine ACK fixtures
use the local durable port, not native provider receipts or provider I/O.
No unchanged provider scenario, original image, expiry image or benchmark was
rerun for this test-only slice. Product code and old logical model are unchanged.
The prior checkpoint's67 current source/docs,1344 evidence paths and both archives
(67/1344 members) verified before documentation changes; preservation receipts
and the next chained checkpoint retain that history. The first finalization
incorrectly classified the preexisting coordination gitlink DIRECTORY as an added
FILE: current git paths were unfiltered while the baseline excluded nonregular
entries. `failed-finalization.json` and the original script remain unchanged.
Correction uses the same regular-file filter and separately checks frozen
coordination commit/tree; no source/test/provider rerun or authority repair was
needed for that custody-coordinate error. The first corrected script then had
an extra metadata-dictionary brace and failed parsing before any verification
step. Its source and a clearly labeled tool-return failure record remain as
`finalize-r2.py` / `failed-finalization-r2-syntax.json`; correction3 fixes only that
brace. Neither custody failure is a source/test or provider qualification failure.

[Host binding investigation](EXPIRY_HOST_BINDING_INVESTIGATION.md) confirms no
existing end-to-end test-image Host seam: exact admission, hardcoded authority
binding, recovery-mode coupling and the executable/gate FD3 conflict need an
exact independently reviewed design. New-source provenance and a narrow tagged
cross-package bridge remain unresolved; this is NOT an implementation handoff or
permission for a generic factory. No new receipt/admission/Host launch was made.
Already-approved separate-image consent is not requested again.

## Private map/copy continuation — scoped APPROVE

[Private allocation events](PRIVATE_OBSERVER_ALLOCATION_EVENTS.md): three bounded
no-delete maps, including discarded growth, <=35 direct candidate objects,
45744 requested bytes and50920 conditional normal-slot-upper bytes. Accepted
private copy prefixes include the last schema-invalid row; one terminal rejected
adapter response reserves up to4MiB BEFORE private guards. The separate joint
copy envelope is4967802 conditional slot bytes. These are named cumulative source
events, not full-call/live/peak/RSS bounds or actual escape claims.

Independent review:
`/home/user/.cache/spec-kitty-parity-resume-followup/private-allocation-review-b2hrne1x/REVIEW.md`.
Parent/reviewer private unit/race each32 top-level /296 outcomes, zero failures/
skips, both-package vet. Exact3 reviewed hashes remain unchanged:
- `retained_private_accounting_test.go`: `a5e6236db121ea995d1a4a9b11f7cbb8608aa221ff35c7a2ad55952b3f16db0c`.
- `retained_private_geometry_test.go`: `01f2c5c3f439709bc3d6390c92459c60c508bd92729f9bb0641517448e248a32`.
- `PRIVATE_OBSERVER_ALLOCATION_EVENTS.md`: `1de175920858a206cd7c163205e8399a0f9c1fd2a4e750eb3d866f9114caa98c`.
This addition changes no product source or provider behavior by itself.

## Kernel one-shot acquisition and buffer families — scoped APPROVE

Research found a real unbounded potential allocation-event sequence: os.Open's
EINTR loop repeats syscall path conversion without our check. Exact design was
approved BEFORE edits:
`/home/user/.cache/spec-kitty-parity-resume-followup/kernel-open-design-review-c8h_4os1/NOTE.md`.
Product delta is ONLY two proc/exe callsite substitutions, unused-import removal
and private `retainedkernel/file_open.go`. ONE unix.Open attempt with read-only/
CLOEXEC, pre/post liveness, no close of failed/unowned sentinel FDs, valid FD0,
hardcoded NewFile and owned-file cleanup/error joining. No retry or new numeric
cap, admission, public API or provider image change. All identity/pidfd/peer/hash/
socket guards otherwise match original source exactly. Up to6 additional liveness
callbacks per attempted check must be included by future polling recipes.

[Kernel buffer leaf](RETAINED_KERNEL_BUFFER_EVENTS.md) charges four named families:
three32KiB arrays; two conditional Buffer40->scan48 objects; two write-only proc
backing histories including possible append-make temporaries; two explicit suffix
string copies. Subtotal1278048 conditional normal-slot-upper bytes per ATTEMPTED
check, including failed prefixes, not completed hashes only. Count/image work are
jointly bounded by the existing schedule. Fields/FieldsFunc, SHA/FIPS, formatting,
file/stat/path/net/syscall/context/finalizer and compiler events remain open.

Independent exact6-file source review:
`/home/user/.cache/spec-kitty-parity-resume-followup/kernel-open-source-review-g1_ic1av/ANALYSIS.md`.
Full kernel unit/race each18 top /29 outcomes, zero failures/skips, vet. Real proc/
exe readonly/blocking/CLOEXEC/digest, genuine peer/memfd/source-inode, FD0 isolated
child, ownership, cancellation/revocation and synthetic failed-close joining
controls pass. The synthetic FD case is not a real acquired-descriptor receipt.
Reviewer recomputed exact lifetime patch and proved buffer geometry independently
of finite capacity samples. `six-after.json` gives all6 hashes, unchanged through
parent qualification; product hashes are:
- `lifetime.go`: `69395b56089d7749029a4b853ef8a818c8ec98a4a6b845d5b1cf7efc24dac61e`.
- `file_open.go`: `777d986dbd63e0a0323580a1849d5f25dc79d6e82db8f339d31fb1875d863b2e`.
- Buffer test leaf: `28bfdc9f9cbb1669d58820c01a96276db014ca8b18a766ae3f26c1448f0ddefe`.
The two reviewed leaf documents retain their author-time pending-review text and
historical os.Open source finding; THIS parent record supersedes those statuses
without rewriting their reviewed bytes. Read/Fstat EINTR handling and full resource/
wall-clock semantics are not certified by the one-shot path correction.

### Current parent execution and custody

Evidence:
`/home/user/.cache/spec-kitty-parity-resume-followup/private-geometry-parent-4hxso1he/`.
`qualified-inputs.json`, exact per-command argv/env, raw JSON/stderr/exits and
`qualification-results.json` bind all9 reviewed source/doc hashes and frozen images.

| Fresh control | Passing scope |
|---|---|
| Combined3-package selected unit/race | Each79 top /654 outcomes, zero failures/skips; full packages compiled |
| Package vet | Adapter, productionruntime and retainedkernel pass |
| Architecture | Selected5 guards /14 outcomes, zero failures/skips, not full suite |
| Original WP13 image | Host private-only/final-overwrite and ACK/restart2 top /4 outcomes, no skips |
| Separate expiry/control images | Adapter live unit/race each1 top /4 outcomes, no skips; actual hidden-expired-extra authenticated S1 mismatch, release and no-tag controls |

Narrow native regressions reran because PRODUCT kernel acquisition changed;
no unchanged whole native scenario/oracle or benchmark was repeated. Original
SHA947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad
and TEST/control SHA83d5db39… /59508c18… remain unchanged. Neither adapter-only
expiry evidence nor generic kernel tests mint production Host/new-image authority.
Initial author sources/logs, design receipt, failed agent-resume/administrative
lookups and prior checkpoint/failures remain preserved. No source promotion,
commit, authority repair or Op closure. New chained snapshot:
`private-kernel-checkpoint/manifest.json` under the follow-up cache, prior link
`allocator-leaf-checkpoint/manifest.json`.

## Tagged S1-negative terminal infrastructure — independently scoped APPROVE

[Exact implemented slice and limitations](RETAINED_EXPIRY_CLOSURE_SLICE.md). This
closes the adapter building block, NOT the genuine-Host expiry deliverable.
The private conditional state is FIRST/empty in disabled builds (ordinary136 bytes,
unchanged offsets/method set); E state152/component16. Shared source changes only
that field, authenticated S1 note, Finish tail dispatch. Existing kernel files and
budget caps remain byte-unchanged. E-only two fixed pointer methods arm/consume;
one fixed kernel guard returns error only, no public provider/count/selector/callback.

Armed S1 inequality closes S2 then returns0/sentinel while phase3 remains live;
unchanged CheckLifetime performs reserved terminal full hash. Canonical pointer
Consume checks phase/flags/terminal, fresh pidfd guard, bound and invalid CAS under
same mutex before cancel/unlock. All errors/repeated/copied/wrong-phase/overlap uses
consume aliases, never positive matched proof or actual count. Successful consume
intentionally cancels private context; independent outer contexts remain separate.
Lifetime final scalar and observation CAS are distinct, no atomic joint snapshot.

Independent V1 design review found completed post-terminal Revoke could still earn
consumption. Kept V1 and rejection intact, revised exact V2 with fixed fresh tagged
liveness guard, obtained design APPROVE BEFORE code. V2 rejects completed Revoke/
Close even after terminal check, without another RPC/hash; one existing bounded
pidfd-poll guard and its owned costs are explicit. Full Host bridge/provenance/FD/
root/binding/recovery design remains unapproved/unimplemented and must adopt this.

Evidence under `/home/user/.cache/spec-kitty-parity-resume-followup/`:
- Parent `expiry-closure-parent-0nnjq77b/`; entry1649-file baseline before new work,
  exact V1/V2 plans, before-source/diff, pre-product ordinary layout evidence,
  immutable design/source receipts and all command/raw result logs.
- V1 `expiry-closure-design-review-kpbiohb1/REVIEW.md` REJECTED; V2
  `expiry-closure-design-review-qcf2ufea/REVIEW_V2.md` APPROVE only exact slice.
- Actual frozen8-file independent source review
  `expiry-closure-source-review-thdenxg0/REVIEW.md` APPROVE only infrastructure;
  all8 files read completely, source3-hunk reconstruction/canonical CAS/lock order/
  guard/legacy behavior and author raw logs independently checked.
- Author ordinary unit/race107 top /1010 outcomes each; E113 /1077 each; both
  three-package vet, zero failures/skips. New E alone7 top /68 outcomes, including
 8 closure controls,24 bad-coordinate phase controls, read/lifecycle/state/scalar/
  budget/copy/cancel/Close/revoke and synchronized overlap controls.
- Reviewer focused ordinary unit/race4 /22 each, E10 /89 each; vets, no failures/
  skips.4110 existing source/cache files and git status preserved during review.
- Relevant CURRENT changed dispatch native controls, once per build AFTER source
  approval: ordinary and E original-WP13 Host1 /3 EACH; ordinary and E separate-
  image adapter1 /4 EACH; zero failures/skips. Still no actual tagged Host expiry.
  These unarmed controls prove tag alone did not mint authority or alter the old
  mismatch/equality path; actual images and reviewed8 files unchanged throughout.

Go-list verifies ordinary/E exact GoFiles exclusion; actual diagnostic-only compile
shows canonical pointer heap escape at Arm's store. It selected zero tests and is
NOT a qualification/performance ELF or complete allocation certificate. E new
state/handle/poll/context/mutex/wrapper costs remain owned/open. Old3A+1 full hash
budget unchanged. No new timing, benchmark/whole oracle/gate-image build or ordinary
admission/candidate/image retarget. Historical120 timings remain prior-source ONLY,
not transferred to this current normal/tagged compile.

Newest exclusive chained custody: `expiry-closure-checkpoint/manifest.json`, links
prior observation-qualification manifest SHA
`2a41759321f3d757bc178280882d2875a594a9f9807c58067ebe859a0f626fe1`.
Prompt-to-artifact audit: full owned memory, full Host expiry/root and native negative
census, maxwork/private64/large/refusal/cancel performance remain OPEN. No commit,
promotion, cap/admission change, architect Op closure or goal-complete call.

## Prior four-deliverable goal — negative controls and actual original-Host timing

Active goal1792063964395-11a6c741a62c6 requires complete memory, genuine-Host expiry,
full negative matrix and performance qualification. [Contract/checklist](OBSERVATION_QUALIFICATION_PLAN.md)
was independently read/reviewed alongside two new test-only files. All four remain
OPEN; exact source-based/scoped evidence is not a complete-layer verdict.

New negative matrix:48 coordinate xphase/count-decoy cases,8 middle-count/outer-
decoy controls,4 exact-read failures that cannot reach S1; irreversible copied/
failed handle consumption and sanitized zero-count failures asserted. Real Unix
exchanges to simulated same-process endpoint, NOT admitted Host/expiry proof.
Statistics unit covers nearest-rank, ownership and checked sums. Independent new
selected unit/race each4 top /71 outcomes, no failures/skips, vet both packages;
parent broader three-package unit/race each106 top /1009 outcomes, no failures/
skips, vet. Exact3-file source/method review:
`observation-matrix-review-z7aDZRii/REVIEW.md` under the follow-up cache. Hashes:
- `retained_negative_matrix_test.go`: `bc9ff81ef1396d6f78b5d87a345479ce0b97de91ae5887ec12ee507beca3b31e`.
- `retained_performance_test.go`: `f68ee01ea8a5046def9cf8e3a696649d5e4e347d41410b06f555cf0b6ad29457`.
- `OBSERVATION_QUALIFICATION_PLAN.md`: `4781619ee73010b0cc3e0141f86327b6dada74843abe48415457f6921f8b6d45`.
All3 unchanged through actual execution/audit. Reviewed plan retains its author-
time pending-native wording; this record and results provide the current state.

After method review, ONE ordinary source-bound original-WP13 genuine Host run
measures120 calls (4 small workloads x30) through actual Start/VerifyRetainedRecords.
Initial/restarted private-only P95=89.902520/120.393235ms; actual3-ACK overwrite
(13 publication rows) P95=217.028547/237.193993ms. Private rows6/9, actual fresh
restarted epoch. Every sample matched/unchanged attribution/projection/ready state;
no retry/discard/warmup/forcedGC or memory instrument. Native1 top /3 outcomes,
zero failures/skips; all1648 pre/post inputs and provider stable. [Full results,
command/image/test ELF/report hashes and limitations](OBSERVATION_PERFORMANCE_RESULTS.md).
Independent artifact-only verification of all120 samples/cardinalities/statistics/
source/image/ELF: `observation-native-review-bDd64Lwk/REVIEW.md`. These are approved
SMALL-WORKLOAD MEASUREMENTS, not full performance/SLO/expiry/memory acceptance.
Parent first WORK-location postprocessor failed AFTER native exit0; corrected by
reading existing Go1.26 JSON build-output, preserved test ELF without rerunning.

Full memory remains open: research36-family registry is incomplete/unreviewed,
including unrecovered/truncated read windows and untraced helpers; parent verified
its recorded source hashes. Kernel callback derivation cache-only, no memory test
or new accepted bound. Original JSON leaf's author-time pending text remains
superseded by its earlier exact scoped approval, not by the research registry.

More exact Host draft reads47 complete files and authenticates detached sources/
acyclic tuple/receipt. Still not approved/implemented. Parent follow-up completely
reads8 capability ports and recovery, locating the existing ReaderV2-only workspace
wrapper and direct scheduler reads; candidate U1 closure needs independent review.
Post-consume context cancellation, exact tagged binding/schema/profile and unsupported
Attach/transfer scope are explicit next design work, not permission to implement.
No new bridge/FD/provenance/admission code or provider/source change occurred.

Parent receipts: `observation-closure-parent-gm0zjqbi/`; research:
`owned-event-registry-f8vszzJ4/`, `expiry-host-exact-design-xlv8pfj_/`, all under
`/home/user/.cache/spec-kitty-parity-resume-followup/`. New exclusive chained
custody: `observation-qualification-checkpoint/manifest.json`, prior private-json
manifest SHA `6b037bdbfbbad459e83379b25e3053b127204aa91464ef9c79dcf547dfffe071`.
All prior failures/reviews/images/candidate/coordination preserved, plus failed
lookup/billing/resume/postprocessing attempts. No unchanged oracle/benchmark/
architecture rerun, commits, promotion, authority/Op change or goal completion.

## Prior private JSON named-event continuation — scoped APPROVE

[JSON event leaf](PRIVATE_JSON_ALLOCATION_EVENTS.md) adds only test bookkeeping:
source-bounded strict<=65 /Core<=260 entries, independent trusted reconstructed
input bounds, EOF-safe decoder requests512/1536/3584/7680 with per-event slots
512/1536/4096/8192, Decoder304->scan320 /Reader40->scan48, conditional Valid
scanner64, and possible append-from-nil fast copies. Pointees, map/array/span/token/
scanner stacks, Marshal outputs/scratch and other owned costs remain open. The
5860400 scalar conditional slot upper reserves a branch superset; it is not actual
allocation telemetry, simultaneous live storage, full JSON/call or memory acceptance.

Companion controls verify concrete schemas, no custom JSON/Text hooks, separate
trusted input maxima, actual EOF/capacity/Token/compaction behavior and a real
193-row fake-reader failed prefix. Canonical case aliases reach input fallback
AND reconstruction fallback before refusal; healthy-path counts are not universal.
No native receipt or measured allocation/fallback census is credited to this test.

Independent exact3-file review:
`/home/user/.cache/spec-kitty-parity-resume-followup/private-json-review-0TNvIl09/REVIEW.md`.
Parent/reviewer pinned unit/race each38 top-level /302 outcomes, zero failures/skips;
both-package vet. Sources and full packages compile without provider env; no opt-in
skip bypass. No product source/API/cap/admission change; no unchanged provider,
architecture or benchmark execution was repeated. Exact frozen reviewed hashes:
- `retained_json_accounting_test.go`: `2ce7a4429c59d38d63680136ab1d1fed1755e718b8b9a5433e19d7ac4df22de2`.
- `retained_private_json_accounting_test.go`: `9d627bf4555b3b12488209f5d4e1ed1320a813466d417f4b396cec2831ac7050`.
- `PRIVATE_JSON_ALLOCATION_EVENTS.md`: `36a94ef52228d946d9c5e01243c4f7fe37464140cc24e09d2b0aa6e1c6a072e3`.
The leaf's author-time pending status is retained as a reviewed snapshot; this
parent record supplies the current scoped verdict without rewriting those bytes.

Parent evidence:
`/home/user/.cache/spec-kitty-parity-resume-followup/private-json-parent-l5v7lctg/`.
Initial sources/unit, exact final commands/logs/exits, review/source hashes and
preservation receipts retained. Chained snapshot: `private-json-checkpoint/manifest.json`
under the follow-up cache, linking `private-kernel-checkpoint/manifest.json`.

[Host design candidate](EXPIRY_HOST_DESIGN_CANDIDATE.md) is NOT a reviewed handoff.
Read-only Plan output was cached by the parent (agent could not write); parent
source/encoding checks add capability-comparability, accurate modified-source
provenance and95401-byte inventories versus65536-byte normal record constraints.
Receipt/tuple distinction and acyclic proof inputs, normal-build absence, FD4/FD3
ownership, recovery/freshness/restart and terminal S1 negative finalization remain
unresolved. No Host bridge/technical receipt/launch or admission was implemented.
All agents finished. No product/candidate promotion, commit, Op closure or goal
completion occurred. Full memory/negative/Host/P95 qualification remains OPEN.

## Completion audit and next work

The two authority decisions are supplied and recorded; do not request them again.
The expiry mechanism has source-bound independently reviewed adapter execution
evidence, NOT Host-composed new-image qualification. Full observer-owned allocation
is **not** closed: remaining codec/scanner/private JSON/string/schema costs,
connection/context/callback objects, kernel/library/compiler event multiplicities
and unlisted rounding/disclosure terms remain open. Every successful partial
ledger still marks fullCallCoverage=false. Do not promote it to memory acceptance.

[Private JSON investigation](PRIVATE_JSON_ALLOCATION_INVESTIGATION.md) is source-only,
unimplemented/unreviewed. It derives candidate decoder/canonicalization counts,
refill requests and trusted-binding bounds; refutes a universal property-count
argument for generic Marshal cycle tracking while distinguishing shallow private
schemas. Parent verified old JSON build selection and corrected the static example
length4095->4094 without rewriting research. No Go/Core execution or exploit is
claimed for that example. More source-bound recipes and tests are required.

Next work is those finite allocation recipes and a separately reviewed technical
binding for Host-composed new-image evidence, without weakening original lifecycle
or inventing an admission factory. Full live negative/P95 qualification and the
remaining parity layers are still open. Original unchanged-image expiry evidence
remains unavailable; the new evidence is accurately scoped to its own TEST image.
