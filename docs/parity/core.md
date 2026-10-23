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
| `agent mission create` | done | `TestDiffMissionCreate`, `TestDiffMissionCreateDuplicate`, `TestDiffMissionCreatePendingOrigin` | — (a staged `.kittify/pending-origin.yaml` fails closed after the scaffold commit and rolls the git state back, CORE-09) |
| `agent mission branch-context` | done | `TestDiffBranchContext` | — |
| `agent tasks status` (human + `--json`) | done | `TestDiffTasksReadAndAnnotate`, `TestDiffMoveTask*`, `TestDiffStatusEmit`, `TestDiffTasksStatusStaleness`, `TestDiffOrphanedEventWarnings` | `--owned-checkout` / flagless adoption ported (`TestDiffOwnedTasksStatus`, `TestDiffOwnedLifecycleWalk`; see [owned.md](owned.md)); claim liveness: Linux `/proc`, darwin `kinfo_proc` start time (CORE-08, Go-only on darwin), signal-0 elsewhere |
| `agent tasks list-tasks` | done | `TestDiffTasksReadAndAnnotate` | — |
| `agent tasks mark-status` | done | `TestDiffTasksReadAndAnnotate`, move-task tests, `TestDiffOwnedLifecycleWalk` | `--owned-checkout` / flagless adoption ported; see [owned.md](owned.md) |
| `agent tasks add-history` | done | `TestDiffTasksReadAndAnnotate` | — |
| `agent tasks move-task` | done | `TestDiffMoveTaskCoord`, `TestDiffMoveTaskLanes`, `TestDiffMoveTaskWorktree` (TRACE 5.1–5.12, F2–F4, 9.4–9.6), `TestDiffMoveTaskArbiter`, `TestDiffMoveTaskArbiterIssueMatrix`, `TestDiffMoveTaskSelfReviewFallback`, `TestDiffMoveTaskProfile`, `TestDiffMoveTaskInvocationID`, `TestDiffPreReviewGate*` (Declared, NoNewFailures, UnverifiedBaseline, JUnit, Edges), `TestDiffCoreUsageErrors` | see below |
| `agent tasks list-dependents` | done | `TestDiffTasksExtras` | — |
| `agent tasks validate-workflow` | done | `TestDiffTasksExtras` | — |
| `agent tasks check-terminability` | done | `TestDiffTasksExtras` | — |
| `agent tasks finalize-tasks` | done (area PLAN, see planning.md) | — | — |
| `agent tasks map-requirements` | done (area PLAN, see planning.md) | — | — |
| `agent status emit` | done | `TestDiffStatusEmit` | — |
| `agent status materialize` | done | `TestDiffTasksReadAndAnnotate` | — |
| `agent status lifecycle` | done | `TestDiffStatusLeaves`, runtime `TestDiffPostMerge`, `TestDiffOrphanedEventWarnings` | — (post-mission history, `reopened` state and `last_follow_up_at`, CORE-01) |
| `agent status validate` | done | `TestDiffStatusLeaves`, `TestDiffOrphanedEventWarnings` | — (orphaned / unsafe legacy-slug warnings once per upstream `read_events` pass, CORE-02) |
| `agent status migrate` | done (removed-command stub) | `TestDiffStatusLeaves` | — |
| `agent status reconcile` | done (removed-command stub) | `TestDiffStatusLeaves` | — |
| `agent status doctor` | done | `TestDiffStatusLeaves`, `TestDiffStatusDoctorChecks`, `TestDiffOrphanedEventWarnings` | — (issue-matrix check CORE-03, sparse scan over lane/coord worktrees CORE-04, wrapping findings table CORE-05) |

### move-task coverage

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
derived views, the approval warnings (unstamped approval and the #5721
forced-approval "not a review approval" line, shared with `status emit`)
and the dependency alert. Also: arbiter overrides (`--force` planned ->
approved/done over a rejection: `[category] explanation` ReviewOverride
annotation, rejection `review_ref` reused on the forward event, no
approval artifact), the OLD-timing `--skip-review-artifact-check`
override persist, the FR-002 verdict revert (an emitted-transition failure
deletes and commits away the just-written review-cycle artifact), and the
`ReviewerSelfApproval` lifecycle event of `--self-review-fallback`.

Also implemented (wave 5):
* `--invocation-id` reads the dispatch Op record and stamps the resolved
  binding (profile, profile version, model, provider); mismatches with
  `--profile` / `--model`, a malformed or missing record, and a wrong
  mission / WP / action (review for in_review/approved/done) are refused with
  upstream's messages (CORE-06).
* The declared pre-review regression gate (`internal/prereview`, CORE-07):
  `review.test_command` scope source (`{output_file}` substitution, `sh -c`),
  lazy fork-point baseline capture in a detached scratch worktree with the
  `chore: Capture baseline tests for WPnn` commit, head run with the 300 s
  timeout / 30 s heartbeat / machine-wide run lock
  (`~/.spec-kitty/gate-locks/pre-review-gate-run.lock`), JUnit and
  `FAIL <test>` parsing, baseline diff, budget assessment (`scope_identity`),
  gate-binding resolution from the review step contract + DRG activation
  (no-contract / no-binding / not-activated reasons), the override tier
  (frontmatter `pre_review_test_scope`, `review.pre_review_test_command`),
  `fail_on_pre_review_regression` block, `--force` bypass, terminal
  timed_out/cancelled hard stops and the byproduct compensator.
* The OLD-timing arbiter persist runs before the issue-matrix guard, so an
  issue-matrix refusal leaves the arbiter override on disk (DOCS-07).
* Lane-deliverable commits relay pre-commit hook stderr as the
  "produced warnings on a successful commit" log line.

### tasks status

Rows carry the event-sourced verdict flags (`_stale_verdict` /
`stale_review_artifact`, `_damaged_verdict`) for approved/done WPs, the
in_review stall label (`review.stall_threshold_minutes`, default 30) and the
in_progress staleness object (lane-worktree commit heartbeat since the merge
base with the default branch, `--stale-threshold`, live-claim suppression via
`shell_pid` + creation-time baseline read from the primary-dir snapshot like
upstream, `not_applicable` for repo-root workspaces, fallback when lanes.json
is missing). Human output renders the arbiter override history, stale/stall
markers, the "stale WP(s) detected" hint and the 👤 human-in-charge marker.

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
* Claim liveness: `CreationTimeBaseline(pid)` (psutil `create_time()`
  string), `IsProcessAlive`, `IsClaimingProcessAlive(pid, baseline)`.
* Lifecycle (`internal/lifecycle`): `Persist`, `EmitMissionCreated`,
  `EmitArtifactPhase`, `EmitReviewerSelfApproval`.
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
  whitespace like rich's `rstrip_end`; `cellWidth` widens a narrow
  emoji-capable character followed by VS16 (rich 15 `narrow_to_wide`).
* Test helper: `comparePairSteps` logs every step's output when
  `KITTY_TEST_TRACE=1`.

## Wave 5 (area CORE): new library API

`internal/prereview` (new; the move-task pre-review regression gate):
* Config: `ReviewSection(root)`, `BlockEnabled(root)`, `TestCommandTemplate(root)`, `Truthy`, key constants.
* Commands: `ShlexValid`, `ShellQuote`, `BuildShellCommand(template, subs)` (build_shell_command_with_substitutions).
* Scope source: `DeclaredSource` (`NewDeclaredSource`, `TestCommand`, `OutputFile`, `ParseMode`, `Identity`, `ParseResults`), `Raw`.
* Baseline: `Failure`, `Baseline` (`Object`, `Save`), `LoadBaseline`, `Sentinel`, `Diff`, `ParseJUnit`, `ArtifactPath`, `Capture(CaptureOptions)`, `UTCSecondStamp`.
* Runs: `RunRaw(cmd, RunOptions)` (timeout, heartbeat, process-group kill, machine lock), `RunScopedTestsAtHead`, `RuntimeRoot`, `PythonExecutable`.
* Verdicts: `Scope`, `Verdict`, `Observer`, `EvaluateWithScope`, `EvaluatePreReview`, `AssessBudget`/`Assessment`, `AggregateVerdicts`, `Translate`/`Effect`, `Metadata`, `ConsoleWarning`, `BlockMessage`, `EmptyScopeVerdict`, `NotDeclaredVerdict`, `PyG`.
* Bindings: `ResolveGateBindings(root, missionType, edge)` / `BindingResolution`, `PreReviewHandler`.

Additive exports elsewhere (CORE-owned packages):
* `internal/status`: `PostMissionEvents`, `FormatPostMissionEvents`, `MissionReopenedEvent`/`FollowUpRecordedEvent`, `SlugResolverWarnings`, `DoctorHooks` (variadic `RunDoctor` hooks); liveness split by platform (`liveness_linux.go`, `liveness_darwin.go`, `liveness_other.go`).
* `internal/mission`: `UnsafeSegmentReason`; `internal/mission/create`: pending-origin refusal with git rollback.
* `internal/dossier`: `LoadSnapshot`, `SnapshotPath` (pydantic-faithful MissionDossierSnapshot validation).
* `internal/orchestrator`: `SelectorAmbiguousCrash`.
* `internal/workflow`: `CheckClaimAncestry`, `ResolveClaimAncestry`, `AncestryResult`; `RunCharterPreflight`, `PreflightResult`, `CharterPreflightOrAbort`, `CharterPreflightWarnOnly` (+ `EmitCharterPreflight` / `CharterPreflightWarning`); report-only receipt + material-input check in `CheckAnalysisReport`.
* `internal/nextrt`: guard-failure paths on every advancing decision; WP prompt mission-type payload.
* `internal/testkit/missionfx`: `WriteOpRecord`.

## Wave 5 extensions to other areas' packages (additive)

* `internal/originfresh/review.go` (CONS): `PlanReviewLane`, `ReviewLaneAction` + kinds, `CheckBranch`, `Headline`, `LaneDivergedText`, `ReviewWarningText`.
* `internal/retrospective/writer.go` (CONS): `AuthorRuntime` (runtime-actor capture with a provenance kind; `Author` unchanged).
* `internal/commands/orchestrator/register.go` (shared): `verb.mission` renders `SelectorAmbiguousCrash`.
* `internal/healthcheck` (MAINT) and `internal/issuematrix` / `internal/missionart` (PLAN) are read only.

## Irreducible differences

* Commit-guard pre-commit hook (CORE-11): the hook installed by `agent action implement` invokes the running Go binary; upstream's pins its Python entry point. A Go build has no Python entry point to name.
* `agent tests stale-check` test normalisation (CORE-20): the oracle prints interpreter `UserWarning`s with site-packages paths on stderr first; only the final line is compared.
* Override-tier pre-review runs (`pre_review_test_scope`): upstream runs `sys.executable -m pytest`; Go runs `python3 -m pytest` (or `uv run … pytest` with uv + pyproject.toml, like upstream).
* `completeness_status must be one of {...}` (dossier snapshot validation) renders a Python set whose order depends on string-hash randomisation; Go prints one fixed order.
* Built-in asset paths in prompts (`Reference Docs`) name the Go pack cache instead of site-packages (cross-cutting item 7).
* Claim liveness on darwin is Go-only tested (no oracle on that platform).
* `MissionSelectorAmbiguous` traceback (CORE-19): rich's framed traceback with the exact final exception lines (f6127735); the frames inside the panel are sourceless (upstream shows site-packages paths and source excerpts).

## Remaining work

* None for CORE-01..CORE-20 / DOCS-01 / DOCS-07.

## Wave 5 sweep (area SWEEP)

* Dirty verdicts (`internal/refadvance`, port of `git/ref_advance._dirty_verdicts`): the review-lane fast-forward (`agent action review` origin freshness, `advance_branch_ref`) refuses on tracked changes and on untracked or gitignored paths a `reset --hard` to the new tip would overwrite (`?? TEAM.md (would be overwritten by reset --hard to <sha12>)`), at most 20 entries listed; the `mission close --discard` lane-worktree removal lists every untracked file individually (`--untracked-files=all`), exempts a VCS-lock-only `meta.json`, and ignores gitignored entries (no reset target). `TestDiffDirtyVerdicts`.
* The `_SlugResolver` orphaned / unsafe-slug warning is logged by every `status.ReadStream` (= one upstream `read_events` pass) through `status.SlugWarnLog` (installed by the CLI logging bootstrap in human mode), plus `status.ExtraReadPasses` where upstream reads the log more often than the port (locate_work_package's reconstruct, the current-agent read, the forced-move arbiter read, append-verify reads and the transition readback, the review-gate inferences, tasks status per-WP rows / verdicts / arbiter rows, `materialize`'s progress and lifecycle passes, `next`'s guard presence reads and stall check, `agent status emit`'s current-lane read); reads with no upstream pass run under `status.Quietly`. Counts match the oracle along a full WP lifecycle (`TestDiffOrphanedEventWarningsAcrossCommands`).
* `next` (advancing, human mode) prints upstream's stalled in_review interventions (`_print_stalled_wp_interventions`, `review.stall_threshold_minutes`).

New library API: `internal/refadvance` (`DirtyVerdicts`, `Options`, `Verdict`, `Reason`, `Lines`, `TargetTreePaths`, `StatusEntries`, `Entry`, `FormatEntryLines`); `internal/status`: `SlugWarnLog`, `ExtraReadPasses`, `Quietly`.
