# Porting guide: functional parity with upstream spec-kitty

This repository is a Go implementation of the upstream Python `spec-kitty`
CLI (`spec-kitty-cli`, targeting published **4.0.0rc7**, tag `v4.0.0rc7`
at `1b0fe9fc388b095f9964471b0177b4213e9c6c98`). See the
[current RC7 parity record](parity/RC7.md). The goal is **functional adherence, not code parity**: the
same commands, flags, exit codes, JSON outputs, on-disk files and git effects
an agent or user observes, implemented idiomatically in Go. Python internals,
module layout and incidental implementation details are *not* reproduced.

## What "parity" means here

Must match (verified by differential tests against the oracle):

1. Command paths, arguments and flags (including hidden aliases agents use).
2. Exit codes, and the **JSON** emitted by `--json` modes (key names, nesting,
   value types, ordering when upstream is ordered). Human-readable text should
   carry the same information; exact wording/colour/box art is best-effort.
3. Files written to the project and to `$HOME` (paths, formats; byte-identical
   where the upstream format is deterministic: `meta.json`,
   `status.events.jsonl`, `status.json`, `lanes.json`, WP frontmatter,
   `config.yaml`, rendered agent command files, skills).
4. Git effects: branches, worktrees, commits (messages and file sets), merges.
5. Guard behaviour: what is refused, with which error code/message.

Not required: Python-only diagnostics (bytecode doctor, ruff/mypy lint
internals), SaaS/hosted sync (upstream ships it disabled in this tree), byte
equality of hashes that depend on Python source files.

## Reference material (local, not committed)

Reference root `REF=$HOME/.cache/spec-kitty-parity/rc7-1b0fe9fc38`. Recreate the upstream
checkout and oracle with `scripts/setup-reference.sh ../spec-kitty "$REF"`.
Then capture the exact command inventory and subprocess help:

```sh
"$REF/oracle-venv/bin/python" scripts/capture-reference-tree.py "$REF"
python3 scripts/capture-reference-help.py "$REF"
```

| What | Where |
|---|---|
| Upstream source (read-only) | `$REF/upstream` (`src/specify_cli`, `src/charter`, `src/runtime`, `src/kernel`, `packs/built-in`) |
| **Oracle** (real wheel install) | `$REF/oracle-bin/spec-kitty`. Never use an editable install: it runs init in local template mode and writes extra files |
| External status reducer package | `$REF/oracle-venv/lib/python3.13/site-packages/spec_kitty_events` |
| Full command tree (276 paths) | `$REF/inventory/cli-tree.json`, raw help in `$REF/inventory/help/*.txt` |
| Templates and resolution priority | `$REF/upstream/src/specify_cli/template`, `$REF/upstream/packs/built-in/missions` |
| On-disk formats | Upstream schemas and the fixtures in `internal/testkit/missionfx/testdata` |
| End-to-end workflow | The [README](../README.md), `internal/testkit/missionfx`, and `internal/commands/closeout` differential scenarios |

Always run the oracle with an isolated `HOME` (it writes global files on every
invocation) and `SPEC_KITTY_NO_UPGRADE_CHECK=1`.

## Repository layout

```
cmd/spec-kitty/            main + one blank-import file per command area (imports_<area>.go)
internal/cli               click/typer-compatible command framework (flags, help, usage errors exit 2)
internal/app               root command, Register()/Group() helpers, Version
internal/jsonx             ordered JSON objects + Python json.dumps-compatible encoder
internal/yamlx             ordered YAML parse + ruamel-compatible emitter
internal/fm                Markdown frontmatter documents (WP files)
internal/ulid, clock       python-ulid compatible ids, datetime.isoformat timestamps
internal/gitx              git executable wrapper
internal/project           project-root resolution (worktree aware), ~/.kittify paths
internal/assets            go:embed of the upstream wheel's data files (scripts/sync-assets.sh)
internal/testkit           sandboxes, Go/oracle pair runner, output normalisation
internal/rich              rich-compatible console output: markup, wrapping, tables, panels, log lines
internal/pyjson            CPython json/repr semantics (repr of dicts/lists in error messages)
internal/pyyaml            PyYAML safe_load / yaml.dump port (for files upstream writes with PyYAML)
```

Domain and command packages live under `internal/<area>/...`.

## Conventions

- **Registering commands**: in your package, `func init() { app.Register(func(root *cli.Command) { ... }) }`.
  Use `app.Group(root, "agent", "Commands for AI agents...")` to get-or-create
  shared groups. Add `cmd/spec-kitty/imports_<area>.go` with a blank import
  of your package. Never edit another area's files to register.
- **Help text**: copy the upstream one-line and long help from
  `inventory/help/<path>.txt`. Flags: copy names, aliases, defaults, choices.
  Keep the help's original line breaks (the Commands panel shows the first
  paragraph unjoined). The renderer reproduces typer's rich help byte for
  byte; see `docs/parity/help.md` for the declaration conventions
  (`Metavar`, `Arg.Type`, `Flag.Min/Max`, `Panel`, `Deprecated`, ...) and
  `go test ./internal/helpparity` to check every path.
- **Console output**: use `internal/rich` for rich markup, wrapped text, tables
  and panels rather than writing another renderer; extend it there if needed.
- **JSON output**: build `*jsonx.Object` in upstream key order and print with
  `jsonx.Dumps(v, jsonx.Pretty)` (= `json.dumps(indent=2)`, ensure_ascii) unless
  upstream uses another style. Files: use the exact `json.dumps` options listed
  in `formats.md` §A.0 (`jsonx.Options{...}`).
- **YAML**: parse with `yamlx.Parse*`, emit with `yamlx.Emit` (ruamel style).
  For files upstream writes with PyYAML (`safe_dump`/`yaml.dump`: sorted keys,
  different indentation and line folding) use `internal/pyyaml`.
- **Errors**: usage errors → `cli.Usage(cmd, ...)` (exit 2). Domain failures →
  print upstream's message/JSON and return `cli.Exit(1)` (or the upstream code).
- **Paths**: resolve the project with `project.Locate("")`; it follows linked
  worktrees back to the primary checkout like upstream.
- **Timestamps / ids**: `clock.NowUTCISO()`, `ulid.New()`. Tests may set
  `SPEC_KITTY_FAKE_NOW`.
- **No new third-party dependencies** without coordinating (currently only
  `gopkg.in/yaml.v3`).

## Testing

- Unit tests next to the code.
- End-to-end tests use `testkit.NewGitSandbox(t)` + `s.Run(...)` (builds the
  binary once).
- Mission fixtures: `internal/testkit/missionfx` gives an initialised project
  (`NewProject`, Go-only; `NewOraclePair`, both sides set up by the oracle's
  init) and the two-WP calc mission (`AuthorCalc`, `OracleCalcMission`,
  `NewLocalMission`, `AnalysisReport`). Reuse and extend it instead of writing
  per-package fixtures.
- Differential tests use `testkit.NewPair(t)` and skip unless
  `SPEC_KITTY_ORACLE` points at the oracle:

  ```sh
  PARITY_TMP=$(mktemp -d /var/tmp/spec-kitty-parity.XXXXXX)
  TMPDIR="$PARITY_TMP" \
  SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty \
  SPEC_KITTY_HELP_INVENTORY=$REF/inventory/help go test ./...
  ```

  Keep scratch outside initialized projects and the real home directory:
  root-discovery tests must not inherit an ancestor's `.kittify` directory.
  A disk-backed location also avoids small tmpfs inode limits in broad runs.

  `Pair.Compare` diffs exit code + normalized stdout; `Pair.CompareTree`
  diffs file trees/contents. Normalisation masks ULIDs, timestamps, SHAs,
  UUIDs and sandbox paths.
- `go vet ./... && go test ./...` must pass without the oracle too.
