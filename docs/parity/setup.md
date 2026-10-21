# Area B parity: project setup & agent integration

Upstream: `spec-kitty-cli` 4.0.0rc8. Packages: `internal/kittify`,
`internal/agents`, `internal/bootstrap`, `internal/commands/setup`
(registered via `cmd/spec-kitty/imports_setup.go`).

Differential tests run with `SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty`;
every `*WithoutOracle` test runs in plain `go test ./...`.

## Commands

| Command path | Status | Tests (internal/commands/setup) | Known deviations |
|---|---|---|---|
| *(root callback)* startup bootstrap | done | `bootstrap_test.go`, all oracle tests compare `$HOME` trees | Freshness lock contents differ (Go records its own asset revision). |
| *(root callback)* schema gate | done | `TestUpgradeStaleAndLegacyMatchOracle`, `TestUpgradeWithoutOracle` | No PyPI query: the upgrade nag (`ALLOW_WITH_NAG`) never fires. |
| *(startup)* `.kitty.env` operator env loading | done | `TestDoctorSetupLeavesMatchOracle` (env-file tiers) | A `SPEC_KITTY_HOME=` line is dropped silently (upstream also emits a `UserWarning`). |
| `init` | done | `TestInitMatchesOracle` (8 `--ai` selections, project + HOME trees + `.git/config`), `TestInitErrorsMatchOracle`, `TestInitNamedDirAndNoGit`, `TestInitWithoutOracle` | `metadata.yaml` `python_version`/`platform_version` carry Go runtime values. Interactive agent picker is a plain numbered prompt, not the arrow-key UI. |
| `agent config` (group) | done | `TestAgentConfigOrphansMatchOracle` (bare group exit 2) | Help box layout comes from the shared `internal/cli` renderer. |
| `agent config list` | done | `TestAgentConfigMatchesOracle` | |
| `agent config add` | done | `TestAgentConfigMatchesOracle`, `TestAgentConfigWithoutOracle` | |
| `agent config remove` | done | `TestAgentConfigMatchesOracle`, `TestAgentConfigOrphansMatchOracle` | |
| `agent config status` | done | `TestAgentConfigMatchesOracle`, `TestAgentConfigOrphansMatchOracle` | |
| `agent config sync` | done | `TestAgentConfigMatchesOracle` (`--sync-hooks`, `--create-missing --json`, `--keep-orphaned`) | Orphan scan is sorted (upstream iterates a hash-randomised set). YAML parse-error text differs (PyYAML vs yaml.v3); exit codes/envelope match. |
| `agent config set` | done | `TestAgentConfigMatchesOracle` | |
| `verify-setup` | done | `TestVerifySetupMatchesOracle` (text, `--json`, `--diagnostics`, `--mission`) | |
| `config` | done | `TestConfigAndSessionsMatchOracle` | `--show-origin` package-default paths name embedded assets (`packs/built-in/...`) instead of site-packages paths; the org-pack tier is not consulted. |
| `session-start` | done | `TestConfigAndSessionsMatchOracle` | |
| `session-stop` | done | `TestConfigAndSessionsMatchOracle` | |
| `upgrade` | done (current-schema + rc5–rc8 window); partial for old projects | `TestUpgradeCurrentProjectMatchesOracle`, `TestUpgradeStaleAndLegacyMatchOracle`, `TestUpgradeFlagsMatchOracle`, `TestUpgradeWithoutOracle` | See "Upgrade" below. |
| `doctor` (group) | done | `TestDoctorSetupLeavesMatchOracle` (bare group: help + stderr, exit 2) | |
| `doctor skills` | done | `TestDoctorSetupLeavesMatchOracle` (incl. `--fix`) | `pack_skills` always `[]` (org/pack skill catalog not ported). |
| `doctor command-files` | done | `TestDoctorSetupLeavesMatchOracle` | |
| `doctor tool-surfaces` | done | `TestDoctorToolSurfacesMatchOracle` (2 agent mixes, `--kind`, `--tool`, drift/missing, `--fix`) | `--fix` repairs through the upgrade repair path; its `repair` block lists surface ids, not upstream's internal effect hashes. Plugin bundles (`dist/spec-kitty-plugins/*`) are reported but never staged. |
| `doctor state-roots` | done | `TestDoctorSetupLeavesMatchOracle` | |
| `doctor env-file` | done | `TestDoctorSetupLeavesMatchOracle` | |
| `doctor channel` | done | `TestDoctorSetupLeavesMatchOracle` | |
| `doctor bytecode` | done (trivial) | — | No `.pyc` files exist in the Go port: always clean; `root` is the executable's directory. |
| `regen` | done (refusal path) | — | Outside an upstream source checkout it refuses like upstream (exit 2); inside one it reports that fixture regeneration is a Python maintainer tool. |
| `materialize` | missing | — | Derived status views belong to the status model (area A). |
| `migrate` (group + 12 leaves) | missing | — | One-time mission-data backfills (area A data model); `upgrade` covers routine migration. |

## Upgrade

* Compat planner (`compat.planner`): project states, safety registry,
  decisions and exit codes 4/5/6, JSON contract (`--json` with `--project`
  or `--dry-run`, `--cli`, `--plan-json` envelopes). `cli.latest_version`
  is always `null` / `latest_source: "none"` (no network).
* Migration catalog: all 124 registered upstream migrations (ids, descriptions,
  target versions, `runs_first`) are generated into
  `migrations_catalog.go`, so selection, the migration-plan table and
  `pending_migrations` match upstream for any recorded version.
* Ported migrations (detect/apply): `4_0_0rc5_install_lane_tip_recorder`,
  `4.0.0rc7_exclude_spec_kitty`, the gitignore backfills
  (`3.2.4_*`, `3.2.5_*`, `3.2.6rc3_*`) and the merge-driver seeding
  migrations (`3.2.6_*`, `3.2.7_review_cycle_*`, `4.0.0rc5_decision_index_*`).
  Every other migration detects as "not needed" and is recorded as
  `skipped` / "Not applicable"; the finalizer's repairs (global runtime,
  skills, command skills, profiles, orientation, activations, merge-driver
  git config) then bring generated surfaces current.
* Finalizer: schema stamp (PyYAML-style re-dump), activation provisioning,
  surface repair, clone-local merge-driver config, baseline-diff auto-commit
  `chore: apply spec-kitty upgrade changes (A -> B)` and the FR-023
  no-commit reasons. Worktree fan-out under `.worktrees/` is not ported
  (integrating lane/coordination worktrees are skipped upstream as well).
* `--plan-json` is compared up to `migrations`; `effects`/`dispositions`
  (upstream's per-asset ownership inventory) are emitted empty.
