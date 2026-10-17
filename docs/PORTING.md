# Porting guide: functional parity with upstream spec-kitty

This repository is a Go implementation of the upstream Python `spec-kitty`
CLI (`spec-kitty-cli`, aligned with **4.0.0rc8**, upstream main
`dccf199f7a`). The goal is **functional adherence, not code parity**: the
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

Scratch root `SCRATCH=/tmp/claude-1000/-home-user-Documents-code-spec-kitty-redesign/664548cd-9b83-4356-9286-3ed140fe95e0/scratchpad`

| What | Where |
|---|---|
| Upstream source (read-only) | `$SCRATCH/upstream` (`src/specify_cli`, `src/charter`, `src/runtime`, `src/kernel`, `packs/built-in`) |
| **Oracle** (real wheel install) | `$SCRATCH/oracle-spec-kitty` — use this one; `$SCRATCH/venv` is an editable install that behaves differently (local template mode) |
| External status reducer package | `$SCRATCH/formats/wheelvenv/lib/python3.13/site-packages/spec_kitty_events` |
| Full command tree (276 paths) | `$SCRATCH/inventory/cli-tree.md`, `cli-tree.json`, raw help in `inventory/help/*.txt` |
| Template invocations / priority | `$SCRATCH/inventory/template-invocations.md`, `priority.md` |
| On-disk formats (very detailed) | `$SCRATCH/inventory/formats.md` |
| End-to-end oracle trace | `$SCRATCH/trace/TRACE.md`, snapshots in `$SCRATCH/trace/snapshots/` |

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
```

Domain and command packages live under `internal/<area>/...`.

## Conventions

- **Registering commands**: in your package, `func init() { app.Register(func(root *cli.Command) { ... }) }`.
  Use `app.Group(root, "agent", "Commands for AI agents...")` to get-or-create
  shared groups. Add `cmd/spec-kitty/imports_<area>.go` with a blank import
  of your package. Never edit another area's files to register.
- **Help text**: copy the upstream one-line and long help from
  `inventory/help/<path>.txt`. Flags: copy names, aliases, defaults, choices.
- **JSON output**: build `*jsonx.Object` in upstream key order and print with
  `jsonx.Dumps(v, jsonx.Pretty)` (= `json.dumps(indent=2)`, ensure_ascii) unless
  upstream uses another style. Files: use the exact `json.dumps` options listed
  in `formats.md` §A.0 (`jsonx.Options{...}`).
- **YAML**: parse with `yamlx.Parse*`, emit with `yamlx.Emit` (ruamel style).
  For files upstream writes with PyYAML `safe_dump` (sorted keys, different
  indentation) write a dedicated emitter option rather than approximating.
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
- Differential tests use `testkit.NewPair(t)` and skip unless
  `SPEC_KITTY_ORACLE` points at the oracle:

  ```sh
  SPEC_KITTY_ORACLE=$SCRATCH/oracle-spec-kitty go test ./...
  ```

  `Pair.Compare` diffs exit code + normalized stdout; `Pair.CompareTree`
  diffs file trees/contents. Normalisation masks ULIDs, timestamps, SHAs,
  UUIDs and sandbox paths.
- `go vet ./... && go test ./...` must pass without the oracle too.
