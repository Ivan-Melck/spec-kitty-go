---
affected_files: []
cycle_number: 7
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command:
reviewed_at: '2026-08-24T14:33:56Z'
reviewer_agent: unknown
verdict: rejected
wp_id: WP04
---

---
affected_files:
  - internal/execution/start.go
  - internal/workspace/service.go
  - internal/gitrepo/workspace.go
  - internal/helpercmd/start_run.go
cycle_number: 6
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./internal/scenario -run '^TestReviewWP04ExactReplayCannotSwitchPhysicalRepository$' -count=3
reviewed_at: '2026-08-24T14:32:36Z'
reviewer_agent: mini-local
verdict: rejected
wp_id: WP04
---

# WP04 Review Cycle 6 — Rejected

Reviewer: `mini-local` using Reviewer Renata  
Candidate: `3b33a3c31e4728959d7a5f77d39839fe7fb13b1f`

This report completes runtime review invocation `139710f74e8a474489eedc06c901d721` after the preceding reviewer was interrupted. The prior uncommitted cycle-5 report identified the same suspected seam; this takeover independently reconstructed and reproduced it three consecutive times against an archive of the exact candidate.

## Verdict

**REJECT.** Cycle-3 target-publication locking, digest-conflict precedence, and direct-SQL workspace seals remain green. One authority blocker remains: an exact StartRun replay can replace the physical Git repository behind an already-durable RepositoryID, create and publish the workspace through clone B, and return the prior run authority while SQLite continues to identify repository A as canonical.

## Blocking finding

### High — Exact replay bypasses immutable physical repository binding

`StartSaga.Start` identifies an exact existing operation at `internal/execution/start.go:54-65` and intentionally skips `WorkspacePreparer.Validate`. That preserves digest-conflict precedence, but it also skips the only production path that compares `s.git.Binding()` with the requested/durable repository and invokes `BindRepository` (`internal/workspace/service.go:218-236`).

The subsequent `EnsureReady` call at `internal/workspace/service.go:57-68` runs only `validateIdentity`. That helper loads the durable repository by RepositoryID and checks project/work/lane membership, but never compares the live adapter's `CommonDir`, `PrimaryPath`, `ManagedRoot`, target ref, or primary snapshot with the durable `RepositoryBinding`. `gitrepo.Repository.Create` and `Probe` then use the replay adapter's `r.binding.CommonDir` at `internal/gitrepo/workspace.go:89-158`, so successful Git evidence can come from a different clone.

The exact-candidate production-helper reproduction performed:

1. Start `operation-binding` through repository A and crash at SR-04, after authority/tracker durability but before workspace reservation.
2. Clone A to physical repository B at the same base commit.
3. Replay the exact same schema, correlation, OperationID, ActorID/ProcessID, RepositoryID, graph/lane/workspace/branch/base/tracker command, database, tracker store, and managed workspace root; only helper `--repository` points to B.
4. Observe `OK`, `run_ready`, and the replayed authority capsule.

All three runs produced the same split. Representative evidence:

```text
durable repository common_dir = .../001/repository/.git          # A
durable repository managed_root = .../001/workspaces             # A
workspace canonical_path = .../001/workspaces/workspace-binding  # A root
capsule/workspace git_dir = .../002/clone-b/.git/worktrees/workspace-binding
workspace stage/status = ready/ready
```

The target-ref publication lock is consequently acquired in B, not the durable repository A. This violates T014 canonical repository identity, T015 immutable repository/workspace composition, and T016 replay verification. A stable RepositoryID cannot safely compose authority facts from one durable binding with Git facts from another physical repository.

## Required remediation

After the durable digest lookup establishes an exact replay—but before tracker replay, workspace reservation, Git creation/probe, target-lock acquisition, or capsule/token return—compare the live adapter's complete canonical `RepositoryBinding` with the stored binding for the RepositoryID. Refuse any mismatch with a complete `repository_binding_conflict` diagnostic and no capsule/token.

Preserve the fixed precedence law:

- a changed command under an existing OperationID returns `idempotency_conflict` before repository/workspace semantics;
- a new invalid command publishes no authority or workspace effects;
- an exact command still must authenticate its live physical repository against durable binding.

Add a committed real-helper SR-02/SR-04 matrix changing only the physical primary/common directory under the same RepositoryID. Assert no branch/worktree or ready workspace is created through B, no target lock in B authorizes publication, and repository A plus its primary checkout remain unchanged. Include a deletion mutant that removes the live-vs-durable binding comparison.

## Cleared cumulative blockers

- Final target read, workspace probe, ready CAS, and post-CAS read remain serialized under repository-scoped target authority.
- Target movement in the publication window and SR-09 target drift cannot return a usable ready capsule.
- Existing-operation digest lookup still precedes changed lane/base/repository-ID/work/revision/tracker/dependency semantics.
- V7 still rejects WorkspaceID rewrite, terminal status revival, and quarantine-evidence mutation.
- SR05-SR09 crash replay, workspace drift quarantine, managed-path refusal, typed helper diagnostics, and dependency preparation remain green in the bounded regression set.

## Validation

- Exact real-helper A→B replay probe, `-count=3`: **FAIL** all three times with `run_ready` and clone-B GitDir, reproducing the blocker.
- `go test ./internal/workspace ./internal/gitrepo ./internal/execution ./internal/controlstore/sqlite -count=1`: PASS.
- Focused SR05-SR09, changed-command digest, and target/workspace-drift scenarios, `-count=3`: PASS (65.381s).
- `go test -race ./internal/workspace ./internal/gitrepo -count=1`: PASS; no race report.
- `go vet ./internal/workspace ./internal/gitrepo ./internal/execution ./internal/controlstore/sqlite`: PASS.
- `git diff --check 864b4f947e5e05b20673e131241efef39f983033..3b33a3c31e4728959d7a5f77d39839fe7fb13b1f`: PASS.

## Contract and anti-pattern checks

- `contracts/commands-and-results.md`: **FAIL** — exact replay can return authority composed with a different physical repository.
- `contracts/fault-boundaries.md`: **PASS** for the bounded SR05-SR09/target regression set.
- Dead code: **PASS** — the affected validation, workspace, Git, and helper paths are production-reachable.
- Synthetic-fixture test: **PASS** — the decisive probe uses the real helper, OS process, SQLite, fake tracker store, two real Git repositories, and a real worktree.
- Silent empty return: **PASS**.
- FR coverage: **FAIL** — FR-004/005/018 and T014/T015/T016 remain violated.
- Frozen surface: **PASS** — no new migration concern is implicated.
- Locked decision: **FAIL** — canonical immutable repository binding is a MUST-level mission law.
- Shared-file ownership: **PASS** for the reviewed vertical path.
- Production fragility: **FAIL** — an apparently exact recovery can publish authority against the wrong Git repository without diagnostic refusal.

WP05 and WP06 depend on WP04 and should rebase only after this binding-authentication blocker is remediated.
