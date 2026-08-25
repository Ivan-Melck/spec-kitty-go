---
affected_files: []
cycle_number: 3
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./internal/scenario -run '^TestWorkspaceMaintenanceHelperReportsLiveRefusalsWithoutMutation$' -count=5
reviewed_at: '2026-08-24T16:58:12Z'
reviewer_agent: codex
verdict: approved
wp_id: WP05
---

# WP05 Review Cycle 3 — Approved

Candidate: `b238465245012d6d63e762d06b671884f18209d5`

Lane head `5c5cc74bfb7d247024a7232cb60844b1dd0c78f9` contains only
mission synchronization and bookkeeping after the candidate. A path-scoped
diff confirms that `cmd/`, `internal/`, `go.mod`, and `go.sum` are
identical to the candidate.

## Verdict

The cycle-2 blocker is resolved. `workspace-inspect` and
`workspace-cleanup` are reachable from the shipped helper root and execute
the shared production policy:

- resolve the durable workspace by WorkspaceID or the original OperationID;
- read the exact lease generation/status and immutable repository binding;
- bind and authenticate the live repository adapter against the durable
  canonical common-dir, primary path, managed root, target ref, repository,
  and project facts;
- invoke `Repository.Inspect`, `ValidateCleanup`, and, only on the cleanup
  route after binding validation, `Repository.Cleanup`;
- return complete typed diagnostic JSON while retaining the read-only
  inspection, discrepancy list, and exact preserved commit IDs.

The real helper-process matrix covers active cleanup plus OperationID
resolution, dirty, quarantined, unregistered, branch-drift, head-drift,
path-binding, and unknown-workspace refusals. Each refusal carries the
NFR-006 schema/correlation/operation attribution, relevant actor/process and
workspace aggregates, observed and expected facts, retry safety, recovery
actions, and injected occurrence time. Refused cleanup preserves the
workspace and owned branch evidence.

The redundant `workspace.Service.Quarantine` and
`sqlite.Store.QuarantineIntentCount` APIs are removed. Exact non-test
call-site searches find production callers for every remaining remediation
surface: `WorkspaceLeaseStatus`, `ValidateRepositoryBinding`,
`IsQuarantined`, `ValidateCleanup`, `Repository.Inspect`, and
`Repository.Cleanup`.

## Cumulative acceptance

The prior cycle's cumulative evidence remains valid and bounded smoke was
re-run against the remediation:

- recovery is keyed by the durable OperationID envelope, with immutable
  repository/workspace binding and typed ambiguity decisions;
- expiry is generation-CAS/token fenced, and deadline expiry plus revision
  invalidation share the same workspace quarantine transition;
- SR-00 through SR-09 still run through real helper process boundaries, and
  partial/dependency-conflicted preparation never reports ready;
- the SIGSTOP/SIGCONT writer still commits only in quarantined W1 after
  expiry, while W2 and the primary checkout remain unchanged and stale
  control-plane actions fail;
- shipped v7 migration SQL remains digest-frozen and real v8 remains a
  forward migration.

## Validation evidence

- PASS: live nine-case helper-process inspection/refusal matrix, `-count=5`
- PASS: focused helper/Git inspection and cleanup policy tests, `-count=5`
- PASS: `StartRunRecovery|Zombie` cumulative process smoke, `-count=3`
- PASS: SQLite recovery/expiry/invalidation and v7→v8 migration smoke,
  `-count=5`
- PASS: `go test ./...`; all changed and acceptance-critical packages were
  also exercised uncached by the focused repetitions above
- PASS: focused race run over helper, Git, SQLite, and scenario packages
- PASS: `go vet ./...`
- PASS: `staticcheck ./...`
- PASS: dead-code/call-site guard; removed APIs have zero occurrences and all
  remaining public remediation functions have live non-test callers
- PASS: three review-only mutants were killed: bypassing inspection refusal
  made the OS matrix fail; removing unknown-workspace recovery actions made
  diagnostic completeness fail; removing the cleanup route made the
  production-root reachability test fail. A fourth policy-bypass mutant made
  the direct cleanup test expose successful active-workspace cleanup.

The earlier 100x evidence remains the release-level repetition record; this
remediation cycle intentionally used the requested bounded 3x/5x smoke.

## Mandatory anti-pattern checklist

1. Dead code: **PASS** — all new/remediated public surfaces have production
   callers, and the prior test-only exports were removed.
2. Synthetic-fixture test: **PASS** — the acceptance matrix launches the
   shipped helper against real SQLite, Git worktrees, filesystem state, and
   OS processes.
3. Silent empty return: **PASS** — every inspected refusal is typed and
   evidence-bearing.
4. FR coverage: **PASS** — T019–T023, including live T021/FR-020 and NFR-006,
   are covered cumulatively.
5. Frozen surface: **PASS** — v7 is digest-pinned and unchanged; v8 is the
   appended forward migration.
6. Locked decision: **PASS** — quarantine remains control state; no chmod,
   kill-as-proof, workspace transfer, or destructive evidence cleanup exists.
7. Shared-file ownership: **PASS** — the candidate records the cross-package
   wiring rationale and later lane commits are bookkeeping-only.
8. Production fragility: **PASS** — no panic, unchecked transient retry, or
   mutation-before-binding-validation path was found.
