# Mission Specification: Software Development Mission Method

**Mission Branch**: `feat/software-development-mission-method`  
**Created**: 2026-08-27  
**Status**: Draft  
**Input**: Preserve Spec Kitty's software-development mission method while replacing the canonical Markdown task ledger with tracker-backed structured work and immutable executable graph generations.

## Mission Intent

Phase 2 proved that Beads can own the human-visible work catalog without gaining
execution, review, workspace, or integration authority. Phase 3 now restores
the larger Spec Kitty method above that adapter: a human or agent turns intent
into an approved specification, an approved plan, structured tracker work, and
an immutable executable `WorkRevision`.

The smallest proving scenario contains two dependent work items. A mission
author specifies and plans them, decomposes them into the configured tracker,
finalizes generation 1, edits the tracker afterward, observes drift, and creates
generation 2 through an explicit reconciliation decision. At no point may a
tracker status or a Markdown checkbox advance the mission or silently change a
running contract.

This mission ends at a finalized and reconciled executable graph plus the typed
`next` control surface. Production run allocation, worktree lifecycle,
implementation/review dispatch, acceptance, merge, external APIs, and migration
remain later phases.

## Domain Language

- **Mission**: A governed lifecycle with authoritative steps, approvals,
  decisions, blockers, and terminal outcomes. It is not inferred from tracker
  statuses.
- **Mission step**: A versioned contract defining required inputs, durable
  outputs, approval conditions, and legal transitions.
- **Artifact**: A typed, durable mission output with identity, revision,
  provenance, and approval state. A file may render an artifact but is not
  automatically its authority.
- **Work definition**: Mutable planning content held by the configured primary
  tracker before finalization.
- **WorkRevision**: An immutable, generation-numbered snapshot of the executable
  work graph, its source revision, criteria, dependencies, write scopes,
  provider revisions, and digest.
- **Drift**: A difference between the finalized graph and a later tracker
  observation. Drift is information, never an automatic mission transition.
- **Reconciliation**: A governed decision that creates a successor generation
  and assigns an explicit disposition to every affected active or accepted work
  item.
- **Disposition**: One of `continue`, `supersede`, `invalidate`, or `fork`.
- **Derived projection**: A read-only human or compatibility rendering of
  authoritative state, including `tasks.md`.
- **Next outcome**: One typed response from `query`, `action`, `work_ready`,
  `review_ready`, `decision_required`, `blocked`, or `terminal`.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Progress through governed specification and planning (Priority: P1)

As a mission author, I can turn an initial intent into approved specification
and plan artifacts whose required content, provenance, and approvals determine
whether decomposition may begin.

**Why this priority**: The mission method is the product boundary that prevents
the tracker from becoming an accidental workflow engine.

**Independent Test**: Create a mission, attempt every step with absent,
incomplete, rejected, stale, and approved predecessor artifacts, and verify
that only the approved path reaches decomposition.

**Acceptance Scenarios**:

1. **Given** a new software-development mission, **When** its intent is
   accepted, **Then** the next legal action is specification and the mission
   records the governing step-contract revision.
2. **Given** a specification missing required acceptance framing, **When** the
   author requests approval, **Then** approval fails with typed findings and
   planning remains unavailable.
3. **Given** an approved specification, **When** planning completes with
   architecture, risk, compatibility, and validation outputs, **Then** an
   independent approval permits decomposition.
4. **Given** an approved artifact whose content later changes, **When** a
   downstream transition is requested, **Then** the stale approval is rejected
   rather than silently applying to the new content.

---

### User Story 2 - Decompose into tracker-backed work and finalize it (Priority: P1)

As a mission planner, I can create a dependency-aware work graph in the
configured tracker, validate its quality and coverage, and finalize an immutable
executable generation without maintaining a second task checklist.

**Why this priority**: This is the direct replacement for the inherited
Markdown task ledger and the first useful end-to-end Phase 3 result.

**Independent Test**: Decompose a two-item mission into the durable fake tracker
and Beads, exercise invalid graphs and criteria, then finalize and reproduce the
same graph digest from either provider.

**Acceptance Scenarios**:

1. **Given** an approved plan, **When** the planner decomposes it, **Then** the
   tracker receives stable work identities, descriptions, structured criteria,
   dependencies, write scopes, and plan provenance.
2. **Given** missing requirement coverage, a dependency cycle, ambiguous
   ownership, over-broad work, or an item that lacks sufficient standalone
   context, **When** finalization is requested, **Then** it fails with findings
   that identify the affected work and violated rule.
3. **Given** a valid graph, **When** finalization succeeds, **Then** generation 1
   pins the planning source, tracker revisions, normalized graph, lane-collapse
   result, dependency-satisfaction rule, and deterministic digest.
4. **Given** the same semantic graph observed twice with different provider
   ordering, **When** each observation is normalized, **Then** both produce the
   same executable digest.
5. **Given** a finalized graph, **When** a compatibility `tasks.md` is rendered,
   **Then** it can be deleted and regenerated without changing any mission,
   work, approval, or execution fact.

---

### User Story 3 - Detect and reconcile post-finalization drift (Priority: P1)

As a mission authority, I can see when tracker work changes after finalization
and explicitly decide how a successor graph generation relates to work already
running, accepted, or integrated.

**Why this priority**: Immutability without defined contact with changing
reality merely moves ambiguity elsewhere. Reconciliation makes graph evolution
safe and explainable.

**Independent Test**: Finalize generation 1, mutate descriptions, criteria,
dependencies, identities, and write scopes, then prove that every affected work
item requires exactly one legal disposition before generation 2 can become
current.

**Acceptance Scenarios**:

1. **Given** generation 1 and an unchanged tracker, **When** drift is checked,
   **Then** the result is clean and no new generation is created.
2. **Given** a tracker edit after finalization, **When** drift is checked,
   **Then** the old generation remains immutable and the result identifies the
   semantic changes and affected work.
3. **Given** unresolved affected work, **When** reconciliation is submitted,
   **Then** it fails without changing the current generation.
4. **Given** complete authorized dispositions, **When** reconciliation
   succeeds, **Then** it creates a successor generation naming its predecessor,
   approver, old and new digests, and every affected-work decision.
5. **Given** superseded or invalidated active work, **When** it later attempts
   handoff or acceptance, **Then** the old generation is ineligible; invalidated
   work also emits the authority action required for later workspace
   quarantine.
6. **Given** integrated history affected by a new requirement, **When** the
   graph is reconciled, **Then** existing history is retained and new repair or
   replacement work is created rather than rewriting the integrated result.

---

### User Story 4 - Use one typed next-action loop (Priority: P1)

As an interactive agent or human-operated harness, I can ask what happens next
and receive one typed legal outcome with the minimum action-scoped governance
needed to continue.

**Why this priority**: The mission engine needs a simple product surface that
does not expose internal storage or make agents reconstruct workflow from
files.

**Independent Test**: Query every mission state and transition guard, including
missing input, required approval, available work, pending decision, durable
blocker, and terminal completion; verify an exact typed outcome and stable
identity references.

**Acceptance Scenarios**:

1. **Given** a mission in any valid state, **When** `next` is queried repeatedly
   without intervening change, **Then** it returns an equivalent result and
   performs no mutation.
2. **Given** required human input, **When** `next` is queried, **Then** it returns
   `query` or `decision_required` with a stable decision identity rather than
   guessing an answer.
3. **Given** a durable blocker, **When** `next` is queried, **Then** it returns
   `blocked` with bounded recovery guidance and does not skip the gate.
4. **Given** finalized ready work, **When** `next` is queried, **Then** it returns
   `work_ready` referencing the exact `WorkRevision`; it does not infer
   readiness from tracker status.
5. **Given** an action outcome, **When** its run capsule is inspected, **Then**
   the selected actor profile and doctrine revisions explain which governance
   authorized the action.

---

### User Story 5 - Preserve compatibility without dual authority (Priority: P2)

As an existing Spec Kitty user, I can inspect familiar mission summaries and a
generated task document while learning the new tracker-backed workflow, without
being asked to update both representations.

**Why this priority**: Compatibility should reduce migration cost without
preserving accidental Markdown control semantics.

**Independent Test**: Generate, corrupt, delete, and regenerate every declared
projection; verify that canonical mission and work queries remain unchanged and
that commands refuse to parse projection edits as authority.

**Acceptance Scenarios**:

1. **Given** finalized tracker work, **When** `tasks.md` is generated, **Then** it
   clearly identifies itself as derived and presents work, dependencies,
   criteria, generation, and drift status.
2. **Given** edited checkboxes or prose in `tasks.md`, **When** mission state is
   queried or advanced, **Then** the edits have no authoritative effect.
3. **Given** no Beads installation, **When** the durable fake tracker is
   selected, **Then** the complete mission-method conformance scenario remains
   runnable.

### Edge Cases

- Specification or plan content changes after approval but before decomposition.
- Two planners attempt to finalize the same mission graph concurrently.
- Provider observations contain identical timestamps but different content.
- Provider ordering changes without a semantic graph change.
- A work item is deleted, duplicated, rebound to another project, or replaced
  with an unknown metadata schema after finalization.
- A dependency edit creates a cycle only after overlapping write scopes are
  collapsed into lanes.
- One edit changes criteria for already accepted but not integrated work.
- One edit makes currently running work unnecessary.
- A `continue` decision is requested for a materially changed executable
  contract.
- A fork would reuse an existing WorkID or leave ambiguous dependency ancestry.
- Reconciliation crashes before or after publishing the new current generation.
- Doctrine or profile content changes between a query and the requested action.
- A renderer encounters a missing provider while a previously finalized
  WorkRevision remains available.
- A user tries to advance a step by editing a generated document or changing a
  tracker status directly.
- Multiple `next` callers race while one caller resolves a decision.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Versioned mission definition | As a mission author, I use an explicitly selected, versioned software-development mission definition whose absence or incompatibility fails loudly. | High | Open |
| FR-002 | Typed step contracts | As a mission engine, each step declares required inputs, outputs, approval conditions, legal predecessors, and legal successors. | High | Open |
| FR-003 | Authoritative mission state | As an operator, mission advancement is decided only by mission state and approved evidence, never by tracker status or projected files. | High | Open |
| FR-004 | Typed artifact service | As a mission author, specification and plan outputs have stable identity, content revision, provenance, validation state, and approval state. | High | Open |
| FR-005 | Approval freshness | As an approver, my verdict is bound to the exact artifact revision and becomes stale when that content changes. | High | Open |
| FR-006 | Specify gate | As a planner, I may begin planning only after intent, scope, requirements, acceptance framing, assumptions, and boundaries pass specification validation and approval. | High | Open |
| FR-007 | Plan gate | As a decomposer, I may create executable work only after architecture, data flow, risk, compatibility, migration, and validation strategy pass plan validation and approval. | High | Open |
| FR-008 | Tracker-backed decomposition | As a mission planner, I decompose the approved plan into stable tracker work with descriptions, structured criteria, dependencies, write scopes, and provenance. | High | Open |
| FR-009 | Provider-neutral mission method | As an operator, the same decomposition and finalization laws run against the durable fake tracker and any compatible configured primary tracker. | High | Open |
| FR-010 | Work-quality validation | As a reviewer, I receive typed findings for missing standalone context, untestable criteria, overreach, ambiguous ownership, and invalid work boundaries. | High | Open |
| FR-011 | Coverage validation | As a mission authority, every required specification outcome maps to one or more work items and every work item traces to approved plan scope. | High | Open |
| FR-012 | Graph validation | As a scheduler, unknown dependencies, self-dependencies, work cycles, and cycles introduced by write-scope lane collapse fail before finalization. | High | Open |
| FR-013 | WorkRevision finalization | As a mission authority, I create one immutable, generation-numbered executable graph only after all finalization gates pass. | High | Open |
| FR-014 | Complete revision identity | As a future run, I can identify the mission, generation, predecessor, planning source, provider observations, normalized graph, criteria, write scopes, dependency policy, lane graph, and digest that define my contract. | High | Open |
| FR-015 | Deterministic normalization | As an auditor, semantically equivalent provider observations produce the same graph digest regardless of ordering or presentation differences. | High | Open |
| FR-016 | Drift detection | As a mission author, later tracker changes produce a structured drift comparison without mutating or revoking the current generation automatically. | High | Open |
| FR-017 | Successor generation | As a mission authority, reconciliation creates a new immutable generation naming its predecessor rather than editing an existing WorkRevision. | High | Open |
| FR-018 | Complete dispositions | As a reconciler, I must assign exactly one legal `continue`, `supersede`, `invalidate`, or `fork` disposition to every affected active or accepted work item. | High | Open |
| FR-019 | Disposition enforcement | As a downstream authority, I reject handoff, review, acceptance, or integration attempts whose generation disposition no longer permits the action. | High | Open |
| FR-020 | Integrated-history preservation | As an operator, reconciliation never rewrites integrated history; changed requirements create explicit repair, replacement, or forked work. | High | Open |
| FR-021 | Action-scoped governance | As an auditor, every action-capable outcome records the selected actor profile, doctrine identity, revisions, digest, and granted authority without loading unrelated governance. | High | Open |
| FR-022 | Typed next protocol | As an agent, one `next` query returns exactly one of `query`, `action`, `work_ready`, `review_ready`, `decision_required`, `blocked`, or `terminal` with stable identities and typed data. | High | Open |
| FR-023 | Query purity and idempotency | As an orchestrator, repeated `next` queries are non-mutating, while repeated action requests use explicit operation identity and return deterministic replay or conflict results. | High | Open |
| FR-024 | Durable decisions and blockers | As a human or agent, missing input, deferred decisions, validation failures, and blockers are durable mission facts with recovery guidance rather than transcript-only state. | High | Open |
| FR-025 | Derived tasks projection | As a compatibility user, I can generate `tasks.md` from authoritative mission and tracker state, and its deletion or modification cannot alter that state. | Medium | Open |
| FR-026 | Projection disclosure | As a reader, every generated compatibility artifact identifies its source generation, render time, and non-authoritative status. | Medium | Open |
| FR-027 | Executable proving scenario | As a maintainer, I can run a two-item mission from intent through finalization, tracker drift, explicit reconciliation, and regenerated projections using both fake and Beads providers. | High | Open |
| FR-028 | Evidence and audit trail | As a reviewer, step transitions, approvals, finalization, drift, reconciliation, and next outcomes expose machine-readable evidence bound to source and graph revisions. | High | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Zero silent drift | Across at least 1,000 generated post-finalization mutations spanning every work field, zero mutations may alter a running contract without a drift result and explicit reconciliation. | Safety | High | Open |
| NFR-002 | Deterministic revisions | Repeating normalization and finalization 100 times over the same semantic graph must produce one digest and byte-equivalent canonical content. | Reliability | High | Open |
| NFR-003 | Concurrent finalization | Across at least 100 two-process finalization races, exactly one current generation is published and every losing attempt returns replay or conflict without partial state. | Concurrency | High | Open |
| NFR-004 | Crash recovery | Fault injection at every artifact-approval, decomposition, finalization, and reconciliation persistence boundary must yield either the complete prior state or one complete successor state after restart. | Reliability | High | Open |
| NFR-005 | Bounded local response | For a 100-item local mission graph, 95% of `next`, validation, drift, and projection queries complete within 500 ms excluding external provider process time; finalization and reconciliation complete within 2 seconds excluding provider time. | Performance | Medium | Open |
| NFR-006 | Provider parity | All provider-neutral mission-method conformance cases pass against the durable fake provider; every capability applicable to Beads passes against the qualified Beads adapter. | Portability | High | Open |
| NFR-007 | Race safety | All mission, artifact, work-revision, reconciliation, governance, and next packages pass the race detector with no waived finding. | Reliability | High | Open |
| NFR-008 | Architecture enforcement | Positive and mutant-negative tests prove tracker adapters, projections, and transports cannot advance mission state or mint execution authority. | Maintainability | High | Open |
| NFR-009 | Governance provenance | One hundred action capsules retain the exact profile and doctrine revisions selected at action time, and later governance changes do not alter their recorded interpretation. | Auditability | High | Open |
| NFR-010 | Projection independence | Across at least 100 delete, corrupt, and edit trials, projection files cause zero changes to mission, tracker, revision, decision, or approval state. | Safety | High | Open |
| NFR-011 | Bounded PR feedback | The pull-request contract, architecture, fake-provider, and mission-method acceptance gates complete within 10 minutes; longer Beads and fault campaigns remain scheduled or manually runnable. | Delivery | Medium | Open |
| NFR-012 | Evidence privacy | Stored governance and mission evidence contains zero credentials, environment secrets, private transcript text, or unrestricted tool output. | Security | High | Open |
| NFR-013 | Backward comprehension | A fresh agent using only authoritative mission queries and tracker work can correctly state each work item's objective, dependencies, criteria, generation, and next legal action in 100% of qualification cases. | Usability | High | Open |
| NFR-014 | Stable error contract | Every rejected transition, invalid graph, drift conflict, stale approval, and unsupported action has a stable machine-readable kind, bounded diagnostic, and recovery category. | Operability | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Authority separation | The tracker owns mutable work definition before finalization; Spec Kitty exclusively owns mission steps, approvals, WorkRevisions, decisions, and legal advancement. | Architecture | High | Open |
| C-002 | No canonical Markdown ledger | No command may require parsing or editing `tasks.md`, checkbox state, or task prose to determine authoritative work or mission state. | Product | High | Open |
| C-003 | Static mission types | The initial software-development mission is statically registered and versioned; this phase does not introduce a general workflow programming language. | Architecture | High | Open |
| C-004 | Explicit mission type | Missing or unsupported mission type is an error and must not silently become software-development. | Compatibility | High | Open |
| C-005 | Phase boundary | Production execution leases, lane/worktree allocation, implementation/review dispatch, acceptance, merge, and closeout execution remain Phase 4 and Phase 5 work. | Scope | High | Open |
| C-006 | External surfaces deferred | CLI polish beyond the proving commands, Orchestrator API serving, MCP, Team Kitty, and Python-project migration remain Phase 6 work. | Scope | High | Open |
| C-007 | Provider substitutability | Beads-specific protocol or workflow semantics remain behind the tracker adapter and may not enter mission, artifact, revision, reconciliation, or governance contracts. | Architecture | High | Open |
| C-008 | Existing authority kernel | Phase 1 lease, reviewer-independence, workspace-isolation, and serialized-integration laws remain unchanged and are exercised where relevant rather than reimplemented. | Compatibility | High | Open |
| C-009 | Single-machine authority | The current control store remains owned by one machine and filesystem authority; distributed control ownership is out of scope. | Deployment | High | Open |
| C-010 | Control-store boundary | This mission does not migrate the qualified control store to Gapdb; persistence remains behind the existing control-store contract. | Scope | Medium | Open |
| C-011 | Integrated immutability | Reconciliation may add follow-up work but may never rewrite Git history or retroactively change an integrated candidate's evidence. | Safety | High | Open |
| C-012 | No transcript authority | Private conversation may inform authored artifacts but may never be the sole durable source of a requirement, blocker, decision, approval, or reconciliation disposition. | Governance | High | Open |

### Key Entities

- **MissionDefinition**: Versioned identity and ordered step contracts for the
  software-development lifecycle.
- **MissionInstance**: One governed lifecycle with current step, selected
  definition, policy, decisions, blockers, and terminal outcome.
- **StepContract**: Required inputs, produced artifacts, validations, approval
  gate, transition policy, and applicable governance action.
- **ArtifactRevision**: Immutable content identity, provenance, validation
  results, and approval references for a mission output.
- **WorkDefinition**: Mutable provider-backed planning unit with stable WorkID,
  criteria, dependencies, write scope, and provider revision.
- **WorkRevision**: Immutable executable graph generation and canonical digest.
- **WorkDisposition**: Reconciliation decision relating one affected work item
  and its existing runs or accepted candidates to a successor generation.
- **DriftReport**: Structured comparison between a current WorkRevision and a
  later provider observation.
- **GovernanceContextRef**: Action-scoped profile and doctrine identities,
  revisions, digest, and granted authority.
- **NextResult**: Pure typed result naming one legal outcome and its stable
  mission, work, decision, or action identities.
- **Projection**: Regenerable, non-authoritative presentation of canonical
  mission and work state.

## Assumptions

- Phase 2's provider-neutral tracker ports and Beads adapter remain the only
  source of mutable work definitions used by this mission.
- Phase 1's control-store transactions, idempotency, event outbox, and authority
  invariants are available for reuse.
- Action-scoped doctrine and profile material can be identified by immutable
  revision or digest even before external agent dispatch is implemented.
- The first proving mission uses two work items because it is the smallest graph
  that exercises dependency ordering, coverage, drift, and affected-work
  reconciliation without adding Phase 4 execution complexity.
- `review_ready` is reserved in the `next` result vocabulary for forward
  compatibility; Phase 3 may return it only from synthetic contract fixtures
  because production review dispatch is Phase 5.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A new software-development mission progresses from accepted intent
  through approved specification, approved plan, tracker decomposition, and
  finalized generation 1 without creating or reading an authoritative Markdown
  task list.
- **SC-002**: The two-item proving mission produces equivalent normalized work
  identities, dependencies, criteria, and graph digest through two conforming
  tracker implementations.
- **SC-003**: One thousand post-finalization mutations produce one thousand
  explicit drift results and zero silent changes to generation 1.
- **SC-004**: Reconciliation tests exercise `continue`, `supersede`,
  `invalidate`, and `fork`, and every affected active or accepted work item has
  exactly one recorded disposition.
- **SC-005**: One hundred concurrent finalization attempts produce exactly one
  current generation per race and no partial or duplicate generation.
- **SC-006**: Crashes injected at every publication boundary recover to one
  complete, auditable state without lost approvals, work mappings, or decisions.
- **SC-007**: Every mission state returns exactly one stable typed `next`
  outcome; repeated pure queries make zero durable changes.
- **SC-008**: Editing, corrupting, or deleting `tasks.md` in one hundred trials
  changes zero canonical mission, tracker, approval, or WorkRevision facts.
- **SC-009**: One hundred action capsules remain bound to the exact governance
  revisions selected when the action became legal.
- **SC-010**: A fresh agent can understand objective, dependencies, acceptance
  criteria, graph generation, and next legal action for every work item without
  private transcript context.
- **SC-011**: Architecture mutants that let tracker status, adapter imports,
  projections, or transport code advance mission authority all fail the quality
  gate with the violated boundary named.
- **SC-012**: Every required correctness, concurrency, architecture, security,
  reproducibility, and delivery qualification gate passes with no unexplained
  waiver.

## Explicit Non-goals

- Allocating production execution leases, lanes, branches, or worktrees.
- Dispatching implementers or reviewers or integrating Git candidates.
- Replacing the Phase 1 control store with Gapdb.
- Implementing research, documentation, plan, or custom mission types.
- Building a general mission workflow language or dynamic plugin system.
- Serving the Orchestrator API, MCP, Team Kitty, or other remote clients.
- Migrating existing Python Spec Kitty projects or importing historical
  Markdown task authority.
- Making Beads statuses, Molecules, agent identities, hooks, or merge slots part
  of the mission engine.
