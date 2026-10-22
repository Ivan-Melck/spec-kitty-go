# Area F — runtime loop (implement → review → next)

Packages: `internal/workflow` (claims, lane allocation, lane sync, prompts,
commit receipts, analysis gate, top-level implement), `internal/nextrt` (the
`next` state machine: query mode, advancing mode, run store, DAG planner,
board), additive `internal/coord/emit_batch.go`; commands in
`internal/commands/runtime` (registered from `cmd/spec-kitty/imports_runtime.go`).

Differential tests run against the oracle with
`SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty go test ./internal/commands/runtime/`.
Both sandboxes are created with the oracle (init, mission create, finalize,
record-analysis); the commands under test run on each side and stdout, stderr,
exit code, the project tree, `.worktrees/*`, `.kittify` and the per-ref
first-parent git graph are compared after normalisation (ULIDs, SHAs, PIDs,
timestamps, prompt hashes). `TestLocalRuntimeLoop` runs without the oracle.

## Command status

| Command | Status | Differential tests (TRACE steps) | Deviations |
|---|---|---|---|
| `agent action implement` | done | `TestDiffImplementReviewLoop` (4.7, 5.F1, 5.F1b, 5.1, 5.3, 5.13–5.15), `TestDiffPostMerge` (9.10), `TestDiffActionInvocationID`, `TestLocalRuntimeLoop` | ephemeral merge-driver activation skipped; pre-commit commit-guard pins the Go binary |
| `agent action implement/review --invocation-id` / `--model` / `--profile` (dispatch→claim resolved binding read from `kitty-ops/<ULID>.jsonl`) | done | `TestDiffActionInvocationID` (missing/malformed/mismatched Op records, `--model` without invocation, correlated claim) | — |
| `agent action review` | done | `TestDiffImplementReviewLoop` (5.4, 5.8, 5.11, 5.19) | origin-freshness review-lane reconcile skipped |
| `implement` (top level) | done | `TestDiffImplementReviewLoop`, `TestLocalRuntimeLoop` | `--owned-checkout` refused as unsupported (as briefed) |
| `implement --base` (explicit base ref, unresolved/unhonorable refusals, fresh-base guard) | done | `TestDiffImplementBaseRecover` | — |
| `implement --recover` (crash recovery: lane worktree / workspace context rebuilt) | done | `TestDiffImplementBaseRecover` | — |
| `next` (query, `--json`) | done | `TestDiffNextFinalizedBoard` (4.1, 4.2, 9.1), `TestDiffNextFreshMission`, `TestDiffPostMerge` (5.21, 8.2) | `mission_type` governance payload and profile references not rendered in the governance block |
| `next --result success\|failed\|blocked` incl. composition dispatch (step contracts executed through the profile-invocation executor: one Op record per contract step in `kitty-ops/<ULID>.jsonl`, started+completed, committed `op(<profile>): <action> [<id8>]`) | done | `TestDiffNextFreshMission` (kitty-ops + git compared), `TestDiffNextFinalizedBoard`, `TestDiffPostMerge`, `TestDiffCustomMissionRun` | `guard_failure_paths` only for "Required artifact missing"; the Python traceback that upstream logs for a failing decision git log is not emulated |
| `next --answer` (`--decision-id`, pending-decision resolution, `NO_PENDING_DECISION` / ambiguity refusals) | done | `TestDiffCustomMissionRun`, orchestrator `TestDiffAnswerDecisionNative` | — |
| `context` / `context info` | done | `TestDiffRuntimeMisc` | — |
| `context list [--orphaned]` | done | `TestDiffRuntimeMisc` | — |
| `context cleanup [--dry-run]` | done | `TestDiffRuntimeMisc` | — |
| `context mission-resolve` | done | `TestDiffRuntimeMisc` | — |
| `context mission-show` | done | `TestDiffRuntimeMisc` | — |
| `mission run` / `mission-type run` (custom missions: discovery tiers, pydantic-style validation messages, `MISSION_TYPE_CONFLICT`, run start, composed custom steps, `requires_inputs` decision gate) | done | `TestDiffRuntimeMisc`, `TestDiffCustomMissionRun` | — |
| `mission close` | done | `TestDiffRuntimeMisc`, `TestDiffPostMerge` | no retrospective generation |
| `mission follow-up` | done | `TestDiffRuntimeMisc`, `TestDiffPostMerge` | — |
| `mission reopen` | done | `TestDiffRuntimeMisc`, `TestDiffPostMerge` | — |
| `agent tests stale-check` (Python AST port: `internal/pyast` + `internal/staleassert`) | done | `TestDiffRuntimeMisc`, `TestDiffStaleCheck` | stderr compares the last line only (the oracle prints interpreter `UserWarning`s with site-packages paths first) |
| `reconcile` (dossier projection: every non-hidden file of the feature dir, WP static-projection hash, fail-closed schema-invalid WPs, all three divergence kinds) | done | `TestDiffRuntimeMisc` (no snapshot), `TestDiffReconcileDossier` (snapshot recorded by the upstream indexer; parity, runtime-field churn immunity, missing_in_projection / content_mismatch / missing_in_source) | snapshot-load and indexing exception texts are Go's |
| help pages of all of the above | done | `TestDiffRuntimeHelp` | exit codes and usage lines match; box layout is the shared `internal/cli` renderer |

Other deviations: the charter preflight warning (stderr, consumers
`implement` / `next`) checks charter presence only.

## Exported API

### internal/workflow

- Output: `Out{W, Err}`, `NewOut`, `(*Out).Print/Printf/Console/Consolef/Capture/Captured`, `Exit{Code}`, `ExitErr`; `Tracker` (`NewTracker`, `Add/Start/Complete/Error/Render`).
- Receipts: `Receipt`, `Receipts` (`Record`, `MarkRefused`, `Len`, `PrintSummary`, `JSON`).
- Git: `MergeEnv`, `ExcludeFromGitIndex`, `InstallLaneTipRecorder`, `InstallCommitGuard`.
- Wave 4: `BindingRequest{Model, Profile, InvocationID, ProfileVersion}.Resolve(root, slug, wpID, action)`; `ResolveRemote`, `ResolveBaseRef`, `BaseRefUnresolvedMessage`, `UnhonorableBaseError`; `AllocateLaneWorktreeBase`, `CreateLaneWorkspaceBase`; `RecoveryState`, `RecoveryReport`, `ScanRecoveryState`, `RunRecovery`; `ImplementOptions.Base`, `ActionImplementOptions.Dispatch`, `ActionReviewDispatch`. `SyncLaneAfterCoordinationCommit` now runs `internal/autorebase.Attempt` (conflict classifier).
- Lanes: `LaneTestEnv`, `LaneWorkspaceResult`, `AllocError`, `AllocateLaneWorktree`, `CreateLaneWorkspace`, `LaneSyncError` (`Render`), `SyncLaneAfterCoordinationCommit`.
- Claims: `StartImplementation`, `StartReview`, `StartResult`, `ClaimConflict`, `StartRejected`, `ClaimPolicyMetadata`, `ShellPID`, `CommitWorkflowChange`.
- Implement: `ImplementOptions`, `ImplementResult`, `TopImplement`, `CommitPlanningArtifacts`, `DependencyBlockers`, `DependencyError`, `ImplementJSON`, `PrintWorkspaceReadyBanner`.
- Actions: `ActionImplementOptions`, `ActionImplement`, `ActionReview`, `ResolveBinding`, `NormalizeWPID`, `BranchBanner`, `MissingCanonicalStatus`, `IsFixModeDone`, `PreviewClaimableWP`.
- Analysis / preflight: `AnalysisFreshness` (`Verdict`), `CheckAnalysisReport`, `CharterPreflightWarning`, `EmitCharterPreflight`.
- Prompts: `AgentAssignment`, `ResolveAgent`, `RenderResolvedAgentIdentity`, `IsolationBanner`, `WPPromptWrapper`, `WorkspaceContractDescription`, `SharedArtifactGuidance`, `SafeCommitRecipe`, `CharterContextText`, `PromptTmpDir`, `WritePromptFile`, `WriteWorkflowPrompt`, `TopologyJSON`, `ReviewCycleArtifact`, `LoadReviewCycle`, `LatestReviewCycle`, `FixPrompt`, `ReviewPromptMeta` (`NewReviewPromptMeta`, `Write`).

### internal/nextrt

- `Decision` (`ToObject`), `QueryCurrentState(root, slug, agent)`, `DecideNext(root, slug, agent, result)`.
- Composition (wave 4): `StepContract`, `ContractStep`, `StepInput`, `StepContractFromObject`, `LookupStepContract`, `LookupStepContractByID`, `StepContractError`, `ExecuteStepContract`.
- Decisions (wave 4): `NoPendingDecisionError`, `AmbiguousPendingDecisionError`, `ResolvePendingDecisionID`, `ProvideDecisionAnswer`, `AnswerDecision(root, slug, decisionID, answer, agent)`.
- Custom missions (wave 4): `DiscoveredMission`, `DiscoverMissions`, `RunCustomMission(root, key, slug)`; `Template.AuditSteps`, `TemplateStep.AgentProfile/ContractRef`.
- `MissionNotFoundError` (`NextStep`), `QueryValidationError`.
- Lifecycle side effects: `PairPreviousLifecycleRecord`, `WriteIssuanceRecord`, `EmitMissionNextInvoked`.
- Run store: `Template`, `TemplateStep`, `ParseTemplate`, `(*Template).HasStep`, `Snapshot`, `ReadSnapshot`, `RunRef`, `ExistingRun`, `GetOrStartRun`.

### internal/coord (additive, `emit_batch.go`)

- `(*Surface).EmitTransitionBatch(reqs []*status.TransitionRequest) ([]*status.Event, error)` — one "status transition batch" commit for several transitions.
- `(*Surface).AppendAnnotationUncommitted(wpID, delta, actor)` — append a resolved-binding annotation without committing.

### internal/dossier (wave 4, new)

- `Entry{Path, Hash}`, `SnapshotHash(entries)` (compute_dossier_snapshot_hash), `StaticProjectionHash(yamlSrc, meta)` (hash_wp_static_projection with WPMetadata validation), `ValidationError`, `SourceProjection(featureDir)` (Indexer + present_projection), `RecordedProjection(snapshot)`, `Reconcile(source, recorded) Result`, `Result`, `Divergence`.

### internal/invocation (wave 4, new files only)

- `compose.go`: `InvokeOptions`, `InvokeWithHint(root, request, opt)` (explicit profile + action hint), `AvailableProfiles(root)`.
- `claim.go`: `ModelProvider(modelID)`, `ReadStartedForClaim(root, id)`, `PyRepr(s)`.

### internal/pyast, internal/staleassert (wave 4, new)

See `docs/parity/closeout.md` (library API).
