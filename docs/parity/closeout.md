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
| `agent acceptance-verdict` (`--criterion/--negative-invariant`, `--result`, `--verification-method/-command`, `--scope`, `--evidence`, `--actor`, `--no-execute`, `--json`) | done | `TestDiffAcceptanceVerdict` (coord, lanes; TRACE 6.2/6.3, errors, unknown mission), `TestLocalCloseout` | `--owned-checkout` / flagless adoption ported (`TestDiffOwnedAcceptance`); see [owned.md](owned.md) |
| `agent mission acceptance-verdict` | done | same | same |
| `agent mission accept` (`--diagnose`, `--json`, lenient/dirty refusals, origin check read-only) | done | `TestDiffAccept` (for-review-dirty, coord, lanes; TRACE 5.F5, 6.1, 6.4, 6.6, 9.3), `TestDiffAcceptMergeCommit` | none known |
| `accept` (`--mission`, `--mode`, `--lenient`, `--no-commit`, `--json`, git_dirty and `docs/` path-convention refusals) | done | `TestDiffAccept` | none known |
| `accept --merge-commit` (`--mode pr/local`, `--target-branch`, `--attest-first-landing-commit`; PR-merge evidence verified against git and recorded) | done | `TestDiffAcceptMergeCommit` | uses area H's `internal/migrate` verifier/recorder |
| `accept` origin freshness (`--origin-check enforce/warn/off`, `SPEC_KITTY_ORIGIN_CHECK`; `--no-commit`/`--diagnose` read-only) | done | `TestDiffAcceptMergeCommit` (local bare remote whose status log moved ahead: refusal, warn advisories, off, read-only modes) | — |
| `consolidate` default squash (`--dry-run [--json]`, coord-residue refusal and retry, lane→mission merges, mission_number bake, squash onto the target, done transitions, retrospective capture, worktree/branch/ref teardown, coord flatten, `Next:` hints) | done | `TestDiffConsolidateTrace` (TRACE 7.1–7.4 replay), `TestDiffConsolidate` (coord, lanes; dry-run variants, unknown mission, `--json` without `--dry-run`, primary-dirty refusal, `--skip-review-artifact-check` without `--note`, TRACE 8.1), `TestDiffConsolidateNotReady` | none known |
| `consolidate` reconciliation claim and gate (CONS-01/02: the approved-WP claim built before any mutation — lane-tip commits, first-parent authorship, final authored blobs/deletions, two-lane merge-resolution contributions, approved-content presence, excluded canceled commits; the gate after the landing — squash blob/deletion attribution with the `git merge-tree` second chance, merge/rebase per-SHA reachability + excluded + closed-world content commits, `APPROVED_CONTENT_MISSING`, lane re-check against the validated tips, `Reconciliation FAILED`/`refused (fail-closed)` texts, rollback door; PASS anchor `reconciliation_passed_target_sha` and the verified-landing `--resume`) | done | `TestDiffConsolidateReconcile` (teammate content on the mission branch under squash / merge / rebase → FAIL + rollback + `--abort`; lane moved after approval → claim refusal), `TestDiffConsolidatePushRejectedResume` (push rejected after the verified landing, `--resume` finishes the teardown), `internal/consolidate` unit tests | the squash projected-bookkeeping proof and the post-checkpoint coord projection are vacuous unless another writer commits to the coordination branch during the run (not exercised differentially) |
| `consolidate` approved bound (CONS-08 native: `LANE_MOVED_AFTER_APPROVAL`, `APPROVAL_STAMP_MISSING`, `APPROVAL_STAMP_NOT_ON_LANE`, compound refusal text, claim-time `Reconciliation refused (fail-closed) at claim time` footer incl. the earlier-attempt sentences) | done | `TestDiffConsolidateReconcile/lane-moved*`, `TestDiffConsolidateAttestApproved`, `TestDiffConsolidateRollbackAuthority` (resume refused after an earlier attempt) | — |
| `consolidate --attest-approved-reviewed` / `--attest-canceled-superseded` (CONS-03: validation against the status log, re-attestation check, forced operator transition recorded through the status door before the claim, `⚠️  Operator attestation recorded ...` lines, attestation stamps read by the claim) | done | `TestDiffConsolidateAttestApproved` (unstamped approval refused, then attested and consolidated; invalid requests), `TestDiffConsolidateAutoRebaseManual` (CLI-boundary validation) | — |
| `consolidate` canceled-with-provenance WPs (excluded from the merge-ready precondition, gates and done bookkeeping; fully-canceled lanes skipped; mixed-lane canceled-content attribution with work windows, closed world and attestation lift; fully-canceled dependency lanes: unreadable-branch refusal, unstamped refusal, carried content `CANCELED_REACHABLE_VIA_DEPENDENCY`) | done | `TestDiffConsolidateCanceledLane` (canceled leaf lane; canceled dependency lane carried by lane-b), `internal/consolidate` unit tests (windows, never-started, details, attestation stamps) | mixed lanes (two WPs in one lane) are covered by unit tests only: the calc fixture has one WP per lane |
| `accept --normalize-encoding` / `ArtifactEncodingError` (strict UTF-8 artifact reads; repair through `charter.encoding_recovery.recover` with `.bak` backups; ambiguous refusal; backup collision) | done (area ENC) | `TestDiffAcceptNormalizeEncoding` | detection via `internal/charsetnorm` (maintenance.md, "Charset detection") |
| `consolidate` merge gates (evidence/risk/dependency/issue-matrix completeness + verdict terminality, `policy.merge_gates` mode block/warn/off) | done | `TestDiffConsolidateGates` (blocking risk refusal before any mutation, warn mode proceeds) | — |
| `consolidate` transaction record (`.kittify/runtime/merge/<mission_id>/state.json` + post-fix marker, `completed_wps`, `mission_number_baked`), `--resume`, `--abort` (rollback report, coordination worktree teardown), rollback report on any failure inside the mutation span | done | `TestDiffConsolidateRebase`, `TestDiffConsolidateAutoRebaseManual`, `TestDiffConsolidateSkipLanes`, `TestDiffConsolidateRollbackAuthority`, `TestDiffConsolidatePushRejectedResume` (state.json compared) | — |
| `consolidate` rollback authority (CONS-04/05: pre-mutation snapshot, per-phase post-mutation tips, advance intents, settled/unsettled branches, ADR-A2 restore targets, `UNEXPLAINED_BRANCH_MOVE` pre-check and claim-time refusal, `NOT restored` outcomes with the record kept, `--abort --release-branch/--release-reason` binding the release to the live tip and reporting `kept ... (released by operator: ...)`; dirty-checkout resync refusal with the role-aware residue context; resume anchor guard H3/H4/H5) | done | `TestDiffConsolidateRollbackAuthority` (re-anchor after a settled move, unsettled move refused on `--resume` and on a re-run, `--abort` NOT restored + record kept, release), `TestDiffConsolidateResumeLaneDiverged` (H3) | — |
| `consolidate` sparse-checkout preflight (CONS-06: `core.sparseCheckout` on the primary or a lane worktree refuses unless `--allow-sparse-checkout`; managed lane sparse checkouts exempt) | done | `TestDiffConsolidateAttestApproved` (refusal text with affected paths; override WARNING record on stderr) | — |
| `consolidate` protected status target (CONS-09 native: a coordination-less mission whose recorded target is protected refuses before any branch moves, `PROTECTED_BRANCH_REFUSED: ...` + `Next step:` + footer; dry-run forecast with `error_code`) | done | `TestDiffConsolidateReconcile/protected` | — |
| `consolidate --strategy merge` (no-ff `Merge <mission> into <target>`, meta.json driver two-way) | done | `TestDiffConsolidateStrategies/lanes-merge`, `TestDiffConsolidateReconcile/teammate-merge` | — |
| `consolidate --strategy rebase` (rebase + fast-forward, per-SHA reconciliation, rollback) | done | `TestDiffConsolidateRebase`, `TestDiffConsolidateReconcile/teammate-rebase` | — |
| `consolidate` stale-lane auto-rebase with the conflict classifier (status event-log union, status.json rematerialize, coordination/target-owned take-theirs, pyproject deps / `__init__` imports / URL-list unions, uv.lock regenerate, default manual) and `guarded_merge_abort` (CONS-07) | done | `TestDiffConsolidateAutoRebaseManual` (manual conflict aborts, lane stays stale), `TestDiffConsolidateAutoRebaseGuardedAbort` (an operator edit in the lane worktree keeps the merge in progress); orchestrator `TestDiffConsolidateMissionAutoRebaseClassifier`; classifier and TOML validator against CPython in `internal/autorebase` | — (TOML validation uses PYF's `internal/pytoml` with tomllib's messages) |
| `consolidate --skip-lanes` (no-lane missions: synthesized manifest, planning-artifact closeout) | done | `TestDiffConsolidateSkipLanes` | — |
| `consolidate` review-artifact consistency gate (`REJECTED_REVIEW_ARTIFACT_CONFLICT`: a terminal WP whose event-sourced verdict is `changes_requested` without a complete override blocks the dry-run forecast — human block and `--json` `blocked`/`blockers` — and the real merge before any merge state is written; `--skip-review-artifact-check --note` records committed `ReviewOverride` evidence per WP and merges) | done (FIX5) | `TestDiffConsolidateReviewArtifactGate` (forged rejected verdict on the coordination log: dry-run, `--json`, `review`, refusal, skip; git state compared) | — |
| `consolidate` materialization-drift warning (`_warn_on_materialization_drift`: a present status.json disagreeing with its event log is listed before the run); the coordination worktree's pre-merge cleanliness check judges it as a MISSION checkout (this mission's coordination residue such as a stale status.json is disposable) | done (FIX5) | `TestDiffConsolidateUnreadableStatus/coord-drift`, `/coord-driftdirty` | — |
| unreadable or corrupt status logs (`StoreError` with its chained `JSONDecodeError`, `UnicodeDecodeError`, `IsADirectoryError`, `PermissionError`): dependency-gate skip text (primary log read through `read_wp_frontmatter`), uncaught tracebacks of the dry-run review-artifact preview and of the run's drift check (coordination log), `agent tasks status` `status_failed` | done (FIX5) | `TestDiffConsolidateUnreadableStatus` (10 cases), `TestOracleGitProbeErrorText` (every `GitProbeError` probe against the oracle's own `git_probes` functions) | traceback frames sourceless (see Irreducible differences) |
| `consolidate` origin freshness gate (status evidence + approved lane branches, enforce/warn/off) and `--push` (target sync preflight `TARGET_BRANCH_NOT_SYNCHRONIZED`/`REFRESH_FAILED`, push after the stale check) | done | `TestDiffConsolidatePush` (local bare remote) | — |
| `consolidate` stale-assertion analyzer (`internal/staleassert`, Python AST port) | done | `TestDiffConsolidateTrace`, `TestDiffStaleCheck` (runtime) | — |
| `agent mission merge` (delegates to consolidate; default strategy `merge`; `--auto-retry` relocation via `spec-kitty` on PATH) | done | `TestDiffConsolidateNotReady`, `TestDiffConsolidateStrategies/coord-agent-merge`, `TestDiffAgentMergeAutoRetry` | — |
| `review` (merged-mission validation: WP lane gate incl. the review-artifact consistency finding, dead-code scan, `mission-review-report.md`; `--mode lightweight|post-merge`) | done | `TestDiffConsolidate` (TRACE 8.3), `TestDiffConsolidateTrace`, `TestDiffConsolidateNotReady`, `TestDiffConsolidateReviewArtifactGate` | dead-code scan is a port of the upstream Python-symbol heuristic |
| `retrospect create` (`--overwrite/--update`, `--json`) | done | `TestDiffConsolidate`, `TestDiffConsolidateNotReady` | none known |
| `retrospect summary` (`--json`, `--json-out`, `--limit`, `--since`, `--filter`, `--include-malformed`) | done | `TestDiffConsolidate` | none known |
| `retrospect backfill` (`--dry-run`, `--json`, `--since/--until/--mission`, `--emit-skipped`, `--emit-failures`; `.kittify/missions/` registry, pre-screened skips before `already_exists`, `missing_artifacts` / `generator_exception` failures, `RetrospectiveSkipped` / `RetrospectiveCaptureFailed` events, authored record + commit) | done | `TestDiffConsolidate` (`--dry-run --json`), `TestDiffRetrospectBackfill` (CONS-10: dry-run, writing path, emit flags, human panel, filter, bad date) | — |
| `agent retrospect summary` | done | `TestDiffConsolidate` | none known |
| `agent retrospect synthesize` (`--mission`, `--proposal-id`, `--apply`, `--json`) | partial | `TestDiffConsolidate`, `TestDiffConsolidateNotReady` | generator-schema records (what consolidate and `retrospect create` write) match upstream: empty dry-run/apply batch, `--proposal-id` refused with `unsupported_generator_record`. Legacy-schema records with applicable proposals: owned by DOC-19 (wave 5, `retrospect synthesize --apply`) |
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
- Transaction record and rollback authority: `MergeState` (`Object`, `Save`,
  `Rollback(root) *RollbackReport`), `RollbackReport` (`Render`), `LoadState`,
  `ClearState`, `Abort(w, root, handle, releaseBranches, releaseReason)`,
  `UnexplainedBranchMove`.
- Reconciliation (wave 5, `reconcile.go`, `reconcile_canceled.go`,
  `attribution.go`, `bound.go`, `probes.go`, `projection.go`): the claim
  builder and verifier are package-internal (`buildClaim`, `claim.verify`);
  exported: `ApprovedBoundRefusal(root, statusDir, manifest, coordBase,
  windowBase, excluded) (string, error)` (approved_bound_refusal),
  `BoundRefusal`, `RenderBoundRefusals(refusals, slug)`,
  `BoundRefusalCodes(text)`, codes `LaneMovedAfterApproval`,
  `ApprovalStampMissing`, `ApprovalStampNotOnLane`, `AttestApprovedFlag`,
  `ProbeError` (GitProbeError).
- Entry checks: `SparseCheckoutRefusal(root, command) string`,
  `ProtectedStatusTargetRefusal(root, slug, manifest, excluded)
  *PolicyRefusal` (`excluded` nil = acceptably canceled WPs), `PolicyRefusal`.
- `Options` gained `AllowSparse`, `AttestCanceled`, `AttestApproved`,
  `AttestReason`; FIX5: `SkipReviewArtifact`, `Note`
  (`--skip-review-artifact-check` / `--note`).
- Review-artifact consistency gate (FIX5, `review_artifacts.go`):
  `ReviewArtifactFindings(statusDir, wpIDs) ([]ReviewArtifactFinding, error)`
  (find_rejected_review_artifact_conflicts over materialize_snapshot; read
  errors propagate), `ReviewArtifactFinding{WPID, Lane, Verdict}` with
  `Format()` and `Diagnostic()`. Uncaught-exception rendering of status-log
  failures and the drift warning live in `pyexc.go` (package-internal).
- Orchestrator lane merge (area I, extended): `MergePreflight`,
  `RunLaneMerge` (evaluates the merge gates, the protected status target and
  the approved bound), `LaneMergeError`, `ApprovedBoundError{Msg, Codes}`,
  `DestructiveRefusal`, `OriginGateInputs(root, slug)`.

`internal/autorebase` (new): `Attempt(Options) Report` (attempt_auto_rebase),
`Options{Root, LaneID, Branch, MissionBranch, Worktree, Env, EnsureDrivers,
Disposable}` (`Disposable` nil = the lane residue context of the branch's
mission), `Report`, `Classify(file, hunk)`, `ValidateResolution`,
`ResolveText(file, body)`, `Classification`/`Resolution`, rule ids `Rule*`,
`ValidateTOML(src)` (TOML 1.0 validity, checked against `tomllib`),
`IsTargetOwnedPath`, `EnsureDriverConfig(root)`, `MergeEnv()`;
`AbortRefused` (guarded_merge_abort's DESTRUCTIVE_OP_ONLY_COPY refusal),
`LaneResidue(root, slug)`.

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
  `Width`; wave 5: `NotFoundError` (generate's FileNotFoundError),
  `EmitSkipped(dir, missionID, slug, reason)`, `EmitCaptureFailed(dir,
  missionID, slug, category, message, remediation, attemptedKind, missing)`.

## Foundation changes / extensions to other areas' packages

FIX5: `internal/status` event-log reads word failures like CPython
(`StoreError{Msg, Cause}`, `*pyjson.OSError`, `*pyjson.UnicodeError`, JSON
via `pyjson.Loads`); `coord.Surface.EmitAnnotationAt` (explicit event
timestamp); `pyjson.AsOSError` / `OSErrorString` / `Strip` / `Splitlines` /
`TranslateNewlines` / `FileLines` (see util.md).

None to area A. Wave 4 (area L) uses area H's `internal/migrate`
(`VerifyPrMergeEvidence`, `RecordPrMergeBaseline`) for `accept
--merge-commit`, and wires the merge gates and origin gate into area I's
`orchestrator-api consolidate-mission` / `accept-mission` (see
`docs/parity/orchestrator.md`).

Wave 5 (area CONS) adds no foundation change. It reads other areas'
packages without changing them: `internal/healthcheck.ScanSparse` (sparse
checkout scan), `internal/coord.IsProtected` / `Surface.EmitTransition`
(protected-branch policy, attestation transitions), `internal/mergedriver.Bodies`
(driver replay), `internal/status` event reading. `internal/autorebase` now
applies the lane residue context by default, which also changes CORE's lane
sync (`internal/workflow/lanesync.go`) the way upstream's
`worktree_allocator` does: a failed sync auto-rebase whose lane worktree
holds an operator edit leaves the merge in progress and says so.

Wave 5 (area FIX2, test harness only): the closeout git-state and bare-remote
comparisons (`gitState`, `remoteLog`) render history with
`testkit.Sandbox.GitHistory` (refs in name order, commits depth-first from
each tip) instead of `git log --all --topo-order`, which interleaved
independent branches by commit date and flaked `TestDiffConsolidatePush`
when the two sandboxes' commits fell into different seconds;
`testkit.Normalize` now masks all-letter (a-f) short SHAs, which flaked
`TestDiffConsolidateRollbackAuthority` about once per thousand abbreviations
(the closeout-local rollback-line mask is gone).

## Irreducible differences

- Excluded (canceled) commits in a merge/rebase FAIL (`excluded
  (canceled/removed) commit ... IS reachable` clauses naming several
  commits) are listed in sorted order. Upstream iterates
  `ApprovedWpCommitSet.excluded_shas`, a `frozenset[str]` of SHAs: string
  hashing is randomised per process (`PYTHONHASHSEED`), so upstream's own
  order changes from run to run and has no fixed value to match.
- Uncaught tracebacks (unreadable coordination log in the dry-run preview or
  the drift check) render the exact exception lines and chaining, but the
  frames inside the panel are sourceless: upstream's show site-packages
  paths and Python source excerpts.

(FIX5: the former "refusal details quote the Go error text" entry is fixed —
status-log failures now carry CPython's `StoreError` / `JSONDecodeError` /
`UnicodeDecodeError` / `OSError` text and `GitProbeError` probes run git with
upstream's argv; see the rows above.)

## Remaining work

None.

## Wave 5 (area COLOR)

Output on the shared engine with upstream markup (see help.md "Colour").
Fixes from the colour-mode suites: `accept`, `review` and `consolidate`
report "Unable to locate repository root (missing .git or .kittify)." outside
a project; consolidation's Resume / would-assign / Assigned lines carry their
markup and rollback reports print through the console (markup off,
highlighting on for the failure path and off for `--abort`); `archive ...
--json` prints through `console.print` (highlighted, wrapped);
`retrospect backfill` runs its loop under the transient spinner
(`rich.SpinnerProgress`: drawn on a terminal, a blank line otherwise);
`agent retrospect synthesize` refusals are `[red]Error:[/red]` soft-wrapped.
Bare `agent mission`, `agent release` and `agent retrospect` print their help
and exit 2 (typer `no_args_is_help`).
