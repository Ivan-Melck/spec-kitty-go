# WP05 review feedback — independent repair cycle

Verdict: **REJECT**

Reviewer identity: `reviewer-renata-wp05` (distinct from implementation actor `codex-wp05`).

## Blocking finding 1 — startup admission is not derived from an actual product restore

`Lifecycle.MarkProductRecovered` accepts any `controljournal.ValidatedJournalV1` and sets the two readiness booleans (`internal/sidecar/gapdb/lifecycle.go:564-578`). `BindOwnerStartupEpochV1` then treats those booleans as sufficient restore proof (`internal/sidecar/gapdb/startup_proof.go:53-67`). A caller can obtain that journal capability directly from `ValidateJournalV1` over caller-supplied records; it is not bound to `checkpoint.RestoreV1`, a `RestoreReaderV1`, the configured database, its observed revision, checkpoint selection, or the restored projection digest.

The real GJ-02 test exercises this bypass directly: it calls `ValidateJournalV1(registry, nil)`, passes that generic journal to `MarkProductRecovered`, and immediately mints the startup proof (`internal/controljournal/application/reconcile_test.go:165-182`). No product restore or store scan occurs. On a non-pristine database the same sequence could mark an invented empty history as recovered and admit a new epoch.

This violates the closed-readiness ordering required by FR-004/T027 and makes the purported repair in `fa75ee8` a boolean gate rather than proof that product restore preceded epoch admission.

Remediation:

- Make epoch/startup admission consume a constructor-sealed capability produced only by a successful `checkpoint.RestoreV1` over the configured read-only product adapter (including pristine restore), bound to project, mission, database/owner lifetime, observed durable revision, terminal watermark, and projection digest.
- Do not let a generic `ValidatedJournalV1` or caller-settable `RestoreResultV1{Ready:true}` promote sidecar readiness.
- Change the real admitted-binary GJ-02 setup to run the actual restore path before `BindOwnerStartupEpochV1`, and add a mutant proving that direct validation of invented empty history cannot mint startup authority for a nonempty database.

## Blocking finding 2 — an admitted predecessor service remains able to publish after epoch replacement

`EpochAdmissionV1.valid` is a permanent self-hash over value fields (`internal/controljournal/application/service.go:98-112`). `Service.Apply` checks only that private value and that the batch matches it, then reads the operation and aggregate heads before publication (`service.go:475-519`). It neither consults a revocable owner-lifetime capability nor proves the admission is still the current runtime binding. Consequently an old `Service` remains usable after another admission durably replaces `runtime/current`.

I ran a temporary deletion-style adversarial test (removed after execution): create `oldService`, admit a second epoch into the same port, then submit a previously unused/disjoint batch through `oldService`. The observed result was `Kind: committed`, `CommittedRevision: 3`. The durable `runtime/current` already named the replacement epoch.

The existing test does not cover this case. `TestEpochAdmissionOnlyAtOwnerRestart` creates `newService` from the replacement admission and submits an old batch (`service_test.go:477-483`); that proves local value mismatch, not invalidation of the already-issued predecessor service. This leaves a gap in T027's predecessor-epoch refusal and C-009's durable-current-authority law.

Remediation:

- Make an issued service/admission capability become unusable when its owner lifetime is superseded, without introducing the forbidden ordinary global epoch CAS. A process-scoped revocable admission gate bound to the sealed owner-lifetime state is one viable shape; a composition-level drain/close capability is acceptable only if the type/architecture boundary makes bypass impossible.
- Add a test that retains the old service, admits a replacement epoch, and proves the old service cannot publish even to a previously untouched aggregate. Include a transition race test so an in-flight predecessor cannot commit after replacement becomes current.
- Preserve the rule that ordinary commands do not mutate or CAS `runtime/current`.

## Repairs and evidence that passed

- The prior raw checkpoint wrapper is gone. `CheckpointInputV1` now accepts only `checkpoint.ValidatedCheckpointV1` plus the provider-observed selector revision.
- `PublicationV1` is called with the forming command OperationID; record/head/publication IDs and watermark agree; project/mission and current-event exclusion checks fail closed; returned bytes are defensive copies; selector revision zero means absence and a positive revision becomes exact CAS.
- Zero/forged capability, cross-project, cross-mission, wrong-forming-operation, and current-prefix construction paths refuse. Narrow checkpoint capability/current-prefix/epoch-binding tests passed 10 times and under race twice.
- Stored aggregate heads are read and semantically decoded before publication; revision races remain protected by provider CAS.
- Operation-record decode now fails loudly before mutation.
- Exact `PreflightAtomicBatch` is non-mutating and uses the admitted encoder; its focused adapter tests passed 10 times and under race.
- GJ-01/GJ-02 terminal paths call the application universal-invariant seam. The real admitted external `gapdbd` response-drop/reconcile path passed twice under race.
- Application package passed 10 repetitions and two race repetitions; focused GJ-01/idempotency/concurrency cases passed 100 repetitions.
- M1 budget/schema/dependency/reducer guards and mutants passed five repetitions.
- Focused sidecar startup-proof tests passed 10 repetitions and under race; vet and staticcheck passed for application/checkpoint/GapDB/sidecar packages.
- Exact WP05 and prerequisite commit diffs pass `git diff --check`.
- A broad checkpoint regex run was intentionally superseded after it selected WP06's already-approved 16,001-record proof five times; the narrow WP05 cutover gates above replaced it.
- No SQLite, fallback backend, internal GapDB server, human-formatted CLI parsing, or watch-based reconciliation was found in the WP05 path.

## Anti-pattern checklist

1. Dead code: **N/A (staged capability)** — application is intentionally consumed by declared downstream WP07/WP08; adding an out-of-scope caller here would weaken package ownership.
2. Synthetic-fixture test: **FAIL** — the restart test substitutes a new-service/local-value mismatch for invalidating the retained old service, and GJ-02 substitutes a generic empty validated journal for actual restore.
3. Silent empty return: **PASS** — malformed bound or stored operation bytes now produce explicit indeterminate/refusal results before mutation.
4. FR coverage: **FAIL** — FR-004/FR-022 and NFR-007 do not have production-path evidence for restore-before-admission or predecessor-service invalidation.
5. Frozen surface: **PASS** — WP05 consumes the approved checkpoint capability without reintroducing or modifying the frozen raw journal seam.
6. Locked decision: **FAIL** — generic restore marking and non-revoked predecessor services contradict closed readiness, new-restart-epoch authority, and durable-authority-only constraints.
7. Shared-file ownership: **PASS** — sidecar/adapter prerequisite changes and the approved WP06 checkpoint merge are explicit, isolated dependency commits.
8. Production fragility: **PASS** — no new panic/bare-raise equivalent or transient-race crash path was found.

Contracts walked: `control-journal-v1.md`, `operation-record-v1.schema.json`, `event-envelope-v1.schema.json`, `checkpoint-v1.schema.json`, `runtime-current-v1.schema.json`, `m1-product-budgets-v1.json`, `sidecar-lifecycle-v1.md`, and the GJ-02 portions of `evidence-adoption-v1.md`. Adoption record, evidence manifest, ordered-trial, and sidecar-provenance shapes beyond the startup/GJ-02 fields are downstream or orthogonal to WP05.
