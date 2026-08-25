---
affected_files:
  - internal/execution/start.go
  - internal/workspace/service.go
  - internal/controlstore/sqlite/migrations.go
cycle_number: 3
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./... -count=1 && go test -race ./... -count=1 && go vet ./... && staticcheck ./...
reviewed_at: '2026-08-24T13:56:36Z'
reviewer_agent: reviewer-renata
verdict: rejected
wp_id: WP04
---

# WP04 Review Cycle 3

**Reviewer:** reviewer-renata  
**Candidate:** `864b4f947e5e05b20673e131241efef39f983033` (review-lane HEAD `a867db726dad7f537e21983f4a4b7535be5f70e0` adds workflow merge/status commits)  
**Verdict:** REJECT

## Blocking findings

### 1. Target movement in the final probe-to-CAS gap still publishes a stale ready workspace

`internal/workspace/service.go:139-154` reads `TargetHead`, probes workspace
facts, and only then calls the independent SQLite `AdvanceWorkspace` CAS that
publishes `StageReady`. No target observation is carried into or revalidated
after that CAS. The two systems therefore have an exposed TOCTOU window.

A review-only deterministic adapter returned the pinned target from the final
`TargetHead` call, moved the live target during the immediately following
workspace probe, and left all worktree facts otherwise valid. `EnsureReady`
committed and returned `StageReady` while the live target was
`9999999999999999999999999999999999999999` and the pinned base remained
`0123456789abcdef0123456789abcdef01234567`. This violates T016/T017 and the
definition of done: the returned capsule was not based on the live target at
publication.

The new initial preflight and `StageOwnershipVerified` target read correctly
reject ordinary stale bases and movement at earlier stages. They do not close
movement between the last read and the ready commit, which was an explicit
cycle-1 remediation requirement.

**Required remediation:** Make target authentication part of an honest final
publication protocol rather than an unguarded read followed by a separate DB
write. Persist the observed target and re-probe at the publication boundary;
if the target differs, do not expose usable readiness and return typed
`target_changed` observed/expected SHAs. Commit a faulting-adapter test that
moves the target specifically after the final target read and before the ready
CAS, and a mutant that removes the final target guard.

### 2. Cross-field OperationID reuse can bypass the durable idempotency-conflict seam

`StartSaga.Start` computes the complete digest but calls
`workspaces.Validate` at `internal/execution/start.go:55-57` before
`authority.Start` consults the already-recorded operation digest at lines
59-62. Consequently, changed fields that fail workspace preflight return their
field-specific validation error instead of the mandatory idempotency conflict
for a reused OperationID.

Two real-helper reproductions crashed after SR-04, then retried the same
OperationID with one changed command field:

- changed LaneID returned `invalid_command: lane is not the finalized lane`;
- changed pinned base returned `target_changed: pinned base is not the
  repository target head`.

Both should have returned `idempotency_conflict`, as the command contract says
that any semantically different reuse of an OperationID is a conflict. The
committed changed-workspace/branch test passes only because those values remain
syntactically and semantically preflight-valid long enough to reach the digest
lookup. The all-fields digest unit test proves hashing, not precedence at the
durable command seam.

**Required remediation:** Add a read-only existing-operation identity check
before state-dependent workspace preflight. A new invalid command must still
produce no operation/run/lease/tracker/workspace mutation; an existing exact
command may continue the saga; any existing changed command must return
`idempotency_conflict` before lane, repository, target, or workspace state is
consulted. Commit real-helper replay tests for each cross-field class,
including changed lane, base, repository, work/revision, tracker identity, and
dependencies after SR-02/SR-04.

### 3. V7 direct-SQL guards allow WorkspaceID rewrite and quarantine revival

The `immutable_workspace_binding_update` trigger in
`internal/controlstore/sqlite/migrations.go` omits `workspace_id`. A
review-only populated-store probe successfully executed:

```sql
UPDATE workspaces
SET workspace_id='workspace-rewritten'
WHERE workspace_id='workspace-review';
```

The same schema also accepts `quarantined -> ready` with the quarantine reason
and timestamp erased. A second direct update successfully revived the record.
This contradicts the data model's permanent Workspace identity and one-way
`ready -> quarantined` lifecycle. It also makes the new SR-09 quarantine
evidence mutable at the authoritative SQLite layer.

The existing committed mutation test checks repository facts, LeaseID, and a
non-monotonic stage jump, but does not kill either mutation above.

**Required remediation:** Seal WorkspaceID as immutable and enforce monotonic
status/quarantine transitions in forward-only v7 DDL. A quarantined workspace
must never become ready again and its reason/time must be immutable once set.
Add committed direct-SQL mutation tests that fail if WorkspaceID can change or
quarantine can be cleared/revived.

## Cycle-1 remediation that passed

- Rejected managed-root paths no longer create directories in the primary
  checkout. Final and ancestor symlinks for managed roots and database/tracker
  paths are resolved and rejected before a store opens; committed real-Git
  tests preserve primary HEAD/index/tree/file-set bytes.
- The complete start command digest covers schema/correlation, claim identity,
  ActorID/ProcessID/timestamp/TTL, tracker identity/revision/assignee, and all
  repository/lane/workspace/branch/base/dependency fields. Dependency ordering
  is canonical. Changed ProcessID after SR-02 and changed workspace/branch
  after SR-04 no longer reveal the prior token.
- Finalized WorkRevision membership authenticates WorkID-to-Lane and repository
  project identity. Ordinary invented lanes and stale bases fail before
  authority or workspace publication.
- StageReady now re-probes registration, clean state, branch, HEAD, GitDir, and
  index and quarantines moved, removed, and re-registered worktrees. The
  independent-process SR05-SR09 crash/replay matrix is committed and green.
- The live helper now requires schema version, correlation ID, ActorID,
  ProcessID, and injected RFC3339 timestamp, propagates them into StartRun, and
  emits typed diagnostic envelopes for exercised parse/open/Git failures.
- Real two-process concurrency still proves overlapping edit intervals,
  distinct runs/tokens/workspaces/branches/Git dirs/indexes/commits, and
  byte-identical primary checkout state.

## Migration and scope evidence

- `git diff c2468c8..864b4f9 -- internal/controlstore/sqlite/migrations.go`
  shows the shipped v6 prefix unchanged and only additive v7 objects.
- The frozen-v6 digest, populated real-v6-to-v7 preservation, and workspace
  trigger tests passed ten consecutive runs.
- The root helper dispatch is unchanged by candidate `864b4f9`; StartRun
  remains reachable through the existing production command path.
- No ambient CWD discovery, `Chdir`, shell command string, or primary-checkout
  mutation was found in the changed Git path.

## Contract round-trip

- `contracts/commands-and-results.md`: **FAIL** — full digest coverage exists,
  but changed lane/base OperationID reuse returns preflight errors instead of
  the required idempotency conflict.
- `contracts/fault-boundaries.md`: **FAIL** — the committed SR05-SR09 matrix is
  now present and green, but target movement in the last probe/publication
  window can still produce false readiness.
- `contracts/provider-contract.md`: **PASS** — tracker data remains separately
  durable and does not become workspace or execution authority.

## Verification

Green candidate-suite evidence:

```text
go test ./internal/gitrepo ./internal/workspace ./internal/execution ./internal/helpercmd ./internal/scenario -count=1
go test ./internal/controlstore/sqlite -run '<frozen-v6|populated-v6-to-v7|workspace-guards>' -count=10
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
staticcheck ./...
go test ./internal/scenario -run '^TestWorkspaceConcurrency$' -count=20
git diff --check 74137dad..864b4f9
```

Review-only failing probes (removed after capture):

```text
go test ./internal/workspace -run TestReviewTargetMovementBetweenFinalProbeAndReadyCAS -count=1
  FAIL: published ready after target moved in probe/CAS gap

go test ./internal/scenario -run 'TestReviewChanged(Lane|Base)Replay' -count=1
  FAIL: observed invalid_command and target_changed, wanted idempotency_conflict

go test ./internal/controlstore/sqlite -run TestReviewWorkspaceIdentityAndQuarantineCannotBeRewrittenDirectly -count=1
  FAIL: identity_rewritten=true quarantine_revived=true
```

WP05 and WP06 depend on WP04 and should rebase only after these publication,
idempotency, and durable-guard blockers are remediated.
