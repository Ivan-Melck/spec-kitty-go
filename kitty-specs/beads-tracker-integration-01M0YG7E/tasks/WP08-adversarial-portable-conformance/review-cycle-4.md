---
affected_files: []
cycle_number: 4
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T19:22:01Z'
reviewer_agent: user
wp_id: WP08
---

# WP08 Review Feedback — Cycle 4 Changes Requested

Candidate: `e7995c77afe9bcbb643a044dafe13351a20ea127`

This correction closes the human-repetition topology, shared-validator, shared-file-ownership, and case-specific local-authority portions of cycle 3. The exact census and all focused gates pass. One part of the prior blocker remains: the dishonest provider that experiences a fault is still not the provider inspected by the universal assertion, and the new mutant does not make that connection load-bearing.

## Blocker — the provider side of the case topology is still asserted in parallel

For an applicable census case, `providerRoot` is computed beneath the case-owned stale workspace and assigned independently to `audit.providerRoot` (`internal/scenario/beads_fault_test.go:385-397`). The same string is separately passed to the dishonest helper (`:405-418`). `faultAuthorityAudit.run` proves only that this declared path is lexically beneath the workspace (`:549-554`); it neither derives that path from the adapter operation nor observes the dishonest provider state. The `universalAssertionState` it then audits still carries `TrackerPath: fixture.tracker`, the durable-fake store created by `newWorkspaceScenario`, and `assertAttributionFacts` opens that fake store (`internal/scenario/universal_assertions.go:21-35,95-96,285-290`). The actual fault executes against the different dishonest store rooted at `.wp08-provider`.

Consequently, deleting the root argument from `runNamedReadFault`/an apply-timeout helper (their variadic API then allocates an unrelated `t.TempDir`) does not make either universal audit red: `audit.providerRoot` still contains the expected string, while the operation silently moved elsewhere. That is the exact disconnected-provider mutation this correction must kill.

The new canary mutant (`internal/scenario/beads_fault_test.go:456-494`) is valuable but proves a different fact. It manually corrupts the case control database's workspace row after the provider call. The provider call can be deleted or routed to another root and the mutant still behaves identically: the separate canary remains green and the case audit detects `immutable_workspace_binding`. Thus it proves case-owned local authority, not that the audited topology is the one whose provider experienced the fault.

Remediation:

- Derive the provider binding/root evidence from the fixture or typed operation result that actually executed the fault; do not assign an expected root independently beside the call.
- Make the before/after audit consume a provider probe or provider-binding fact for that exact dishonest adapter/store. For mutating cases, include the observed mutation/attribution state; for read-only cases, at least prove the actual binding identity and consumed runtime belong to the lease-scoped root.
- Add a deletion mutant that routes the real fault to an unrelated temporary provider root while leaving the declared lease-scoped path and separate canary healthy. The audit/census must fail with the affected case ID and a provider-topology invariant. Retain the current database-corruption mutant as the independent local-authority control.

The human campaign does not have this defect. Each repetition now provisions its own work/run/lease/workspace subdivision and uses the same `authorityFixture.tracker` and `authorityFixture.database` for the production `TrackerSaga`, durable conflict record, provider observation, and universal assertion.

## Verified closures and evidence

- Each applicable fault provisions distinct work/run/lease/workspace facts; before/after local-authority audits no longer borrow one shared stale canary.
- Each human repetition uses its own work, lane, run, lease, workspace, provider key, operation, actor, and process within the shared rigorously partitioned scenario.
- The disconnected local-workspace mutant corrupts the affected database while a separate canary stays green.
- Real stale evidence and the stale-trust mutant both pass through `validateProviderObservationDecision`.
- Real warning success and the warning-prose mutant both pass through `validateProtocolDecision`.
- Commit `e7995c77` explicitly records the coordinated `internal/scenario/universal_assertions.go` crossing and rationale.
- Intent-qualified effect aggregation by canonical provider key is exercised by the exact human campaign.

## Verification performed

- `go test ./internal/scenario -run '^TestBeadsFaultRegistryExactCensus$' -count=1 -timeout=12m` — pass, approximately 101 seconds.
- Topology and unsafe-decision mutants, `-count=5` — pass, approximately 53 seconds.
- The same topology and unsafe-decision mutants under `-race` — pass, approximately 13 seconds.
- `go test ./internal/scenario -run '^TestBeadsHumanAssignmentPreservation100$' -count=1 -timeout=12m` — pass, exactly 100/100, approximately 558 seconds.
- Human post-attribution overwrite mutant under `-race -count=3` — pass.
- A three-subtest filter was intentionally rejected by the exact 100-case census; the full campaign above is the admissible result.
- `go vet ./internal/tracker/conformance ./internal/scenario` — pass.
- `go test ./internal/architecture -count=1` — pass.
- Candidate/current implementation diff check — pass; no implementation drift after the candidate commit.
- Lane status contains only the pre-existing untracked `.spec-kitty/` directory.

## Anti-pattern checklist

1. Dead code: **PASS** — no new production public surface; added test helpers have live callers.
2. Synthetic-fixture test: **FAIL** — the provider-root expectation and provider operation are parallel inputs; deleting/rerouting the operation's root does not fail the topology audit.
3. Silent empty return: **PASS** — no new silent empty-return production path.
4. FR coverage: **FAIL** — FR-018/FR-022 local-authority facts are now exact, but the required exact provider-to-authority topology is not load-bearing.
5. Frozen surface: **PASS** — no frozen contract surface changed.
6. Locked decision: **PASS** — no human-output parsing, provider authority leakage, unsafe retry, or false CAS entered production.
7. Shared-file ownership: **PASS** — the out-of-map shared-file edit has an explicit coordination rationale in the candidate commit.
8. Production fragility: **N/A** — the correction changes scenario/conformance test surfaces only.
