# Area A — core mission / status / WP / lanes model

Packages: `internal/mission` (+ `mission/create`), `internal/status`,
`internal/wp`, `internal/lanes`, `internal/coord`, `internal/review`,
`internal/lifecycle`, `internal/atomicfile`; commands in
`internal/commands/agentcore` (registered from `cmd/spec-kitty/imports_core.go`).

Differential tests run against the oracle with
`SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty go test ./internal/commands/agentcore/`
(both sandboxes are initialised with the oracle's `init`; mission creation,
finalize-tasks, record-analysis and `agent action implement|review` are run
with the oracle on both sides as setup). `TestLocalCoreLifecycle` and the
package unit tests run without the oracle.

## Command status

| Command | Status | Differential tests | Deviations |
|---|---|---|---|
| `agent mission create` | done | `TestDiffMissionCreate`, `TestDiffMissionCreateDuplicate` | — |
| `agent mission branch-context` | done | `TestDiffBranchContext` | — |
| `agent tasks status` (human + `--json`) | done | `TestDiffTasksReadAndAnnotate`, `TestDiffMoveTask*`, `TestDiffStatusEmit` | `--owned-checkout` refused ("not supported by this build") |
| `agent tasks list-tasks` | done | `TestDiffTasksReadAndAnnotate` | — |
| `agent tasks mark-status` | done | `TestDiffTasksReadAndAnnotate`, move-task tests | `--owned-checkout` refused |
| `agent tasks add-history` | done | `TestDiffTasksReadAndAnnotate` | — |
| `agent tasks move-task` | done (see deviations) | `TestDiffMoveTaskCoord`, `TestDiffMoveTaskLanes`, `TestDiffMoveTaskWorktree` (TRACE 5.1–5.12, F2–F4, 9.4–9.6), `TestDiffCoreUsageErrors` | see below |
| `agent tasks list-dependents` | done | `TestDiffTasksExtras` | — |
| `agent tasks validate-workflow` | done | `TestDiffTasksExtras` | — |
| `agent tasks check-terminability` | done | `TestDiffTasksExtras` | — |
| `agent tasks finalize-tasks` | missing (wave 3) | — | library support: `lanes.Compute`, `lanes.Write`, `coord.Surface.EmitTransition` |
| `agent tasks map-requirements` | missing (wave 3) | — | — |
| `agent status emit` | done | `TestDiffStatusEmit` | `--profile`/`--invocation-id` not supported by `move-task` (see below) |
| `agent status materialize` | done | `TestDiffTasksReadAndAnnotate` | — |
| `agent status lifecycle` | done | `TestDiffStatusLeaves` | post-mission history (reopen / follow-up events) not rendered |
| `agent status validate` | done | `TestDiffStatusLeaves` | the "orphaned event" stderr WARNING (human mode) is not printed |
| `agent status migrate` | done (removed-command stub) | `TestDiffStatusLeaves` | — |
| `agent status reconcile` | done (removed-command stub) | `TestDiffStatusLeaves` | — |
| `agent status doctor` | done | `TestDiffStatusLeaves` | issue-matrix check not evaluated (no finding); sparse-checkout finding simplified (primary only); human findings table never wraps |

### move-task deviations

Implemented: the full guard sequence (self-review options, protected-branch
refusal, coord skip-primary metadata refusal, agent ownership incl. the
#5196 role allowance, done-exit, rejected-verdict, feedback file, planned
rollback / re-implement edge messages, unchecked subtasks from the event
snapshot, review readiness of the lane worktree — husk, detached HEAD,
in-progress git ops, behind-base, uncommitted changes, no implementation
commit, kitty-specs contamination with coord-residue filtering —, done merge
ancestry + `--done-override-reason`), the transition plan (forward hops,
backward rewinds, wire-contract force rules from `spec_kitty_events`),
review-cycle artifacts (rejection with `--review-feedback-file`, first-pass
approval artifact, commit + read-back durability envelope), lane deliverable
auto-commit before `for_review` (incl. the safe_commit "unrequested paths"
warning), the pre-review gate metadata (`--skip-pre-review-gate`, env skip,
`no_coverage`, undeclared `skipped`), the claim/reviewer resolved-binding
annotations, the runtime-state annotation (agent / shell_pid / assignee /
note / tracker refs / rollback reset + claim release), review-lock release,
derived views, approval-stamp warning and the dependency alert.

Not implemented (returns an error or is skipped):
* `--owned-checkout` (refused), `--invocation-id` (refused: Op records are
  area C), `--model` without `--invocation-id` refused like upstream;
  `--profile` is recorded as the resolved profile without the registry
  schema-version lookup (upstream resolves `agent_profile_version` through
  the profile registry).
* A *declared* pre-review regression gate (`review.test_command` /
  `pre_review_test_scope`): the scoped pytest run is not executed; the
  transition records the undeclared `skipped` verdict.
* Arbiter overrides (`--force` from planned to approved/done over a
  rejection) and `--skip-review-artifact-check` override evidence persistence
  are not recorded (the guard messages are).
* Issue-matrix approval blocker (`issue-matrix.md` referenced issues) is not
  evaluated.
* `--self-review-fallback` validation is implemented; the reviewer
  self-approval annotation it emits is not.
* Forced-approval "not a review approval" warning (#5721) is not printed;
  the unstamped-approval warning is.

## Exported API (for wave 3)

Mission identity / meta / resolution (`internal/mission`):
* `ResolveHandle(root, handle) (dirName, error)` — the `--mission` resolver
  used by every tasks/status command: literal dir with meta.json, identity
  ladder (ULID, mid8 prefix, dir name, NNN-stripped, numeric prefix), bare
  modern slug (`calc-ops` → `calc-ops-<mid8>`), errors `*NotFoundError`,
  `*AmbiguousError`, `*SelectorAmbiguousError` (render with
  `agentcore.renderResolveError` semantics: JSON envelope exit 1, human exit 2).
* `ResolveIdentity`, `Index`, `List`, `BareModernDirName`.
* `ReadMeta`, `ReadMetaOrEmpty`, `WriteMeta(dir, meta, validate)`,
  `RenderMeta` (sorted keys, indent 2, unicode kept), `MissionNumber*`,
  `MissionType`, slug helpers (`DirName`, `Mid8FromSlug`, `StripNumericPrefix`,
  `SafePathSegment`), `ProjectConfig`, `AutoCommit`.
* `ArtifactKindForPath`, `IsCoordResidue` (kind_for_mission_file /
  coord-residue classifier).
* `mission/create.Create(Options) (*Result, error)` — the full mission
  create (meta, spec scaffold, coord branch + worktree, lifecycle events).

Status model (`internal/status`):
* Rows: `Event` (lane transition), `Annotation` + `Delta`
  (InnerStateChanged), lifecycle envelopes via `internal/lifecycle`.
  `AppendEvents(dir, items...)`, `ReadStream`, `ReadRows`, `SerializeLine`.
* Reducer: `Reduce(transitions, annotations, withImplementer)`,
  `ReduceDir`, `MaterializeSnapshot`, `Materialize(dir)` (status.json),
  `Snapshot.WP/Lane/Render`.
* FSM: `ValidateTransition(from, to, GuardContext)`, `CheckTransition`,
  `AllowedTargets`, `MayTransition`, `ResolveLaneAlias`, `EnsureLane`,
  `LaneTargetsForEmit`, `IsBackwardTransition`, `ProgressBucket`.
* Pipeline: `Prepare(*TransitionRequest, PrepareInput)` (alias collapse,
  review-gate inference, force-note refusal, evidence, guard, lane-head
  stamp, claim annotation), `DependencyReadiness`, `UncheckedSubtasks`,
  `DeriveFromLane`, `NewAnnotation`.
* Actors: `SelfAssertingActor`, `ResolvedBinding.ToDelta`, `ActorKey`,
  `LatestImplementer`, `IsLatestImplementer`, `ParseAgentBoundary`.
* Derived views: `RefreshDerivedViews(featureDir, repoRoot)` writes
  `.kittify/derived/<slug>/{status,board-summary,progress,lifecycle}.json`
  (ledger posture + git-operation gate), `BoardSummary`, `ProgressObject`,
  `LifecycleObject`, `ComputeProgress`.
* Validation: `Validate(dir)`, `ValidateEventSchema`,
  `ValidateTransitionLegality`, `ValidateDoneEvidence`,
  `ValidateTimestampContinuity`, `ValidateMaterializationDrift`.

Coordination topology (`internal/coord`):
* `Resolve(root, slug) *Surface` — topology, coord branch/worktree/dir,
  `StatusDir()` (read), `WriteDir()` (materializes the coord worktree),
  `CommitRoot()`, `Transactional()`.
* `Surface.EmitTransition(*status.TransitionRequest) (*EmitResult, error)` —
  the transactional status door: in-lock from-lane derivation, dependency
  verdict, lane-head stamp, append event + claim annotation, materialize,
  commit `chore(spec-kitty): status transition WPnn` (coord worktree for
  coord topologies, primary checkout otherwise), derived-view refresh.
* `Surface.EmitAnnotation(wpID, delta, actor, autoCommit)` — runtime
  annotation; committed as `chore(spec-kitty): inner-state annotation WPnn`
  for coord / stored lanes|single_branch missions when autoCommit.
* `CommitPaths(dir, msg, paths...)`, `IsProtected(root, slug, branch)`,
  `ProtectedBranches`, `LaneHead`.

Work packages (`internal/wp`): `Read`, `Find`, `Match`, `Files`, `LoadAll`,
`WP.Write` (fm.WPFieldOrder), `Dependencies`/`CoerceDependencies`,
`Subtasks`, `AuthoredRoster` (fail-closed subtask roster).

Lanes (`internal/lanes`):
* `Compute(ComputeInput) (*Manifest, error)` — compute_lanes (union-find
  write-scope + surface collapse, collapse report, stable lane ids read back
  from the previous manifest, frozen membership #5573, planning lane,
  single_branch), with `DependencyCycleError`, `MembershipFrozenError`,
  `ComputationError`; `BuildFrozenMembership`, `StartedWPIDs`,
  `GlobsOverlap`, `Fnmatch`, `TopologicalSort`, `InferSurfaces`.
  Golden-tested against the oracle's Python (`testdata/compute_golden.json`).
* `Read`/`Write` lanes.json (`Manifest.Render`, fixed key order).
* Naming: `MissionBranchName`, `LaneBranchName`, `WorktreePath`,
  `CoordWorktreePath`, `CoordMissionDir`, `ParseBranch`.
* Workspaces: `ResolveWorkspace(root, slug, wp)` (planning-artifact /
  planning-lane / persisted context / code lane), `WorkspaceContext`
  + `SaveContext`/`ListContexts`/`FindContextForWP`
  (`.kittify/workspaces/<slug>-<lane>.json`).
* Gates and refs: `EvaluateForReviewGate`, `HasCommitBeyondBase`,
  `RecordTip`/`RecordTipForWP` (`refs/spec-kitty/lane-tip/<branch>`),
  `ReadClaimBase` (`refs/spec-kitty/wp-base/<mission>/<wp>`).

Review cycles (`internal/review`): `Create(CreateInput) (*Cycle, error)`
(review-cycle-N.md on the REVIEW_CYCLE surface, commit
`chore: Record review-cycle-N (<verdict>) for WPnn on <mission>`, read-back
durability `Outcome`), `Pointer`, `NextCycleNumber`, synthetic refs.

## Foundation changes

* `internal/cli`: missing-argument usage errors strip a `{name}` metavar to
  `name` (typer renders `{task_id}` in usage but names `task_id` in the
  error) and drop the `...` suffix for such variadic args.
* `agentcore.RichWrap` (area A's console helper) now keeps in-width trailing
  whitespace like rich's `rstrip_end`.
