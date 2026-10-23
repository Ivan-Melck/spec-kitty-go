# Irreducible differences review (wave 5, area FIX5)

Every "Irreducible differences" entry of the parity docs was re-checked
against the oracle (4.0.0rc8, CPython 3.13.15, PyYAML 6.0.3, ruamel.yaml
0.19.1, python-jsonschema 4.26, rich 15). An entry stays only when it needs
something a Go binary cannot have or that upstream itself does not fix:
site-packages paths, Python source frames or excerpts, CPython's
hash-randomised `set`/`frozenset` iteration over strings, the interpreter's
identity or version, wall-clock-dependent frame counts, or SQLite's internal
page layout. Deterministic Python text and behaviour were ported.

| Doc | Entry | Verdict | Reason / proof |
|---|---|---|---|
| closeout | refusal details quote the Go error text (`coordination surface could not be materialized`, `StoreError`, `GitProbeError`) | fixed | status-log reads raise CPython's `StoreError` / `JSONDecodeError` / `UnicodeDecodeError` / `OSError` text; the dependency gate reads the primary log through `read_wp_frontmatter`; unreadable coordination logs crash with upstream's chained traceback; probes use upstream's git argv. `TestDiffConsolidateUnreadableStatus`, `TestOracleGitProbeErrorText` |
| closeout | excluded commits listed sorted (frozenset of SHAs) | irreducible | `excluded_shas` is a `frozenset[str]`: per-process string-hash order |
| closeout | (found while fixing) review-artifact consistency gate and drift warning absent | fixed | `TestDiffConsolidateReviewArtifactGate`, `TestDiffConsolidateUnreadableStatus/coord-drift` |
| core | commit-guard hook names the Go binary | irreducible | interpreter identity / entry point |
| core | stale-check UserWarning records | partly fixed | FP-ceiling record logged (`TestDiffStaleCheck`); the `SyntaxError while parsing` record embeds CPython's parser diagnostic and a site-packages location |
| core | `sys.executable -m pytest` | irreducible | interpreter identity |
| core | `completeness_status must be one of {...}` | irreducible | `set` of strings: per-process hash order (verified with several `PYTHONHASHSEED`s) |
| core | built-in asset paths in prompts | irreducible | site-packages path |
| core | claim liveness on darwin Go-only tested | not a difference | coverage note, moved out of the list |
| core | `MissionSelectorAmbiguous` traceback frames | irreducible | site-packages frames and source excerpts |
| doctrine | DOC-20 built-in doctrine paths | irreducible | site-packages path |
| doctrine | DOC-05 fixture root | irreducible | site-packages-derived path |
| doctrine | DOC-06 `.failed/cause.yaml` traceback | irreducible | Python source frames |
| doctrine | warnings records name the site-packages file | irreducible | site-packages path (Go prints the relative module path) |
| doctrine | additionalProperties messages listing several keys | corrected | the `false` message is sorted upstream (`sorted(extras, key=str)`) and matches; only errors of a schema-valued `additionalProperties` follow `set` order (hash-randomised) |
| doctrine | `retrospective.proposal.rejected` order | irreducible | `set[str]` of proposal ids |
| maintenance | encoding-provenance pack path | irreducible | site-packages path |
| maintenance | doctor provenance built-in root | irreducible | site-packages path |
| maintenance | misconfigured `SPEC_KITTY_PACKS_ROOT` warnings | moved to Remaining work | location/excerpt are site-packages-specific, but the message is deterministic; the per-command emission sequence is not modelled |
| maintenance | rebaseline `rglob` order | fixed | CPython 3.13 `Path.rglob` order (`migrate.PyRglobAll`). `TestDiffMigrateRebaselineRglobOrder`, `TestOraclePyRglobAll` |
| maintenance | uncaught traceback frames | irreducible | site-packages frames and source excerpts |
| orchestrator | artifact-submit stale-parent order | irreducible | `required | parents.keys()` is a `set[str]` |
| owned | seam exception traceback frames | irreducible | site-packages frames and source excerpts |
| owned | masked values (uuid4, correlation ids, tokens, cache keys) | not a difference | random per run in both, or sandbox-path dependent |
| owned | `issue_counts` key order | irreducible | `frozenset` of strings |
| owned | claim-lock sidecar pid / `MainThread` | not a difference | process identity; thread name matches |
| planning | template-selection digests | irreducible | site-packages path hashed |
| planning | `coupling:` line order | irreducible | `set[str]` of module paths |
| planning | `issue_counts` key order | irreducible | `frozenset` of strings |
| setup | freshness-lock `template_source_signature` | irreducible | hashes upstream's Python modules and install paths |
| setup | `environment.python_version` | irreducible | interpreter version |
| setup | agent config sync orphan order | irreducible | `set[str]` |
| setup | `doctor bytecode` / `regen` | irreducible | interpreter install (`.pyc`) / upstream source checkout |
| setup | install method | irreducible | interpreter identity |
| setup | `init` live auto-refresh frames | irreducible | wall-clock-dependent frame count |
| tracker | traceback frames | irreducible | site-packages frames and source excerpts |
| tracker | ruamel `_ref` referers on delete | not a difference | no tracker command deletes a merged key |
| tracker | SQLite page layout | irreducible | SQLite internal page layout |
| util | traceback frames | irreducible | site-packages frames and source excerpts |
| util | workflow export / plugin build warning paths | irreducible | site-packages path |
| util | YAML `ReaderError` beyond 8 KiB | fixed | lazy chunked reader (binary 4096-byte chunks, TextIOWrapper 8192-byte decoding). `TestOracleStreamReaderChunks` |
| util | recursive YAML aliases | fixed | `recursive_objects` semantics (ruamel deep → None, PyYAML error, cycles otherwise), `pyjson.Repr` `[...]`/`{...}`. `TestOracleRecursiveAliases` |
| util | OSC 8 link ids | irreducible | `count(getrandbits(24))`: random per process |
| util | colour note | not a difference | moved out of the list |
| help | none | — | — |

Found while checking, not on any list (see the docs' Remaining work):
`next` / `agent action implement` / the status board do not run
`WPMetadata` validation on WP frontmatter (a non-string list item such as a
recursive alias that ruamel resolves to None is refused upstream); the
status board reads frontmatter scalars leniently upstream
(`extract_scalar`), so an invalid-YAML frontmatter still renders there.
