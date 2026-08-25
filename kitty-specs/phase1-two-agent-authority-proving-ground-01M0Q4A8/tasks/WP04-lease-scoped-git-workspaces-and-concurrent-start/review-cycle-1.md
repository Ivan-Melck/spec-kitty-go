---
affected_files:
  - internal/controlstore/sqlite/authority.go
  - internal/execution/start.go
  - internal/gitrepo/repository.go
  - internal/helpercmd/start_run.go
  - internal/workspace/service.go
  - internal/scenario/workspace_concurrency_test.go
cycle_number: 1
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./... && go test -race ./... && go vet ./... && staticcheck ./...
reviewed_at: '2026-08-24T13:12:08Z'
reviewer_agent: reviewer-renata
verdict: rejected
wp_id: WP04
---

# WP04 Review Cycle 1

**Reviewer:** reviewer-renata  
**Candidate:** `74137dad59a7abf6b18564417470991dee446a81` (review-lane HEAD `0ad1864` adds coordination-state merge data only)  
**Verdict:** REJECT

## Blocking findings

### 1. Path refusal mutates the primary checkout, and final-component store symlinks escape into Git

`gitrepo.Bind` calls `canonicalManagedRoot` at
`internal/gitrepo/repository.go:52` before checking overlap at lines 56-57.
`canonicalManagedRoot` creates the requested directory. A review-only real-Git
probe passed `<primary>/created-before-refusal` as the managed root. `Bind`
returned the expected `path_unsafe` error, but the directory had already been
created in the primary checkout and its tree digest changed. A refusal has
therefore violated NFR-005 before returning.

`ValidateExternalPath` at lines 82-95 resolves only the parent and then joins
the unexamined basename. A second real-filesystem probe created an external
`control.db` symlink whose target was `<primary>/control.db`.
`ValidateExternalPath` returned `nil`. The live helper subsequently opens that
accepted path as SQLite/tracker storage, so the final symlink target can place
DB/WAL/provider writes inside Git despite T014 and NFR-007.

**Required remediation:** Validate candidate roots without creating them inside
forbidden trees; creation may occur only after containment is proven. Resolve
or safely reject an existing final path component, including symlinks, and
close TOCTOU-practical store-opening paths so the file actually opened is
outside the primary checkout, common directory, and managed workspace root.
Add tests that assert rejected validation changes no primary HEAD/index/tree
bytes and that symlinked database and tracker paths into each forbidden root
are refused before either store opens.

### 2. StartRun idempotency does not cover workspace or ProcessID identity

`StartSaga.Start` claims authority first, then consumes the independently
supplied repository/lane/workspace/branch/base/dependency request at
`internal/execution/start.go:41-58`. The durable start digest in
`internal/controlstore/sqlite/authority.go:429-435` omits ProcessID,
correlation, repository, lane, WorkspaceID, branch, pinned base, and dependency
set. A workspace reservation does not exist before SR-05, so nothing pins those
parts during the earlier crash windows.

Two built-helper reproductions demonstrate the consequences:

1. Crash at SR-04 after the tracker effect, retry the same OperationID with a
   different WorkspaceID and owned branch. The command succeeds and publishes
   the changed workspace as `ready` with `Replayed=true`.
2. Crash at SR-02 after the lease/run commit, retry the same OperationID under
   a different ProcessID. The new process receives the original RunID,
   LeaseID, generation, and opaque fencing token and completes a ready
   workspace. The returned aggregate even reports the changed ProcessID rather
   than the process durably stored on the run.

This violates FR-004, FR-007, the command contract's semantic-idempotency law,
and the immutable Run/Lease/Workspace composition. A different command can be
substituted after a partial effect, and a different process can obtain an
existing process's authority token.

**Required remediation:** Give the complete StartRun command one canonical
durable identity/digest before any effect. It must cover schema/correlation,
ActorID/ProcessID, claim IDs, tracker request, repository/lane/workspace,
branch/base, and canonical dependency set. Identical replay resumes; any
changed field returns an idempotency conflict without disclosing the prior
token or creating another effect. Persist enough immutable command data before
SR-01/SR-02 to enforce this through every crash window. Add OS-process replay
tests changing each identity class after SR-02, SR-04, and before SR-05.

### 3. Invented lane and stale target base become authoritative ready capsules

`validateStartRun` at `internal/execution/start.go:83-103` validates only the
portable syntax of the requested LaneID and object ID. It never checks the
finalized WorkRevision's WorkID-to-Lane mapping. The repository adapter records
`TargetRef`, but neither binding nor workspace preparation proves the requested
`PinnedBase` equals the required current target/accepted lane base.

Black-box helper probes reproduced both failures:

- Work `work-real` is finalized on `lane-real`; starting it with
  `lane-invented` returns `run_ready` and persists/returns `lane-invented`.
- Advance `refs/heads/main`, then start against its previous commit. The stale
  commit is created, verified against itself, and returned as a ready
  `VerifiedBase` instead of a typed `target_changed` result.

This imports caller strings as lane/base authority and defeats FR-005, FR-009,
the lane/workspace separation, and T016/T017's exact-base and movement laws.

**Required remediation:** Compose the command with authoritative finalized
graph/lane and repository/target facts. Reject a LaneID that is not the work's
immutable lane. Resolve and record the expected base from the appropriate
authoritative target/accepted lineage, re-probe the target before publication,
and return typed observed/expected SHAs when it moved. Add live-helper tests for
invented/cross-work lanes, arbitrary commits, stale target movement, and target
movement between reservation and ready publication.

### 4. SR-09 replay skips the advertised Git re-probe and returns false readiness

`EnsureReady` loops only while `!record.Ready()` at
`internal/workspace/service.go:70`. A durably ready row therefore bypasses the
`StageReady` probe at lines 151-159; that branch is unreachable for the valid
record it is meant to verify.

A real-helper crash probe stopped at SR-09 after the ready commit and before
the response. Before replay, the worktree received a new commit. Retrying the
same OperationID returned `run_ready` from the cached row without executing a
Git probe. The capsule's `VerifiedBase` differed from actual workspace HEAD.
This is precisely the lost-response/stale-fact boundary where the plan says a
cached stage is insufficient.

**Required remediation:** On every replay of a ready allocation, probe Git
registration, canonical path, owned branch, HEAD/base, Git directory, and index
identity before returning the recorded success. Return the contract's typed
movement/not-ready diagnostic when facts differ. Add an OS-process SR-09 test
that moves/removes/re-registers the worktree between lost response and replay;
use a mutant that removes the ready re-probe to prove the assertion is live.

### 5. The live `start-run` helper cannot carry the required command envelope or guarantee typed failures

`internal/helpercmd/start_run.go:25-70` defines no `--schema-version`, separate
`--correlation`, or injected `--at` flag. It hardcodes schema version 1 and uses
`time.Now()` plus OperationID as correlation at lines 70 and 98-101. Thus the
live adapter cannot accept the command envelope required by
`contracts/commands-and-results.md` and cannot deterministically replay the
caller timestamp.

The helper also passes raw SQLite, fake-store, and Git/edit errors to the
generic encoder at lines 81-88 and 112-117. That encoder emits only `message`
for non-diagnostic errors. A live invocation with the required
`--schema-version 1` flag exits 2, writes zero stdout bytes, and prints only
flag usage to stderr. It does not emit schema/correlation/operation IDs,
aggregates, observed/expected state, retry safety, or recovery actions.

Committed independent-process crash coverage exists only for SR-06 in
`internal/scenario/workspace_concurrency_test.go:76-109`. Review-only probes
confirmed identical replay currently succeeds at SR-05, SR-07, SR-08, and
SR-09, but those named contract boundaries have no committed OS-process
evidence or adversarial movement assertions; the SR-09 false-ready defect above
survives.

**Required remediation:** Make `start-run` a transport adapter for a complete
typed command: require/validate schema version, correlation ID, ActorID,
ProcessID, and injected RFC3339Nano timestamp, and propagate them without
inventing values. Translate every refusal, including parse/open/Git/edit
failures, to the complete stable diagnostic envelope. Commit the SR-05 through
SR-09 independent-process crash/replay matrix with boundary-specific durable
state, idempotency, movement, and primary-isolation assertions.

## Evidence that passed

- Git production calls use argv with explicit `git -C <canonical path>`; no
  ambient `Getwd`/`Chdir`, shell command string, `sh -c`, or `bash -c` was
  found.
- The true two-helper concurrency scenario uses SR-00/WORK/EDIT barriers,
  proves overlapping active intervals, distinct runs/tokens/workspaces/
  branches/Git dirs/indexes/commits, and byte-identical primary HEAD/index/tree.
- Dependency candidates are sorted deterministically. Conflicting later
  dependencies leave the owned branch at pinned base, while identical replay
  produces the same deterministic merge head.
- Reservation precedes worktree creation; a merely existing unregistered
  directory does not advance readiness. Public stage mutations revalidate the
  live lease token in the SQLite transaction, and the v7 triggers prevent
  updates to repository and workspace identity plus non-monotonic stage jumps.
- Shipped v6 is byte-identical to candidate `c2468c8`; independent declaration
  hashes both equal
  `3c001a74872315dc3e6db8c9386de23f54087e45c48e0f544139f175b261718c`.
  V7 is forward-only/additive. Populated graph, tracker, work, run/lease, and
  prior terminal guards survive the cumulative upgrade tests.
- Identical review-only OS-process recovery at SR-05, SR-07, SR-08, and SR-09
  completed successfully before adversarial state changes were injected.

## Contract round-trip

- `contracts/commands-and-results.md`: **FAIL** — complete StartRun identity is
  neither accepted by the helper nor digest-pinned; changed replay can disclose
  the prior token, and raw helper failures omit the typed result shape.
- `contracts/fault-boundaries.md`: **FAIL** — SR-09 trusts cached ready state
  over Git fact, and the committed SR-05..09 OS-process matrix is incomplete.
- `contracts/provider-contract.md`: **PASS** — the fake provider remains
  informational and separately durable; the blocker is the orchestration
  command's failure to bind provider/workspace identity atomically.

## Anti-pattern checklist

1. **Dead code: PASS.** New workspace/repository/start surfaces have live
   production callers through the helper vertical path.
2. **Synthetic-fixture test: PASS.** The crown-jewel scenario uses real helper
   processes, SQLite, tracker storage, Git repository, and worktrees, although
   its coverage is incomplete as described above.
3. **Silent empty return: PASS.** No Go equivalent of a silent empty catch was
   found; incomplete raw helper error encoding is finding 5.
4. **FR coverage: FAIL.** FR-004/005/007/009/018/019 and NFR-004/005/006/007
   have reproduced violations or missing boundary proof.
5. **Frozen surface: PASS.** Shipped v6 is unchanged and digest-pinned; new DDL
   is forward-only v7.
6. **Locked decision: FAIL.** Mutating validation, caller-invented lane/base,
   cached readiness, and partial command identity contradict explicit MUST
   laws in spec, plan, and contracts.
7. **Shared-file ownership: PASS.** The commit records the approved ownership
   exceptions for IDs, schema version assertions, and root dispatch; shared
   changes are limited to the WP04 vertical path.
8. **Production fragility: N/A.** Go has no new bare `raise`; raw failure
   translation is handled as a diagnostic-contract blocker instead.

## Verification

Green candidate-suite evidence:

```text
go test ./internal/gitrepo ./internal/workspace ./internal/execution ./internal/controlstore/sqlite ./internal/scenario
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
go test ./internal/scenario -run '^TestWorkspaceConcurrency$' -count=100 -timeout=5m
go test -race ./internal/scenario -run '^TestWorkspaceConcurrency$' -count=10 -timeout=3m
go test ./internal/gitrepo ./internal/workspace ./internal/controlstore/sqlite -run '<WP04 focused set>' -count=50
git diff --check 74137dad^..74137dad
```

The 100-run OS-process concurrency proof completed in 126.766 seconds. All
review-only failing probes were removed after capture; the lane contains no
reviewer production changes. Manual fault injection was more useful than a
syntactic mutation tool here: it exposed the surviving cached-ready path and
changed-command replay paths that the committed suite does not constrain.

WP05 and WP06 depend on WP04 and should rebase only after these authority,
path, and recovery blockers are remediated.
