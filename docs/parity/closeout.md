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
| `agent mission accept` (`--diagnose`, `--json`, lenient/dirty refusals) | done | `TestDiffAccept` (for-review-dirty, coord, lanes; TRACE 5.F5, 6.1, 6.4, 6.6, 9.3) | none known |
| `accept` (`--mission`, `--mode`, `--lenient`, `--no-commit`, `--json`, git_dirty and `docs/` path-convention refusals) | done | `TestDiffAccept` | `--merge-commit` (PR-merge recording) is refused ("not supported by this build") |
| `consolidate` default squash (`--dry-run [--json]`, coord-residue refusal and retry, lane→mission merges, mission_number bake, squash onto the target, done transitions, retrospective capture, worktree/branch/ref teardown, coord flatten, `Next:` hints) | done | `TestDiffConsolidateTrace` (TRACE 7.1–7.4 replay after the TRACE phase-5 rejection history: dry-run, `MERGE_UNSAFE_WORKTREE_DIRTY` refusal on reviewer feedback drafts, oracle `safe-commit` of the residue inside the coord worktree, retried consolidate, re-run after teardown), `TestDiffConsolidate` (coord, lanes; dry-run variants, unknown mission, `--json` without `--dry-run`, primary-dirty refusal, `--skip-review-artifact-check` without `--note`, TRACE 8.1), `TestDiffConsolidateNotReady` | the merge gates (evidence/risk/dependency/issue-matrix) print the passing lines for a merge-ready mission instead of evaluating risk scores and issue matrices; origin freshness check, `--push`, `--abort`/`--resume` (refused: "not supported by this build"), `--attest-*`, `--release-*`, `--skip-lanes`, sparse-checkout detection and the stale-assertion analyzer (always "No likely-stale assertions detected.") are not ported |
| `consolidate --strategy merge` (no-ff `Merge <mission> into <target>`, meta.json driver two-way) | done | `TestDiffConsolidateStrategies/lanes-merge` (full lane history visible on the target, including the stale-lane auto-rebase merges) | same as above |
| `consolidate --strategy rebase` | partial | none | the rebase + fast-forward is implemented, but upstream's per-SHA reconciliation (`Reconciliation FAILED: approved WPnn commit ... is NOT reachable from the target` + branch rollback report) is not ported: when the rebase rewrites approved lane commits (target moved since the lanes forked) upstream refuses and rolls back, Go lands the rebased history |
| `agent mission merge` (delegates to consolidate; default strategy `merge`) | done | `TestDiffConsolidateNotReady` (dry-run, handle resolution), `TestDiffConsolidateStrategies/coord-agent-merge` | `--auto-retry` is accepted but does not relocate into a worktree |
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
else `Merge <lane> into <mission>`). A conflicted auto-rebase is aborted and
reported as a stale lane (upstream's conflict classifier is not ported). Then
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
  failures return `*Exit{Code}` after printing), `Options`,
  `ResolveMissionSlug(root, handle)`, `ResolveRetention(primaryDir, del, rm)`
  / `Retention` (meta.json `retain_branches`/`retain_worktrees` policy),
  `NextMissionNumber(root)`.

`internal/retrospective`:
- `Generate(root, slug, invokedAt) (*Record, error)`, `Author(root, slug, kind,
  command)`, `Capture(root, slug)` (terminus capture: writes
  `retrospective.yaml` and appends `RetrospectiveCaptured` to the status log),
  `Record`, `Finding`, `Serialize`, `LoadRecord`, `ClassifyRecord`,
  `BuildSummary(project, since, limit)`, `MissionDirs`, `MissionStates`,
  `DefaultPolicySource`, `PolicyKeys`, `Now`, `FileName`, `GeneratorVersion`,
  `Width`.

## Foundation changes / extensions to area A packages

None. Area G only adds its own packages and `cmd/spec-kitty/imports_closeout.go`.
