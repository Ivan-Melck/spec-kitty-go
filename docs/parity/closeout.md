# Parity: acceptance, merge and closeout (area G)

Go implementation of the upstream `spec-kitty` 4.0.0rc8 acceptance, merge
and closeout commands. Code: `internal/commands/closeout` (commands,
registered through `cmd/spec-kitty/imports_closeout.go`) and the libraries
`internal/acceptance`, `internal/consolidate` and `internal/retrospective`.

Differential tests (`SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty go test
./internal/commands/closeout/...`) build the precondition state with the
ORACLE in both sandboxes (calc mission: create, author, finalize, analysis,
implement/review/approve of WP01 and WP02, verdicts, accept), then run the
command under test in both and compare exit code, normalized stdout and
stderr, `kitty-specs/` (and where relevant `.worktrees/`, `.kittify/derived`,
`.kittify/workspaces`) file trees with contents, and the git state
(`git log --all --topo-order` with subjects, bodies, decorations and
name-status file sets; worktree list; status; branches; all refs).
`TestLocalCloseout` is the Go-only smoke test (no oracle).

| Command path | Status | Tests | Deviations |
|---|---|---|---|
| `agent acceptance-verdict` (`--criterion/--negative-invariant`, `--result`, `--verification-method/-command`, `--scope`, `--evidence`, `--actor`, `--no-execute`, `--json`) | done | `TestDiffAcceptanceVerdict` (coord, lanes; TRACE 6.2/6.3, errors, unknown mission), `TestLocalCloseout` | `--owned-checkout` is refused ("not supported by this build") |
| `agent mission acceptance-verdict` | done | same | same |
| `agent mission accept` (`--diagnose`, `--json`, lenient/dirty refusals, origin check read-only) | done | `TestDiffAccept` (for-review-dirty, coord, lanes; TRACE 5.F5, 6.1, 6.4, 6.6, 9.3), `TestDiffAcceptMergeCommit` | none known |
| `accept` (`--mission`, `--mode`, `--lenient`, `--no-commit`, `--json`, git_dirty and `docs/` path-convention refusals) | done | `TestDiffAccept` | none known |
| `accept --merge-commit` (`--mode pr/local`, `--target-branch`, `--attest-first-landing-commit`; PR-merge evidence verified against git and recorded) | done | `TestDiffAcceptMergeCommit` | uses area H's `internal/migrate` verifier/recorder |
| `accept` origin freshness (`--origin-check enforce/warn/off`, `SPEC_KITTY_ORIGIN_CHECK`; `--no-commit`/`--diagnose` read-only) | done | `TestDiffAcceptMergeCommit` (local bare remote whose status log moved ahead: refusal, warn advisories, off, read-only modes) | — |
| `accept --normalize-encoding` / `ArtifactEncodingError` (strict UTF-8 artifact reads; repair through `charter.encoding_recovery.recover` with `.bak` backups; ambiguous refusal; backup collision) | done (area ENC) | `TestDiffAcceptNormalizeEncoding` | detection via `internal/charsetnorm` (maintenance.md, "Charset detection") |
| `consolidate` default squash (`--dry-run [--json]`, coord-residue refusal and retry, lane→mission merges, mission_number bake, squash onto the target, done transitions, retrospective capture, worktree/branch/ref teardown, coord flatten, `Next:` hints) | done | `TestDiffConsolidateTrace` (TRACE 7.1–7.4 replay), `TestDiffConsolidate` (coord, lanes; dry-run variants, unknown mission, `--json` without `--dry-run`, primary-dirty refusal, `--skip-review-artifact-check` without `--note`, TRACE 8.1), `TestDiffConsolidateNotReady` | squash content-attribution reconciliation (`content commit ... belongs to NO approved WP — un-attributable`) is not ported: Go prints the verified line; `--attest-*` and `--release-*`: the CLI-boundary validation (reason required, `--abort` only, releasable-branch check, dry-run notices; `TestDiffConsolidateAutoRebaseManual`) is ported, but recording an attestation in the status log and keeping a released branch are not; sparse-checkout detection not ported |
| `consolidate` merge gates (evidence/risk/dependency/issue-matrix completeness + verdict terminality, `policy.merge_gates` mode block/warn/off) | done | `TestDiffConsolidateGates` (blocking risk refusal before any mutation, warn mode proceeds) | — |
| `consolidate` transaction record (`.kittify/runtime/merge/<mission_id>/state.json` + post-fix marker), `--resume`, `--abort` (rollback report, coordination worktree teardown), rollback report on any failure inside the mutation span, ADR-A2 restore targets | done | `TestDiffConsolidateRebase`, `TestDiffConsolidateAutoRebaseManual` (stale lane → rollback report → `--abort`), `TestDiffConsolidateSkipLanes` | simplified rollback authority: no advance intents, no unsettled-branch refusal (`UNEXPLAINED_BRANCH_MOVE`), no `NOT restored` outcome (a moved run-movable branch is always restored to its restore target) |
| `consolidate --strategy merge` (no-ff `Merge <mission> into <target>`, meta.json driver two-way) | done | `TestDiffConsolidateStrategies/lanes-merge` | the merge-strategy content-commit attribution check is not ported (approved-commit reachability is) |
| `consolidate --strategy rebase` (rebase + fast-forward, per-SHA reconciliation, rollback) | done | `TestDiffConsolidateRebase` (`Reconciliation FAILED: approved WPnn commit ... is NOT reachable` + rollback report + record kept, then `--abort`) | same attribution gap as `merge` |
| `consolidate` stale-lane auto-rebase with the conflict classifier (status event-log union, status.json rematerialize, coordination/target-owned take-theirs, pyproject deps / `__init__` imports / URL-list unions, uv.lock regenerate, default manual) | done | `TestDiffConsolidateAutoRebaseManual` (manual conflict aborts, lane stays stale); orchestrator `TestDiffConsolidateMissionAutoRebaseClassifier` (rule-resolved conflicts and the `auto-rebase(lane=…): N conflicts resolved by classifier rules [...]` commit); classifier and TOML validator against CPython in `internal/autorebase` | `merge --abort` after a refused attempt is not guarded (`guarded_merge_abort` refusal not ported); post-merge validation failures name the exception differently (`TOMLDecodeError('<go message>')`) |
| `consolidate --skip-lanes` (no-lane missions: synthesized manifest, planning-artifact closeout) | done | `TestDiffConsolidateSkipLanes` | — |
| `consolidate` origin freshness gate (status evidence + approved lane branches, enforce/warn/off) and `--push` (target sync preflight `TARGET_BRANCH_NOT_SYNCHRONIZED`/`REFRESH_FAILED`, push after the stale check) | done | `TestDiffConsolidatePush` (local bare remote) | — |
| `consolidate` stale-assertion analyzer (`internal/staleassert`, Python AST port) | done | `TestDiffConsolidateTrace`, `TestDiffStaleCheck` (runtime) | — |
| `agent mission merge` (delegates to consolidate; default strategy `merge`; `--auto-retry` relocation via `spec-kitty` on PATH) | done | `TestDiffConsolidateNotReady`, `TestDiffConsolidateStrategies/coord-agent-merge`, `TestDiffAgentMergeAutoRetry` | — |
| `review` (merged-mission validation: WP lane gate, dead-code scan, `mission-review-report.md`; `--mode lightweight|post-merge`) | done | `TestDiffConsolidate` (TRACE 8.3), `TestDiffConsolidateTrace`, `TestDiffConsolidateNotReady` | dead-code scan is a port of the upstream Python-symbol heuristic |
| `retrospect create` (`--overwrite/--update`, `--json`) | done | `TestDiffConsolidate`, `TestDiffConsolidateNotReady` | none known |
| `retrospect summary` (`--json`, `--json-out`, `--limit`, `--since`, `--filter`, `--include-malformed`) | done | `TestDiffConsolidate` | none known |
| `retrospect backfill` (`--dry-run`, `--json`, `--since/--until/--mission`, `--emit-*`) | done | `TestDiffConsolidate` (`--dry-run --json`) | only the dry-run path has a differential test |
| `agent retrospect summary` | done | `TestDiffConsolidate` | none known |
| `agent retrospect synthesize` (`--mission`, `--proposal-id`, `--apply`, `--json`) | partial | `TestDiffConsolidate`, `TestDiffConsolidateNotReady` | generator-schema records (what consolidate and `retrospect create` write) match upstream: empty dry-run/apply batch, `--proposal-id` refused with `unsupported_generator_record`. Legacy-schema records with applicable proposals are not ported (their `--apply` would mutate glossary/doctrine, owned by area C) |
| `archive create` (`--by`, `--reason`, `--json`) | done | `TestDiffConsolidate`, `TestDiffConsolidateNotReady` | none known |
| `archive list` (`--json`) | done | `TestDiffConsolidate`, `TestLocalCloseout` | none known |
| `agent release prep` (`--channel`, `--repo`, `--json`) | done | `TestDiffConsolidate` (alpha/beta/stable), `TestLocalCloseout` | none known |

Git effects of the default consolidate match TRACE 7.4 exactly. Per lane: the
stale-lane gate (files changed on both the mission branch and the lane since
their merge-base, minus target-owned bookkeeping and identical end states);
when stale and the lane worktree exists, the mission branch is merged into the
lane worktree (`Merge branch 'kitty/mission-<slug>' into
kitty/mission-<slug>-lane-x`, status.json re-materialized when status files
are staged), then the lane is merged into the mission branch in a scratch
worktree with the custom merge drivers activated (fast-forward when possible,
else `Merge <lane> into <mission>`). A conflicted auto-rebase runs the
conflict classifier (`internal/autorebase`); a Manual verdict aborts the
merge and the lane is reported stale. Then
`chore(<slug>): assign mission_number=N` on the mission branch,
`feat(kitty/mission-<slug>): squash merge of mission` (parent: previous target
tip), `chore(<slug>): record done transitions for merged WPs`,
`chore(<slug>): capture mission retrospective`, and on the coord topology
`chore(<slug>): flatten coordination metadata after branch deletion (#3086)`;
lane worktrees, lane branches, `refs/spec-kitty/*` refs, the coord worktree and
mission branch are removed; `.kittify/workspaces/<slug>-lane-*.json` deleted.
The derived views under `.kittify/derived/<slug>/` are refreshed only by the
done transitions (as upstream), not after the retrospective event.

## Library API

`internal/acceptance` (acceptance matrix, readiness summary, accept flow):
- Matrix: `Matrix`, `ReadMatrix`, `MatrixFromObject`, `WriteMatrix`,
  `RenderMatrix`, `MatrixReadDir`, `MatrixWriteDir`, `MatrixFile`,
  `UpdateCriterion`, `NewInvariant`, `PopulateFromReview`, `SpliceOwnedRows`,
  `ValidateEvidence`, `EnforceNegativeInvariants` (+ `GateContext`),
  `ResolveActor`, verdict constants (`VerdictPassPendingConsolidation`, ...).
- Readiness: `Collect(root, slug, CollectOptions) (*Summary, error)`,
  `Summary`, `WPState`, `Check`, `Bucket`, `PathFate`, `MissionConfig`/
  `LoadMissionConfig`, `ChooseMode`, `DirtyGate`, `StatusEntries`/`StatusEntry`,
  `UncheckedSubtaskRows`, `IsSelfBookkeeping`, `IsCoordResidueFor`,
  `ChecksFailedMessage`, `PlanningArtifactOnlySkip`, `LaneOrder`.
- Accept: `Perform(summary, mode, actor, tests, autoCommit) (*Result, error)`,
  `RecordAcceptance`, `StampBirthCutover`, commit outcomes `Outcome`/
  `SurfaceOutcome` via `CommitMission`, `CommitArtifact`, `ResidualCommit`,
  `WriteAndCommitMatrix`, `ResidualFailureDetail`, status constants
  (`StatusCommitted`, ...), `Error`, `ParseError`.

`internal/consolidate`:
- `Run(w, root, Options) error` (whole consolidate flow, dry-run included;
  failures return `*Exit{Code}` after printing), `Options{…, Resume,
  OriginCheck, SkipLanes, Push}`, `ResolveMissionSlug(root, handle)`,
  `ResolveRetention(primaryDir, del, rm)` / `Retention`, `NextMissionNumber`,
  `PrimaryBranch(root)`.
- Gates: `GatePolicy`, `LoadGatePolicy(root)`, `GateResult`, `RiskScore(m)`,
  `EvaluateGates(root, primaryDir, statusDir, matrixDir, wpIDs, pol)`.
- Transaction record: `MergeState` (`Object`, `Save`, `Rollback`,
  `BeginAttempt`), `LoadState`, `ClearState`, `Abort(w, root, handle)`.
- Orchestrator lane merge (area I, extended): `MergePreflight`,
  `RunLaneMerge` (now evaluates the merge gates), `LaneMergeError`,
  `DestructiveRefusal`, `OriginGateInputs(root, slug)`.

`internal/autorebase` (new): `Attempt(Options) Report` (attempt_auto_rebase),
`Options{Root, LaneID, Branch, MissionBranch, Worktree, Env, EnsureDrivers}`,
`Report`, `Classify(file, hunk)`, `ValidateResolution`, `ResolveText(file,
body)`, `Classification`/`Resolution`, rule ids `Rule*`, `ValidateTOML(src)`
(TOML 1.0 validity, checked against `tomllib`), `IsTargetOwnedPath`,
`EnsureDriverConfig(root)`, `MergeEnv()`.

`internal/originfresh` (new): `Env`, `Setting`, `ReadOnly`, `ResolveMode`,
`NotCheckedWarning`, `Verdict` (`Payload`), `ConfiguredRemotes`,
`ResolveRemote`, `RemoteHeads`, `FetchBranches`, `ApprovedLaneBranches`,
`Refused{Code, Codes, Verdicts, Message, Headline}`, `Run(root, slug, setting,
laneBranches, mergeRecord) ([]string, *Refused)`.

`internal/staleassert` (new): `Finding`, `Report`, `RunCheck(base, head,
root)`, `FPCeiling`; built on `internal/pyast` (new: Python tokenizer/parser
subset with CPython field order — `Parse`, `Node`, `Walk`, `Defs`,
`StrConstants`, `IsAssertionBearing`, `NamesIn`, `StrValuesIn`,
`ImportBindsName`, `AssignmentListsDunderAll`; checked against CPython's `ast`
over the whole upstream source tree).

`internal/retrospective`:
- `Generate(root, slug, invokedAt) (*Record, error)`, `Author(root, slug, kind,
  command)`, `Capture(root, slug)` (terminus capture: writes
  `retrospective.yaml` and appends `RetrospectiveCaptured` to the status log),
  `Record`, `Finding`, `Serialize`, `LoadRecord`, `ClassifyRecord`,
  `BuildSummary(project, since, limit)`, `MissionDirs`, `MissionStates`,
  `DefaultPolicySource`, `PolicyKeys`, `Now`, `FileName`, `GeneratorVersion`,
  `Width`.

## Foundation changes / extensions to other areas' packages

None to area A. Wave 4 (area L) uses area H's `internal/migrate`
(`VerifyPrMergeEvidence`, `RecordPrMergeBaseline`) for `accept
--merge-commit`, and wires the merge gates and origin gate into area I's
`orchestrator-api consolidate-mission` / `accept-mission` (see
`docs/parity/orchestrator.md`).
