# Mission Specification: Production Mission Execution and Hubnot Composition

**Mission Branch**: None allocated — pre-admission candidate, not an active mission.
**Created**: 2026-10-13
**Status**: Candidate; entry-work authorized; program admission pending.
**Input**: Confirmed M3 delegation and authorization to prepare M3/M4 specifications
and recorded admission decisions; preserve independent gates and exact M2 evidence.

Template provenance: canonical `specify_cli.runtime.resolver.resolve_template`
resolved `software-dev/spec-template.md` at the `global_mission` tier; template
SHA-256 `004273f296fbfbe517389837a434c44157f7713a48ccdb304024f519820ffd51`.
This candidate follows its required sections; no previous mission was copied.
[Authorization](../decisions/2026-10-13-m3-m4-entry-authorization.json) is not admission.
The [program](../../PRODUCTION_RUNTIME_PROGRAM.md) is normative over this candidate.

## Outcome and primary falsification target

Compose real agents, the exact qualified Hubnot provider, lease-scoped workspaces,
independent review and real Git integration over adopted M2 consumer-owned ports.
Falsify the claim that one valid authority and recoverable evidence survive
when GapDB, Hubnot, Git, filesystems, processes, agents and humans disagree.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Concurrent real execution (Priority: P1)

An operator dispatches two independent finalized work items to two real agents
in distinct workspaces without exposing the primary checkout to agent writes.
**Independent Test**: a real composition-root campaign runs both items, then a
same-work race and expired-writer recovery on the same attributable fixture.

**Acceptance Scenarios**:
1. Given independent work and admissible leases, when two configured real agents
   execute concurrently, then distinct lease/workspace/run identities produce
   attributable handoffs and the primary checkout remains unchanged.
2. Given one work revision, when two processes race for authority, then exactly
   one acquires its lease and the other receives a typed refusal.
3. Given an expired writer, when it continues ordinary file writes in quarantine,
   then those bytes survive as quarantined evidence but stale handoff/review/
   integration authority is refused; replacement authority gets a fresh workspace.
4. Given dirty or unowned paths, when recovery runs, then no such workspace is
   deleted; a same-fixture owned admissible path control proves attribution works.
5. Given an unfinished blocking dependency, when dispatch evaluates its dependent,
   then it remains blocked; after admissible dependency completion the same
   dependent is dispatched without external tracker facts granting permission.
6. Given an admissible live lease, when its owner renews it, then the renewal is
   durable; stale owner/epoch/lease renewal refuses without extending authority.

### User Story 2 — Tracker disagreement cannot grant authority (Priority: P1)

An operator receives honest, bounded progress and recovery despite provider
unavailability, stale replies, dishonest markers or later human edits.
**Independent Test**: execute real admitted-provider effects and paired controlled
responses through the production tracker port; compare original and later edits.

**Acceptance Scenarios**:
1. Given stale/dishonest/unavailable/contradictory tracker responses, when the host
   observes progress, then it records drift or reconciliation, not execution,
   review or integration permission.
2. Given attributable provider effects, when a human reassigns before compensation,
   then mismatched revision, value or operation marker independently refuses
   compensation; an unchanged same-fixture effect can be compensated admissibly.

### User Story 3 — Independent review and serialized integration (Priority: P1)

An operator reviews immutable candidates and integrates only reviewed, fenced
work; disagreements preserve candidates and expose repair work.
**Independent Test**: one real handoff/review/integration journey plus same-actor,
competing-fence and merge-conflict variants through the production entry point.

**Acceptance Scenarios**:
1. Given an immutable candidate, when the same ActorID attempts review under a
   different ProcessID, then it is refused; an independent ActorID can review.
2. Given one repository/target fence, when two integrations race, then only one
   is authoritative and the other receives a typed conflict/stale refusal.
3. Given conflicting changes, when integration fails, then both candidates remain,
   repair becomes explicit typed work and no primary checkout edits are fabricated.
4. Given an independent rejection of an immutable candidate, when implementation
   continues, then a newly attributable candidate must be handed off and reviewed;
   the rejected candidate cannot gain approval by reusing mutable files or history.

### Edge Cases

Lost acknowledgments reconcile the original operation identity; restart invalidates
old epoch capsules; provider or process identity changes refuse; partial workspace
creation never grants a lease; ambiguous cleanup preserves unowned state. None of
these cases may be hidden behind a retry-to-green or tracker status update.

### Exact falsification census

| Case | Canonical adversarial observation | Requirement |
|---|---|---|
| F1 | Two processes race for the same work. | FR-002 |
| F2 | Two real agents execute independent work in distinct workspaces. | FR-003 |
| F3 | Same ActorID attempts review under another ProcessID. | FR-005 |
| F4 | Expired zombie continues ordinary writes in quarantine. | FR-004 |
| F5 | Tracker replies are stale/dishonest/unavailable/human-contradicted. | FR-006 |
| F6 | Human reassignment occurs between effect and compensation. | FR-007 |
| F7 | Revision, value and operation-marker comparisons independently gate compensation. | FR-007 |
| F8 | Simultaneous integration races for one repository/target fence. | FR-008 |
| F9 | A merge conflict preserves candidates and creates repair work. | FR-009 |

Dirty/unowned-workspace preservation (FR-010) and restart/idempotency (FR-011)
are additional exit witnesses, not replacements for any of these nine cases.
F4 corrects the outline's imprecise “writes fail closed”: ordinary quarantined
writes may continue; **authority** is fenced. Frozen product law is unchanged.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status | Delivery | No-op passable? |
|----|-------|------------|----------|--------|----------|-----------------|
| FR-001 | Real composition | Operator invokes admitted agent/Hubnot/Git/workspace ports at the production root rather than unavailable seams. | High | Open | [build] | no |
| FR-002 | Same-work race | Exactly one lease survives competing processes; the loser has a typed refusal. | High | Open | [build] | no |
| FR-003 | Independent concurrency | Two real agents complete independent work in distinct lease-scoped workspaces. | High | Open | [build] | no |
| FR-004 | Zombie recovery | Ordinary quarantined writes survive while expired authority is fenced and replacement gets a fresh workspace. | High | Open | [build] | no |
| FR-005 | Independent review | Immutable handoff review rejects same ActorID despite a different ProcessID; independent review succeeds. | High | Open | [build] | no |
| FR-006 | Tracker adversity | Stale, dishonest, unavailable and human-contradicted observations yield bounded typed reconciliation, never authority. | High | Open | [build] | no |
| FR-007 | Guarded compensation | Revision, value and operation marker each gate compare-before-compensate independently. | High | Open | [build] | no |
| FR-008 | Repository fence | Competing integration on one repository/target serializes with one valid authority. | High | Open | [build] | no |
| FR-009 | Conflict repair | Conflicts preserve candidates and mint explicit repair work, not success. | High | Open | [build] | no |
| FR-010 | Preservation | Recovery preserves dirty/unowned workspaces with an owned-path positive control. | High | Open | [build] | no |
| FR-011 | Restart/idempotency | Production recovery reconciles original operations and refuses pre-restart capsules without repeating ambiguous effects. | High | Open | [build] | no |
| FR-012 | Dependency dispatch | Blocked dependencies prevent dispatch; admissible completion permits the same dependent through production readiness. | High | Open | [build] | no |
| FR-013 | Lease renewal | An admissible owner renews durably; stale owner/epoch/lease cannot extend authority. | High | Open | [build] | no |
| FR-014 | Rejection cycle | Independent rejection requires a newly attributable candidate and fresh admissible review before integration. | High | Open | [build] | no |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Exact census | All nine canonical adversarial cases execute and terminate; zero selected proof skips or omitted cases. | Reliability | High | Open |
| NFR-002 | Bounded adapters | Every configured timeout, input/output byte bound, resource and retry limit is immutable before campaign execution; observed usage stays within those bounds or refuses. | Security | High | Open |
| NFR-003 | Authority isolation | Zero tracker/process/filesystem facts grant authority; zero primary checkout agent edits; zero unowned workspace deletions. | Security | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Exact predecessor | Consume only the recorded adopted M2 commit/tree/evidence; current unqualified changes cannot inherit that proof. | Governance | High | Open |
| C-002 | Frozen laws | No M2 wire/validator/canonicality/admission changes, SQLite/Python fallback, qualification imports or direct GapDB access in kernels. | Architecture | High | Open |
| C-003 | Exact provider | Hubnot source/binary/protocol/policy/signer and conformance evidence must be immutably bound before activation. | Supply chain | High | Open |
| C-004 | Scope | No neutral acceptance/public API, MCP, core release qualification or new infrastructure/credentials/services. | Program | High | Open |
| C-005 | Independent gates | WP review, acceptance, merge, mission review and retrospective remain separately attributable; technical delegation does not waive them. | Governance | High | Open |

### Key Entities

WorkRevision, ActorID, ProcessID, RuntimeEpochID, lease, workspace/path identity,
run capsule, immutable candidate/handoff, review outcome, repository/target fence,
attributed provider effect and typed recovery/repair work retain consumer-owned laws.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: All nine program adversarial cases pass at real production entries with exact source/binary/provider bindings and non-vacuous controls. — [build] · no-op passable: no
- **SC-002**: Two real independent agents yield two distinct workspaces and attributable candidates, with one valid authority per work revision and repository/target at every observation. — [build] · no-op passable: no
- **SC-003**: Every recorded durable blocked state exposes a typed next action; no missing observation is reported as success. — [build] · no-op passable: no
- **SC-004**: Production controls prove blocked-to-ready dependency dispatch, admissible-versus-stale lease renewal and rejection-to-new-candidate review, with no required path omitted. — [build] · no-op passable: no

## Admission, stop condition and rollback

Only M2 is a dependency. Before activation, record immutable predecessor mission,
commit/tree, accepted manifest/review verdict and all consumed provider/law bindings;
verify a schema-valid acyclic manifest and this candidate's scope. Outstanding
entry evidence is listed in [admission readiness](../../parity/ADMISSION_READINESS.md).
If evidence is missing/contradictory, a retained law would require amendment, or a
mandatory disproof fails, stop and retain the attempt. Roll back by removing the
unqualified composition root, preserving qualified domain services and evidence;
never enable another authority or erase candidates/workspaces to get green.
