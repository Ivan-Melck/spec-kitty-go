# Area B parity: project setup & agent integration

Upstream: `spec-kitty-cli` 4.0.0rc8. Packages: `internal/kittify`,
`internal/agents`, `internal/bootstrap`, `internal/commands/setup`
(registered via `cmd/spec-kitty/imports_setup.go`).

Differential tests run with `SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty`;
every `*WithoutOracle` test and every test that only drives the Go binary
runs in plain `go test ./...`. The Go binary never reaches pypi.org under
test: `testkit` points its PyPI lookups at a closed local port (see
`SPEC_KITTY_GO_PYPI_JSON_URL` below) unless a test serves a fake index.

## Commands

| Command path | Status | Tests (internal/commands/setup) | Known deviations |
|---|---|---|---|
| *(root callback)* startup bootstrap | done | `bootstrap_test.go`, all oracle tests compare `$HOME` trees | Freshness-lock signature (irreducible, see below). |
| *(root callback)* schema gate + upgrade nag | done | `TestUpgradeStaleAndLegacyMatchOracle`, `TestUpgradeWithoutOracle`, `TestUpgradeCLIQueriesFakeIndex` | Nag and "no upgrade available" notices are TTY-only (as upstream); no SGR colours. |
| *(startup)* `.kitty.env` operator env loading | done | `TestDoctorSetupLeavesMatchOracle`, `TestKittyEnvHomeLineWarnsLikeOracle` | The `UserWarning` names `<spec-kitty>/specify_cli/__init__.py:34` instead of the site-packages path. |
| `init` | done | `TestInitMatchesOracle`, `TestInitErrorsMatchOracle`, `TestInitNamedDirAndNoGit`, `TestInitWithoutOracle`, `TestInitArrowKeyPicker*` (PTY) | `metadata.yaml` `python_version` (irreducible). Arrow-key picker matches frame text; colours not emitted. With a non-terminal stdin and `SPEC_KITTY_FORCE_INTERACTIVE` a line prompt is used (upstream's readchar crashes there). |
| `agent config` (group + list/add/remove/status/sync/set) | done | `TestAgentConfigMatchesOracle`, `TestAgentConfigOrphansMatchOracle`, `TestAgentConfigWithoutOracle` | `sync` orphan scan order (irreducible). |
| `verify-setup` | done | `TestVerifySetupMatchesOracle` | |
| `config` | done | `TestConfigAndSessionsMatchOracle`, `TestConfigShowOriginOrgPackTier` | `--show-origin` package-default paths name embedded assets (`packs/built-in/...`). |
| `session-start` / `session-stop` | done | `TestConfigAndSessionsMatchOracle` | Session upgrade cache (`~/.kittify/last-cli-check.json`) refreshes in a re-exec of the Go binary instead of `python -m`. |
| `upgrade` | done | see "Upgrade" | |
| `doctor` (group), `command-files`, `state-roots`, `env-file`, `channel` | done | `TestDoctorSetupLeavesMatchOracle` | |
| `doctor skills` | done | `TestDoctorSetupLeavesMatchOracle` | `pack_skills` always `[]` (Remaining work). |
| `doctor tool-surfaces` | done | `TestDoctorToolSurfacesMatchOracle` | `--fix` `repair` block lists surface ids; plugin bundles reported, not staged (Remaining work). |
| `doctor bytecode` | done (trivial) | — | Irreducible (no `.pyc` files). |
| `regen` | done (refusal path) | — | Irreducible (Python maintainer tool). |
| `materialize`, `migrate` (group + 12 leaves) | done | see maintenance.md | Owned by the maintenance area. |

## Upgrade

* **Migrations:** all 124 upstream migrations are ported with their own
  detect / can_apply / apply (dry-run wording, change lines, warnings,
  errors, `manual_review_required` / `preserved_paths`, JSON reports in
  `changes_made[0]`), fail-closed detection, `runs_first` reselection
  (`charter_pack_cutover`), legacy-id normalisation, version-stamp
  capture/restore and `runs_on_worktrees`. Catalog order, plan table and
  `pending_migrations` match for any recorded version.
* **Corpora** (`upgrade_corpora_test.go`, `--dry-run --verbose`,
  `--dry-run --json`, `--yes --json`, tree + modes + contents, commit, re-run):
  fresh projects stamped 0.1.0 / 2.0.0 / 3.0.0 / 3.2.0 / 3.2.6 / 4.0.0rc1;
  legacy layouts v0.1 (`.specify/`, `specs/`), v0.9 (claude and
  claude+codex: lanes dirs, scripts, memory, stale commands), v2.0 (no
  agents block, stale commands, flow-style frontmatter), v3.0
  (constitution dir with a legacy governance key, `.kittify/doctrine`,
  failed first run and recovery run); worktree fan-out (lane, feature with
  untracked file, integrating `kitty/mission-*` worktree skipped) at 3.2.0
  and 4.0.0rc8 including per-worktree commits.
* **Outcome / finalizer:** failure reasons incl. `worktree_failure`,
  manual-review section and JSON fields, held-for-review warnings,
  structured migration summaries, version-stamp settle with the
  "metadata.yaml restored" notice, auto-commit with the upstream candidate
  order (mutation log for moves/copies standing in for the `sys.audit`
  hook), index untracks committed from a temporary index.
* **Compat planner:** PyPI JSON provider (2 s timeout, no redirects, 1 MiB
  cap, prerelease channel via `SPEC_KITTY_PRERELEASE`), nag cache
  (`$XDG_CACHE_HOME/spec-kitty/upgrade-nag.json`, 0600, ownership checks),
  `upgrade.yaml` / `SPEC_KITTY_NAG_THROTTLE_SECONDS` / `SPEC_KITTY_NO_NAG`,
  read-only vs `query_latest` semantics, `ALLOW_WITH_NAG` /
  `install_method_unknown`, `--agent-check` (snooze / never-ask / always
  reasons) and `--agent-choice` recording, the "no upgrade available"
  notifier and the session-presence upgrade cache.
  Go-only test seam: `SPEC_KITTY_GO_PYPI_JSON_URL` (a URL template with one
  `%s` for the package) replaces `https://pypi.org/pypi/%s/json`; upstream
  hardcodes the URL, so these paths are tested Go-only against a fake index
  (`compat_latest_test.go`); install method is always `unknown`.
* `--plan-json`: compared up to `migrations`; `effects` / `dispositions`
  are emitted empty (Remaining work).

## Library API added in wave 5

* `kittify`: `VersionStamp` (`CaptureVersionStamp`, `Restore`, `Version`),
  `Metadata.NormalizeAndSaveLegacyIDs`, `LegacyMigrationID`,
  `CanonicalSchemaCapabilities`; `StampSchemaVersion` writes like
  `atomic_write` (0600).
* `agents`: `Profile.AppliesToLanguages`, `ProjectionResult.Pruned`,
  `ProfileAdmit` hook (activation + language gate), `SkillEntry.Origin`.
* Additive extensions to other areas: `fm.FlowKeys` / `fm.RestoreFlow`
  (ruamel flow-sequence round trip), `charter.DefaultGovernance`,
  `charter.HashContent`, `migrate.MissionNeedsCutover`,
  `migrate.OnLayoutMove`; `migrate.BackfillWPIDs` now backfills
  `dependencies: []` and keeps flow sequences (FrontmatterManager.read);
  `rich` table cells keep whitespace runs when wrapping; `testkit`
  environment sets the PyPI seam.

## Irreducible differences

* Freshness-lock `template_source_signature` (MAINT-05): upstream hashes
  its own Python module files and their install paths; the Go binary
  records a signature of its embedded templates and renderer identity.
* `metadata.yaml` `environment.python_version` (MAINT-07): there is no
  Python interpreter; the Go toolchain version is recorded.
  `platform`/`platform_version` match.
* `agent config sync` orphan order (MAINT-09): upstream iterates a
  hash-randomised set; Go sorts.
* `doctor bytecode` / `regen` (MAINT-14): no bytecode exists; fixture
  regeneration is a Python maintainer tool.
* Install-method detection: the Go binary is not installed by pip/pipx/uv,
  so `install_method` is always `unknown`.
* Terminal styling: the Go console emits no SGR colours on a TTY (picker,
  nag, notices).

## Remaining work

* MAINT-03 / MAINT-11 / MAINT-12 / MAINT-13: the tool-surface assessment
  engine (`upgrade/assessment.py`, `tool_surface/*`, `skills/installer.py`,
  `skills/command_installer.py`, about 7k upstream lines) is not ported:
  `--plan-json` `effects`/`dispositions` stay empty, `doctor skills`
  `pack_skills` is `[]`, `doctor tool-surfaces --fix` lists surface ids
  instead of effect ids, and plugin bundles are not staged.
* MAINT-15: `agents.VibeSkillPathConfigured` still uses a regex TOML read;
  `internal/pytoml` (PYF-03) has not landed on functional-parity yet.
