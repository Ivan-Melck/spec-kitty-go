# Area B parity: project setup & agent integration

Upstream: `spec-kitty-cli` 4.0.0rc8. Packages: `internal/kittify`,
`internal/agents`, `internal/bootstrap`, `internal/toolsurface`,
`internal/commands/setup` (registered via `cmd/spec-kitty/imports_setup.go`).

Differential tests run with `SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty`;
every `*WithoutOracle` test and every test that only drives the Go binary
runs in plain `go test ./...`. The Go binary never reaches pypi.org under
test: `testkit` points its PyPI lookups at a closed local port (see
`SPEC_KITTY_GO_PYPI_JSON_URL` below) unless a test serves a fake index.

## Commands

| Command path | Status | Tests (internal/commands/setup) | Known deviations |
|---|---|---|---|
| *(root callback)* startup bootstrap | done | `bootstrap_test.go`, all oracle tests compare `$HOME` trees | Freshness-lock signature (irreducible, see below). |
| *(root callback)* schema gate + upgrade nag | done | `TestUpgradeStaleAndLegacyMatchOracle`, `TestUpgradeWithoutOracle`, `TestUpgradeCLIQueriesFakeIndex`, `TestNagColourMatchesOracle` (PTY), `TestSafetyModes` | |
| *(startup)* `.kitty.env` operator env loading | done | `TestDoctorSetupLeavesMatchOracle`, `TestKittyEnvHomeLineWarnsLikeOracle` | The `UserWarning` names `<spec-kitty>/specify_cli/__init__.py:34` instead of the site-packages path. |
| `init` | done | `TestInitMatchesOracle`, `TestInitErrorsMatchOracle`, `TestInitNamedDirAndNoGit`, `TestInitWithoutOracle`, `TestInitArrowKeyPicker*` and `TestInitColourOutputMatchesOracle` (PTY, SGR compared) | `metadata.yaml` `python_version` and the transient progress frames (irreducible, below). With a non-terminal stdin and `SPEC_KITTY_FORCE_INTERACTIVE` a line prompt is used (upstream's readchar crashes there). |
| `agent config` (group + list/add/remove/status/sync/set) | done | `TestAgentConfigMatchesOracle`, `TestAgentConfigOrphansMatchOracle`, `TestAgentConfigWithoutOracle` | `sync` orphan scan order (irreducible). |
| `verify-setup` | done | `TestVerifySetupMatchesOracle` | |
| `config` | done | `TestConfigAndSessionsMatchOracle`, `TestConfigShowOriginOrgPackTier` | `--show-origin` package-default paths name embedded assets (`packs/built-in/...`). |
| `session-start` / `session-stop` | done | `TestConfigAndSessionsMatchOracle` | Session upgrade cache (`~/.kittify/last-cli-check.json`) refreshes in a re-exec of the Go binary instead of `python -m`. |
| `upgrade` | done | see "Upgrade" | |
| `doctor` (group), `command-files`, `state-roots`, `env-file`, `channel` | done | `TestDoctorSetupLeavesMatchOracle`, `TestDoctorColourTablesMatchOracle` (PTY, 80 columns, SGR compared) | |
| `doctor skills` | done | `TestDoctorSetupLeavesMatchOracle`, `TestPackSkillsMatchOracle` | |
| `doctor tool-surfaces` | done | `TestDoctorToolSurfacesMatchOracle`, `TestToolSurfaceFixMatchesOracle` | |
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
  (`compat_latest_test.go`); install method is always `unknown`
  (irreducible, below). `compat.safety_modes` is ported: doctor
  subcommands and `orchestrator-api` verbs are classified by their
  mutating flags, so read-only doctor leaves nag and pass the schema gate.
* **Tool-surface engine** (`internal/toolsurface`): the owner assessments
  of `upgrade/assessment.py` and `tool_surface/*` — `global_assets`,
  `managed_skills` (+ skills manifest, backups, prune), `command_skills`,
  `provisioning`, `agent_profiles`, `native_config`, `session_presence`,
  `plugin_bundle` — produce coalesced `PhysicalEffect`s (upstream effect
  ids), dispositions, diagnostics and rechecks. `--plan-json` emits the
  full `effects` / `dispositions` / `diagnostics` / roots / artifacts;
  `--dry-run` counts them and flags incomplete previews; the real run
  applies the prepared owner batches with upstream rechecks (input-root
  changes become `surface_repair_failed`), `repair_preparation_failed`,
  `drift_unresolved` (exit 1) and absolute repaired/regenerated paths.
  Tested by `TestUpgradePlanEffectsMatchOracle` (two agent mixes, fresh and
  drifted, `--plan-json` / `--dry-run` / `--json` / commit / trees) and
  `TestUpgradeCurrentProjectMatchesOracle` (full plan comparison).
* **Pack skills** (MAINT-11): org/project charter-pack skill catalogs
  (`org-charter.yaml` `required_skills` / `skill_namespace`,
  `activated_skills`), rendered `<ns>-<id>` skills with upstream
  `source_hash`, staging under `.kittify/runtime/pack-skills`, drift /
  stale / orphaned / missing / unresolvable findings, `doctor skills --fix`
  projection and the graphless-org-pack rich WARNING lines.
* **config.yaml writers** keep comments and styles (rtyaml round trip):
  provisioning (`mission_type_activations`), charter seeding and the
  identity writer (`TestCommentedConfigMatchesOracle`).

## Library API added in wave 5

* `toolsurface` (new): effect model — `FileState` (`Absent`, `FileOf`,
  `DirOf`), `Root`, `Proof`, `Action`, `Effect` (`NewEffect`, `ID`,
  `Destination`), `Coalesce` / `MustCoalesce`, `Phases`, `Disposition`
  (`NewDisposition`), `Diagnostic` (`Diag`), `Artifact`, `Assessment`
  (`Incomplete`, `HasErrors`, `EffectIDs`), `ApplyResult`, `Digest`;
  owners — `AssessGlobalAssets`, `CatalogSkill` / `BuiltinCatalog` /
  `ProjectSkillCatalog`, `AssessProjectSkills` (`SkillAssessOptions`),
  `AssessSkillInstallation` / `ApplyProjectSkills`, `PrepareCommands` /
  `ApplyCommands` (`CommandOptions`, `CommandEntry`, `LoadCommandManifest`,
  `SerializeCommandManifest`), `VerifyCommandSkills`, `PrepareProvisioning`
  (`Provisioning.Effects`, `IsConfigError`), `LocalPresenceContent`;
  surfaces — `RunToolSurfaces` (`Outcome`, `Report`, `Finding`, `Status`,
  `RepairResult`), `CountSurfaces`, `AllKinds`, `KindTokens`,
  `KindFromToken`; upgrade — `PrepareUpgradeRepairs` → `UpgradeRepairs`
  (`Owners`, `Complete`, `Effects`, `Plan*JSON`, `PreservedCount`,
  `ErrorMessages`, `ApplyProvisioning`, `Apply` → `UpgradeApplyReport`);
  pack skills — `PreparePackSkills`, `FindPackSkillFindings`
  (`PackSkillFinding`), `ProjectPackSkills`, `PackSkillCatalogError`;
  init drift policy — `RunSurfaceRepair` (`DriftPolicySummary`);
  `LogWriter` (rich WARNING sink, set by commands in non-JSON modes).
* `bootstrap`: `Family` / `FamilyEffect`, `AssessRuntimeFamily`,
  `AssessCommandsFamily`, `AssessSkillsFamily`, `EnsureSkillsSelection`,
  `ApplySelectedFamilies`; prepared writes carry ownership proofs.
* `agents`: `RenderSkillsManifest`, `SkillEntry.Provenance` /
  `WithProvenance`, `SameEntry`; `VibeSkillPathConfigured` /
  `PrepareVibeSkillPath` / `EnsureVibeSkillPath` read `.vibe/config.toml`
  with `pytoml` (MAINT-15, `TestVibeSkillPathMatchesUpstream`).
* `kittify`: `RenderConfigUpdate` (comment-preserving config render);
  `EnsureIdentity` keeps comments.
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
* Additive extensions in wave 5 TSE: `rich.Segment`, `rich.RenderSegments`,
  `rich.RenderStyled`, `rich.ColorSystemOf`, `rich.StyledPanelLines`
  (`PanelSpec`), `rich.StyledMarkupLines` (`MarkupLine`) — styled
  segments / panels / markup lines under the detected colour system (used
  by the init picker, banner, panels, step tree); `rich.PaddedTable`
  (rich `Table(box=None, padding=(0, 2), show_edge=False)`: min widths,
  collapse, ellipsis, crop, markup cells, styles — the doctor tables);
  `rich.PrintNoMarkup` (`console.print(markup=False)`, highlighted on a
  TTY); `missionstate.WriteRefusal` is called (not changed) for the
  foreign-checkout `--fix` refusal.
* Changes in area DOC packages for skill reprojection: `charter activate` /
  `charter deactivate` (`internal/commands/doctrine/charter_activate.go`)
  end with `reproject_pack_skills` (`toolsurface.ReprojectPackSkills`:
  `Skill file synced` / `Skill file preserved` lines, refusal exit 1);
  `charter.Activate` materializes an absent `activated_skills` from the org
  packs' `required_skills` (new `charter.OrgRequiredIDs`, PackContext
  absent-key default); `charter.LoadOrgDRGLenient` skips a fragment-less
  pack before `load_org_pack` (no spurious `PACK_LEVEL_MISSING`
  UserWarning). Test: `TestCharterSkillReprojectionMatchesOracle`.

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
* Install-method detection: upstream inspects the Python interpreter
  (`sys.executable` under uv/pipx/Homebrew prefixes, a source checkout,
  the distribution's `INSTALLER` record). A standalone Go binary has none
  of these, so `install_method` is always `unknown` — the same answer
  upstream gives for an install outside those channels (the oracle venv
  install also reports `unknown`).
* `init` progress tree on a TTY: upstream drives a rich `Live` display
  whose transient redraw frames depend on wall-clock refresh timing (8/s)
  and step durations; Go prints only the final coloured tree, so the
  visible end state matches (`TestInitColourOutputMatchesOracle` compares
  everything before and after the transient region).

## Remaining work

* None from the wave-5 sweep. `kittify.LoadAgentConfig` now logs upstream's
  `WARNING  Config file not found: <root>/.kittify/config.yaml` per call
  (sink `kittify.WarnLog`, installed by the CLI logging bootstrap in human
  mode); `session-start` loads the agent config like upstream and the skill
  installer's re-load is accounted with `kittify.AgentConfigMissingWarning`.
  Counts and text match the oracle for `agent config list/status/sync/add`,
  `session-start`, `doctor command-files/tool-surfaces/skills`, `upgrade
  --dry-run` (`kittify.TestDiffAgentConfigMissingWarnings`).
