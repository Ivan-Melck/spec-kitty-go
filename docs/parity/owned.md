# Area OWN — `--owned-checkout` and write-target resolution

Packages: `internal/ownedcheckout` (port of `mission_runtime/resolution.py`'s
owned half, `core/owned_mission.py` and `cli/commands/_owned_checkout.py`:
ownership claims, the `OwnedCheckout` fact, `resolve_owned_or_adopt`,
typed refusal envelopes, the stale repository-root copy, the runtime-lock
`.gitignore` helpers) and `internal/placement` (the read side of
`placement_seam(repo_root, handle).read_dir(kind)` for the repository-root
case: `resolve_artifact_surface` → `resolve_planning_read_dir` + the
composed-slug backfill recovery and the four-`CoordState` classifier).
Commands: every upstream path that accepts `--owned-checkout`, plus the
single_branch write-checkout refusals and claim lock of
`orchestrator-api start-implementation` and the placement-seam consumers.

Vocabulary: R = repository root checkout, P = owned checkout (a linked
worktree on the mission's write branch, created with `git worktree add`
exactly as upstream expects), S = an unrelated sibling worktree.

Differential tests run against the oracle with
`SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty go test ./internal/commands/<pkg>/ -timeout 0`.
The owned fixtures (`missionfx.NewOwnedPair`, `OracleOwnedMission`,
`OwnedCalcMission`) create R with the oracle, add P (`kitty/demo-owned`) and
S (`codex/sibling`) as linked worktrees, mint the mission into P with
`agent mission create --owned-checkout`, and each step runs from R, P and S,
with the flag and flagless (adoption from P), comparing exit code, stdout,
stderr and the git state of R and P (refs, per-ref first-parent graph,
status of both checkouts).

## Command status

| Command | Status | Differential tests | Deviations |
|---|---|---|---|
| `agent mission create --owned-checkout` (default topology single_branch, protected-target refusals, `owned_checkout` / `canonical_repo_root` payload) | done | `TestDiffOwnedMissionCreate` | none known |
| `specify` | n/a | — | upstream only mentions `--owned-checkout` in `--topology` help (no option) |
| `agent tasks status` (flag + flagless adoption, sole-active default gated by adoption) | done | `TestDiffOwnedTasksStatus`, `TestDiffOwnedLifecycleWalk` | none known |
| `agent tasks move-task` (owned local review lifecycle: claimed → in_progress → for_review → in_review → approved → done; force / skip / arbiter / `--to done` from non-approved stay refused as upstream) | done | `TestDiffOwnedLifecycleWalk` (8 variants: R/P/S × flag/flagless × stale copy) | none known |
| `agent tasks mark-status` | done | `TestDiffOwnedLifecycleWalk` | none known |
| `spec-commit` | done | `TestDiffOwnedLifecycleWalk` | none known |
| `agent mission check-prerequisites` (+ `--resume-probe` owned refusal) | done | `TestDiffOwnedPlanningCommands` | none known |
| `agent decision open` / `resolve` / `defer` / `cancel` / `list` / `verify` | done | `TestDiffOwnedPlanningCommands` | none known |
| `agent decision repair-runtime-lock` (owned only; flock, temporary-index commit, `RUNTIME_LOCK_PRECONDITION_REFUSED` / `_RACE` / `_REPAIR_FAILED`) | done | `TestDiffOwnedPlanningCommands` | none known |
| `agent mission setup-plan` / `plan` (interview skipped when owned) | done | `TestDiffOwnedLifecycleWalk`, `TestDiffOwnedPlanningCommands` | none known |
| `agent tasks map-requirements` | done | `TestDiffOwnedPlanningCommands` | none known |
| `agent mission finalize-tasks` | done | `TestDiffOwnedPlanningCommands`, `TestDiffOwnedLifecycleWalk` | none known |
| `agent mission record-analysis` (+ `--report-only`) | done | `TestDiffOwnedPlanningCommands`, `TestDiffOwnedNextAndActions` | finding counts masked (upstream `issue_counts` frozenset order) |
| `research` | done | `TestDiffOwnedPlanningCommands` | none known |
| `next --owned-checkout` (P-rooted runtime, advancement commit `chore(next): persist <slug> advancement [skip ci]`, R-governed analysis currency) | done | `TestDiffOwnedNextAndActions` | run ids / prompt cache keys masked |
| `agent action implement` / `review --owned-checkout` (validate, then `OWNED_ACTION_UNSUPPORTED` pointing at `next` / `move-task`) | done | `TestDiffOwnedNextAndActions` | none known |
| `accept`, `agent mission accept` | done | `TestDiffOwnedAcceptance` | none known |
| `agent acceptance-verdict`, `agent mission acceptance-verdict` | done | `TestDiffOwnedAcceptance` | none known |
| `charter generate` (explicit claim + `--mission-handle`, owned destinations) | done | `TestDiffOwnedCharterAndOrchestratorDecisions` | none known |
| `agent context resolve` | done | `TestDiffOwnedLifecycleWalk` | none known |
| `orchestrator-api open-decision` / `resolve-decision` / `defer-decision` / `cancel-decision` | done | `TestDiffOwnedCharterAndOrchestratorDecisions` | none known |
| `orchestrator-api start-implementation` single_branch repo-root lane: `WRITE_CHECKOUT_WRONG_BRANCH` → `_OCCUPIED` (cross-Mission scan) → `_DIRTY` (spec-kitty-owned paths exempt, resume exemption), checkout claim lock (`<git-common-dir>/spec-kitty-locks/__checkout-<sha1[:16]>__.status.lock`, 120 s, holder sidecar, `STATUS_LOCK_HELD`) | done | `TestDiffSingleBranchWriteCheckoutRefusals`, `TestDiffSingleBranchClaimLockHeld` | none known (the raw `--mission` handle flows into messages and the occupancy exclude, as upstream: a short handle reads its own claim as occupancy) |
| `validate-encoding`, `validate-tasks` `--mission` (`read_dir(PRIMARY_METADATA / WORK_PACKAGE_TASK)`) | done | `TestDiffPlacementSeamHandleReads` | traceback frames of the uncaught seam exception (last line identical) |
| `doctor identity` / `doctor topology` `--mission` | done | `TestDiffPlacementSeamHandleReads` | traceback frames (as above) |
| `doctor decisions` (STATUS_STATE events dir, unmaterialized placeholder) | done | `TestDiffPlacementSeamCoordStates` | none known |
| `doctor review-cycle-reconcile` (REVIEW_CYCLE + exception absorption) | done | `TestDiffPlacementSeamCoordStates`, `TestDiffDoctorReviewCycleReconcile` | none known |
| `doctor coordination` marker missions (`resolve_planning_read_dir(WORK_PACKAGE_TASK)`) | done | `TestDiffDoctorCoordinationStrand*` | none known |
| `mission current` (`resolve_feature_dir_for_mission`) | done | `TestDiffPlacementSeamHandleReads` | traceback frames (as above) |
| `migrate backfill-runtime-state` `_flip_phase` placement cross-check (`PlacementMismatchError`) | done | `TestDiffBackfillRuntimeStateFlipPlacement` | none known |

## Exported API

`internal/ownedcheckout`:

* Claims: `ResolveOwnershipClaim(checkout, primary) Claim`, `Claim.Err()`,
  `ClassifyWorktreeTopology`, `ReadWorktreeRegistry` / `RegistryError`,
  `IsWorktreeOf`, `GitToplevel`, `GitCommonDir`, `PyResolve`; `Error{Code,
  Message}` and the refusal code constants (`OwnershipBrokenPointer`,
  `OwnedOptionUnsupported`, `OwnedActionUnsupported`, ...).
* Facts: `Fact{RepositoryRoot, OwnedRoot, MissionDir, MissionSlug, Topology,
  WriteBranch}`, `ResolveMission(repoRoot, checkout, handle, Options)`,
  `Adopt`, `ResolveOrAdopt(repoRoot, claim *string, handle, cwd, Options)`,
  `WouldAdopt`, `RefuseAction`, `RequireUnstagedIndex`, `Fact.IsProtected`,
  `Fact.Files`, `Fact.MissionRel`; `Options{Allowed, TargetOverride,
  DiscoverSole}`, `LifecycleTopologies`, `NextTopologies`,
  `SelectionRequired`, `MetaReadError`, `StoredTopology`,
  `ExpectedWriteBranch`, `CurrentBranch`; create-time `CreateRoot`,
  `ResolveCreateRoot`, `CreateRoot.IsProtected`.
* Surface: `Help`, `Envelope`, `SuccessFalseEnvelope`, `JSONErrorEnvelope`,
  `FlatErrorEnvelope`, `ResultErrorEnvelope`, `Payload`, `Emit`,
  `StaleCopy`, `StaleCopyValue`, `StaleCopyWarningText`,
  `EchoStaleCopyWarning`.
* Runtime lock: `RuntimeLockFile`, `RuntimeLockIgnoreState`,
  `EnsureRuntimeLockIgnore` / `IgnoreDelta.Restore`, `PreconditionError`,
  `RelTo`.

`internal/placement`: `PrimaryDir(root, handle)` (read_dir of a PRIMARY
kind, with backfill recovery), `PlanningReadDir(root, handle)`
(`resolve_planning_read_dir`), `ReadDir(root, handle, Kind)`
(`Primary` / `Coord` partition; `*ReadPathNotFound` = StatusReadPathNotFound
with `Deleted` for CoordinationBranchDeleted), `UnsafeSegmentError`,
`CoordBranchExists` (moved from `internal/migrate`, which delegates).

Additive hooks in other packages: `lanes.OwnedCheckoutKind` /
`lanes.OwnedWorkspace`; `safecommit.Options.Owned`,
`safecommit.RouterOptions.Owned`, `safecommit.OwnedPlacement`;
`create.Options.Owned` (+ `Result.OwnedCheckout` / `CanonicalRepoRoot`);
`coord.HatchActive`; `nextrt.AnalysisGoverningRoot`;
`workflow.CheckAnalysisReportIn(dir, root)`;
`acceptance.WriteAndCommitMatrixOwned`; test fixtures in
`internal/testkit/missionfx/owned.go`.

## Irreducible differences

* An uncaught seam exception (`MissionSelectorAmbiguous`,
  `UnsafePathSegmentError`, `ActionContextError`, `PlacementMismatchError`)
  prints Python traceback frames upstream; Go prints the same exception
  line without frames (the tests compare the last stderr line).
* Values masked in the tests: next run ids (uuid4), prompt cache keys
  (sha256 of a sandbox path), correlation ids, `report_transaction`
  tokens, `issue_counts` key order (hash-randomized frozenset).
* The checkout claim-lock holder sidecar records Go's pid and the thread
  name `MainThread`.

## Remaining work

* `placement.ReadDir` does not port `require_coord_worktree_branch` (a
  materialized coordination worktree on the wrong branch raises upstream)
  and has no owned variant (owned reads go through `ownedcheckout.Fact`).
* The upstream lock-order `RuntimeError` (taking the checkout lock while
  holding a Mission lock) has no Go equivalent; a failing `git status` in
  the dirty scan is `LANE_ALLOCATION_FAILED` instead of a traceback.
