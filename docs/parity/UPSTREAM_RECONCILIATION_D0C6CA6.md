# Official-main reconciliation: d0c6ca61

## Immutable source

Official `https://github.com/spec-kitty/spec-kitty` main advanced again during
continuation. Current captured commit:
`d0c6ca61d87f1d92512bb45386351aae3be1eba5`, tree
`d7192f896d5837de5ca526f1fab91dbfe56d9fe6`.
Detached reference: `/tmp/spec-kitty-parity-reference-d0c6ca6`.

The prior [4e7c538 inventory](upstream-main-4e7c538.json) and
[7d7cb16 inventory](upstream-main-7d7cb16.json) remain immutable historical
captures. [Current inventory](upstream-main.json) was regenerated and checked.
The user's Python checkout HEAD **and local main** remain
`bcb80f5230bd001ac8bebce25bb70788f9a32438`; object fetch/detached worktree creation
did not move them. Reference paths are conveniences, not authority identities.

Comparison `4e7c538..d0c6ca61`: **53 files, +3072/-110**. Much is accepted
architecture, governed mission artifacts, documentation and tests. Inventory
changes only commit/tree: API **1.15.0**, minimum provider **0.1.0**, **27 verbs**
and **62 root completion registrations** remain unchanged. No executable
conformance is inferred from that unchanged wire inventory.

## Landed behavioral deltas and named oracles

Paths below are relative to the immutable official reference. Oracle tests were
inspected, **not executed** as upstream behavioral acceptance in this continuation.

| Delta | Source | Upstream behavioral oracle / required Go decision |
|---|---|---|
| Preserve present unreadable TOML rather than wiping other tables | `src/specify_cli/core/toml_table.py`; `cli/commands/moments.py` | `tests/specify_cli/core/test_toml_table.py`, `tests/cli/commands/test_moments_command.py`. Missing files may start empty. Malformed/non-UTF-8/non-missing OS errors refuse writes; strict mode keeps the original error, non-strict raises `UnreadableTomlTableError`. Moments on/off exits 1 with the unchanged file named, no traceback. Administrative preference changes are not hosted consent. |
| Checkout-bound isolated environment | `scripts/dev/activate-isolated-env.sh` | `tests/scripts/test_activate_isolated_env.py`. Current Git worktree wins; script location is fallback only outside Git. Missing/non-executable checkout CLI refuses before activation; checkout switching restores prior environment then rebinds. `SPEC_KITTY_HOME`, `SPEC_KITTY_ISOLATED_ENV`, `VIRTUAL_ENV`, `PATH` follow activation/deactivation. Go setup/harness parity must preserve that isolation, not import Python tooling into production. |
| Runtime-classified command-skill regeneration, explicit outcomes and retired drift | `src/specify_cli/skills/command_installer.py`, `tool_surface/{repair,providers/command_skills}.py`, `cli/commands/{init,upgrade,agent/config}.py`, two `upgrade/migrations/m_3_2_0rc35_*` modules | `test_command_installer_derived_regeneration_702.py`, `test_upgrade_command_skill_drift.py`, `test_agent_config.py`, `test_init_idempotent.py`. Each batch consults runtime `STATE_SURFACES`: automatic regeneration requires DERIVED/IGNORED; otherwise exact-path consent. Regenerated paths are reported and counted once, not additionally added. Retired manifest-owned drift may be pruned under the same gate; unowned/user-edited/removal-drift paths are not blanket overwrite authority. |
| Mission-pack/lifecycle CI routing | `.github/workflows/ci-router.yml`, `scripts/ci/gate_selection.py` | `tests/architectural/test_gate_selection_authority.py`, `test_workflow_coherence.py`. Consumed subtrees fan out to actual consumer modules; README-only changes remain zero-module. This is CI coverage routing, not a new user capability or acceptance waiver. |

## Accepted architecture is not yet implemented behavior

The complete new ADR
`docs/adr/4.x/2026-10-13-2-the-event-ledger-is-the-publication-seam-default-on-with-local-retention.md`
was read. It specifies shared-package versioned ledger rows, cursor-based local
publication by default, hosted-only drain gating, and two-tier retention.
Current publication callbacks/moment producers still drain-gate; accepting the
ADR does not mean its implementation or confirmation tests shipped.

Committed records are never pruned: status/decision/mission ledgers,
`.kittify/canonical-events.jsonl`, decision records and `kitty-ops/<id>.jsonl`.
Local caches/run logs may receive explicit dry-run-first cleanup, preserving open,
locked and unfinished/resumable missions, with locked run-index removal. The
newest-20 / newer-than-30-days defaults remain proposals for operator confirmation.
`hosted.drain` stays off by default. `SPEC_KITTY_NO_MOMENT_HANDLERS` and
`SPEC_KITTY_SYNC_DISABLE` still gate the implemented moment registration path;
this architecture record is not permission to delete those checks in Go.

Go's immutable authority, local-first/no-mandatory-hosted behavior, explicit
compatibility translation, memory-primary projections, source-bound evidence
and admission gates remain unchanged. No new M3/M4/M5 admission, event schema,
retention command or frontend completion is declared by this reconciliation.

## Verification

Current d0c6ca61 and retained 4e7c538 capture `--check` pass; nine capture safety
unit tests pass. Source comparison and checkout identities were checked directly.
Latest-main claims remain bounded to this immutable check, not indefinitely to a
moving branch. The progress/proxy development corrections have their own evidence;
no upstream inventory or accepted ADR transfers qualification to them.
