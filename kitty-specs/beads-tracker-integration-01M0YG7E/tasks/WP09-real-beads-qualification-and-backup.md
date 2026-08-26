---
work_package_id: WP09
title: Real Beads Qualification and Backup
dependencies:
- WP06
- WP07
requirement_refs:
- FR-003
- FR-004
- FR-005
- FR-007
- FR-008
- FR-009
- FR-010
- FR-011
- FR-012
- FR-018
- FR-019
- FR-020
- FR-023
- NFR-001
- NFR-002
- NFR-006
- NFR-011
- NFR-013
planning_base_branch: feat/beads-tracker-integration
merge_target_branch: feat/beads-tracker-integration
branch_strategy: Implement in the Spec Kitty lane allocated from accepted WP06 and WP07 dependency candidates; merge the reviewed result into feat/beads-tracker-integration. Do not choose or reconstruct the dependency base manually.
subtasks:
- T043
- T044
- T045
- T046
- T047
- T048
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/tracker/beads/
create_intent:
- internal/tracker/beads/real_integration_test.go
- internal/tracker/beads/worktree_integration_test.go
- internal/tracker/beads/backup_integration_test.go
- internal/testcampaign/cmd/beads-qualification/**
- scripts/install-qualified-beads.sh
execution_mode: code_change
model: ''
owned_files:
- internal/tracker/beads/real_integration_test.go
- internal/tracker/beads/worktree_integration_test.go
- internal/tracker/beads/backup_integration_test.go
- internal/testcampaign/cmd/beads-qualification/**
- scripts/install-qualified-beads.sh
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP09 - Real Beads Qualification and Backup

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Qualify the production adapter against the actual Beads v1.2.2 executable and
a disposable Dolt workspace. Produce reproducible evidence for catalog, graph,
discussion, assignment observation, concurrent identity, latency, offline use,
JSONL interchange, and native backup recovery without modifying the project
checkout, user configuration, or any non-temporary provider state.

## Context

The fake provider and dishonest subprocess harness prove portable contracts and
rare failure behavior. They cannot prove the real `bd` command shapes, embedded
Dolt lifecycle, linked-worktree convergence, or native backup semantics. This
package supplies that external evidence while preserving the same authority
boundary: Beads owns visible work facts; Spec Kitty alone owns run, lease,
workspace, review, acceptance, and integration authority.

The initial compatibility tuple is deliberately narrow:

```text
Beads version:      1.2.2
JSON envelope:      BD_JSON_ENVELOPE=1
schema_version:     1
source tag commit:  6c124203e771433a3550c348771a5b5e27fd3c21
```

Do not broaden this range because a newer semantic version appears available.
Beads v1.2.2 is a recovery release after non-monotonic v1.2.0/v1.2.1 behavior;
support follows evidence, not version ordering.

The test binary and installer must honor these safety rules:

- Never initialize Beads in this repository or any user project.
- Never install hooks, edit agent instruction files, enable Beads Git syncing,
  or run interactive setup.
- Never trust an arbitrary `bd` found on `PATH`; use an explicit absolute path
  whose version and SHA-256 digest are verified.
- Permit network access only during explicit installation. Qualification after
  installation must be capable of running offline.
- Put repositories, worktrees, Beads data, backups, restored data, logs, and
  evidence beneath independently created temporary directories.
- Never run restore, removal, or overwrite operations against a caller-supplied
  existing workspace. Destructive restore is allowed only inside a validated
  test-owned temporary root.
- Parse only documented JSON protocol output for product state. Setup commands
  may use exit status, but their human text is never semantic input.

## Branch Strategy

Spec Kitty owns dependency and lane selection. Start only from the finalized
WP06/WP07 dependency base and work in the generated WP09 lane. Merge the
accepted candidate into `feat/beads-tracker-integration`. Do not branch from
`main`, reimplement dependency work, or alter shared campaign/CI files owned by
WP10.

Begin with:

```bash
spec-kitty agent action implement WP09 --agent codex
```

Read the mission spec, plan, research, data model, quickstart, both Markdown
contracts, and `contracts/qualification-evidence.schema.json`. Inspect accepted
WP03–WP07 APIs before coding. Stay within the exact owned files.

### Subtask T043: Create a checksum- and version-pinned installer

**Purpose**: Obtain or build exactly the qualified Beads executable in an
operator-selected tool directory and refuse any binary whose provenance,
version, or digest is not allowlisted.

**Steps**:

1. Implement `scripts/install-qualified-beads.sh` as a strict, non-interactive
   installer for exactly `github.com/steveyegge/beads/cmd/bd@v1.2.2`. Pin the
   source/tag identity and the approved artifact/build digest in reviewed
   constants; do not follow `latest`, branches, redirects without validation,
   or an unversioned module request.
2. Require an explicit destination directory outside the repository by default.
   Resolve it to an absolute path, create only the final tool subdirectory, and
   fail if the target would overwrite an unrelated existing file. Never use a
   broad home/workspace path as a cleanup target.
3. Build with the documented embedded-Dolt requirements (`CGO_ENABLED=1` and
   the characterized `gms_pure_go` tags) or install a platform-specific release
   whose checksum is separately allowlisted. Record which path produced the
   binary; do not treat locally different builds as the same digest.
4. Compute SHA-256 before execution and compare it byte-for-byte with the
   allowlist for the exact GOOS/GOARCH/build method. Then invoke the absolute
   binary using the schema-1 JSON version command and verify `1.2.2`.
5. Emit a small machine-readable installation result containing version,
   digest, absolute executable path, source version, GOOS/GOARCH, and build
   method. Do not emit the environment, credentials, module cache, or raw
   command transcripts.
6. Make repeated installation idempotent only when the existing binary already
   matches the same allowlisted digest and version. On mismatch, fail safely
   and require an explicit new destination; do not silently replace it.
7. Keep downloads/build caches and partial output in a unique temporary
   directory with a targeted cleanup trap. A failed install must not dirty the
   repository or modify global Go/user configuration.

**Files**:

- `scripts/install-qualified-beads.sh` — pinned installation, verification,
  idempotency, cleanup, and machine-readable result.

**Validation**:

- Correct v1.2.2 artifact/build installs and reports its verified digest.
- Wrong version, wrong digest, missing build requirements, partial download,
  and existing mismatched destination all fail before qualification.
- A repository status snapshot is identical before and after installation.
- Network-disabled reuse of an already verified installation succeeds without
  attempting an update.

### Subtask T044: Build a disposable stealth provider fixture

**Purpose**: Give every real-provider test an isolated, reproducible Beads
workspace with explicit lifecycle and no side effects outside its temporary
root.

**Steps**:

1. Under `internal/testcampaign/cmd/beads-qualification/`, implement the test
   driver and shared fixture support. Require `BEADS_BIN` or an explicit flag
   containing the absolute verified executable; verify version, schema, and
   digest again before any provider initialization.
2. Allocate a fresh top-level temporary root and create separate subdirectories
   for the Git repository, canonical Beads workspace, linked worktrees, native
   backup, restore target, and evidence output. Record canonical paths and
   reject any path escaping the top-level root.
3. Initialize Git only in the disposable repository. Initialize Beads with
   non-interactive, stealth, skip-agent, and skip-hook controls. Set explicit
   `BEADS_DIR`, `BD_NON_INTERACTIVE=1`, and `BD_JSON_ENVELOPE=1`; never rely on
   current-directory discovery.
4. Snapshot the real repository's status and relevant agent/hook configuration
   read-only before the campaign, then assert they are unchanged afterward.
   Do not read or report credential values.
5. Provide deterministic seed helpers for mapped work, structured criteria,
   dependencies, comments, and external human mutations using the same adapter
   or documented JSON commands. Do not use Dolt SQL to shortcut setup.
6. Cleanup only the fixture-owned temporary root after all child processes are
   reaped. Preserve it only behind an explicit debugging option, label retained
   paths as sensitive, and never enable retention in CI by default.

**Files**:

- `internal/testcampaign/cmd/beads-qualification/**` — qualification entrypoint,
  fixture lifecycle, verified binding, scenario registry, and safe cleanup.

**Validation**:

- Parallel fixture instances cannot share a workspace or cleanup each other.
- Initialization produces no hooks or agent-file edits in either repository.
- Failure at each setup boundary cleans only the owned temporary root.
- The checked-out repository and user-visible configuration remain unchanged.

### Subtask T045: Exercise real catalog, graph, criteria, and discussion

**Purpose**: Prove the provider-neutral capabilities survive an actual Beads
v1.2.2 round trip rather than only the fake/harness representations.

**Steps**:

1. In `internal/tracker/beads/real_integration_test.go`, gate real tests on the
   explicitly verified `BEADS_BIN`. Skip with a precise reason in ordinary unit
   runs; the qualification driver treats a missing/skipped required scenario as
   non-admissible, never pass.
2. Preflight the disposable binding and assert exact version/schema, workspace
   identity, storage mode, declared capabilities, and no mutation during
   inspection. Verify incompatible identity is rejected before create.
3. Create at least 100 mapped work items with stable `WorkID`, project metadata,
   operation provenance, title, description, design context, and structured
   criteria. Read them back and compare stable criterion IDs, verifier kind,
   required flag, evidence hint, mapping uniqueness, and observation revisions.
4. Exercise provider-owned update while preserving unrelated metadata. Directly
   modify one issue through documented `bd --json`, re-read, and prove the
   canonical observation revision detects content drift even where timestamps
   do not provide sufficient distinction.
5. Add/list/remove blocking dependencies with asymmetric IDs and verify exact
   `blocked depends on blocker` direction, ready projection diagnostics, cycle
   rejection, and no use of provider readiness as execution authority.
6. Append/list adapter comments with operation markers, seed ordinary human
   comments, and verify ordering, exact replay, unknown optional fields, and
   non-leakage of bodies in diagnostics.
7. Exercise visible assignment observation and atomic claim only at the level
   the adapter declares. Assert no tracker action mints a lease, workspace,
   review verdict, acceptance result, or integration result.
8. Measure 100 sequential healthy preflights and 200 warm single-item reads
   over the 100-item catalog. Record p50/p95/max and host; assert p95 targets of
   <=1 second and <=500 ms respectively on the qualification host.

**Files**:

- `internal/tracker/beads/real_integration_test.go` — real-provider catalog,
  criteria, graph, discussion, assignment-observation, and latency scenarios.

**Validation**:

- All declared real capabilities pass round-trip without internal imports,
  direct SQL, human-output parsing, or network access after installation.
- Latency sample counts and percentiles satisfy NFR-001/NFR-002 or produce a
  clear failing/non-admissible record with host details.
- Unknown optional provider fields remain compatible; missing required fields
  and unsupported schema/version fail before mutation.

### Subtask T046: Prove worktree convergence and concurrent identity

**Purpose**: Demonstrate that separate Git worktrees share one explicit provider
workspace while respecting Beads' documented writer mode and maintaining unique
Spec Kitty mappings under contention.

**Steps**:

1. In `internal/tracker/beads/worktree_integration_test.go`, create two linked
   Git worktrees beneath the fixture root. Give both adapters the same absolute
   canonical Beads binding rather than allowing local discovery.
2. From each worktree, inspect identity and prove byte-for-byte equivalent
   workspace identity, version, schema, and concurrency mode. Point one adapter
   at a second disposable Beads directory and prove mutation fails with
   `workspace_mismatch` before spawning the mutating command.
3. Exercise the documented embedded-mode writer serialization with two OS
   processes and prove no lock corruption or false multi-writer capability.
   Separately run the 1,000-create contention requirement only in a provider
   mode qualified for concurrent writers; do not pretend an adapter mutex turns
   embedded mode into provider-native concurrency.
4. For the supported multi-writer mode, create at least 1,000 distinct works
   concurrently across processes/worktrees. Assert zero Beads ID collisions,
   duplicate Spec Kitty mappings, lost creations, or foreign workspace writes.
5. Cross-read every mapping from the opposite worktree and compare canonical
   provider observations. Record mode, process count, attempted/terminal/pass
   counts, collisions, duplicates, and duration.
6. Ensure cleanup removes worktrees through targeted Git operations before the
   fixture root is removed. Never register or delete a worktree outside the
   validated temporary repository.

**Files**:

- `internal/tracker/beads/worktree_integration_test.go` — two-worktree identity,
  mismatch refusal, embedded serialization, and supported-mode contention.

**Validation**:

- Both worktrees observe one canonical workspace and each other's writes.
- Divergent workspace identity refuses before mutation.
- Embedded mode is reported/treated as adapter-serialized, not provider-native.
- The qualified multi-writer run completes 1,000 creates with zero identity or
  mapping collisions; absence of a supported mode is non-admissible, not pass.

### Subtask T047: Verify assignment observation and safe refusal

**Purpose**: Prove against real Beads that visible assignment is useful but
cannot transfer authority or justify unsafe automatic cleanup.

**Steps**:

1. Add real assignment scenarios to `real_integration_test.go`. Project a mapped
   actor with an operation marker only through the guarantee level the adapter
   declares, then observe the actual assignee, marker, provider revision, and
   mutation outcome.
2. Confirm the provider projection does not create or extend a Spec Kitty lease
   and does not allocate or transfer a workspace. Provider status/assignment is
   evidence only.
3. Simulate a human using documented `bd --json` to change assignment after the
   Spec Kitty projection. Re-observe and prove the changed revision/value/marker
   becomes `attribution_lost`; preserve the human's exact value.
4. Request compensation when the apparent state still matches the original
   projection. Because v1.2.2 exposes no primitive enforcing expected revision,
   assignee, and marker at mutation time, require
   `safe_compensation_unsupported` and verify no provider mutation occurred.
5. Repeat the human-change scenario enough times to expose observation/write
   races within the bounded qualification budget. Count every attempted,
   terminal, preserved, and failed outcome; zero human overwrites are allowed.
6. Redact actors and markers where evidence policy requires it. Never include
   issue descriptions, comment bodies, raw stderr, credentials, or environment
   dumps in the qualification record.

**Files**:

- `internal/tracker/beads/real_integration_test.go` — real assignment projection,
  direct-human change, authority-negative, and safe-refusal scenarios.

**Validation**:

- Direct human assignment always survives automated recovery attempts.
- Matching pre-read state still cannot promote unsupported cleanup to CAS.
- Result kinds are `attribution_lost` or `safe_compensation_unsupported` as
  applicable, never `compensated`.
- Beads assignment never creates local execution authority.

### Subtask T048: Qualify interchange, native backup, restore, and evidence

**Purpose**: Prove both portable data interchange and a genuinely restorable
native backup while reporting them as different capabilities.

**Steps**:

1. In `internal/tracker/beads/backup_integration_test.go`, export the seeded
   workspace through the bounded JSONL path. Verify record count, mappings,
   criteria metadata, dependency facts, and comments without loading unbounded
   output or calling the export a complete backup.
2. Run Beads' supported Dolt-native filesystem backup initialization and sync
   against a fixture-owned empty backup directory. Characterize status through
   documented machine output or exit status only as permitted by the contract;
   do not parse prose into product state.
3. Allocate a separate empty restore workspace inside the same validated temp
   root. Before any restore, prove the source, backup, and destination are
   distinct descendants of that root and the destination contains no user data.
   Refuse symlinks, path escape, repository roots, home paths, or preexisting
   non-fixture content.
4. Restore only into that disposable destination, then bind a fresh adapter and
   inspect recovered catalog mappings, criteria, dependencies, comments, and
   workspace identity. Never invoke destructive restore on the live fixture
   binding or a caller-supplied workspace.
5. Test export success with backup failure, backup success with restore failure,
   and restore success with inspection mismatch. None may be collapsed into a
   pass; emit precise capability/scenario failure.
6. Have the qualification command aggregate schema-1 redacted evidence matching
   the contract: source commit/tree, provider version/digest, protocol schema,
   storage mode, GOOS/GOARCH, timestamps, exact scenario census, latency
   distributions, authority-invariant failures, safe issues, and verdict.
7. Write evidence only to an explicit path inside the fixture/evidence root.
   Missing required scenarios, skipped provider tests, digest/version mismatch,
   incomplete census, restore mismatch, or invariant failure produces `fail` or
   `non_admissible`, never pass.

**Files**:

- `internal/tracker/beads/backup_integration_test.go` — JSONL interchange,
  native backup, guarded temporary restore, and recovered-state inspection.
- `internal/testcampaign/cmd/beads-qualification/**` — scenario orchestration,
  latency/census aggregation, redaction, and evidence emission.

**Validation**:

- JSONL export and native backup are reported as distinct operations.
- A restored disposable workspace contains the expected work, mappings,
  criteria, dependency edges, and comments.
- Every restore target is proven temporary and empty; negative path/symlink
  cases refuse before any destructive command.
- Evidence validates against the mission schema and contains no secret/body/raw
  transcript sentinels.

## Test Strategy

Keep ordinary `go test ./...` provider-independent by requiring an explicit,
verified `BEADS_BIN` for real integration tests. The dedicated qualification
entrypoint turns missing/skipped real tests into non-admissible evidence. Once
installed, run the full suite with network access disabled or actively checked
to demonstrate NFR-011.

Use one top-level test-owned temporary root per campaign and validate every
subpath before setup, cleanup, backup, or restore. Capture repository status
before/after. Use direct subprocess argv and structured JSON; never shell-parse
provider output. Record timings around adapter calls rather than setup/download.

Run focused tests normally and under the race detector. Contention tests must
use separate OS processes and declare the actual provider mode. Avoid sleeps as
proof; use bounded polling and observable terminal conditions.

## Definition of Done

- The pinned installer verifies exact v1.2.2 identity and an allowlisted SHA-256
  before use, is idempotent only for an exact match, and never dirties the repo.
- Qualification initializes only a disposable stealth/non-interactive Beads
  workspace and installs no hooks or agent files.
- Real catalog, mapping, criteria, graph, readiness, discussion, assignment
  observation, drift, and capability-limit scenarios pass offline.
- Two linked worktrees resolve one canonical workspace; mismatch refuses before
  mutation; supported-mode contention completes 1,000 creates with zero ID or
  mapping collisions.
- Preflight and warm-read latency samples satisfy NFR-001/NFR-002 with host and
  distribution recorded.
- Human assignment changes are never overwritten; unsupported safe compensation
  never issues a provider mutation.
- JSONL interchange is bounded and explicitly distinct from native backup.
- Native backup restores into a separately validated empty temporary workspace,
  whose provider-neutral state matches expected data.
- Evidence records the exact compatibility tuple, digest, mode, source revision,
  scenario census, timings, invariant count, and honest verdict without secrets.
- Missing/skipped required scenarios and mismatched evidence cannot pass.
- Tests pass normally and under `-race`; cleanup leaves no child, worktree, repo
  mutation, user configuration change, or non-temporary artifact.
- No restore/delete/overwrite command can target outside the validated fixture
  root, and no file outside `owned_files` changes.
- Record task completion through Spec Kitty's event log:

```bash
spec-kitty agent tasks mark-status T043 --status done
spec-kitty agent tasks mark-status T044 --status done
spec-kitty agent tasks mark-status T045 --status done
spec-kitty agent tasks mark-status T046 --status done
spec-kitty agent tasks mark-status T047 --status done
spec-kitty agent tasks mark-status T048 --status done
```

## Risks

- **Supply-chain ambiguity**: local builds differ by toolchain/platform. Bind
  every digest to build method and platform; never bless a runtime-computed hash.
- **Accidental repository mutation**: Beads initialization can touch hooks and
  agent files. Use only disposable repositories and explicit stealth controls.
- **Unsafe cleanup/restore**: path bugs could destroy real data. Require a
  unique validated temp root, reject symlinks/escapes, and restore only to an
  empty child directory.
- **False concurrency claims**: embedded serialization is not native CAS or
  multi-writer safety. Run 1,000-create evidence only in a qualified mode.
- **Flaky performance evidence**: process startup and shared runners vary.
  record host/distribution and fail honestly; do not loosen targets dynamically.
- **False backup terminology**: JSONL omits Dolt history. Label it interchange
  and separately prove native restore.
- **Human change loss**: pre-read comparison cannot fence direct `bd` writers.
  Real tests must show safe refusal and exact preservation.
- **Evidence leakage**: work bodies, comments, env, and stderr may be sensitive.
  Serialize an allowlisted schema and scan with secret sentinels.

## Reviewer Guidance

Begin by proving all filesystem targets. The installer destination, provider
workspace, worktrees, backup, restore destination, and evidence path must be
absolute and intentionally scoped. Reject any restore or recursive cleanup that
can accept repository, home, symlink-escaped, or preexisting user paths.

Verify the installer checks a reviewed allowlist before executing the binary;
a digest calculated and immediately trusted at runtime is not verification.
Confirm version `1.2.2`, schema `1`, build method, and platform are all bound to
the evidence, and that qualification works without network after installation.

Review real-provider scenarios for semantic truth: exact work/criteria mapping,
asymmetric dependency direction, human comments, readiness as diagnostics only,
two-worktree identity, actual concurrency mode, and human assignment survival.
Do not accept embedded adapter locking as proof of provider-native concurrency
or read-then-write cleanup as safe compensation.

Finally restore the native backup only into a fresh test-owned destination and
inspect through the adapter. Confirm JSONL is called interchange, not backup.
Validate exact scenario census, latency samples, source/provider identity,
redaction, and verdict rules; any skipped required scenario, invariant failure,
or restore mismatch must prevent a passing record.
