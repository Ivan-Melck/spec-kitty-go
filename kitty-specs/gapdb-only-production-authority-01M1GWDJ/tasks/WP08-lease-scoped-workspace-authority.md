---
work_package_id: WP08
title: Lease-Scoped Workspace Authority
dependencies:
- WP02
- WP03
- WP04
- WP07
requirement_refs:
- FR-012
- FR-020
- FR-023
- FR-024
- FR-025
- NFR-001
- NFR-003
- NFR-004
- NFR-007
- NFR-016
- C-004
- C-005
- C-011
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
base_branch: kitty/mission-gapdb-only-production-authority-01M1GWDJ
base_commit: c2ca33b25792348816597530e96e338abde5525e
created_at: '2026-09-04T11:45:39.413576+00:00'
subtasks:
- T040
- T041
- T042
- T043
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/workspace/
create_intent:
- internal/execution/workspace_authority_v2.go
- internal/execution/workspace_authority_v2_test.go
- internal/controlstore/gapdb/workspace_authority.go
- internal/controlstore/gapdb/workspace_authority_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/execution/authority_v2.go
- internal/execution/authority_v2_test.go
- internal/execution/workspace_authority_v2.go
- internal/execution/workspace_authority_v2_test.go
- internal/workspace/**
- internal/controlstore/gapdb/workspace_authority.go
- internal/controlstore/gapdb/workspace_authority_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP08 - Lease-Scoped Workspace Authority

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the
frontmatter, and behave according to its guidance before parsing the rest of
this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If profile resolution fails, use `spec-kitty agent profile list` to resolve the
closest implementation profile and record the governed invocation before any
production edit.

## Objective

Implement workspace authority as a versioned, lease-scoped capability over the
GapDB-only journal. A workspace is permanently bound to one repository, path,
run, lease generation, and runtime epoch. Preparation is resumable and
observable. Expiry quarantines that workspace forever from Spec Kitty's
authority perspective, and a replacement lease always receives a fresh
WorkspaceID and fresh path even when a zombie process continues direct POSIX
writes in the old directory.

Implementation command:

```bash
spec-kitty agent action implement WP08 --agent codex
```

## Branch, dependency, and ownership rules

- Begin from the accepted WP04 and WP07 revisions assigned in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- Planning and merge target: `feat/gapdb-only-production-authority`.
- Use only the Spec Kitty-provided lane worktree.
- Edit only `internal/workspace/**`, `internal/execution/authority_v2.go`,
  `internal/execution/authority_v2_test.go`, and
  `internal/controlstore/gapdb/workspace_*.go`, plus the two explicitly owned
  `internal/execution/workspace_authority_v2*.go` composition files.
- Preserve concurrent work and do not revert or absorb another agent's edits.
- Consume WP02 journal, WP03 GapDB, WP04 restore, and WP07 lease/effect
  contracts without modifying or bypassing them.
- Filesystem and Git implementations remain outbound adapters behind
  workspace-owned ports. They never enter the pure model or reducers.
- Apply `post-approval-defect-9.md`. Execution owns cross-capability atomic
  composition; workspace contributes opaque validated binding/quarantine
  members, and neither layer invents raw Git state as a GapDB-fenced fact.

Read the mission `spec.md`, `plan.md`, `research.md`, `data-model.md`, and
`contracts/authority-journal-v2.md`, plus the frozen workspace behavior tests,
before editing. Preserve observable product laws, not old store method shapes.

## Core safety model

Fencing tokens can reject Spec Kitty operations; they cannot stop arbitrary
filesystem writes by a stale process. Therefore this WP must not claim POSIX
write fencing. It enforces two complementary boundaries:

1. workspace isolation: a mutable directory belongs forever to one lease; and
2. authority validation: every later gate rejects evidence from an expired,
   quarantined, predecessor-epoch, or otherwise non-current workspace.

After lease 17 expires, workspace W1 remains evidence for lease 17. Lease 18
gets W2. If the stale lease-17 process writes W1, it cannot make W1 current,
transfer it to lease 18, create an admissible handoff, or pass review,
acceptance, or integration authority checks.

### Enforceable execution-slot binding

The execution-slot state must record whether the current active lease has an
immutable workspace binding. An unbound slot records neither a WorkspaceID nor
a workspace-path-claim digest; a bound slot records both, and they are
write-once for that lease generation. Initial binding updates the execution
head and creates the workspace authority in one sealed publication. It must not
leave a bound workspace while the execution slot still appears unbound.

The ordinary execution service may expire only an unbound slot. When the slot
is bound, its `ExecutionExpireV2` route must refuse before publication and
direct callers to the execution-owned workspace composition. That composition
alone may publish the execution expiry together with the exact bound
workspace's irreversible quarantine in one batch. Reducer validation must also
reject loss, replacement, or partial mutation of the binding across heartbeat,
outcome, expiry, and replacement transitions. A replacement generation starts
unbound and later binds a fresh WorkspaceID and fresh path claim; it never
inherits its predecessor's binding.

## T040: Define workspace-owned model, ports, events, and reducer

**Purpose**: Replace the broad workspace store with capability-owned semantic
contracts and an immutable workspace binding that later gates can verify.

**Files**: existing or new files under `internal/workspace/**`.

**Implementation requirements**:

1. Define the workspace aggregate coordinate and immutable binding fields:
   WorkspaceID, ProjectID, MissionID, WorkRevisionID, WorkID, RunID, LeaseID,
   lease generation/fence, RuntimeEpochID, RepositoryID, LaneID, canonical
   absolute path identity, owned branch/ref, pinned base, and dependency set.
2. Make repository, lease, workspace, and path bindings write-once. No stage
   transition, retry, restart, replacement, or reconciliation may change them.
3. Treat the canonical path as recorded identity, not filesystem authority.
   Reject relative, non-canonical, outside-managed-root, aliasing, symlink-
   ambiguous, duplicate, or overlong paths before publication.
4. Keep path validation/observation typed and bounded. Pure code may compare
   canonical recorded facts but cannot call `os`, Git, or filesystem adapters.
5. Define a closed monotonic lifecycle covering reservation, creation intent,
   worktree observed, base verified, dependency preparation requested/observed,
   ownership verified, ready, quarantine, and any explicit reconciliation state
   required for ambiguous external mutation.
6. Define versioned events for binding created, preparation requested,
   preparation observed/advanced, ready published, and quarantined. One
   publication emits at most one workspace event and one version increment.
7. Define narrow command publication, current observation, and preparation
   effect ports owned by `internal/workspace`; do not expose raw keys, provider
   batches, database handles, or a generic CRUD repository.
8. Implement a pure reducer that validates identity, exact prior version,
   monotonic stages, immutable binding, recorded observations, and quarantine
   terminality. It must have no persistence/external capability.
9. Register the workspace event family at the exact schema/reducer tuple in the
   closed v2 registry. Unknown versions fail playback closed.
10. Return defensive immutable values and typed bounded diagnostics without
    leaking control-store values or private filesystem content.

**Required tests**:

- Every legal stage/event reduces deterministically.
- Binding-field mutation after creation refuses for each field independently.
- Non-canonical/aliased/outside-root path observations refuse before publish.
- Unknown stage/event/reducer versions fail closed.
- Quarantined state cannot return to ready or any preparation stage.
- Reducer/API-shape tests prove no Git, filesystem, GapDB, or SQL capability is
  reachable from the pure reducer.

**Exit evidence**:

```bash
go test ./internal/workspace -run 'Model|Binding|Event|Reducer|Path' -count=10
```

## T041: Make preparation staged, atomic, and restart-safe

**Purpose**: Preserve real worktree setup while ensuring every external
mutation is preceded by durable intent and every authority stage is resumable.

**Files**: `internal/workspace/**`.

**Implementation requirements**:

1. Replace `ReserveWorkspace`/`AdvanceWorkspace` persistence shape with typed
   workspace commands that construct sealed Authority Journal commit plans.
2. The first publication atomically binds the absent WorkspaceID and permanent
   absent path claim to the exact current run/lease/work/repository coordinates,
   advances the execution head with an exact record-revision mutation condition
   so it records the immutable WorkspaceID/path-claim pair, and records the
   first preparation effect intent before any directory or Git mutation. A key
   changed by this publication cannot also be a read-only assertion target.
3. For each external preparation step, follow:
   durable requested/effect intent -> adapter action -> bounded observation ->
   guarded journal transition. Never call the adapter first.
4. Retain the observable preparation laws represented by reserved,
   worktree-created, base-verified, dependencies-prepared,
   ownership-verified, and ready, while using semantic events instead of table
   field updates.
5. Verify repository binding, managed root, pinned target/base, registered
   worktree, owned branch, isolated GitDir/index, clean state, and exact
   dependency head at their defined stages.
6. Represent changed authority, including the execution head during initial
   binding, as exact mutation conditions. Represent only genuinely unchanged
   execution or eligibility authority as true read-only assertions in one
   sealed publication. Never use a same-value CAS as a fake assertion, and
   never overlap an assertion with a mutation. Repository/Git target state
   remains guarded external evidence, not a GapDB condition.
7. Resolve exact OperationID/PublicationID replay before current-epoch checks.
   Changed operation meaning refuses before workspace or adapter reads.
8. On restart, reduce the durable workspace stage and effect observations, then
   continue from the first unresolved intent. Do not rerun an external step
   blindly or generate a new operation/effect identity.
9. If an adapter response was lost, inspect the intended path and repository
   registration, compare all attributable facts, and either record the exact
   observation or enter reconciliation-required state.
10. Publish `ready` only while holding the cooperating repository target guard
    across pre-probe, GapDB publication, and post-probe. Persist both exact
    observations. Drift, uncertainty, or guard loss withholds ready according
    to frozen behavior; success is never optimistic.

**Required fault tests**:

- Crash before initial binding causes zero filesystem/Git calls.
- Crash after binding/intent resumes with the same WorkspaceID, path,
  OperationID, and EffectID.
- Crash after worktree creation but before observation reconciles rather than
  creating a second directory.
- Restart from every preparation stage reaches the same canonical ready state.
- Target, base, branch, dependency, GitDir, index, registration, or cleanliness
  drift yields the exact typed refusal and no ready publication.
- Same WorkspaceID with a different lease/repository/path is an idempotency or
  binding conflict, never a rebind.

**Exit evidence**:

```bash
go test ./internal/workspace -run 'Prepare|Ready|Restart|Ambiguous|Fault' -count=10
```

## T042: Quarantine expiry and give replacements fresh workspaces

**Purpose**: Turn the stale-writer finding into an enforceable product
invariant without pretending the database can revoke POSIX access.

**Files**: `internal/workspace/**` and its tests.

**Implementation requirements**:

1. Provide a workspace event/plan contribution that execution-owned
   composition atomically publishes with lease expiry: execution authority
   becomes expired and its bound workspace becomes quarantined in the same
   GapDB revision.
   This composition also owns the exact execution-slot transition needed to
   establish or consume the immutable WorkspaceID/path-claim binding. The
   general execution service must refuse bound-slot expiry rather than emit its
   former execution-only publication.
2. Bind quarantine evidence to WorkspaceID, LeaseID, generation/fence,
   RuntimeEpochID, WorkRevisionID, WorkID, reason, actor/process authority where
   relevant, and recorded occurrence time.
3. Make quarantine monotonic and irreversible within the production authority
   graph. Cleanup may remove external files later but cannot make the identity
   ready, transferable, or reusable.
4. A replacement lease must use a newly allocated WorkspaceID and distinct
   canonical path. Reject a replacement request that reuses the old ID, path,
   permanent path claim, branch authority, GitDir, index, or mutable workspace
   record.
5. Do not transfer filesystem ownership or rewrite the old workspace binding to
   name the replacement lease. W1 stays evidence for expired lease 17; W2 is
   created for lease 18.
6. On runtime epoch replacement, predecessor workspace capsules refuse even if
   their wall-clock lease has not yet elapsed. Recovery records/inherits their
   disposition according to WP07; it does not reactivate them.
7. Define a reusable, pure current-workspace authority predicate for handoff,
   review, acceptance, and integration. It must compare exact epoch, work
   generation, WorkID, RunID, LeaseID/generation/fence, WorkspaceID/path,
   repository binding, ready status, and non-quarantine state.
8. Validate the current-workspace predicate before any candidate publication,
   reviewer mutation, acceptance decision, repository fence, or Git mutation.
   Return the frozen earliest typed stale/quarantined refusal.
9. Treat post-quarantine file changes as external observations only. They may
   support forensic reconciliation, but never restore authority or contaminate
   W2's candidate.
10. Keep cleanup separate, attribution-safe, and non-authoritative. Cleanup
    failure must not undo lease expiry or quarantine.

**Mandatory zombie scenario**:

1. Process A obtains lease 17 and ready workspace W1.
2. A stops heartbeating but remains alive.
3. Lease 17 expires; one publication expires it and quarantines W1.
4. Process B obtains lease 18 and receives fresh W2.
5. A resumes and changes files and commits inside W1.
6. Every Spec Kitty gate rejects A/W1 evidence without mutating candidate,
   review, acceptance, integration, or W2 state.
7. B/W2 remains able to progress under lease 18.

Run this scenario at least 100 times, including concurrent A/B activity. Assert
that no result depends on preventing A's direct filesystem write.

**Exit evidence**:

```bash
go test ./internal/workspace -run 'Expire|Quarantine|Replacement|Zombie|AuthorityGate' -count=10
```

## T043: Implement GapDB adapter and adversarial qualification

**Purpose**: Persist workspace publications through one v2 atomic batch and
prove crash, restart, concurrency, and stale-writer behavior.

**Files**: `internal/controlstore/gapdb/workspace_*.go` plus tests under the two
owned wildcard surfaces.

**Adapter requirements**:

1. Implement the workspace-owned ports using WP03's v2 committer/reader; raw
   GapDB access remains entirely in the adapter package.
2. Translate each sealed CommitPlan to exactly one conditional GapDB batch with
   global operation binding/state, immutable workspace event, workspace head,
   execution/guard conditions, and effect intent/observation membership.
3. Require absence for first WorkspaceID and path-authority records; require
   exact provider record revision and logical version for updates.
4. Reject key aliasing, duplicate path authority, partial membership, unknown
   versions, over-budget observations, and physical head/event deletion.
5. Reconcile ambiguous acknowledgements by PublicationID and complete
   operation/event/head membership. Never retry under a new identity.
6. Restore workspace state from WP04 checkpoint plus suffix and bounded origin
   playback with byte-identical binding, stage, quarantine, and result digests.
7. Expose typed bounded outcomes only; do not leak keys, socket paths, provider
   values, filesystem contents, or raw adapter errors.
8. Add no SQLite/SQL path, backend selector, fallback, dual write, importer,
   table-compatible API, or provider dependency in `internal/workspace`.

**Concurrency and fault campaigns**:

- At least 100 same-workspace conflicting stage transitions: one winner,
  typed losers, zero partial state.
- At least 100 independent-workspace preparations: both can progress without a
  global workspace lock/head.
- At least 100 expiry/replacement/zombie trials across clean and unclean owner
  restart, with no stale candidate authority.
- Faults before/after every workspace publication and external preparation
  boundary, including response loss and contradictory re-observation.
- Race detector coverage over service, reducer, adapter, replacement, and
  current-authority predicate.

**Mandatory controlled mutants**:

- mutable lease/repository/path binding;
- workspace path reused by replacement lease;
- quarantine can transition back to ready;
- lease expires without atomic workspace quarantine;
- same-value CAS substitutes for a read-only assertion on genuinely unchanged
  authority, or initial bind asserts and mutates the execution head together;
- execution expiry wins but stale workspace binding still publishes;
- workspace binding wins but expiry omits its quarantine contribution;
- cleanup failure rolls back quarantine;
- preparation adapter call occurs before durable effect intent;
- crash retry allocates a new WorkspaceID, path, OperationID, or EffectID;
- ready publishes without final repository/workspace observation;
- zombie W1 passes a later authority gate;
- epoch-only or fence-only check omits full capsule coordinates;
- process-local mutex substitutes for provider CAS;
- two workspace events target one aggregate in one publication;
- restart trusts projection state not proven by replay;
- adapter or filesystem implementation leaks into the workspace kernel.

Each mutant requires a passing control and must fail for its intended semantic
reason. Grep-only assertions do not count as behavioral proof.

**Validation commands**:

```bash
go test ./internal/workspace ./internal/controlstore/gapdb -count=10
go test -race ./internal/workspace ./internal/controlstore/gapdb -count=1
go vet ./internal/workspace ./internal/controlstore/gapdb
staticcheck ./internal/workspace ./internal/controlstore/gapdb
go test ./internal/architecture -count=1
go test ./... -run '^$' -count=1
gofmt -d internal/workspace internal/controlstore/gapdb
git diff --check
```

## Definition of Done

- Workspace model, events, reducer, and ports are capability-owned and pure.
- Repository, lease, epoch, run, work, and path binding is immutable.
- Every preparation mutation follows durable intent, action, observation, and
  guarded publication, and resumes from every crash boundary.
- Ready status requires complete attributable repository/workspace evidence.
- Lease expiry and workspace quarantine publish atomically.
- Replacement always gets a fresh WorkspaceID and distinct canonical path.
- Quarantined workspace identity is never reactivated, reused, or transferred.
- The zombie scenario proves stale direct writes cannot pass authority gates.
- GapDB adapter uses sealed v2 batches and restores byte-identical state.
- Parity, refusal, 100-run contention/zombie, fault, mutant, and race gates pass.
- No SQLite, provider-kernel, process-lock, or optimistic-ready path exists.
- T040–T043 are recorded `done` through the canonical event log.
- The committed WP is handed to an independent reviewer and not self-approved.

After all implementation and validation are genuinely complete:

```bash
spec-kitty agent tasks mark-status T040 T041 T042 T043 \
  --status done \
  --mission gapdb-only-production-authority-01M1GWDJ \
  --json
```

## Risks and mitigations

| Risk | Required mitigation |
|---|---|
| Fencing token is described as filesystem fencing | State explicitly that arbitrary POSIX writes remain possible and rely on workspace isolation plus gate validation. |
| Replacement inherits old mutable state | Require fresh identity/path and reject every reuse coordinate. |
| Crash repeats a non-idempotent Git/filesystem action | Persist intent first; reconcile exact observations before retry. |
| Ready is published from stale probes | Final probe plus guarded repository/workspace publication and post-ambiguity membership checks. |
| Quarantine and expiry can split | Publish execution and workspace transitions in one sealed multi-aggregate batch. |
| Cleanup becomes authority | Make cleanup attribution-safe, best-effort, and unable to alter quarantine law. |
| Global workspace lock destroys concurrency | CAS only touched workspace/execution/guard aggregates and prove disjoint progress. |
| Reducer or kernel imports filesystem/Git | Enforce WP01 architecture rules and capability-free reducer signatures. |

## Independent reviewer guidance

The reviewer must reproduce the zombie scenario with a real writable stale
directory. It is not enough to mock A's write away or assert that the token was
expired. Confirm A can alter W1 after quarantine and that all later authority
gates still reject W1 while B progresses in W2.

Inspect the complete preparation and replacement chronology. Require durable
intent before external mutation, attributable re-observation after ambiguity,
exact immutable bindings, one-event-per-aggregate publication, and provider CAS
for every changed authority fact. Verify replay resolves committed operations
before current-epoch checks and restart never trusts process-local memory.

Reject any implementation that chmods or deletes W1 as its correctness
mechanism, reassigns W1 to lease 18, derives current authority from the path or
branch alone, uses a process mutex for exclusivity, silently retries Git work,
or lets cleanup failure reactivate authority. Also reject dead production APIs,
test-only substitute implementations, missing mutant controls, or a workspace
kernel dependency on GapDB, SQL, Git implementation, filesystem, or transport.
