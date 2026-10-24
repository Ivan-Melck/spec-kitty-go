# Current RC8 parity target

The selected target is upstream `main` at
`ae979820332ee259eba467d03a00e8bb50196237`, reporting the unreleased
`4.0.0rc8`. The latest published/tagged release candidate at selection time
was `4.0.0rc7`; this port advances the existing main-based RC8 port from
`dccf199f7a42fd91556610163c989794d210eac9`.

Implementation and focused comparisons are complete for the areas below.
Full-suite verification is still running; completed parity is not yet claimed.

The reference contains 279 command paths (previously 276). New commands are
`charter pack vendor`, `migrate ledger`, and `migrate activation-ledger`.
Five existing command definitions changed. The source comparison covers
285 changed paths, including refactors checked for observable effects.

The built-in pack is the external open-packs snapshot
`af1c370f34601f2aa00d45a7a9597ab8d6a46ea1`. Assets come from the wheel after
its build hook verifies that pin. All 724 embedded assets match an independently
rebuilt reference wheel byte for byte.

| Area | Implemented behavior and focused evidence |
|---|---|
| Mission capability | `requires_substantive_spec`, explicit false and built-in fallback; mission creation reads the activation authority. |
| Activation | Tracked allow/deny ledger, cross-layer identity, organization-required floor, guarded writes, migration, presets, cascade barriers, diagnostics and bundle freshness. Activation, context, collision and consistency oracle scenarios pass. |
| Migration and upgrade | Ordered 131-entry manifest, tracked/clone-local records, read-only report, settlement, init seeding and adoption. All new RC8 migrations are present. Legacy upgrade corpora, already-current projects and worktree fan-out pass focused comparisons. |
| Agent delivery | Hyphenated command skills, shared/native frontmatter, configured-agent global commands, freshness, Antigravity retirement, narrow ignore rules and ownership-aware legacy retirement. Init matrices, configuration, repair and exported plugin bundles pass comparisons. |
| Global assets | Full-batch recheck under the owner lock, source-role retention, environment/membership/identity checks and bounded rebuild after a destination changes during preparation. Ten recheck scenarios, including a filesystem-root anchor, match the oracle; concurrent cold-start issue writers pass. |
| Charter and languages | Language catalog, configured-language authority, polyglot/JVM detection, interview settlement, synthesis soundness, reach-only edges and creation records. Context 1.4.0, declared identities, denial consumers, optional activation-ledger bundle reporting and authority paths pass integration comparisons. |
| Pack vendor | Offline digest/compatibility checks and scratch fetches that update the pin without replacing the live tree. Local-repository matrix and 75 PEP 440 specifier cases pass. |
| Runtime and owned checkout | Strict review locks, preclaim refusals, owned issue-verdict/acceptance, exact mission lookup, degraded startup, single-branch evidence and profile denials. Concurrent sibling-row preservation, remote-only refusal, owned planning and analysis freshness pass. |
| Bulk edit | Occurrence-map readiness, finalization, implementation inference and review-diff gates. Sixteen readiness cases, 1,800 path/policy/status cases, git-history/ref cases, rendering and CLI refusal scenarios pass. |
| Consolidation and placement | Canceled-contribution attribution, rename/binary/mode/Unicode cases, known coordination-kind projection, single-branch evidence and explicit research/documentation artifact kinds. Fourteen attribution histories, evidence cases and classifier comparisons pass. |
| Glossary events | Pinned event-contract validation before writes, resolution-batch prevalidation, provenance timestamps and locked torn-tail quarantine. All 408 validation cases and five interactive resolution/write/refusal/recovery scenarios match RC8. |
| Governance and diagnostics | Primary-tracking-aware Op commits, tracked team settings preservation, orientation, diagnostic roots, guarded agent configuration and current event-contract version. Focused Op, health, malformed-file and warning comparisons pass. |

The Go executable has no Python runtime dependency. Python is used only to
build the development oracle and compare behavior. Differential fixtures
normalize random IDs, timestamps, local paths and runtime environment metadata.
Renderer freshness signatures include implementation code and its installation
path, so legacy-project upgrade fixtures warm each executable's own global
cache before comparing project changes.

## Reference and verification

The isolated reference is `$HOME/.cache/spec-kitty-parity/rc8-ae97982033`.
Use an isolated `HOME` and `SPEC_KITTY_NO_UPGRADE_CHECK=1`, as described in
[the porting guide](../PORTING.md). The reference setup script has been run
end to end, including rebuilding its wheel and comparing assets. Optional
reference-agent and Python autorebase checks also pass.

All 279 plain help snapshots were captured with
`scripts/capture-reference-help.py` in the subprocess environment used by the
parity test. A `CliRunner` capture wraps long usage lines differently.
The full colour and usage-error matrices are included in the broad campaign.

The complete updated campaign uses immutable source at
`/tmp/spec-kitty-rc8-final-source` and executable
`/tmp/spec-kitty-rc8-final-bin`, with bounded package/test concurrency.
It follows an earlier immutable survey used to identify gaps. Follow-up
artifact-classification and glossary-ledger changes are verified separately
and must be included in final acceptance.

Glossary producer model and JSON Schema data can be reproduced with the
reference interpreter:

```sh
$REF/oracle-venv/bin/python scripts/sync-event-contracts.py --check
```

Full differential completion, the post-change native suite and final vet
results remain to be recorded here before declaring parity.
