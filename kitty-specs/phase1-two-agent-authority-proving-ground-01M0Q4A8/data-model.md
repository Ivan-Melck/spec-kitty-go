# Phase 1 Data Model

**Mission**: `phase1-two-agent-authority-proving-ground-01M0Q4A8`

**Purpose**: Define the minimum persistent concepts required to falsify the
authority design. Names describe Go-native domain concepts, not Python storage
compatibility.

## Identity Rules

Each identity is a distinct Go type and a stable serialized string:

`ProjectID`, `RepositoryID`, `MissionID`, `WorkID`, `WorkRevisionID`, `LaneID`,
`ActorID`, `ProcessID`, `RunID`, `LeaseID`, `WorkspaceID`, `OperationID`,
`HandoffID`, `ReviewID`, and `IntegrationID`.

IDs are never inferred from paths, branch names, tracker keys, or process IDs.
Database foreign keys store the canonical ID. Human-readable provider keys and
paths are attributes.

## Aggregate Relationships

```text
Project
  └─ Repository
      ├─ WorkRevision ── WorkItem ── Criteria / Dependency
      │                    └─ Lane
      │                        └─ Run ── Lease ── Workspace
      │                                   └─ Handoff ── Review
      └─ (RepositoryID, TargetRef) ── IntegrationAttempt

Operation ── OperationStage / ExternalEffect
Actor ── Process ── Run / Review
```

Lane and Workspace are intentionally not parent/child equivalents. A lane is
durable accepted-candidate lineage. A workspace is one lease generation's
mutable checkout.

## Core Records

### Project and Repository

`Project` binds the control store to one project identity.

`Repository` contains:

- canonical common-directory identity;
- managed workspace root;
- primary checkout path used only for isolation assertions;
- default target ref;
- created and verified timestamps.

The repository binding is immutable. Rebinding requires a new control store.

### WorkRevision

An immutable executable-graph generation containing:

- monotonically increasing revision number within a mission;
- source/provider revisions;
- graph digest and criteria digest;
- immutable WorkItems, criteria, dependency edges, and lane assignments;
- finalized timestamp and author ActorID.

Lifecycle: `draft -> finalized`. A finalized revision never changes.

Graph validation occurs before finalization and after any lane-collapse
projection. A cyclic graph cannot become executable.

### Reconciliation

A reconciliation relates an old WorkRevision to a new one with one disposition:

- `continue`: old run may finish under its old contract; evidence records both
  digests and rationale;
- `supersede`: old run may preserve work but cannot create an acceptable new
  handoff or integration;
- `invalidate`: active old authority expires and its workspace quarantines;
- `fork`: continuing lines receive distinct WorkIDs and explicit edges.

The record is immutable and includes actor, time, rationale, old/new work IDs,
and old/new digests. No disposition rewrites already integrated history.

### Actor and Process

`Actor` is a durable human/agent identity. `Process` is one execution of an
actor and includes host-local process metadata and timestamps.

Reviewer independence defaults to:

```text
review.actor_id NOT IN handoff.contributor_actor_ids
```

Different ProcessIDs do not satisfy independence. A waiver, if ever allowed in
the proving ground, is an explicit policy record and visible audit event.

### Run

One implementation, review, or repair attempt. Phase 1 primarily exercises
implementation runs.

Fields include WorkID, WorkRevisionID, LaneID, ActorID, ProcessID, OperationID,
status, current lease generation, and timestamps.

Implementation status:

```text
starting -> active -> handed_off
    |         |
    +------> expired
    +------> blocked
```

Status alone grants no authority; the current unexpired lease and token do.

### Lease

Time-bounded control-plane authority for one run generation:

- monotonically increasing generation per WorkID/WorkRevision;
- opaque fencing token digest;
- issued, heartbeat, deadline, expired timestamps;
- status `active | expired | surrendered`.

Invariant: at most one active implementation lease exists for a WorkID and
WorkRevision. Expiry is a compare-and-swap over the observed generation.

### Workspace

A real Git worktree permanently bound to one LeaseID and generation:

- repository, workspace ID, owned branch, canonical path;
- pinned base and dependency-prepared head;
- observed Git registration and head;
- preparation stage and status;
- quarantine reason/time.

Lifecycle:

```text
reserved -> worktree_created -> base_verified
         -> dependencies_prepared -> ownership_verified -> ready
         -> quarantined
```

Any preparation state may become `blocked`. Only `ready` is usable. A workspace
can never be rebound to a replacement lease. Quarantine means Spec Kitty
rejects authority through it; it does not claim to prevent POSIX writes.

### Operation and OperationStage

An idempotent command/saga identified by `(kind, idempotency_key)` and linked to
its aggregate IDs. It stores result or typed failure once terminal.

Stages are append-only observations of durable boundaries. Re-entry reads the
operation, probes external fact where necessary, and advances from proven
state. A file, branch, provider value, or cached stage alone is insufficient.

### ExternalEffect

Tracker mutation evidence:

- provider and provider work key;
- operation marker;
- expected prior revision/value;
- applied revision/value;
- response status (`confirmed | ambiguous | unavailable`);
- compensation status and conflict evidence.

Compensation is legal only when the current provider revision, value, and
marker still demonstrate attribution to this operation.

### Handoff

Immutable acceptance input containing:

- exact candidate commit;
- WorkID and WorkRevisionID;
- LaneID;
- RunID, LeaseID, and lease generation;
- contributor ActorIDs;
- criteria and evidence digests;
- verified base/ancestry evidence;
- submitted timestamp.

Creation revalidates current authority. Later review reads this record and the
candidate commit, never the mutable workspace.

### Review and ReviewCriterion

One candidate-bound judgment with reviewer ActorID and ProcessID, verdict
(`approved | changes_requested`), criterion results, evidence, and timestamp.

The verdict and all criterion rows commit atomically. Approval makes the
candidate eligible for integration; it does not mark work done.

The initial review-slot policy permits one accepted terminal verdict per
handoff generation. Contending writers receive an explicit conflict.

### IntegrationAttempt

A recoverable Git mutation saga keyed for exclusion by `(RepositoryID,
TargetRef)` and containing:

- mission/work/handoff/candidate IDs;
- preflight target SHA and candidate SHA;
- mutation lease owner and deadline;
- current phase;
- intended/result SHAs;
- repair evidence on conflict;
- terminal result and timestamps.

Phases:

```text
queued -> authority_acquired -> preflighted -> workspace_ready
       -> target_moved -> recorded -> done
       -> conflict | blocked
```

Recovery probes the target ref and graph relationship before advancing from
`target_moved`; cached phase never overrules Git fact. `done` has a uniqueness
constraint per accepted work result.

## Relational Constraints

Initial SQLite schema must enforce at least:

- unique typed primary IDs;
- unique `(operation_kind, idempotency_key)`;
- one active implementation lease per `(work_id, work_revision_id)` using a
  partial unique index or equivalent transactional guard;
- unique workspace canonical path and owned branch within a repository;
- immutable Workspace-to-Lease binding;
- immutable candidate SHA on Handoff, Review, and IntegrationAttempt;
- unique active integration authority for `(repository_id, target_ref)`;
- one terminal done record for an integrated WorkID/WorkRevision result;
- foreign keys enabled on every connection;
- optimistic revision columns on mutable aggregates.

## Event and Diagnostic Records

Every accepted/refused command emits a structured event or durable operation
result with:

- schema version and event/result kind;
- correlation and OperationID;
- aggregate IDs;
- ActorID and ProcessID when known;
- expected and observed state;
- retry safety;
- human/machine recovery actions;
- timestamp from the injected clock.

An outbox may publish those committed events later, but Phase 1 has no external
consumer. It exists only if a proving scenario requires atomic event capture.

## Storage Boundaries

- SQLite is authoritative for execution, workspace binding, handoff, review,
  integration bookkeeping, idempotency, and audit records.
- Git is authoritative for commits, refs, ancestry, trees, and worktree facts.
- The fake tracker is authoritative only for its own work/assignment/revision
  state.
- Filesystem existence is evidence to inspect, never authority by itself.

The fake tracker uses a separately durable store so no single SQLite
transaction can make the cross-system saga falsely atomic.
