---
work_package_id: WP09
title: One-Way Tasks Compatibility Projection
dependencies:
- WP05
- WP06
- WP07
requirement_refs:
- FR-003
- FR-025
- FR-026
- FR-028
- NFR-005
- NFR-008
- NFR-010
- NFR-012
- C-001
- C-002
- C-005
- C-006
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T048
- T049
- T050
- T051
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/projection/
create_intent:
- internal/projection/model.go
- internal/projection/tasks.go
- internal/projection/tasks_test.go
- internal/projection/atomic_file.go
- internal/projection/atomic_file_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/projection/model.go
- internal/projection/tasks.go
- internal/projection/tasks_test.go
- internal/projection/atomic_file.go
- internal/projection/atomic_file_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP09 - One-Way Tasks Compatibility Projection

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Provide a deterministic, human-readable `tasks.md` compatibility projection
from authoritative mission and WorkRevision state, with explicit provenance
and a prominent non-authoritative disclosure. The projection may be rendered
to bytes or atomically replaced on disk, but it must never be parsed, imported,
or treated as an input to mission, tracker, approval, or execution authority.

## Context

Spec Kitty is replacing the inherited Markdown task ledger with tracker-backed
work definitions and immutable executable graph generations. Some users and
tools still benefit from a familiar `tasks.md` summary, so Phase 3 retains it as
a one-way projection. Its value is comprehension and migration compatibility,
not control.

WP05 supplies the immutable WorkRevision v2 graph and generation identity.
WP06 supplies typed drift/reconciliation status. WP07 supplies authoritative
mission state and the pure next result. WP09 combines stable read values into a
package-local projection model and renders them. It does not query providers,
advance a mission, create approvals, write tracker work, or infer legal action
from Markdown.

Enforce these boundaries:

- Canonical state is the only input. Do not read `tasks.md` before rendering or
  compare it to decide whether canonical state changed.
- Every output identifies mission state version, WorkRevision ID, generation,
  graph digest, drift status, render time, and its non-authoritative nature.
- Rendering is deterministic for the same validated model and supplied render
  time. Map iteration, filesystem order, locale, and provider order must not
  affect bytes.
- Work identities, objectives, dependencies, criteria, write scopes, lanes,
  and relevant reconciliation facts come from the immutable/current read model,
  never from tracker status or private transcript context.
- The renderer emits presentation only. There is no `ParseTasks`, `Import`,
  `Apply`, bidirectional sync, checkbox update, or Markdown reader contract.
- Atomic file output either preserves the complete previous file or exposes the
  complete new file. Failure must not leave a truncated destination.
- Diagnostics are stable, bounded, and safe: no credentials, unrestricted
  artifact bodies, provider output, filesystem contents, or transcript text.
- Keep the package read-only with respect to domain authority. It may depend on
  public read values from mission/workgraph/artifact capabilities, but no
  authority or provider package may import projection to decide state.

## Branch Strategy

Use only the Spec Kitty lane worktree allocated for WP09 after WP05, WP06, and
WP07 are accepted. The merge target is
`feat/software-development-mission-method`. Do not branch from `main`, create a
manual worktree, reuse another package's lane, or edit dependency-owned source.
All implementation and tests must remain inside the exact five owned files.

Start implementation with:

```bash
spec-kitty agent action implement WP09 --agent codex
```

Before coding, read the projection sections of `spec.md`, `plan.md`,
`data-model.md`, `research.md`, `quickstart.md`, and
`contracts/mission-method-commands.md`.

### Subtask T048: Define the canonical projection read model

**Purpose**: Give the renderer one validated, immutable input assembled from
authoritative state without making the projection package an authority query
service.

**Steps**:

1. In `internal/projection/model.go`, define a package-local `TasksView` (or
   equivalently named value) containing mission identity and state version,
   current step, WorkRevision ID/generation/graph digest, typed drift status,
   supplied render time, and canonically ordered projected work and lane values.
2. Each projected work item must contain stable WorkID, title/objective,
   description or bounded design context needed for standalone comprehension,
   dependency WorkIDs, structured criteria, write scopes, lane identity, and
   any typed reconciliation status needed to explain current applicability.
   Do not include provider status as readiness or authority.
3. Reuse or translate public read-only mission/workgraph types without exposing
   their mutable backing collections. The model constructor must defensively
   copy slices/maps and validate all identities, positive versions/generation,
   digest shape, UTC render time, unique work/lane membership, and dependency
   references.
4. Accept only the closed drift states defined upstream (`clean`,
   `observation_only`, `changed`, plus an explicit not-checked value only if the
   upstream contract has one). Do not infer drift from provider revision or
   free-form text.
5. Canonically sort work by WorkID; dependencies, criteria, and scopes by their
   stable keys; and lanes/members/edges by ID. Normalize nil and empty
   collections so equivalent views produce equal output.
6. Define stable projection error kinds and bounded recovery categories for no
   finalized revision, corrupt read model, unsupported drift state, unsafe
   output path, and write/replace failure. Human messages are explanatory only.

**Files**:

- `internal/projection/model.go` — immutable view values, constructors,
  canonical ordering, validation, and projection diagnostics (approximately
  180–260 lines).
- `internal/projection/tasks_test.go` — model validation, copying, ordering, and
  stable error-kind cases.

**Validation**:

- Equivalent input permutations create one canonical view.
- Missing/current revision mismatch, duplicate work, unknown dependency,
  invalid lane membership, malformed digest, and invalid drift state fail with
  exact typed diagnostics.
- Caller mutation after construction cannot alter view accessors or later
  rendered bytes.
- Model construction performs no provider, store, filesystem, or mission
  mutation and does not accept Markdown bytes as input.

### Subtask T049: Render deterministic, fully disclosed tasks Markdown

**Purpose**: Produce a useful compatibility document that is unmistakably a
snapshot and contains enough context for a fresh reader without recreating a
task-state machine.

**Steps**:

1. In `internal/projection/tasks.go`, render UTF-8 Markdown from the validated
   canonical view into bytes or an `io.Writer`. The renderer must be pure: no
   clock reads, filesystem reads, provider calls, or hidden global state. Render
   time is an explicit input carried by the view.
2. Begin with a visually prominent warning such as **GENERATED,
   NON-AUTHORITATIVE PROJECTION**. State that edits, checkbox changes, deletion,
   and prose changes have no effect and that the tracker plus Spec Kitty query
   surfaces are the authoritative sources.
3. Include a source block with mission ID/state version/current step,
   WorkRevision ID/generation/graph digest, typed drift status, render timestamp,
   and a stable regeneration hint. A reader must be able to identify exactly
   which generation the file describes.
4. Render each work item with stable identity, objective/context, lane,
   dependencies, acceptance criteria including required/verifier metadata, and
   write scopes. Prefer ordinary headings, tables, and bullets over actionable
   checkboxes so the output does not invite agents to maintain a second ledger.
5. Escape or safely delimit user/provider-authored Markdown metacharacters,
   headings, fences, links, HTML, checkbox-like tokens, and control characters
   so content cannot forge the disclosure/header or restructure sibling items.
   Preserve meaning without executing HTML or embedding unsafe links.
6. Choose and document exact whitespace: LF line endings, one terminal newline,
   stable heading depth, stable empty-value text, no trailing whitespace, and no
   locale-sensitive formatting. Do not add a generated random identifier.
7. Keep rendering independent of output destination. The same view rendered to
   memory and passed to T050's writer must be byte-for-byte identical.

**Files**:

- `internal/projection/tasks.go` — pure deterministic renderer and Markdown-safe
  text helpers (approximately 180–280 lines).
- `internal/projection/tasks_test.go` — golden output, disclosure, escaping,
  ordering, UTF-8, whitespace, and determinism tests.

**Validation**:

- One hundred input permutations of the same view produce exactly one byte
  sequence; repeated rendering never mutates the view.
- Golden output contains every required provenance/disclosure field and every
  work dependency, criterion, scope, and lane fact.
- Adversarial titles/descriptions cannot inject a counterfeit authority notice,
  sibling heading, code fence, HTML execution, link target, or checklist state.
- No renderer function reads an existing file or exposes parse/import/apply
  behavior.

### Subtask T050: Replace destination files atomically and safely

**Purpose**: Make optional on-disk projection output crash-safe and path-bounded
without coupling the pure renderer to filesystem authority.

**Steps**:

1. In `internal/projection/atomic_file.go`, define a narrow writer that accepts
   an explicit trusted root, repository-relative destination, complete rendered
   bytes, and intended mode policy. Clean and resolve the destination, reject
   absolute paths, `..` escape, NULs, directories, and unsupported targets.
2. Validate the root and every existing destination parent. Reject symlinked
   destination/parent traversal or any resolved path outside the trusted root.
   Keep diagnostics path-bounded and do not expose unrelated filesystem data.
3. Create a uniquely named temporary file in the destination directory so the
   final rename stays on one filesystem. Apply the intended permissions, write
   all bytes, sync and close successfully, then atomically rename over the
   destination. Sync the parent directory where supported by the target policy.
4. On any pre-rename failure, close and remove only the exact temporary file and
   preserve the previous destination unchanged. Never use a broad glob, recursive
   removal, shell command, or unresolved environment variable for cleanup.
5. Define behavior for an absent destination, existing regular file, concurrent
   writers, cancellation before commit if context is accepted, permission
   failure, short write, sync failure, and rename failure. Readers must observe
   complete old or complete new bytes, never a partial document.
6. Keep atomic writing generic to complete projection bytes but do not add file
   reading, change detection, merge, checkbox preservation, reverse sync, or
   authority callbacks.

**Files**:

- `internal/projection/atomic_file.go` — bounded destination validation,
  same-directory temporary write, sync/rename, and exact cleanup.
- `internal/projection/atomic_file_test.go` — old/new visibility, path escape,
  symlink, fault, permissions, cleanup, and concurrent writer tests.

**Validation**:

- Injected faults at create, chmod, write, sync, close, rename, and directory
  sync leave either the complete previous file or complete replacement.
- Absolute, traversal, symlinked, directory, and outside-root destinations are
  refused before modifying the target.
- Concurrent writers leave one complete valid rendering and no leaked temporary
  files; no test observes truncated or mixed bytes.
- Deleting the destination and rerunning creates the same bytes for the same
  view and render time.

### Subtask T051: Prove projection independence and absence of a Markdown reader

**Purpose**: Demonstrate that corruption, editing, or deletion of `tasks.md`
cannot change canonical state and permanently guard the one-way dependency.

**Steps**:

1. In `tasks_test.go`, create a canonical two-item view from fixed mission,
   WorkRevision, drift, and next-state fixtures. Fingerprint all authoritative
   inputs, render the projection, then edit headings, criteria, dependencies,
   generation text, drift text, and checkbox-like content in the output. Assert
   the authoritative fingerprint and rerendered bytes remain unchanged.
2. Repeat corruption, arbitrary replacement, and deletion trials at least 100
   times. Regeneration must depend only on the canonical view and must overwrite
   or recreate the projection without reading or interpreting its prior bytes.
3. Add focused architecture/source checks in the owned tests proving the
   production projection package exports no parse/import/apply/update-from-file
   API and contains no Markdown parser dependency. Invoke the WP01 architecture
   policy seam where available to prove authority/provider packages may not
   import projection and projection cannot mint mission authority.
4. Include a mutant-style negative fixture or source graph showing that a
   hypothetical authority-to-projection import or Markdown reader path is
   rejected with the named architecture rule. Do not weaken or duplicate the
   central architecture policy inside production projection code.
5. Assert renderer and file-writer diagnostics contain no supplied credential,
   transcript, provider-output, or unrestricted content payload. Keep error
   classification stable under corrupt UTF-8/control-shaped input.
6. Measure a representative 100-item view. Rendering plus local atomic write
   should meet the 500 ms p95 target on the qualification platform, excluding
   external provider time; avoid fragile single-sample timing floors.
7. Run the targeted tests under the race detector and record T048–T051 complete
   only after the full validation block passes.

**Files**:

- `internal/projection/tasks_test.go` — 100-trial independence, no-reader,
  architecture, privacy, performance, and regeneration tests.
- `internal/projection/atomic_file_test.go` — delete/corrupt/replace and
  concurrency scenarios at the file boundary.

**Validation**:

```bash
go test ./internal/projection
go test -race ./internal/projection
go test ./internal/architecture ./internal/...
go vet ./internal/projection
gofmt -d internal/projection
```

- One hundred corrupt/edit/delete trials cause zero authoritative state changes
  and regenerate from the original canonical view.
- The package exposes no production Markdown reader or reverse synchronization
  API, and architecture mutants fail with the expected rule identity.
- A representative 100-item projection remains within the local performance
  budget without a brittle lower-bound assertion.
- Targeted and repository-wide internal package tests pass under normal and
  race builds.

## Definition of Done

- A validated immutable read model carries exact mission state, WorkRevision,
  generation, graph digest, drift, work, criteria, dependency, scope, and lane
  facts without provider status or Markdown input.
- Rendering is pure, deterministic, UTF-8, canonical, Markdown-safe, and
  includes a prominent non-authoritative warning plus complete source identity.
- The generated document uses presentation rather than an actionable checklist
  and can be deleted and regenerated without changing canonical state.
- Atomic output validates a trusted-root-relative destination, rejects escape
  and symlink hazards, and exposes only complete old or complete new files.
- There is no production parser, importer, apply command, checkbox updater,
  reverse synchronization path, or authority callback for `tasks.md`.
- Architecture checks preserve inward dependency direction: projection may read
  public authority values, but authority/provider code cannot depend on it.
- Corruption, edit, replacement, and deletion campaigns produce zero mission,
  tracker, approval, revision, decision, or next-state changes.
- Unit, race, architecture, vet, formatting, and 100-item performance evidence
  pass without exposing sensitive content.
- Record event-sourced subtask completion only after validation succeeds:

```bash
spec-kitty agent tasks mark-status T048 --status done
spec-kitty agent tasks mark-status T049 --status done
spec-kitty agent tasks mark-status T050 --status done
spec-kitty agent tasks mark-status T051 --status done
```

## Risks

- **Dual authority by convenience**: A parser added for compatibility can become
  a second state machine. Keep the boundary output-only and guard exported APIs.
- **Misleading stale output**: A readable file can look current after drift.
  Always disclose source state version, generation, digest, drift, and render
  time prominently.
- **Markdown injection**: Work prose can forge headings or warnings. Escape or
  delimit untrusted text with adversarial golden tests.
- **Partial replacement**: Direct writes can truncate a useful prior file.
  Write/sync/close a same-directory temporary and rename atomically.
- **Path escape and symlink races**: A caller-selected destination can leave the
  trusted root. Reject unsafe components and symlink traversal; keep the
  single-machine/filesystem authority assumption explicit.
- **False independence tests**: Comparing only output does not prove state was
  untouched. Fingerprint canonical inputs and use collaborators that fail on
  any attempted authority mutation or existing-file read.
- **Brittle performance evidence**: Tiny elapsed-time floors create flaky CI.
  Measure representative p95/upper bounds and keep provider time out of scope.

## Reviewer Guidance

Treat any Markdown read path as a release-blocking authority regression. Search
the production package for parsing, importing, applying, checkbox preservation,
existing-file merge, and reverse-sync concepts. The only legal data direction
is canonical typed state to projection bytes to optional atomic file.

Inspect the disclosure before presentation polish: it must be impossible for a
reasonable reader to mistake the file for canonical task state, and it must
identify the exact mission state version and WorkRevision generation/digest.
Then test adversarial work prose for heading, fence, link, HTML, control-byte,
and checklist injection without losing human comprehension.

Review atomic replacement as a safety boundary. Confirm destination containment,
symlink refusal, same-directory temporary creation, complete write/sync/close
before rename, precise cleanup, and preservation of the old destination on every
injected failure. No broad deletion is acceptable.

Finally, run the architecture and 100-trial corruption suite. Confirm the tests
would fail if authority imported projection, if a reader appeared, or if the
renderer inspected the existing destination. Cosmetic differences are secondary;
the product law is that editing or deleting `tasks.md` can never change what
work exists, what may run, or what may be accepted.
