# Orchestrator API 2.0 Contract

## Status and boundary

Proposed canonical application boundary. API 1.3 remains a compatibility projection for existing consumers; it is not the vocabulary from which new domain commands are derived.

The API is transport-neutral. An in-process Go caller, local CLI, Unix-socket service, Team Kitty, CI integration, dashboard, or optional MCP adapter consumes the same typed capabilities. Remote callers never open GapDB, mutate Git refs, allocate workspaces, mint leases, or manufacture acceptance evidence.

## Envelope laws

Every mutating request carries:

| Field | Rule |
|---|---|
| `command` and `version` | Closed typed command vocabulary |
| `operation_id` | Required idempotency identity |
| `request_digest` | Canonical digest verified by the host |
| `actor_id` and `process_id` | Explicit accountable subject and execution instance |
| scope identities | Mission/work/revision/run/candidate as required; never inferred from the working directory |
| expected revision/epoch/fence | Required where concurrent authority could change |
| governance and policy revisions | Required where they affect admissibility |
| deadline/cancellation handle | Resource bound only; never authority by itself |

Every response is either a typed result or a typed diagnostic containing a stable code, retry class, safe message, relevant current revision, and legal next actions. Human-formatted output is a client concern.

## Command families

The canonical vocabulary contains named legal operations rather than a generic transition primitive.

### Mission definition and executable graph

- create, inspect, amend, and explicitly cancel a mission;
- observe tracker work and finalize a `WorkRevision` generation;
- reconcile drift with `continue`, `supersede`, `invalidate`, or `fork`;
- record or answer a governed decision.

### Execution and workspace authority

- claim ready work;
- heartbeat, release, expire, or abandon an exact lease;
- create and bind a fresh lease-scoped workspace;
- report workspace facts and quarantine an old generation;
- record run observations and terminal implementation outcome.

### Candidate, review, and integration

- create an immutable candidate handoff from observed Git facts;
- begin review under an explicit independence policy;
- record candidate-bound review evidence and verdict;
- request acceptance evaluation;
- reserve, execute, reconcile, or abandon serialized integration.

### Qualification and evidence

- assemble the required acceptance claim set;
- propose, approve, or retire provider bindings;
- execute a qualification node as a native operation;
- submit typed evidence through a trusted host capability;
- grant or revoke an explicit scoped waiver;
- evaluate acceptance against the complete current claim set.

### Recovery and administration

- inspect health, storage revision, runtime epoch, and recovery status;
- request a checkpoint or side-effect-free playback;
- reconcile pending external effects and drift;
- export a terminal mission record.

There is no command equivalent to “set lane,” “force successful,” “write raw event,” “set lease owner,” or “accept arbitrary evidence.”

## Queries

At minimum:

- `GetMission`, `ListMissions`, and `GetMissionProgress`;
- `WatchMissionProgress(after_revision)` with resume and bounded backpressure behavior;
- `GetWorkRevision`, `ListReadyWork`, `GetRun`, and `GetWorkspace`;
- `GetCandidate`, `GetReview`, `GetAcceptance`, and `GetIntegration`;
- `GetOperation`, `WatchOperation`, and `ListOutstandingDecisions`;
- `GetQualificationGraph` and `GetEvidenceIndex`;
- `GetHealth`, `GetRuntimeEpoch`, and `ExplainDecision`.

Queries return the observed storage revision and projection freshness. They do not silently repair state.

## Native asynchronous operations

Commands that may outlive a request return a `NativeOperationID`. The operation is durable and queryable independently of the initiating connection.

```text
accepted -> working <-> input_required
          -> cancellation_requested
          -> completed | failed | cancelled
```

- Disconnecting a client does not cancel the operation.
- Cancellation is cooperative, can race with completion, and does not imply rollback.
- Required input references a durable `DecisionID` with typed choices and authorization policy.
- Terminal results are idempotently retrievable for a bounded operational retention period.
- Mission history and evidence outlive operation-result retention.

## Progress contract

Progress is a first-class query and event stream conforming to `progress.schema.json`. It reports graph position and blockers, not an author-maintained estimate. A watch resumes strictly after a supplied journal revision. If the requested history is outside watch retention, the server returns a typed reset diagnostic and a fresh snapshot revision.

## Compatibility with API 1.3

API 1.3 is retained behind an explicit projection table:

- representable reads and commands delegate to API 2.0;
- deprecated generic transitions are mapped only when the requested action has one unambiguous named API 2.0 equivalent;
- impossible, unsafe, or lossy requests fail with an upgrade diagnostic;
- no API 1.3 caller can bypass current authority, evidence, or review policy; and
- compatibility code cannot be imported by the domain or application kernel.

## Error classes

Required classes include invalid request, unsupported version, not found, revision conflict, idempotency conflict, stale epoch/fence, lease expired, independence violation, drift decision required, evidence inadmissible, provider unavailable, unsafe compensation, backpressure, degraded recovery, and internal failure.

Each class declares whether retry is safe with the same operation identity, safe only after a new observation or decision, or forbidden.

## Conformance proofs

- Every mutating application capability is reachable without MCP.
- Equivalent CLI and transport calls produce equivalent typed results and diagnostics.
- API clients cannot access GapDB or host mutation capabilities directly.
- Removing early authority checks or adapter dependency guards kills a mutant test.
- A slow consumer receives bounded backpressure or a resume reset, never unbounded memory growth.
- Duplicate requests cannot create duplicate workspaces, leases, external effects, or integration attempts.

