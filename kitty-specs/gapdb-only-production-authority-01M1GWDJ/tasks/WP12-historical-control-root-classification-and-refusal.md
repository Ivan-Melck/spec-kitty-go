---
work_package_id: WP12
title: Historical Control-Root Classification and Refusal
dependencies:
- WP01
requirement_refs:
- FR-026
- FR-027
- FR-028
- FR-029
- FR-030
- FR-034
- NFR-012
- NFR-014
- C-007
- C-008
- C-017
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T059
- T060
- T061
- T062
- T063
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controlroot/
create_intent:
- internal/controlroot/classifier.go
- internal/controlroot/model.go
- internal/controlroot/signatures.go
- internal/controlroot/classifier_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/controlroot/classifier.go
- internal/controlroot/model.go
- internal/controlroot/signatures.go
- internal/controlroot/classifier_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP12 — Historical Control-Root Classification and Refusal

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Implement a bounded, deterministic, read-only classifier that distinguishes
safe empty/current control roots from historical, mixed, unsafe, malformed, or
ambiguous roots before any sidecar or current authority mutation. Historical or
uncertain state must produce canonical `OFFLINE_DISPOSITION_REQUIRED` evidence,
never an implicit import, fallback, waiver, or “prefer current” decision.

## Context

Deleting the SQLite adapter without recognizing its on-disk state would make an
existing project appear empty. Automatically reading or translating it would
create hidden migration and dual authority. M2 therefore recognizes historical
evidence without executing SQL and stops production startup before it can create
new truth beside old truth.

Read the mission `spec.md`, `plan.md`, `research.md` Decisions 8 and 9,
`data-model.md` historical disposition section, and
`contracts/historical-state-disposition-v1.schema.json`. WP01's architecture
guards are the only dependency. WP13 will call the classifier before starting
GapDB; this WP supplies the stable typed contract and proof but does not edit
the production host.

The schema's classification vocabulary is closed:

- `empty_supported` maps to `initialize`;
- `current_supported` maps to `recover`; and
- `historical`, `mixed`, `unsafe`, `malformed`, and `ambiguous` map to
  `offline_disposition_required`.

Every returned disposition has `current_mutation_count: 0`. Findings are
bounded, canonical, stable-code evidence. Human prose may explain them but must
not be the machine decision surface.

Stay inside the five owned paths. In particular, do not import the SQLite
driver, open a database, start `gapdbd`, create a socket/directory/lock, alter
the root, add a converter, or edit startup composition from this lane.

## Branch Strategy

- Planning branch: `feat/gapdb-only-production-authority`.
- Final mission merge target: `feat/gapdb-only-production-authority`.
- Spec Kitty computes the execution lane and worktree in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- Use the runtime capsule's exact branch and workspace; do not infer either.
- Other agents are working in the same repository. Preserve their changes and
  restrict commits to this WP's ownership.

Start or resume implementation with:

```bash
spec-kitty agent action implement WP12 --agent <name> --mission gapdb-only-production-authority-01M1GWDJ
```

## T059 — Bounded read-only root, path, ownership, and symlink model

### Purpose

Define safe inspection independently of provider formats. Path traversal,
foreign ownership, shared roots, symlinks, filesystem errors, and unbounded
trees must become explicit classification facts rather than opportunities to
guess.

### Implementation guidance

1. In `model.go`, implement the closed disposition model matching the JSON
   Schema exactly: schema version, canonical root digest, classification,
   bounded findings, zero mutation count, action, and diagnosis code.
2. Give each finding a closed/stable `kind` and uppercase `detail_code`, with
   optional normalized relative path and observed SHA-256. Reject absolute,
   escaping, empty, overlong, duplicate, or non-canonical relative paths.
3. Define explicit inspection budgets: maximum entries, depth, findings, path
   bytes, bytes read per signature candidate, total bytes hashed/read, and
   elapsed/context limit. Budget exhaustion returns `ambiguous` or `unsafe`
   with an actionable stable code; it never truncates into a safe result.
4. Canonicalize the requested root lexically and inspect it with `Lstat`-style
   semantics before following anything. A root symlink, relevant-entry symlink,
   traversal component, or resolved target outside the trusted root is unsafe.
5. Do not follow directory or file symlinks while walking. Avoid TOCTOU-prone
   “check then follow” behavior; open/read only entries whose observed identity
   still matches the bounded inspection fact where the platform permits.
6. Accept an explicit ownership/trust policy rather than consulting ambient
   usernames. On the qualified Linux surface, compare stable numeric owner and
   relevant permission/link/device/inode facts. Foreign or unexpectedly shared
   writable roots refuse.
7. Treat permission denial, disappearing/replaced entries, special files,
   sockets from an untrusted layout, hard-link ambiguity, mount/device crossing,
   and unsupported filesystem identity as unsafe or ambiguous—not empty.
8. Build the root digest from normalized root identity and the canonical sorted
   finding/entry observations required for disposition. Do not include map
   order, current time, absolute host-specific prose, or mutable error strings.
9. Keep the classifier read-only by construction: its interfaces expose stat,
   bounded read, and directory enumeration only. They must not expose create,
   rename, remove, chmod, lock, database open, or process execution.
10. Test normalization, root absence/empty directory, permissions, foreign
    owner, root/child symlink, hard link, special file, disappearing entry,
    budget exhaustion, cancellation, and deterministic finding order/digest.

### Validation

```bash
go test ./internal/controlroot -run 'Test.*Root|Test.*Path|Test.*Ownership|Test.*Bound' -count=3
```

## T060 — Historical SQLite and companion signature detection without SQL

### Purpose

Recognize historical authority even when files are renamed or copied, using
bounded byte/file-layout evidence only. The classifier must avoid both false
emptiness and dependency on a SQL engine.

### Implementation guidance

1. In `signatures.go`, declare a closed registry of known historical control
   filenames/layouts from the shipped Phase 1–3 runtime and historical fixture
   evidence. Include the actual `control.db` family and any documented legacy
   locations; do not invent unrelated generic names as authoritative matches.
2. Detect the canonical 16-byte SQLite database header `SQLite format 3\x00`
   with an exact bounded prefix read. This content signature applies to copied
   or renamed files regardless of extension.
3. Detect recognized companion relationships such as `-wal`, `-shm`, and
   rollback-journal names. A companion without its base is still historical or
   ambiguous evidence, not safe emptiness.
4. Where companion file magic/layout is used, parse only the minimal bounded
   bytes required by a documented signature. Do not parse pages, schemas,
   tables, PRAGMAs, WAL records, or application payloads.
5. Distinguish confidence: exact SQLite header, known historical path, known
   companion, and corroborated groups can yield historical; incomplete or
   conflicting look-alikes yield ambiguous. Never classify by extension alone.
6. Detect renamed/copy cases by content signature and deterministic digest,
   including a valid SQLite header under a neutral filename and nested within
   the bounded control root.
7. Reject oversized, sparse, truncated, changing, unreadable, or special-file
   candidates conservatively. Limit reads to signature windows plus explicitly
   budgeted hashing required for evidence.
8. Ensure production code imports only standard filesystem/encoding/hash
   packages. It must not import `database/sql`, any SQLite/SQLCipher driver,
   provider CLI package, or invoke a subprocess.
9. Keep signature registry tests exact so removing a known historical pattern
   is itself detected. Historical test fixtures may be static small byte files;
   do not generate them with a compiled SQLite driver.
10. Test named database, renamed database, copied database, valid companion
    set, orphan companions, truncated header, case/extension look-alikes,
    header appearing after offset zero, text containing the header phrase, and
    random binary collisions around the prefix.

### Validation

```bash
go test ./internal/controlroot -run 'Test.*Historical|Test.*SQLite|Test.*Signature|Test.*Companion' -count=3
go list -deps ./internal/controlroot
```

The dependency output must contain no database/SQL package or SQLite driver.

## T061 — Current, empty, mixed, malformed, and ambiguous canonical disposition

### Purpose

Combine bounded observations into one total, deterministic classification whose
action cannot be misinterpreted by startup.

### Implementation guidance

1. In `classifier.go`, implement precedence explicitly. Unsafe path/ownership
   facts cannot be overridden by recognizable provider state. Historical plus
   current evidence is always `mixed`, never “prefer current.”
2. Define exact current-layout signatures from the M2 contract: authority
   binding/database identity and expected GapDB sidecar/data layout markers.
   Recognizable but incomplete, contradictory, wrong-version, or malformed
   current state is `malformed` or `ambiguous`, not `current_supported`.
3. `empty_supported` requires a safe trusted root with no current, historical,
   unknown authority-like, or uninspectable entries. Ignored/unrelated files
   must be governed by a narrow documented allowlist, not a broad “unknown is
   harmless” rule.
4. `current_supported` requires one coherent current authority identity and no
   historical, split, shared, foreign, malformed, or ambiguous evidence. This
   classification authorizes WP13 to attempt normal recovery, not to declare
   the runtime healthy.
5. `historical` requires recognized old authority and no current authority.
   `mixed` covers any co-presence or split identity. `unsafe` covers root/path/
   owner/filesystem safety. `malformed` covers definitely current but invalid
   layouts. `ambiguous` covers bounded uncertainty and suspicious look-alikes.
6. Generate a canonical sorted, deduplicated findings list. Bound it at the
   contract maximum of 256 and fail closed before silent truncation.
7. Validate every result against the model invariants corresponding to
   `historical-state-disposition-v1.schema.json`, including exact action mapping,
   stable diagnosis code, SHA-256 root digest, and mutation count zero.
8. Make repeated classification of unchanged bytes identical. Time, traversal
   order, platform error prose, and randomized map order must not affect output.
9. Test every pairwise precedence combination, especially historical+current,
   current+unsafe, historical+unsafe, malformed+historical, and ambiguous+
   otherwise-empty.

### Validation

```bash
go test ./internal/controlroot -run 'Test.*Classif|Test.*Disposition|Test.*Precedence|Test.*Canonical' -count=10
```

## T062 — Pre-sidecar zero-mutation refusal integration contract

### Purpose

Expose the minimal startup gate WP13 must call before any current authority
activity, and make bypass/waiver/import semantics unrepresentable.

### Implementation guidance

1. Expose one narrow classifier/gate API that returns a validated disposition;
   do not expose a boolean such as `HasLegacyDB` that callers may ignore.
2. For `historical`, `mixed`, `unsafe`, `malformed`, or `ambiguous`, return a
   typed `OFFLINE_DISPOSITION_REQUIRED` result carrying canonical disposition
   evidence and bounded recovery guidance.
3. The gate must complete before any sidecar lifecycle, socket connection,
   database initialization, authority binding, checkpoint write, lock, or
   runtime epoch publication. Model this ordering in a spy-driven integration
   contract that WP13 can reuse.
4. Assert `current_mutation_count` is always zero, including filesystem errors,
   cancellation, budget exhaustion, and internal validation failure. Inspection
   may read bytes/stat metadata only.
5. Provide only two positive continuations: initialize for `empty_supported`
   and recover for `current_supported`. Both return typed decisions; neither
   performs the action itself.
6. Do not provide `--force`, `--ignore-legacy`, waiver callback, environment
   switch, backend selector, “prefer current,” rescue read, auto rename, import,
   copy, export, or conversion hook.
7. Offline disposition guidance may name a future external/manual workflow but
   must not execute or bundle it. It must never claim that historical missions
   were migrated.
8. Add compile-time/API tests and spy call-order tests proving refusal occurs
   before every mutation-capable boundary. A classifier error also fails closed.
9. Add mutants for ignored result, post-sidecar invocation, force flag, importer
   callback, mutation during diagnosis, current-preference, and historical-as-
   empty; require each to be rejected by the contract tests/architecture guard.

### Validation

```bash
go test ./internal/controlroot -run 'Test.*Gate|Test.*Offline|Test.*ZeroMutation|Test.*Ordering' -count=3
```

## T063 — Classification matrix, properties, and semantic mutants

### Purpose

Prove the classifier is conservative, deterministic, bounded, and resistant to
renames, copied data, unsafe roots, and plausible look-alikes.

### Implementation guidance

1. Build a table-driven fixture matrix covering all seven classifications and
   all three actions. Store only minimal static fixtures under the owned
   `testdata` path when in-memory/temp fixtures cannot express the case.
2. Include empty/new root, valid current root, named historical DB, renamed/
   copied SQLite-header file, orphan WAL/SHM/journal, historical+current mixed,
   two current authority identities, malformed current metadata, and future
   unknown-version current state.
3. Include unsafe root/entry symlinks, traversal, foreign owner where safely
   simulatable, world/shared writable policy violations, hard links, special
   files, permission denial, concurrent replacement, device boundary, excessive
   depth/count/bytes, and cancellation.
4. Include look-alikes that must not become definite historical evidence:
   `.db` text, header phrase outside offset zero, prefix one byte short, random
   files named `control.db`, unrelated `-wal` text, and known names in comments
   or nested unrelated documentation.
5. Add property/fuzz tests for arbitrary filenames and bounded byte prefixes.
   Assert no panic, no path escape/follow, bounded output, valid classification,
   exact action mapping, canonical order, deterministic digest, and zero
   mutation count.
6. Add semantic mutants for filename-only classification, extension-only
   detection, symlink following, unknown-file ignore, truncated safe result,
   mixed-as-current, historical-as-empty, SQL subprocess/import, implicit
   importer, waiver/force, and nonzero mutation.
7. Require an exact stable mutant and fixture census. Removing a case must fail
   the census rather than reduce evidence silently.
8. Repeat property seeds and classification orders. Run the race detector over
   concurrent replacement and shared-reader tests.
9. Snapshot target content and directory entries before/after classification to
   prove no application mutation; do not treat access-time updates as a product
   write claim without platform support.
10. Run dependency inspection and `git diff --check`; ensure only owned paths
    changed and no generated database files remain.

### Validation

```bash
go test ./internal/controlroot -count=3
go test -race ./internal/controlroot -count=1
go list -deps ./internal/controlroot
git diff --check
```

## Definition of Done

- The seven classifications and three actions form a validated total mapping
  identical to the published schema.
- Root inspection is bounded, deterministic, symlink-safe, ownership-aware,
  conservative under errors, and read-only by API construction.
- Known historical files, exact SQLite headers, companions, and copied/renamed
  variants are detected without importing or executing SQL.
- Empty/current roots are accepted only with complete safe evidence; mixed,
  unsafe, malformed, and ambiguous roots can never “prefer current.”
- Every disposition has canonical findings, stable diagnosis, a deterministic
  root digest, and `current_mutation_count: 0`.
- The integration gate returns `OFFLINE_DISPOSITION_REQUIRED` before sidecar or
  current-state mutation and exposes no waiver, fallback, importer, selector,
  force, or conversion path.
- The fixture/property/mutant census covers valid roots, copied/renamed history,
  look-alikes, unsafe roots, resource exhaustion, and concurrent replacement.
- Focused, repeated, race, dependency, and diff checks pass with changes only
  inside the declared ownership surface.
- Completion is recorded through the event log:

```bash
spec-kitty agent tasks mark-status T059 T060 T061 T062 T063 --status done --mission gapdb-only-production-authority-01M1GWDJ
```

## Risks

- **False emptiness**: unreadable, unknown, or changing state is ambiguous or
  unsafe, never empty.
- **False certainty from names**: exact header/layout evidence distinguishes
  historical state from ordinary `.db` files and textual look-alikes.
- **Symlink/TOCTOU escape**: do not follow symlinks; bind reads to observed path
  identity where supported and refuse instability.
- **Unbounded inspection**: enforce count/depth/byte/time/finding budgets before
  allocation and fail closed when exhausted.
- **Atime confusion**: claim zero application writes, not universal absence of
  filesystem-managed access metadata changes.
- **Current-layout drift**: closed version/signature checks make future unknown
  layouts explicit rather than treating them as safe current state.
- **Accidental migration surface**: diagnosis returns evidence and guidance only;
  it cannot rename, import, convert, copy, start, connect, or mutate.

## Reviewer Guidance

Review the negative space as carefully as classification accuracy. Confirm the
package imports no SQL/SQLite or process-execution surface and that its public
interfaces cannot mutate the root. Trace every refusal to a pre-sidecar outcome
with mutation count zero and stable evidence.

Verify renamed/copy detection uses exact bounded content signatures, while
extension/name/text look-alikes remain ambiguous or unrelated according to the
closed registry. Exercise symlink, ownership, hard-link, special-file, budget,
and concurrent-replacement cases and confirm none silently collapse to empty.

Reject any force/waiver/import/fallback/backend-selection seam, a “prefer
current” mixed-root rule, unbounded traversal, symlink following, SQL parsing,
mutable diagnosis, nondeterministic findings, filename-only certainty, or
mutant census that can shrink without failure.
