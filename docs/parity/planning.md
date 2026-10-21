# Parity: planning and authoring (area E)

Go implementation of the upstream `spec-kitty` 4.0.0rc8 planning/authoring
surface (specify → plan → tasks → analysis, plus the commit helpers agents
call throughout phases 4–7).

Code: `internal/commands/planning` (commands), registered through
`cmd/spec-kitty/imports_planning.go`. Libraries: `internal/safecommit`,
`internal/decision`, `internal/issuematrix`, `internal/reqmap`,
`internal/missionart`, `internal/branchctx`.

Differential tests set up the precondition with the ORACLE in both sandboxes
(`missionfx.NewOraclePair`, `RunOracle`), then run the Go binary on one side
and the oracle on the other and compare exit code, normalized stdout/stderr,
the `kitty-specs/` (and where relevant `.worktrees/`) trees and the git state
(log with name-status over all refs, worktree list, status, branches):

    SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty go test ./internal/commands/planning/...

Go-only tests (`local_test.go`, `internal/reqmap`) run without the oracle.

## Commands

| Command path | Status | Tests | Deviations |
|---|---|---|---|
| `safe-commit` (`-m` repeated, `--to-branch`, `--json`, mission-aware target, coord/lane worktrees) | done | `TestDiffSafeCommit`, `TestDiffSafeCommitMission`, `TestLocalSafeCommit` | the `POST_CONSOLIDATION_WRITE` guard capability for a published mission is not distinguished (STANDARD guard always) |
| `spec-commit` (`--mission`, `-m/--message`, `--json`, directory expansion, router placement) | done | `TestDiffSafeCommitMission`, `TestLocalSafeCommit` | `--owned-checkout` refused ("not supported by this build") |
| `agent mission check-prerequisites` (`--json`, `--paths-only`, `--include-tasks`, `--require-tasks`, `--resume-probe`) | done | `TestDiffCheckPrerequisites`, `TestLocalCheckPrerequisites` | `--owned-checkout` refused |
| `agent check-prerequisites` (hidden alias) | done | `TestDiffCheckPrerequisites` | as above |
| `agent decision open` / `resolve` / `defer` / `cancel` (incl. `--dry-run`, idempotent re-open, terminal conflicts) | done | `TestDiffDecisions`, `TestLocalDecisions` | `--owned-checkout` refused with `OWNED_OPTION_UNSUPPORTED` |
| `agent decision verify` (`--fail-on-stale/--no-fail-on-stale`, marker scan) | done | `TestDiffDecisions` | `DECISION_LOG_FORKED` (primary vs coordination decision stream divergence) is not detected |
| `agent decision list` (`--status`) | done | `TestDiffDecisions` | none known |
| `agent decision repair-runtime-lock` | partial | `TestDiffDecisions` (missing `--owned-checkout` usage error) | requires `--owned-checkout`, which is unsupported: always returns the `OWNED_OPTION_UNSUPPORTED` envelope |
| `agent mission setup-plan` (spec gate, requirement-ID lint, scaffold mode, commit mode, lifecycle markers) | done | `TestDiffSetupPlan` | documentation missions' gap analysis / generator detection (`_run_documentation_wiring`) not ported; `--owned-checkout` refused |
| `agent tasks map-requirements` (individual, `--batch`, `--replace`, `--tracker-ref`, stale gate, router auto-commit) | done | `TestDiffMapRequirements` | `--owned-checkout` refused |
| `agent mission finalize-tasks` (`--validate-only`, gates, frontmatter normalisation, `lanes.json` via `lanes.Compute`, status bootstrap on the coord branch, acceptance matrix, issue-matrix scaffold) | done | `TestDiffFinalizeTasks`, `TestDiffFinalizeTasksHuman`, `TestDiffIssueMatrix` | the `wps.yaml` tasks-outline manifest flow is not ported; ownership inference (`infer_ownership`) for WPs missing `execution_mode`/`owned_files` is not ported; `--refresh-planning-commit` and `--owned-checkout` refused |
| `agent tasks finalize-tasks` | done | `TestDiffTasksFinalizeTasks` | as above |
| `agent mission record-analysis` (DIRTY_WORKTREE refusal, `--input-file`/stdin, findings carrier, router commit) | done | `TestDiffRecordAnalysis`, `TestLocalRecordAnalysisDirty` | `--report-only` report transaction not ported (refused); `--owned-checkout` refused |
| `agent issue-verdict` | done | `TestDiffIssueMatrix` | none known |
| `issue-matrix migrate` | done | `TestDiffIssueMatrixMigrate` | none known |
| issue-matrix approval blocker in `agent tasks move-task` (`--to approved/done`) | done | `TestDiffIssueMatrix` | none known |
| `agent mission repair` | done | `TestDiffTracerAndRepair` | none known |
| `agent tracer-append` | done | `TestDiffTracerAndRepair` | none known |
| `intake` (path, `-`, handoff packet, `--show`, `--force`, `--auto`) | done | `TestDiffIntake` | the destructive-overwrite ownership proof is not ported: an existing brief is always protected unless `--force` |
| `specify` (create + scaffold-only guidance, `--json` enrichment, `--mission` deprecated alias, `--topology`, widen interview) | done | `TestDiffLifecycle`, `TestDiffLifecycleInteractive`, `TestLocalLifecycleAndResearch` | none known |
| `plan` (delegates to `setup-plan`, plan interview) | done | `TestDiffLifecycle`, `TestDiffLifecycleInteractive` | `--owned-checkout` forwarded to `setup-plan`, which refuses it |
| `tasks` (delegates to `agent mission finalize-tasks`) | done | `TestDiffLifecycle` | none known |
| `research` (template scaffold behind the destructive-overwrite guard, `--force`, unfilled-plan gate) | done | `TestDiffLifecycle`, `TestLocalLifecycleAndResearch` | `--owned-checkout` refused; the overwrite guard has no package-ownership prover or backup, so an existing file is overwritten only with `--force` |

`agent context resolve` was left to area C (brief item 5).

Help output: flag names, metavars, defaults and help text are copied from
`inventory/help/*.txt`. The panel layout comes from `internal/cli` and
differs from Rich/Typer in column padding, the Arguments type column
(`TEXT` instead of `<str>`) and a `[a|b|c]` choices suffix on flags that have
both a metavar and choices (e.g. `specify --topology`). These are foundation
rendering differences shared by every area.

The interviews: in non-interactive mode (no TTY, or
`SPEC_KITTY_NON_INTERACTIVE`) every widen question is opened and deferred;
with `SPEC_KITTY_FORCE_INTERACTIVE=1` the prompts read stdin (`!cancel`
cancels, blank defers, anything else resolves), matching
`run_specify_interview` / `run_plan_interview`.

## Library API

### internal/safecommit

`safe_commit` and the mission commit router.

* `Commit(repoRoot, worktreeRoot, ref, message string, paths []string, opts Options) (*Result, error)`:
  the guarded commit (HEAD/ref check, staged-unrelated preservation, path
  fates). `Preflight` runs its checks without committing.
* `CommitForMission(root, slug string, files []string, message, kind string, opts RouterOptions) *RouterResult`:
  routes planning artifacts to the right surface (primary vs coordination
  worktree) by artifact kind; `RouterResult` / `SurfaceOutcome` describe each
  surface's outcome.
* `Placement(root, slug, kind) string`, `IsPrimaryKind(kind)`,
  `TargetBranch(root, slug)`, `PrimaryBranch(root)`,
  `ResolveCommitPath(root, candidate)`, `IsWorktreeOf(repo, worktree)`.
* Git helpers: `StatusEntries`, `CachedChangedPaths`, `CommittedPaths`,
  `IsTracked`; `Repr` (Python repr of a string).
* Errors: `*Error` with codes `Code*` (`IsCode(err, code)`); status
  constants `Status*`.

### internal/decision

The Decision Moment ledger (`decisions/index.json`, per-decision artifacts,
runtime lock).

* `Context{Root, Slug, EntrySlug}` with `Open(OpenRequest) (*OpenResponse, error)`,
  `Terminal(TerminalRequest) (*TerminalResponse, error)` (resolve / defer /
  cancel by `Status`), `LedgerDir()`.
* `Load(missionDir) (*Index, error)`, `Save(missionDir, *Index)`,
  `FindByLogicalKey(idx, flow, stepID, slotKey, inputKey) *Entry`,
  `Verify(missionDir) (*VerifyResult, error)` (deferred decisions vs
  `[NEEDS CLARIFICATION]` markers).
* Paths: `Dir`, `IndexPath`, `LockPath`, `ArtifactPath`;
  `RenderArtifact(*Entry)`, `WriteArtifact(missionDir, *Entry)`.
* `Flows`, `AllStatuses`, `Status*`, error codes `Code*`, `NewError`;
  time formatting `ISO`, `PydanticTime`.

### internal/issuematrix

Structured `issue-matrix.json` (and legacy `issue-matrix.md`) reader,
validator and scaffold.

* `LoadRows(dir) *Rows`, `ParseDocument(*jsonx.Object) *Rows`,
  `ParseStructuredRows(markdown) ([]Row, []Diagnostic)`, `RowsFromLegacy`.
* `DetectReferences(path)` / `DiscoverReferences(dir)` find `#NNNN` issue
  references in spec/plan/tasks; `ScaffoldRows(refs) *Rows`.
* `Validate(path, content *string) *Result`; `ArtifactPresent(dir)`.
* `ApprovalBlocker(featureDir, target, primaryDir) string`: the move-task
  approval blocker message ("" = not blocked).
* Constants `JSONFile`, `Verdicts`, `EntryFields`, diagnostic codes `Diag*`.

### internal/reqmap

Requirement-ID grammar and requirement mapping.

* `Parse(token, missionType) (ID, bool)`, `Canonical`, `Classify`,
  `FindAll`; `ID.Canonical/IsFunctional/String`.
* Spec scanning: `ParseSpecIDs`, `DeclaredIDs`, `DeclaredShapePatterns`,
  `FindUndeclaredRequirementCitations`, `FindBareProseRequirementIDs`,
  `LintSpec` (`LintError`, `LintWarning`), `BlankHTMLComments`.
* Mapping: `TokenizeRefs`, `ReadAllWPRawRefs`, `TasksMDRefs`,
  `TasksMDSections`, `ComputeCoverage` (`Coverage`), `BucketOrder`,
  `FailingReasons`.

### internal/missionart

Mission artifact helpers shared by setup-plan, finalize-tasks and research.

* `ResolveTemplate(name, root, missionType) ([]byte, bool)`: project
  overrides, project templates, global `~/.kittify`, then the embedded
  built-in pack.
* Substantive-content checks: `IsSubstantive`, `IsSubstantiveText`,
  `IsPlanSubstantive`, `HasSubstantiveFRRow`, `DescribeTechnicalContextGap`,
  `DescribePlanFieldRequirements`, `PlanTemplateName`.
* `ValidateFeatureStructure(featureDir, checkTasks) *Structure`,
  `WriterArtifacts`, `MissionTypeFor`, `MissionYAML`, `IsCommitted`,
  `RepoTreePath`, `InventoryKey`.

### internal/branchctx

* `Inject(payload, target, current, matchTarget, expectedCheckout) *jsonx.Object`:
  the branch contract keys added to planning JSON payloads.
* `CurrentBranch(dir)`, `FeatureTargetBranch(featureDir, repoRoot)`,
  `UTCStamp()`.

## Changes outside the area

* `internal/commands/agentcore/tasks_move.go` (area A, allowed by the
  brief): additive `issueMatrixBlocker` fact and an approved/done guard
  using `issuematrix.ApprovalBlocker`.
* No foundation (`internal/cli`, `app`, `jsonx`, `testkit`, `rich`) changes.
