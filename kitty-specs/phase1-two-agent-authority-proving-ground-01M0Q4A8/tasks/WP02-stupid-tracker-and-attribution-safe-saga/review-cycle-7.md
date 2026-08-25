---
affected_files:
- internal/tracker/fake/provider.go
- internal/tracker/fake/store.go
- internal/tracker/fake/provider_test.go
cycle_number: 7
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./internal/tracker/fake -run '^TestReviewCycle7' -count=1 -v
reviewed_at: '2026-08-24T10:32:08Z'
reviewer_agent: mini-local
verdict: rejected
wp_id: WP02
---

# WP02 Review Cycle 7 — Rejected

Reviewer: `mini-local` using Reviewer Renata

Candidate implementation: `1f8a83fcc1ba59bf19125193f88d2e9eac94406a`

Remediation commit: `1f8a83f`

## Verdict

**REJECT.** Candidate `1f8a83f` clears both cycle-5 defects in ordinary durable state: assignment ambiguity/unavailability persists explicit unknown observation fields across reopen, and lost-save compensation replay after a later human reassignment returns the historical compensation tuple without changing the human state. However, the fake provider still treats the first matching mutable history row as authoritative without validating its value against the same operation's durable compensation request or rejecting duplicate conflicting rows. Altered or ambiguous provider history therefore becomes a fabricated successful replay and can be persisted by the saga as a provider state that never occurred.

## Finding

### High — Compensation replay trusts altered or duplicate history and fabricates a successful provider tuple

`internal/tracker/fake/provider.go:133-139` treats the `Compensated` marker as proof that a compensation happened and delegates recovery to `historicalCompensation`. At `internal/tracker/fake/provider.go:177-184`, that helper reverse-scans for one matching kind/work-key/marker/revision and returns its `Value` without checking that it equals the durable `CompensationRequest.RestoreAssignee`, that exactly one matching row exists, or that the compensation record and history form one coherent mutation.

An exact-commit review probe created assignment revision 2, compensation revision 3 restoring `original`, and an intervening human reassignment revision 4. It then exercised three reopened-history conditions:

- missing compensation history: correctly returned `tracker.ErrorAttributionLost` and preserved the human state;
- compensation history value altered from `original` to `forged-owner`: incorrectly returned success with `{Revision:3, Assignee:"forged-owner", Replayed:true, Noop:true}`;
- a second conflicting compensation row appended for the same work/marker/revision: incorrectly selected the last row and returned the same fabricated success.

The durable request still proves that revision 3 was intended to restore `original`; no provider mutation ever assigned `forged-owner`. Because `TrackerSaga.Compensate` persists the provider replay mutation on a lost-save retry, the accepted result can turn corrupted/ambiguous fake-provider history into a false append-only control-store observation. The later human state remains revision 4/`human-owner`, so compare-before-compensate safety is preserved, but diagnostic and ledger truthfulness are not.

Make the provider's durable idempotency record self-sufficient for replay, or validate recovered history against all immutable operation evidence. At minimum, reject with `ErrorAttributionLost` when the recovered compensation value differs from `request.RestoreAssignee`, when multiple matching rows disagree, or when the compensated record and history cannot prove exactly one coherent mutation. Add provider and lost-control-save saga tests for missing, altered, and duplicate/incoherent history; assert no human mutation and no fabricated `CompensationDone` observation.

## Prior blocker disposition

- Assignment ambiguous/unavailable observation truth, including close/reopen: **cleared**. Pending prior evidence remains immutable while the response observation uses revision `0`, value `""`, and marker `""` for timeout-before, timeout-after, and unavailable.
- Normal lost-save compensation replay after intervening human change: **cleared**. Replay returns revision 3/`original`/empty marker, the ledger persists that exact historical tuple, and the provider remains revision 4/`human-owner`.
- Missing compensation history: **cleared safely** with `ErrorAttributionLost`.
- Altered or duplicate conflicting compensation history: **blocking**, as above.

## Cumulative contract checks

- Complete `DiagnosticError` envelopes for assignment/compensation repository failures and provider refusals: PASS.
- Helper JSON preserves schema, correlation, operation, actor/process attribution, timestamp, revisions, observed/expected state, retry safety, recovery actions, and cause classification: PASS.
- Real helper-process SR-03/SR-04 reopen/replay/compensation: PASS ×12.
- Versioned v3→v4 empty and used migration with immutable prior evidence and no lazy DDL: PASS ×20.
- Comment markers, same-operation replay, repeated compensation no-op, and provider/control-store durability separation: PASS.
- Tracker ownership remains external attribution only and never creates or extends execution authority: PASS.

## Validation evidence

- `go test ./...` on unmodified candidate — PASS (scenario 14.714s).
- `go test -race ./...` — PASS (scenario 16.386s; no race report).
- `go vet ./...` — PASS.
- `staticcheck ./...` — PASS.
- `go test ./internal/tracker/fake -run 'TestCycle5|TestProviderSuccessfulCompensation|TestProviderRejectsOperationMarker' -count=30` — PASS.
- `go test ./internal/controlstore/sqlite -run 'TestCycle5|TestCycle3|TestCycle1ExternalEffectMigration' -count=20` — PASS.
- `go test ./internal/scenario -run 'TestCycle1NamedTrackerCrashBoundariesRecoverAcrossProcesses' -count=12` — PASS (real helper processes, both SR-03 and SR-04 each run).
- `go test ./internal/scenario -run 'TestTrackerSaga' -count=100` — PASS.
- `go test ./internal/helpercmd ./internal/execution -count=20` — PASS.
- `git diff --check f26c93c214c6568f1abd1147fcc343b5a6d833a7..1f8a83fcc1ba59bf19125193f88d2e9eac94406a` — PASS.
- Exact-commit review-only missing-history probe — PASS: typed attribution loss, human revision 4 preserved.
- Exact-commit review-only altered-history probe — **FAIL**: fabricated successful revision 3/`forged-owner` replay.
- Exact-commit review-only duplicate-conflicting-history probe — **FAIL**: last conflicting row accepted as successful replay.

## Mutation sensitivity

- Delete the assignment unknown-state serialization branch: **KILLED** by `TestCycle5AssignmentUnknownObservationsRemainUnknown` for timeout-before, timeout-after, and unavailable.
- Reconstruct compensated replay from current work instead of historical mutation: **KILLED** by `TestCycle5CompensationReplayReturnsOneHistoricalProviderState`.
- Remove/mangle compensation history: **KILLED** by the attribution-lost path and review probe.
- Alter the matching compensation history value or add a duplicate conflicting row: **SURVIVES** the committed suite and production validation, causing this rejection.

## Anti-pattern checklist

1. Dead code: **PASS** — production helper/scenario paths consume the saga and provider.
2. Synthetic-only evidence: **PASS** — SR-03/SR-04 execute independent OS helper processes.
3. Silent empty return: **PASS** — unknown observation fields are explicit and asserted.
4. FR/T007/T008 coverage: **FAIL** — corrupted or ambiguous replay history can be promoted to false success.
5. Frozen surface: **N/A** — no WP02 frozen file identified.
6. Locked decision: **FAIL** — append-only observations must be proven provider facts.
7. Shared-file ownership: **PASS** — candidate remediation is scoped to WP02 production/tests.
8. Production fragility: **FAIL** — the durable fake lacks coherence validation at its replay recovery boundary.
