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
