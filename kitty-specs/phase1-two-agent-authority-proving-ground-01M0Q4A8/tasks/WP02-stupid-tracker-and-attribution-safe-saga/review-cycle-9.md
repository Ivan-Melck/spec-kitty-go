---
affected_files:
- internal/tracker/fake/provider.go
- internal/tracker/fake/provider_integrity_test.go
- internal/tracker/fake/provider_test.go
- internal/controlstore/sqlite/external_effect_test.go
cycle_number: 9
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./internal/tracker/... ./internal/execution -count=20
reviewed_at: '2026-08-24T10:46:58Z'
reviewer_agent: mini-local
verdict: approved
wp_id: WP02
---

# WP02 Review Cycle 9 — Approved

Reviewer: `mini-local` using Reviewer Renata

Candidate implementation: `5a67743b89976e91879d5e04b029eb31f9b92b8d`

Remediation commit: `5a67743`

## Verdict

**APPROVE.** The final replay-provenance defect is closed without weakening compare-before-compensate. Completed compensation replay now requires an adjacent assignment→compensation revision relation, matching work key, marker, assignment value, and restore value, and a complete scan that rejects conflicting duplicate or same-revision evidence. Exact and identical duplicate evidence remains idempotently replayable. Missing, altered, non-adjacent, or conflicting evidence fails with attribution lost; the saga records decision evidence, never records `CompensationDone`, and preserves a later human reassignment.

## Final blocker disposition

- Altered compensation value: **cleared** — rejected across reopen; integrated saga records attribution lost and no compensation-done observation.
- Duplicate conflicting compensation: **cleared** — complete history scan rejects it rather than accepting the first authentic row.
- Missing compensation history: **cleared** — typed attribution loss.
- Identical duplicate compensation evidence: **cleared** — accepted as one coherent idempotent provider state.
- Assignment provenance: **cleared** — missing, altered, conflicting duplicate, or wrong-revision assignment evidence is rejected.
- Restore provenance: **cleared** — history must equal the immutable replay request; changed request or history is rejected.
- Revision provenance: **cleared** — compensation must be the immediate causal successor of the attributable assignment.
- Reopen and lost-save + intervening human reassignment: **cleared** — valid replay returns revision 3/`original`/empty marker while provider remains revision 4/`human-owner`; corrupt replay cannot publish `CompensationDone`.

## Cumulative contract verification

- Fake-provider durability remains separate from SQLite; no fake type leaks into the execution provider port.
- Assignment/comment CAS, opaque markers, timeout-before, timeout-after, unavailable, revision-conflict, and same-operation replay remain intact.
- Assignment ambiguous/unavailable response observations retain immutable prior ledger fields while encoding provider observation as explicit unknown, including close/reopen.
- Compare-before-compensate preserves later human state and repeated successful compensation is a no-op.
- Every execution-repository and provider refusal path returns a complete `DiagnosticError`; helper JSON retains schema, correlation/operation, actor/process, timestamp, revisions, observed/expected state, retry safety, and recovery actions.
- Real helper-process SR-03/SR-04 crash recovery reopens and continues the production saga safely.
- Exact empty and used v3 stores migrate to v4 with immutable prior evidence and without lazy DDL.
- Tracker assignment never creates, extends, or validates execution authority.
- Relevant contract examples in `contracts/provider-contract.md` and `contracts/commands-and-results.md` match the implementation and tests.

## Validation evidence

- `go test ./...` — PASS (scenario 14.765s).
- `go test -race ./...` — PASS (scenario 16.571s; no race report).
- `go vet ./...` — PASS.
- `staticcheck ./...` — PASS.
- `go test ./internal/tracker/... ./internal/execution -count=20` — PASS.
- `go test ./internal/tracker/fake -run 'TestReviewCycle7' -count=50` — PASS.
- Exact-candidate additional provenance matrix (missing assignment, non-adjacent compensation, conflicting assignment duplicate, same-marker foreign event, exact and identical duplicate evidence) ×20 — PASS.
- `go test ./internal/controlstore/sqlite -run 'TestReviewCycle7|TestCycle5|TestCycle3|TestCycle1ExternalEffectMigration' -count=20` — PASS.
- `go test ./internal/scenario -run 'TestTrackerSaga' -count=100` — PASS.
- Real-process SR-03/SR-04 ×10 — PASS.
- `go test ./internal/helpercmd ./internal/execution -count=20` — PASS.
- Independent-process concurrent first-open, 32 processes/run ×2 — PASS.
- `git diff --check kitty/mission-phase1-two-agent-authority-proving-ground-01M0Q4A8..5a67743b89976e91879d5e04b029eb31f9b92b8d` — PASS.

## Mutation sensitivity

- Skip assignment→compensation adjacency/revision relation: **KILLED** by the non-adjacent provenance probe.
- Skip assignment history existence/value/marker checks: **KILLED** by committed altered-assignment and review-only missing-assignment probes.
- Skip compensation restore-value comparison: **KILLED** by committed altered-history and altered-replay-request probes.
- Return after the first matching compensation and skip the rest of history: **KILLED** by committed duplicate-conflict probe.
- Delete assignment unknown-state serialization: **KILLED** for timeout-before, timeout-after, and unavailable.
- Reconstruct replay from current work rather than historical mutation: **KILLED** by the lost-save + human-reassignment tests.

## Anti-pattern checklist

1. Dead code: **PASS** — the production helper consumes the provider, saga, and repository paths.
2. Synthetic-fixture test: **PASS** — contract scenarios invoke production provider/saga/store code; crash tests use independent OS processes.
3. Silent empty return: **PASS** — empty observation fields are explicit unknown-state encoding and asserted.
4. FR coverage: **PASS** — FR-004/015/016/018/019 and listed constraints/NFRs have behavioral assertions.
5. Frozen surface: **N/A** — no WP02 frozen surface is identified.
6. Locked decision: **PASS** — provider attribution remains informational and compensation is evidence-gated.
7. Shared-file ownership: **PASS** — remediation changes are localized to provider code and directly paired saga/provider tests.
8. Production fragility: **PASS** — corrupt recovery fails loudly into typed decision-required handling without mutating provider state.
