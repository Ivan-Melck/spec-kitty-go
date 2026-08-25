---
affected_files:
  - internal/execution/start.go
  - internal/helpercmd/start_run.go
  - internal/workspace/service.go
cycle_number: 5
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./... -count=1 && go test -race ./... -count=1 && go vet ./... && staticcheck ./...
reviewed_at: '2026-08-24T14:26:35Z'
reviewer_agent: reviewer-renata
verdict: rejected
wp_id: WP04
---

# WP04 Review Cycle 5

**Reviewer:** reviewer-renata  
**Candidate:** `3b33a3c31e4728959d7a5f77d39839fe7fb13b1f` (review-lane HEAD `c43efa30333d22d42647528150126958ab4edc13` adds workflow merge/status commits)  
**Verdict:** REJECT

## Blocking finding

### Exact replay can switch to a different physical Git repository under the durable RepositoryID

The remediation correctly checks the complete StartRun digest before semantic
workspace preflight. For an existing exact operation, however,
`internal/execution/start.go:57-66` deliberately skips `workspaces.Validate`.
The helper also removed its unconditional `BindRepository` call at
`internal/helpercmd/start_run.go:111-116`.

`EnsureReady` now calls only `validateIdentity` at
`internal/workspace/service.go:58`. That function loads the durable repository
row by RepositoryID but never compares it with `s.git.Binding()`. The full
`Validate` method performs that comparison/binding for a new operation, but it
is not reached by an exact replay. The physical primary path and Git common
directory are transport/binding facts, not fields in the StartRun digest.

A review-only real-helper reproduction proved the bypass:

1. Start `operation-binding` against repository A and crash after SR-04.
   Repository A's canonical binding is durable; no workspace exists yet.
2. Clone A to repository B so both have the same base commit.
3. Replay the exact same OperationID, command envelope, RepositoryID,
   workspace root, branch, and base, changing only the helper's physical
   `--repository` path from A to B.
4. The helper returned `OK`, `run_ready`, and the prior authority capsule.
   The capsule's workspace path remained under A's durable managed root, while
   its GitDir was under repository B's `.git/worktrees/workspace-binding`.

Thus a semantically exact operation resumes against a Git common directory
that contradicts SQLite's immutable repository binding. The new ref lock is
also acquired in B rather than in the bound repository. This defeats T014's
canonical repository identity, T015's immutable repository/workspace
composition, and T016's requirement that replay verify each external effect.

**Required remediation:** After the durable digest check identifies an exact
operation, compare the live adapter's complete canonical binding against the
stored repository row before tracker replay, reservation, Git creation, lock
acquisition, or token/capsule return. Preserve the current precedence law:
changed command fields must still return `idempotency_conflict` before binding
or workspace semantics, while a new invalid command must still publish zero
authority/workspace effects. Add a committed real-helper SR-02/SR-04 replay
test that changes only the physical repository/common directory under the same
RepositoryID and requires `repository_binding_conflict`, no capsule/token, no
branch/worktree in the second repository, and no change to the original
primary checkout.

## Cycle-3 remediation that passed

- The final target read, workspace probe, ready CAS, and post-CAS target read
  now execute under one repository-scoped OS flock. The deterministic
  publication-window mutant is killed: target movement does not yield
  `StageReady`.
- Cooperating `UpdateTarget` waits for publication authority; the lock is
  released on callback failure/crash unwind. SR05-SR09 recovery and exact
  replay complete without deadlock.
- SR09 target drift returns typed `target_changed` and durably quarantines the
  workspace rather than returning a cached capsule.
- Existing-operation digest lookup precedes finalized lane/base/repository-ID/
  work/revision/tracker/dependency semantic preflight. Real-helper mutations
  after SR-02/SR-04 all return `idempotency_conflict` without a capsule/token.
- V7 now rejects direct WorkspaceID rewrite, terminal status revival, and
  quarantine reason/timestamp mutation. The mutation-guard test passed 50
  consecutive runs.

## Cumulative regression evidence that passed

- Managed-root and store final/ancestor symlink attacks are rejected before
  mutation. Rejected paths preserve primary HEAD/index/tree/file-set bytes.
- StageReady re-probes registration, cleanliness, branch, HEAD, GitDir, and
  index and quarantines moved, removed, and re-registered worktrees.
- The helper requires and emits schema version, correlation ID, ActorID,
  ProcessID, and injected timestamp and returns complete typed diagnostics.
- The committed two-process test retains overlapping edit intervals, distinct
  runs/tokens/workspaces/branches/Git dirs/indexes/commits, and byte-identical
  primary checkout facts.
- Shipped v6 remains frozen. V7 is additive from the v6 prefix, and populated
  v6 tracker/work-graph evidence survives upgrade.
- Git remains argv-only with explicit paths; no ambient CWD or shell command
  string was introduced. Root helper dispatch is unchanged.

## Contract round-trip

- `contracts/commands-and-results.md`: **FAIL** — digest conflict precedence is
  fixed, but exact replay can compose the recorded RepositoryID with a
  different physical repository and return a usable capsule.
- `contracts/fault-boundaries.md`: **PASS** — the SR05-SR09 matrix, target
  publication lock, crash unwind, and target-drift quarantine are exercised.
- `contracts/provider-contract.md`: **PASS** — tracker state remains separately
  durable and does not become repository/workspace authority.

## Anti-pattern checklist

1. **Dead code: PASS for the blocking path.** `LookupStartOperation`,
   `WithTargetAuthority`, and binding access all have production callers.
2. **Synthetic-fixture test: PASS.** The decisive replay and concurrency tests
   invoke the real helper, SQLite, tracker, Git, and worktree path.
3. **Silent empty return: N/A.** No Go equivalent was introduced.
4. **FR coverage: FAIL.** T014/T015/T016 and FR-004/005/018 remain violated by
   the reproduced repository-binding swap.
5. **Frozen surface: PASS.** The v6 migration body remains unchanged; WP04 DDL
   stays in v7.
6. **Locked decision: FAIL.** Canonical repository identity is an explicit
   MUST-level design law and exact replay currently bypasses it.
7. **Shared-file ownership: PASS.** Shared execution/schema edits stay within
   the approved WP04 vertical path.
8. **Production fragility: N/A.** No bare Go panic/raise path was added.

## Verification

Green candidate evidence:

```text
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
staticcheck ./...
go test ./internal/scenario -run '^TestWorkspaceConcurrency$' -count=20
go test ./internal/workspace -run '^TestTargetMovementAfterFinalReadCannotPublishReady$' -count=100
go test ./internal/gitrepo -run '^TestCooperatingTargetUpdateCannotCrossPublicationAuthorityLock$' -count=50
go test ./internal/controlstore/sqlite -run '^TestWorkspaceReservationBindingAndRepositoryFactsAreImmutable$' -count=50
go test ./internal/scenario -run '<digest-conflict|SR05-SR09|target-drift>' -count=2
go test ./internal/controlstore/sqlite -run '<frozen-v6|populated-v6-to-v7>' -count=10
go test ./internal/gitrepo ./internal/helpercmd -run '<path-symlink|typed-envelope>' -count=50
git diff --check 864b4f9..3b33a3c
```

Review-only failing probe, removed after capture:

```text
go test ./internal/scenario -run TestReviewExactReplayCannotSwitchPhysicalRepositoryBinding -count=1 -v
  FAIL: exact replay crossed immutable physical repository binding;
        original=<repo-A>, replay=<repo-B>, capsule_git_dir=<repo-B>/.git/worktrees/workspace-binding
```

WP05 and WP06 depend on WP04 and should rebase only after this repository
authority blocker is closed.
