# Area M: help renderer parity and shared code

Scope: `--help` output for every command path, bare groups, `-h`, the root
help (rich and plain "simple" renderer, banner), usage errors (Error panel
and the "Usage:" / "Try ... --help" lines), the hidden
`__force_multi_command_mode__` command, `testkit.Normalize`, and the
consolidation of duplicated rich table / cell-width code into
`internal/rich`.

Upstream renders help with typer 0.27's `rich_utils` (a fork of rich-click)
on its vendored click 8.5, with rich 15. `internal/rich/layout.go` is a
plain-text port of the parts of rich's layout engine involved (Text
wrapping/`divide_line`, Padding, Align, Group, Columns, box-less Table with
`_calculate_column_widths` / `_collapse_widths` / ratio distribution, Panel),
and `internal/cli/help.go` composes them exactly like
`rich_format_help`. Without a terminal the output is unstyled, so it matches
byte for byte, including the trailing spaces rich pads every line with.

## Tests

| Test | What it proves |
|---|---|
| `internal/helpparity` `TestHelpInventoryParity` | `--help` of every inventoried path vs `$REF/inventory/help/*.txt` (COLUMNS=160), byte for byte. Skips when the inventory is absent. Current: 231 match, 0 differ, 4 groups only list subcommands other areas have not ported, 41 paths not implemented. |
| `internal/helpparity` `TestHelpWidthsOracle` (oracle) | every implemented path at COLUMNS=60/80/100, `SPEC_KITTY_SIMPLE_HELP=1` at 160 and `=0` at 60, vs the oracle. All pass. Also surveyed by hand at 40 and 211. |
| `internal/testkit` `TestNormalizeShortSHAs` | short-SHA masking incl. all-digit abbreviations. |
| existing oracle suites of every package | usage-error and help changes, the rich consolidation (setup/agentcore tables, wrapping) and the Normalize change keep them green. |

Usage errors were checked against the oracle with a 694-probe survey (every
path with an unknown option, a near-miss option, missing required
parameters, and an unknown subcommand for groups): identical except for
paths other areas have not ported and one non-help console wrap in the
`merge` stub (see open issues).

## Behaviour implemented (click 8.5 / typer 0.27 semantics)

- Help layout: usage line wrapped by click's formatter (width
  `max(min(cols, 80) - 2, 50)`), help text (`inspect.cleandoc`, first
  paragraph joined, later paragraphs keep line breaks, rich markup tags
  stripped), Arguments / Options / custom `rich_help_panel` panels / Commands
  panel; type column `<str>`, `<int>`, `<float>`, `<a|b>` choices,
  `<int range> [lo<=x<=hi]`; `[default: ...]` (typer's `_get_default_string`)
  and `[required]` laid out with rich `Columns` (they drop to their own lines
  when the help does not fit); deprecated commands; root subcommands sorted,
  other groups in upstream registration order; trailing blank line from
  click's help echo.
- Root plain help (`SPEC_KITTY_SIMPLE_HELP` truthy, or width < 100): click
  `HelpFormatter` output with `make_default_short_help(45)`.
- Root banner before the rich root help when the first non-option token is
  `init` and no agent-harness marker / `SPEC_KITTY_NO_BANNER` is set.
- Parsing: parse errors (unknown option, missing option value, value given to
  a flag) are raised before eager `--help`; `BadOptionUsage` errors print only
  the Error panel; short-option clusters (`-hx`); conversion errors in
  command-line order, then missing parameters (arguments first), then
  `Got unexpected extra argument(s) (...)`; difflib suggestions for unknown
  long options (`(Possible options: ...)`, hidden/negative/`--help` included)
  and subcommands (`Did you mean 'a', 'b'?`, hidden commands included);
  messages `'x' is not a valid int.` / `int range` / `float`,
  `'x' is not one of 'a', 'b'.`, `N is not in the range lo<=x<=hi.`,
  `Missing option '--a' / '-b'. Choose from:\n\t...`; unknown subcommands are
  resolved before the group callback runs; `upgrade` keeps upstream's quirks
  (no `-h`, parse errors under a context named `upgrade`).

## Command status (`--help`, COLUMNS=160)

| Path | Status |
|---|---|
| `spec-kitty` | partial (lists unported subcommands) |
| `spec-kitty __force_multi_command_mode__` | done |
| `spec-kitty accept` | done |
| `spec-kitty agent` | done |
| `spec-kitty agent acceptance-verdict` | done |
| `spec-kitty agent action` | done |
| `spec-kitty agent action implement` | done |
| `spec-kitty agent action review` | done |
| `spec-kitty agent check-prerequisites` | done |
| `spec-kitty agent config` | done |
| `spec-kitty agent config add` | done |
| `spec-kitty agent config list` | done |
| `spec-kitty agent config remove` | done |
| `spec-kitty agent config set` | done |
| `spec-kitty agent config status` | done |
| `spec-kitty agent config sync` | done |
| `spec-kitty agent context` | done |
| `spec-kitty agent context resolve` | done |
| `spec-kitty agent decision` | done |
| `spec-kitty agent decision cancel` | done |
| `spec-kitty agent decision defer` | done |
| `spec-kitty agent decision list` | done |
| `spec-kitty agent decision open` | done |
| `spec-kitty agent decision repair-runtime-lock` | done |
| `spec-kitty agent decision resolve` | done |
| `spec-kitty agent decision verify` | done |
| `spec-kitty agent issue-verdict` | done |
| `spec-kitty agent mission` | done |
| `spec-kitty agent mission accept` | done |
| `spec-kitty agent mission acceptance-verdict` | done |
| `spec-kitty agent mission branch-context` | done |
| `spec-kitty agent mission check-prerequisites` | done |
| `spec-kitty agent mission create` | done |
| `spec-kitty agent mission finalize-tasks` | done |
| `spec-kitty agent mission merge` | done |
| `spec-kitty agent mission record-analysis` | done |
| `spec-kitty agent mission repair` | done |
| `spec-kitty agent mission setup-plan` | done |
| `spec-kitty agent profile` | done |
| `spec-kitty agent profile get` | done |
| `spec-kitty agent profile list` | done |
| `spec-kitty agent profile show` | done |
| `spec-kitty agent release` | done |
| `spec-kitty agent release prep` | done |
| `spec-kitty agent retrospect` | done |
| `spec-kitty agent retrospect summary` | done |
| `spec-kitty agent retrospect synthesize` | done |
| `spec-kitty agent status` | done |
| `spec-kitty agent status doctor` | done |
| `spec-kitty agent status emit` | done |
| `spec-kitty agent status lifecycle` | done |
| `spec-kitty agent status materialize` | done |
| `spec-kitty agent status migrate` | done |
| `spec-kitty agent status reconcile` | done |
| `spec-kitty agent status validate` | done |
| `spec-kitty agent tasks` | done |
| `spec-kitty agent tasks add-history` | done |
| `spec-kitty agent tasks check-terminability` | done |
| `spec-kitty agent tasks finalize-tasks` | done |
| `spec-kitty agent tasks list-dependents` | done |
| `spec-kitty agent tasks list-tasks` | done |
| `spec-kitty agent tasks map-requirements` | done |
| `spec-kitty agent tasks mark-status` | done |
| `spec-kitty agent tasks move-task` | done |
| `spec-kitty agent tasks status` | done |
| `spec-kitty agent tasks validate-workflow` | done |
| `spec-kitty agent tests` | done |
| `spec-kitty agent tests stale-check` | done |
| `spec-kitty agent tracer-append` | done |
| `spec-kitty archive` | done |
| `spec-kitty archive create` | done |
| `spec-kitty archive list` | done |
| `spec-kitty charter` | partial (lists unported subcommands) |
| `spec-kitty charter activate` | done |
| `spec-kitty charter bundle` | done |
| `spec-kitty charter bundle validate` | done |
| `spec-kitty charter consistency-check` | missing (not ported) |
| `spec-kitty charter context` | done |
| `spec-kitty charter deactivate` | done |
| `spec-kitty charter fetch` | missing (not ported) |
| `spec-kitty charter generate` | done |
| `spec-kitty charter interview` | done |
| `spec-kitty charter lint` | missing (not ported) |
| `spec-kitty charter list` | done |
| `spec-kitty charter mission-type` | done |
| `spec-kitty charter mission-type list` | done |
| `spec-kitty charter new` | done |
| `spec-kitty charter org` | missing (not ported) |
| `spec-kitty charter org init` | missing (not ported) |
| `spec-kitty charter org validate` | missing (not ported) |
| `spec-kitty charter pack` | partial (lists unported subcommands) |
| `spec-kitty charter pack assemble` | missing (not ported) |
| `spec-kitty charter pack asset` | done |
| `spec-kitty charter pack asset list` | done |
| `spec-kitty charter pack asset path` | done |
| `spec-kitty charter pack list` | done |
| `spec-kitty charter pack path` | done |
| `spec-kitty charter pack regenerate-graph` | missing (not ported) |
| `spec-kitty charter pack validate` | missing (not ported) |
| `spec-kitty charter preflight` | done |
| `spec-kitty charter resynthesize` | done |
| `spec-kitty charter status` | done |
| `spec-kitty charter synthesize` | done |
| `spec-kitty charter validate` | done |
| `spec-kitty commit-guard-hook` | done |
| `spec-kitty config` | done |
| `spec-kitty consolidate` | done |
| `spec-kitty context` | done |
| `spec-kitty context cleanup` | done |
| `spec-kitty context info` | done |
| `spec-kitty context list` | done |
| `spec-kitty context mission-resolve` | done |
| `spec-kitty context mission-show` | done |
| `spec-kitty cutover-guard` | done |
| `spec-kitty dispatch` | done |
| `spec-kitty docs` | done |
| `spec-kitty docs query` | done |
| `spec-kitty doctor` | partial (lists unported subcommands) |
| `spec-kitty doctor bytecode` | done |
| `spec-kitty doctor channel` | done |
| `spec-kitty doctor charter-packs` | missing (not ported) |
| `spec-kitty doctor command-files` | done |
| `spec-kitty doctor contracts` | done |
| `spec-kitty doctor coordination` | done |
| `spec-kitty doctor cutover` | done |
| `spec-kitty doctor decisions` | done |
| `spec-kitty doctor env-file` | done |
| `spec-kitty doctor identity` | done |
| `spec-kitty doctor invocation-pairing` | missing (not ported) |
| `spec-kitty doctor mission-state` | done |
| `spec-kitty doctor mission-type` | missing (not ported) |
| `spec-kitty doctor ops` | missing (not ported) |
| `spec-kitty doctor provenance` | done |
| `spec-kitty doctor review-cycle-reconcile` | done |
| `spec-kitty doctor run-index` | done |
| `spec-kitty doctor shim-registry` | done |
| `spec-kitty doctor skills` | done |
| `spec-kitty doctor sparse-checkout` | done |
| `spec-kitty doctor state-roots` | done |
| `spec-kitty doctor tool-surfaces` | done |
| `spec-kitty doctor topology` | done |
| `spec-kitty doctor workspaces` | done |
| `spec-kitty events` | done |
| `spec-kitty events tail` | done |
| `spec-kitty glossary` | done |
| `spec-kitty glossary conflicts` | done |
| `spec-kitty glossary list` | done |
| `spec-kitty glossary resolve` | done |
| `spec-kitty glossary show` | done |
| `spec-kitty glossary validate` | done |
| `spec-kitty implement` | done |
| `spec-kitty init` | done |
| `spec-kitty intake` | done |
| `spec-kitty invocations` | done |
| `spec-kitty invocations list` | done |
| `spec-kitty issue-matrix` | done |
| `spec-kitty issue-matrix migrate` | done |
| `spec-kitty lint` | done |
| `spec-kitty materialize` | done |
| `spec-kitty merge` | done |
| `spec-kitty merge-driver-acceptance-matrix` | done |
| `spec-kitty merge-driver-decision-index` | done |
| `spec-kitty merge-driver-event-log` | done |
| `spec-kitty merge-driver-issue-matrix` | done |
| `spec-kitty merge-driver-meta` | done |
| `spec-kitty merge-driver-review-cycle` | done |
| `spec-kitty merge-driver-traces` | done |
| `spec-kitty migrate` | done |
| `spec-kitty migrate backfill-identity` | done |
| `spec-kitty migrate backfill-merge-commit` | done |
| `spec-kitty migrate backfill-mission-type` | done |
| `spec-kitty migrate backfill-provenance` | done |
| `spec-kitty migrate backfill-runtime-state` | done |
| `spec-kitty migrate backfill-topology` | done |
| `spec-kitty migrate backfill-wp-status` | done |
| `spec-kitty migrate charter-encoding` | done |
| `spec-kitty migrate normalize-lifecycle` | done |
| `spec-kitty migrate rebaseline-dossier-hashes` | done |
| `spec-kitty migrate repin-hooks` | done |
| `spec-kitty migrate rewrite-opposed-by` | done |
| `spec-kitty mission` | done |
| `spec-kitty mission close` | done |
| `spec-kitty mission current` | done |
| `spec-kitty mission follow-up` | done |
| `spec-kitty mission info` | done |
| `spec-kitty mission list` | done |
| `spec-kitty mission reopen` | done |
| `spec-kitty mission run` | done |
| `spec-kitty mission show` | done |
| `spec-kitty mission switch` | done |
| `spec-kitty mission-type` | done |
| `spec-kitty mission-type close` | done |
| `spec-kitty mission-type current` | done |
| `spec-kitty mission-type follow-up` | done |
| `spec-kitty mission-type info` | done |
| `spec-kitty mission-type list` | done |
| `spec-kitty mission-type reopen` | done |
| `spec-kitty mission-type run` | done |
| `spec-kitty mission-type show` | done |
| `spec-kitty mission-type switch` | done |
| `spec-kitty next` | done |
| `spec-kitty ops` | done |
| `spec-kitty ops log` | done |
| `spec-kitty ops undo` | done |
| `spec-kitty orchestrator-api` | missing (not ported) |
| `spec-kitty orchestrator-api accept-mission` | missing (not ported) |
| `spec-kitty orchestrator-api answer-decision` | missing (not ported) |
| `spec-kitty orchestrator-api append-history` | missing (not ported) |
| `spec-kitty orchestrator-api artifact-read` | missing (not ported) |
| `spec-kitty orchestrator-api artifact-submit` | missing (not ported) |
| `spec-kitty orchestrator-api cancel-decision` | missing (not ported) |
| `spec-kitty orchestrator-api check-prerequisites` | missing (not ported) |
| `spec-kitty orchestrator-api consolidate-mission` | missing (not ported) |
| `spec-kitty orchestrator-api contract-version` | missing (not ported) |
| `spec-kitty orchestrator-api defer-decision` | missing (not ported) |
| `spec-kitty orchestrator-api design-context` | missing (not ported) |
| `spec-kitty orchestrator-api design-status` | missing (not ported) |
| `spec-kitty orchestrator-api design-validate` | missing (not ported) |
| `spec-kitty orchestrator-api interview-record` | missing (not ported) |
| `spec-kitty orchestrator-api list-ready` | missing (not ported) |
| `spec-kitty orchestrator-api mission-state` | missing (not ported) |
| `spec-kitty orchestrator-api next` | missing (not ported) |
| `spec-kitty orchestrator-api open-decision` | missing (not ported) |
| `spec-kitty orchestrator-api plan` | missing (not ported) |
| `spec-kitty orchestrator-api record-analysis` | missing (not ported) |
| `spec-kitty orchestrator-api resolve-decision` | missing (not ported) |
| `spec-kitty orchestrator-api resolve-workspace` | missing (not ported) |
| `spec-kitty orchestrator-api specify` | missing (not ported) |
| `spec-kitty orchestrator-api start-implementation` | missing (not ported) |
| `spec-kitty orchestrator-api start-review` | missing (not ported) |
| `spec-kitty orchestrator-api tasks` | missing (not ported) |
| `spec-kitty orchestrator-api transition` | missing (not ported) |
| `spec-kitty plan` | done |
| `spec-kitty plugin` | done |
| `spec-kitty plugin build` | done |
| `spec-kitty profile-invocation` | done |
| `spec-kitty profile-invocation complete` | done |
| `spec-kitty profiles` | done |
| `spec-kitty profiles get` | done |
| `spec-kitty profiles list` | done |
| `spec-kitty profiles show` | done |
| `spec-kitty reconcile` | done |
| `spec-kitty regen` | done |
| `spec-kitty research` | done |
| `spec-kitty retrospect` | done |
| `spec-kitty retrospect backfill` | done |
| `spec-kitty retrospect create` | done |
| `spec-kitty retrospect summary` | done |
| `spec-kitty review` | done |
| `spec-kitty safe-commit` | done |
| `spec-kitty session-start` | done |
| `spec-kitty session-stop` | done |
| `spec-kitty spec-commit` | done |
| `spec-kitty specify` | done |
| `spec-kitty tasks` | done |
| `spec-kitty tracker` | done |
| `spec-kitty tracker bind` | done |
| `spec-kitty tracker map` | done |
| `spec-kitty tracker map add` | done |
| `spec-kitty tracker map list` | done |
| `spec-kitty tracker providers` | done |
| `spec-kitty tracker status` | done |
| `spec-kitty tracker sync` | done |
| `spec-kitty tracker sync publish` | done |
| `spec-kitty tracker sync pull` | done |
| `spec-kitty tracker sync push` | done |
| `spec-kitty tracker sync run` | done |
| `spec-kitty tracker unbind` | done |
| `spec-kitty upgrade` | done |
| `spec-kitty validate-encoding` | done |
| `spec-kitty validate-tasks` | done |
| `spec-kitty verify-setup` | done |
| `spec-kitty workflow` | done |
| `spec-kitty workflow export` | done |
| `spec-kitty workflow import` | done |
| `spec-kitty workflow list` | done |

"missing" paths belong to areas still porting them (orchestrator-api is
area I; charter consistency-check/fetch/lint/org and charter pack
assemble/regenerate-graph/validate and doctor charter-packs/invocation-pairing/
mission-type/ops are area K). The four "partial" groups match as soon as those
subcommands are registered with their upstream help.

## Exported API

`internal/cli` (additive fields; zero values keep the old behaviour):

| Symbol | Purpose |
|---|---|
| `Flag.Panel` | `rich_help_panel` name (default "Options") |
| `Flag.HideDefault`, `Flag.DefaultText` | suppress / override the rendered `[default: ...]` |
| `Flag.Min`, `Flag.Max` (`*int`), `cli.Bound(n)` | click `IntRange` on an Int flag: parse-time range check and `<int range> [lo<=x<=hi]` type column |
| `Arg.Type` | Arguments-panel type column (default `<str>`; an `Arg.Metavar` starting with `<` is also taken as the type) |
| `Arg.Panel`, `Arg.Hidden` | custom panel / hidden argument |
| `Command.Deprecated` | "(deprecated)" in help and listings |
| `Command.ShortHelp` | click `short_help` (listing text instead of the help's first paragraph) |
| `Command.GroupUsage` | usage line `... COMMAND [ARGS]...` for a leaf that upstream implements as a callback-only typer sub-app (`charter list`) |
| `Command.NoShortHelp` | no `-h` alias (upstream `upgrade`) |
| `Command.ParseUsagePath` | command path shown in usage errors raised while parsing |
| `UsageError.NoContext` | click error without context: Error panel only |
| `cli.RootHelpBanner` | hook printing the banner before the root rich help |

Declaration conventions the renderer relies on: an argument's display name is
its `Metavar` stripped of `{}`/`[]` (or its name with `-` -> `_`), usage shows
`{name}` / `[name]` (+ `...` when variadic); an option's `Metavar` is its type
column text; `Default: false` on a negatable flag shows `[default: no-x]`,
`Default: nil` shows nothing (typer `Optional[bool] = None`).

`internal/rich`:

| Symbol | Purpose |
|---|---|
| `Measurement`, `Options`, `Renderable`, `Measure`, `RenderLines` | rich measuring/rendering protocol (plain text) |
| `Text` (`NewText`, `FromMarkup`), `WrapText` | rich Text wrapping (justify, overflow fold/crop/ellipsis/ignore, tabs) |
| `Group`, `Padding` (`Pad`), `AlignLeft`, `Columns`, `Grid` (+`Column`, `AddRow`), `BoxPanel` | rich renderables used by help and error output |
| `RatioReduce`, `RatioDistribute`, `CollapseWidths` | rich `_ratio` / table width algorithms |
| `CellLen`, `CharWidth`, `SetCellSize` | rich cell widths from rich's unicode 17 table (`celltable.go`, generated) incl. ZWJ/VS16 handling |
| `HeavyTable`, `TableSpec`, `TableX`, `AlignText` | the HEAVY_HEAD tables formerly in `internal/commands/setup/richtable*.go` |
| `Width` | console width (moved here from `internal/cli`; `cli.Width` delegates) |

`internal/testkit`: `NormalizeValues` now masks all-digit 7-12 digit short
SHAs (not when part of a decimal/version or list number); `"pid"` values are
masked before SHAs. `Binary` names its temp dir `sk-go-bin-<pid>-*` and
removes directories of finished test processes (12 GB of leftovers had filled
`/tmp`).

## Deviations / open issues

- Terminal width: like before, the width is `COLUMNS` (> 20) else 80; rich
  also queries the terminal size of stdin/stdout/stderr, which only matters
  for interactive use.
- `merge` (hidden stub, util area): its refusal message is printed unwrapped,
  upstream wraps it at the console width (not a help-renderer issue; not
  changed).
- Help colours/styles are not reproduced (no ANSI output on a terminal).
