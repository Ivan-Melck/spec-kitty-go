# Area H parity: migrations, materialize, mission-health doctor

Upstream: `spec-kitty-cli` 4.0.0rc8. Command package `internal/commands/maintenance`
(registered via `cmd/spec-kitty/imports_maintenance.go`; the `doctor` leaves
attach to area B's `doctor` group through `app.Group`). Libraries:
`internal/migrate` (one-time mission-data migrations), `internal/healthcheck`
(read-only doctor checks and the mission-state audit engine) and
`internal/missionstate` (`doctor mission-state --fix` / `--teamspace-dry-run`).

Differential tests live in `internal/commands/maintenance/*_test.go` and run
with `SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty go test ./internal/commands/maintenance/`.
They set up the precondition with the oracle on both sides (`missionfx.NewOraclePair`,
`OracleCalcMission`, legacy corpora written identically into both sandboxes),
then compare exit code, normalized stdout/stderr and the touched files.
They run in parallel (one sandbox pair each). `TestDiffMaintenanceHelp` checks
the help text of every command below. Go-only tests (`*WithoutOracle`,
`internal/missionstate` unit tests) run in plain `go test ./...`.

## Commands

| Command path | Status | Differential tests | Known deviations |
|---|---|---|---|
| `materialize` | done | `TestDiffMaterialize` (all missions, `--mission`, unknown handle, `--json`), `TestMaterializeWithoutOracle` | |
| `migrate` (group: project layout migration) | done | `TestDiffMigrateLayout`, `TestDiffMigrateBackfills` (`--dry-run`, `--force`, `-v`, group flags forwarded to leaves, refusal of a flag the leaf does not declare), `TestMigrateWithoutOracle` | |
| `migrate backfill-identity` | done | `TestDiffMigrateBackfills`, `TestDiffMigrateIdentitySentinel` | |
| `migrate backfill-merge-commit` | done | `TestDiffMigrateBackfillMergeCommit` (merge / squash / unlanded / branch tip, `--attest-first-landing-commit`, `--target-branch`, `--dry-run`, bad SHA, unknown mission) | |
| `migrate backfill-mission-type` | done | `TestDiffMigrateBackfills` | |
| `migrate backfill-provenance` | done | `TestDiffMigrateBackfillProvenance` | |
| `migrate backfill-runtime-state` | done | `TestDiffMigrateBackfillRuntimeState` | verify_backfill's pre-#2985 legacy-carrier tolerance, subtask superseded-by-history tolerance, foreign-lane write refusal and the placement-port PRIMARY-home cross-check are not ported (a divergent seed row is always a mismatch). |
| `migrate backfill-topology` | done | `TestDiffMigrateBackfills` (incl. `--restamp-single-branch`) | |
| `migrate backfill-wp-status` | done | `TestDiffMigrateBackfillWPStatus` (evidence manifest good/bad/missing) | WP frontmatter strict validation (`WPMetadata`) is approximated by a known-field check. |
| `migrate charter-encoding` | done | `TestDiffMigrateCharterEncoding` (`--dry-run`, `--yes`, `--json`) | |
| `migrate normalize-lifecycle` | done | `TestDiffMigrateNormalizeLifecycle`, `TestNormalizeLifecycleWithoutOracle` | |
| `migrate rebaseline-dossier-hashes` | partial | `TestDiffMigrateRebaselineDossierHashes` | Canonical (`sha256:`) snapshots, unreadable and source-missing snapshots match; re-indexing a legacy snapshot through the dossier indexer is not ported (reported as `reindex_failed: dossier re-index is not supported by this build`, file untouched). No 4.0 workflow writes dossier snapshots. |
| `migrate repin-hooks` | done | `TestDiffMigrateRepinHooks`, `TestRepinHooksWithoutOracle` | |
| `migrate rewrite-opposed-by` | done | `TestDiffMigrateRewriteOpposedBy` | |
| `doctor mission-state` (`--audit`) | done | `TestDiffDoctorMissionStateAudit` (mode validation, `--fail-on`, `--mission`, legacy/corrupt corpora), `TestDiffDoctorMissionStateFixCalc` (audit from a coordination worktree: `CHECKOUT_DISAGREEMENT` findings + disagreement report) | |
| `doctor mission-state --fix` | done | `TestDiffDoctorMissionStateFix` (dup-key heal, dirty-tree refusal, `--allow-dirty`, `--mission` not found / residue, `--manifest-path`, legacy status rows: aliases, typed lane rows, duplicates, quarantine, coercions, CRLF, drifted `status.json`; manifest, quarantine and every rewritten file compared), `TestDiffDoctorMissionStateFixCalc` (refusal from a lane/coordination worktree), `TestMissionStateFixWithoutOracle` | The wedged-`lanes.json` rebuild (`lanes_rebuilt_from_event_log`) reuses `lanes.Compute` with frontmatter ownership; its literal-path `owned_files` re-validation is simplified (no `create_intent` glob suggestions). The repair lock is an advisory `flock` on `<git-common-dir>/spec-kitty-mission-state.lock`. |
| `doctor mission-state --teamspace-dry-run` | done | `TestDiffDoctorMissionStateTeamspaceDryRun` (empty project, calc mission, audit blockers, transition-matrix/business-rule violations, side logs; envelope sha256 values compared unmasked) | Envelope/payload validation re-implements the spec-kitty-events 10.5.1 `Event` / `StatusTransitionPayload` rules and `validate_transition` (semantic messages exact); pydantic shape-error wording for malformed envelopes (`ENVELOPE_INVALID`) is approximated. A `--mission` selection error prints the exception line instead of rich's traceback box. |
| `doctor mission-state --fixture-dir / --include-fixtures` | missing | — | Test-only scan-root overrides: `--fixture-dir` is refused (exit 2); `--include-fixtures` reports the bundled fixtures as absent (the wheel's audit fixtures are not embedded). |
| `doctor identity` | done | `TestDiffDoctorIdentityTopology` | |
| `doctor topology` | done | `TestDiffDoctorIdentityTopology` | |
| `doctor provenance` | done | `TestDiffDoctorRunIndexProvenanceCutover`, `TestDiffDoctorProvenanceFindings` (catalog + template-set leaks, ambiguous template-set paths, unreadable charter) | "Under the installed built-in pack root" is approximated (the Go build embeds its packs): `$SPEC_KITTY_PACKS_ROOT/built-in` when set, else an existing file inside a `packs/built-in/` tree. Former-checkout proof (pyproject/origin/index checks) is not attempted, so such a template-set path is reported as ambiguous. |
| `doctor workspaces` | done | `TestDiffDoctorWorkspacesSparse` (husks, `--fix`, `--json`) | |
| `doctor sparse-checkout` | done | `TestDiffDoctorWorkspacesSparse` (incl. `--fix`) | |
| `doctor coordination` | done | `TestDiffDoctorCoordination` (healthy, dirty, never-created branch, missing worktree, `--check-staleness`, `--fix`, `--mission`) | |
| `doctor run-index` | done | `TestDiffDoctorRunIndexProvenanceCutover` | |
| `doctor cutover` | done | `TestDiffDoctorRunIndexProvenanceCutover` | |
| `doctor review-cycle-reconcile` | done | `TestDiffDoctorReviewCycleReconcile` | Review-cycle placement (`placement_seam(...).read_dir(REVIEW_CYCLE)`) is approximated by the coordination resolver plus the merged-mission absorption. |
| `doctor decisions` | done | `TestDiffDoctorDecisions` (index/event-log reconcile, forks, `--repair`) | |
| `doctor contracts` | done | `TestDiffDoctorRegistries` | |
| `doctor shim-registry` | done | `TestDiffDoctorRegistries` | |

Doctor leaves owned by area C and not handled here: `charter-packs`,
`invocation-pairing`, `ops`, `mission-type`.

## Exported library API

### `internal/missionstate`

- `RepairRepo(root string, o Options) (*RepairReport, error)` — `repair_repo`; `Options{Mission *string; ManifestPath string; AllowDirty bool; Argv []string; CLIVersion string}`. Errors are `*RepairError`.
- `RepairDuplicateKeys(root string, allowDirty bool, argv []string, cliVersion string) (*RepairReport, error)` — dual-key `review_feedback` heal (`*DupKeyError` when unrepairable).
- `TeamspaceDryRun(root string, handle *string) (*DryRunReport, error)`; `EventsPackageVersion`.
- `SelectMissionDirs(root string, handle *string) (dirs, residue []string, err error)`.
- `RepairReport` (`Count`, `QuarantinedRows`, `ToObject`, `ToJSON`), `MissionResult`, `FileChange`, `RowTransformation`, `DryRunReport`, `RowMapping`.
- `ResolveInvocation(cwd) (invoking, canonical string)`, `WriteRefusal(cwd, root) string`, `AuditDisagreements(cwd, root string, mission *string) []Disagreement` — checkout identity (`core/checkout_identity`).
- `DeterministicULID(seed string) string`, `ScrubSecretArgs(argv []string) []string`.
- Constants: `AuditRoot`, `LanesRebuiltAction`, `StatusJSONDriftNotWrittenAction`, the `LanesRebuildSkipped*` actions, schema versions.

### `internal/healthcheck`

- Mission-state audit: `RunAudit(root string, only *string) *AuditReport`, `AuditReport{Missions}` (`Summary`, `ToObject`, `AddFinding`), `MissionAudit`, `Finding`, `IsTeamspaceBlocker`, `TrackedSpecs`, `IsMissionDir`.
- Identity / topology: `ClassifyMission`, `AuditRepo`, `DuplicatePrefixes`, `AmbiguousSelectors`, `IdentityState`, `StoredTopologyRow`, `TopologyRows`, `TopologyRow`, `PrimaryDirForHandle`.
- Coordination: `CollectCoordination`, `FixCoordination`, `CoordFinding`.
- Decisions: `DiagnoseDecisions`, `RepairDecisions`, `DetectDecisionForks`, `DecisionsReport`, `ForkReport`, `SurfaceLog`, `StreamFinding`.
- Workspaces / sparse checkout: `ScanWorkspaceHusks`, `FixWorkspaceHusks`, `RegisteredWorktrees`, `RefreshStaleCoordWorktrees`, `HuskReport`, `HuskEntry`, `HuskFix`, `ScanSparse`, `SparseReport`, `SparseState`.
- Registries: `CheckShimRegistry`, `ShimReport`, `ShimEntry`, `CheckContractRegistry`, `ContractReport`, `ContractRecord`, `IsFileLineAnchor`, `SchemaError`, `ConfigError`.
- Review cycles: `ReviewCycleReconcile`, `ReconcileReport`, `StrandedFinding`.

### `internal/migrate`

- Shared: `LoadMetaFailClosed`, `MetaReadError`, `MissionDirs`, `SpecsExist`, `UpdateMeta`, `OptStr`, `PyPathLess`, `PrimaryBranch`.
- Layout: `ExecuteLayoutMigration(projectDir string, dryRun bool) *LayoutReport`.
- Identity / topology / mission type: `BackfillIdentity`, `BackfillMissionIdentity`, `IdentityResult`, `BackfillTopology`, `BackfillMissionTopology`, `RestampSingleBranch`, `TopologyResult`, `RestampResult`, `StoredTopology`, `ClassifyTopology`, `DeriveTopology`, `TopologyFromMeta`, `RoutesThroughCoordination`, `LaneIDs`, `HasCodeLanes`, `CoordBranchExists`, `Topo*` constants, `BackfillMissionType`, `MissionTypeResult`.
- Merge evidence: `VerifyPrMergeEvidence`, `RecordPrMergeBaseline`, `PrMergeEvidence`.
- Provenance / charter encoding / opposed-by: `BackfillProvenance`, `ProvenanceSummary`, `RecoverEncoding`, `Recovered`, `AmbiguousBody`, `CharterFiles`, `WriteEncodingProvenance`, `RewriteOpposedBy`, `RewriteResult`.
- Lifecycle / event log: `NormalizeLifecycle`, `NormalizeResult`, `RebuildEventLog`, `RebuildResult`, `BackfillWPIDs`, `DeterministicID`, `LifecycleState`, `LifecycleObject`, `IsMissionCompleted`, `WriteDerivedViews`, `PyStoreError`.
- Runtime state / WP status: `BackfillRuntimeState`, `VerifyRuntimeBackfill`, `CutoverMission`, `CutoverRepo`, `CutoverResult`, `SectionSubtaskRows`, `ApplyWPStatusBackfill`, `ApplyWPStatusBackfillRepo`, `WPStatusResult`, `CoordinationSurfaceIsLive`.
- Hooks / dossiers: `InstallPreCommitHook`, `HookInstall`, `RecordedSnapshotFiles`, `RebaselineSnapshots`, `RebaselineOutcome`.

## Foundation changes and extensions to other areas' packages

- `internal/cli`: `Context.InvokedSubcommand` (click's `ctx.invoked_subcommand`; additive, used by the `migrate` group callback).
- No changes to area A's packages.
