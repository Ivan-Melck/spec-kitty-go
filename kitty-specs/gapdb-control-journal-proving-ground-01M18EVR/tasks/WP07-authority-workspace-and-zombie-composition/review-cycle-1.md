---
affected_files: []
cycle_number: 1
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command:
reviewed_at: '2026-08-30T19:51:41Z'
reviewer_agent: user
wp_id: WP07
---

# WP07 review feedback — cycle 1

Verdict: **changes requested**.

Reviewed implementation commit `ca7447d7f1906a3c883d6c5bd8ab142414148462` and separately owned architecture-classification amendment `c3263af01c52404ed85da3ce5484a9d0869e1bf5`. The lane subsequently merged mission-status commits at `d8730a3ae02cfe3b2adddf9ee6d42286c6bc9ea3`; those commits do not change the reviewed implementation.

## Blockers

### 1. Expiry and owner restart split one logical transition across durable batches

Locations: `internal/provingground/authority.go:417-455`, `internal/provingground/authority.go:462-528`, and `internal/provingground/workspace.go:187-218`.

`Expire` first publishes `authority.expired`, clears `state.current`, unlocks the work aggregate, and only then publishes a separately identified `workspace.quarantined` operation. `ReplaceRuntime` similarly publishes `runtime.owner_restarted`, changes the live epoch and clears predecessor authorities, then emits one or more separate quarantine operations. This violates C-008 and the control-journal contract's one-command/one-batch law.

The failure is observable, not theoretical. A reviewer-only fault test injected `ambiguous_response` on the quarantine application call after claim, bind, and expiry had committed. The result was:

- `Current("work-a")` returned no authority;
- W1 remained `active-bound`;
- retrying the exact expiry `OperationID` returned `stale_lease` before it could reconcile or finish quarantine; and
- a replacement claim succeeded while W1 remained durably active-bound.

The same injection after the owner-restart batch left W1 active-bound in the predecessor binding, while retrying the same restart `OperationID` returned `invalid_command` because the new epoch was already installed. The ephemeral reviewer test was removed after reproduction.

Remediation: reduce lease expiry into one `ValidatedBatchV1` containing the authority-expiry and all attributable workspace-quarantine events/heads. Reduce owner restart into one restart command batch containing the runtime event, predecessor-authority invalidations, and all predecessor workspace quarantines. Lease expiry and owner restart must remain distinct event/command kinds, but each logical command must be internally atomic. Publish the memory projection only after acknowledged commit or same-operation reconciliation. Add cuts before application, after ambiguous application response, and before memory publication; exact same-operation retry must reconstruct the complete result and must never expose an invalid-authority/active-workspace split.

### 2. `Restore` can grant authority from a caller-constructed memory projection

Location: `internal/provingground/authority.go:158-266`.

The comment says `Projection` is derived from validated history and cannot grant authority, but it is an exported, unsealed struct and `Restore` accepts it directly. Structural checks then install `state.current`, active workspace bindings, generation, fence, and head revisions. No `ValidatedJournalV1`, sealed checkpoint/restore admission, admitted playback result, or durable read proves those facts. A caller can synthesize a structurally consistent `Projection`, call `Restore`, and obtain a service whose `Authorize` accepts the invented current authority. This contradicts C-009 and makes crash recovery trust memory-shaped input rather than the journal.

Remediation: make production restore consume a constructor-sealed output from the WP06 validated restore/playback path (or perform that derivation internally), and keep raw projection injection test-only and incapable of constructing a live service. Add a mutant proving a hand-built projection cannot create current authority, plus an end-to-end restore test that rebuilds the exact two-work/binding census from accepted product history.

### 3. GJ-03 orphan attribution is process-local and disappears at the crash boundary

Locations: `internal/provingground/authority.go:199-201` and `internal/provingground/workspace.go:91-98, 238-280`.

The only fact distinguishing an `orphan-unbound` workspace from `indeterminate` is membership in the in-memory `s.orphans` map. A reviewer test executed GJ-03, retained the exact registered Git/workspace facts, discarded the service (simulating process-memory loss), created a fresh service, and reconciled the physical workspace. The exact attributable orphan was classified `indeterminate`, not `orphan-unbound`. Existing `TestGJ03OrphanHasNoAuthorityAndCannotBePromoted` reuses the same service and therefore does not test the actual crash boundary.

Remediation: derive orphan attribution after restart from verified Git/filesystem/workspace-record facts plus durable absence of a binding. Continue to forbid promotion and automatic deletion. Add a fresh-service/process-memory-loss GJ-03 test and prove retry allocates a fresh identity while preserving W1 evidence.

### 4. The slice bypasses the retained execution/capsule surface and is fake-only dead production code

Locations: all new `internal/provingground` production files, especially `authority.go:53-64, 92-146` and `workspace.go:27-56, 59-185`.

The WP required composition with the existing execution/workspace/Git APIs. The new package uses workspace and Git types, but never imports or adapts `internal/execution`; it creates a second `Service`, `Authority`, claim/heartbeat/expiry vocabulary, and capsule-shaped state. `EnsureWorkspace` returns `WorkspaceBinding`, not the frozen `controljournal.AuthorityCapsuleV1` or retained `execution.RunCapsule`, so GJ-04 only replays an in-memory binding and never round-trips the required authority capsule contract.

All application tests use `recordingApplier`, all Git tests use `fakeGit`, and no non-test production caller constructs or calls `provingground.Service`. The only external references are architecture classification/guards. Compile-time interface assertions do not prove that `application.Service`, `gitrepo.Repository`, retained execution, admitted restore, and the capsule contract compose at runtime. This fails the review prompt's dead-code and frozen-contract checks and leaves NFR-017 primary-checkout isolation unproved at this boundary.

Remediation: implement the proving-ground composition as an adapter/caller of the retained execution service and exact frozen authority capsule, rather than a parallel domain surface. Add at least one live production caller (the later campaign command may be the intended owner) and an integration test using the real `application.Service` plus real `gitrepo.Repository` against a temporary real Git repository. The test must return/replay the exact `AuthorityCapsuleV1`, verify primary HEAD/index/tree isolation, and exercise GJ-04 across reconstructed durable state. Keep the one-host, one-process-host qualification boundary explicit; external/multiprocess combined proof may remain WP09/WP10 scope.

## Evidence that passed

- `go test ./internal/provingground -count=1` — pass (`0.153s`), including both 100-repetition focused campaigns.
- `go test -race ./internal/provingground -count=1` — pass (`1.925s`).
- Focused closed-graph, M1 dependency mutants, and exact proving-ground classification tests — pass (`0.684s`).
- `go vet ./internal/provingground` and `git diff --check` — pass.
- The zombie test genuinely writes W1 through POSIX and stale authority calls refuse.
- The bind-versus-expire lock ordering prevents a late binding from escaping quarantine in the tested in-process race.
- The architecture amendment classifies only exact `internal/provingground`; an unknown nested package still fails.
- No new proving-ground import references SQLite, an internal GapDB server, or a fallback authority path.

These green checks do not cover the reproduced atomicity/crash-recovery failures above.

## Contract round-trip

- `contracts/control-journal-v1.md`: **FAIL** — expiry/restart split required facts across batches and lose same-operation continuation.
- `contracts/authority-capsule-v1.schema.json`: **FAIL** — the WP neither produces nor round-trips this exact capsule at workspace delivery/GJ-04.
- `contracts/ordered-trial-v1.schema.json` and evidence contracts: orthogonal to this WP's implementation; WP09 owns final evidence materialization.
- Other storage/checkpoint/sidecar schemas: unchanged by WP07, but their sealed restore boundary must be used rather than replaced by raw `Projection` trust.

## Anti-pattern checklist

1. Dead code: **FAIL** — no live non-test production caller exists for the new module.
2. Synthetic-fixture test: **PASS with limitation** — tests call the new production methods, but fake-only ports cannot establish the required real composition.
3. Silent empty return: **PASS** — no unexplained silent empty-return path found in the WP diff.
4. FR coverage: **FAIL** — FR-023/C-008 crash atomicity, GJ-03 restart reconciliation, exact capsule delivery, and NFR-017 real primary isolation are not proven.
5. Frozen surface: **FAIL** — the frozen authority capsule exists but the new run-delivery path invents and returns a different shape instead of using it.
6. Locked decision: **FAIL** — C-008 and C-009 are contradicted by split batches and unsealed projection restore.
7. Shared-file ownership: **PASS** — architecture policy classification was handled in the separately scoped `c3263af` amendment and its mutant proves exactness.
8. Production fragility: **N/A** — no Go panic/raise-style addition found; the blockers are explicit state-transition defects.

WP08 and WP09 depend on WP07 and must not proceed against this implementation until the corrected WP07 is approved and downstream lanes are rebased.
