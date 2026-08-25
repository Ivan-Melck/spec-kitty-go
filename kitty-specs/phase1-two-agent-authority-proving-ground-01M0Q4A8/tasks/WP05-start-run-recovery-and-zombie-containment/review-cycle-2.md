---
affected_files: []
cycle_number: 2
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command:
reviewed_at: '2026-08-24T16:32:21Z'
reviewer_agent: unknown
verdict: rejected
wp_id: WP05
---

# WP05 Review Cycle 1 — Changes Requested

Candidate: `443fa0e0ede98a9e0db7a4265a169f9c0abdb401`

## Blocking finding

### 1. T021 / FR-020 is test-only rather than a live production capability

The new safe Git inspection and cleanup implementation is not reachable from
any production command or service path. Exact Go call-site searches show:

- `(*gitrepo.Repository).Cleanup` is called only from `*_test.go` files.
- `(*gitrepo.Repository).Inspect` is called only by that otherwise-unreachable
  `Cleanup` method and by tests.
- `(*workspace.Service).Quarantine` has no caller at all.
- `workspace.IsQuarantined` and `(*sqlite.Store).QuarantineIntentCount` are
  test-only exported seams.
- The shipped helper command named `inspect` still calls only
  `AuthoritySnapshot`; it never loads a durable workspace or invokes the new
  Git/filesystem inspection and cleanup-refusal path.

This fails the review prompt's mandatory dead-code anti-pattern check and does
not provide a live T021/FR-020 surface. It also leaves the new cleanup refusals
as bare `workspace.Error` values rather than command results carrying the
NFR-006 envelope (schema/correlation/OperationID, relevant aggregate IDs,
observed and expected facts, retry safety, and recovery actions).

Required remediation:

1. Wire inspection and cleanup eligibility/refusal through a real production
   command or application-service path that loads the immutable repository and
   workspace binding and invokes the Git adapter without mutation during
   inspection.
2. Translate every refusal at that command boundary into the complete typed
   diagnostic envelope required by `contracts/commands-and-results.md` and
   NFR-006.
3. Add black-box helper/service tests proving the live path reports
   registration, path, branch, head, and dirty discrepancies; refuses unknown,
   dirty, active, and quarantined worktrees; and preserves exact commits.
4. Remove or internalize exported test-only APIs that are not part of the live
   design, or give each a genuine production caller. Do not retain future
   scaffolding solely for tests.

WP08 depends on WP05 and must rebase after the corrected WP05 candidate is
available.

## Acceptance and adversarial evidence

The core recovery work is otherwise strong: recovery uses the durable
OperationID envelope; repository/workspace bindings are immutable; expiry uses
generation/token fencing; deadline expiry and revision invalidation share the
same quarantine transition; SR-00 through SR-09 are exercised by helper
process crashes; dependency conflict/partial preparation never becomes ready;
and the SIGSTOP/SIGCONT zombie commits in quarantined W1 while W2 and the
primary checkout remain unchanged. The v7 migration text is digest-frozen and
v8 is a forward migration. A quarantine-predicate mutant was killed by both
the SQLite expiry test and the real zombie/invalidation scenarios.

Validation run:

- PASS: `go test ./internal/execution ./internal/workspace ./internal/gitrepo ./internal/controlstore/sqlite`
- PASS: `go test ./...`
- PASS: `go test -race ./...`
- PASS: `go vet ./...`
- PASS: `staticcheck ./...`
- PASS: focused adversarial scenario bundle, `-count=10`
- PASS: isolated SQLite busy-timeout test, `-count=10`
- INCONCLUSIVE: `go test ./internal/scenario -run 'StartRunRecovery|Zombie' -count=100` reached Go's 10-minute package timeout while run concurrently with other stress checks. Focused 10x repetitions passed; this timeout is not the rejection reason.
- NON-BLOCKING STRESS NOTE: a 25x SQLite package loop run concurrently with the
  100x process suite tripped the legacy busy-wait test's 3.5-second lower bound
  at 3.1–3.4 seconds. The same test passed 10/10 in isolation.

## Mandatory anti-pattern checklist

1. Dead code: **FAIL** — blocker described above.
2. Synthetic-fixture test: **PASS** — recovery/zombie tests execute the real
   helper, SQLite, filesystem, and Git paths.
3. Silent empty return: **PASS** — no unexplained silent failure return was
   found in the candidate production paths.
4. FR coverage: **FAIL** — T021/FR-020 has adapter-level assertions but no live
   production command-path assertion; NFR-006 is therefore also incomplete at
   this boundary.
5. Frozen surface: **PASS** — v7 remains unchanged and is pinned by digest; v8
   is appended as a forward migration.
6. Locked decision: **PASS** — no chmod/kill/filesystem-fencing claim, workspace
   transfer, or destructive quarantined cleanup was introduced.
7. Shared-file ownership: **PASS** — the candidate commit records its cumulative
   wiring rationale across the authority/workgraph/workspace seams.
8. Production fragility: **PASS** — no new panic/unchecked transient-race raise
   was found.
