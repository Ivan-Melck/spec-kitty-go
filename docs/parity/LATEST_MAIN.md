# Current RC8 parity target

The target selected on 2026-10-23 is upstream `main` at
`ae979820332ee259eba467d03a00e8bb50196237` (version `4.0.0rc8`, not yet a
published/tagged release). The latest published/tagged release candidate at
selection time was `4.0.0rc7`; this port intentionally advances to main's RC8.
The previous reference was `dccf199f7a42fd91556610163c989794d210eac9`.

This document tracks the update; it does not claim completed parity.

The updated reference has 279 command paths (previously 276). New commands
are `charter pack vendor`, `migrate ledger`, and `migrate activation-ledger`.
Five existing command definitions also changed. Across the source packages,
285 paths changed; source-only refactors are checked for observable effects
before any port is needed.

The exact built-in pack is upstream's pinned external open-packs snapshot
`af1c370f34601f2aa00d45a7a9597ab8d6a46ea1`. Assets are now taken from the
wheel after its build hook verifies that pin, rather than from git-tracked
files (the pack is no longer tracked in the core repository).

| Area | State | Work |
|---|---|---|
| Reference and assets | refreshed | Separate wheel-installed oracle and 279-path help inventory; old reference retained. Asset/reference scripts updated for fetched packs and exact revision selection. |
| Mission capability | implemented; broader verification pending | `requires_substantive_spec` schema and prerequisite resolution, including explicit false and built-in fallback. |
| Activation ledger | implemented; broader verification ongoing | Source precedence, identity-aware exclusions, organization-required floor, guarded writes, migration CLI, presets, cascade barriers, list and health diagnostics, and bundle freshness. Basic migration, activation, preset, health, list and cascade differential scenarios pass; remaining consumers and edge cases are being audited. |
| Migration ledger and upgrade | in progress | Manifest order, tracked/clone-local persistence, read-only report, success-only settlement, init seeding, adoption/backfill and repository gates implemented. Report oracle scenarios and foundational unit checks pass. All new RC8 migration implementations are present, including project-intent rename and ownership-proven stale-template repair. Complete runner/worktree differentials remain. |
| Agent and skill delivery | implemented; broad verification ongoing | Hyphenated command skills, shared/native frontmatter projection, configured-agent global commands and scoped freshness, Antigravity retirement, narrow gitignore rules, ownership-aware legacy retirement and collision diagnostics. Init agent matrix, eight migration fixtures (dry/real plus trees), six diagnostic/repair fixtures and native ownership/race checks pass. |
| Charter and languages | implemented in part; verification ongoing | Language catalog, configured-language authority, observed polyglot/JVM detection, interview settlement and narrowing, synthesis soundness gate, project-intent rename, reach-only edges/roots, creation records and activation memory. Pipeline, language, interview, soundness, migration and activation-memory oracle scenarios pass. Pack vendor and remaining context/identity edges are still pending. |
| Runtime and owned checkout | pending | Review-lock validation, owned issue-verdict and acceptance changes, exact mission lookup and degraded startup handling. |
| Consolidation | pending | Canceled contribution attribution, binary/mode/rename cases and review evidence changes. |
| Full verification | in progress | Initial differential campaign against the new oracle; rerun affected packages after fixes, then the complete suite and help/colour matrix. |

The isolated reference for this update is
`$HOME/.cache/spec-kitty-parity/rc8-ae97982033`. Run the oracle with an isolated
`HOME` and `SPEC_KITTY_NO_UPGRADE_CHECK=1`, as described in
[the porting guide](../PORTING.md).

The first broad oracle survey was stopped after collecting failures because
some test packages built the executable while source edits were in progress.
It is a gap inventory, not final verification. Future broad runs use a fixed
`SPEC_KITTY_GO_BIN` snapshot. Native checks found two legacy metadata test
expectations (updated for ledger authority) and the outstanding help changes.

A later survey runs from an immutable source copy and binary, with bounded
package/test concurrency. Its early mission-create failures exposed a direct
legacy config reader; the implementation now uses the activation seam, and
focused documentation/unknown-type mission-create scenarios pass. The survey
is still in progress and is not a final acceptance run.

The shell-invoked oracle wraps long usage lines differently from a
`CliRunner(terminal_width=...)` capture. The help snapshots are being recaptured
with `scripts/capture-reference-help.py` in the same subprocess environment as
the parity test.
