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
| Activation ledger | in progress | Shared source precedence, strict ledger validation, legacy upgrade scope, and plain stderr diagnostics. Deny semantics, migration, write guards and health reporting still need completion. |
| Migration ledger and upgrade | pending | Manifest order, tracked/clone-local partitions, init seeding, adoption/backfill, repository gates and new migrations. |
| Agent and skill delivery | pending | Hyphenated command skills, conformant frontmatter, configured-agent global commands, Antigravity retirement and narrow gitignore rules. |
| Charter and languages | pending | Language settlement, synthesis soundness gate, context/identity changes, pack vendor and updated freshness behavior. |
| Runtime and owned checkout | pending | Review-lock validation, owned issue-verdict and acceptance changes, exact mission lookup and degraded startup handling. |
| Consolidation | pending | Canceled contribution attribution, binary/mode/rename cases and review evidence changes. |
| Full verification | in progress | Initial differential campaign against the new oracle; rerun affected packages after fixes, then the complete suite and help/colour matrix. |

The isolated reference for this update is
`$HOME/.cache/spec-kitty-parity/rc8-ae97982033`. Run the oracle with an isolated
`HOME` and `SPEC_KITTY_NO_UPGRADE_CHECK=1`, as described in
[the porting guide](../PORTING.md).
