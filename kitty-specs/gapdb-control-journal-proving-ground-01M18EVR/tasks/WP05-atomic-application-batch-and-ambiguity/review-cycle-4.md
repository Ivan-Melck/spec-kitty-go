---
affected_files: []
cycle_number: 4
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command:
reviewed_at: '2026-08-30T18:46:10Z'
reviewer_agent: reviewer-renata-wp05-cycle3
wp_id: WP05
---

# WP05 review feedback — repair cycle 3

Verdict: **REJECT**

Reviewer identity: `reviewer-renata-wp05-cycle3` (independent from implementation actor `codex-wp05`).

## Blocking finding 1 — restore observation can drift before replacement epoch admission

The new sealed restore capability binds `ObservedStoreRevision`, but epoch admission checks that revision only for the zero/non-pristine case and never establishes that the store is still at the restored observation. In `internal/controljournal/application/service.go`, `admitRuntimeEpochFactsV1` validates the restore facts before acquiring the DB/project/mission authority lock, then derives only `runtime/current` and the runtime aggregate head before publishing the replacement. A command on a retained old service can therefore commit after restore and before replacement admission. The replacement still commits and the new ready projection omits an accepted durable command.

I reproduced this with a temporary deletion-style reviewer test (removed after execution):

1. Admit the old service at database revision 1.
2. Capture replacement restore facts with `ObservedStoreRevision == 1`.
3. Publish an old-epoch command through the retained service; it commits at revision 2.
4. Call `admitRuntimeEpochFactsV1` with the captured restore facts.
5. The replacement is incorrectly accepted at revision 3.

Observed failure:

```text
stale restore observation admitted: restore_revision=1 late_revision=2
replacement={Kind:committed CommittedRevision:3}
```

This violates closed readiness (FR-004), complete disposable restore (FR-018), restart admission (FR-022/NFR-007), and durable-authority-only C-009.

Remediation:

- Linearize the successful product-restore observation and epoch replacement so no predecessor operation can commit between the observation that produced `RestoreAdmissionV1` and durable replacement publication. Merely hashing the observed revision is insufficient.
- Fail closed if the store changes after the admitted restore observation. Preserve the law that ordinary commands do not read, mutate, or CAS `runtime/current`; solve the startup transition at the owner/replacement boundary.
- Add both race orders: an old command completed before the restore cut may precede replacement, while an old command attempting after the restore cut must not commit before or after the replacement becomes current. Assert that the replacement projection includes every accepted predecessor command through the admitted observation.
- Add a stale `ObservedStoreRevision` mutant with otherwise valid startup/product proofs and exact runtime-current/head records; it must refuse before replacement readiness.

## Blocking finding 2 — the external restore translator does not bind scanned records to their canonical namespace

`adapterRestoreReaderV1.refreshJournalV1` decodes each scanned value and copies its revision, but never verifies either:

- `record.Key == spk/v1/<project>/<mission>/journal/<decoded EventID>`; or
- `decoded EventEnvelope.ProjectID == the reader's configured ProjectID`.

`RestoreV1` subsequently validates event syntax/order, but the pure journal seam has no provider key and does not compare event project identity with `SourceIdentityV1`. As a result, the translator can mint a sealed restore admission whose claimed source contradicts the records actually reduced.

I reproduced both cases with a temporary reviewer test (removed after execution):

1. A scan under `spk/v1/project/mission/journal/` returned key `.../journal/not-the-event-id` containing EventID `event-reviewer-honesty`. Restore returned `Ready:true` and sealed admission.
2. The canonical-looking key `.../journal/event-reviewer-honesty` contained a valid envelope with `ProjectID=other-project`. Restore again returned `Ready:true` while the sealed facts claimed `Source.ProjectID=project`.

This contradicts the frozen key form in `contracts/control-journal-v1.md`, the exact source binding promised by `RestoreAdmissionV1`, and the corruption fail-closed rules in FR-016/FR-020/C-011.

Remediation:

- Before appending any scanned event, require the exact canonical journal key derived from the decoded EventID and require the decoded project identity to equal the configured reader/source project.
- Reject mismatched key suffixes, wrong project identities, duplicate/misplaced provider records, and records outside the configured mission prefix before a usable restore capability can exist.
- Add wrong-key, cross-project, and cross-namespace mutants through `NewRestoreReaderV1 -> RestoreV1 -> AdmissionV1`, asserting typed non-readiness and no product proof.

## Prior blocker closures that passed

- A generic `ValidatedJournalV1`, caller-built `RestoreResultV1{Ready:true}`, zero `RestoreAdmissionV1`, and invented empty history no longer mint startup readiness. `RestoreAdmissionV1` is constructor-sealed by successful `RestoreV1`; the seal binds SourceIdentity, DB/host owner identity, observed revision, terminal watermark, pristine bit, projection digest, and selected checkpoint identity/digest.
- `Lifecycle.MarkProductRecovered` compares the sealed restore with its admitted DB/host lifetime and produces a mission-specific `ProductRestoreProofV1`. `AdmitRuntimeEpochV1` requires matching sealed startup and product-restore proofs.
- The real admitted-binary GJ-02 path now executes `Adapter -> NewRestoreReaderV1 -> RestoreV1 -> MarkProductRecovered -> BindOwnerStartupEpochV1 -> AdmitRuntimeEpochV1` before the response-drop command.
- Retained and copied old services revoke after same-process epoch replacement. The authority registry is keyed by DB+project+mission; the exclusive lock covers durable epoch publication/reconciliation and generation rotation; `Apply` and `Reconcile` hold the shared generation guard across their complete windows. The two transition race orders pass repeatedly, and ordinary commands do not read/mutate/CAS `runtime/current`.
- `RestoreReaderV1` remains exactly three methods. Because GapDB ScanPrefix cannot scan after a revision watermark, a valid-checkpoint suffix attempt returns typed `NotReadyReplayBound` before any provider Scan and does not fall back to an origin prefix read. Origin traversal is bounded to 32 provider pages, 16,000 events, and 48 MiB.
- The prior sealed-checkpoint cutover, exact non-mutating batch preflight, fail-loud operation decode, one-batch/idempotency/CAS laws, and GJ-01/GJ-02 invariants remain green.

## Independent verification

- `go test ./internal/controljournal/application -count=10 -timeout=10m` — pass.
- `go test -race ./internal/controljournal/application -count=2 -timeout=10m` — pass.
- `SPEC_KITTY_M1_GAPDBD=/tmp/go-kitty3-gapdb-admission.cOGcrB/gapdbd go test -race ./internal/controljournal/application -run '^TestReconcileRealGJ02ResponseDrop$' -count=1 -timeout=5m -v` — pass against SHA-256 `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`.
- `go test ./internal/architecture -run '^TestM1' -count=3 -timeout=10m` — pass, including mutant controls.
- `go vet` and `staticcheck` over application, checkpoint, public GapDB adapter, and sidecar packages — pass.
- `git diff --check 1b24645..9eff77e` — pass.
- `go test ./internal/controljournal/checkpoint -count=1 -timeout=15m` — pass in 154.298s, including the real large-history proof.

## Contract round-trip and anti-pattern review

In scope and walked: `control-journal-v1.md`, `event-envelope-v1.schema.json`, `operation-record-v1.schema.json`, `checkpoint-v1.schema.json`, `runtime-current-v1.schema.json`, `m1-product-budgets-v1.json`, `sidecar-lifecycle-v1.md`, and the GJ-02/startup portions of `evidence-adoption-v1.md`. The remaining adoption record, evidence manifest, ordered-trial, and non-startup sidecar-provenance shapes are downstream or orthogonal to WP05.

1. Dead code: **N/A (staged capability)** — WP07/WP08 are declared consumers.
2. Synthetic fixture: **FAIL** — existing tests do not attack restore-to-admission revision drift or key/source mismatch; both reviewer mutants reached false readiness.
3. Silent empty return: **PASS** — malformed operation and restore values fail loudly on the covered paths.
4. FR coverage: **FAIL** — FR-004/FR-016/FR-018/FR-020/FR-022 and C-009/C-011 lack closed behavior at the two cuts above.
5. Frozen surface: **PASS** — the exact three-method restore seam and sealed journal/checkpoint capabilities remain intact.
6. Locked decision: **FAIL** — a stale or source-mismatched restored projection can still become ready.
7. Shared-file ownership: **PASS** — repair changes are isolated prerequisite/application commits and no reviewer implementation edits remain.
8. Production fragility: **PASS** — no panic-style or transient crash path was found in the reviewed repair.

Process-scope note: the revocation registry is in-memory and therefore cannot revoke a service retained in a different OS process. The same-process WP05 race contract is satisfied; WP07/WP10 must either prove the product-host model excludes simultaneous application processes or add an enforceable composition boundary before claiming cross-process predecessor invalidation.
