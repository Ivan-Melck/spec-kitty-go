# Parity: standalone utility commands (area D0, wave 5 PYF)

Go implementation of the upstream `spec-kitty` 4.0.0rc8 utility commands.
Code: `internal/mergedriver` (merge-driver library), `internal/commands/util/*`
(commands) plus the shared Python-fidelity packages `internal/rich`,
`internal/pyyaml`, `internal/yamlx`, `internal/pytoml`, `internal/rtyaml` and
`internal/pyjson`. Registered through `cmd/spec-kitty/imports_util.go`.

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
| `merge` (hidden deprecated stub) | done | `drivers.TestMergeStubParity` (incl. COLUMNS=60/33 wrapping), `drivers.TestMergeStubColorParity` (FORCE_COLOR / TTY_COMPATIBLE; standard, 256 and truecolor systems; NO_COLOR; dumb TERM) | none |
| `validate-encoding` (`--mission/--fix/--all/--backup/--no-backup`) | done | `encoding.TestValidateEncodingParity`, unit tests | mission handle resolution is a local approximation of `placement_seam` (literal dir, mission_id/mid8, numeric prefix, bare slug); OWN area owns the seam |
| `commit-guard-hook` (hidden) | done | `commitguard.TestCommitGuardParity`, `TestCommitGuardE2E` | none known (PYF-11: the active WP's STATUS is read through the coordination resolver) |
| `lint` (`[file_path]`, `--json`, `--fix`, stdin hook payload) | done | `lint.TestLintParity`, `TestLintStdinPayload` | ruff/mypy are external tools: output depends on their presence/version, as upstream |
| `cutover-guard` (`--base-ref`, `--paths-from`, `--json`) | done | `cutover.TestCutoverGuardParity`, `TestCutoverGuardBaseRefParity`, `TestCutoverGuardNoProject` | traceback frames (see Irreducible differences) |
| `validate-tasks` (LEGACY; `--mission/--fix/--all/--agent/--shell-pid`) | done | `validatetasks.TestValidateTasksParity` (incl. `.worktrees`, `--fix` with the lock files compared, narrow console), `TestValidateTasksParityEdgeCases`, `TestValidateTasksE2E` | none known (PYF-07: `--fix` holds the Mission write lock with the coordination-aware key; PYF-08: Python 3.13 `rglob` directory order; PyYAML error text exact) |
| `events tail` (`--mission/--json/--once/--max-events/--from-offset/--from-invariant`) | done | `events.TestTailParity`, `TestTailParityResume`, `TestPollAndResume`, `TestTailE2E` | traceback frames |
| `workflow list` | done | `workflow.TestWorkflowParity`, `TestWorkflowE2E` | none known |
| `workflow export {workflow_id} {output}` (`--project-root`, `--force`) | done | same | built-in workflow paths (see Irreducible differences) |
| `workflow import {source}` (`--project-root`, `--force`) | done | same, plus `TestValidateMatchesPydantic` | none known (pydantic `ValidationError` text and PyYAML error text exact) |
| `docs query {term}` (`--json`, `--divio-type`, `--section`) | done | `docs.TestDocsQueryParity`, `TestDocsQueryE2E` | none known (ruamel error text exact; malformed index: rich traceback) |
| `plugin build` (`--target claude-code|codex`, `--output-dir`, `--skip-validate`) | done | `plugin.TestPluginBuildParity` (fresh build, no-op rebuild, stub `claude` validate pass/fail, preserved local edits, user hooks, bad hooks, bad ledger; file modes compared), `TestPluginBuildProjectProfiles`, `TestPluginBuildE2E` | none known (PYF-10: project-layer profiles and `ArtifactLayerCollisionWarning` lines projected; warning path, see Irreducible differences) |
| `plugin`, `docs`, `workflow`, `events` (bare groups) | done | `plugin.TestUtilGroupsBareParity` | none (help byte-identical: `go test ./internal/helpparity`) |

## Irreducible differences

- Tracebacks: every uncaught exception prints rich's framed traceback
  (`rich.PrintTraceback`: panel geometry at the typer stderr console width,
  centred title, "The above exception was the direct cause ..." /
  "During handling ..." separators, `[NOTE]` lines, exception lines wrapped
  like rich). Frames inside the panel are rendered sourceless
  (`in tail_command:159`): upstream's show wheel site-packages paths and
  source excerpts. Tests mask the panel body (`testkit.MaskTracebackFrames`).
- `workflow export`: built-in workflows are embedded, so their paths print as
  `<spec-kitty>/charter/offering/workflows/...` instead of the wheel's
  site-packages path (only in the "Unknown workflow_id ... Searched:"
  message); `plugin build` collision warnings name
  `<spec-kitty>/charter/offering/agent_profiles/repository.py:558` likewise.
- YAML `ReaderError` in files over 8 KiB: PyYAML reports an undecodable /
  non-printable character when its 8 KiB buffer reaches it, after any earlier
  syntax error; the port checks the whole input first.
- Recursive YAML aliases (`&a [*a]`) build cyclic Python objects upstream;
  the jsonx value space cannot hold cycles.
- Coloured output: rich's OSC 8 hyperlink ids are random per process, so
  `[link=...]` markup is not reproducible byte for byte. Traceback frames
  (above) keep upstream's pygments-styled source excerpts out of reach.
- Colour (PYF-14, area COLOR): every renderer goes through the segment
  engine in `internal/rich` (see help.md "Colour" and the engine API there);
  the `internal/colorparity` harness and `SPEC_KITTY_TEST_COLOR=standard|256|
  truecolor` (reruns every differential suite with colour on) verify it.

## Remaining work

- Round-trip config writers (PYF-04): the tracker, `internal/kittify`
  (agent config add/remove/sync, init vcs/agents) and
  `migrate rewrite-opposed-by` use `internal/rtyaml`. Other areas'
  config.yaml writers (charter activation and the `charter:` pointer,
  `ProvisionMissionTypes`, upgrade migrations) still emit via `yamlx.Emit`
  and drop comments; each switch is `rtyaml.Load` + `Doc.Apply(cfg,
  rtyaml.Snap(cfg_before_edit))` + `Doc.Dump`.
- `internal/dossier` and `internal/commands/planning/flowfm.go` still use
  gopkg.in/yaml.v3 for node-style inspection (no error text involved).

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

Shared packages:

- `internal/rich`: markup (`Render`, `Strip`, `MarkupError`), wrapping,
  `Print`/`Printf` (coloured on a colour console), tables, panels and log
  lines (see help.md); terminal (`IsTerminal(fd)`, `Width`, `StderrWidth`,
  `TyperWidth(stderr)`); tracebacks (`Frame{Name,Line}`,
  `Exception{Type,Value,Frames,Notes,Cause,Context}` + `Error()`,
  `ShortTypeName`, `ParseException(line, frames...)`,
  `RenderTraceback(exc, width)`, `PrintTraceback(w, exc)`,
  `Traceback(w, typ, value, frames...)`); colour (`ColorSystem` with
  `NoColor`/`Standard`/`EightBit`/`TrueColor`, `DetectColorSystem(fd)`,
  `ParseColor`/`Color`, `ParseStyle`/`Style` (`Add`, `Render`),
  `NullStyle`; `colordata.go` generated from rich by `testdata/richgen.py`).
- `internal/pyyaml`: PyYAML 6 and ruamel.yaml 0.19 (`rt` and `safe`)
  scanner, parser, composer and constructors with exact error text:
  `Load`, `LoadInput`, `SafeLoad`, `LoadRuamel`, `LoadRuamelInput`,
  `LoadRuamelPy`, `Compose`, `ComposeInput`, `ComposeDocument`
  (+`DocInfo{Version, Tags}`), `ComposeOptions`, `Dialect` (`PyYAML`,
  `Ruamel`, `RuamelSafe`), `Input` (`StringInput`, `FileInput`,
  `TextFileInput`), `Node`/`NodeKind`/tag constants, `MarkedYAMLError`,
  `ReaderError`, `PyError`, `ClassName`, `ResolveRuamel`, `ResolvePlain`,
  `PyInt`, `PyFloat`, `DecodeBase64`; round-trip comment plumbing
  (`CommentToken`, `PyList`, `NewList`, `Truthy`, `CommentAt`, `ListAt`);
  `Dump` (`yaml.dump` emitter). Corpus tests vs PyYAML/ruamel
  (`testdata/ygen.py`).
- `internal/yamlx`: `Parse`, `ParseWith(src, ParseOptions{Safe, File,
  TextFile})`, `ParseObject` (ruamel loader, jsonx value space), `Emit`,
  `EmitWith`, `FormatScalar`.
- `internal/pytoml` (new): CPython 3.13 `tomllib.loads`: `Loads(s)
  (*jsonx.Object, error)`, `DecodeError{Msg}` (+`Repr`), `Date`, `Time`,
  `DateTime` (`ISO()`). Corpus test (`testdata/tgen.py`).
- `internal/rtyaml` (new, PYF-04): ruamel `YAML()` + `preserve_quotes`
  round trip. `Load(in)` / `LoadString(src)` -> `*Doc{Root, Info}`;
  `Doc.Dump`, `Doc.DumpWidth`, `Dump`, `DumpWidth` (RoundTripRepresenter,
  Serializer and RoundTripEmitter: comments at their columns, quote / block
  / flow styles, anchors and merges, int and float formats, `%YAML` / `%TAG`
  directives, YAML 1.1 documents). Model: `Map` (`NewMap`, `Len`, `Keys`,
  `Get`, `Has`, `Set` with CommentedMap's style keeping, `Delete`), `Seq`,
  `Str`, `Int`, `Float`, `Bool`, `Date`, `TimeStamp`, `Tagged`, `Anchor`,
  `Tag`, `CommentAttr`, `Merge`. Editing through a jsonx view: `Snap(v)`,
  `ContentSnapshot(before)`, `Doc.Apply(after, snap)`, `PlainEqual`.
  Verified against ruamel on a 17.5k-case corpus (`testdata/rtgen.py`:
  load/dump, assign a tracker block, delete a key) and `merge_test.go`.
- `internal/pyjson`: CPython `json` decoding, `repr`/`str` semantics.

## Foundation changes

- PYF-01/02/03/04 call-site sweep in other areas' files:
  `internal/commands/maintenance` `uncaught` and
  `orchestrator-api design-context` print rich tracebacks;
  `internal/healthcheck` parses pyproject.toml with `pytoml`;
  `internal/orchestrator.ParseWpsManifest` loads `wps.yaml` with the ruamel
  safe loader; doctor provenance names YAML errors with `pyyaml.ClassName`;
  the `internal/charter` profile loader parses with `yamlx.ParseWith{Safe,
  File}`; `internal/autorebase` validates TOML with `pytoml`;
  `internal/migrate` rewrite-opposed-by and the `internal/kittify` config
  writers use `internal/rtyaml`; `internal/yamlx` is backed by
  `internal/pyyaml` (so all 60+ `yamlx.Parse` callers report ruamel's exact
  error text).
- Additive: `internal/charter` `AgentProfileSet` / `LoadAgentProfileSet`,
  `internal/agents.ProfileFromObject` (PYF-10); `internal/cli.Width`
  delegates to `rich.TyperWidth`.
- `internal/cli`: `Arg.ErrorHint` (optional) names an argument in
  "Missing argument" errors when the usage line shows a `{name}` metavar.
- `internal/rich.Table` honours the console width (columns collapse and
  cells wrap/ellipsize like rich); unchanged output when the table fits.
