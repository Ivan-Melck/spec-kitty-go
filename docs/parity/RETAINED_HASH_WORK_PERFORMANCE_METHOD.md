# Retained hash-work, preflight and cancellation — V2 INSTALLED AUTHOR WORK

Status: installed author work, **NOT independently reviewed or qualified**. Installed paths: `internal/productionruntime/retained_hash_work_performance_test.go` and this document at `docs/parity/RETAINED_HASH_WORK_PERFORMANCE_METHOD.md`. The original V2 cache bytes remain preserved at `/home/user/.cache/hash-workload-draft-v2-7seas616/`. V1 remains preserved at `/home/user/.cache/hash-workload-draft-y9ker1d6/` and is superseded, not modified.

## Corrected real geometry

All256 initial operation roots use the original operation+permanent workspace-claim fixture shape. There are **FIVE** base records per root: operation binding, operation head, one journal event, permanent workspace claim, publication witness. Operation head replaces, rather than accompanies, an aggregate head in `deriveSealedRecords`. No runtime_host or other nonoperation event/schema is introduced. No special permission is needed for this private fixture correction.

Positive:159 roots×3 effects +97 roots×2 effects =671 effects. `256*5+671=1951` measured rows. Negative independent root: one additional legitimate canonical effect on index255,672 effects,1952 measured rows. Every effect is a real `EffectIntentReference` with canonical bytes and `ReducerStateDigestV2` commitment; sorted exact `OperationState.EffectIDs` include every effect. One operation event2/2, real operation reducer, canonical absent head condition, real runtime epoch and host-scoped coordinate are used. Host authority plus zero-padded index makes operation/event/effect IDs and claims unique across both roots.

Exactly **SIX physical roles** occur: binding256, operation_head256, journal_event256, workspace_path_claim256, publication_witness256, effect_intent671/672. This is not seven/all-ten-role closure. Aggregate_head, eligibility_guard, checkpoint_record and checkpoint_slot are separate OPEN work. Large values, maximum4096-row closure, private64 chain and all-role workload are outside this method.

Effects and claims are generic canonical sealed retained-record fixtures, not capability execution/admission grants or evidence that an effect ran. All ACKs used in the future native workload come from the actual original Host coordinator, never from an ACK constructor or structural Host.

## Exact image/work arithmetic

I=5327126 bytes; SHA256 `947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad`; H=32<<30=34359738368. Reserved calls A=r+195+3; reserved full hashes3A+1. `floor(H/I)=6449`; guarded `rmax=((floor(H/I)-1)/3)-198=1951`.

|rows|calls|hashes|bytes|result|
|---:|---:|---:|---:|---|
|1951|2149|6448|34349308448|accept; ratio≈0.999696|
|1952|2150|6451|34365289826|refuse;5551458 bytes over H|

Positive actual-private-six traversal nominally uses `(3*(1951+6+3)+1)*5327126 = 31328828006` hash bytes, ratio≈0.911789. **Near32GiB is the conservative reserved-work boundary, not32GiB actually consumed.** The method adds no actual hash/RPC counter instrumentation. ACK cap256, record cap4096, reserved private195 and all product policies remain unchanged. No refusal cause is inferred from latency.

## Future actual-Host workload

Start independent durable roots through ordinary original-WP13 `Start`. No reconciler, restarts or intentional checkpoint publications. Seal all256 real plans, measure and validate them outside timing. Build actual `BuildReducerSetV2`/tracker/coordinator on `h.owner.(*productionOwner).adapter`, publish through real `PublicationCoordinatorV2.Publish`, actual observer-only ACK recorder and `liveProofSink{host:h}`. Re-measure and validate actual `ACK.Plan()` footprints after publication. No fake ACK, provider selector, admission alteration, raw writer or structural Host is used.

Root0 runs these three cases, then closes before root1 starts:

1. `pub1951_match`:256 genuine ACKs; expected positive proof publication1951/private6/total1957, exact captured attribution.
2. `pub1951_already_canceled`: same valid-root ACKs; independent context from `context.Background()` canceled before sampling; expected exact `context.Canceled`, zero proof.
3. `ack257_scalar_refuse`: append one opaque copy of an existing genuine ACK, yielding257 supplied ACKs but still only256 actual publications. Expected zero proof and exact sanitized retained-records refusal. This probes scalar length refusal, not257 distinct publications.

Root1 independently seals/publishes256 roots with672 effects and1952 measured rows. `pub1952_hash_preflight_refuse` expects zero proof and exact sanitized retained-records refusal. Its genuine ACKs are not forged to simulate the boundary.

Ordinary source/gate tests must show cancellation returns before composition/provider work;257 scalar gate precedes callbacks/copy/provider/count work; image-dependent work gate follows scalar footprint preflight and precedes publication-map cloning and provider observation. The actual private planner must accept1951/refuse1952 at the pinned image. These causal checks, not latency or public error text alone, establish the intended seam. Public sanitized refusal cannot independently identify its private cause.

## Sampling, outcomes and invariants

Exactly30 sequential first-inclusive observations per case;120 samples if complete. Only `VerifyRetainedRecords` is timed between `time.Now` and `time.Since`. Startup, publication, source hashing, filesystem checks, assertions, reporting, close and setup contexts are outside timing. No retries, warmup, discards, outlier filtering, forcedGC, allocation instrumentation, benchmark calibration, concurrent mutation, intentional background compilation or load during timing. Ordinary scheduler/GC/lifecycle supervision effects remain. Setup naturally affects caches; this is not cold-start latency.

Use the existing `retainedPerformanceStatistics` helper, preserving raw order. Record expected_outcome, observed_outcome, positive-only matched, expected_outcome_met, proof zero, error presence/type, matched counts, before/after/proof attribution and explicit invariants. Expected refusal/cancellation is not positive proof. Only exact public `*ProgressError` kind ProgressIncomplete, field retained_records and message `PROGRESS_INCOMPLETE: retained_records` is sanitized_refusal. Canceled requires the exact sentinel. Zero success, wrapped/content errors, deadlines and nonzero error proofs fail.

Every observation checks unchanged ready Host state/status, attribution and baseline, owner/projection/retained-composition pointer identity, database/epoch and authority-directory identity. Positive count equality and negative zero-proof conditions are exact. The first unexpected sample is appended before Fatal; sync the failing prefix before Close, then save final close diagnostics. Do not silently rerun or substitute a later successful attempt. Hard kill cannot guarantee the latest in-memory sample was flushed. Existing cooperative30-second and private5-second deadlines remain; no hard-wall promise or latency SLO follows.

## Reporting, profile and custody

Own report env: `SPEC_KITTY_OBSERVATION_HASH_WORK_REPORT`; exact original image env: `SPEC_KITTY_WP13_GAPDBD`; reviewed custody manifest env: `SPEC_KITTY_OBSERVATION_HASH_WORK_CUSTODY`. No opt-ins skips the native test; partial opt-ins fail. Native execution still requires **independent method review first**. Installed author source must be independently reviewed and freshly recompiled by the parent after review; add-only overlay diagnostics do not qualify that future executable.

Reserve report BEFORE provider/root setup using O_EXCL0600 in a preexisting real runner-owned0700 nonsymlink directory. After creation, Fstat must confirm an actual regular0600 runner-owned single-link file; on failure Close, join errors and preserve the file (never Remove). Use a short runner-owned reportdir path to satisfy UNIX-socket limits. Both exclusive evidence roots and their0700 authority/runtime subdirectories remain beneath it even on failure. No `RemoveAll`, `t.TempDir` cleanup or root deletion. Lifecycle Close may remove its own ephemeral socket/process artifacts; this is disclosed.

Future native profile: ordinary **Go1.26.7/linux/amd64, GOAMD64=v1, CGO_ENABLED=1**, empty GOFLAGS/GOEXPERIMENT/GODEBUG; no race, coverage, sanitizers, qualification tags, custom optimization/link flags or semantic overlays. Preserve effective go-env, exact compiler/toolchain version/hash, build/run commands and raw environments, source snapshots/inventory including dirty/untracked dependencies, module sums and admission/adoption inputs, exact image/ELF hash, compiled test executable hash, and independent method-review artifact. The custody schema in source binds those files by absolute path/SHA256 and checks image/executable plus independent_method_approved/ordinary_build flags. Flags/self hashes are not authentication; independent reviewer must verify inventory completeness, authorship, effective environment and build/run correspondence. No mixed-geometry approval field remains.

Only after ordinary tests and independent review, compile the installed reviewed source with the pinned ordinary toolchain, finish compilation, verify no intentional background compile/load, then directly execute the bound test binary from `internal/productionruntime` with exact anchored `-test.run '^TestRetainedHashWorkOriginalHostNative$' -test.count=1 -test.timeout=45m -test.v`. Unset unrelated native/performance/qualification opt-ins. Do not run a broad package suite with the native image variable set. This command shape is not authorization to execute native/provider/performance work during drafting.

## Diagnostic evidence and boundaries

The preserved cache-only V2 diagnostics used an ADD-ONLY Go overlay mapping then-nonexistent repository test filenames to cache sources. Existing source files were never overridden. Diagnostic commands use exact installed Go1.26.7, -mod=readonly and anchored provider-free test names; all SPEC_KITTY_* variables are removed. They type-check existing package tests, but execute only pure planner/seal/footprint/reducer units. Logs, effective environment, overlay map and source hashes are preserved; every failed attempt would remain under its numbered attempt directory. No native/provider/performance test is selected.

`TestHashWorkV2DiagnosticPlanner` checks1951/1952 exact reservation arithmetic and zero/oversized/overflow inputs. `TestHashWorkV2DiagnosticSealedGeometry` invokes real SealCommitPlanV2 and MeasureRecordFootprintV2 for both256-plan sets, confirms671/672 effects,1951/1952 rows, six roles, five-base shape and operation2/2/current supplied epoch, actual reducer acceptance and3903 cross-root unique semantic rows. An additional package-local add-only diagnostic test checks the actual unexported store planner and unchanged256/4096 limits. Exact outcomes are in diagnostic command/stdout/status artifacts.

Passing overlay units establish those pure source/type/geometry facts only. They do not establish actual original-Host ACK persistence, private-six readback, cancellation/refusal/no-provider-call behavior or timings. Installed provider-free method and boundary tests additionally exercise scalar outcomes/report mutations, real private planner arithmetic, filesystem reservation refusal and source-order guards; these remain ordinary author tests, not independent method review or native evidence. The source is now installed author work; independent review and a fresh ordinary rebuild are still required. Source edits invalidate prior executable binding. Native outcomes are unobserved. All whole-layer, whole-memory, genuine-expiry and performance-SLO qualification flags remainfalse, including a future complete report.

## Completion and external acceptance boundary — author addendum

`Complete` means **in-memory timed cases plus Host cleanup checked BEFORE the final report descriptor write/sync/Close**. It requires schema1, stage `final`, the exact four cases/root indices and thirty sequential valid samples each, correct root geometry, completed Host cleanup, nonempty required report/root paths and admission identity, and constructor-consistent distinct paths. Evidence roots are absolute clean sibling paths; authority/runtime are their separate named children and each runtime session is a separate direct runtime child. Every sample's before/after database and epoch must match its selected root; positive proof attribution must match too. This is report consistency, not authentication of filesystem identities or observations.

For the accepted classes, `error_type` must match `fmt.Sprintf("%T", err)`: matched has `<nil>`; canceled requires the exact `context.Canceled` sentinel (`*errors.errorString` in pinned Go1.26.7); sanitized refusal requires a direct `*productionruntime.ProgressError`. These constraints follow from `hashWorkObserved`'s source class definitions, not an SDK/provider assumption. Type text alone cannot attest that an error was observed. Deadlines/wrapped cancellation/other classes never satisfy a complete case.

**ANY measurement acceptance additionally requires external raw command exit0, successful final write/truncate/sync/Close outcomes, and independently reconciled source/ELF/effective-profile custody.** Preserve the exact command, stdout/stderr/exit, all prefixes and final report, and reconcile them together; this JSON is not self-attestation. A late write/sync/Close failure can leave an incomplete/stale report or even previously written `complete:true`. It invalidates that attempt and cannot be repaired by rerunning, replacing its prefix, or substituting a later success. No later self-rewrite or extra authentication boolean is invented here.

Manifest authorship, independent-review authenticity, source/dependency/adoption inventory completeness and correspondence between actual source, executable ELF, build/run commands and effective profile remain **EXTERNAL independent-review requirements**. Existing flags and file-binding equality are necessary inputs/guards, not authentication. The remaining metadata-acceptance tests document only these external boundaries; schema/stage/path/root-link/error-type corruption is now refused.

Filesystem unit checks use disposable unit scratch, never evidence roots. The foreign-owner dynamic check is optional and unexecuted without privilege; no escalation is requested and no runtime coverage of that branch is claimed then. The umask-child helper's normal standalone skip is not missing acceptance coverage: its parent unit invokes that helper in a separate process and requires success. Closed/read-only descriptor tests establish scoped write/Close refusal and prefix preservation, not injected first-Close/Sync kernel faults, live Host cleanup failures or hard-kill durability.
