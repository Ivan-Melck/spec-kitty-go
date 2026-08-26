---
affected_files: []
cycle_number: 3
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T18:29:06Z'
reviewer_agent: user
wp_id: WP08
---

# WP08 Review Feedback — Cycle 3 Changes Requested

Candidate: `f3a4c11fc636f6c994349b7cdbe0bf831635662d`

This correction closes most cycle-2 findings. The shared dishonest factory now exposes the real `*beads.BoundAdapter` for every typed port, twelve method-deletion controls prove those ports are load-bearing, the 25-case evidence is populated from actual typed operations and observed mutation state, the explicit entered/cancel barriers are stable under load, strict-script controls are substantially complete, and the 100-run production-saga campaign plus its unsafe overwrite mutant are real. One prior architectural blocker remains, and one required unsafe mutant is still only illustrative.

## 1. Blocker — the provider fault and universal authority audit still use different topologies

`TestBeadsFaultRegistryExactCensus` creates one `authorityFixture` and one stale authority before iterating all 25 cases (`internal/scenario/beads_fault_test.go:375-376`). Each actual fault then runs through `runNamedReadFault` or an apply-timeout helper, all of which create a separate dishonest-provider fixture beneath their own `t.TempDir`. None receives the `workspaceScenario`, its control store, its tracker path, its repository, its pinned work, or its lease/workspace facts.

The new `faultAuthorityAudit.run("before"/"after")` calls are measured rather than literal, but both calls inspect only the separate shared `authorityFixture`. A provider fault could mutate or corrupt its own relevant authority/control-store topology while this audit remains green, because the audited database/repository/tracker never experienced that fault. Reusing one canary before and after every unrelated case is the same causal disconnect identified in cycle 2.

The human campaign is materially better: its production `TrackerSaga`, external effect, provider store, and universal audit now share the same SQLite/fake-tracker files. However, the lease/workspace expectations and stale probe still belong to the unrelated `work-phase1-matrix-stale`, not the repetition's `work-human-*`. Thus attribution evidence is causal, while the claimed per-repetition lease/workspace authority evidence remains a canary.

Remediation:

- Create/provision the authority scenario per applicable case (and per human repetition, unless a rigorously isolated per-case subdivision is introduced).
- Route the tracker fault/recovery through the same scenario's provider and control-store boundary, or otherwise make the affected work/run/lease/workspace facts the exact ones included in `universalAssertionState`.
- Derive before/after expected authority and attribution facts from that case's state; do not borrow them from a separate work item simply because the files are co-located.
- Add a topology mutant that corrupts the fault case's own lease/workspace/attribution fact while leaving a separate canary untouched. The case must fail. That mutant will prove the coupling, not merely the callback wiring.

## 2. High — the stale-trust mutant is not killed by a safety assertion

`TestBeadsUnsafeDecisionMutantsAreKilled/trust-stale-response` reads the real stale response, assigns its provider token to `unsafeTrusted`, and verifies only that it differs from the persisted `truth_revision`. That is the normal stale fixture behavior and is already covered elsewhere. No production decision or shared evidence validator accepts/rejects `unsafeTrusted`, so a downstream implementation that actually treats the stale token as current truth is not made red by this subtest.

Make the unsafe path feed the same reconciliation/evidence assertion used by the real stale case and require that assertion to reject the trusted-stale decision. The timeout-as-not-applied and blind-retry controls already use this load-bearing pattern; apply it here as well. The warning control would also be stronger if it used the real decision validator rather than manufacturing `unsafeErr` locally.

## 3. Medium — the out-of-map shared-file edit lacks an explicit coordination record

The candidate modifies `internal/scenario/universal_assertions.go`, which is outside WP08's declared owned files. The change is small and plausibly necessary, but commit `f3a4c11` contains no explicit coordination rationale naming the file or cross-package ownership. Record that coordination in the implementation/move-task note when correcting the causal topology, per the shared-file ownership gate.

## Verified closures and evidence

- Shared fake/dishonest semantics execute the same `conformance.Case.Exercise` functions; the dishonest fixture supplies real `BoundAdapter` ports rather than fake/manual behavior.
- Twelve deletion controls cover `ReadWork`, `CreateWork`, `UpdateWork`, dependency put/remove/list, readiness, discussion append/list, and assignment observe/project/compensate.
- All five apply-before-loss cases execute typed adapter operations, observe provider state before retry, and assert one mutation invocation.
- Exact 25-case census fields come from typed errors and runtime mutation observations rather than the old ID-only summary table.
- Explicit before-apply and after-apply barriers eliminate the previous 100 ms setup/preflight race.
- Strict decoder/runtime tests reject schema version drift, unknown scenario/step fields, duplicate IDs, unknown subactions, extra argv, environment/workspace mismatch, argv mismatch, exhaustion, and unknown actions.
- The 100-run campaign uses `execution.NewTrackerSaga`, persistent SQLite close/reopen evidence, deterministic channels, zero compensation writes, and a real unsafe post-attribution provider mutant.

## Verification performed

- `go test ./internal/tracker/conformance -count=10` — pass.
- Formerly flaky combined shared/deletion/fault/protocol/apply-timeout/mutant suite, `-count=3` — pass, approximately 159 seconds.
- `TestBeadsDishonestExplicitBarrierSurvivesSchedulerLoad -count=10` — pass, 200 loaded barrier repetitions, approximately 55 seconds.
- Human 100-run campaign plus overwrite mutant — pass, approximately 226 seconds.
- Focused `-race` over durable fake, shared dishonest, exact 25-case census, 100-run campaign, and overwrite mutant — pass, approximately 342 seconds.
- `go vet ./internal/tracker/conformance ./internal/scenario` — pass.
- `go test ./internal/architecture -count=1` — pass.
- `git diff f3a4c11^ f3a4c11 --check` — pass.

## Anti-pattern checklist

1. Dead code: **PASS** — added quality/test seams have live callers.
2. Synthetic-fixture test: **FAIL** — authority results audit a separate canary topology rather than the topology that experienced each provider fault.
3. Silent empty return: **PASS** — no unexplained silent empty-return path found.
4. FR coverage: **FAIL** — FR-018/FR-022 still lack causally coupled per-fault authority evidence; the stale-trust unsafe decision is not rejected by a shared assertion.
5. Frozen surface: **PASS** — no frozen contract artifact changed.
6. Locked decision: **PASS** — no provider-name authority switch, human-output parsing, false CAS, or unsafe retry entered production.
7. Shared-file ownership: **FAIL** — `internal/scenario/universal_assertions.go` changed without the required explicit coordination note.
8. Production fragility: **N/A** — changes are quality/test tooling.
