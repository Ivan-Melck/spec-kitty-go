# Parity: standalone utility commands (area D0)

Go implementation of the upstream `spec-kitty` 4.0.0rc8 utility commands.
Code: `internal/mergedriver` (merge-driver library), `internal/commands/util/*`
(commands plus the shared packages `internal/rich` (formerly `uconsole`), `internal/pyyaml`), `internal/pyjson`.
Registered through `cmd/spec-kitty/imports_util.go`.

Differential tests run both binaries in identical sandboxes
(`SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty go test ./internal/commands/util/...`)
and compare exit code, normalized stdout/stderr and the resulting file trees
(contents, and permission bits where the command sets them).

| Command path | Status | Tests | Deviations |
|---|---|---|---|
| `merge-driver-event-log` (hidden) | done | `drivers.TestDriversParity`, `TestDriversGo` | none known |
| `merge-driver-meta` (hidden, incl. `SPEC_KITTY_META_MERGE_TWO_WAY`) | done | same (49 cases over all 7 drivers: clean union, conflicts, malformed input) | none known |
| `merge-driver-traces` (hidden) | done | same | none known |
| `merge-driver-acceptance-matrix` (hidden) | done | same | none known |
| `merge-driver-issue-matrix` (hidden) | done | same | none known |
| `merge-driver-review-cycle` (hidden) | done | same | none known |
| `merge-driver-decision-index` (hidden) | done | same | none known |
| `merge` (hidden deprecated stub) | done | `drivers.TestMergeStubParity` | none |
| `validate-encoding` (`--mission/--fix/--all/--backup/--no-backup`) | done | `encoding.TestValidateEncodingParity`, unit tests | mission handle resolution is a local approximation of `placement_seam` (literal dir, mission_id/mid8, numeric prefix, bare slug) |
| `commit-guard-hook` (hidden) | done | `commitguard.TestCommitGuardParity`, `TestCommitGuardE2E` | reads ownership/context files directly; lanes via `status.Reduce` |
| `lint` (`[file_path]`, `--json`, `--fix`, stdin hook payload) | done | `lint.TestLintParity`, `TestLintStdinPayload` | ruff/mypy are external tools: output depends on their presence/version, as upstream |
| `cutover-guard` (`--base-ref`, `--paths-from`, `--json`) | done | `cutover.TestCutoverGuardParity`, `TestCutoverGuardBaseRefParity`, `TestCutoverGuardNoProject` | none known |
| `validate-tasks` (LEGACY; `--mission/--fix/--all/--agent/--shell-pid`) | done | `validatetasks.TestValidateTasksParity` (incl. `.worktrees`, `--fix`, narrow console), `TestValidateTasksParityEdgeCases`, `TestValidateTasksE2E` | no mission write lock is taken around the repair (the lock file lives under the git common dir); PyYAML parser error *details* in the "YAML parse error" warning differ (prefix identical); `rglob` order is sorted instead of directory order; tracebacks are reduced to `Traceback` + final exception line |
| `events tail` (`--mission/--json/--once/--max-events/--from-offset/--from-invariant`) | done | `events.TestTailParity`, `TestTailParityResume`, `TestPollAndResume`, `TestTailE2E` | tracebacks reduced to exit code + final exception line |
| `workflow list` | done | `workflow.TestWorkflowParity`, `TestWorkflowE2E` | none known |
| `workflow export {workflow_id} {output}` (`--project-root`, `--force`) | done | same | built-in workflows are embedded: their paths print as `<spec-kitty>/charter/offering/workflows/...` instead of the wheel's site-packages path (only visible in the "Unknown workflow_id ... Searched:" message) |
| `workflow import {source}` (`--project-root`, `--force`) | done | same, plus `TestValidateMatchesPydantic` | pydantic `ValidationError` text reproduced for the WorkflowSequence/ActionStep schema (types, missing/extra fields, validators, input truncation); PyYAML *syntax* error text differs (prefix `workflow file '<path>' is not valid: ` identical) |
| `docs query {term}` (`--json`, `--divio-type`, `--section`) | done | `docs.TestDocsQueryParity`, `TestDocsQueryE2E` | ruamel YAML syntax error text differs (prefix identical) |
| `plugin build` (`--target claude-code|codex`, `--output-dir`, `--skip-validate`) | done | `plugin.TestPluginBuildParity` (fresh build, no-op rebuild, stub `claude` validate pass/fail, preserved local edits, user hooks, bad hooks, bad ledger; file modes compared), `TestPluginBuildE2E` | agent profiles are the built-in set only (upstream also projects project-layer profiles found under the staging root); BuildError and other uncaught exceptions print `Traceback` + final exception line |
| `plugin`, `docs`, `workflow`, `events` (bare groups) | done | `plugin.TestUtilGroupsBareParity` | help text is best-effort (Arguments panel type column shows `TEXT`) |

## Library API

`internal/mergedriver` exposes every driver for in-process use (consolidate):
`Names`, `Bodies`, `CLIBody`, `Outcome`, `Body`, `Error`, `Crash`, and per driver
`Run*` (file paths in, result written to ours) plus the pure reconcilers
`MergeEventLogTexts`/`MergeEventLogFiles`/`MergeEventPayloads`/`SerializeEvents`/
`ReadEventLogText`/`DetectConcurrentFork`, `ReconcileMetaPayloads`/`MetaJSON`/
`RunMeta`/`RunMetaFromEnv` (`MetaTwoWayEnv`), `MergeTraceTexts`/`UnionTraceTexts`,
`ReconcileAcceptanceMatrixDocuments`, `ReconcileIssueMatrixDocuments`,
`ReconcileReviewCycleTexts`, `UnionDecisionIndex`/`ReconcileDecisionIndexDocuments`/
`ValidateDecisionIndex`.

Shared packages: `internal/rich` (formerly `uconsole`) (rich-compatible markup stripping, wrapping,
tables with rich column collapsing/ellipsis, panels, RichHandler-style log
lines), `pyyaml` (PyYAML `safe_load` YAML 1.1 typing and `yaml.dump` emitter),
`pyjson` (CPython `json` decoding, `repr`/`str` semantics).

## Foundation changes

- `internal/cli`: `Arg.ErrorHint` (optional) names an argument in
  "Missing argument" errors when the usage line shows a `{name}` metavar.
- `internal/rich.Table` now honours the console width
  (columns collapse and cells wrap/ellipsize like rich); unchanged output when
  the table fits.
