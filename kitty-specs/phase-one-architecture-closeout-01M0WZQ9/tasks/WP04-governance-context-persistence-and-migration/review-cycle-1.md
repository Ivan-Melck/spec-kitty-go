---
affected_files: []
cycle_number: 1
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reproduction_command:
reviewed_at: '2026-08-26T00:47:01Z'
reviewer_agent: user
wp_id: WP04
---

# WP04 Review Feedback — Cycle 1

## Blocking issue 1: the run-owned governance binding is mutable after insert

`internal/controlstore/sqlite/migrations.go:162-179` adds the nullable `runs.governance_context_id` foreign key and an insert-time non-null trigger, but it adds no `BEFORE UPDATE OF governance_context_id ON runs` guard. Consequently, direct adapter SQL can:

- change a newly admitted run from context A to context B; or
- change a migrated historical run from `NULL` to a current context, fabricating historical provenance.

Both operations violate FR-010, FR-011, NFR-007, the governance-context contract (immutable run ownership and honest `unknown_pre_provenance`), and the WP definition of done. The existing immutability test at `internal/controlstore/sqlite/governance_context_test.go:126-135` checks context and entry rows only, so it does not detect this path.

Remediation: extend forward migration v12 with a run-binding immutability constraint that permits lifecycle status changes but rejects every change to `runs.governance_context_id`, including `NULL -> value`, `value -> NULL`, and `value A -> value B`. Add acceptance assertions for all three mutations and verify that `GovernanceForRun`/`GovernanceForLease` still report the original verified context or `unknown_pre_provenance` after each rejected attempt. Do not backfill historical rows.

## Blocking issue 2: both new public inspection methods are test-only dead code

`internal/controlstore/sqlite/governance_context.go:32-45` exports `GovernanceForRun` and `GovernanceForLease`, but repository-wide call-site search finds only definitions and `_test.go` callers. This fails the review capsule's mandatory dead-code check, which requires every new public function to have a live production caller.

Remediation: connect lease/run governance inspection to the intended production capability boundary and exercise the real consumer path, or reduce the surface so no exported test-only API remains while preserving the required lease-to-run inspection behavior. Tests must invoke that production path rather than only the concrete SQLite methods.

## Coordination and downstream impact

WP04 commit `3c0c1c3` also makes fixture-only compatibility edits in `internal/controlstore/sqlite/authority_test.go`, whose lane owner is WP02. Those lines only supply the newly required governance context to existing WP02 tests; no WP02 product behavior is attributed to WP04. Preserve that coordination when rebasing.

WP05 and WP06 depend on WP04. They must rebase after the corrected WP04 product commit is accepted.

## Revalidation

Run:

```bash
go test ./internal/controlstore/sqlite -run 'Governance|Migration' -count=10
go test -race -count=1 ./internal/controlstore/sqlite
go test -count=1 ./internal/governancecontext ./internal/execution ./internal/controlstore/sqlite
go vet ./internal/governancecontext ./internal/execution ./internal/controlstore/sqlite
staticcheck ./internal/governancecontext ./internal/execution ./internal/controlstore/sqlite
```

Do not run the monolithic scenario campaign; that remains WP05 scope.
