---
work_package_id: WP01
title: M2 Contracts and Permanent Architecture Guards
dependencies: []
requirement_refs:
- FR-001
- FR-023
- FR-031
- FR-033
- FR-035
- NFR-013
- NFR-014
- NFR-015
- C-001
- C-004
- C-005
- C-006
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T001
- T002
- T003
- T004
- T005
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/architecture/
create_intent:
- internal/architecture/m2_contracts_test.go
- internal/architecture/m2_dependency_guard_test.go
- internal/architecture/m2_mutant_control_test.go
- internal/architecture/m2_registry_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/architecture/m2_contracts_test.go
- internal/architecture/m2_dependency_guard_test.go
- internal/architecture/m2_mutant_control_test.go
- internal/architecture/m2_registry_test.go
- internal/architecture/policy.go
- internal/architecture/policy_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP01 — M2 Contracts and Permanent Architecture Guards

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Turn M2's entry evidence, versioned contracts, package boundaries, and keyspace
laws into executable architecture tests before any replacement authority code
lands. The resulting guard suite must fail closed under semantic mutations and
remain effective when implementations are renamed, embedded, wrapped, moved,
or hidden behind harmless decoys.

## Context

M2 replaces the historical SQLite authority with one memory-primary runtime
whose only durable production substrate is the exact GapDB tuple admitted by
M1. This first package does not implement the new journal or any capability.
It establishes the non-negotiable boundary within which WP02–WP16 must work.
This package was reopened by `post-approval-defect-2.md`; the corrected global
head spelling, operation tuple, and coordinator reachability law replace the
previously approved forms.

Read these mission artifacts before editing:

- `spec.md`, especially FR-001, FR-023, FR-031, FR-033, and FR-035;
- `plan.md`, especially the immutable M1 entry evidence, architecture,
  validation strategy, and IC-01;
- `research.md`, especially Decisions 1, 4, 5, 9, and 10;
- `data-model.md`, especially the closed `spk/v2/` keyspace and identity laws;
- every document in `contracts/`, including the event-family registry;
- existing architecture machinery in `internal/architecture/`; and
- the M1 guards as prior art, not as a template that may be copied blindly.

The governing laws are immutable M1 admission; capability-owned production
authority; GapDB knowledge confined to adapters; pure journal/playback code;
host-global OperationID identity; the closed `spk/v2/` keyspace; fail-closed
package classification; semantic rather than spelling-based inspection; and
non-production, path-scoped treatment of historical evidence.

The six files in `owned_files` are the complete writable surface for this WP.
The permanent `DefaultPolicy` must classify every closed M2 capability package
with exact or slash-bounded entries before downstream packages appear; a
test-only overlay is not repository admission. Do not edit `reachability.go`,
contracts, program evidence, module files, or production packages. If the
existing reusable architecture API is insufficient, stop and report the exact
missing primitive instead of smuggling production behavior into a test helper
or expanding ownership silently.

## Branch Strategy

- Planning branch: `feat/gapdb-only-production-authority`.
- Final mission merge target: `feat/gapdb-only-production-authority`.
- Execution worktrees are allocated by Spec Kitty per the computed lane in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- Do not assume WP01 receives a dedicated branch name or fixed worktree path.
  Use the worktree and branch reported by the runtime action capsule.
- Keep every commit limited to the six owned architecture policy/test files. Other
  lanes may be changing shared mission or production surfaces concurrently.

Start or resume implementation with:

```bash
spec-kitty agent action implement WP01 --agent <name> --mission gapdb-only-production-authority-01M1GWDJ
```

## T001 — Executable contract and schema validation

### Purpose

Make the complete M2 contract directory a closed, executable contract set.
The test must prove syntax, schema compilation, identifier uniqueness, and the
cross-document laws needed by later packages. Merely unmarshalling JSON or
checking for filenames is not sufficient.

### Implementation guidance

1. Create `internal/architecture/m2_contracts_test.go`.
2. Locate the repository root from the test source location using a bounded,
   deterministic helper; never depend on the current working directory.
3. Declare the exact required contract census:
   `authority-journal-v2.md`, `event-family-registry-v2.json`,
   `historical-state-disposition-v1.schema.json`,
   `m2-qualification-v1.schema.json`, and
   `operation-binding-v2.schema.json`.
4. Reject a missing file, an unexpected schema file, duplicate `$id`, invalid
   JSON, malformed JSON Schema, or an unclosed document with actionable path
   diagnostics.
5. Compile all `*.schema.json` documents through the repository's existing
   JSON Schema dependency with format assertions enabled. Register resources
   by `$id` before compilation so cross-schema references cannot be skipped.
6. Exercise each schema with at least one valid boundary fixture and negative
   fixtures for omitted required fields, additional fields, invalid enums,
   malformed identities/digests, and values beyond declared bounds.
7. Parse the event-family registry into a test-owned closed model. Validate:
   schema version, registry identity, publication rule, exact ordering tuple,
   non-empty families, unique family names, positive schema/reducer versions,
   normalized owner import paths, and a closed expected family census.
8. Assert the registry includes every owner promised by the plan: runtime,
   operation, mission, both artifact aggregates, workgraph, execution,
   workspace, handoff, review, acceptance, integration target/attempt,
   external effect, and outbox.
   Require the operation family to use exactly event schema 2/reducer 2 and
   reject operation tuple `1/1`. Treat registry ID/version, publication rule,
   ordering tuple, and every family/schema/reducer/owner field as compatibility
   identity rather than descriptive metadata.
9. Cross-check key semantic phrases in `authority-journal-v2.md` only where the
   Markdown file is presently the normative contract. Keep these assertions
   structural and exact enough to fail when a law disappears, but avoid prose
   snapshots that fail on punctuation.
10. Cover at minimum: one event per affected aggregate per publication,
    host-global OperationID binding, one conditional batch, immutable
    version-addressed events, deterministic ordering, and pure playback.
11. Ensure test diagnostics name the contract, violated law, expected value,
    and observed value. Avoid generic `contract invalid` failures.

### Validation

Run:

```bash
go test ./internal/architecture -run 'TestM2Contract' -count=1
```

Then prove at least one in-memory schema mutant and one registry mutant are
rejected by the same production-facing validator used for the positive case.

## T002 — Exact M1 adoption and archive revalidation guard

### Purpose

Prevent M2 from silently inheriting a different GapDB build, source tree,
evidence manifest, release archive, or unsigned decision. Ordinary test runs
must validate local immutable coordinates; explicit qualification may perform
the remote archive download, but routine unit tests must not depend on network
availability.

### Implementation guidance

1. Add the M1 entry guard to `internal/architecture/m2_contracts_test.go` or a
   narrowly focused section in `m2_registry_test.go`; do not create a fifth
   file.
2. Decode `docs/programs/evidence/m1-adoption.json` using a closed typed shape
   or strict JSON decoding. Reject unknown fields and trailing JSON.
3. Assert these exact admitted coordinates:
   - decision `adopt`;
   - source commit `e05a5e46feb3e65ff42c4544c16678c440ef3818`;
   - source tree `c7b932b3d85833beee1a84d8a1ae7f4e4a75ec73`;
   - manifest SHA-256
     `397f93a9b0f1ea24715ba57b0cb58aeb489e13a7e27e8bc76259597f36f2f309`;
   - tuple SHA-256
     `4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d`;
   - sidecar SHA-256
     `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`;
   - signer `github:lynncoleart`; and
   - a non-empty, explicitly supported signature kind and value.
4. Cross-check the same coordinates in the human-readable closeout and the
   machine M1 admission record where those documents own the coordinate.
   Reject disagreement; never choose one source opportunistically.
5. Assert the immutable release tag `m1-gapdb-evidence-v1`, archive name
   `spec-kitty-m1-crown-e05a5e4.tar.zst`, and archive SHA-256
   `86b8cd608f5cd9d1dce7ce25dffa5d87e1df25a233847316416f14e7948a18df`.
6. Keep remote acquisition outside the default `go test` path. The local guard
   proves the pinned command/coordinates and record consistency; the final M2
   qualification lane must download, hash, extract, and re-run the archive.
7. Do not hard-code the current working-tree hash of `m1-adoption.json` as the
   adoption identity. Its canonical fields are the law; line endings or
   harmless formatting are not a provider change.
8. Add table-driven mutations for each coordinate, `adopt` to `refuse`, empty
   signature, unknown signature kind, inconsistent closeout values, mutable
   release aliases such as `latest`, and incomplete archive commands.
9. Reuse a single validator for positive evidence and all mutants. A separate
   mutant-only comparator is not admissible evidence.
10. Ensure later M2 tests can call a narrow helper that returns validated entry
    coordinates without importing production GapDB code into architecture
    policy.

### Validation

Run the focused test twice and verify deterministic diagnostics:

```bash
go test ./internal/architecture -run 'TestM2Entry|TestM2Admission' -count=2
```

The default test must perform no network calls, start no sidecar, and mutate no
repository file. Document the exact remote revalidation command as validated
test data only if one already exists in repository documentation or tooling;
do not invent a second downloader.

## T003 — Permanent dependency, package, and keyspace guards

### Purpose

Establish a permanent closed architecture for the new production authority.
This guard must allow the intentional GapDB adapter while denying provider
knowledge, SQL authority, table-shaped seams, raw key access, and scope-local
operation identities everywhere else.

### Implementation guidance

1. Create `internal/architecture/m2_dependency_guard_test.go`.
2. Reuse `DefaultPolicy`, graph loading, typed-source loading, and reachability
   machinery where they already express the needed rule. Do not replace a
   semantic graph check with repository-wide substring search.
3. Declare the intended M2 tier additions before their packages land, using
   activation-on-package-presence where available. A package's first source
   file must activate its complete boundary; missing or unclassified packages
   must not pass during incremental delivery.
4. Keep the pure v2 journal surface closed to ambient I/O, time, randomness,
   filesystem, Git, tracker, provider, and application capability imports.
   Context, encoding, hashing, sorting, validation, and provider-neutral domain
   types may be allowed deliberately.
5. Permit the public GapDB module only from the concrete GapDB adapter, sidecar
   supervisor, and qualification-only external runner. Reject GapDB server
   internals from every package.
6. Deny all SQLite and SQL-driver families from production and test packages,
   including alternate drivers, SQLCipher variants, blank imports, aliases,
   build-tagged files, generated Go files, and transitive production edges.
7. Preserve the tracker layering rule: tracker adapters may implement the
   provider contract but may not import mission/artifact authority or become a
   second execution authority.
8. Require capability packages to depend inward on consumer-owned ports and
   provider-neutral journal contracts. Deny direct dependencies from mission,
   artifact, workgraph, execution, workspace, handoff, review, integration, or
   projection packages to concrete control-store adapters.
   The foundation publication coordinator is the only capability-reachable
   durable publication seam; a capability reaching the GapDB adapter directly
   must fail even through a wrapper or interface value.
9. Encode the closed production `spk/v2/` keyspace contract in
   `internal/architecture/m2_registry_test.go`. Assert canonical namespaces
   for authority binding, runtime epochs, global operations, globally
   enumerable `spk/v2/heads/<scope>/<kind>/<id>` aggregate heads, scoped
   immutable events, checkpoints, and workgraph eligibility shards. Explicitly
   reject the former `spk/v2/scopes/<scope>/heads/...` spelling and any alias.
10. Prove operation bindings are outside mission/project scoped journal
    prefixes. Reject helper APIs or constants that construct
    `.../missions/<id>/operations/...` or accept mission identity as the root
    authority for OperationID uniqueness.
11. Require fixed-width lowercase hexadecimal logical versions and
    unambiguous component escaping. Deny caller-controlled raw production keys,
    generic `Get(string)`/`Put(string, ...)` repositories in capability
    packages, and table/row/query vocabulary that recreates the god-store.
12. Keep historical evidence allowances explicit, path bounded, read-only, and
    outside production reachability. A broad documentation or test exemption
    that admits executable SQL code is a guard failure.
13. Sort violations deterministically and require stable rule IDs, source
    package, target package/symbol, and recovery-oriented diagnostic details.

### Validation

Run:

```bash
go test ./internal/architecture -run 'TestM2Dependency|TestM2Package|TestM2Keyspace|TestM2Registry' -count=1
```

Positive fixtures must include unrelated aggregates and legitimate adapter
composition. A guard that enforces safety by globally serializing or banning
the admitted adapter is incorrect.

## T004 — Semantic AST and reachability mutant controls

### Purpose

Demonstrate that every permanent guard actually fires and cannot be defeated
by easy source reshaping. The mutant suite is part of the product boundary,
not test decoration.

### Implementation guidance

1. Create `internal/architecture/m2_mutant_control_test.go`.
2. Build minimal, type-correct in-memory source graphs for positive controls
   and one mutation at a time. Reuse the exact inspection functions exercised
   against the real repository.
3. Include import mutants for direct, aliased, blank, dot, generated,
   build-tagged, internal-provider, alternate-driver, and transitive edges.
4. Include reachability mutants where a forbidden implementation is hidden
   behind an interface, constructor variable, embedded field, generic helper,
   method value, function value, or wrapper package.
5. Include rename mutants proving the guard follows types/call edges rather
   than identifiers such as `SQLiteStore`, `GapDBRepository`, or `Database`.
6. Include decoy mutants: comments, string literals, dead-looking test fixture
   names, and unrelated types with suspicious names must not create false
   positives. Conversely, an innocently named production edge must be caught.
7. Include keyspace mutants for a mission-scoped operation binding, variable
   width version, ambiguous component encoding, raw caller key, v1/current
   mixed prefix, the rejected scope-first aggregate-head spelling, and global
   journal head or shared projection head.
8. Include architecture-policy deletion mutants: remove each critical package
   classification, allowed-direction restriction, forbidden-provider prefix,
   reachability target, and keyspace registry entry; each removal must change
   the result and be rejected.
9. Include embedding and interface-satisfaction mutants that prove a concrete
   adapter cannot leak into a capability merely because the direct import is
   held by an intermediate package.
10. Include missing-target activation mutants. A predeclared package may be
    absent before its WP lands, but once any production file appears the guard
    must require its full classification and registered critical seams.
11. Require an exact mutant census with stable IDs. Removing a mutant from the
    list must fail the census rather than quietly reduce confidence.
12. Bound parsing, type checking, graph size, output, and runtime so malformed
    source fails closed without hanging CI or producing unbounded diagnostics.
13. Do not write mutants into the repository tree or invoke destructive Git
    operations. Use in-memory fixtures or isolated temporary modules.

### Validation

Run:

```bash
go test ./internal/architecture -run 'TestM2.*Mutant' -count=1
go test -race ./internal/architecture -run 'TestM2.*Mutant' -count=1
```

The mutant report must identify every expected mutant as killed. A parse error
is an acceptable kill only for a mutant whose intended violation is malformed
source; architecture mutants must reach and fail the intended semantic rule.

## T005 — Focused contract and guard census evidence

### Purpose

Provide a fast, exact, reviewable census proving WP01's boundary is live. This
is the first layer of M2 evidence and will be consumed by later capability and
final crown campaigns.

### Implementation guidance

1. Define a stable census in `internal/architecture/m2_registry_test.go` that
   lists every WP01 contract class, package/keyspace law, reachability control,
   and mutant category by durable ID.
2. Make the test compare exact sorted IDs and counts. It must reject duplicate,
   missing, unexpected, skipped, or non-terminal entries.
3. Record at least these evidence categories: contract schema compilation,
   contract negative boundaries, event-family registry, M1 entry tuple,
   release archive coordinates, package directions, pure-kernel imports,
   provider reachability, SQLite/SQL denial, global OperationID keyspace,
   current v2 keyspace, type/AST reachability, renames, embedding, decoys, and
   guard-deletion mutants.
4. Keep evidence source-bound by reporting the test package and exact census;
   do not claim final M2 qualification or runtime behavior from WP01.
5. Ensure failures contain stable evidence IDs suitable for later machine
   collection. Human prose may accompany them but is not the identity.
6. Run all existing architecture tests as a regression check. WP01 strengthens
   existing laws; it must not delete M1 or Phase 1–3 guards to make new packages
   fit.
7. Run the focused suite repeatedly and with the race detector. These tests
   should be fast enough for the PR gate and deterministic under isolated runs.
8. Use `git diff --check` and confirm only the declared owned files changed.
9. Capture the exact validation commands and terminal outcomes in the WP
   handoff/review evidence; do not add generated evidence artifacts to another
   WP's owned surface.
10. Mark subtask completion only after its focused proof is green.

### Validation

Run the focused and complete architecture gates:

```bash
go test ./internal/architecture -run 'TestM2' -count=3
go test -race ./internal/architecture -run 'TestM2' -count=1
go test ./internal/architecture -count=1
git diff --check
```

If an existing repository-level architecture test is expensive, preserve it in
the complete command and keep the `TestM2` subset bounded for normal iteration.

## Definition of Done

- All four owned test files exist, are formatted, and contain substantive
  executable checks rather than placeholder assertions.
- The complete contract census compiles, validates positive boundaries, and
  rejects representative closed-schema mutations.
- The event-family registry has a unique, exact, versioned census with stable
  owners and ordering semantics; operation tuple `2/2` is admitted and `1/1`
  is refused.
- M1 adoption, source/tree, manifest, tuple, sidecar, release tag, archive name,
  and archive digest are revalidated without a default network dependency.
- Package-direction tests preserve capability ownership, pure playback, the
  narrow GapDB adapter allowance, tracker bounds, and unknown-package refusal.
- Keyspace tests preserve global OperationID identity and the closed `spk/v2/`
  layout, including the globally enumerable aggregate-head prefix and rejection
  of its scope-first predecessor, without introducing a hot global journal
  head.
- Reachability tests reject direct and wrapped capability-to-GapDB adapter
  calls while allowing the foundation publication coordinator and composition
  root.
- Semantic mutants cover direct and indirect imports, aliases, build tags,
  generated files, renames, wrappers, interfaces, embedding, decoys, missing
  activation, and deleted guard rules.
- Every declared mutant is killed by the same validator used for the repository
  or positive control; the exact census rejects silent mutant removal.
- Focused tests pass three times, the focused race gate passes, all architecture
  tests pass, and `git diff --check` is clean.
- No owned test starts a sidecar, accesses the network, mutates repository
  evidence, or weakens an existing architecture law.
- The implementation handoff names any deliberate test-only exception and why
  it cannot become production reachability.
- Completion is recorded through the event log, not by editing Markdown task
  markers:

```bash
spec-kitty agent tasks mark-status T001 T002 T003 T004 T005 --status done --mission gapdb-only-production-authority-01M1GWDJ
```

## Risks

- **Spelling guards**: renames can preserve forbidden semantics. Prefer parser,
  type, dependency, and reachability evidence; reserve text checks for genuine
  configuration, documentation, binary, or key-prefix surfaces.
- **Over-restriction**: a total adapter ban defeats M2. Include positive legal
  adapter and unrelated-aggregate controls.
- **Deferred packages**: absence may defer a rule, but the first production
  file must activate complete classification and critical-seam checks.
- **Mutable admission**: ordinary CI must use pinned local coordinates;
  explicit qualification owns fresh remote archive acquisition.
- **Self-proving helpers**: mutants must exercise the real repository validator
  and deleted-rule mutations must demonstrate sensitivity.
- **Historical exceptions**: constrain them to exact non-production paths and
  prove those paths cannot execute as current authority.
- **Unbounded checks**: reuse bounded loaders, timeouts, output limits, and
  deterministic sorting so the focused M2 subset remains PR-fast.

## Reviewer Guidance

Review WP01 as an executable architecture boundary, not merely as test
coverage. In particular, verify:

- each positive fixture represents a legal M2 architecture rather than an
  empty or impossible graph;
- every negative fixture reaches its intended rule and cannot pass due to an
  unrelated parse/type error;
- M1 coordinates exactly match the signed adoption record and release
  closeout, with no mutable alias or network dependency in ordinary tests;
- contract schemas are compiled and exercised, not merely listed;
- event-family and keyspace registries are closed, duplicate-free, and
  resilient to reorderings where order is not semantic;
- operation event tuple `2/2` and the global aggregate-head spelling are the
  only admitted forms; their replaced forms fail dedicated mutants;
- OperationID bindings are demonstrably host-global while aggregate events
  remain scoped and independently writable;
- pure journal/playback layers cannot reach provider, filesystem, Git, tracker,
  clock, randomness, or application capability code;
- provider allowances are limited to the concrete GapDB adapter, sidecar, and
  qualification-only runner;
- tracker adapters cannot acquire mission, artifact, execution, or Git
  authority;
- rename, interface, wrapper, embedding, generated-file, and build-tag mutants
  prove semantic analysis rather than naming dependence;
- comments and harmless decoys do not trigger false positives;
- deleting a rule, registry entry, or mutant is itself detected;
- the existing M1 and Phase 1–3 architecture tests remain intact and green;
  and
- all edits remain inside WP01's six-file ownership boundary.

Reject the WP if the suite can be made green by deleting a census entry,
renaming a forbidden type, moving an import behind a wrapper, limiting scans to
currently existing packages, or turning the exact M1 tuple into a configurable
value.
