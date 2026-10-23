# Maintenance parity: migrations, materialize, mission-health doctor

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
| `migrate backfill-runtime-state` | done | `TestDiffMigrateBackfillRuntimeState`, `TestDiffMigrateBackfillRuntimeStateTolerances` (pre-#2985 full-payload claim carrier, planned subtask seed superseded by later history, tampered subtask seed stays red), `TestDiffMigrateBackfillRuntimeStateFromWorktree` (cutover from the coordination worktree) | The `_flip_phase` placement-port PRIMARY-home cross-check is ported (`TestDiffBackfillRuntimeStateFlipPlacement`: PlacementMismatchError recorded per mission in the corpus run, uncaught for `--mission`). The foreign-checkout write refusal of the write-intent verify is ported by construction: the CLI resolves the primary root, so the verified feature dir is always canonical and the refusal cannot fire (same as upstream; proven from the coordination worktree). |
| `migrate backfill-topology` | done | `TestDiffMigrateBackfills` (incl. `--restamp-single-branch`) | |
| `migrate backfill-wp-status` | done | `TestDiffMigrateBackfillWPStatus` (evidence manifest good/bad/missing; strict `WPMetadata` validation: wrong types, bad lane, legacy `work_package_title`, timestamps, duplicate keys, non-mapping / missing frontmatter, non-UTF-8), `TestDiffMigrateBackfillWPStatusTypeError` (the uncaught `shell_pid` TypeError) | |
| `migrate charter-encoding` | done | `TestDiffMigrateCharterEncoding` (`--dry-run`, `--yes`, `--json`), `TestDiffMigrateCharterEncodingCorpus` (37-file corpus: cp1252/latin-1/cp1250-1256/koi8-r/Shift-JIS/EUC-JP/GB2312/GBK/Big5/EUC-KR, UTF-8/16/32 with BOMs, stray bytes, binary junk, 1-4 byte files; dry-run, `--yes --json`, rewritten files, `.bak` backups, per-mission and global provenance), `TestDiffMigrateCharterEncodingAmbiguousOnly`, `TestDiffMigrateCharterEncodingBOMDecodeErrors` | Detection is the charset_normalizer port (see "Charset detection" below). BOM decode failures print the plain `Traceback` header plus the exact exception line (and the `[NOTE]` line), not rich's framed traceback (PYF-01). |
| `migrate normalize-lifecycle` | done | `TestDiffMigrateNormalizeLifecycle`, `TestNormalizeLifecycleWithoutOracle` | |
| `migrate rebaseline-dossier-hashes` | done | `TestDiffMigrateRebaselineDossierHashes` (canonical / unreadable / non-object snapshots), `TestDiffMigrateRebaselineReindex` (software-dev / typeless / research / custom / documentation missions, per-file classification and keys, invalid UTF-8, hidden files, the rewritten snapshot file, hashes compared unmasked), `TestDiffMigrateRebaselineReindexFailures` (invalid artifact key, schema-invalid and TypeError WP frontmatter, org-pack manifest override with ghost artifacts, schema-invalid and malformed org manifests) | Artifacts sharing an `artifact_key` keep upstream's `rglob` (directory) order in `artifact_summaries`; Go walks in name order (Irreducible differences). |
| `migrate repin-hooks` | done | `TestDiffMigrateRepinHooks`, `TestRepinHooksWithoutOracle` | |
| `migrate rewrite-opposed-by` | done | `TestDiffMigrateRewriteOpposedBy` | The edited artifact keeps every byte except the removed top-level `opposed_by:` block; upstream re-dumps it through ruamel round-trip, which also normalises `---`, key spacing, sequence/mapping indentation and trailing spaces. Identical for canonically formatted files (see Remaining work). |
| `doctor mission-state` (`--audit`) | done | `TestDiffDoctorMissionStateAudit` (mode validation, `--fail-on`, `--mission`, legacy/corrupt corpora), `TestDiffDoctorMissionStateFixCalc` (audit from a coordination worktree: `CHECKOUT_DISAGREEMENT` findings + disagreement report) | |
| `doctor mission-state --fix` | done | `TestDiffDoctorMissionStateFix` (dup-key heal, dirty-tree refusal, `--allow-dirty`, `--mission` not found / residue, `--manifest-path`, legacy status rows: aliases, typed lane rows, duplicates, quarantine, coercions, CRLF, drifted `status.json`; manifest, quarantine and every rewritten file compared), `TestDiffDoctorMissionStateFixCalc` (refusal from a lane/coordination worktree), `TestDiffDoctorMissionStateFixWedgedLanes` (wedged `lanes.json` rebuild; literal `owned_files` zero-match refusal with nearest-match suggestions, `create_intent` suppression, glob entries skipped), `TestDiffDoctorMissionStateFixLock` (lock contention, record truncated on release, planted symlink refused), `TestMissionStateFixWithoutOracle` | |
| `doctor mission-state --teamspace-dry-run` | done | `TestDiffDoctorMissionStateTeamspaceDryRun` (empty project, calc mission, audit blockers, transition-matrix/business-rule violations, side logs; envelope sha256 values compared unmasked), `TestDiffDoctorMissionStateTeamspaceEnvelopes` (`ENVELOPE_INVALID`: speedate timestamp errors, event_id and aggregate_id errors), `TestPydanticDatetimeAgainstPython` | A `--mission` selection error prints the exception line instead of rich's traceback box (PYF-01). |
| `doctor mission-state --fixture-dir / --include-fixtures` | done | `TestDiffDoctorMissionStateFixtures` (`--audit`, `--teamspace-dry-run`, `--fix` over a sandbox fixture tree, `--mission`, missing scan root, the bundled fixtures inside and outside a project, flag conflict), `TestBundledAuditFixtures` | The bundled fixtures are embedded and materialized once under `~/.kittify/cache/audit-<digest>/fixtures` (upstream: `<site-packages>/specify_cli/audit/fixtures`); the path only shows in the "not found" message. |
| `doctor identity` | done | `TestDiffDoctorIdentityTopology` | |
| `doctor topology` | done | `TestDiffDoctorIdentityTopology` | |
| `doctor provenance` | done | `TestDiffDoctorRunIndexProvenanceCutover`, `TestDiffDoctorProvenanceFindings` (catalog + template-set leaks, ambiguous template-set paths, unreadable charter), `TestDiffDoctorProvenanceFormerCheckout` (former-checkout proof: tracked file, deleted worktree file, wrong project name, foreign origin, untracked file, Windows paths), `TestDiffDoctorProvenanceBadPacksRoot` | The built-in root is `$SPEC_KITTY_PACKS_ROOT/built-in` when it is a directory; without it the Go binary has no installed pack tree, so no absolute path classifies as built-in (Irreducible differences). |
| `doctor workspaces` | done | `TestDiffDoctorWorkspacesSparse` (husks, `--fix`, `--json`) | |
| `doctor sparse-checkout` | done | `TestDiffDoctorWorkspacesSparse` (incl. `--fix`) | |
| `doctor coordination` | done | `TestDiffDoctorCoordination` (healthy, dirty, never-created branch, missing worktree, `--check-staleness`, `--fix`, `--mission`), `TestDiffDoctorCoordinationStrand` (live stranded coord revert found and healed by `--fix`: SHA-scoped revert, marker cleared), `TestDiffDoctorCoordinationStrandRefusals` (legacy marker without recorded SHAs, pruned worktree / STUCK), `TestDiffDoctorCoordinationStrandBranchMismatch` (foreign branch, unrecorded status commit) | Marker missions resolve through `placement.PlanningReadDir` (`resolve_planning_read_dir`, OWN-10). |
| `doctor run-index` | done | `TestDiffDoctorRunIndexProvenanceCutover` | |
| `doctor cutover` | done | `TestDiffDoctorRunIndexProvenanceCutover` | |
| `doctor review-cycle-reconcile` | done | `TestDiffDoctorReviewCycleReconcile`, `TestDiffPlacementSeamCoordStates` (materialized / unmaterialized / deleted coordination surface) | Review-cycle placement is `placement.ReadDir(REVIEW_CYCLE)` with the upstream exception absorption (OWN-10). |
| `doctor decisions` | done | `TestDiffDoctorDecisions` (index/event-log reconcile, forks, `--repair`) | |
| `doctor contracts` | done | `TestDiffDoctorRegistries` | |
| `doctor shim-registry` | done | `TestDiffDoctorRegistries` | |

Doctor leaves owned by area C and not handled here: `charter-packs`,
`invocation-pairing`, `ops`, `mission-type`.

Go-only tests that compare against the oracle venv's Python directly (skipped
when it is absent): `internal/dossier.TestWPMetadataValidationAgainstPython`
(pydantic `WPMetadata` error text and the `shell_pid` TypeError over 24
frontmatter shapes) and `internal/missionstate.TestPydanticDatetimeAgainstPython`
(pydantic/speedate lax datetime verdicts and messages).

## Charset detection (area ENC)

`internal/charsetnorm` is a port of charset_normalizer 3.5.2 as installed in
the oracle venv (`from_bytes` / `best()`): the same candidate list
(`IANA_SUPPORTED_MB_FIRST`, BOM/SIG and declared-encoding priorities), chunking
(`cut_sequence_chunks`, including the multi-byte re-cut), the ten mess
detectors, coherence detection over the `FREQUENCIES` tables, the definitive /
multi-byte / post-definitive shortcuts, fallbacks, submatch folding and
`CharsetMatch.__lt__` ordering. Ordering goes through a port of CPython 3.13's
`list.sort` (timsort, `listobject.c`), because `__lt__` is not a strict weak
ordering and the result depends on the exact comparisons made.

Decoders cover every codec charset_normalizer may pick: 67 single-byte
charmaps, UTF-8/16/32 (+LE/BE), UTF-7, the 16 stateless CJK codecs (byte tries
probed from CPython, with gb18030's four-byte ranges and euc_kr's KS X 1001
make-up sequences ported from `_codecs_cn.c` / `_codecs_kr.c`), `hz` and the
seven `iso2022_*` codecs (state machine from `_codecs_iso2022.c`, charset maps
probed from CPython), with CPython's strict and `errors="ignore"` semantics.
Unicode properties (`unicodedata` 15.1 names → CharInfo flags, `isprintable`,
`isalpha`, `str.lower()` incl. Final_Sigma, `remove_accent`) and the constants
are generated by `$REF/work-ENC/gen.py` (run with the oracle venv's Python; it
writes `internal/charsetnorm/data/*.bin` (zlib) and `constants_gen.go`; gofmt
the latter afterwards).

`charsetnorm.Recover` is `charter.encoding_recovery.recover` (BOM sniff, strict
UTF-8, cp1252 tie-break, honest confidence, lossless `--unsafe` bypass,
`UnicodeDecodeError` text for bytes after a BOM). `internal/charterenc` is
`charter.activation._io` (`load_charter_file`: decode, provenance record,
`CHARTER_ENCODING_AMBIGUOUS`).

Call sites switched to it:

| Upstream use | Go | Tests |
|---|---|---|
| `migrate charter-encoding` (`load_charter_file`) | `internal/migrate.RecoverEncoding` | the four `TestDiffMigrateCharterEncoding*` tests |
| `accept --normalize-encoding`, `ArtifactEncodingError` (`acceptance.normalize_feature_encoding`, `_read_text_strict`) | `internal/acceptance/encoding.go`, `internal/commands/closeout/accept_encoding.go` (+3 hunks in `acceptance.Collect`, the flag in `accept.go`) | `closeout.TestDiffAcceptNormalizeEncoding` (cp1252 repair + `.bak`, ambiguous refusal, backup collision) |
| charter reads (`read_interview_answers`, `ensure_charter_bundle_fresh`/`sync`, `_index_yaml_assets`, `generate`'s `_sync_charter_if_present`) | `internal/charter/encoding_io.go` (`ReadInterview`, `BuildContext`, `ContextJSON`, `RenderCompact`, `Compile`, `charter generate`) | `charterenc.TestDiffCharterReadProvenance`, `TestDiffMissionPromptProvenance`, `TestDiffCharterNonUTF8Answers` (record sequence per command; `CHARTER_ENCODING_AMBIGUOUS` envelopes) |
| `validate-encoding`, `text_sanitization` | unchanged: upstream repairs byte-by-byte through cp1252 and never calls charset_normalizer | — |

Charter loads that raise (`CharterEncodingError`, `UnicodeDecodeError`) unwind
to the command boundary: `charter context` / `charter generate` render their
`Unexpected error` envelope; every other command goes through
`internal/commands/encguard` (registered last), which prints the uncaught
exception line (`CharterEncodingError: CHARTER_ENCODING_AMBIGUOUS`) and exits 1.

Go-only tests against the oracle venv's Python (skipped when absent):
`charsetnorm.TestCodecsMatchCPython` (strict + ignore decode of fuzzed inputs
for all 99 codecs), `TestFromBytesMatchesCharsetNormalizer` (every match:
order, encoding, chaos, coherence, languages, submatches, `language`; corpus +
generated text in every language/encoding), `TestRecoverMatchesUpstream`
(`recover` safe/unsafe incl. error text), `TestPyListSortMatchesCPython`
(timsort driven by random inconsistent comparison tables). Sizes are tunable
with `CHARSETNORM_FUZZ_N`, `CHARSETNORM_DETECT_N`, `CHARSETNORM_SEED`.

Exported API (`internal/charsetnorm`): `FromBytes(seq) *Matches`,
`FromBytesOpts(seq, Options)`, `DefaultOptions()`, `Matches` (`All`, `Best`,
`Len`), `Match` (`Encoding`, `Chaos`, `Coherence`, `Languages`, `Language`,
`BOM`, `CouldBeFromCharset`, `Submatches`, `Runes`, `String`),
`CoherenceMatch`, `LookupCodec(name) Codec` (`Decode(b, ignore)`),
`IANAName`, `Recover(data, unsafe) (*RecoveryResult, error)`, `RecoveryResult`,
`Candidate`, `CP1252Codec`, `DetectBOM`, `DecodeBOMCodec`, `DecodeError`,
`NotedError`. `internal/charterenc`: `LoadFile`, `LoadBytes`, `Content`,
`Error` (`Code`, `Body`), `CodeAmbiguous`, `CodeNotNormalized`,
`AmbiguousBody`, `WriteProvenance`, `ProvenancePath`, `Warn`.
`internal/charter`: `EnsureBundleFresh`, `SyncCharterIfPresent`, `CatchLoad`,
`LoadPanic`, `IsCharterEncodingError`, `ExceptionMessage`.
`internal/acceptance`: `ArtifactEncodingError`, `EncodingBackupCollisionError`,
`NormalizeFeatureEncoding`.

## Irreducible differences

- Encoding-provenance records written by charter loads of built-in doctrine assets carry the installed pack path (site-packages upstream, the materialized embedded pack under `~/.kittify/cache/packs/` in Go); `TestDiffCharterReadProvenance` compares them by their pack-relative part.

- `doctor provenance`: "the installed built-in pack root" is the Python wheel's site-packages location; the Go binary embeds its packs and has no such tree, so without `SPEC_KITTY_PACKS_ROOT` nothing classifies as under it.
- A misconfigured `SPEC_KITTY_PACKS_ROOT` makes upstream log `UserWarning` lines that carry site-packages source paths, line numbers and source excerpts; Go prints nothing (`TestDiffDoctorProvenanceBadPacksRoot` compares stdout and exit code).
- `migrate rebaseline-dossier-hashes`: artifacts that share an `artifact_key` are ordered by upstream's `Path.rglob` (filesystem directory order) inside `artifact_summaries`; Go uses name order. Hashes and counts do not depend on it.
- Uncaught exceptions (`migrate rebaseline-dossier-hashes` non-object snapshot, `migrate backfill-wp-status` TypeError, `doctor mission-state --fix` symlinked lock, `--teamspace-dry-run --mission` selection errors) print rich's framed traceback (`rich.Traceback` via `uncaught`; PYF-01 sweep) with the exact final exception line; the frames inside the panel are sourceless (upstream shows site-packages paths and source excerpts: irreducible).

## Remaining work

- `migrate rewrite-opposed-by`: the artifact rewrite and the graph dump go through `internal/rtyaml` (ruamel round trip, width 4096; PYF-04), so comments and styles of non-canonically formatted artifacts are kept as upstream.
- Charset detection (ENC): a charter-read encoding failure inside a runtime command that renders charter context (`next`, `agent action implement/review` prompts) unwinds to `encguard`'s uncaught-exception line; those commands' own upstream handlers for `CharterEncodingError` were not individually compared (`charter context` / `charter generate` were). Rich's framed traceback for these and for the `migrate charter-encoding` BOM decode errors is PYF-01's sweep (its helper was not yet in functional-parity at 608d3f50).

## Exported library API

### `internal/missionstate`

- `RepairRepo(root string, o Options) (*RepairReport, error)` — `repair_repo`; `Options{Mission *string; ManifestPath string; AllowDirty bool; Argv []string; CLIVersion string; ScanRoot string}` (`ScanRoot` = `scan_root`, the `--fixture-dir` override). Errors are `*RepairError`, or `*NoFollowError` (a symlink planted at the repair lock path; upstream does not catch it).
- `RepairDuplicateKeys(root string, allowDirty bool, argv []string, cliVersion string)`, `RepairDuplicateKeysIn(root, scanRoot string, ...)` `(*RepairReport, error)` — dual-key `review_feedback` heal (`*DupKeyError` when unrepairable).
- `TeamspaceDryRun(root string, handle *string)`, `TeamspaceDryRunIn(root, scanRoot string, handle *string)` `(*DryRunReport, error)`; `EventsPackageVersion`.
- `SelectMissionDirs(root string, handle *string)`, `SelectMissionDirsIn(root, scanRoot string, handle *string)` `(dirs, residue []string, err error)`.
- `NoFollowError` (`kernel.no_follow.NoFollowPathError`).
- `RepairReport` (`Count`, `QuarantinedRows`, `ToObject`, `ToJSON`), `MissionResult`, `FileChange`, `RowTransformation`, `DryRunReport`, `RowMapping`.
- `ResolveInvocation(cwd) (invoking, canonical string)`, `WriteRefusal(cwd, root) string`, `AuditDisagreements(cwd, root string, mission *string) []Disagreement` — checkout identity (`core/checkout_identity`).
- `DeterministicULID(seed string) string`, `ScrubSecretArgs(argv []string) []string`.
- Constants: `AuditRoot`, `LanesRebuiltAction`, `StatusJSONDriftNotWrittenAction`, the `LanesRebuildSkipped*` actions, schema versions.

### `internal/healthcheck`

- Mission-state audit: `RunAudit(root string, only *string) *AuditReport`, `RunAuditIn(root, scanRoot string, only *string) *AuditReport` (custom scan root: every directory is a Mission; `mission_dir` serialised like `_stable_mission_dir_for_json`), `BundledAuditFixtures() string` (the wheel's audit fixtures, materialized under `~/.kittify/cache`), `AuditReport{Missions}` (`Summary`, `ToObject`, `AddFinding`), `MissionAudit`, `Finding`, `IsTeamspaceBlocker`, `TrackedSpecs`, `IsMissionDir`.
- Identity / topology: `ClassifyMission`, `AuditRepo`, `DuplicatePrefixes`, `AmbiguousSelectors`, `IdentityState`, `StoredTopologyRow`, `TopologyRows`, `TopologyRow`, `PrimaryDirForHandle`.
- Coordination: `CollectCoordination`, `FixCoordination`, `CoordFinding` (stranded coord-revert markers are re-verified against the committed coordination ref and healed by `FixCoordination`).
- Built-in pack provenance: `BuiltInPackRoot() string`, `IsBuiltInPackPath(p string) bool`, `IsAbsolutePathAnyOS(v string) bool`, `MissionSuffix(mission string) string`, `SuffixMatches(sourcePath, suffix string) bool`, `MatchesMissionSource(sourcePath, suffix string) bool` (includes the former-checkout proof).
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
- Hooks / dossiers: `InstallPreCommitHook`, `HookInstall`, `RecordedSnapshotFiles`, `RebaselineSnapshots(root string, dryRun bool) ([]RebaselineOutcome, error)` (re-indexes non-canonical snapshots through the dossier indexer port: `Indexer.index_feature` + `compute_snapshot` + the expected-artifacts manifest loader with the org-pack tier; the error is an `*UncaughtError` for a non-object snapshot), `RebaselineOutcome`, `UncaughtError`.

## Foundation changes and extensions to other areas' packages

- `internal/cli`: `Context.InvokedSubcommand` (click's `ctx.invoked_subcommand`; additive, used by the `migrate` group callback).
- `internal/dossier` (CORE), additive new files: `authored.go` (`ReadAuthoredWP(path) (*jsonx.Object, bool, error)` = `read_authored_wp_frontmatter` with strict `WPMetadata` validation; `WPProjectionHash(path) (hash string, verr error, ok bool)` = the indexer's `_hash_wp_projection`; `PyTypeError`) and `wpmeta_pydantic.go` (`WPMetadataValidationError(src, meta) string` = `str(pydantic.ValidationError)` for `WPMetadata` with ruamel round-trip input types; `WPMetadataTypeError(src, meta) string` = the uncaught `shell_pid` TypeError). One-line fix in `dossier.go`: `shell_pid` accepted yamlx's `int` values (they were rejected as invalid, so a WP with a numeric `shell_pid` hashed as non-present).
- `internal/healthcheck` reads pyproject.toml with `internal/pytoml` (CPython `tomllib`; PYF-03 sweep).
- `docs/parity/setup.md`: the `materialize` / `migrate` rows point here (DOCS-03).

## Wave 5 (area COLOR)

Doctor grids and cells carry upstream's styles; `doctor mission-state --fix`
lines print through the console (soft wrap, highlighting); uncaught migrate
exceptions with PEP 678 notes render them as rich `[NOTE]` lines.
