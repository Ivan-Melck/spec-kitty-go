# Mission Specification: Phase 1 Two-Agent Authority Proving Ground

**Mission ID**: `01M0Q4A8889J13B2G0FZ2MEG7J`

**Mission Slug**: `phase1-two-agent-authority-proving-ground-01M0Q4A8`

**Mission Type**: `software-dev`

**Planning Branch**: `mission/phase1-authority-proving-ground`

**Merge Target**: `main`

**Created**: 2026-08-23

**Status**: Draft

**Input**: Prove or disprove the Go-native Spec Kitty authority model before
adding Beads or the full mission engine. Preserve safe concurrent work while
process lifetime, SQLite authority, fake-tracker state, filesystem state, and
real Git history disagree.

**Primary References**:

- `docs/TECHNICAL_PROJECT_PLAN.md`
- `docs/PYTHON_BEHAVIOR_INVENTORY.md`
- `docs/CONCURRENCY_SCENARIOS.md`
- `docs/MINI_REVIEW_RESOLUTION.md`

## Problem Statement

Spec Kitty's most valuable technical property is that multiple agents can work
on one project concurrently without overwriting one another, while review and
integration remain controlled. Prior language-port experiments risked
preserving Python representations instead of this product behavior.

The proposed Go architecture separates:

```text
Tracker:     what work exists and what humans discuss
Spec Kitty: whether work may run and how it is judged
Git:         what source history actually happened
```

That separation is coherent but not yet empirically proven. Database fencing
cannot prevent an expired process from continuing arbitrary POSIX writes.
Cross-system operations can crash between SQLite, tracker, filesystem, and Git
boundaries. Review and merge can lose or duplicate evidence if authority is not
explicit.

This mission builds the smallest executable system capable of attacking those
claims with independent processes, a deliberately weak fake tracker, a real
SQLite control database, and real Git worktrees. Its purpose is falsification.
If the experiment reveals an unenforceable invariant, the relevant architecture
decision changes before broader implementation.

## Goals

1. Prove concurrent work on distinct items uses isolated mutable workspaces.
2. Prove contenders for one item cannot both hold valid execution authority.
3. Prove lease expiry quarantines an old workspace and replacement never
   inherits it.
4. Prove a zombie writer may mutate its old directory but cannot hand off,
   review, accept, or integrate through stale authority.
5. Prove review judges an immutable candidate and enforces ActorID-level
   independence.
6. Prove integration is serialized by repository and target ref, resumable
   after crashes, and conflict preserving.
7. Prove external compensation never overwrites later human state.
8. Produce evidence to ratify, revise, or reject the concurrency, workspace,
   review, and integration ADRs.

## Non-Goals

This mission does not:

- integrate Beads or another production ticket provider;
- implement the complete software-development mission engine;
- implement Team Kitty, hosted sync, the Orchestrator API, MCP, or dashboard;
- migrate Python projects or generate compatibility projections;
- implement additional mission types, plugins, or a workflow DSL;
- reproduce Python package structure, PRIMARY/COORD routing, Markdown task
  mutation, Git-committed runtime bookkeeping, or sparse-checkout workarounds;
- claim distributed or multi-machine control-store support.

## User Scenarios and Testing

### User Story 1 - Execute Independent Work Concurrently (Priority: P1)

As a project operator, I want independent agent processes to implement distinct
work simultaneously without sharing a mutable checkout or granting ambiguous
authority.

**Why this priority**: Safe parallel execution is the crown jewel. Without it,
the rewrite loses the behavior it exists to preserve.

**Independent Test**: Start two OS processes against two ready items in one
immutable WorkRevision. Demonstrate overlapping execution in distinct real Git
worktrees while sharing one SQLite control store and fake tracker.

**Acceptance Scenarios**:

1. **Given** two independent ready items, **when** two processes claim them at a
   barrier, **then** both receive distinct runs, lease tokens, workspaces,
   branches, paths, and Git indexes.
2. **Given** one ready item, **when** multiple processes race to claim it,
   **then** at most one receives a valid implementation lease and usable
   workspace.
3. **Given** one ActorID using two ProcessIDs, **when** it performs distinct
   work concurrently, **then** the processes retain separate authority and
   audit records.
4. **Given** active implementation, **when** agents edit and commit, **then**
   the primary checkout's HEAD, index, and tree remain unchanged.

### User Story 2 - Recover Partial Allocation Honestly (Priority: P1)

As an operator, I want every start-run boundary recoverable so that a crash
never converts a partial database row, assignment, branch, or directory into a
falsely ready workspace.

**Why this priority**: Existing Python incidents show that existence can be
mistaken for readiness and dependency preparation can stop halfway.

**Independent Test**: Kill allocation after every named durable boundary,
restart, and prove retry completes the same operation or returns a typed
recovery/decision result.

**Acceptance Scenarios**:

1. **Given** a lease commits before later effects, **when** the process dies,
   **then** retry resumes the same operation before expiry or expires it without
   a phantom workspace.
2. **Given** a worktree exists but its base or ownership is unverified, **when**
   recovery runs, **then** it remains `workspace_preparing`, never ready.
3. **Given** dependency preparation crashes or conflicts halfway, **when**
   recovery runs, **then** no partial head is published as the run base.
4. **Given** a successful response is lost, **when** the same operation ID is
   replayed, **then** exactly one lease, workspace, assignment, and outbox row
   exist.

### User Story 3 - Contain a Zombie Writer (Priority: P1)

As an operator, I want expired authority isolated from replacement work so that
a suspended agent can continue writing only to its quarantined workspace and
cannot contaminate valid results.

**Why this priority**: A control database can fence commands but cannot fence
ordinary filesystem writes. This tests the enforceable invariant.

**Independent Test**: Suspend process A with lease generation 17 and workspace
W1. Expire it, grant process B generation 18 and W2, resume A, and make A write
and commit inside W1.

**Acceptance Scenarios**:

1. **Given** generation 17 expires, **when** B replaces it, **then** W1 is
   quarantined and B receives a distinct WorkspaceID, branch, and path.
2. **Given** A resumes, **when** it writes inside W1, **then** W2 and the primary
   checkout do not change.
3. **Given** A presents token 17, **when** it attempts any control mutation,
   handoff, review, or integration, **then** it receives a typed stale-authority
   refusal.
4. **Given** W1 contains useful output, **when** A is later proven quiescent,
   **then** preservation requires explicit reconciliation over an exact commit
   and never revives generation 17.

### User Story 4 - Review an Immutable Candidate Independently (Priority: P1)

As a mission owner, I want review to judge a specific candidate under a
specific WorkRevision so that later workspace movement cannot alter approval
and an implementer cannot approve itself by changing processes.

**Independent Test**: Submit candidate C, modify the workspace afterward, then
review from a same-ActorID/different-ProcessID process and a distinct ActorID.

**Acceptance Scenarios**:

1. **Given** a valid candidate, **when** handoff occurs, **then** it binds the
   lease generation, commit, WorkRevision, lane, contributors, criteria, and
   evidence digest.
2. **Given** the workspace changes after handoff, **when** review starts,
   **then** it observes C, not the live directory or moving branch.
3. **Given** the implementer starts another process, **when** the same ActorID
   attempts independent review, **then** the gate rejects it absent an explicit
   visible waiver.
4. **Given** a distinct reviewer submits a verdict, **when** it commits, **then**
   the candidate-bound evidence persists transactionally and approval produces
   `approved`, not `done`.
5. **Given** review writers contend, **when** policy permits one verdict slot,
   **then** one succeeds and the other receives an explicit conflict; no verdict
   disappears in a Git index.

### User Story 5 - Integrate Deterministically and Recoverably (Priority: P1)

As a maintainer, I want accepted work to enter one serialized target-ref path so
that lanes and missions cannot race Git and a crash never duplicates or hides
an integration.

**Independent Test**: Approve two candidates, request integration
simultaneously, and inject crashes before and after target-ref advancement.

**Acceptance Scenarios**:

1. **Given** two approved candidates, **when** integration races for the same
   repository and target ref, **then** only one process holds mutation authority.
2. **Given** the target advances, **when** the next integration begins,
   **then** it revalidates against the new target.
3. **Given** a crash after target movement but before bookkeeping, **when**
   recovery runs, **then** it proves Git outcome before recording success and
   writes `done` exactly once.
4. **Given** a conflict, **when** integration stops, **then** target and every
   candidate remain reachable and explicit repair evidence is created.
5. **Given** separate missions target the same ref, **when** they integrate,
   **then** serialization applies across mission boundaries.

### User Story 6 - Recover External Effects Safely (Priority: P2)

As an operator, I want fake-tracker assignment and compensation to be
idempotent and attribution safe so that timeouts and later human edits cannot
cause recovery to overwrite legitimate state.

**Independent Test**: Inject unavailable and ambiguous responses, then modify
the external assignee/revision before compensation.

**Acceptance Scenarios**:

1. **Given** the tracker applies an assignment but loses its response, **when**
   retry checks the operation marker and revision, **then** it recognizes its
   effect without duplication.
2. **Given** a human later changes the assignee, **when** stale compensation
   runs, **then** it records a conflict and preserves the human value.
3. **Given** state still matches the operation's revision, value, and marker,
   **when** compensation runs, **then** it undoes only that effect idempotently.
4. **Given** tracker unavailability during start, **when** policy evaluates the
   operation, **then** it produces one explicit blocked/deferred result and
   never an untyped half-success.

### User Story 7 - Reconcile Work Generations (Priority: P2)

As a mission owner, I want work changes to create explicit executable graph
generations so an active or approved contract cannot mutate silently.

**Independent Test**: Create a second WorkRevision representing equivalent,
replacement, invalidating, and forked changes while work is active or approved.

**Acceptance Scenarios**:

1. **Given** an equivalent edit, **when** the owner selects `continue`, **then**
   the run remains eligible with durable old/new digests and rationale.
2. **Given** replacement work, **when** the owner selects `supersede`, **then**
   the old run cannot submit an acceptable handoff or integration.
3. **Given** a material contract change, **when** the owner selects
   `invalidate`, **then** active authority expires and its workspace quarantines.
4. **Given** two valid lines, **when** the owner selects `fork`, **then** each
   has a distinct WorkID and unambiguous edges.
5. **Given** lane collapse creates a cycle, **when** revision validation runs,
   **then** it rejects the graph before execution.

## Edge Cases

- Same ActorID through multiple ProcessIDs.
- Lost successful response and idempotent replay.
- SQLite commit before the next tracker or filesystem step.
- Tracker applies a request but times out.
- Worktree directory and Git registration disagree.
- Owned branch base or head moves manually.
- Target advances after WorkRevision finalization or review.
- Dependency preparation stops halfway or conflicts.
- A lease expires while its process is suspended rather than dead.
- A zombie creates a valid Git commit after expiry.
- Workspace changes after immutable handoff.
- Same-actor review from a new process.
- Review verdict contention.
- Integration crash immediately before or after target advancement.
- Cleanup encounters a dirty, unknown, active, or quarantined workspace.
- WorkRevision changes while work is active, approved, or integrated.

## Requirements

### Functional Requirements

| ID | Title | Requirement | Priority | Status |
|---|---|---|---|---|
| FR-001 | Stable typed identity | Define Project, Mission, Work, WorkRevision, Actor, Process, Run, Lane, Workspace, Handoff, Review, Integration, and Operation IDs once and preserve them across persistence. | High | Open |
| FR-002 | Immutable work graph | Pin work, criteria, dependencies, lane DAG, planning source, graph digest, and provider revisions in numbered WorkRevisions. | High | Open |
| FR-003 | Atomic execution lease | Grant at most one valid implementation lease per WorkID and WorkRevision through transactional compare-and-swap. | High | Open |
| FR-004 | Idempotent start | Replay one OperationID without duplicating authority or external effects. | High | Open |
| FR-005 | Staged allocation | Persist reservation, worktree creation, base, dependencies, branch ownership, and final workspace readiness separately. | High | Open |
| FR-006 | Isolated concurrency | Execute distinct work concurrently without shared mutable directories, branches, or Git indexes. | High | Open |
| FR-007 | Lease fencing | Require the current token for run, workspace, handoff, review, and integration control mutations. | High | Open |
| FR-008 | Quarantine and replacement | Bind a workspace permanently to one lease generation; replacement always receives a new identity, branch, and path. | High | Open |
| FR-009 | Dependency integrity | Apply accepted dependency candidates deterministically and never publish a partial/conflicting base. | High | Open |
| FR-010 | Immutable handoff | Bind candidate commit, valid lease, WorkRevision, lane, contributors, criteria, and evidence. | High | Open |
| FR-011 | Independent review | Reject same-ActorID independent approval by default and persist candidate-bound verdicts transactionally. | High | Open |
| FR-012 | Target-ref integration | Serialize target mutation by repository plus target ref across lanes and missions. | High | Open |
| FR-013 | Resumable merge | Recover all integration crash boundaries by probing Git; record successful integration and `done` exactly once. | High | Open |
| FR-014 | Conflict preservation | Preserve target and candidates and emit explicit repair state/evidence on conflict. | High | Open |
| FR-015 | Stupid fake tracker | Expose work, criteria, dependencies, assignment, comments, revision CAS, markers, and injected failures—no mission/execution authority. | High | Open |
| FR-016 | Safe compensation | Undo tracker state only when revision, expected value, and marker still prove attribution. | High | Open |
| FR-017 | Revision reconciliation | Support and enforce `continue`, `supersede`, `invalidate`, and `fork`. | Medium | Open |
| FR-018 | Typed diagnostics | Return stable blockers, conflicts, stale failures, and decision-required recovery with relevant IDs and actions. | High | Open |
| FR-019 | Fault injection | Provide deterministic crash hooks after every named durable boundary in the scenario catalog. | High | Open |
| FR-020 | Safe inspection/cleanup | Diagnose without mutation by default; never delete an unknown, dirty, active, or quarantined workspace. | High | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|---|---|---|---|---|---|
| NFR-001 | Race safety | `go test -race ./...` completes without a race report. | Reliability | High | Open |
| NFR-002 | Repeated contention | The complete Phase 1 scenario suite passes 100 consecutive runs with zero invariant violation. | Reliability | High | Open |
| NFR-003 | Process realism | SQLite, Git-index, lease, and process-death tests use independent OS processes, not only goroutines. | Test fidelity | High | Open |
| NFR-004 | Honest crash state | Every injected boundary exposes pre-effect, committed, or typed recoverable state—never false success. | Reliability | High | Open |
| NFR-005 | Primary isolation | The primary checkout changes zero times across implementation, review, crash, and zombie scenarios. | Safety | High | Open |
| NFR-006 | Diagnostic completeness | Every refusal contains schema/correlation/operation IDs, relevant aggregate IDs, observed/expected state, retry safety, and recovery actions. | Operability | High | Open |
| NFR-007 | Path safety | Every mutable/cleanup path is canonical, owned, and contained in the managed workspace root. | Security | High | Open |
| NFR-008 | Determinism | Fixed clock, IDs, graph, commits, and fault point yield stable outcomes and integration ordering. | Testability | Medium | Open |
| NFR-009 | Bounded dependencies | Production Phase 1 code has no dependency on Beads, Team Kitty, MCP, HTTP, Python, Markdown parsing, or projections. | Maintainability | High | Open |
| NFR-010 | Go-native modularity | Domain rules do not import CLI, SQLite implementation, or Git process details; capabilities communicate through narrow typed contracts. | Maintainability | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|---|---|---|---|---|---|
| C-001 | One-machine authority | One local machine/filesystem authority owns the SQLite control store; shared/distributed DB access is unsupported. | Architecture | High | Open |
| C-002 | Real SQLite | Contention scenarios use a real SQLite WAL database and independent processes. | Technical | High | Open |
| C-003 | Real Git | Worktree, branch, conflict, and integration scenarios use real temporary Git repositories and argument-safe commands. | Technical | High | Open |
| C-004 | Tracker boundary | The fake tracker cannot implement lease, workspace, review, acceptance, merge-slot, mission, or Git-hook semantics. | Architecture | High | Open |
| C-005 | No production integrations | Beads, Team Kitty, Orchestrator API, MCP, hosted sync, and migration remain out of scope. | Scope | High | Open |
| C-006 | No compatibility kernel | Python file layouts and runtime bookkeeping may inform tests but cannot shape core package dependencies. | Architecture | High | Open |
| C-007 | Enforceable POSIX law | Quarantine and stale-command refusal are required; claiming to make a zombie-owned directory immutable is forbidden. | Safety | High | Open |
| C-008 | Test before generalization | Implement only mechanisms required by the Phase 1 scenario catalog. | Delivery | High | Open |

### Key Entities

- **WorkRevision**: Immutable executable graph generation.
- **Lane**: Durable scheduling and accepted-candidate lineage, not a checkout.
- **Run**: One implementation, review, or repair attempt.
- **Lease**: Time-bounded authority with a monotonically increasing token.
- **Workspace**: Mutable worktree permanently bound to one lease generation.
- **Operation**: Idempotent staged saga spanning local and external effects.
- **Handoff**: Immutable candidate/evidence boundary.
- **Review**: Actor/process-attributed verdict over one candidate and revision.
- **Integration**: Serialized attempt against one repository target ref.
- **ExternalEffect**: Tracker mutation with expected value, revision, and marker.

## Success Criteria

### Measurable Outcomes

- **SC-001**: Two different items execute concurrently in separate real
  worktrees with overlapping active intervals and zero primary-checkout changes.
- **SC-002**: Across 100 contention runs, one item produces exactly one valid
  implementation lease and at most one ready workspace.
- **SC-003**: Every replacement receives a new WorkspaceID, branch, and path;
  zero quarantined workspace is transferred implicitly.
- **SC-004**: A zombie successfully writes its old POSIX workspace in the test
  but completes zero valid control, handoff, review, or integration operations.
- **SC-005**: Every start-run fault boundary recovers idempotently or returns a
  typed blocker/decision; no partial allocation reports ready.
- **SC-006**: Same-ActorID/different-ProcessID independent review is rejected;
  a different ActorID can persist a candidate-bound verdict.
- **SC-007**: Concurrent review writers produce allowed verdicts or explicit
  conflicts; zero verdict silently disappears.
- **SC-008**: Target-ref integration mutation never overlaps, including across
  missions, and revalidates after each target advance.
- **SC-009**: Integration crash recovery creates no duplicate target commit and
  exactly one `done` record per integrated work item.
- **SC-010**: Every conflict preserves target and candidate commits and emits
  explicit repair evidence.
- **SC-011**: A later human reassignment survives stale compensation in every
  injected case.
- **SC-012**: All four WorkRevision dispositions produce their specified
  eligibility results in deterministic tests.
- **SC-013**: `go test -race ./...` passes and the full Phase 1 scenario suite
  passes with `-count=100`.
- **SC-014**: The mission records an evidence-backed ratify, revise, or reject
  verdict for authority, workspace, review, and integration ADRs.

## Mission Exit Rule

This mission succeeds if it produces a trustworthy architectural verdict,
including a verdict that the current design is wrong. It fails if difficult
scenarios are skipped, real process/Git behavior is replaced with convenient
mocks, or production integrations are added before the authority model survives
the proving ground.
