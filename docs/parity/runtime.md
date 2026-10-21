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
| `agent action implement` | done | `TestDiffImplementReviewLoop` (4.7, 5.F1, 5.F1b, 5.1, 5.3, 5.13–5.15), `TestDiffPostMerge` (9.10), `TestLocalRuntimeLoop` | `--invocation-id` refused; `--model` without invocation refused (as upstream); no Op records under `kitty-ops/<ULID>.jsonl`; ephemeral merge-driver activation skipped; pre-commit commit-guard pins the Go binary |
| `agent action review` | done | `TestDiffImplementReviewLoop` (5.4, 5.8, 5.11, 5.19) | origin-freshness review-lane reconcile skipped; no Op records |
| `implement` (top level) | done | `TestDiffImplementReviewLoop`, `TestLocalRuntimeLoop` | `--base`, `--recover`, `--owned-checkout` refused as unsupported |
| `next` (query, `--json`) | done | `TestDiffNextFinalizedBoard` (4.1, 4.2, 9.1), `TestDiffNextFreshMission`, `TestDiffPostMerge` (5.21, 8.2) | composed step contracts / composition dispatch not executed (no Op records); `mission_type` governance payload and profile references not rendered in the governance block |
| `next --result success\|failed\|blocked` | done | `TestDiffNextFreshMission`, `TestDiffNextFinalizedBoard`, `TestDiffPostMerge` | `guard_failure_paths` only for "Required artifact missing" |
| `next --answer` | missing | `TestDiffNextFinalizedBoard` (refusal exit only) | decision answers not supported |
| `context` / `context info` | done | `TestDiffRuntimeMisc` | — |
| `context list [--orphaned]` | done | `TestDiffRuntimeMisc` | — |
| `context cleanup [--dry-run]` | done | `TestDiffRuntimeMisc` | — |
| `context mission-resolve` | done | `TestDiffRuntimeMisc` | — |
| `context mission-show` | done | `TestDiffRuntimeMisc` | — |
| `mission run` / `mission-type run` | partial | `TestDiffRuntimeMisc` | MISSION_KEY_UNKNOWN / required-field refusals exact; real custom-mission runs are minimal |
| `mission close` | done | `TestDiffRuntimeMisc`, `TestDiffPostMerge` | no retrospective generation |
| `mission follow-up` | done | `TestDiffRuntimeMisc`, `TestDiffPostMerge` | — |
| `mission reopen` | done | `TestDiffRuntimeMisc`, `TestDiffPostMerge` | — |
| `agent tests stale-check` | partial | `TestDiffRuntimeMisc` | regex/line based instead of Python AST; string-literal heuristics not ported |
| `reconcile` | partial | `TestDiffRuntimeMisc` | snapshot rebuild is an approximation of the upstream dossier builder |
| help pages of all of the above | done | `TestDiffRuntimeHelp` | exit codes and usage lines match; box layout is the shared `internal/cli` renderer |

Other deviations: the charter preflight warning (stderr, consumers
`implement` / `next`) checks charter presence only.

## Exported API

### internal/workflow

- Output: `Out{W, Err}`, `NewOut`, `(*Out).Print/Printf/Console/Consolef/Capture/Captured`, `Exit{Code}`, `ExitErr`; `Tracker` (`NewTracker`, `Add/Start/Complete/Error/Render`).
- Receipts: `Receipt`, `Receipts` (`Record`, `MarkRefused`, `Len`, `PrintSummary`, `JSON`).
- Git: `MergeEnv`, `ExcludeFromGitIndex`, `InstallLaneTipRecorder`, `InstallCommitGuard`.
- Lanes: `LaneTestEnv`, `LaneWorkspaceResult`, `AllocError`, `AllocateLaneWorktree`, `CreateLaneWorkspace`, `LaneSyncError` (`Render`), `SyncLaneAfterCoordinationCommit`.
- Claims: `StartImplementation`, `StartReview`, `StartResult`, `ClaimConflict`, `StartRejected`, `ClaimPolicyMetadata`, `ShellPID`, `CommitWorkflowChange`.
- Implement: `ImplementOptions`, `ImplementResult`, `TopImplement`, `CommitPlanningArtifacts`, `DependencyBlockers`, `DependencyError`, `ImplementJSON`, `PrintWorkspaceReadyBanner`.
- Actions: `ActionImplementOptions`, `ActionImplement`, `ActionReview`, `ResolveBinding`, `NormalizeWPID`, `BranchBanner`, `MissingCanonicalStatus`, `IsFixModeDone`, `PreviewClaimableWP`.
- Analysis / preflight: `AnalysisFreshness` (`Verdict`), `CheckAnalysisReport`, `CharterPreflightWarning`, `EmitCharterPreflight`.
- Prompts: `AgentAssignment`, `ResolveAgent`, `RenderResolvedAgentIdentity`, `IsolationBanner`, `WPPromptWrapper`, `WorkspaceContractDescription`, `SharedArtifactGuidance`, `SafeCommitRecipe`, `CharterContextText`, `PromptTmpDir`, `WritePromptFile`, `WriteWorkflowPrompt`, `TopologyJSON`, `ReviewCycleArtifact`, `LoadReviewCycle`, `LatestReviewCycle`, `FixPrompt`, `ReviewPromptMeta` (`NewReviewPromptMeta`, `Write`).

### internal/nextrt

- `Decision` (`ToObject`), `QueryCurrentState(root, slug, agent)`, `DecideNext(root, slug, agent, result)`.
- `MissionNotFoundError` (`NextStep`), `QueryValidationError`.
- Lifecycle side effects: `PairPreviousLifecycleRecord`, `WriteIssuanceRecord`, `EmitMissionNextInvoked`.
- Run store: `Template`, `TemplateStep`, `ParseTemplate`, `(*Template).HasStep`, `Snapshot`, `ReadSnapshot`, `RunRef`, `ExistingRun`, `GetOrStartRun`.

### internal/coord (additive, `emit_batch.go`)

- `(*Surface).EmitTransitionBatch(reqs []*status.TransitionRequest) ([]*status.Event, error)` — one "status transition batch" commit for several transitions.
- `(*Surface).AppendAnnotationUncommitted(wpID, delta, actor)` — append a resolved-binding annotation without committing.
