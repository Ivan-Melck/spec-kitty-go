---
work_package_id: WP16
title: Structural SQLite Eradication and Absence Proof
dependencies:
- WP14
- WP15
requirement_refs:
- FR-026
- FR-027
- FR-028
- FR-029
- FR-030
- FR-031
- FR-032
- FR-033
- FR-035
- NFR-014
- NFR-017
- C-004
- C-006
- C-007
- C-008
- C-017
- C-018
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T080
- T081
- T082
- T083
- T084
- T085
history: []
agent_profile: randy-reducer
authoritative_surface: internal/controlstore/sqlite/
create_intent:
- internal/architecture/m2_absence_test.go
- internal/architecture/m2_absence_mutants_test.go
- scripts/m2-absence/inspect.sh
- scripts/m2-absence/mutants.sh
execution_mode: code_change
model: ''
owned_files:
- internal/controlstore/sqlite/**
- internal/architecture/m2_absence_test.go
- internal/architecture/m2_absence_mutants_test.go
- scripts/m2-absence/inspect.sh
- scripts/m2-absence/mutants.sh
- go.mod
- go.sum
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP16 - Structural SQLite Eradication and Absence Proof

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the
frontmatter, and behave according to its guidance before parsing the rest of
this prompt.

- **Profile**: `randy-reducer`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If profile resolution fails, run `spec-kitty agent profile list`, resolve the
closest semantic-compression implementation profile, and record that governed
profile invocation before editing production code.

## Objective

Delete the fully replaced SQLite implementation and prove that no production or
test authority path can select, load, import, invoke, copy, generate, or recover
it. Remove its driver and exclusive dependency closure, add layered source and
artifact absence gates with killing mutants, and verify the exact candidate from
a clean checkout without rewriting immutable historical evidence.

Implementation command:

```bash
spec-kitty agent action implement WP16 --agent codex
```

## Branch, dependency, and ownership rules

- Start from the accepted WP14 and WP15 revisions assigned to WP16 in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- The planning and merge target is `feat/gapdb-only-production-authority`.
- Use only the Spec Kitty-created lane worktree; do not create one manually.
- Edit or delete only `internal/controlstore/sqlite/**`,
  `internal/architecture/m2_absence_test.go`,
  `internal/architecture/m2_absence_mutants_test.go`, `scripts/m2-absence/**`,
  `go.mod`, and `go.sum`.
- Preserve other agents' concurrent edits and never revert unrelated changes.
- Do not delete SQLite until WP14 behavior parity and WP15 historical-state
  diagnosis prove every production consumer has a supported replacement.
- Do not fix remaining imports outside owned files. Report them as a blocking
  replacement gap to the owning WP; deletion must not be forced past one.
- Never add a production or test-only SQLite driver, fallback, compatibility
  mode, importer, exporter, comparison read, dual write, or backend selector.

Before editing, read `spec.md`, `plan.md`, `research.md`, `data-model.md`,
`quickstart.md`, all contracts, the WP14 behavior census, WP15 historical-state
contracts, and current source/module/build/install surfaces. Historical SQLite
mentions in immutable program evidence are context, not executable permission.

## Eradication laws

1. Structural absence means unreachable and absent across source, generated and
   tagged code, dependency graphs, binaries, SBOM, installation, help,
   configuration, fallback, migration, subprocess, and runtime surfaces.
2. Disabled code, an unused module, a test-only driver, or a default-off backend
   selector violates the requirement even when normal execution uses GapDB.
3. Historical roots receive a typed offline disposition before any current
   mutation; production never reads, opens, imports, converts, or repairs them.
4. Historical source/evidence remains immutable. Path-scoped documentation
   exemptions permit truthful references, never executable source or artifacts.
5. The absence gate reasons about reachability as well as lexical tokens so
   aliases, wrappers, reflection, generated files, build tags, copied code, and
   subprocesses cannot evade it.
6. Every checked forbidden surface has at least one positive killing mutant.
7. Evidence is source-bound: source tree, module graph, binaries, SBOM, install,
   help/config, runtime probes, checks, and mutants name one exact candidate.
8. Failure of any M2 absence gate rejects the unshipped composition; rollback
   never reactivates SQLite as a fallback.

## Required deletion preflight

- Enumerate every current SQLite file, import, symbol user, schema/migration,
  module, transitive dependency, test fixture, helper, binary surface, build
  rule, generated edge, tag, reflection string, subprocess, configuration key,
  and historical documentation occurrence before deletion.
- Classify each occurrence as executable/reachable, dependency/artifact,
  historical evidence, or unrelated lexical text with a documented rule.
- Require accepted WP14 parity evidence for every capability formerly served by
  SQLite and accepted WP15 diagnosis for historical/mixed/unsafe roots.
- Require zero unresolved production and test consumers before deleting the
  package or removing the driver.
- Preserve a machine-readable preflight result in test output/evidence; do not
  create an editable waiver list that can silently authorize a forbidden edge.

### Subtask T080 - Inventory and delete the SQLite implementation

#### Purpose

Remove the SQLite store, schema, migrations, adapter implementations, and
SQLite-only tests only after all semantic consumers have qualified replacements.

#### Steps

1. Inventory the complete `internal/controlstore/sqlite/**` tree by role:
   implementation, schema, migration, fixture, test, helper, generated artifact,
   and documentation embedded in executable source.
2. Resolve every inbound production and test import using Go package loading for
   all supported build contexts, not only a text search on the default platform.
3. Cross-reference each former consumer with WP05–WP14's consumer-owned port,
   GapDB adapter, domain query, and frozen behavior proof.
4. Stop if any consumer lacks a tested replacement or still needs SQLite-shaped
   request/evidence types. Record the exact package and contract gap.
5. Delete the entire `internal/controlstore/sqlite/**` implementation, including
   schema DDL, migrations, SQL helpers, fixtures, adapter tests, and test drivers.
6. Do not retain a compile stub, deprecated forwarding package, archived `.go`
   file, build-tagged copy, generated copy, vendored copy, or test-only package.
7. Confirm product tests now prove behavior through capability contracts and
   domain projections rather than preserved SQLite row semantics.
8. Confirm WP15's classifier uses bounded signature/header inspection only and
   does not import or execute the deleted package.

#### Files

- `internal/controlstore/sqlite/**` (delete after preflight)

#### Validation

- `go list` across supported packages and test variants reports no import of the
  deleted package before and after removal.
- No owned SQLite file remains in tracked, generated, tagged, ignored, or
  package-loading surfaces.
- WP14 behavior parity and WP15 historical diagnosis tests remain green.
- A test-only reintroduction is rejected by the absence gate.

### Subtask T081 - Remove the driver and exclusive dependency closure

#### Purpose

Remove `modernc.org/sqlite` and every module needed exclusively by SQLite while
preserving dependencies that have independently verified non-SQLite consumers.

#### Steps

1. Capture the pre-removal module graph and identify the direct SQLite driver,
   every exclusive transitive, replacement relationships, and shared modules.
2. Remove the direct `modernc.org/sqlite` requirement only after T080's import
   census is clean.
3. Run `go mod tidy` from the exact candidate tree using the repository's
   declared Go version and supported build contexts.
4. Inspect changes to `go.mod` and `go.sum`; remove only dependencies proven
   exclusive to SQLite and retain independently reachable dependencies.
5. Recompute the module graph and `go list -deps -test` closure for every
   production package and test package.
6. Reject driver aliases, forks, embedded amalgamations, CGO SQLite libraries,
   command modules, or checksum-only residue that the declared policy forbids.
7. Ensure no replace/exclude/tool directive, build tool, or code generator can
   reacquire a SQLite implementation.
8. Keep dependency evidence deterministic and bind it to the exact `go.mod`,
   `go.sum`, source tree, toolchain, and candidate revision.

#### Files

- `go.mod`
- `go.sum`
- `scripts/m2-absence/**`

#### Validation

- `go mod tidy` is clean on a second run.
- `go mod graph`, `go list -deps -test ./...`, and module metadata contain no
  reachable SQLite driver or exclusive transitive.
- A mutant direct, indirect, aliased, tool-only, or test-only driver requirement
  is detected.
- The full supported package graph still compiles without network-time fallback.

### Subtask T082 - Add source and reachability absence guards

#### Purpose

Prove no production or test source path can reach SQLite through direct imports,
aliases, generated/tagged files, reflection, subprocesses, or copied code.

#### Steps

1. Create `internal/architecture/m2_absence_test.go` as the fast structural gate
   over the repository source and resolved Go package graph.
2. Inspect ordinary, test, generated, platform-specific, and build-tagged Go
   files using Go-aware loading plus bounded content/signature checks.
3. Reject direct and indirect SQL/SQLite imports, aliased driver paths, blank
   imports, linkname/plugin/reflection loading, CGO/linker edges, and wrappers
   whose reachable purpose is SQLite access.
4. Reject SQLite schemas, migration assets, copied driver/amalgamation
   signatures, generated implementations, renamed files, embedded assets, and
   executable fixture packages.
5. Reject subprocess construction that can invoke SQLite, an importer, a legacy
   helper, or a dynamically discovered database executable.
6. Reject fallback, rescue-read, comparison-read, shadow/dual-write, migration,
   compatibility, and backend-selection control flow even if currently false.
7. Inspect flags, environment variables, config structs/files, defaults, build
   tags, runtime probes, and help registries for an authority substrate selector.
8. Follow symlinks and normalized paths safely within declared roots, bound file
   count/bytes/time, and fail closed on unreadable or ambiguous candidate files.
9. Allow historical textual references only through exact path-scoped rules;
   token suppression, line comments, extensions, or broad directory exemptions
   must not hide executable content.

#### Files

- `internal/architecture/m2_absence_test.go`
- `scripts/m2-absence/**`

#### Validation

- Default and supported tagged/platform package graphs pass with zero edges.
- Generated, ignored, nested, copied, renamed, and symlinked forbidden fixtures
  are rejected.
- Reflection and subprocess mutants fail even without a lexical import.
- Broadening a historical exemption to an executable path fails a guard test.

### Subtask T083 - Prove binary, SBOM, install, help/config, and runtime absence

#### Purpose

Extend structural proof beyond source so every shipped artifact and supported
runtime surface is demonstrably free of SQLite implementation and selection.

#### Steps

1. Add deterministic scripts under `scripts/m2-absence/**` that build every
   production binary from the exact clean candidate using declared flags and
   capture source, module, toolchain, build, and artifact digests.
2. Generate the pinned deterministic CycloneDX SBOM from the complete module and
   build graph, then inspect components and dependency edges for SQLite.
3. Inspect each binary's dependencies, symbols, sections, strings, embedded
   assets, dynamic libraries, and subprocess references using declared tools.
4. Exercise the clean installation path into an isolated prefix and inspect all
   installed binaries, libraries, scripts, generated files, completions,
   examples, defaults, and configuration templates.
5. Capture version/help/config/schema outputs as machine evidence and reject any
   backend, database path, SQLite, migration, import, compatibility, rescue,
   comparison, or dual-write surface.
6. Run bounded startup/runtime probes against empty, current, historical, mixed,
   copied, renamed, nested, symlinked, malformed, and ambiguous roots.
7. Require historical roots to yield WP15's typed offline disposition before
   mutation; never allow the probe to open SQLite or create current authority.
8. Produce a source-bound manifest of inspected artifacts, commands, digests,
   tool versions, outcomes, and zero-edge counts for WP17 consumption.

#### Files

- `scripts/m2-absence/**`

#### Validation

- Source-clean but binary-embedded and SBOM-only mutants are rejected.
- A clean install contains no undeclared or unchecked file.
- Help/config/runtime backend-selector mutants are rejected.
- Runtime probes record zero SQLite loads, opens, subprocesses, imports, current
  mutations on unsupported historical roots, or fallback attempts.

### Subtask T084 - Add killing mutants for every forbidden surface

#### Purpose

Demonstrate that the layered absence gate detects each prohibited edge instead
of passing because the repository happens to contain none.

#### Steps

1. Create `internal/architecture/m2_absence_mutants_test.go` with isolated,
   bounded fixtures for every surface checked by T082 and T083.
2. Include direct import, blank/aliased import, indirect wrapper, generated file,
   build-tag file, platform file, reflection/plugin/linkname, CGO, subprocess,
   copied/renamed code, embedded schema, symlink, and ignored-file mutants.
3. Include direct/test-only/tool-only module requirements, transitive/forked
   drivers, SBOM component/edge, binary symbol/string/library, and installed-file
   mutants.
4. Include flags, environment variables, config fields/defaults, help text,
   runtime probes, migration/import commands, and hidden backend-selector mutants.
5. Include dual write, shadow write, comparison read, rescue read, fallback,
   automatic import, and historical-state conversion control-flow mutants.
6. Include a test-only driver and prove it is equally forbidden; tests do not
   receive a compatibility exception.
7. For each mutant, assert the expected stable finding kind, normalized path or
   artifact coordinate, bounded diagnostic, and nonzero gate result.
8. Include clean negative controls and legitimate historical documentation
   references so the gate proves discrimination rather than token banning.
9. Fail the mutant census if a declared surface lacks a case, a mutant is not
   reached, or a false-positive exemption masks it.

#### Files

- `internal/architecture/m2_absence_mutants_test.go`
- `scripts/m2-absence/**`

#### Validation

- Every forbidden-surface mutant is killed by the production absence command.
- Removing any detector causes its paired mutant to survive and the census to
  fail.
- Clean controls and exact historical references pass without a broad waiver.
- Mutant execution cannot modify the real worktree, module cache, or install.

### Subtask T085 - Verify a clean candidate and historical exemptions

#### Purpose

Prove the absence claim from a clean source checkout and clean module/build /
install environment, including vendor-like and historical-document handling.

#### Steps

1. Create an isolated clean checkout of the exact candidate commit using the
   repository's safe qualification workflow; do not copy the dirty worktree.
2. Verify tracked source/tree identity before building and fail on unexpected
   generated, ignored, nested, symlinked, or untracked executable material.
3. Use isolated module/build caches and a declared toolchain. Verify dependencies
   with normal module mode and a materialized vendor-like dependency tree or the
   repository's equivalent offline graph inspection.
4. Build and test without relying on a pre-existing driver, shared binary,
   global config, ambient database tool, or developer-machine fallback.
5. Run source, reachability, module, SBOM, binary, clean-install, help/config,
   runtime, and mutant gates against the same source/tree/dependency/binary tuple.
6. Enumerate immutable historical evidence and documentation paths that may
   truthfully mention SQLite. Exempt exact paths and expected artifact classes,
   never directory prefixes capable of containing executable code.
7. Prove an executable, generated, embedded, configured, or copied SQLite edge
   placed adjacent to or beneath a historical path is still rejected.
8. Emit deterministic evidence with candidate/source digests, module and SBOM
   digests, binary/install digests, commands, tool versions, exemption matches,
   mutant census, and zero-edge verdict.
9. Leave the repository and external historical evidence unchanged; temporary
   checkout, caches, and install roots must be isolated and bounded.

#### Files

- `scripts/m2-absence/**`
- `internal/architecture/m2_absence_test.go`
- `internal/architecture/m2_absence_mutants_test.go`

#### Validation

Run focused checks during development and the clean-candidate script before
handoff:

```bash
go test ./internal/architecture -run 'TestM2SQLiteAbsence|TestM2AbsenceMutants'
go test -race ./internal/architecture -run 'TestM2SQLiteAbsence|TestM2AbsenceMutants'
go list -deps -test ./...
go mod graph
go mod tidy
git diff --exit-code -- go.mod go.sum
go vet ./...
staticcheck ./...
git diff --check
```

Run the exact script entry point created under `scripts/m2-absence/**` for clean
checkout, deterministic SBOM, binary, install, configuration, runtime, and
mutant qualification. Record the command and artifact paths in the handoff.

## Definition of Done

- T080–T085 are complete only within the declared owned files.
- `internal/controlstore/sqlite/**` is deleted with no stub, archive, generated,
  tagged, copied, vendored, ignored, or test-only implementation remaining.
- `modernc.org/sqlite` and every SQLite-exclusive transitive are absent from the
  direct, test, tool, module, checksum, vendor-like, SBOM, and build graphs.
- Source and reachability checks find zero direct, indirect, aliased, generated,
  tagged, reflective, subprocess, CGO, embedded, copied, or symlinked edges.
- Binary, deterministic CycloneDX SBOM, clean install, help/config, and runtime
  inspections find zero SQLite implementation or backend-selection surfaces.
- No importer, exporter, migration, fallback, comparison/rescue read, shadow or
  dual write, backend selector, or test-only driver exists.
- Historical roots still receive typed offline disposition without being opened
  or imported, and immutable historical evidence is not modified.
- Every forbidden surface has a reached and killed mutant plus clean negative
  control; historical exemptions are exact and path-scoped.
- Clean-checkout and vendor-like/offline verification binds all evidence to one
  exact source/tree/dependency/binary/install tuple.
- Focused and race tests, module checks, `go mod tidy`, `go vet`, `staticcheck`,
  clean-build/install qualification, and `git diff --check` pass.
- The implementation handoff records exact commands, toolchain, digests,
  exemptions, mutant census, artifact manifest, and any bounded caveat.
- Completion is recorded through the event log, not by editing frontmatter:

```bash
spec-kitty agent tasks mark-status T080 T081 T082 T083 T084 T085 \
  --status done \
  --mission gapdb-only-production-authority-01M1GWDJ
```

## Risks and mitigations

- **Premature deletion:** Removing SQLite before all consumers move can conceal
  behavior loss. Require WP14 parity and zero unresolved imports first.
- **Test-only retention:** A driver kept for comparison remains reachable. Port
  tests to domain evidence and reject test/tool dependencies equally.
- **Lexical-only proof:** Token search misses aliases and dynamic edges. Combine
  Go-aware reachability, content signatures, artifacts, and runtime probes.
- **Overbroad exemption:** A docs-directory waiver can hide executable content.
  Match exact historical paths/classes and keep nested executable mutants.
- **Incomplete dependency cleanup:** Tidy alone may miss tools or alternate build
  contexts. Inspect module, test, tool, tagged, SBOM, and vendor-like graphs.
- **Artifact/source mismatch:** A clean source test can accompany a stale binary.
  Bind all outputs and inspections to exact candidate and artifact digests.
- **Compatibility relapse:** A fallback makes rollback appear easy but restores
  split authority. Reject the candidate as a unit without reactivating SQLite.

## Reviewer guidance

Review WP16 as a structural absence proof, not a default-backend switch. Require
direct source-bound evidence that:

1. Replacement and parity were proven before deletion, and the full SQLite
   package/schema/migration/test surface is gone.
2. No SQLite driver or exclusive dependency survives in normal, test, tool,
   alternate-build, module, vendor-like, SBOM, binary, or install graphs.
3. Go-aware reachability and bounded signature checks cover aliases, wrappers,
   generated/tagged/platform files, reflection, subprocesses, copied code,
   embedded assets, CGO, ignored files, and symlinks.
4. Flags, environment, config, defaults, help, runtime probes, and commands
   expose no backend selector, importer, migration, fallback, dual write,
   comparison read, or rescue read.
5. Test-only drivers and compatibility helpers receive no exemption.
6. Every declared forbidden surface has a positive killing mutant and clean
   control with stable diagnostics.
7. The clean-checkout campaign inspects one exact source/tree/dependency/SBOM /
   binary/install tuple under isolated caches and declared tooling.
8. Historical SQLite evidence remains truthful and unchanged, while exemptions
   are exact path-scoped and cannot shelter executable content.
9. Unsupported historical roots still refuse before any current mutation and
   production never opens or imports them.
10. The diff is confined to WP16 ownership and `go mod tidy` has not removed an
    independently required dependency or introduced an unreviewed one.

Reject if SQLite is merely disabled; if any test, tag, generator, module, binary,
install, configuration, subprocess, or runtime surface can reach it; if an
importer/fallback/dual path remains; if evidence comes from a dirty or mismatched
tree; or if historical exemptions are broad enough to mask executable code.
