# Data Model: Production Orchestration Architecture

This is a conceptual contract for later implementation missions. It does not assert that these records are present in the current SQLite-qualified Phase 3 runtime.

## Authority ownership matrix

| State category | Single writable authority | Durable evidence | Allowed projections | Forbidden inference |
|---|---|---|---|---|
| Mission intent and lifecycle | Spec Kitty mission capability | Control journal + checkpoint | CLI, API, Team Kitty, optional MCP | Tracker status advances mission |
| Mutable work definition | Configured primary tracker, Beads by default | Tracker revision/observation | Pinned WorkRevision, human views | Assignment grants execution authority |
| Executable graph | Spec Kitty WorkRevision generation | Immutable revision + reconciliation events | Progress, scheduler, compatibility tasks | Later tracker edit mutates a running contract |
| Run and lease | Spec Kitty execution capability | Durable lease/epoch events | Run capsule, progress | Process identity or ticket claim equals authority |
| Mutable workspace | Host workspace capability | Lease binding + Git/workspace facts | Scoped path in a run capsule | A replacement inherits an expired workspace |
| Candidate | Spec Kitty handoff capability + Git commit fact | Handoff event + candidate digest/ref | Review context, progress | Live directory contents are a candidate |
| Review | Spec Kitty review capability | Candidate-bound verdict events | Tracker comment, progress, acceptance evidence | Same actor/different process is independent |
| Acceptance | Spec Kitty acceptance capability | Acceptance decision + evidence references | CLI/API result, tracker summary | Closed tickets or successful prose output imply acceptance |
| Integration | Spec Kitty integration capability + Git ref outcome | Integration phase events + observed refs | Progress, terminal work state | Tracker merge slot moves Git authority |
| Native operation | Spec Kitty application operation capability | Operation events and result | CLI follow, API, optional MCP Task | Transport session owns operation state |
| Mission progress | Spec Kitty projection capability | Rebuildable from journal/checkpoint/graph | List/Get/Watch clients | Markdown or Beads status is canonical progress |

## Core identity types

All identities are opaque, globally unique, bounded strings with explicit parsers. Display names, slugs, paths, and tracker identifiers never substitute for durable identity.

| Identity | Scope and role |
|---|---|
| `MissionID` | One durable mission lifecycle |
| `WorkRevisionID` | One immutable generation of the executable graph |
| `WorkID` | Stable work identity within and across reconciled generations when semantics remain continuous |
| `ActorID` | Durable accountable human or agent subject |
| `ProcessID` | One execution process; not a substitute for actor identity |
| `RuntimeEpochID` | One authority-owner lifetime after durable recovery |
| `OperationID` | Idempotency and replay identity for one application command |
| `RunID` | One implementation or review execution attempt |
| `LeaseID` | One time-bounded authority grant within one runtime epoch |
| `WorkspaceID` | One mutable checkout permanently bound to its lease generation |
| `HandoffID` | One immutable candidate submission |
| `ReviewID` | One candidate-bound independent review attempt |
| `IntegrationID` | One serialized integration attempt |
| `DecisionID` | One explicit human or governed decision point |
| `ClaimID` | One acceptance proposition |
| `EvidenceID` | One evidence envelope |
| `NativeOperationID` | One long-running application operation; may equal or reference `OperationID` by contract |
| `MCPTaskID` | Optional transport handle for one native operation |

## ControlEvent

An immutable accepted domain fact.

| Field | Rule |
|---|---|
| schema version | Required; unknown versions fail closed or invoke an explicit migration reducer |
| event identity | Unique and immutable |
| aggregate identity/type | Required and bounded |
| runtime epoch | Required for authority-affecting events |
| operation identity | Required for command-derived events |
| actor/process | Required when attributable action occurs |
| causation/correlation | Distinct optional chains; correlation is never authority |
| governance and policy revisions | Required when policy affects admissibility |
| occurred/recorded time | Host-generated UTC; time alone never orders authority |
| event kind and payload | Closed kind/version pair with strict schema |
| external references | Bounded IDs/digests, never unbounded logs or transcript bodies |
| storage revision | Assigned by the control substrate and used for global resume/watch position |

### Invariants

- Same `OperationID` and canonical request digest replays the prior outcome.
- Same `OperationID` and different digest conflicts before any effect.
- Event reduction is deterministic for one schema/reducer version.
- Playback consumes events and recorded observations; it never invokes side-effect adapters.
- External-effect intent and its later observation are separate facts.

## MissionCheckpoint

A versioned materialized projection used to bound restore work.

| Field | Rule |
|---|---|
| mission identity | Required |
| through storage revision | Inclusive journal boundary |
| reducer version | Required to decode projection semantics |
| executable generation | Required |
| serialized state | Bounded, checksummed, and versioned |
| source event digest | Binds checkpoint to its contributing journal prefix |
| created time | Informational UTC |

Checkpoints are accelerators, not independent authority. A checkpoint whose digest or reducer contract cannot be validated is ignored or rejected; the runtime never guesses around it.

## RuntimeEpoch

| Field | Rule |
|---|---|
| epoch identity | New durable identity for every recovered owner start |
| database identity | Must match the expected project control store |
| started revision/time | Required |
| predecessor | Optional reference to the recovered epoch |
| recovery decision | Required when a predecessor exists |

### Epoch transition

```text
closed/unavailable
    -> recovering
    -> checkpoint restored
    -> journal replayed
    -> new epoch committed
    -> external reconciliation
    -> ready

any validation failure -> degraded or closed, never silently ready
```

Predecessor leases are not current in the new epoch. Their workspace records survive for inspection and quarantine; replacement authority receives a new workspace.

## NativeOperation

A transport-neutral asynchronous request to drive one bounded application outcome.

| Field | Rule |
|---|---|
| operation identity and request digest | Required idempotency pair |
| command kind and version | Required typed operation |
| actor/process/policy/governance | Required according to command authority |
| mission/work/candidate scope | Explicit identifiers; never inferred from ambient directory |
| status | Closed state vocabulary below |
| progress revision | Monotonic within the operation |
| input requests | Typed outstanding decisions; no free-form hidden prompt authority |
| cancellation intent | Recorded separately from actual terminal effect |
| result or diagnostic | Required at terminal status |
| created/updated/retention | Host-generated and bounded |

### Native operation states

```text
accepted -> working -> input_required -> working
                    -> cancellation_requested
          -> completed
          -> failed
          -> cancelled
```

Rules:

- Cancellation is cooperative and can race with completion.
- `cancellation_requested` does not revoke a lease, undo an external effect, or prove rollback.
- An input response must reference an outstanding `DecisionID` and satisfy its policy.
- Operation retention may expire a query projection, but never erases mission events or evidence.

## MissionProgress

A bounded read model at one storage revision.

| Field group | Required content |
|---|---|
| identity | mission ID, display name, mission type, active WorkRevision generation |
| lifecycle | phase, overall status, acceptance/integration position |
| work summary | total and counts by canonical state |
| active authority | run/work/actor/process, durable authority expiry, workspace identity; no fencing secret |
| graph | ready set, critical path, blocked path |
| blockers/decisions | stable IDs, bounded reason, responsible authority, available actions |
| drift | tracker, Git, workspace, policy, or projection findings with severity |
| next actions | typed legal action descriptors, not suggested shell prose |
| observation | journal revision, last progress time/event, checkpoint staleness |

Percentage, if emitted, is derived and labeled with its weighting rule. It never replaces graph or blocker detail.

## AcceptanceClaim

| Field | Rule |
|---|---|
| claim identity | Stable within the acceptance-plan revision |
| capability | Stack-neutral canonical name |
| statement | Human-readable proposition |
| source | Organization, project, mission type, mission, or human addition |
| scope | Mission, work, candidate, artifact, environment, or external system |
| requiredness | Required, optional, or conditionally required with typed predicate |
| freshness policy | Exact candidate by default; any reuse rule names all stable inputs |
| allowed evidence kinds | Explicit provider capabilities and trust requirements |
| dependencies | Other claims that must complete first |

Required claims are the strict union across policy layers. A lower layer cannot silently remove a higher-layer requirement.

## QualificationBinding

| Field | Rule |
|---|---|
| claim identity | Required |
| provider identity/version/digest | Required and revision-pinned |
| binding source | Organization, project, mission, or explicit operator decision |
| execution policy | Argument array or typed remote request; bounded environment, network, timeout, output, and secret policy |
| evidence decoder | Versioned typed protocol; human-formatted output is never parsed as state |
| status | Proposed, approved, unavailable, or retired |

Detection can create `proposed`; only explicit approved configuration creates an active binding.

## EvidenceEnvelope

| Field | Rule |
|---|---|
| evidence and claim identities | Required |
| candidate and WorkRevision | Exact identities required |
| outcome | `pass`, `fail`, `error`, `skipped`, or `waived` |
| provider identity/revision | Required |
| runner and trust domain | Required |
| policy/governance revisions | Required when they affect execution/admissibility |
| started/finished | UTC, bounded duration |
| input digest | Required for reuse decisions |
| result/artifact/log digests | Bounded; payload bodies live in an artifact store |
| waiver | Signer, authority, reason, scope, expiry, and policy basis when outcome is `waived` |

An envelope is evidence, not an acceptance decision. The gate evaluates the complete required claim set and admissibility policy.

## MCPTaskProjection

| Field | Mapping |
|---|---|
| MCP task ID | Opaque transport ID referring to one native operation |
| status `working` | Native operation accepted/working/cancellation requested as appropriate; detail remains in progress resource |
| status `input_required` | Outstanding native operation input requests projected through MRTR task input |
| status `completed` | Native operation terminal success result |
| status `failed` | JSON-RPC execution failure; domain negative outcomes may still be successful tool results with typed diagnostics |
| status `cancelled` | Native operation confirmed cooperative cancellation |
| TTL | Transport retrieval lifetime only |
| poll interval | Advisory transport pacing |

The projection never maps task loss, TTL, or cancel acknowledgement directly to mission deletion, lease revocation, or compensation.

## ProgramMission

| Field | Rule |
|---|---|
| program and mission identity | Stable and unique |
| title/outcome | One coherent delivery boundary |
| primary falsification target | Exactly one required |
| dependencies | At most three named stable contract/evidence dependencies |
| entry evidence | Explicit artifacts or merged commits required to start |
| exit evidence | Executable proof and documentation required to complete |
| non-goals | Explicit boundaries |
| parallel group | Optional scheduling hint, never authority |
| risk/rollback | Required for production mutations |
| status | Proposed, admitted, active, blocked, complete, superseded, or cancelled |

The program graph must be acyclic. Mission admission is a decision based on current evidence; proposed mission count may change through explicit reconciliation.

## Recovery walkthrough

1. GapDB validates database identity, restores its latest valid snapshot, and replays complete WAL frames.
2. Spec Kitty loads the latest valid mission checkpoint and reduces later control events through the recovered storage revision.
3. The authority runtime commits a new `RuntimeEpochID` with the recovered predecessor and reducer versions.
4. All predecessor leases become non-current; their workspaces remain old-generation records and are quarantined from replacement.
5. Spec Kitty inspects Git refs/workspaces, Beads observations, pending outbox effects, and artifact availability through typed adapters.
6. Reconciliation records continue, supersede, invalidate, fork, retry, compensate, or decision-required outcomes as applicable.
7. Progress publishes the recovered revision, drift, blockers, and legal next actions.
8. Side-effect-free playback can independently reproduce the mission projection and explanation from checkpoint plus events.
