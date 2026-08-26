---
work_package_id: WP04
title: Canonical Binding and Provider Health
dependencies:
- WP03
requirement_refs:
- C-003
- C-005
- C-006
- FR-003
- FR-004
- FR-005
- FR-006
- FR-017
- FR-019
- NFR-001
- NFR-011
planning_base_branch: feat/beads-tracker-integration
merge_target_branch: feat/beads-tracker-integration
branch_strategy: Planning artifacts for this mission were generated on feat/beads-tracker-integration. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/beads-tracker-integration unless the human explicitly redirects the landing branch.
subtasks:
- T016
- T017
- T018
- T019
- T020
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/tracker/beads/
create_intent:
- internal/tracker/beads/binding.go
- internal/tracker/beads/binding_test.go
- internal/tracker/beads/inspect.go
- internal/tracker/beads/inspect_test.go
- internal/tracker/beads/writer_lock.go
- internal/tracker/beads/writer_lock_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/tracker/beads/binding.go
- internal/tracker/beads/binding_test.go
- internal/tracker/beads/inspect.go
- internal/tracker/beads/inspect_test.go
- internal/tracker/beads/writer_lock.go
- internal/tracker/beads/writer_lock_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP04 - Canonical Binding and Provider Health

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Bind every lane of one project to the same explicit provider workspace and make
compatibility, health, capability, and writer-mode facts observable before any
mutation. The result must turn environmental assumptions into validated typed
state: absolute executable identity, allowlisted version and schema, canonical
workspace identity, storage mode, read-only state, and exact concurrency limits.

This work package also supplies the cross-process serialization required by
embedded single-writer mode. That lock coordinates Spec Kitty processes only;
it must never be represented as provider-side compare-and-swap or protection
against a human invoking the provider directly.

Implementation command:

```bash
spec-kitty agent action implement WP04 --agent codex
```

## Context

WP01 provides provider-neutral capability, binding, inspection, health, and
diagnostic values. WP02 admits provider-specific code only in the classified
adapter tier. WP03 provides the bounded typed process runner and schema-1 JSON
decoder. Build this package on those contracts rather than creating alternate
command execution, error, or capability models.

Beads can discover state from a current repository, redirect files, environment,
or nested directories. That behavior is convenient for a human CLI but unsafe
for concurrent Spec Kitty lanes: two linked worktrees could silently select
different mutable catalogs. This WP therefore accepts an explicit project
binding, canonicalizes it once, passes it to every command, and verifies that
provider observations still describe the bound workspace.

The initial qualification target is exactly Beads v1.2.2 with JSON envelope
schema 1. Version ordering is not a capability proxy: v1.2.2 is a recovery
release and later-looking artifacts may expose different unqualified schemas.
Capability reporting must derive from the characterized version, schema,
storage mode, and implemented adapter ports—not wishful semantic-version ranges.

Inspection is read-only. It may report missing state and precise operator
guidance, but it may not initialize a database, repair one, install hooks, edit
agent instructions, change Git configuration, or silently bind a nearby
workspace. Disposable qualification setup belongs to a later operator/test
surface, not to discovery.

### Non-negotiable boundaries

1. One project binding names one absolute canonical executable and one absolute
   canonical provider workspace owned by one machine/filesystem authority.
2. Current working directory is never sufficient binding input, and a lane
   worktree may not override the canonical workspace through ambient discovery.
3. Every mutation requires successful compatible preflight for its exact
   executable digest, protocol schema, workspace identity, and concurrency mode.
4. Read-only inspection never repairs or initializes provider state.
5. Embedded mode serializes adapter-started writes across OS processes.
6. Server mode permits concurrent writes only after its server identity and
   health have been qualified explicitly.
7. Local writer serialization is not CAS, does not fence direct writers, and
   does not upgrade guarded-update or safe-compensation capabilities.
8. No binding or provider fact may mint a Spec Kitty run, lease, fence,
   workspace generation, review verdict, or integration permission.

## Branch Strategy

Work only in the Spec Kitty lane allocated after WP03 has been accepted. The
planning base and merge target are both `feat/beads-tracker-integration`.
Spec Kitty determines the dependency-specific base; do not choose or recreate
one manually.

Do not edit the primary checkout, create extra worktrees, merge to `main`, or
push directly to a protected branch. Change only the six files listed in
`owned_files`.

## T016: Define binding values and canonical workspace identity

**Purpose**: Represent one explicit, immutable project-to-provider binding that
resolves identically from every linked Git worktree.

**Files**:

- `internal/tracker/beads/binding.go`
- `internal/tracker/beads/binding_test.go`

**Implementation requirements**:

1. Implement the adapter-side construction and validation for the
   provider-neutral `TrackerBinding` introduced by WP01. Reuse its public value
   semantics; do not define a second competing portable binding struct.
2. Require a durable project identity, absolute executable path, absolute
   workspace path, supported protocol schema, storage/concurrency mode, and
   explicit read-only setting. Zero or unknown enum values must be rejected.
3. Canonicalize paths with `filepath.Abs`, `filepath.Clean`, and symlink
   evaluation when the target exists. Preserve a typed failure when a path is
   missing or cannot be resolved; do not fall back to the caller's working
   directory or PATH.
4. Resolve the executable once. The validated binding carries the resolved
   absolute path and its file identity; later commands do not search PATH again.
5. Derive `WorkspaceIdentity` from a versioned, length-framed canonical encoding
   of stable workspace facts, including canonical path and provider-reported
   database identity where v1.2.2 supplies one. Do not hash current worktree,
   process ID, timestamps, health, or transient lock state.
6. Use a collision-resistant full digest for identity and a stable textual
   encoding suitable for equality and diagnostics. Do not expose raw provider
   database contents or credentials in that encoding.
7. Keep identity deterministic across relative spellings, `.`/`..`, and symlink
   aliases that resolve to the same workspace. Distinct existing workspaces
   must not compare equal merely because they share a basename.
8. Defensively copy caller-provided byte slices or maps. A validated binding
   cannot change because the construction input is later mutated.

**Tests**:

- equivalent absolute, relative, cleaned, and symlinked inputs converge;
- two distinct workspace directories produce distinct identities;
- a relative executable, missing workspace, unknown mode, or empty project ID
  returns a typed invalid binding;
- identity remains stable across two simulated linked-worktree working dirs;
- no test writes hooks, Git config, agent files, or provider state.

**Exit evidence**: `go test ./internal/tracker/beads -run 'Binding|WorkspaceIdentity'`.

## T017: Enforce version, schema, digest, and workspace preflight before mutation

**Purpose**: Produce a mutation-readiness proof only after the exact configured
provider surface is known to be compatible and unchanged.

**Files**:

- `internal/tracker/beads/binding.go`
- `internal/tracker/beads/inspect.go`
- their focused tests

**Implementation requirements**:

1. Hash the resolved executable bytes with SHA-256 using bounded streaming I/O.
   Record lowercase hexadecimal identity and compare it with the binding's
   expected digest where one is configured or required by policy.
2. Invoke only WP03's typed read-only commands to obtain executable version,
   schema/database information, provider workspace location/identity, and mode.
   Do not introduce a shell command or parse human-formatted `version`, `where`,
   `info`, or `doctor` output.
3. Accept exactly executable version `1.2.2` and protocol envelope schema `1`
   for mutation readiness. Return distinct `version_skew` and `schema_skew`
   diagnostics with stable recovery codes.
4. Compare the provider-observed canonical workspace with the configured
   binding. Any redirect, nested discovery, changed `BEADS_DIR`, or mismatched
   provider database identity produces `workspace_mismatch` before a mutating
   subprocess starts.
5. Verify executable digest and path at preflight, then carry an immutable
   readiness result tied to those observations. Do not use version output alone
   as binary identity.
6. Make readiness explicit in the adapter call path: mutation helpers must
   require a successful readiness value or execute a guard that completes
   before writer-lock acquisition and command start. A zero/stale/incompatible
   result is a typed refusal.
7. Keep a preflight cache, if any, process-local, short-lived, keyed by every
   security-relevant binding fact, and invalidated by executable/workspace file
   identity changes. Correctness must not depend on caching.
8. Preserve WP03's mutation-outcome classification. A preflight failure is
   `not_started`; it cannot become ambiguous because no mutation was launched.

**Tests**:

- wrong executable version refuses before the mutation spy is called;
- future, zero, and absent schemas refuse before writer-lock acquisition;
- changed executable bytes fail digest verification;
- workspace redirection and nested-workspace selection fail deterministically;
- a compatible binding produces one immutable readiness result;
- repeated preflight does not modify executable, workspace, repo, or user files.

**Exit evidence**: a mutation-spy test proves refusal order, not merely the final
error kind.

## T018: Implement read-only health and capability inspection

**Purpose**: Give operators and application services a complete typed report
without granting inspection permission to fix the system it observes.

**Files**:

- `internal/tracker/beads/inspect.go`
- `internal/tracker/beads/inspect_test.go`

**Implementation requirements**:

1. Implement the provider-neutral `Inspector` contract from
   `contracts/tracker-capabilities.md`: binding, exact capabilities, health,
   and zero or more typed diagnostics.
2. Distinguish healthy, degraded/read-only, incompatible, unavailable, and
   lock-contended observations as defined by WP01 values. A useful read-only
   report may be returned with diagnostics even when mutation readiness fails.
3. Derive capability levels from characterized v1.2.2 behavior plus the
   configured storage mode. Explicitly report unguarded catalog update,
   content-derived observation revision, unsupported arbitrary safe
   compensation, and the correct concurrent-writer level.
4. Do not claim an operation merely because a command appears in help or a
   later provider release. Report only ports this adapter has implemented and
   qualified at the current dependency revision.
5. Use only no-fix/no-init protocol requests. If a command supports a repair
   flag, make that flag unrepresentable through the inspection constructor.
6. Return stable recovery codes for missing executable, unavailable workspace,
   malformed protocol, version/schema skew, workspace mismatch, read-only
   storage, and unhealthy server mode. Provider prose is cause detail only and
   never selects a branch.
7. Keep routine diagnostics bounded and free of credentials, environment
   values, descriptions, criteria text, comments, and raw stdout/stderr.
8. Sort multi-diagnostic output in deterministic semantic order so evidence and
   tests do not depend on map iteration or subprocess scheduling.

**Tests**:

- compatible embedded and server fixtures report different exact concurrency
  levels without changing mutation guarantees;
- missing/unavailable/skewed fixtures return stable health and recovery codes;
- a future unknown optional JSON field does not break schema-1 inspection;
- fake executable and workspace snapshots are byte-identical before and after;
- source/argv assertions prove no `init`, `fix`, hook, agent, or Git setup path.

**Exit evidence**: `go test ./internal/tracker/beads -run 'Inspect|Health|Capability'`.

## T019: Serialize embedded writers and qualify server-mode concurrency

**Purpose**: Honor the provider's single-writer embedded mode across independent
Spec Kitty processes without overstating what the adapter lock can protect.

**Files**:

- `internal/tracker/beads/writer_lock.go`
- `internal/tracker/beads/writer_lock_test.go`

**Implementation requirements**:

1. Implement an OS-visible advisory writer lock rooted at the canonical binding,
   not at a lane worktree. All processes using the same binding must derive the
   same lock identity/path.
2. Acquire with context cancellation and a bounded policy. Translate exhausted
   contention to typed `lock_contended`; do not spin forever or silently run the
   provider command concurrently.
3. Hold the lock across the complete mutating subprocess lifecycle and its
   immediate result classification. Release it on every return, panic-safe
   cleanup path, timeout, and process exit.
4. Ensure a crashed lock holder does not leave permanent logical ownership.
   Prefer kernel-released advisory locking already used elsewhere in the module
   over ad hoc PID files whose stale ownership is guessed.
5. Keep lock files permission-restricted and avoid provider payloads, secrets,
   actor IDs, work descriptions, or comments in names or contents.
6. In qualified server mode, bypass adapter serialization only after preflight
   proves the configured server identity and healthy multi-writer mode. A mode
   claim from configuration alone is insufficient.
7. A server health failure closes mutation readiness. It must not fall back to
   embedded mode or another workspace automatically.
8. Document in API names, comments, and diagnostics that serialization protects
   Spec Kitty subprocesses only. It cannot fence a human/direct writer and does
   not turn unguarded update into CAS or enable safe compensation.

**Tests**:

- two independent helper processes contend for one embedded lock and never
  overlap their recorded critical sections;
- cancellation and timeout return bounded typed contention;
- normal release and killed-holder recovery allow a later process to acquire;
- bindings for different workspaces do not block one another;
- qualified server mode permits overlap while unqualified/spoofed mode refuses;
- capability output remains `unguarded`/`unsupported` where appropriate even
  while local serialization succeeds.

**Exit evidence**: repeat the real-process contention test at least 25 times and
run it under the race detector where supported.

## T020: Prove worktree convergence, safe refusal, and preflight latency

**Purpose**: Attack the architectural claim directly with two lane contexts and
adversarial provider identities while measuring, rather than assuming, local
preflight performance.

**Files**:

- `internal/tracker/beads/binding_test.go`
- `internal/tracker/beads/inspect_test.go`
- `internal/tracker/beads/writer_lock_test.go`

**Implementation requirements**:

1. Create two linked-worktree-shaped temporary directories pointing to the same
   explicit canonical provider workspace. Run independent bindings/inspections
   and prove equal workspace identities and equal compatibility facts.
2. Repeat with one lane aimed at a nested, redirected, missing, or different
   workspace. Every mutating path must refuse before lock acquisition and
   before the fake command harness records a mutation.
3. Cover missing executable, changed digest, v1.2.1/v1.2.3 version skew,
   missing/future schema, read-only workspace, embedded lock contention,
   unavailable server, malformed JSON, timeout, and mismatched identity.
4. Snapshot both lane repositories, the canonical workspace, agent instruction
   files, Git config, and hook directories before discovery. Assert byte/path
   census equality afterward; expected lock-file metadata is the only narrowly
   documented test allowance during actual writer-lock cases.
5. Measure 100 sequential healthy local preflight operations after separately
   characterizing process-start overhead. Record sample count, p50, p95, max,
   GOOS/GOARCH, and non-secret host detail in test output.
6. Assert NFR-001's p95 under one second only for the characterized healthy local
   helper/provider fixture. Use a generous upper bound for individual CI noise;
   do not add a fragile lower-bound timing assertion.
7. Run the healthy preflight with network access unavailable to the fake
   executable and prove no command attempts remote access or package download.
8. Keep performance evidence diagnostic and deterministic enough to reproduce;
   do not commit host-specific golden timings.

**Required mutant controls**:

- removing workspace-identity comparison causes the divergent-lane test to fail;
- moving compatibility refusal after lock acquisition causes the ordering spy
  to fail;
- deriving a lock from current worktree instead of canonical binding causes the
  cross-process overlap test to fail;
- promoting adapter serialization to guarded update causes the capability test
  to fail;
- adding an initialization/fix command causes the no-mutation census to fail.

**Exit evidence**:

```bash
go test ./internal/tracker/beads -run 'Binding|Inspect|Health|Writer|Worktree|Preflight' -count=10
go test -race ./internal/tracker/beads
```

## Test Strategy

Use deterministic fake executables that speak the WP03 schema-1 protocol and
record only command kind, start time, workspace path, and mutation intent. Do
not parse human output in test helpers. Unit-test pure canonicalization and
capability derivation separately from subprocess inspection; reserve real
processes for writer contention and lane convergence.

During development:

```bash
go test ./internal/tracker/beads -run 'Binding|Inspect|Writer'
go test ./internal/tracker/beads -run 'Worktree|Preflight' -count=10
```

Before handoff:

```bash
go test ./internal/tracker/... -count=10
go test -race ./internal/tracker/...
go vet ./internal/tracker/...
```

The real Beads qualification campaign is not part of this WP's ordinary test
dependency. These tests must run offline with a fake executable. Later packages
will confirm the same binding and health contracts against the pinned binary.

## Definition of Done

- Every validated binding contains an absolute resolved executable, verified
  executable digest, canonical workspace path/identity, schema, mode, and
  read-only state.
- Two lane contexts converge on one workspace identity; divergence is a typed
  pre-mutation refusal.
- Version `1.2.2` and schema `1` are the only mutation-qualified initial
  combination.
- Inspection reports exact implemented capabilities and never initializes,
  fixes, installs, edits, or silently rebinds provider/repository state.
- Embedded writers serialize across OS processes on one binding.
- Server-mode concurrency requires observed healthy qualification.
- Writer serialization is never exposed as CAS, guarded update, safe
  compensation, or protection from direct external writers.
- Missing, unavailable, skewed, malformed, read-only, contended, and mismatched
  states have stable provider-neutral diagnostics and recovery codes.
- One hundred healthy local preflights record distribution evidence and meet
  the p95 target without a fragile minimum-duration assertion.
- All six owned files pass focused tests, repetitions, race detection, vet, and
  `git diff --check`.
- Only files listed in `owned_files` are changed.
- Record completion through the event-sourced status command:

```bash
spec-kitty agent tasks mark-status T016 T017 T018 T019 T020 --status done --mission beads-tracker-integration-01M0YG7E
```

## Risks

- **Implicit discovery**: a convenient current-directory fallback creates one
  mutable provider per lane. Require the explicit canonical binding everywhere.
- **Symlink/path aliasing**: lexical cleaning alone can make one workspace look
  like several. Resolve existing path identity before hashing.
- **Executable replacement**: path and version can stay constant while bytes
  change. Bind and verify the digest.
- **Inspection side effects**: health tools often offer repair flags. Keep fix
  and initialization unrepresentable in read-only constructors.
- **False concurrency guarantee**: adapter locking serializes only participating
  processes and cannot fence direct writers or create provider CAS.
- **Lock leakage**: a PID file or incomplete cleanup can strand all mutations.
  Use process-scoped advisory ownership and crash-recovery tests.
- **Server optimism**: configured server mode is not health evidence. Require an
  observed qualified endpoint identity before allowing overlap.
- **Timing flakiness**: process startup varies across CI hosts. Measure its
  distribution, avoid lower bounds, and keep the p95 assertion scoped.
- **Diagnostic leakage**: paths may disclose usernames and command output may
  contain work content. Emit bounded identities/recovery codes, not transcripts.

## Reviewer Guidance

Begin by running the divergent-worktree and refusal-order mutant tests. A final
`workspace_mismatch` error is insufficient if a lock was acquired or subprocess
started first. Verify every mutating entry point consumes the exact compatible
binding/readiness result and that no ambient current-directory discovery remains.

Trace the embedded lock through two independent OS processes. Reject a Go
mutex, a lock rooted in the lane worktree, unbounded polling, or stale PID-file
guessing. Then inspect capability reporting: the lock may advertise
`adapter_serialized` concurrency, but arbitrary updates remain unguarded and
safe compensation remains unsupported.

Finally, compare filesystem and repository censuses before and after inspection.
Reject any automatic `init`, repair, hook installation, agent-file edit, Git
configuration change, or fallback workspace selection. Confirm performance
results record actual samples and host context instead of asserting that a
single fast run proves NFR-001.
