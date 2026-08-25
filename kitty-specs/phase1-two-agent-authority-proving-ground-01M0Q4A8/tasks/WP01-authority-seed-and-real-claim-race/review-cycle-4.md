---
affected_files: []
cycle_number: 4
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command:
reviewed_at: '2026-08-24T05:02:22Z'
reviewer_agent: unknown
verdict: rejected
wp_id: WP01
---

# WP01 Review Cycle 4 — Rejected

Reviewer: `mini-local` using Reviewer Renata  
Candidate implementation: `8d7b4201d6b5baca72e8f452fe50a082dc685079`  
Coordination base: `94fd5596489998e79b68974a320e6cdd203cf2a5`

## Findings

### High — the committed first-open test does not force the concurrency path it claims to prove

`TestWP01ConcurrentFirstOpenConvergesAcrossProcesses` starts helpers sequentially and has no barrier before `sqlite.Open`; the existing fault barrier is installed and reached only after the helper has already opened the database. Deleting the sole `os.IsExist(err) -> waitForTokenKey(...)` recovery in `loadOrCreateTokenKey` therefore leaves this test green for 10 repetitions. The committed evidence can pass while the exact independent-process first-open race is broken, contrary to NFR-003, C-002, T005, and the prompt's tests-as-scaffold gate.

Paths: `internal/scenario/claim_race_test.go:113`, `internal/scenario/harness.go:29`, `internal/helpercmd/root.go:75`, `internal/controlstore/sqlite/db.go:145`.

Reproduction:

1. In a disposable `git archive 8d7b420` tree, remove the `os.IsExist` recovery at `internal/controlstore/sqlite/db.go:147-149`.
2. Run `go test ./internal/scenario -run '^TestWP01ConcurrentFirstOpenConvergesAcrossProcesses$' -count=10`.
3. It still passes (`13.496s`).

A review-only subprocess probe that blocks 32 independently spawned processes before `Open` passes 10 rounds on the candidate (`0.611s`) and fails the deletion mutant on round 2 with `control.db.token-key: file exists`. Add this deterministic pre-open barrier to the committed evidence and assert every opener succeeds plus the resulting database/key invariants.

### Medium — lease-diagnostic tests do not pin required actor/process attribution

The heartbeat/expiry diagnostic test only checks that `AggregateIDs` is non-empty. Removing both `actor_id` and `process_id` from `leaseCommandAggregates` leaves `TestHeartbeatAndExpiryRequireCurrentGenerationAndToken` green, even though NFR-006 and the command envelope require both identities. A review-only probe that checks exact correlation, operation, actor, process, lease, and generation attribution kills the mutant.

Paths: `internal/controlstore/sqlite/authority_test.go:200`, `internal/controlstore/sqlite/authority.go:364`, `internal/execution/model.go:161`.

Reproduction:

1. In a disposable tree, remove `actor_id` and `process_id` from `leaseCommandAggregates`.
2. Run `go test ./internal/controlstore/sqlite -run '^TestHeartbeatAndExpiryRequireCurrentGenerationAndToken$' -count=1` — it passes.
3. Strengthen assertions to compare every relevant aggregate value for wrong-token heartbeat, stale generation, early expiry, and command-validation refusals.

### Medium — public production-only test scaffold remains dead code

`sqlite.AuthoritySnapshot` and its method have no non-test production caller; all hits are scenario/unit tests. This fails the generated prompt's mandatory dead-code anti-pattern gate. Move the snapshot helper to test-only support or provide a real production consumer justified by the WP contract.

Paths: `internal/controlstore/sqlite/authority.go:275`, `internal/scenario/claim_race_test.go:104`.

Reproduction: `rg -n 'AuthoritySnapshot' --glob '*.go' --glob '!**/*_test.go' internal cmd` finds only the declaration/method.

## Cycle-3 blocker disposition

- Actual `70e6005` migration: **cleared in behavior**. Executed exact old code to create empty and used databases, then opened with exact `8d7b420`. Same-operation replay preserved token/run/lease/generation; the raw legacy bearer secret was absent from DB, WAL, SHM, token-key, and sibling sidecars.
- Heartbeat/expiry command diagnostics: **implementation cleared; committed assertion incomplete** as described above.
- SR-02 post-commit crash/lost response: **cleared**. Ten real-process repetitions exited 86 with empty stdout, replayed the same durable capsule from a replacement process, and retained exactly one operation/run/lease/event. Removing SR-02 is killed by the test.
- Immutable seed identity conflict: **cleared**. Conflicting project/mission is rejected and the guard-deletion mutant is killed.
- Concurrent first-open: **implementation cleared; committed concurrency proof incomplete** as described above.

## Validation

- `go test -count=1 ./internal/ids ./internal/controlstore/sqlite ./internal/execution` — PASS (`sqlite` 4.238s).
- `go vet ./...` — PASS.
- `staticcheck ./...` — PASS.
- `git diff --check 94fd559..8d7b420` — PASS.
- `go test ./internal/scenario -run 'TestWP01(LostResponseReplay|ConcurrentFirstOpenConvergesAcrossProcesses|CrashAtClaimBoundaryLeavesNoAuthority|HelperRejectsUnsafeIDBeforeFaultPath|ContentionCeiling)$' -count=10` — PASS (67.252s).
- `go test ./internal/scenario -run '^TestWP01ClaimRace$' -count=100` — PASS (105.312s).
- `go test -race -count=1 ./...` — PASS (scenario 9.100s; no race report).
- Exact-commit executable legacy migration/replay/secret scan — PASS.
- Review-only complete lease-diagnostic probe, count 10 — PASS.
- Review-only synchronized independent first-open probe, 10 rounds x 32 OS processes — PASS.

Mutation/deletion results:

- Delete SR-02 hook: KILLED (lost-response test times out waiting for the boundary).
- Disable immutable seed conflict: KILLED (expected conflict becomes nil).
- Disable legacy upgrade: KILLED (`no such column: correlation_id`).
- Disable post-migration checkpoint/VACUUM: KILLED (raw legacy token remains in DB).
- Delete first-open EEXIST recovery: **SURVIVED committed test**; killed by synchronized review probe.
- Delete diagnostic actor/process attribution: **SURVIVED committed test**; killed by complete review probe.

## Anti-pattern checklist

1. Dead code: **FAIL** — public `AuthoritySnapshot` is test-only scaffold in production.
2. Synthetic-fixture test: **PASS** — production migration is exercised; exact prior executable behavior was independently verified.
3. Silent empty return: **PASS** — reviewed returns are validation/no-op/control-flow cases, not swallowed failures.
4. FR coverage: **FAIL** — NFR-003/C-002 first-open evidence and NFR-006 identity attribution are not mutation-resistant.
5. Frozen surface: **N/A** — no frozen file is identified for WP01.
6. Locked decision: **PASS** — no reviewed path contradicts an in-scope MUST NOT clause.
7. Shared-file ownership: **PASS** — production changes stay in WP01-owned files.
8. Production fragility: **PASS** — no new bare raise/panic-style transient request path.

## Verdict

**REJECT.** Exact `8d7b420` clears the prior behavioral defects, but two required tests pass for the wrong reason and the mandatory dead-code check still fails. Do not alter production behavior merely to satisfy these findings; strengthen deterministic evidence and remove or legitimately consume the snapshot scaffold.
