# Official-main reconciliation: 4e7c538
Status: **source reconciliation only; full Go workflow/CLI/API parity remains absent**.
This adds requirements/oracles to [LATEST_MAIN](LATEST_MAIN.md), not implementation,
mission admission, transferred qualification, or a compatibility facade.

## Immutable reference and inspected scope
Official repository: `https://github.com/spec-kitty/spec-kitty`.
The supplied newest-main recheck is pinned, not an indefinitely moving `main`:

| Binding | Original | Rechecked |
|---|---|---|
| Commit | `7d7cb16dc65fa3a8fd3900aadbec76c5e2cd2b3f` | `4e7c538d3d1f21044b127eccd49061a4cb9f451c` |
| Tree | `61974083e507b6b652eedd5a20ceb21fa91289fa` | `442baca5832fdfa72c4b156be3bfa30a9eb2fc27` |
| Inventory | [retained snapshot](upstream-main-7d7cb16.json) | [current snapshot](upstream-main.json) |

Local detached checkouts `/tmp/spec-kitty-parity-reference` and
`/tmp/spec-kitty-parity-reference-4e7c538` are conveniences, not source identity.
Git-object diff with replacements disabled: **135 files, +8415/-134**.
Inspected the complete name/stat census; all three production-file diffs below;
new routing/regeneration tests and changed consumer/installer/upgrade assertions;
both new CI scripts and their reader, architectural, evidence and job tests;
acceptance-jvm/nightly/mission-status diffs and related scenario/upgrade docs.
Mission archaeology was not exhaustively read; mission records, archive exceptions,
CODEOWNERS and timing/registry changes are not product-delivery evidence.
All upstream paths below are relative to the rechecked commit/tree.

## Surface inventory: no delta, not behavioral equivalence
Both pinned captures passed `scripts/parity/capture_upstream.py --check` locally.
JSON comparison changes **only** `upstream_commit` and `upstream_tree`:
API **1.15.0**, minimum provider **0.1.0**, **27** allowed verbs, forbidden verbs,
error vocabulary, **62** root completion registrations/hidden/deprecated flags,
and all **11** inventoried source SHA-256 values are identical.
Direct Git diff also finds no changes in `src/specify_cli/orchestrator_api/`,
`core/upstream_contract.json`, `_completion_manifest.json` or `__init__.py`
(the latter three under `src/specify_cli/`). `orchestrator_api/envelope.py` retains
`contract_version`, `command`, `timestamp`, `correlation_id`, `success`, `error_code`, `data`.
This verifies the captured API/verb/envelope/root-manifest surface only: help,
all nested CLI behavior, delegates and executable conformance are not proved equal.
The production changes below are outside the capture's source-hash coverage.

## Production requirements added or clarified
### R1 — One declaration-first command target, not ambient write authority
`src/specify_cli/core/command_target.py` adds frozen `CommandRepoTarget` with
canonical primary `repo_root`, `target_branch`, and `DECLARATION` versus
`VALIDATED_FALLBACK` provenance. Validate the slug as one safe segment; use the
existing read-path canonicalizer for mission handles/metadata. Collapse worktrees
to the primary checkout; explicit `repo_root` takes precedence over `cwd`, whose
default is read once at this boundary. A primary mission declaration beats HEAD.
Delegate branch selection to existing placement authority, not a second resolver.
Absent metadata/declaration uses that authority's fallback: with `origin/HEAD` it
can select `main`; without it, it can still select checked-out HEAD. Non-ambient
fallback is deferred upstream #6152, not delivered here. Corrupt metadata/traversal
refuse; missing Git marker refuses on the discovered-`cwd` path, while an explicit
non-Git root retains the existing resolver's permissive collapse behavior.
Worktree-preferring status READ roots remain separate; this entry does not publish,
integrate, grant a write capability, or route every mutating command.

`src/specify_cli/cli/commands/agent/mission_record_analysis.py` routes both report-only
and full-analysis target selection through this entry when no owned checkout exists;
`owned.write_branch` still wins. The branch still delegates to the previous authority:
this is centralized routing/provenance, not a newly universal branch-selection policy.
Require report target = placement recheck target; the undeclared `release-2`/no-remote
regression must commit only `analysis-report.md`, not refuse with placement changed.

Oracles: `tests/specify_cli/core/test_command_target.py`,
`test_command_target_behaviour.py`, `test_command_target_placement_parity.py` in that directory;
`tests/specify_cli/cli/test_command_target_routing.py` (source ratchet, not execution);
`tests/specify_cli/cli/commands/agent/test_record_analysis_undeclared_target.py::test_report_only_hands_the_transaction_the_placement_target`
(real resolver/transaction); `test_planning_commit_outcome_consumers.py` alongside it.

### R2 — Manifest-owned DERIVED skill drift regenerates
`src/specify_cli/skills/command_installer.py` passes `adopt_only` into preservation.
Normal reconcile regenerates drifted, manifest-owned regular command-skill files
`.agents/skills/spec-kitty.<command>/SKILL.md` to canonical bytes without overwrite
consent, refreshing manifest hash. Even hand edits on this DERIVED/git-IGNORED
projection are replaced; cross-checkout manifest/ignored-byte disagreement is not
protected customization. Canonical bytes remain unchanged, including mtime on repeat.
Adopt-only remains byte-nonwriting and reports edited owned bytes as consent-required;
unowned noncanonical collisions and third-party skills stay protected. Batch preflight
must refuse collisions before any repair; preview/apply and path safety remain active.

Oracles: `tests/specify_cli/skills/test_command_installer_derived_regeneration_702.py`
(install, provider reconcile, no-op, third-party, collision controls);
`tests/specify_cli/skills/test_command_installer.py` (including adopt-only tests);
`tests/specify_cli/cli/commands/test_upgrade_command_skill_drift.py::test_real_drift_is_regenerated_by_unattended_upgrade`
checks actual `upgrade --yes` human/JSON exits, bytes, hash and absent drift banner.

## Development qualification oracle, not M4 product acceptance
`scripts/ci/scenario_contract.py` statically checks nonempty Gherkin/scenarios/steps,
outline rows/backgrounds/inherited tags and exactly one Java binding per gating step.
Promise IDs are shape-only references, not resolved promises; WIP requires owner/date;
WIP/manual binding gaps are informational. Unsupported expressions fail; this subset
reader is not the full Cucumber engine or an execution/acceptance decision service.
Oracles: `tests/architectural/test_scenario_contract.py` plus its broken fixtures,
`tests/ci/test_scenario_contract_reader.py` (empty census, matching and parser controls).

`scripts/ci/acceptance_evidence.py` emits schema-v1 per-OS evidence from outermost
JUnit counters and run metadata. Missing XML, malformed counters/XML, failures/errors,
zero/all-skipped gating execution or a non-success gating CI step cannot be green;
WIP is non-gating. Oracles: `tests/ci/test_acceptance_evidence.py` and
`tests/ci/test_acceptance_bdd_job.py`. `acceptance-jvm/build.gradle` isolates gating/WIP/
adhoc reports; refuses unknown pass or simultaneous pass/tag selectors; only WIP may
execute nothing. Nightly retains no-retry gating, always-written/uploaded evidence,
strict missing-artifact failure and 30-day retention; these are repository CI laws.
Java `environment/{MissionFixture,RunMetadata}.java` and `steps/RunHooks.java` build
fixtures through CLI and stamp evidence. `tests/scenarios/mission/mission-status.feature`
adds lookup, same-project isolation and missing-mission refusal witnesses, not new
status implementation, WP-lane coverage or a completed planning/execution journey.

## Go dispositions, conflicts and dependency gates
Go has no corresponding public analysis/declaration-router or command-skill installer;
`internal/helpercmd/runner.go` exposes native initialize/status/progress/operation/drive,
not upstream init/upgrade/status-by-name/API 1.15. Native mission-scoped operation
membership/inspection is related isolation evidence, not CLI lookup parity.
`internal/integration/acceptance_authority_v2.go` has candidate/evidence/waiver authority;
it is neither these CI tools nor the complete M4 cross-provider claim runtime.
Translate observable R1/R2 outcomes only through future admitted consumer-owned ports:
mutable `meta.json`, HEAD, paths, manifests and generated bytes must never become Go
journal authority. Preserve quarantined/fresh workspaces and independent review.
Record compatibility choices for ambient fallback, permissive explicit roots and
replacement of generated edits; do not import Python, CI/JVM tooling or legacy state
into production, or relax origin-freshness/network conflicts already recorded.
[Production program](../PRODUCTION_RUNTIME_PROGRAM.md) remains binding: exact M1/M2
adopted; **M3/M4/M5 proposed**, remaining immutable entry decisions required. M3 owns
real execution/Git/workspace composition; M4 neutral providers; M5 requires accepted
M2 + M3 + M4 before the API/native-CLI join. Reconcile affected dependency bindings
explicitly; no source update transfers frozen evidence or admits a successor.
Verification here: source/tree and inventory checks only; upstream/Go behavioral and
JVM campaigns were not run. No upstream writes, network, installs or facades introduced.
