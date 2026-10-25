# Spec Kitty — Go CLI, RC7

A standalone Go implementation of the Spec Kitty CLI, targeting published
[`v4.0.0rc7`](https://github.com/spec-kitty/spec-kitty/releases/tag/v4.0.0rc7)
at commit `1b0fe9fc388b095f9964471b0177b4213e9c6c98`.
It provides mission authoring, agent commands, governance, worktrees, review
and consolidation under the executable name `spec-kitty`.

The target is RC7. Command skills retain names such as `spec-kitty.specify`;
RC8-only commands and ledger migrations are outside this release's surface.
See [parity scope and verification](docs/parity/RC7.md).

## Requirements

- **Go 1.26 or newer** to build or install from this checkout.
- **Git** on `PATH`, with your Git name and email configured for workflow commits.
- An installed AI assistant, such as Codex or Claude Code, to author and execute
  missions through the generated commands and skills.

Templates and skills are embedded. Running the Go CLI requires neither Python
nor an upstream Spec Kitty installation. Python and `uv` are needed only for
comparisons against the upstream reference during development.

## Install from this checkout

Run from this repository's root. On Linux or macOS:

```sh
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install ./cmd/spec-kitty
export PATH="$HOME/.local/bin:$PATH"
spec-kitty --version
```

Add the `export PATH=...` line to your shell's startup file for future sessions.
Expected version:

```text
spec-kitty-cli version 4.0.0rc7
```

To keep the executable inside the repository instead:

```sh
CGO_ENABLED=0 go build -trimpath -o bin/spec-kitty ./cmd/spec-kitty
./bin/spec-kitty --version
./bin/spec-kitty --help
```

You can copy the binary to a machine with the same operating system and CPU
architecture. If you already have Spec Kitty installed, `command -v spec-kitty`
shows which executable your shell will run.

## Initialize a project

Create a project configured for Codex:

```sh
spec-kitty init my-project --ai codex --non-interactive
cd my-project
git init -b main
git add .
git commit -m "Initialize Spec Kitty project"
```

`init` creates project files; it does **not** initialize Git or create commits.
For an existing Git repository, enter its root and run:

```sh
spec-kitty init . --ai codex --non-interactive
```

Use `--ai claude`, or `--ai codex,claude` for both assistants. Omit `--ai` and
`--non-interactive` to choose assistants interactively. `spec-kitty init --help`
lists all supported agents.

The scaffold includes `.kittify/config.yaml`, metadata, agent commands and
skills, and ignore rules. Codex's command skills live under
`.agents/skills/spec-kitty.<command>/SKILL.md`. Shared runtime assets are
created under `~/.kittify/` on CLI startup.

```sh
spec-kitty agent config list
spec-kitty verify-setup
```

## Work on a mission

Open your assistant in the initialized project and use its installed Spec
Kitty commands or skills:

1. **Specify** the behavior and acceptance criteria.
2. **Plan** the implementation approach.
3. **Tasks** — create and finalize work packages.
4. **Implement and review** — follow the runtime prompts in the allocated
   workspace, with an independent session reviewing each work package.
5. **Accept and consolidate** the approved work.

For example, in a harness with slash commands, start with
`/spec-kitty.specify Add CSV export`, then `/spec-kitty.plan` and
`/spec-kitty.tasks`. In a skill-based harness, invoke the installed
`spec-kitty.specify`, `spec-kitty.plan` and `spec-kitty.tasks` skills.
The CLI supplies state and prompts; the assistant performs the work described.

Once a mission exists, replace `<mission>` with its actual slug or handle:

```sh
spec-kitty next --mission <mission> --json
spec-kitty agent tasks status --mission <mission>
```

`next` without `--result` queries the state. After completing the issued step,
the agent advances with the real outcome:

```sh
spec-kitty next --agent codex --mission <mission> --result success --json
```

Use `failed` or `blocked` when appropriate, and handle requested decisions
before advancing. Per-work-package actions are:

```sh
spec-kitty agent action implement WP01 --agent codex --mission <mission>
spec-kitty agent action review WP01 --agent codex --mission <mission>
```

Follow the returned prompt and workspace path. When review and acceptance
evidence are ready:

```sh
spec-kitty agent mission accept --mission <mission> --diagnose
spec-kitty agent mission accept --mission <mission>
spec-kitty consolidate --mission <mission> --dry-run
spec-kitty consolidate --mission <mission> --yes
```

Consolidation merges approved work and applies the mission's cleanup policy.
The gates explain any remaining blockers.

## Small tasks without a mission

Dispatch an ad-hoc request under a routed agent profile:

```sh
spec-kitty dispatch "Explain how configuration is loaded"
```

Follow the returned context capsule. Close the Op with the invocation ID
printed there:

```sh
spec-kitty profile-invocation complete --invocation-id <id> --outcome done
```

Use `failed` or `abandoned` when that describes the actual outcome.

## Existing projects and repairs

Preview project migrations before applying them:

```sh
spec-kitty upgrade --dry-run
spec-kitty upgrade --yes
```

For missing generated agent commands or skills:

```sh
spec-kitty agent config sync --create-missing --keep-orphaned
spec-kitty verify-setup
```

Use `spec-kitty doctor --help` to select a diagnostic. Every command group has
`--help`, for example `spec-kitty charter --help` and
`spec-kitty agent action --help`.

RC7 does not undo migrations applied by RC8. Use an RC7-compatible project
checkout when switching an existing RC8 project back to this version.

## Uninstall

RC7 has no dedicated uninstall command. If you are retiring Spec Kitty from
a project, complete the optional project cleanup before removing the CLI.

### Optional project cleanup

Back up mission work, custom configuration and memory before removing project
files. In each affected repository:

1. Run `git worktree list`. Save any changes in Spec Kitty worktrees, then
   remove unwanted worktrees with `git worktree remove <path>`.
2. Remove Spec Kitty hooks from your assistant's settings and the Git hooks
   directory shown by `git rev-parse --git-path hooks`. Remove only Spec Kitty
   hooks or hook entries; restore any previous hooks from their backups.
3. Inspect `git config --local --get-regexp '^merge\.spec-kitty-'`. Remove
   the corresponding `merge.spec-kitty-*` sections from local Git configuration,
   together with their `merge=spec-kitty-*` entries in `.gitattributes`.
4. Use the manifests in `.kittify/` to identify generated agent files. Remove
   those files, such as `.agents/skills/spec-kitty.<command>/SKILL.md`, and
   Spec Kitty orientation blocks in `AGENTS.md` or `CLAUDE.md` between
   `<!-- spec-kitty:orientation -->` and `<!-- /spec-kitty:orientation -->`.
   Keep unrelated skills, instructions and settings.
5. After backing them up, remove `.kittify/` and any unwanted Spec Kitty entries
   in `.gitignore` and the file shown by `git rev-parse --git-path info/exclude`.
   Archive `kitty-specs/` and `kitty-ops/` if you want to retain mission and Op
   history; deleting those directories removes that history from the working
   tree.

Review `git diff` before committing project cleanup.

### Remove the executable

Locate the active installation:

```sh
command -v spec-kitty
```

For the installation command documented above:

```sh
rm -f "$HOME/.local/bin/spec-kitty"
```

For the local build, run from this checkout:

```sh
rm -f ./bin/spec-kitty
```

If you used another `GOBIN` or copied the executable elsewhere, remove that
copy instead. A plain `go install` normally installs to `~/go/bin` unless
`GOBIN` or `GOPATH` changes it; `go env GOBIN GOPATH` shows your settings.
Remove any Spec Kitty aliases from your shell configuration. Refresh Bash's
command cache with `hash -r` (or `rehash` in zsh), then run
`command -v spec-kitty` again. Another installed copy may still be found.

### Optional user-level cleanup

Inspect the inventories in `~/.kittify/cache/*-assets.json` before deleting
them: they identify generated files in your assistants' global command and
skill directories. Remove only the Spec Kitty files you no longer need;
preserve unrelated files and any customizations you want to keep.

After backing up custom content, you can remove the shared runtime and state
directories if no other Spec Kitty installation uses them:

```sh
rm -rf "$HOME/.kittify" "$HOME/.spec-kitty"
```

If `SPEC_KITTY_HOME` is set, inspect and clean that directory instead of the
default locations. Upgrade preferences and caches can also be removed from
`${XDG_CONFIG_HOME:-$HOME/.config}/spec-kitty` and
`${XDG_CACHE_HOME:-$HOME/.cache}/spec-kitty`. These locations can be shared
with the Python CLI. Remove any obsolete Spec Kitty environment settings
from your shell configuration, then restart your assistant sessions.

## Development and verification

```sh
go test ./...
go vet ./...
```

Set `SPEC_KITTY_ORACLE` to enable upstream CLI differential tests. The
[porting guide](docs/PORTING.md) explains how to build the reference, capture
its help inventory, and run differential tests. The
[RC7 parity record](docs/parity/RC7.md) identifies the reference and verification
scope.

The earlier authority-kernel program is preserved in the
[historical redesign overview](docs/HISTORICAL_REDESIGN_README.md).

## License

[MIT](LICENSE).
