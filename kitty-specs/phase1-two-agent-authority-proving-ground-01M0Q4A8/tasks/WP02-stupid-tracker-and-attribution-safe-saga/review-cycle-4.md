---
affected_files: []
cycle_number: 4
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command:
reviewed_at: '2026-08-24T10:03:37Z'
reviewer_agent: unknown
verdict: rejected
wp_id: WP02
---

---
affected_files:
- internal/execution/external_effect.go
- internal/controlstore/sqlite/external_effect.go
- internal/scenario/tracker_saga_test.go
cycle_number: 3
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./internal/execution ./internal/controlstore/sqlite -run '^TestCycle3' -count=1 -v
reviewed_at: '2026-08-24T10:02:41Z'
reviewer_agent: mini-local
verdict: rejected
wp_id: WP02
---

# WP02 Review Cycle 3 — Rejected

Reviewer: `mini-local` using Reviewer Renata

Candidate implementation: `e63dd0acdfb2186fb72efdf18aecfbe9b016e3ac`

Remediation commits: `5e620bb`, `169a8c7`, `e63dd0a`

## Verdict

**REJECT.** The five cycle-1 findings are substantially remediated: immutable pending recovery, named process boundaries, provider refusal envelopes, comment markers, and v4 migration now pass adversarial and mutation checks. Approval remains blocked because control-repository failures still bypass the mandatory typed command envelope, and the append-only observation ledger records false provider state after compensation.

## Findings

### High — Control-repository failures still escape the typed command diagnostic contract

`internal/execution/external_effect.go` returns repository errors directly from both command paths and from every external-effect save. Representative sites are assignment read at lines 145-146, initial pending save at lines 167-168, confirmed/observed saves at lines 182-183 and 225-226, compensation read at lines 244-249, and compensation saves at lines 269-270, 284-285, 297-298, and 304-305.

An exact-`e63dd0ac` review repository injected ordinary failures into assignment read, assignment pending save, and compensation read. All three escaped as raw `*errors.errorString` rather than `*execution.DiagnosticError`:

```text
assign repository read: failure escaped typed diagnostics: *errors.errorString control read failed
assign pending save: failure escaped typed diagnostics: *errors.errorString control save failed
compensate repository read: failure escaped typed diagnostics: *errors.errorString control read failed
```

This leaves the helper's `encodeTrackerFailure` to emit an unstructured `message` and exit 1, losing schema version, correlation/operation IDs, actor/process attribution, injected timestamp, expected provider revision, observed/expected state, retry safety, and recovery actions. It violates the command/error envelope contract and NFR-006/NFR-009.

Wrap every repository read/save failure at the saga boundary with a complete command diagnostic while preserving the original error as `Cause`. Add table-driven assignment and compensation coverage for initial read, pending save, post-provider observation save, ambiguity save, conflict save, and compensation save/replay failures, plus helper JSON assertions. Fault-controller crash sentinels may remain internal control flow handled explicitly by the helper.

### High — Successful compensation appends stale pre-compensation provider reality

`internal/controlstore/sqlite/external_effect.go:51-57` derives every non-conflict observation from `AppliedRevision`, `AppliedValue`, and the assignment marker. It does not branch on `CompensationStatus` or use `CompensationRevision`. After a successful compensation from revision 2/`agent-a` to revision 3/`original`, the durable observation row therefore claims revision 2, value `agent-a`, and the old operation marker.

An exact-candidate review probe saved a completed effect and queried the last append-only observation:

```text
compensation observation records stale applied state:
revision=2 value="agent-a" marker="operation-observation"
```

The current `external_effects` row contains `compensation_revision=3`, so this is specifically a false append-only observation, not merely absent evidence. It violates T007's durable observation requirement and the data-model rule that stages are append-only observations of proven durable boundaries. Ambiguous compensation is also currently serialized as if the original applied state were an observed fact.

Record the compensated revision/value/marker for completed compensation, and represent ambiguous compensation without asserting an unobserved provider fact. Add assertions over the observation ledger after successful, repeated, attribution-lost, unavailable, and timeout-after compensation.

## Prior finding disposition

- Pending post-provider recovery: **cleared**. Fail-second-save replay preserves prior revision 1/value `original`, confirms applied revision 2, and compensates to revision 3/value `original`.
- SR-03/SR-04 production-helper process proof: **cleared**. Both named boundaries exit helper process 86, reopen both stores, replay the same operation, preserve immutable evidence, and compensate safely.
- Provider refusal and command attribution: **partially cleared**. Assign/provider/refusal and validation paths now carry actor/process and other envelope fields, but repository failures remain raw as described above.
- Comment marker fidelity: **cleared**. Fresh and replayed comments return their own marker.
- Versioned schema: **cleared**. Lazy DDL is removed; exact empty and genuinely used v3 databases migrate to v4, preserve authority state, and enforce immutable effect evidence.

## Validation evidence

- `go test -count=1 ./internal/tracker/... ./internal/execution ./internal/controlstore/sqlite` — PASS (`sqlite` 3.832s).
- `go test -count=5 ./internal/scenario -run 'TestCycle1|TrackerSaga'` — PASS (12.217s).
- `go test -count=1 ./...` — PASS (scenario 13.952s).
- `go test -race -count=1 ./...` — PASS (scenario 16.735s; no race report).
- `go test ./internal/scenario -run '^TestTrackerSaga' -count=100` — PASS (7.600s).
- `go test ./internal/controlstore/sqlite -run 'TestCycle1ExternalEffect(MigrationFromExactPriorVersion|PriorEvidenceIsImmutable)$' -count=20` — PASS (1.220s).
- Review probe: genuinely used v3 database with an existing operation and token key migrates to v4, preserves the operation, and rejects prior-evidence rewrite — PASS.
- `go test ./internal/scenario -run '^TestWP01ConcurrentFirstOpenConvergesAcrossProcesses$' -count=3` — PASS (6.319s; 32 synchronized processes per run).
- `go vet ./...` — PASS.
- `staticcheck ./...` — PASS.
- `git diff --check kitty/mission-phase1-two-agent-authority-proving-ground-01M0Q4A8..e63dd0ac` — PASS.
- Review-only repository failure diagnostics — **FAIL** on assign read, assign save, and compensate read.
- Review-only compensated observation fidelity — **FAIL**, recording revision 2/`agent-a`/old marker instead of revision 3/`original`/cleared marker.

## Mutation sensitivity

- Delete the initial pending `SaveExternalEffect`: **KILLED** by both fail-second-save recovery and SR-03/SR-04 committed scenarios.
- Delete comment-marker override: **KILLED** by `TestProviderPersistsWorkAndCASMutations`.
- Disable v4 migration: **KILLED** for both empty and used v3 cases.
- Delete actor attribution from tracker aggregates: **KILLED** across the provider-refusal table.
- Bypass pending-effect recovery: covered by the fail-second-save and SR-04 reopen scenarios.

## Anti-pattern checklist

1. Dead code: **PASS** — the production helper dispatches `RunTracker`, which constructs and invokes the saga/provider path.
2. Synthetic-fixture test: **PASS** — SR-03/SR-04 scenarios invoke the production helper in independent OS processes and reopen real stores.
3. Silent empty return: **PASS** — reviewed no-op returns are explicit idempotent provider results.
4. FR coverage: **FAIL** — durable compensation observations and all typed failure results are not correctly covered.
5. Frozen surface: **N/A** — no WP02 frozen file is identified.
6. Locked decision: **FAIL** — the append-only observation ledger contradicts proven durable provider reality after compensation.
7. Shared-file ownership: **PASS** — the small root dispatch and v4 migration crossings are required integration points and their commit subjects state the rationale.
8. Production fragility: **FAIL** — transient repository failures leak untyped through the helper instead of carrying recovery semantics.

## Scope and passing boundaries

The provider remains narrow and does not import lease, mission, workspace, Git, review, or integration concepts. Its store is separately durable and process-safe. Tracker ownership never creates or extends execution authority, compare-before-compensate preserves later human assignment, repeated compensation remains a no-op, and the new shared root/migration changes are localized to required dispatch and schema integration.
