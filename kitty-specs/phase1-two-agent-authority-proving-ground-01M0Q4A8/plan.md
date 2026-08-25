# Implementation Plan: Phase 1 Two-Agent Authority Proving Ground

**Planning Branch**: `mission/phase1-authority-proving-ground`

**Merge Target**: `main`

**Date**: 2026-08-23

**Spec**: `kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/spec.md`

**Input**: Prove or disprove the concurrency authority model using a deliberately
small Go system, real SQLite WAL control state, a stupid fake tracker,
independent processes, and real temporary Git repositories/worktrees.

## Summary

Build one executable vertical proving ground around five capabilities:

1. immutable work revisions and typed identity;
2. run leases plus staged, lease-scoped workspace allocation;
3. immutable handoff plus ActorID-independent review;
4. repository+target-ref serialized integration;
5. deterministic fault injection and scenario observation.

The proving ground uses a pure-Go SQLite driver and real Git commands. It does
not implement Beads, Team Kitty, an HTTP/API surface, the full mission engine,
Markdown compatibility, or multiple providers. Each capability owns a narrow
state machine and exposes typed commands/queries. SQLite and Git are adapters;
they do not define domain rules.

The deliverable is not merely a passing implementation. It is an evidence-backed
ADR verdict. A failed crown-jewel scenario is a successful falsification result
and must revise the architecture before Phase 2.

## Technical Context

**Language/Version**: Go 1.26.4; module language floor initially `go 1.26`

**Primary Dependencies**:

- Go standard library;
- `modernc.org/sqlite v1.57.0`, pinned, as the CGo-free `database/sql` driver;
- no assertion framework is required initially; add only if test clarity earns it.

**Storage**: One project-scoped SQLite database in WAL mode, on one local
machine/filesystem authority. Real Git repositories and worktrees store source
history. The fake tracker has a separate durable test store so cross-store saga
behavior is real rather than simulated inside one transaction.

**Testing**: Go unit/model tests, real-SQLite adapter tests, real-Git integration
tests, independent-process contention tests, named fault-injection scenarios,
`go test -race ./...`, repeated `-count=100` acceptance runs.

**Target Platform**: Linux amd64 proving environment. APIs remain portable Go;
platform-specific process/filesystem behavior is isolated behind adapters.

**Project Type**: Single Go module and local command/helper binaries.

**Performance Goals**: Correctness dominates latency. Local uncontended claim,
heartbeat, and status commands should complete within 500 ms in the test
environment. No scenario may rely on a sleep longer than its deterministic
clock/fault synchronization requires.

**Constraints**: One SQLite writer at a time; same-host WAL only; primary
checkout remains unchanged; every mutable workspace belongs permanently to one
lease generation; no production integrations.

**Scale/Scope**: Two primary agents, up to 32 claim contenders, two missions for
cross-mission integration contention, 28 Phase 1 proving scenarios, and seven
WorkRevision reconciliation scenarios.

## Charter Check

No project charter has been synthesized. The binding project inputs are the
mission specification, the technical project plan, the Python behavior
inventory, Mini's review resolution, and the scenario catalog.

The plan applies these explicit laws from those inputs:

- tracker state never grants execution or Git authority;
- database fencing protects only the control plane;
- lane lineage and mutable workspace lifetime are separate;
- reviewer independence compares ActorID, not only ProcessID;
- integration serializes by repository plus target ref;
- compensation compares external attribution before undoing;
- Phase 1 does not earn Beads or external surfaces.

No charter exception is required. Re-check after design by verifying all
implementation concerns map to mission requirements and no deferred integration
appears in the source graph.

## Resolved Technical Decisions

### D-01: Pure-Go SQLite driver

Use `modernc.org/sqlite v1.57.0`. Its package documentation identifies it as a
CGo-free `database/sql` driver, which lets test helper processes use the same Go
binary without a platform C toolchain. The driver version is pinned so SQLite
engine/DSN semantics cannot drift invisibly.

Rejected for this mission:

- `github.com/mattn/go-sqlite3`: mature, but requires CGo and a system C
  toolchain; Phase 1 gains no product value from that deployment dependency;
- an in-memory fake database: cannot prove cross-process locks, WAL recovery,
  or transaction contention;
- a client/server database: expands operations and evades the architecture
  under test.

### D-02: WAL and connection policy

Initialize and verify:

```text
PRAGMA journal_mode = WAL
PRAGMA foreign_keys = ON
PRAGMA busy_timeout = 5000
```

Use the driver's validated DSN settings for every connection, including
`_journal_mode=WAL`, `_foreign_keys=1`, `_busy_timeout=5000`, and
`_txlock=immediate` for explicit transactions. Query and assert effective
pragmas at startup rather than trusting configuration strings.

SQLite WAL allows readers and a writer concurrently but still permits only one
writer. `BEGIN IMMEDIATE` makes writer acquisition explicit at transaction
start instead of allowing a deferred read transaction to fail later during
upgrade. `SQLITE_BUSY` after the bounded timeout is translated to a typed
contention result, not retried forever.

Long-lived read transactions are prohibited in application services because
they can delay WAL checkpoint progress. Tests explicitly hold a reader when
verifying this diagnostic posture. V1 refuses known network/shared filesystem
locations because WAL requires same-host shared memory semantics.

### D-03: Database location and repository identity

The control database is anchored under a project runtime root resolved from the
Git common directory, not a worktree-local `.git` file or ambient CWD. Startup
records and verifies the canonical repository identity and rejects binding the
same store to a different repository.

Tests use a temporary project runtime directory outside candidate worktrees.
No SQLite database, WAL, SHM, lock, or process record is committed to Git.

### D-04: Lane and workspace split

`LaneID` identifies durable scheduling and accepted-candidate lineage.
`WorkspaceID` identifies one mutable checkout. Every implementation, review, or
repair run receives a new workspace permanently bound to that lease generation.
A same-lane successor is based on a pinned accepted lane head but never inherits
the predecessor's directory or Git index.

### D-05: Immutable handoff boundary

Review and integration accept only a Handoff containing an exact candidate
commit, WorkRevision, valid lease generation, lane lineage, contributor
ActorIDs, criteria digest, and evidence digest. They do not read a live mutable
workspace or a moving branch tip as authority.

### D-06: Transactional review evidence

Review verdict and criterion results commit atomically in SQLite. Optional
large evidence may be content-addressed later, but Phase 1 needs only small
structured evidence. Git files and tracker comments are not authoritative
review records.

### D-07: Target-ref integration lease

The integration uniqueness key is `(RepositoryID, TargetRef)`, not MissionID.
The attempt records preflight target SHA, candidate SHA, intended result, and
phase. Git movement and SQLite bookkeeping form a recoverable saga. Cached
phase is advisory; restart probes Git before skipping or replaying work.

### D-08: Deterministic fault protocol

Production services depend on a tiny `Faults` interface whose default
implementation is a no-op. Tests supply a named-boundary controller using
process-safe barriers and explicit crash exit codes. Business code calls only
named boundaries from the scenario catalog. It does not contain scenario
branching or test-only state transitions.

### D-09: Fake tracker stays stupid

The fake tracker supports work, criteria, dependencies, assignment, comments,
revision CAS, operation markers, and injected unavailable/timeout/ambiguous
results. It deliberately has no mission state, lease, workspace, review,
acceptance, merge slot, or Git hook.

Its state is separately durable. A fake implemented as another table in the
control transaction would conceal the cross-store crash window this mission
must test.

## Project Structure

### Documentation

```text
kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── commands-and-results.md
│   ├── fault-boundaries.md
│   └── provider-contract.md
├── tasks.md
└── tasks/
```

### Source Code

```text
go.mod
go.sum
cmd/
└── spk-phase1-helper/
    └── main.go
internal/
├── ids/
│   ├── ids.go
│   └── ids_test.go
├── workgraph/
│   ├── revision.go
│   ├── reconcile.go
│   └── *_test.go
├── execution/
│   ├── model.go
│   ├── commands.go
│   ├── service.go
│   └── *_test.go
├── workspace/
│   ├── model.go
│   ├── service.go
│   └── *_test.go
├── handoff/
│   ├── model.go
│   ├── service.go
│   └── *_test.go
├── review/
│   ├── model.go
│   ├── service.go
│   └── *_test.go
├── integration/
│   ├── model.go
│   ├── service.go
│   └── *_test.go
├── controlstore/
│   ├── store.go
│   └── sqlite/
│       ├── db.go
│       ├── migrations.go
│       ├── repositories.go
│       └── *_test.go
├── gitrepo/
│   ├── runner.go
│   ├── workspace.go
│   ├── integration.go
│   └── *_test.go
├── tracker/
│   ├── provider.go
│   └── fake/
│       ├── store.go
│       ├── provider.go
│       └── *_test.go
├── faults/
│   ├── faults.go
│   └── controller.go
└── scenario/
    ├── harness_test.go
    ├── start_run_test.go
    ├── zombie_test.go
    ├── review_test.go
    ├── integration_test.go
    └── reconciliation_test.go
```

**Structure Decision**: Organize around product capabilities, not layers or
Python packages. A capability owns its model and application rules. External
mechanisms are explicit adapters (`controlstore/sqlite`, `gitrepo`,
`tracker/fake`). The helper binary exists only to create real independent
processes for contention/crash tests; it contains no alternate business logic.

## Capability and Dependency Rules

```text
workgraph ----+
execution ----+
workspace ----+--> narrow local ports --> controlstore/sqlite
handoff ------+                       --> gitrepo
review -------+                       --> tracker/fake
integration --+

scenario tests --> public capability commands + helper process
cmd helper     --> same capability commands
```

Rules:

- `cmd/` imports capabilities; capabilities never import `cmd/`.
- Domain models do not import `database/sql`, the SQLite driver, `os/exec`, or
  fake tracker implementation.
- The SQLite adapter may import domain record types but may not decide whether
  a transition is legal.
- Git operations accept canonical repository/workspace IDs and explicit paths;
  no adapter searches upward from ambient CWD during a mutation.
- The scenario package cannot write control tables directly to manufacture a
  passing state. Fixtures use seed APIs limited to immutable initial data.
- No `util`, `common`, `manager`, or mega-interface package is introduced.

## Data Flow

### Start run

```text
StartRun command
  -> validate WorkRevision and mission eligibility
  -> SQLite CAS: create operation + run + lease generation/token
  -> fake tracker compare/set assignment with operation marker
  -> reserve WorkspaceID/path/branch
  -> git worktree create
  -> verify pinned base
  -> apply pinned dependency candidates atomically
  -> verify branch ownership/head
  -> SQLite CAS: publish workspace_ready
  -> return Run capsule
```

Every arrow after the first SQLite transaction is a durable saga stage. Retry
loads the operation and verifies actual external state. Directory or branch
existence alone never advances a stage.

### Expiry and replacement

```text
heartbeat deadline passes
  -> SQLite CAS expires generation N
  -> workspace N becomes quarantined
  -> stale token N is rejected everywhere
  -> replacement creates generation N+1
  -> replacement allocates a new WorkspaceID/path/branch
```

The old process may still write. Safety depends on isolated workspace and
authority checks, not process termination.

### Handoff and review

```text
SubmitHandoff
  -> revalidate lease/token/revision
  -> verify candidate commit and ancestry
  -> commit immutable handoff/evidence

SubmitReview
  -> verify ActorID independence policy
  -> verify handoff candidate/revision still eligible
  -> commit verdict + criterion results atomically
  -> work becomes approved or changes_requested
```

### Integration

```text
QueueIntegration
  -> acquire (RepositoryID, TargetRef) mutation lease
  -> record target/candidate preflight SHAs
  -> create detached merge workspace
  -> apply candidate
  -> atomically advance target ref through Git
  -> commit integration result
  -> commit work done exactly once
  -> mark cleanup eligible
```

Recovery treats target movement as fact to probe. It never trusts `current_phase`
alone when deciding whether to create another commit or `done` record.

## Control Store Design

Initial tables, subject to the detailed data model:

- `projects`, `repositories`;
- `work_revisions`, `work_items`, `criteria`, `dependencies`, `lanes`;
- `actors`, `processes`;
- `operations`, `operation_stages`;
- `runs`, `leases`, `workspaces`;
- `handoffs`, `handoff_evidence`;
- `reviews`, `review_criteria`;
- `integration_attempts`, `integration_queue`;
- `events`, `outbox`, `idempotency_keys`.

Critical constraints:

- one active implementation lease per `(work_id, work_revision_id)`;
- unique `(operation_kind, idempotency_key)`;
- unique workspace path and owned branch per repository;
- workspace lease binding is immutable after insert;
- review uniqueness follows explicit review-slot policy;
- one active integration mutation lease per `(repository_id, target_ref)`;
- handoff/review/integration candidate SHAs are immutable;
- foreign keys enabled and schema versioned through forward-only migrations.

Use transaction-local compare conditions in `UPDATE ... WHERE revision = ?`
and verify affected-row counts. Process-local mutexes are optimizations only and
never the correctness mechanism.

## Git Safety Design

- Execute Git as argv, never through a shell command string.
- Resolve and validate repository common directory once at project binding.
- Construct workspace paths from WorkspaceID below one managed root.
- Refuse symlink escapes and paths outside the managed root.
- Record branch owner, base SHA, expected HEAD, and operation ID before creation.
- Treat `git worktree add` success as one stage, not readiness.
- Never reuse the primary checkout for implementation/review/repair.
- Never delete dirty, unknown, active, or quarantined worktrees.
- Preserve candidate branches and commits on conflict.
- Serialize target mutation in SQLite and re-probe target immediately before
  update.

## Fault-Injection Strategy

Implement the named boundaries in `docs/CONCURRENCY_SCENARIOS.md`:

- `SR-00` through `SR-09` for start-run;
- `HR-00` through `HR-07` for handoff/review;
- `IG-00` through `IG-09` for integration.

The helper process accepts a scenario operation and one fault boundary. The
controller can block, release, terminate, drop a response, return a provider
error, or advance the deterministic clock. Test assertions read application
queries, Git refs/trees, filesystem identity, and provider state.

Every scenario asserts the universal safety properties before checking its
specific expected result. This prevents a test from “passing” its local
assertion while violating workspace transfer, primary checkout isolation, or
target-ref serialization elsewhere.

## Implementation Concern Map

### IC-01 — Go module, deterministic primitives, and helper process

- **Purpose**: Establish the smallest buildable module, stable IDs, clock,
  typed errors, fault protocol, and independent-process helper.
- **Relevant requirements**: FR-001, FR-018, FR-019; NFR-003, NFR-008.
- **Affected surfaces**: `go.mod`, `cmd/spk-phase1-helper`, `internal/ids`,
  `internal/faults`.
- **Sequencing/depends-on**: none.
- **Risks**: Building generic frameworks before a scenario consumes them.

### IC-02 — SQLite control store and migrations

- **Purpose**: Provide transactional uniqueness, immutable bindings, operation
  stages, and queryable audit state under real process contention.
- **Relevant requirements**: FR-002 through FR-005, FR-022; NFR-001 through
  NFR-004; C-001, C-002.
- **Affected surfaces**: `internal/controlstore`,
  `internal/controlstore/sqlite`.
- **Sequencing/depends-on**: IC-01.
- **Risks**: Deferred-transaction upgrade races, per-connection PRAGMA drift,
  long readers, or retries that hide `SQLITE_BUSY`.

### IC-03 — Immutable WorkRevision and reconciliation

- **Purpose**: Pin the executable graph and define generation dispositions
  independently of tracker or workspace mechanics.
- **Relevant requirements**: FR-002, FR-017.
- **Affected surfaces**: `internal/workgraph` and its store records.
- **Sequencing/depends-on**: IC-01, then persistence through IC-02.
- **Risks**: Treating text/version changes as automatically equivalent or
  letting a disposition mutate integrated history.

### IC-04 — Fake tracker and external-effect saga

- **Purpose**: Prove timeout, ambiguity, revision CAS, idempotency, and
  compare-before-compensate without borrowing execution semantics.
- **Relevant requirements**: FR-015, FR-016.
- **Affected surfaces**: `internal/tracker`, `internal/tracker/fake`.
- **Sequencing/depends-on**: IC-01; composes with IC-05.
- **Risks**: Co-locating provider state with SQLite control transactions and
  accidentally eliminating the crash window.

### IC-05 — Run lease and staged start operation

- **Purpose**: Grant one authority, heartbeat/expire it, and orchestrate
  tracker/workspace effects idempotently.
- **Relevant requirements**: FR-003 through FR-008, FR-018.
- **Affected surfaces**: `internal/execution` plus store/provider ports.
- **Sequencing/depends-on**: IC-02, IC-03, IC-04, IC-06 ports.
- **Risks**: Actor-based resume instead of operation identity; token checks at
  some but not all mutation boundaries.

### IC-06 — Git repository and workspace adapter

- **Purpose**: Create, verify, quarantine, inspect, and safely clean real
  worktrees with explicit branch/base ownership.
- **Relevant requirements**: FR-005, FR-006, FR-008, FR-009, FR-020;
  NFR-005, NFR-007; C-003, C-007.
- **Affected surfaces**: `internal/gitrepo`, `internal/workspace`.
- **Sequencing/depends-on**: IC-01 and workspace records from IC-02.
- **Risks**: Ambient CWD, symlink/path escape, existence-as-readiness, or
  destructive cleanup.

### IC-07 — Immutable handoff and review

- **Purpose**: Make candidate/evidence immutable and enforce actor-level
  independence with transactional verdict persistence.
- **Relevant requirements**: FR-010, FR-011.
- **Affected surfaces**: `internal/handoff`, `internal/review`.
- **Sequencing/depends-on**: IC-02, IC-03, IC-05, IC-06.
- **Risks**: Reading moving refs, confusing ProcessID separation with reviewer
  independence, or storing verdict authority in Git.

### IC-08 — Serialized integration and recovery

- **Purpose**: Mutate one target ref at a time, recover Git/DB crash windows,
  emit `done` exactly once, and preserve conflict lines.
- **Relevant requirements**: FR-012 through FR-014.
- **Affected surfaces**: `internal/integration`, `internal/gitrepo`.
- **Sequencing/depends-on**: IC-02, IC-03, IC-07.
- **Risks**: Locking by mission rather than target ref, trusting cached phase,
  or deleting conflict evidence.

### IC-09 — Phase 1 scenario harness

- **Purpose**: Execute every start, zombie, review, integration, tracker, and
  WorkRevision scenario with universal assertions.
- **Relevant requirements**: all; especially FR-019 and NFR-001 through
  NFR-005.
- **Affected surfaces**: `internal/scenario`, helper process, test fixtures.
- **Sequencing/depends-on**: IC-01 through IC-08 incrementally.
- **Risks**: Monolithic end-state test, sleeps instead of barriers, direct table
  mutation, or mocks replacing the mechanisms being proved.

### IC-10 — Evidence and ADR verdict

- **Purpose**: Turn scenario results into explicit ratify/revise/reject decisions
  for authority, lane/workspace, review, and integration.
- **Relevant requirements**: mission SC-014.
- **Affected surfaces**: mission contracts, test transcripts, ADRs, final review.
- **Sequencing/depends-on**: IC-09.
- **Risks**: Treating a green subset as proof or allowing a failing scenario to
  become an undocumented follow-up.

## Delivery Slices

Each slice ends with one running scenario; package scaffolding alone is not
progress.

1. **Authority seed**: module + IDs + SQLite + fake tracker; same-work claim race
   proves exactly one valid lease.
2. **Workspace isolation**: real Git + staged allocation; two disjoint claims
   run concurrently in separate worktrees.
3. **Crash recovery**: kill after every `SR-*` boundary; retry is idempotent and
   existence never equals readiness.
4. **Zombie containment**: expire W1, create W2, resume writer in W1, reject all
   stale control operations.
5. **Judgment**: immutable handoff, same-actor refusal, distinct reviewer
   approval/rejection, verdict contention.
6. **Convergence**: target-ref integration serialization, crash matrix,
   conflict preservation, cross-mission contention.
7. **Graph reality**: four WorkRevision dispositions and post-collapse cycle
   rejection.
8. **Torture closeout**: complete suite, race detector, `-count=100`, evidence,
   ADR verdict.

## Validation Strategy

### Unit and model tests

- ID parsing/round-trip and cross-type non-interchangeability;
- WorkRevision digest, graph validation, and reconciliation dispositions;
- legal/illegal run, workspace, review, and integration transitions;
- reviewer independence predicate;
- compare-before-compensate predicate;
- typed failure schema and retry classification.

### Adapter tests

- SQLite startup pragma verification and migration idempotency;
- multiple process claim/heartbeat/expiry contention;
- busy timeout translated to typed result;
- fake tracker timeout-after-apply and revision conflict;
- real Git worktree create/recover/quarantine/dirty cleanup refusal;
- branch movement and dependency conflict detection.

### Scenario tests

Implement every P1 and WR scenario in `docs/CONCURRENCY_SCENARIOS.md`. Use
barriers and controlled clocks instead of probabilistic sleeps. Record exact
Git SHAs and stable IDs in failure output.

### Acceptance commands

```text
go test ./...
go test -race ./...
go test ./internal/scenario -run Phase1 -count=100
```

The final command may be sharded by scenario family if runtime exceeds the CI
budget, but every family must still execute 100 iterations in release evidence.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| SQLite appears atomic across external effects | Separate tracker durability; persist saga stages; inject ambiguous outcomes. |
| WAL is treated as distributed | Enforce single-host local filesystem invariant; reject known shared filesystems. |
| Busy handling hides deadlocks | Bounded busy timeout; typed busy result; no infinite retries. |
| Driver behavior changes | Pin `modernc.org/sqlite v1.57.0`; verify pragmas and DSN behavior in tests. |
| Lane and workspace collapse again | Separate packages/types and immutable Workspace-to-Lease binding. |
| Zombie test passes by killing zombie | Explicitly require zombie write/commit success before stale command refusals. |
| Review evidence leaks to Git | SQLite review tables are authority; no review-file commit path in Phase 1. |
| Integration lock is too narrow | Unique key includes repository and target ref, tested across two missions. |
| Fault harness contaminates product logic | One no-op interface at named boundaries; scenario behavior remains in tests/helper. |
| Architecture airport appears | Every package must be consumed by a passing vertical scenario in its first WP. |
| Python mechanics return through test fixtures | Fixtures compare semantic outcomes and stable IDs, not file placement. |
| Scope expands to Beads/API | Dependency and source-tree guards fail if deferred packages/imports appear. |

## Complexity Tracking

No charter violations are known. The apparent number of capabilities reflects
distinct authority state machines, but delivery remains incremental. Packages
that do not earn their existence through a Phase 1 scenario are removed rather
than retained as future scaffolding.

## Plan Exit Criteria

- Every FR and NFR maps to at least one implementation concern and scenario.
- SQLite driver, WAL, transaction, busy, and same-host decisions are explicit.
- Lane lineage, run lease, and workspace lifetime are separate in code and data.
- Review consumes immutable handoff commits and stores verdict authority in
  SQLite.
- Integration serializes by repository+target ref and has a named crash matrix.
- The fake tracker cannot provide hidden mission/execution authority.
- No deferred integration appears in source structure or dependency plan.
- Work packages can be sliced into independently reviewable vertical results.
