# Spec Kitty Go Re-architecture: Technical Project Plan

Status: Behaviorally characterized revised draft; authority ADRs may be
ratified after the inventory amendments are recorded, while concurrency
mechanism ADRs remain provisional through the Phase 1 proving experiment

Working repository: `go-kitty3`

Date: 2026-08-23

## 1. Executive decision

Build a new Go implementation of Spec Kitty around capability boundaries rather
than porting the Python package structure.

The implementation will preserve Spec Kitty's product method and its strongest
execution property: multiple agents can work safely and concurrently in
isolated lanes and worktrees, then pass through independent review and
controlled integration.

Beads will be the default first-party ticket substrate. It will own the normal
work-tracker concerns: work descriptions, dependency relationships, assignment,
discussion, and human-visible acceptance text. Spec Kitty will continue to own
mission methodology, governance, execution leases, lane/worktree isolation,
review authority, merge policy, and final mission acceptance.

The architectural shorthand is:

```text
Beads:       What work exists, what blocks it, and its visible ownership
Spec Kitty:  Whether work may run, where it runs, and how it is accepted
Git:         The authoritative code and integration history
```

Beads is the default ticket substrate, not the architectural substrate. All
tracker behavior remains behind narrow Go contracts so another provider can be
selected without replacing the mission or execution engines.

The architecture was independently reviewed by Mini on 2026-08-23. Her
accepted findings materially changed workspace replacement, work-revision
reconciliation, reviewer independence, saga compensation, machine authority,
and delivery order. The resolution record is in
`docs/MINI_REVIEW_RESOLUTION.md`.

The current Python host was then characterized at commit
`d060cff9a5c9f8cf369c8786e5bf9b4f89931d0a`. The resulting product contracts,
intentional retirements, incident evidence, and required amendments are in
`docs/PYTHON_BEHAVIOR_INVENTORY.md`; the executable Phase 1 fault matrix is in
`docs/CONCURRENCY_SCENARIOS.md`.

## 2. Why this project exists

Earlier Zig and Go experiments demonstrated that changing implementation
language does not automatically produce a native design. A transliteration can
retain Python's module boundaries, artifact-driven control flow, dynamic
dispatch habits, state duplication, and filesystem conventions even when the
new syntax is Go or Zig.

This project is therefore a behavior-preserving re-architecture, not a package
port.

The desired synthesis is:

- Spec Kitty's mission method, governance, work-package discipline, isolated
  concurrency, implementation/review separation, and controlled merge.
- Beads' ticket metaphor, dependency graph, durable comments, human-visible
  acceptance fields, offline-first operation, and agent-oriented work queries.
- ThickTicket's useful identity distinction between durable actors, external
  provider identities, and ephemeral execution processes.
- A small Go composition root with cohesive vertical capabilities, typed
  contracts, explicit effects, and replaceable adapters.

The project explicitly rejects the idea that one of those systems should be
copied wholesale.

## 3. Product outcomes

### 3.1 Primary outcomes

1. Preserve the complete software-development mission arc where it remains
   valuable:

   ```text
   specify -> plan -> decompose -> implement/review -> accept -> merge
           -> mission review -> retrospective
   ```

2. Replace the hand-maintained Markdown task list with a tracker-backed,
   dependency-aware work graph.
3. Preserve and strengthen concurrent agent execution in isolated worktrees.
4. Make Beads the zero-configuration default while retaining a real tracker
   substitution boundary.
5. Keep Team Kitty, the Orchestrator API, MCP, CLI, and future clients as
   integrations over the same application capabilities rather than alternate
   control planes.
6. Provide a migration path from existing Python Spec Kitty projects without
   requiring Python at runtime.
7. Make correctness observable and testable under crashes, contention, stale
   processes, dirty repositories, provider failures, and merge conflicts.

### 3.2 Secondary outcomes

- Reduce agent tool calls spent creating, parsing, and checking task Markdown.
- Give humans a normal ticket view without weakening mission governance.
- Permit an actor to have multiple simultaneous agent processes without
  confusing identity with execution ownership.
- Make every mutating operation idempotent or explicitly non-repeatable.
- Provide stable machine-readable JSON contracts for agent harnesses.

## 4. Non-goals

The first production release will not:

- Reproduce Python source layout, class hierarchy, decorators, or command
  implementation patterns.
- Reimplement Beads, Dolt, Git, or a general issue-tracker database.
- Adopt Beads Molecules, Gas Town roles, or merge slots as Spec Kitty workflow
  law.
- Make tracker status the mission state machine.
- Make Team Kitty a required service or source of truth.
- Support arbitrary in-process binary plugins.
- Build a general workflow DSL before the first built-in mission type is proven.
- Guarantee full bidirectional synchronization with every external tracker.
- Preserve every historical file layout when a compatibility projection is
  sufficient.
- Use chat transcripts as canonical work, decision, or handoff state.

## 5. Product laws

These are acceptance invariants, not implementation preferences.

### 5.1 Mission authority law

A mission is more than an epic or collection of tickets. The mission engine is
authoritative for intent, phase, governance context, required artifacts,
acceptance gates, and terminal outcome.

No tracker adapter may advance a mission merely because an issue status changed.

### 5.2 Work authority law

Exactly one configured tracker is the primary mutable authority for work
definition during planning and decomposition. Other ticket systems may be
mirrors, exports, or future alternative primaries, but two providers cannot both
be writable authorities for the same mission.

Before execution, Spec Kitty pins a `WorkRevision`: an immutable snapshot of
work IDs, descriptions, dependencies, acceptance criteria, and relevant tracker
versions. Execution consumes that revision. Later tracker edits are surfaced as
drift and require explicit reconciliation; they do not silently change a
running agent's contract.

Each `WorkRevision` is a numbered generation of the executable graph and names
its predecessor. Reconciliation creates a new generation and records an
explicit disposition for affected work and active runs:

- `continue`: the effective executable contract is unchanged and the current
  run remains eligible for review and acceptance;
- `supersede`: replacement work becomes current; the old run may finish only as
  recoverable evidence and cannot be accepted or integrated;
- `invalidate`: the old contract is no longer valid; active work is stopped at
  the Spec Kitty authority boundary and its workspace is quarantined;
- `fork`: the old and new work are intentionally retained as distinct valid
  lines with separate stable WorkIDs and dependencies.

Reconciliation is a mission decision, not a tracker side effect. A dependency
or criterion edit cannot select its own disposition.

### 5.3 Execution authority law

Tracker assignment is visible ownership. A Spec Kitty run lease is execution
authority. An agent may not mutate a worktree merely because a ticket is
assigned to it.

Every execution attempt must bind:

```text
MissionID + WorkID + WorkRevision + ActorID + ProcessID
          + RunID + LaneID + WorkspaceID + Lease
```

### 5.4 Isolation law

Parallel implementation runs never share a mutable checkout. Each run receives
an isolated worktree rooted at a recorded base commit. Two agents must be able
to work on different work packages in the same repository without overwriting
files, branch pointers, task state, or tracker state.

A mutable workspace belongs permanently to the lease generation that created
it. A replacement lease never inherits that workspace. When a lease expires or
is revoked, its workspace is quarantined and excluded from normal execution,
review, and integration. The replacement receives a newly-created workspace.
Potentially useful changes from the quarantined workspace require an explicit,
quiescence-checked reconciliation operation.

### 5.5 Singular integration law

Implementation may fan out. Integration is serialized through a single
Spec Kitty integration authority. Tracker-native merge locks or provider status
transitions are advisory and cannot replace the integration queue.

### 5.6 Review independence law

An implementation run cannot approve its own output. Review is a separate
authority boundary with a separately recorded actor, process, verdict, and
evidence set.

The reviewer `ActorID` must differ from every implementation `ActorID`
contributing to the candidate. A different `ProcessID` belonging to the same
actor is not independent review. Phase 1 has no waiver or bypass. Any future
exception would be a separately specified and ratified feature, not an
interpretation of this law.

### 5.7 Durable coordination law

Information required to resume or judge work must be durable. Work discussion
belongs on the configured tracker when it is human-facing. Execution facts,
leases, commands, and gate evidence belong in Spec Kitty's control state.
Private transcript content is never the only record of a blocker, decision,
handoff, or rejection.

### 5.8 Derived-document law

`tasks.md`, checkboxes, dashboards, and status summaries are projections. Agents
must never be required to maintain the same state in both a tracker and a
Markdown document.

### 5.9 Integration boundary law

CLI, MCP, Team Kitty, and the Orchestrator API call the same application command
and query services. None may import storage implementations or mutate mission,
work, run, or Git state directly.

### 5.10 Honest recovery law

Cross-system operations are explicit sagas with durable steps and compensating
actions. The system must never claim an atomic transaction across SQLite, Beads,
the filesystem, and Git when no such transaction exists.

### 5.11 Attribution-safe compensation law

A compensation may undo an external effect only while the current external
state is demonstrably attributable to the operation being compensated. The
operation records the provider revision, expected value, actor, and operation
marker needed for compare-before-compensate. If a human or later operation has
changed the state, recovery records a conflict or requests a decision rather
than overwriting it.

### 5.12 Single-machine control authority law

The first release permits multiple local processes but exactly one machine and
filesystem authority to open a Spec Kitty control store. The SQLite database is
not supported on NFS, SMB, a multi-host shared volume, or independently mounted
copies.

Remote agents use the Orchestrator API and host-owned execution tools; they do
not open the control database. An agent requiring direct shell access to a
worktree executes within the same machine authority that owns the control store.
Distributed control-store ownership is a separate future architecture.

## 6. Conceptual model

### 6.1 Stable identities

| Identity | Meaning | Canonical owner |
|---|---|---|
| `ProjectID` | Configured repository/project | Project capability |
| `MissionID` | One governed mission instance | Mission capability |
| `WorkID` | Provider-independent work identity | Work capability |
| `ExternalWorkRef` | Provider, connection, remote ID | Tracker adapter |
| `WorkRevisionID` | Immutable executable work-graph snapshot | Mission/work bridge |
| `CriterionID` | Stable acceptance statement | Work revision |
| `ActorID` | Durable human, agent, role, or system principal | Actor capability |
| `ExternalSubjectRef` | Actor identity in Beads, GitHub, Jira, etc. | Identity mapping |
| `ProcessID` | One agent or client process | Runtime boundary |
| `RunID` | One attempt to perform or review work | Execution capability |
| `LaneID` | Scheduling and integration isolation boundary | Lane capability |
| `WorkspaceID` | Concrete worktree/checkout | Workspace capability |
| `ReviewID` | One review verdict and evidence set | Review capability |
| `IntegrationID` | One merge/promotion attempt | Integration capability |

The implementation should define each ID exactly once. Avoid the prototype
failure mode where separate packages invent incompatible `ActorID` types.

### 6.2 Mission and work are related but not identical

```text
Project
  Mission
    MissionRevision
    WorkRevision
      Lane 1..N
        Ordered WorkID references
        Accepted candidate lineage
      WorkItem 1..N
        Criterion 1..N
        Dependency 0..N
        Run 0..N
          Workspace
          Handoff
          Review 0..N
          Integration 0..N
```

A Beads epic can represent a mission in the ticket surface, but it does not
contain the whole mission. A work package normally maps one-to-one to a Beads
issue, but Spec Kitty retains its own stable `WorkID` and stores the mapping.

### 6.3 Actor, assignment, and process

- `ActorID` is durable: `lynn`, `robert`, `codex-reviewer`, or `qa`.
- `ProcessID` is ephemeral: one launched Codex/Claude/local process.
- `ExternalSubjectRef` maps the actor to a provider-specific identity.
- Assignment says who is expected to own the visible ticket.
- A run lease says which process may currently execute it.

This permits two processes belonging to the same durable actor without
collapsing their workspaces or audit trails.

## 7. Source-of-truth matrix

| Concern | Authority | Projections or mirrors |
|---|---|---|
| Mission intent and approved artifacts | Versioned mission artifacts | Team Kitty, dashboard |
| Mission lifecycle and decisions | Spec Kitty control store | CLI/API status, tracker comments |
| Mutable work definition before finalization | Primary tracker (Beads by default) | Derived `tasks.md` |
| Executable work graph | Pinned `WorkRevision` | Tracker labels/status summaries |
| Visible assignment and discussion | Primary tracker | Team Kitty |
| Execution leases and attempts | Spec Kitty control store | Tracker assignment/status |
| Lane and workspace topology | Spec Kitty workspace service | Dashboard/API |
| Source code and commit history | Git | Handoff artifacts |
| Review verdict and evidence | Spec Kitty review store | Tracker comment/status |
| Integration and merge outcome | Git plus Spec Kitty integration record | Tracker closure, Team Kitty |
| Actor identity | Spec Kitty actor directory | Provider identity links |
| External synchronization cursor | Connector adapter state | Diagnostics |

The primary tracker can be replaced. The authorities in the right-hand column
cannot silently become primary because an integration is convenient.

## 8. Architectural shape

### 8.1 Small mechanism-only kernel

The kernel should contain only universally shared mechanics:

- ID construction and validation.
- Clocks and deterministic test clocks.
- Typed errors and error classification.
- Event envelope and causation metadata.
- Idempotency keys.
- Transaction and outbox primitives for the Spec Kitty control store.
- Path safety and repository-root identity.

The kernel must not contain mission steps, tracker statuses, Git commands,
agent roles, or provider conditionals.

### 8.2 Vertical capabilities

Each capability owns its domain types, policies, application operations, store
ports, and contract tests. Adapters implement ports from the outside.

Proposed first-party capabilities:

| Capability | Responsibilities |
|---|---|
| `project` | Project discovery, configuration, repository identity |
| `actor` | Actors, external identity links, process attribution |
| `mission` | Mission type, steps, transitions, decisions, revisions |
| `artifact` | Versioned spec/plan/research/review artifact locations |
| `work` | Stable work IDs, tracker-neutral graph, finalization, drift |
| `execution` | Run attempts, leases, heartbeats, handoffs, recovery |
| `lane` | Lane allocation and concurrency policy |
| `workspace` | Git worktrees, branch ownership, cleanup, safety checks |
| `review` | Reviewer assignment, evidence, verdicts, rejection cycles |
| `integration` | Acceptance preflight, merge queue, merge, conflict work |
| `doctrine` | Action-scoped governance and profile resolution |
| `sync` | Optional primary/mirror tracker synchronization |
| `projection` | Dashboard, Markdown, status, and compatibility views |

This table is a target ownership map, not permission to scaffold fourteen
packages at the start. A capability becomes a package only when an executable
slice demonstrates an independent policy boundary. Phase 1 begins with the
smallest cohesive implementation of control state, execution authority,
workspace isolation, review, and integration. Names and boundaries are split
only when pressure from real behavior earns the separation.

### 8.3 Narrow ports, not a mega-plugin interface

The work capability should depend on independently satisfiable ports such as:

```go
type WorkCatalog interface {
    Create(context.Context, WorkDraft) (ExternalWorkRef, error)
    Get(context.Context, ExternalWorkRef) (WorkRecord, error)
    Update(context.Context, ExternalWorkRef, WorkPatch) (WorkRecord, error)
    List(context.Context, WorkQuery) ([]WorkRecord, error)
}

type WorkGraph interface {
    AddDependency(context.Context, WorkID, WorkID, DependencyKind) error
    Dependencies(context.Context, WorkID) ([]Dependency, error)
    Ready(context.Context, ReadyQuery) ([]WorkRecord, error)
}

type WorkDiscussion interface {
    AddComment(context.Context, WorkID, CommentDraft) (CommentRef, error)
    Comments(context.Context, WorkID) ([]Comment, error)
}

type WorkAssignment interface {
    Assign(context.Context, WorkID, ActorID) error
    ClearAssignment(context.Context, WorkID) error
}
```

Acceptance definitions and provider capabilities should likewise be narrow.
Adapters declare supported capabilities at startup. A mission can reject a
provider that cannot meet its required semantics instead of discovering the
gap halfway through execution.

### 8.4 Explicit application orchestration

Application services coordinate capabilities. They should use straightforward
control flow rather than a service locator, reflective dispatch, implicit event
magic, or arbitrary hook chains.

For example, `StartImplementation` explicitly:

1. loads the mission and pinned work revision;
2. checks work readiness and governance;
3. acquires a Spec Kitty run lease;
4. allocates a lane;
5. creates an isolated worktree;
6. records the workspace binding;
7. mirrors assignment/progress to the tracker;
8. returns the run capsule.

Each completed step is durable. Failures run named compensations or leave a
recoverable operation for `doctor`.

### 8.5 Static composition first

The initial binary uses explicit constructors in one composition root. The
standard profile includes Beads, Git, local artifacts, the built-in mission
types, CLI, and optional HTTP/MCP transports.

Runtime-loaded Go plugins are out of scope. Future third-party executable
integrations should use a versioned process or network protocol. This retains
modularity without inheriting ABI fragility or plugin discovery magic.

## 9. Beads integration

### 9.1 Role

Beads is the standard provider for `WorkCatalog`, `WorkGraph`,
`WorkDiscussion`, visible assignment, and work-history projections.

Use the published Go consumer API behind `adapters/tracker/beads`. Pin an exact
compatible Beads version. Do not import Beads `internal` packages, issue direct
SQL against its schema, or copy its domain structs into the Spec Kitty kernel.

The initial conformance spike must validate whether the public Go API covers
every required operation. Any CLI fallback must be isolated behind the same
adapter and consume documented JSON, never human-formatted output.

### 9.2 Mapping

| Spec Kitty concept | Beads representation |
|---|---|
| Mission ticket surface | Epic bead |
| Work package | Child issue |
| Blocking dependency | `blocks` dependency |
| Non-blocking provenance | `discovered-from` or related edge |
| Human work brief | Description |
| Design constraints | Design field |
| Acceptance definitions | Acceptance field plus namespaced metadata |
| Durable discussion | Comments |
| Visible owner | Assignee string mapped from `ActorID` |
| Completion summary | Close reason/comment |
| Spec Kitty identity | `spec_kitty.*` namespaced metadata |

Spec Kitty should use hash-style Beads IDs for concurrent creation. Sequential
IDs are not suitable for the default multi-agent profile.

### 9.3 Structured acceptance without checkbox labor

The decompose operation produces structured criteria before writing the issue:

```go
type Criterion struct {
    ID          CriterionID
    Text        string
    Verifier    VerifierKind
    Required    bool
    EvidenceHint string
}
```

The Beads acceptance field renders these for humans. Namespaced metadata retains
stable criterion IDs and schema version where supported. Finalization reads the
structured representation into the immutable `WorkRevision`.

Agents do not toggle criteria manually. Tests, review verdicts, policy gates, or
explicit human decisions attach evidence to the criterion in Spec Kitty's
review state. A projection can show open/passed/failed results in Beads without
making that projection authoritative.

### 9.4 Concurrency mode

The adapter must recognize the operational distinction between Beads embedded
and server modes.

- A single-agent or read-heavy local workflow may use embedded mode.
- A mission permitting multiple concurrent writer processes must use a
  multi-writer-safe Beads configuration or an adapter-owned serialization
  mechanism proven by contention tests.
- Startup performs a capability and health preflight before the first parallel
  run.
- Worktrees must resolve to the same canonical Beads workspace. Spec Kitty must
  not create one independent ticket database per worktree.

### 9.5 Beads claims are useful but not sufficient

The adapter may use Beads' atomic claim API to update visible ownership. Spec
Kitty correctness must still rely on its own run-lease uniqueness constraint.
This is required because alternate providers may not have atomic claims and
because filesystem workspace creation cannot participate in the Beads
transaction.

The claim/start workflow is a saga:

```text
Acquire Spec Kitty execution lease
  -> claim/assign Beads issue
  -> allocate lane
  -> create worktree
  -> bind workspace to run
  -> mark run active
```

If a later step fails, the operation records its exact stage. Recovery either
finishes the start or releases the lease. It removes a newly-created workspace
only when it can prove that no process received access and the path remains
owned and clean; otherwise it quarantines the workspace. It clears the tracker
assignment only when it was made by this attempt.

“Made by this attempt” is proven at compensation time, not inferred only from
the local operation log. The adapter performs compare-before-compensate using
the strongest available provider primitive: revision/ETag, expected assignee,
operation marker, or guarded update. If the current assignment no longer
matches, recovery leaves it untouched and records a reconciliation conflict.

### 9.6 Features intentionally not adopted

The standard adapter will not use the following as mission authority:

- Molecules or formulas as the mission engine.
- Beads agent records as the actor directory.
- Beads merge slots as the integration queue.
- Custom statuses as Spec Kitty step states.
- Compaction as canonical mission-memory deletion.
- Provider-side hooks that mutate Git or mission state without going through
  application commands.

## 10. Mission methodology

### 10.1 Mission definitions

A mission type is a versioned Go capability exposing typed step contracts and
transition policy. Built-in mission types are statically registered.

Configuration may select templates, doctrine packs, optional steps, and policy
parameters. It must not become a second programming language for repository
movement or authority rules.

The initial release implements only the software-development mission type.
Research, documentation, and custom/minimal missions are preserved in the
behavior inventory and added only after the software-development path validates
the mission-type boundary. The architecture must not build four speculative
implementations merely to prove that a registry can hold them.

### 10.2 Software-development steps

| Step | Output and gate |
|---|---|
| Initialize | Project, actor, tracker, Git, and governance preflight |
| Specify | Approved intent, scope, requirements, acceptance framing |
| Plan | Architecture, data flow, risks, compatibility, validation strategy |
| Decompose | Tracker-backed work graph with dependencies and criteria |
| Finalize work | Immutable executable `WorkRevision` and drift digest |
| Execute | Dependency-aware implementation/review loops in parallel lanes |
| Accept | Mission-wide requirement and invariant coverage gate |
| Merge | Serialized integration into the target branch |
| Mission review | Post-merge spec-to-code fidelity and risk review |
| Retrospective | Non-blocking learning record and closeout |

`tasks` remains a compatibility command alias if needed, but its semantic action
is `decompose`. It creates or reconciles work items; it does not author a
canonical `tasks.md` checklist.

### 10.3 Work-revision reconciliation

Finalization creates generation 1 of the executable graph. Later accepted
tracker edits create a candidate generation rather than mutating generation 1.
The reconciliation command compares generations and requires one disposition
for every affected active or accepted work item.

Each generation also pins the planning source commit, graph digest,
post-collapse lane DAG, provider revisions, dependency satisfaction levels,
and write-scope decisions. Each run records the exact accepted dependency
candidate commits used to prepare its base. A finalized revision is rejected if
lane collapse produces a cyclic lane graph.

Rules:

- Unaffected work retains identity and may continue.
- A `continue` decision records why the semantic contract is equivalent even if
  tracker text or provider revision changed.
- Superseded or invalidated runs cannot submit an acceptable handoff under the
  old generation.
- Invalidating an active run expires its authority and quarantines its
  workspace; it cannot stop the underlying OS process from writing.
- Forking allocates distinct WorkIDs so both lines can be scheduled and judged
  without ambiguous ancestry.
- Accepted but not integrated work is re-evaluated when dependencies or
  criteria affecting it change.
- Integrated history is never rewritten by reconciliation. Necessary follow-up
  becomes new repair or replacement work.
- The mission records who approved reconciliation and the old/new graph
  digests.

The scheduler, reviewer, accept gate, and integration queue all reject work
whose revision disposition does not permit their action.

### 10.4 Action-scoped governance

Doctrine and agent profiles are loaded for the action being performed. A run
capsule contains the relevant governance digest and explicit authority. Agents
should not receive the entire doctrine catalog on every call.

The run records the profile and doctrine revision so a later review can explain
which governance governed the work.

### 10.5 `next` remains the simple agent surface

The canonical agent loop should stay compact:

```text
spec-kitty next --mission <id> --json
```

It returns one of a small number of typed outcomes:

- `query`: more user or system information is required;
- `action`: perform a mission step;
- `work_ready`: one or more work items can be claimed;
- `review_ready`: review work is available;
- `decision_required`: explicit human authority is needed;
- `blocked`: a durable blocker exists;
- `terminal`: the mission reached a defined outcome.

The engine decides what kind of action is legal. The transport only renders the
result.

## 11. Concurrency and workspace design

### 11.1 Shared control root

All worktrees for one Git repository must discover the same project identity,
Spec Kitty control database, and Beads database.

For Git projects, runtime control state should be anchored from the Git common
directory rather than a worktree-local `.git` file. A project identity file
records the repository root and protects against accidentally binding two
unrelated repositories to the same control state.

Versioned user artifacts remain in the repository. Ephemeral runtime and leases
do not appear in feature branches.

### 11.2 Control-state persistence

Use a small Spec Kitty control database with transactional uniqueness and an
outbox. SQLite in WAL mode is the leading implementation choice because it
supports multiple local processes, compare-and-swap state changes, crash-safe
transactions, and operational inspection without requiring a daemon.

This is a single-machine design. Only processes under the machine/filesystem
authority owning the project may open the database. Remote clients cross the
Orchestrator API boundary. Startup refuses known network/shared filesystem
locations where SQLite locking semantics cannot meet the contract.

The Beads/Dolt database and Spec Kitty control database have separate authority.
Cross-database actions use sagas and idempotency keys.

The schema should begin with:

- projects and repository identities;
- missions and mission transitions;
- decisions and blockers;
- work mappings and immutable work revisions;
- actors, provider identities, and processes;
- runs, leases, heartbeats, and operation stages;
- lanes and workspace bindings;
- handoffs, reviews, criteria evidence, and verdicts;
- integration attempts and merge queue;
- events, outbox entries, and projection cursors;
- idempotency records and schema version.

### 11.3 Run lease

A run lease is a durable compare-and-swap record with:

- work and revision identity;
- role (`implement`, `review`, `repair`, or a mission-defined role);
- actor and process;
- acquired, heartbeat, and expiry timestamps;
- current lifecycle state;
- lane/workspace binding;
- fencing token incremented on every new lease.

Spec Kitty commands that mutate run, workspace, review, or integration state
present the fencing token. This prevents a stale process from using the control
plane, but it cannot prevent arbitrary POSIX writes by a surviving shell, IDE,
or agent process. Filesystem safety therefore comes from never transferring a
mutable workspace between lease generations.

On expiry or revocation, the control plane:

1. marks the old run non-authoritative;
2. quarantines its workspace and branch from normal automation;
3. refuses handoff, review, acceptance, or integration from its fencing token;
4. allocates a new workspace and branch for any replacement lease;
5. leaves the old workspace intact until an explicit reconciliation proves the
   writer is quiescent and decides whether to preserve any output.

### 11.4 Lane lineage and run/workspace lifecycle

A lane is durable scheduling and integration lineage. It records the ordered
work assigned to the lane, dependency-lane edges, accepted candidate history,
and current accepted head. It is not a mutable checkout and does not expire
when one process or workspace expires.

Runs and their lease-scoped workspaces have the lifecycle:

```text
available
  -> reserved
  -> workspace_ready
  -> active
  -> handoff_ready
  -> under_review
  -> accepted | changes_requested | abandoned
  -> integration_queued
  -> integrated | conflict_repair
  -> cleaned

active | workspace_ready | handoff_ready
  -> quarantined
  -> reconciled | archived
```

Transitions are validated by the run/workspace capabilities. Tracker status is
projected from these states where useful but cannot cause the transition. A
subsequent run in the same lane may start from the lane's pinned accepted head,
but always receives a new WorkspaceID, directory, branch, and lease binding.

### 11.5 Worktree contract

Each logical lane records its WorkRevision, ordered work, dependency edges,
accepted candidate sequence, and pinned accepted head.

Each concrete implementation workspace records:

- absolute canonical workspace path;
- repository identity;
- base branch and base commit;
- owned branch name;
- current head;
- work and run IDs;
- actor/process IDs;
- creation operation ID;
- lease generation and fencing token that created it;
- quarantine time and reason, when applicable;
- cleanup eligibility.

Workspace allocation is a durable staged saga. At minimum it distinguishes
lease acquisition, workspace reservation, worktree creation, base verification,
dependency-commit application, branch-ownership verification, and the final
`workspace_ready` publication. A directory or registered Git worktree existing
is never sufficient evidence that allocation completed.

Safety rules:

- Never reuse the user's primary checkout for agent implementation.
- Never delete an unrecognized or dirty worktree.
- Never clean a path using an unresolved environment variable or broad glob.
- Refuse branch ownership collisions.
- Preserve a rejected implementation workspace until explicitly superseded or
  safely archived.
- Never rebind a workspace to a new lease, process, or fencing generation.
- Quarantine an expired or revoked lease's workspace without claiming to make
  the underlying directory immutable.
- Never read a quarantined workspace as stable evidence until the writer is
  proven quiescent and a reconciliation snapshot is created.
- Review and integration consume a recorded handoff commit from a valid lease,
  never the live mutable directory.
- Make workspace creation idempotent for the same operation.
- Detect manual branch movement and require reconciliation.

A handoff is the immutable boundary between implementation and judgment. It
binds the valid lease generation, candidate commit, WorkRevision, lane lineage,
contributor ActorIDs, criterion/evidence digest, and workspace quiescence check.
Review verdicts and criterion results are committed transactionally in the
control store; optional large evidence is content-addressed. Git artifacts,
matrices, tracker comments, and dashboards are projections of that authority.

### 11.6 Dependency scheduling

The scheduler reads the pinned work revision, not a stale `bd ready` result
alone. A candidate is runnable only when:

- all hard dependencies are accepted at the required level;
- no active implementation lease exists;
- mission phase permits execution;
- actor/profile capabilities match;
- required governance context is available;
- repository and tracker preflights pass.

Beads readiness is a valuable query and consistency cross-check. A discrepancy
between Beads readiness and the pinned graph becomes a diagnostic finding.

### 11.7 Integration queue

Accepted work enters a Spec Kitty integration queue ordered by:

1. dependency topology;
2. mission priority policy;
3. acceptance timestamp;
4. stable WorkID tie-breaker.

Only one integration attempt mutates a repository target ref at a time, across
all missions. The serialization key is repository identity plus target ref. A
clean merge records the resulting commit. A conflict preserves both branches,
records evidence, and creates or reopens explicit repair work. It never silently
rewrites another lane. Resume state is an advisory progress cache; recovery
probes Git and durable integration records before declaring any phase complete.

## 12. Events, commands, and queries

### 12.1 Command rules

Every mutation is a typed command with:

- command name and schema version;
- idempotency key;
- actor and process identity;
- correlation and causation IDs;
- expected aggregate revision where relevant;
- explicit target IDs;
- validation performed before effects;
- typed result or typed failure.

Commands that touch a tracker, hosted service, or other external system also
declare the destination and identifier classes that can leave the machine,
required project identity/consent, idempotency key, expected provider
revision/value, attribution marker, retry/offline policy, and whether local
workflow may continue. Compensation is legal only while compare-before-
compensate proves the external state still belongs to that operation.

### 12.2 Event envelope

```go
type Event struct {
    ID            EventID
    Type          string
    SchemaVersion int
    Aggregate     AggregateRef
    ActorID       ActorID
    ProcessID     ProcessID
    CorrelationID string
    CausationID   string
    OccurredAt    time.Time
    Payload       json.RawMessage
}
```

Events describe committed state. They do not substitute for canonical state.
Consumers re-read the referenced aggregate before taking consequential action.

### 12.3 Outbox

State mutation and outbox insertion occur in one control-store transaction.
Projectors and outbound integrations consume the outbox idempotently. Failed
delivery cannot roll back an already-valid mission transition, and a Team Kitty
outage cannot block local work unless the user selected an explicit hosted
policy requiring it.

### 12.4 Query model

Queries return purpose-built views rather than leaking store records. The CLI,
dashboard, MCP, and HTTP API use the same query services. Query handlers are
read-only and cannot repair state as a side effect.

## 13. Integrations

### 13.1 Team Kitty

Team Kitty is an optional outbound/inbound collaboration adapter.

It may:

- consume mission, work, run, review, and integration events;
- publish human-facing status and notifications;
- submit authenticated application commands;
- synchronize explicitly supported collaboration data;
- queue outbound changes while offline.

It may not:

- mutate the control database or Beads directly;
- become required for local missions;
- infer mission transitions from UI state;
- overwrite local canonical state after a sync conflict;
- hide failed delivery.

### 13.2 Orchestrator API

The Orchestrator API is a versioned transport over application commands and
queries. It should expose:

- capability discovery;
- project and mission queries;
- `next`;
- work eligibility and claim requests;
- run heartbeat, progress, and handoff;
- review submission;
- accept and merge commands;
- event streaming with resumable cursors;
- idempotency and optimistic concurrency.

The API owns authentication, authorization, rate limits, and transport errors.
The mission engine owns sequencing and authority.

For the first release, this is also the mandatory boundary for remote agents.
Only the host-side runtime opens SQLite and owns mutable Git workspaces. A
remote model or harness may direct a host-side run through the API, but it does
not mount the control database or directly share a worktree across machines.

### 13.3 MCP

MCP tools wrap the same command/query interfaces. Tool descriptions should be
small, typed, and task-oriented. MCP does not get a privileged mutation path.

### 13.4 Alternative trackers and mirrors

Tracker configuration distinguishes:

- `primary`: the single mutable work authority;
- `mirror`: a downstream projection with declared direction;
- `migration_source`: a temporary read source during provider migration.

GitHub, Jira, ThickTicket, or another provider can later implement the narrow
work ports. Unsupported capabilities fail at mission preflight with a precise
message. The design does not force all providers into a misleading
lowest-common-denominator status model.

## 14. Proposed repository organization

The exact names may evolve, but dependencies should point inward toward domain
capabilities and application ports.

```text
cmd/
  spec-kitty/                 composition root and CLI

internal/
  kernel/                     IDs, clock, errors, events, idempotency
  app/                        explicit cross-capability use cases

  project/                    vertical capability
  actor/
  mission/
  artifact/
  work/
  execution/
  lane/
  workspace/
  review/
  integration/
  doctrine/
  sync/
  projection/

  missiontype/
    softwaredev/
    research/
    documentation/
    minimal/

  adapter/
    controlsqlite/
    trackerbeads/
    gitnative/
    filesystem/
    teamkitty/
    httpapi/
    mcp/

  transport/
    cli/
    http/
    mcp/

testkit/
  fakeclock/
  faketracker/
  fakeagent/
  gitfixture/
  faultinject/

docs/
  decisions/
  contracts/
  operations/
```

Rules enforced by import tests or a dependency analyzer:

- Domain capabilities do not import transports or concrete adapters.
- Tracker adapters do not import workspace or integration internals.
- Mission types call public capability ports, not stores.
- Team Kitty and API packages do not import storage packages.
- Git/workspace packages do not import tracker implementations.
- The composition root is the only place allowed to know all implementations.

## 15. CLI surface

The first release should favor a small coherent command tree with human and
stable JSON rendering:

```text
spec-kitty init
spec-kitty doctor

spec-kitty mission new
spec-kitty mission status
spec-kitty mission next
spec-kitty mission specify
spec-kitty mission plan
spec-kitty mission decompose
spec-kitty mission finalize-work

spec-kitty work list
spec-kitty work show
spec-kitty work ready
spec-kitty work claim
spec-kitty work release

spec-kitty run status
spec-kitty run heartbeat
spec-kitty run progress
spec-kitty run handoff

spec-kitty review submit
spec-kitty accept
spec-kitty merge
spec-kitty retrospective
```

Compatibility aliases can preserve current agent entry points. Aliases dispatch
to the same application command and emit deprecation metadata in JSON rather
than maintaining duplicate implementations.

Every command must define:

- exit-code semantics;
- JSON schema and version;
- idempotency behavior;
- whether it mutates state;
- retry safety;
- expected authority;
- possible typed blockers and decisions.

## 16. Migration and compatibility

### 16.1 Behavioral inventory

Before claiming parity, create a behavior inventory from the current Python
host. Classify behavior as:

- preserve exactly;
- preserve semantically with a new representation;
- provide through a compatibility projection;
- intentionally retire with rationale.

Priority areas are mission sequencing, work-package lifecycle, lanes/worktrees,
review rejection loops, accept/merge safety, doctrine/profile context,
orchestrator API contracts, Team Kitty sync, and recovery commands.

### 16.2 One-way importer

Provide a dry-run-first importer that:

1. discovers existing `.kittify` and mission artifacts;
2. validates the source without mutation;
3. reports unsupported or ambiguous state;
4. creates stable mission/work/actor mappings;
5. imports work definitions into Beads;
6. pins an initial work revision;
7. imports relevant history and review evidence;
8. verifies counts, dependencies, and digests;
9. writes a migration receipt;
10. leaves the Python project recoverable.

No Python interpreter is required. The importer reads known file formats using
Go. It never silently invents missing state.

### 16.3 Compatibility projections

Where users or tools still require familiar files, generate them from canonical
state:

- `tasks.md` as a read-only work summary;
- legacy status views;
- command aliases;
- export bundles for older integrations.

Generated files contain an explicit header stating that edits are not
authoritative.

### 16.4 Differential tests

Run selected scenarios through the Python host and the Go host, normalize
representation differences, and compare semantic outcomes. Differential tests
are particularly important for blocked states, rejection cycles, merge
preflights, stale worktrees, and migration.

## 17. Delivery plan

Each phase ends in an executable vertical result. Package scaffolding without a
working scenario does not count as progress.

### Phase 0: Behavioral characterization and architectural ratification

Objective: attack the proposed product laws with current Python behavior before
freezing foundational architecture. This plan is provisional input to the
inventory, not a decision that outranks observed product behavior.

Current progress:

- Behavior inventory and incident review: complete at the pinned Python commit
  recorded in `docs/PYTHON_BEHAVIOR_INVENTORY.md`.
- Phase 1 executable scenario definitions: complete in
  `docs/CONCURRENCY_SCENARIOS.md`.
- Foundational ADR ratification and selected golden Python fixtures: pending.

Work:

- Build the behavior inventory for current Python Spec Kitty.
- Capture golden command/result fixtures for critical mission transitions.
- Classify every behavior as preserve exactly, preserve semantically, project
  for compatibility, or intentionally retire.
- Use obscure recovery, lane, review, and integration behavior to challenge the
  proposed authority boundaries.
- Define the two-agent concurrency proving experiment in Phase 1.
- Define supported Beads versions and the upgrade/skew policy.
- Decide the control-store driver after a focused SQLite WAL spike.
- Define stable JSON error and result envelopes.
- Revise this plan from the inventory, then ratify product laws and foundational
  ADRs.

Exit criteria:

- Every preserved behavior has an owner and planned test.
- No foundational ADR is final until relevant legacy behavior has attacked it.
- Authority and source-of-truth matrices are approved.
- A reviewer can explain why Beads cannot advance a mission or authorize a
  worktree.
- The Phase 1 torture scenario and failure points are executable test
  descriptions.

### Phase 1: Two-agent authority proving ground

Objective: disprove the hardest architectural claim as cheaply as possible:
safe parallel execution when authority, process lifetime, filesystem state, Git
state, and recovery disagree.

This phase deliberately does not implement Beads, a complete mission, MCP,
Team Kitty, migration, projections, or multiple mission types.

Work packages:

1. Go module plus the minimum race/fault-injection test harness.
2. Stable IDs, deterministic clock, typed failures, and operation IDs needed by
   the experiment.
3. A real SQLite control database containing only work mappings, actors,
   processes, runs, leases, lanes, workspaces, handoffs, reviews, and integration
   attempts needed by the experiment.
4. A deliberately stupid fake tracker so Beads convenience cannot conceal an
   authority leak.
5. A real temporary Git repository with real branches and worktrees.
6. Atomic run leases, generations, fencing checks, heartbeat/expiry, quarantine,
   and replacement-workspace creation.
7. Minimal handoff and actor-level independent review over recorded commits.
8. Minimal serialized integration with conflict preservation.
9. A crash hook after every durable boundary.
10. One executable scenario:

   ```text
   2 work items + 2 actors + 1 fake tracker
   + 1 SQLite control DB + 1 real Git repository

   claim concurrently
     -> allocate separate worktrees
     -> crash or suspend one process at each boundary
     -> expire its lease and quarantine its workspace
     -> start a replacement in a new workspace
     -> let a suspended stale writer resume and mutate the old workspace
     -> finish both valid implementations
     -> review with different ActorIDs
     -> serialize integration
   ```

11. Inject at least: crash after lease, crash after workspace creation, tracker
    outage, stale writer resume, manual branch movement, simultaneous
    integration, and merge conflict.

Exit criteria:

- Two different work items execute concurrently in separate worktrees.
- Two contenders for one item produce at most one valid execution lease.
- An expired workspace is never transferred; its replacement is a distinct
  path and branch.
- A stale writer can continue changing its quarantined POSIX directory but
  cannot hand off, review, accept, or integrate through Spec Kitty.
- Recovery never clears a human-reassigned fake tracker owner.
- Actor-level review independence is enforced.
- Simultaneous integration is serialized and a conflict preserves both lines.
- Repeating recoverable operations is idempotent.
- `go test -race ./...` passes.

Failure to satisfy these criteria pauses broader architecture. The result is
used to revise authority, persistence, and workspace ADRs before Phase 2.

### Phase 2: Beads default tracker

Objective: replace the fake tracker with a production-quality Beads adapter.

Work packages:

1. Public Go API evaluation and pinned dependency.
2. Beads initialization/discovery without unsolicited agent-file mutation.
3. Catalog, graph, discussion, assignment, and acceptance adapters.
4. Stable WorkID-to-Beads mapping and namespaced metadata.
5. Provider capability and health detection.
6. Embedded/safe-multi-writer mode selection and worktree discovery.
7. Error translation, schema/version skew handling, and recovery guidance.
8. Beads conformance suite using temporary databases.
9. High-contention create, ready, claim, update, and comment tests.
10. Backup/export diagnostic and no-network operation test.

Exit criteria:

- Two concurrent processes cannot acquire authoritative Spec Kitty execution
  leases for the same work.
- Concurrent work creation produces no ID collisions.
- All worktrees observe the same ticket state.
- Beads failure never corrupts mission or lane state.
- Version/schema mismatch fails before mutation with a recovery path.
- No human-formatted CLI output is parsed.

### Phase 3: Software-development mission method

Objective: preserve the larger Spec Kitty methodology while eliminating the
canonical Markdown task list.

Work packages:

1. Specify step contracts, artifact service, and approval gate.
2. Plan step contracts, architecture/risk outputs, and approval gate.
3. Decompose structured work graph into Beads.
4. Dependency-cycle, missing-coverage, overreach, and work-quality validation.
5. Generated `WorkRevision`, immutable snapshot, digest, drift detection, and
   `continue | supersede | invalidate | fork` reconciliation.
6. Action-scoped doctrine/profile context and revision capture.
7. `next` query/action/decision/blocked/terminal protocol.
8. Derived `tasks.md` compatibility projection.

Exit criteria:

- A mission progresses from intent to finalized Beads work without a canonical
  task Markdown file.
- A fresh agent can understand each work item without private transcript
  context.
- Modifying Beads after finalization creates a drift result, not a silent run
  contract change.
- Reconciliation produces a new graph generation and an explicit disposition
  for every affected active or accepted work item.
- Mission steps remain authoritative independently of Beads statuses.

### Phase 4: Production mission concurrency

Objective: integrate the proven Phase 1 authority model with real Beads work and
the software-development mission, then meet or exceed current Spec Kitty
multi-agent execution.

Work packages:

1. Git repository identity, common-root discovery, and safe command runner.
2. Harden proven run leases, control-plane fencing, heartbeats, expiry, and
   stale recovery under mission transitions.
3. Lane allocation, generation binding, quarantine, and lifecycle policy.
4. Worktree create/status/quarantine/reconcile/archive/cleanup operations.
5. Branch ownership and base/head drift detection.
6. Run capsule with scoped context, tools, governance, and workspace.
7. Progress and durable handoff records.
8. Failure injection at every claim/lane/worktree saga boundary.
9. Two-agent, same-project concurrency suite.

Exit criteria:

- Two agents edit different work packages concurrently with no shared mutable
  checkout.
- Same-actor concurrent processes remain distinguishable.
- A killed process is recoverable without granting two valid fencing tokens.
- Every replacement lease receives a new worktree; quarantined work cannot enter
  review or integration without explicit reconciliation.
- Dirty or manually modified worktrees are never deleted.
- The primary user checkout remains untouched.
- Tracker outage during start yields a recoverable operation rather than lost
  or duplicated work.

### Phase 5: Implement/review/integration loop

Objective: complete Spec Kitty's differentiated execution method.

Work packages:

1. Dependency-aware implementer selection and run creation.
2. Handoff contract and evidence capture.
3. Independent reviewer runs with a different ActorID; Phase 1 admits no
   same-actor exception.
4. Structured approve/reject verdicts and rejection cycles.
5. Criterion evidence and mission-wide coverage projection.
6. Accept preflight and final readiness gate.
7. Deterministic serialized integration queue.
8. Merge execution, conflict evidence, and repair work creation.
9. Post-merge mission review and retrospective.

Exit criteria:

- An implementation ActorID cannot approve its own work merely by starting a
  different process.
- Rejection retains evidence and returns work through a defined new attempt.
- Accepted independent lanes integrate deterministically.
- Merge conflicts preserve all branches and become explicit repair work.
- Mission acceptance proves requirement and criterion coverage across work
  packages.

### Phase 6: External surfaces and migration

Objective: connect the new core without creating alternate authorities.

Work packages:

1. Versioned Orchestrator API with idempotent commands and resumable events.
2. MCP tool adapter over the same use cases.
3. Team Kitty outbox consumer, offline queue, diagnostics, and conflict policy.
4. Python project dry-run importer and migration receipt.
5. Compatibility commands and derived artifacts.
6. Tracker mirror framework and one proving adapter or fake contract.
7. Upgrade, backup, and rollback operations.

Exit criteria:

- CLI, API, and MCP produce equivalent application outcomes.
- Team Kitty can be unavailable without blocking an ordinary local mission.
- No integration imports control-store implementations.
- A representative Python mission migrates with verified work/dependency/state
  counts and remains recoverable.

### Phase 7: Hardening and release

Objective: prove the system under realistic long-running and adversarial use.

Work packages:

1. Long-duration multi-agent soak tests.
2. State-machine model tests and fuzzing.
3. Filesystem, path traversal, symlink, and unsafe Git-operation review.
4. Crash/restart matrix across all mutating commands.
5. Performance budgets for `next`, readiness, status, and worktree operations.
6. Upgrade/migration compatibility matrix.
7. Operator diagnostics and recovery runbooks.
8. Documentation, examples, and release packaging.

Exit criteria:

- Concurrency, crash, migration, and security suites pass on supported
  platforms.
- No known path silently loses work or grants ambiguous authority.
- Operators can diagnose every durable blocked state without editing databases.
- Release artifacts do not require Python.

## 18. Validation strategy

### 18.1 Test layers

| Layer | Purpose |
|---|---|
| Domain unit tests | State transitions, invariants, validation |
| Port contract tests | Every tracker/store/VCS implementation behaves consistently |
| Adapter integration tests | Real Beads temp DBs and real temporary Git repos |
| Application scenario tests | Multi-capability use cases and compensations |
| Differential tests | Semantic comparison with Python host |
| Contention tests | Multi-process claims, leases, updates, and merge queue |
| Fault-injection tests | Crash after each durable saga stage |
| Fuzz/model tests | Invalid transitions, graph cycles, malformed IDs and paths |
| Soak tests | Long-running parallel missions and stale process recovery |

### 18.2 Mandatory concurrency scenarios

1. Two agents claim different ready work items simultaneously.
2. Two agents race for the same work item; only one gets a valid run lease.
3. The same durable actor runs two different processes on different work.
4. One process dies after tracker assignment but before worktree creation.
5. One process dies after worktree creation but before workspace binding.
6. A stale process resumes arbitrary POSIX writes in its quarantined workspace
   while a replacement works in a distinct workspace.
7. A stale process tries to hand off or integrate using an old fencing token.
8. A human reassigns a ticket before an old saga compensation runs.
9. Two accepted lanes enter integration simultaneously.
10. A merge conflict occurs after other independent work was integrated.
11. A tracker update creates a new graph generation while an older revision is
    executing; each affected work item receives an explicit disposition.
12. All agents operate from separate worktrees while sharing control and Beads
    state.
13. A remote client uses the Orchestrator API while being unable to open SQLite
    or directly mount the host worktree.

### 18.3 Safety properties

- At most one valid implementation lease per work revision.
- A workspace is owned by exactly one lease generation and is never transferred
  to a replacement.
- A quarantined workspace cannot produce an accepted handoff without explicit
  reconciliation.
- At most one integration mutation at a time per target branch.
- No cleanup deletes an unowned or dirty workspace.
- No provider event directly mutates Git.
- No mission transition depends on eventual projection delivery.
- Every non-idempotent external effect has a durable operation record.
- A compensation cannot overwrite external state that no longer matches its
  recorded expected revision/value.
- Only one machine/filesystem authority opens a control store.
- Secrets never enter ticket descriptions, comments, events, diagnostics, or
  migration receipts.

## 19. Observability and operations

`spec-kitty doctor` should report facts, not mutate by default:

- project/repository identity;
- control-store schema and health;
- Beads location, mode, compatibility, and connectivity;
- active and stale leases;
- lane/worktree/branch consistency;
- pending sagas and compensations;
- outbox backlog and integration health;
- work-revision drift;
- merge queue state;
- migration and upgrade status.

Repairs are separate explicit commands with dry-run output. Each repair records
who requested it, what invariants were checked, and exactly what changed.

Structured logs include correlation, mission, work, run, lane, actor, and
process IDs. Human output stays concise; JSON output is complete and stable.

## 20. Principal risks and mitigations

### Beads semantics leak into the core

Risk: mission code begins to depend on Beads statuses, IDs, Molecules, or storage
details.

Mitigation: narrow ports, provider-neutral IDs, adapter conformance tests, import
guards, and an always-available fake tracker implementation.

### Two authorities disagree

Risk: Beads assignment says one thing while Spec Kitty execution state says
another.

Mitigation: explicit authority matrix, saga stage records, drift diagnostics,
and repair commands. Execution lease wins for mutation authority; tracker state
is reconciled visibly.

### Beads upgrade or schema skew

Risk: a fast-moving dependency migrates data incompatibly or a user's `bd`
binary disagrees with the embedded library.

Mitigation: pin a version, use published APIs only, preflight schema
compatibility, document an upgrade coordinator, back up before migrations, and
test adjacent supported versions.

### Modularity becomes plugin soup

Risk: excessive interfaces, tiny packages, runtime registration, and generic
payloads make behavior harder to follow.

Mitigation: vertical capabilities, interfaces defined by consumers, static
composition, typed commands, and a rule that a new capability must own a real
policy boundary.

### Mission methodology is weakened into tickets

Risk: users lose specify/plan/governance/accept/review discipline because the
ticket board looks sufficient.

Mitigation: mission engine remains authoritative; mission gates query work but
cannot be bypassed by closing issues.

### Checkbox work returns in another form

Risk: agents spend time toggling acceptance fields or duplicating status across
criteria, tickets, and mission artifacts.

Mitigation: criteria are definitions; evidence and verdicts derive completion.
Human-only checks are explicit exceptions.

### Cross-store crash windows

Risk: SQLite, Beads, Git, and filesystem operations partially succeed.

Mitigation: durable saga stages, idempotency keys, compare-before-compensate,
control-plane fencing, workspace-per-lease isolation, fault injection, and
honest `decision_required` recovery when automation cannot prove safety.

### Database fencing is mistaken for filesystem fencing

Risk: a stale but living agent continues ordinary file writes after its lease
expires.

Mitigation: never transfer its workspace. Quarantine the old workspace, give
the replacement a new branch/worktree, reject old-token handoff and integration,
and reconcile output only after quiescence is established.

### SQLite is treated as a distributed database

Risk: multiple machines or shared filesystems open one control database and
invalidate local locking assumptions.

Mitigation: one-machine/filesystem authority is a release invariant. Remote
clients use the Orchestrator API and host-owned workspaces.

### Compatibility dominates the new design

Risk: reproducing legacy artifacts and commands recreates Python structure.

Mitigation: compatibility is an outer adapter and projection layer with a
sunset policy. The new capability model never imports it.

## 21. Foundational ADR queue

After Phase 0 behavioral characterization, the project should ratify and record
these decisions before their corresponding implementation phase:

1. Beads as the default tracker, behind provider-neutral work ports.
2. Mission authority versus tracker authority.
3. Stable IDs and external mapping strategy.
4. SQLite control state anchored to Git common repository identity.
5. Run leases, control-plane fencing, expiry, and workspace quarantine policy.
6. WorkRevision generations, reconciliation dispositions, and drift behavior.
7. Lease-scoped worktree ownership, replacement, reconciliation, and cleanup.
8. Actor-level review independence and rejection-cycle semantics; evaluate any
   future exception only as a separately ratified feature.
9. Serialized integration queue and conflict repair.
10. Static composition and external plugin boundary.
11. Versioned command/query/event schemas.
12. Team Kitty and Orchestrator API authority boundaries.
13. Python migration and compatibility sunset policy.
14. Beads version, schema-skew, backup, and multi-writer policy.
15. Attribution-safe external compensation.
16. Single-machine SQLite authority and remote-agent API boundary.

## 22. Decisions still requiring focused validation

These are bounded technical decisions, not unresolved product direction:

- Confirm the exact Beads public API surface needed for criteria metadata,
  comments, events, worktree redirects, and backup operations.
- Measure SQLite WAL behavior under the intended number of local agent
  processes and supported local filesystems; network filesystems remain out of
  scope.
- Choose lease and heartbeat defaults based on real agent runtimes.
- Decide whether review uses a fresh read-only worktree, a detached checkout, or
  a separately owned review workspace for each review mode.
- Define how a host proves a quarantined workspace writer is quiescent before
  reconciliation snapshots any of its output.
- Define the supported Python migration floor and which historical formats are
  intentionally unsupported.
- Select the first non-Beads conformance adapter: fake-only, GitHub, or a local
  in-memory reference.

None of these decisions should reopen the authority boundaries in this plan.

## 23. Release-level definition of done

The Go implementation is ready to replace the Python host for the initial
software-development path when:

- a real project can initialize locally without Python;
- specify and plan produce governed versioned artifacts;
- decompose creates a validated Beads work graph;
- two or more agents concurrently implement independent work in isolated
  worktrees;
- claims, crashes, stale processes, and retries cannot create two valid
  execution authorities for the same work;
- an expired run's workspace is quarantined and never transferred to its
  replacement;
- a zombie writer cannot submit or integrate output through its expired
  authority even if it can still write its old directory;
- independent reviewers with different ActorIDs approve or reject with durable
  evidence; Phase 1 has no same-actor waiver;
- work-graph edits create a new generation with explicit disposition for
  affected work;
- accepted work integrates deterministically and conflicts create recoverable
  repair work;
- final acceptance verifies mission-wide requirement coverage;
- Team Kitty and Orchestrator API can observe and command the workflow without
  becoming alternate authorities;
- a representative existing mission migrates with semantic fidelity;
- all critical behavior has unit, integration, contention, and recovery tests;
- no canonical Markdown task checklist remains.

## Appendix A: Initial review brief for Mini (completed)

Mini completed this review on 2026-08-23. The prompt is retained for design
history; accepted findings and changes are recorded in
`docs/MINI_REVIEW_RESOLUTION.md`.

Please review this plan as an independent technical critic. The goal is not to
endorse the direction; it is to find where it will fail, accrete accidental
complexity, or lose a hard-won Spec Kitty behavior.

The intended decisions are:

1. Preserve Spec Kitty's mission methodology and lane/worktree concurrency.
2. Use Beads as the default ticket substrate rather than building a tracker.
3. Keep Beads replaceable through narrow capabilities.
4. Separate durable actor identity, provider identity, and execution process.
5. Replace canonical `tasks.md` with a tracker-backed work graph and
   evidence-derived acceptance.
6. Keep Team Kitty and the Orchestrator API outside the authority core.

Please concentrate on:

- Any place Beads has accidentally become mission or execution authority.
- Any race, fencing, crash-recovery, worktree, or merge invariant that is
  incomplete.
- Whether the proposed source-of-truth boundaries can actually be operated
  without confusing users or agents.
- Whether the SQLite + Beads + Git saga design is justified or needlessly
  complex.
- Whether `WorkRevision` finalization preserves user editability while keeping
  running work stable.
- Whether the acceptance model truly eliminates checkbox labor.
- Any part of the Python behavior likely to be lost by the migration strategy.
- Interfaces that are too broad, too narrow, or defined at the wrong boundary.
- Features that should be deferred to protect the first vertical slice.
- The smallest executable slice that would disprove the architecture quickly.

For each finding, please provide:

```text
Severity: blocker | high | medium | low
Location: section or decision
Finding: what is wrong or underspecified
Failure scenario: how it breaks in practice
Recommendation: the smallest corrective change
```

Please also answer three summary questions:

1. Does this preserve what Spec Kitty does better than ThickTicket and Beads?
2. Is Beads correctly bounded as a default substrate rather than a hidden
   kernel dependency?
3. What should be changed before the first line of implementation is written?

## Appendix B: Primary references

- Beads repository and product surface:
  <https://github.com/gastownhall/beads>
- Beads public Go consumer API:
  <https://raw.githubusercontent.com/gastownhall/beads/main/beads.go>
- Beads multi-agent coordination:
  <https://github.com/gastownhall/beads/blob/main/docs/multi-agent/coordination.md>
- Beads architecture and issue fields:
  <https://github.com/gastownhall/beads/blob/main/docs/ARCHITECTURE.md>
- ThickTicket architectural clarifications:
  `/home/lynn/projects/thickticket/docs/ARCHITECTURAL_CLARIFICATIONS.md`
- ThickTicket frontier governance:
  `/home/lynn/projects/thickticket/docs/FRONTIER_GOVERNANCE.md`
