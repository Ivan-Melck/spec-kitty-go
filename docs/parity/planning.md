# Parity: planning and authoring (areas E, wave-5 PLAN)

Go implementation of the upstream `spec-kitty` 4.0.0rc8 planning/authoring
surface (specify → plan → tasks → analysis, plus the commit helpers agents
call throughout phases 4–7).

Code: `internal/commands/planning` (commands), registered through
`cmd/spec-kitty/imports_planning.go`. Libraries: `internal/safecommit`,
`internal/decision`, `internal/issuematrix`, `internal/reqmap`,
`internal/missionart`, `internal/branchctx`, `internal/assetpreserve`.

Differential tests set up the precondition with the ORACLE in both sandboxes
(`missionfx.NewOraclePair`, `OracleCalcMission`, `RunOracle`), then run the Go
binary on one side and the oracle on the other and compare exit code,
normalized stdout/stderr, the `kitty-specs/` (and where relevant `.worktrees/`)
trees and the git state (log with name-status over all refs, worktree list,
status, branches). All differential tests run in parallel:

    SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty go test ./internal/commands/planning/... -timeout 0

Go-only tests (`local_test.go`, `internal/reqmap`, `internal/assetpreserve`)
run without the oracle.

## Commands

| Command path | Status | Tests | Deviations |
|---|---|---|---|
| `safe-commit` (`-m` repeated, `--to-branch`, `--json`, mission-aware target, coord/lane worktrees, E2 published missions) | done | `TestDiffSafeCommit`, `TestDiffSafeCommitMission`, `TestDiffSafeCommitPublishedMission`, `TestLocalSafeCommit` | none known (the published-mission write resolves to the Primary Branch and asserts `POST_CONSOLIDATION_WRITE`; off-checkout refuses with `CONSOLIDATED_CONTENT_ABSENT`) |
| `spec-commit` (`--mission`, `-m/--message`, `--json`, directory expansion, router placement) | done | `TestDiffSafeCommitMission`, `TestLocalSafeCommit`, `TestDiffOwnedLifecycleWalk` | `--owned-checkout` / flagless adoption ported; see [owned.md](owned.md) |
| `agent mission check-prerequisites` (`--json`, `--paths-only`, `--include-tasks`, `--require-tasks`, `--resume-probe`) | done | `TestDiffCheckPrerequisites`, `TestLocalCheckPrerequisites`, `TestDiffOwnedPlanningCommands` | `--owned-checkout` / flagless adoption ported (`--resume-probe` refused under an owned checkout, as upstream); see [owned.md](owned.md) |
| `agent check-prerequisites` (hidden alias) | done | `TestDiffCheckPrerequisites` | as above |
| `agent decision open` / `resolve` / `defer` / `cancel` (incl. `--dry-run`, idempotent re-open, terminal conflicts) | done | `TestDiffDecisions`, `TestLocalDecisions`, `TestDiffOwnedPlanningCommands` | `--owned-checkout` / flagless adoption ported (also `list` / `verify`); see [owned.md](owned.md) |
| `agent decision verify` (`--fail-on-stale/--no-fail-on-stale`, marker scan, `DECISION_LOG_FORKED`) | done | `TestDiffDecisions`, `TestDiffDecisionVerifyFork` | none known (a forked PRIMARY/coordination decision-event stream is reported with its reconcile steps and always exits 1) |
| `agent decision list` (`--status`) | done | `TestDiffDecisions` | none known |
| `agent decision repair-runtime-lock` | done | `TestDiffDecisions`, `TestDiffOwnedPlanningCommands` | ported (owned-only: `.gitignore` repair committed through a temporary index; `RUNTIME_LOCK_*` refusals); see [owned.md](owned.md) |
| `agent mission setup-plan` (spec gate, requirement-ID lint, scaffold mode, commit mode, lifecycle markers, documentation wiring) | done | `TestDiffSetupPlan`, `TestDiffSetupPlanDocumentationWiring`, `TestDiffOwnedLifecycleWalk` | `--owned-checkout` / flagless adoption ported (see [owned.md](owned.md)); otherwise none known (documentation missions run the Divio gap analysis for `gap_filling` / `mission_specific`, including upstream's complete-coverage failure, and JSDoc / Sphinx / rustdoc generator detection) |
| `agent tasks map-requirements` (individual, `--batch`, `--replace`, `--tracker-ref`, stale gate, router auto-commit) | done | `TestDiffMapRequirements`, `TestDiffOwnedPlanningCommands` | `--owned-checkout` / flagless adoption ported; see [owned.md](owned.md) |
| `agent mission finalize-tasks` (`--validate-only`, gates, frontmatter normalisation, `lanes.json`, status bootstrap, acceptance matrix, issue-matrix scaffold) | done | `TestDiffFinalizeTasks`, `TestDiffFinalizeTasksHuman`, `TestDiffIssueMatrix`, `TestDiffOwnedPlanningCommands`, `TestDiffOwnedLifecycleWalk` | `--owned-checkout` / flagless adoption ported; see [owned.md](owned.md) |
| finalize-tasks `wps.yaml` tasks-outline manifest flow (pydantic-faithful load errors, manifest dependencies + WP-frontmatter fallback, outline-only validation, tasks.md regeneration / stale report, concern-coverage warnings) | done | `TestDiffFinalizeWpsOutline`, `TestDiffFinalizeWps`, `TestDiffFinalizeWpsHuman`, orchestrator `TestDiffDesignTasksFromOutline` | none known (`wps.yaml` loads with the ruamel safe loader: exact error text; PYF-02 sweep) |
| finalize-tasks ownership inference (`infer_ownership`), code_change + `owned_files: []` contradiction, confined planning_artifact exemption, planning_artifact warning, post-integration acceptance warning, charter activation gate, nearest-match literal-path hint | done | `TestDiffFinalizeOwnershipInference` | none known |
| finalize-tasks `--refresh-planning-commit` / `--allow-orphaned` | done | `TestDiffFinalizeRefreshPlanningCommit` | none known |
| finalize-tasks lane eligibility (canceled WPs, `STALE_CANCELED_DEPENDENCIES`), frozen lane membership (`LANE_MEMBERSHIP_FROZEN`, #5573), parallelization risk report and block mode | done | `TestDiffFinalizeCanceledAndFrozenLanes`, `TestDiffFinalizeWpsHuman`, `TestDiffFinalizeRefusalRestoresStatusSurface` | import-coupling lines are listed in sorted module order (upstream iterates a hash-randomized set; see below) |
| finalize-tasks refusal atomicity (FR-015 mission-directory restore, unpersisted `--target-branch` revert, status-surface guard #5641) | done | `TestDiffFinalizeRefusalRestoresStatusSurface`, `TestDiffDesignTasksFromOutline` | concurrent-writer "kept" reporting (another process changing files mid-run) is not reproduced |
| finalize-tasks on an API-authored mission (`native_finalization_scope`, #5879: stage proof, authoring lock, receipt re-stamp) | done | orchestrator `TestDiffNativeFinalizeAPIAuthored`, `TestDiffDesignTasksFromOutline` | none known |
| `agent tasks finalize-tasks` | done | `TestDiffTasksFinalizeTasks` | none known |
| `agent mission record-analysis` (DIRTY_WORKTREE refusal, `--input-file`/stdin, findings carrier, router commit) | done | `TestDiffRecordAnalysis`, `TestLocalRecordAnalysisDirty`, `TestDiffOwnedPlanningCommands` | `--owned-checkout` / flagless adoption ported; see [owned.md](owned.md) |
| `agent mission record-analysis --report-only` (material-input closure, `DIRTY_ANALYSIS_INPUT`, receipts under the git dir, report-only commit, unchanged re-run, `console.print(dict)` output) | done | `TestDiffRecordAnalysisReportOnly` | `template-selection:*` digests embed the template's absolute path (irreducible, below) |
| `agent issue-verdict` | done | `TestDiffIssueMatrix` | none known |
| `issue-matrix migrate` | done | `TestDiffIssueMatrixMigrate` | none known |
| issue-matrix approval blocker in `agent tasks move-task` (`--to approved/done`) | done | `TestDiffIssueMatrix` | none known |
| `agent mission repair` | done | `TestDiffTracerAndRepair` | none known |
| `agent tracer-append` | done | `TestDiffTracerAndRepair` | none known |
| `intake` (path, `-`, handoff packet, `--show`, `--force`, `--auto`, overwrite guard) | done | `TestDiffIntake`, `TestDiffIntakeOverwriteGuard` | none known (the brief gate routes through `assetpreserve` with no prover and no backup, exactly like upstream) |
| `specify` (create + scaffold-only guidance, `--json` enrichment, `--mission` deprecated alias, `--topology`, widen interview) | done | `TestDiffLifecycle`, `TestDiffLifecycleInteractive`, `TestLocalLifecycleAndResearch` | none known |
| `plan` (delegates to `setup-plan`, plan interview) | done | `TestDiffLifecycle`, `TestDiffLifecycleInteractive`, `TestDiffOwnedPlanningCommands` | `--owned-checkout` forwarded to `setup-plan` (interview skipped under an owned checkout, as upstream); see [owned.md](owned.md) |
| `tasks` (delegates to `agent mission finalize-tasks`) | done | `TestDiffLifecycle` | none known |
| `research` (template scaffold behind the destructive-overwrite guard, `--force`, unfilled-plan gate) | done | `TestDiffLifecycle`, `TestLocalLifecycleAndResearch`, `TestDiffOwnedPlanningCommands` | `--owned-checkout` / flagless adoption ported (see [owned.md](owned.md)); otherwise none known (asset writes route through `assetpreserve`, no prover / no backup, like upstream) |

`agent context resolve` was left to area C (brief item 5).

Help output is byte-identical to `inventory/help` for every path
(`go test ./internal/helpparity`).

The interviews: in non-interactive mode (no TTY, or
`SPEC_KITTY_NON_INTERACTIVE`) every widen question is opened and deferred;
with `SPEC_KITTY_FORCE_INTERACTIVE=1` the prompts read stdin (`!cancel`
cancels, blank defers, anything else resolves), matching
`run_specify_interview` / `run_plan_interview`.

## Irreducible differences

* `record-analysis --report-only`: the `template-selection:<kind>` digests
  hash the selected template's absolute path; upstream records its
  site-packages path for packaged templates, the Go binary records the
  in-binary asset address (`packs/built-in/missions/...`). The
  `package:built-in` / `package:mission-assets` digests do match (the two
  wheel-only `__init__.py` markers are folded in).
* Parallelization risk report: `coupling:` lines for more than one coupled
  module of a lane pair are printed in sorted module order; upstream iterates
  a Python `set`, whose order is hash-randomized per process.
* `analysis-findings` `issue_counts` key order: upstream iterates a
  `frozenset` (hash-randomized); Go uses critical, high, medium, low, info.

## Remaining work

* The analysis-report freshness check (`internal/workflow.CheckAnalysisReport`,
  area CORE) does not yet consult `report_transaction` receipts and the
  material manifest that `--report-only` records
  (`missionart.CollectMaterialInputs`, `planning.ReportIsQualified` are the
  helpers it needs).

## Library API

### internal/safecommit

`safe_commit` and the mission commit router.

* `Commit(repoRoot, worktreeRoot, ref, message string, paths []string, opts Options) (*Result, error)`:
  the guarded commit (HEAD/ref check, staged-unrelated preservation, path
  fates; `Options.PostConsolidation` is `GuardCapability.POST_CONSOLIDATION_WRITE`).
  `Preflight` runs its checks without committing.
* `CommitForMission(root, slug string, files []string, message, kind string, opts RouterOptions) *RouterResult`:
  routes planning artifacts to the right surface by artifact kind.
* Placement: `Placement(root, slug, kind) string`, `ResolvePlacement(root, slug, kind) (string, error)`
  (E2 published short-circuit, `CodeConsolidatedContentAbsent`),
  `LifecyclePhase(root, slug)` (`Phase*`), `ContentPresentAtPrimaryTip`,
  `IsPostConsolidationWriteTarget(root, slug, kind, ref)`, `PrimaryBranch`,
  `PrimaryBranchNoBias`, `TargetBranch`, `IsPrimaryKind`,
  `ResolveCommitPath`, `IsWorktreeOf`.
* Git helpers: `StatusEntries`, `CachedChangedPaths`, `CommittedPaths`,
  `IsTracked`; `Repr`.
* Errors: `*Error` with codes `Code*` (`IsCode(err, code)`); status
  constants `Status*`.

### internal/decision

The Decision Moment ledger (`decisions/index.json`, per-decision artifacts,
runtime lock).

* `Context{Root, Slug, EntrySlug}` with `Open`, `Terminal`, `LedgerDir()`.
* `Load`, `Save`, `FindByLogicalKey`, `Verify(missionDir)`.
* Paths: `Dir`, `IndexPath`, `LockPath`, `ArtifactPath`; `RenderArtifact`,
  `WriteArtifact`.
* `Flows`, `AllStatuses`, `Status*`, error codes `Code*`, `NewError`;
  `ISO`, `PydanticTime`.

### internal/issuematrix

* `LoadRows(dir) *Rows`, `ParseDocument`, `ParseStructuredRows`, `RowsFromLegacy`.
* `DetectReferences`, `DiscoverReferences`, `ScaffoldRows`.
* `Validate(path, content *string) *Result`; `ArtifactPresent(dir)`.
* `ApprovalBlocker(featureDir, target, primaryDir) string`.
* Constants `JSONFile`, `Verdicts`, `EntryFields`, diagnostic codes `Diag*`.

### internal/reqmap

* `Parse`, `Canonical`, `Classify`, `FindAll`; `ID.Canonical/IsFunctional/String`.
* Spec scanning: `ParseSpecIDs`, `DeclaredIDs`, `DeclaredShapePatterns`,
  `FindUndeclaredRequirementCitations`, `FindBareProseRequirementIDs`,
  `LintSpec`, `BlankHTMLComments`.
* Mapping: `TokenizeRefs`, `ReadAllWPRawRefs`, `TasksMDRefs`,
  `TasksMDSections`, `ComputeCoverage`, `BucketOrder`, `FailingReasons`.

### internal/missionart

* `ResolveTemplate(name, root, missionType) ([]byte, bool)` and the tiered
  `ResolveAssetTier(name, subdir, root, mission) (*TemplateResolution, bool)`
  (`Tier*`: mission-scoped / global override, legacy, org packs,
  `~/.kittify` mission and global, packaged default).
* `CollectMaterialInputs(featureDir, root) (*jsonx.Object, error)`
  (analysis_inputs.collect_material_inputs; `*MaterialInputError`,
  `*CharterConfigError`), `ArtifactHashEntry`, `NormalizeTasksMD`,
  `HashInputNames`.
* Substantive-content checks: `IsSubstantive`, `IsSubstantiveText`,
  `IsPlanSubstantive`, `HasSubstantiveFRRow`, `DescribeTechnicalContextGap`,
  `DescribePlanFieldRequirements`, `PlanTemplateName`.
* `ValidateFeatureStructure`, `WriterArtifacts`, `MissionTypeFor`,
  `MissionYAML`, `IsCommitted`, `RepoTreePath`, `InventoryKey`.

### internal/assetpreserve (new)

The prove-ownership-or-preserve guard (`specify_cli.asset_preservation`).

* `GuardDestructiveOverwrite(dest, project, replacementSubstantive, authorized bool, prover Prover, backupParent string) (OverwriteVerdict, error)`.
* `GuardDestructiveRemoval(path, project, prover, isTree, backupParent, dryRun) (OwnershipVerdict, error)`.
* Provers: `Prover` interface, `ManifestProver`, `ManagedPathProver`,
  `CanonicalContentProver` (`VersionMarker`), `AnyProver`; `Proof`.
* Archiving: `ArchiveInto`, `BackupBeforeOverwrite`, `WriteFileVerbatim`,
  `AllocateBackupDir`, `CompactStamp`.

### internal/branchctx

* `Inject(payload, target, current, matchTarget, expectedCheckout) *jsonx.Object`.
* `CurrentBranch(dir)`, `FeatureTargetBranch(featureDir, repoRoot)`, `UTCStamp()`.

### internal/commands/planning (exported for other areas)

* `RecordAnalysisDirtyPreflight`, `ParseAnalysisVerdict` (orchestrator-api).
* `APIAuthoredFinalizeScope`, `FinalizeScope`, `ScopeRefusal` (the
  orchestrator package installs `native_finalization_scope`).
* `ReportIsQualified(root, report, token)` (report-transaction receipts).

## Changes outside the area

* `internal/commands/agentcore/tasks_move.go` (area A, earlier wave):
  additive `issueMatrixBlocker` fact and approved/done guard.
* `internal/rich/pretty.go` (new file, PYF's package): `PrettyRepr`,
  `PrintPretty`, `WithIndentGuides` — rich.pretty for `console.print(<dict>)`.
* `internal/lanes/conflict_pairs.go` (new file, CORE's package):
  `MembershipConflict.Pairs()`, `FrozenMembership.IsEmpty()`.
* No foundation (`internal/cli`, `app`, `jsonx`, `testkit`) changes.
