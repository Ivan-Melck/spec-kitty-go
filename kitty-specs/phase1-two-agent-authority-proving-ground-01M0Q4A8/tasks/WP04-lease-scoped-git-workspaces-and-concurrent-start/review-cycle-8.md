---
affected_files:
  - internal/execution/start.go
  - internal/execution/start_test.go
  - internal/scenario/workspace_concurrency_test.go
  - internal/workspace/service.go
  - internal/workspace/service_test.go
cycle_number: 8
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command: go test ./internal/scenario -run '^TestStartRunExactReplayRejectsDifferentPhysicalRepositoryBinding$' -count=5
reviewed_at: '2026-08-24T15:19:58Z'
reviewer_agent: codex
verdict: approved
wp_id: WP04
---

# WP04 Review Cycle 8 — Approved

Reviewer: `codex`
Candidate: `ff7d85f44c1bff662e2f85602bfda6f2c78c8927`

## Verdict

**APPROVE.** Candidate `ff7d85f` closes the cycle-6 physical-repository replay
blocker. An exact interrupted-operation replay now authenticates the live
canonical Git repository against its durable `RepositoryBinding` before
authority replay, tracker replay, workspace mutation, or capsule publication.
The original canonical repository and a symlink alias resolving to the same
canonical paths both resume idempotently.

## Repository identity and recovery evidence

- `StartSaga.Start` preserves digest-conflict precedence: it first performs the
  durable operation lookup. A changed request still returns
  `idempotency_conflict` before workspace semantics.
- A new operation calls `Service.Validate`; an exact existing operation calls
  the non-establishing `Service.ValidateBinding` before `authority.Start` and
  tracker assignment.
- `Service.Validate` and `Service.EnsureReady` both reach the same
  `authenticateBinding` live-versus-durable comparison. The ready-state entry
  cannot reserve, create, probe, lock, or publish a workspace through a
  different physical clone.
- Canonical identity compares RepositoryID, ProjectID, canonical common
  directory, canonical primary path, managed root, and target ref. The primary
  HEAD/index/tree snapshot remains an integrity snapshot rather than physical
  identity; target movement retains its typed `target_changed` path.
- Binding mismatch returns the stable typed
  `repository_binding_conflict` diagnostic with `decision_required`, complete
  observed/expected binding facts, and repository-specific recovery actions.
  No prior token or capsule is returned.
- The real-helper SR-04 scenario starts through repository A, clones A to B,
  and replays only the physical `--repository` path through B. It proves the
  refusal creates no clone-B branch or worktree, no managed workspace, and no
  workspace row; authority and durable repository records remain byte-for-byte
  equal; the primary checkout remains unchanged. Unit evidence independently
  proves authority claim and tracker assignment are not called.
- The same scenario then resumes through a symlink alias of A and through A
  itself. Both return the same replayed RunID, LeaseID, token, and workspace
  path at verified `ready` state.

## Mutation evidence

A review-only Go overlay disabled the live-versus-durable comparison without
editing tracked production sources. The committed
`TestValidateBindingRejectsASecondPhysicalRepositoryBeforeWorkspaceMutation`
failed immediately because `ValidateBinding` returned nil. The overlay was
removed, and the unmodified test passed again. This kills deletion of the
shared comparison used by initial and ready-state validation.

## Verification

- Focused workspace binding/readiness tests, `-count=20`: PASS.
- Focused execution replay/precedence tests, `-count=20`: PASS.
- Real-helper clone-B rejection plus alias/original replay, `-count=5`: PASS
  (17.352s).
- `go test ./... -count=1`: PASS (scenario package 60.265s).
- `go test ./internal/scenario -run WorkspaceConcurrency -count=100`: PASS
  (130.919s).
- `go test -race ./internal/workspace ./internal/gitrepo ./internal/execution ./internal/controlstore/sqlite -count=1`: PASS; no race report.
- `go vet ./...`: PASS.
- `gofmt -l` on all candidate files: PASS (no output).
- `git diff --check ff7d85f^ ff7d85f`: PASS.
- `staticcheck`: unavailable in the review environment; skipped explicitly.

## Contract round-trip

- `contracts/commands-and-results.md`: **PASS** — exact replay continues only
  under the original physical binding; mismatch returns the complete typed
  result shape with no authority capsule.
- `contracts/fault-boundaries.md`: **PASS** — the real helper exercises SR-04
  recovery and verifies the universal primary-checkout and authority
  invariants proportionately.
- `contracts/provider-contract.md`: **PASS / orthogonal** — the rejection
  precedes tracker-provider mutation and introduces no provider authority.

## Anti-pattern checklist

1. Dead code: **PASS** — the helper's production start path invokes the new
   binding authentication.
2. Synthetic-fixture escape: **PASS** — decisive evidence uses two real Git
   repositories, SQLite, the real helper process, and a canonical symlink.
3. Silent empty return: **PASS** — refusal is typed and complete.
4. FR coverage: **PASS** — T014/T015/T016 and FR-004/005/018 recovery identity
   requirements are behaviorally pinned.
5. Frozen surface: **N/A** — no migration or frozen schema changes.
6. Locked decision: **PASS** — repository binding remains immutable by
   construction.
7. Shared-file ownership: **PASS** — all candidate files belong to WP04.
8. Production fragility: **PASS** — a different clone fails before any
   authority, tracker, filesystem, Git, or workspace-store mutation.

Verdict: **APPROVE** candidate `ff7d85f`.
