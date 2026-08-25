---
affected_files:
- internal/controlstore/sqlite/external_effect.go
- internal/execution/external_effect.go
- internal/tracker/fake/provider.go
- internal/controlstore/sqlite/external_effect_test.go
cycle_number: 5
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./internal/controlstore/sqlite -run '^TestCycle5' -count=1 -v
reviewed_at: '2026-08-24T10:17:55Z'
reviewer_agent: mini-local
verdict: rejected
wp_id: WP02
---

# WP02 Review Cycle 5 — Rejected

Reviewer: `mini-local` using Reviewer Renata

Candidate implementation: `c7363446d6768d39ebfa822e61ebcbcc42ce90cb`

Remediation commit: `c7363446`

## Verdict

**REJECT.** Both direct cycle-3 defects are fixed: every saga repository read/save failure is now a complete typed command diagnostic, helper JSON preserves that envelope, and ordinary successful/repeated compensation records proven provider state. Two uncovered recovery states still make the append-only observation ledger untruthful: assignment ambiguity/unavailability invents the prior provider state, and compensation replay after a lost control save plus a human reassignment combines a historical revision with the human's newer value.

## Findings

### High — Assignment ambiguous/unavailable observations still invent provider state

`internal/controlstore/sqlite/external_effect.go:51-68` special-cases ambiguous and unavailable **compensation**, but not ambiguous or unavailable assignment responses. When `ResponseStatus` is `ambiguous` or `unavailable`, `CompensationStatus` remains `pending` and `AppliedRevision` is zero, so the final switch case serializes `PriorRevision`, `PriorValue`, and an empty marker as though they were observed after the provider request.

An exact-`c7363446` probe stored both unknown outcomes and read the append-only rows:

```text
ambiguous invented provider observation: revision=1 value="original" marker=""
unavailable invented provider observation: revision=1 value="original" marker=""
```

For timeout-after-apply, the real provider is revision 2/`agent-a`/operation marker, proving the durable row false. Timeout-before and unavailable also do not prove that the prior state remained unchanged after the request. This violates T007 and the data-model rule that operation stages are append-only observations of proven durable boundaries.

Represent unknown assignment provider state explicitly, just as the remediation now does for ambiguous/unavailable compensation. Add store and saga assertions for timeout-before, timeout-after, and unavailable assignment rows; the test must distinguish the durable pending pre-call observation from the later unknown response observation.

### High — Replayed compensation can persist an impossible revision/value tuple after a later human change

If the provider completes compensation but the control-store save fails, `ExternalEffect` remains compensation-pending. A later human reassignment advances the provider again. On replay, `internal/tracker/fake/provider.go:131-135` finds the compensated marker and returns the historical compensation revision from `state.Compensated`, but takes assignee and marker from the **current** work. `TrackerSaga.Compensate` copies that mixed `Mutation` into the observation fields at `internal/execution/external_effect.go:271-278`.

An exact-candidate adversarial sequence performed assignment, compensation with a failed control save, human reassignment, then same-operation compensation replay. The human assignment was correctly preserved, but the durable observation was internally impossible:

```text
observation=(3,"human-owner","") current=(4,"human-owner","")
```

Revision 3 was the successful compensation to `original`; `human-owner` exists only at revision 4. The committed repeated-compensation test short-circuits from a control record already marked done, so it never exercises provider replay after lost control acknowledgment.

Make provider replay return one coherent proven state—either the historical compensation mutation or a fresh current provider observation—and persist that same revision/value/marker tuple. Add the exact lost-save → human-change → replay scenario, assert no human overwrite, and compare the appended observation to provider history/current reality.

## Cycle-3 blocker disposition

- Repository failure envelopes: **cleared**. Assignment and compensation cover initial read plus every pending, confirmed, ambiguous, unavailable, conflict, attribution-loss, and replay save path. Diagnostics preserve cause, schema, correlation/operation, actor/process, timestamp, expected revision, observed/expected state, retry safety, and recovery actions.
- Helper JSON: **cleared**. Typed repository diagnostics encode in `TrackerResponse.Error` with no raw `Message` fallback.
- Ordinary compensation truth: **cleared**. Fresh success records the provider mutation's revision/value/marker; already-recorded repeated compensation adds no observation; attribution-lost records the human state; ambiguous/unavailable compensation records unknown fields.

## Prior contract regression checks

- Fail-second-save pending recovery and immutable prior evidence: PASS.
- Real helper-process SR-03/SR-04 reopen/replay/compensation: PASS ×10.
- Comment operation markers and repeated compensation no-op: PASS ×20.
- Versioned v3→v4 migration and immutable evidence: PASS ×10.
- Provider remains separate from SQLite and contains no lease, mission, workspace, Git, review, or integration vocabulary.
- Tracker mutations do not create or extend execution authority.

## Validation evidence

- `go test -count=1 ./internal/tracker/... ./internal/execution ./internal/controlstore/sqlite ./internal/helpercmd` — PASS (`sqlite` 4.348s).
- `go test -count=5 ./internal/scenario -run 'TestCycle1|TrackerSaga'` — PASS (12.606s).
- `go test -count=1 ./...` — PASS (scenario 14.775s).
- `go test -race -count=1 ./...` — PASS (scenario 16.886s; no race report).
- `go test ./internal/scenario -run '^TestTrackerSaga' -count=100` — PASS (9.692s).
- `go test ./internal/scenario -run 'TestCycle1(PostProviderApplyPreObservationRecoveryPreservesCompensationEvidence|NamedTrackerCrashBoundariesRecoverAcrossProcesses)$' -count=10` — PASS (22.023s).
- `go test ./internal/controlstore/sqlite -run 'TestCycle1ExternalEffect(MigrationFromExactPriorVersion|PriorEvidenceIsImmutable)$' -count=10` — PASS.
- `go test ./internal/execution ./internal/helpercmd -run '^TestCycle3' -count=20` — PASS.
- `go test ./internal/scenario -run '^TestWP01ConcurrentFirstOpenConvergesAcrossProcesses$' -count=2` — PASS (32 processes/run).
- `go vet ./...` — PASS.
- `staticcheck ./...` — PASS.
- `git diff --check kitty/mission-phase1-two-agent-authority-proving-ground-01M0Q4A8..c7363446` — PASS.
- Exact-candidate unknown assignment observation probe — **FAIL** for ambiguous and unavailable.
- Exact-candidate lost compensation-save/human-change/replay probe — **FAIL** with revision 3 paired to revision-4 value.

## Mutation sensitivity

- Return raw repository read error instead of `repositoryDiagnostic`: **KILLED** by `TestCycle3AssignRepositoryFailuresAreCompleteDiagnostics`.
- Return raw repository save errors: **KILLED** across all assignment and compensation save-path cases.
- Delete actual provider observation propagation from successful compensation: **KILLED** by `TestCycle3SuccessfulCompensationCarriesActualProviderObservation`.
- The two findings above are surviving semantic mutants in the committed matrix: assignment unknown outcomes are not asserted, and repeated compensation never reaches the provider after a lost control save.

## Anti-pattern checklist

1. Dead code: **PASS** — the production helper consumes the saga/provider/repository path.
2. Synthetic-fixture test: **PASS** — crash and saga tests invoke production code and independent helper processes.
3. Silent empty return: **PASS** — empty provider fields are explicit unknown-state encoding or idempotent no-op results.
4. FR coverage: **FAIL** — T007/FR-019 durable observation truth is not covered for the two recovery sequences above.
5. Frozen surface: **N/A** — no WP02 frozen file is identified.
6. Locked decision: **FAIL** — append-only observations must be proven facts, not mixed or inferred state.
7. Shared-file ownership: **PASS** — this remediation stays inside WP02 implementation/test files.
8. Production fragility: **FAIL** — recovery can durably publish impossible provider evidence even while preserving business state.
