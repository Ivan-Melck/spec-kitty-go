# Mission Specification: Production Orchestration Architecture

**Mission Branch**: `production-runtime-architecture-01M16GVA`  
**Created**: 2026-08-29  
**Status**: Draft  
**Input**: Consolidate the approved Go-native redesign into canonical technical documentation and prepare a dependency-ordered program of bounded Spec Kitty missions.

## Intent Summary

Spec Kitty's maintainers need one ratified, technically precise architecture record that preserves the product's proven mission method and concurrent worktree behavior while removing inherited Python structure and semantics. The record must define the future production boundary among the memory-primary authority runtime, tracker, Git, workspaces, complete orchestration API, stack-neutral acceptance, and optional agent-native transport. It must also turn that architecture into an evidence-backed mission program without claiming that proposed behavior already ships.

The canonical title is **Production Orchestration Architecture**. This is a documentation-oriented software-development mission. It changes architectural doctrine and delivery contracts, not production runtime behavior.

## User Scenarios & Testing

### User Story 1 - Ratify One Coherent Product Architecture (Priority: P1)

As a Spec Kitty maintainer, I want one canonical architecture recommendation so that implementation missions share the same authority boundaries, terminology, and product laws instead of rediscovering or contradicting them.

**Why this priority**: Every later mission depends on the architecture. Ambiguity here would create incompatible persistence, API, acceptance, and concurrency implementations.

**Independent Test**: A reviewer can trace every approved decision from this specification to one canonical design section, identify the owner of each kind of state, and find no conflicting normative statement in active project documentation.

**Acceptance Scenarios**:

1. **Given** the current Phase 1–3 implementation and its evidence, **When** a maintainer reads the architecture recommendation, **Then** delivered behavior, proposed production behavior, and retired behavior are visibly distinct.
2. **Given** a state or operation such as work definition, execution authority, candidate evidence, or integration, **When** its owner is queried, **Then** the documentation identifies exactly one authoritative capability and any external projections.
3. **Given** the prior Python behavior inventory, **When** the new architecture is reviewed, **Then** preserved product behavior and intentionally retired Python-shaped behavior are explicitly traceable.

---

### User Story 2 - Resume and Explain Missions Reliably (Priority: P1)

As an operator, I want active missions to run from fast memory-resident state while remaining resumable and replayable so that a process or machine failure does not erase authority, decisions, or the explanation of what happened.

**Why this priority**: Fast concurrency without reliable recovery would make the runtime unsafe; durable state without memory-primary execution would recreate the contention model being replaced.

**Independent Test**: The design explains, without contradiction, how an authoritative command is accepted, journaled, projected, restored, replayed without side effects, and reconciled with external facts after restart.

**Acceptance Scenarios**:

1. **Given** an authority-changing command, **When** success is reported, **Then** sufficient durable evidence exists to restore and explain the transition.
2. **Given** a runtime restart, **When** the mission resumes, **Then** its state is restored from a checkpoint and subsequent events before external reconciliation proceeds.
3. **Given** a request for historical playback, **When** events are replayed, **Then** recorded external observations are used and no external side effect is executed again.
4. **Given** pre-restart execution authority, **When** a new runtime epoch begins, **Then** stale processes cannot silently regain current authority.

---

### User Story 3 - Drive and Observe the Whole Process (Priority: P1)

As a user, agent harness, dashboard, or hosted integration, I want one complete orchestration contract so that every legitimate mission action and every progress view uses the same product semantics.

**Why this priority**: Separate CLI, hosted, agent, and automation implementations would recreate semantic drift and competing control planes.

**Independent Test**: A client can discover a mission, determine its exact position, obtain the next legitimate actions, drive a complete governed lifecycle, and watch progress without reading tracker state, Markdown, worktree files, or the control store directly.

**Acceptance Scenarios**:

1. **Given** any running mission, **When** progress is requested, **Then** the response reports phase, executable generation, work-state counts, active authority, critical path, blockers, next actions, drift, last progress, and a resumable revision.
2. **Given** a long-running orchestration operation, **When** its caller disconnects and later returns, **Then** the caller can resume observation using an explicit operation handle.
3. **Given** a request to mutate workflow state, **When** no typed legal command represents it, **Then** the system refuses rather than offering a generic transition or force-success operation.
4. **Given** local CLI, hosted, and optional agent-native clients, **When** they perform the same operation, **Then** they reach the same application capability and receive equivalent typed outcomes.

---

### User Story 4 - Accept Work Using Local Proof (Priority: P1)

As an organization or project maintainer, I want acceptance to express what must be proven while allowing the local stack to determine how it is proven so that Spec Kitty works for software, documentation, research, data, migration, and mixed-stack projects.

**Why this priority**: A language-shaped gate silently excludes valid projects and carries accidental implementation baggage into the new runtime.

**Independent Test**: The design can represent the same acceptance claim using different local providers, validate a common evidence envelope, and reach the same gate decision without the kernel naming an ecosystem tool.

**Acceptance Scenarios**:

1. **Given** organization, project, mission-type, and mission-specific requirements, **When** an acceptance plan is resolved, **Then** their strict required union is retained and every claim has an explicit provider or actionable missing-provider decision.
2. **Given** evidence for a prior candidate, **When** a new candidate is proposed, **Then** stale evidence cannot satisfy the new candidate unless its admissibility policy proves the inputs unchanged.
3. **Given** a permitted waiver, **When** it is applied, **Then** its signer, scope, candidate, rationale, and policy basis are visible.
4. **Given** a project using an unfamiliar toolchain, **When** it supplies a conforming provider, **Then** the acceptance kernel can judge its evidence without adding toolchain knowledge.

---

### User Story 5 - Offer Agent-Native Access Without Requiring It (Priority: P2)

As an MCP-capable user, I want an optional stateless agent-native adapter so that my host can use tools, resources, progress subscriptions, and long-running tasks without making MCP a prerequisite for ordinary Spec Kitty use.

**Why this priority**: MCP can make the complete API broadly available to agents, but forcing it on every user would create needless adoption cost and protocol coupling.

**Independent Test**: The same mission can be created, run, observed, accepted, and resumed using only the normal CLI; installing the optional adapter adds no unique authority or workflow behavior.

**Acceptance Scenarios**:

1. **Given** a standard installation without the optional adapter, **When** a user runs a mission, **Then** the complete supported workflow remains available.
2. **Given** a stateless adapter request, **When** continuity is needed, **Then** explicit Spec Kitty identities and revisions carry it rather than a transport session.
3. **Given** a long-running agent-native tool call, **When** the negotiated task extension is available, **Then** the call can return a task handle, request input, report progress, and accept cooperative cancellation without replacing mission or lease authority.
4. **Given** an expired transport task, **When** the mission is queried later, **Then** durable mission history and resumability remain intact.

---

### User Story 6 - Execute the Redesign as Bounded Missions (Priority: P1)

As the project owner, I want a dependency-ordered implementation program so that the redesign can proceed autonomously through as many missions as necessary without becoming one unreviewable rewrite.

**Why this priority**: The design spans persistence, APIs, tracker composition, acceptance, concurrency, packaging, and optional integrations. Each needs its own disproof experiment and acceptance evidence.

**Independent Test**: The program graph is acyclic, every mission has a bounded outcome and falsification target, and each dependency states what stable contract or evidence the successor consumes.

**Acceptance Scenarios**:

1. **Given** the ratified architecture, **When** the program is decomposed, **Then** each mission owns one coherent delivery boundary and retains its own specify, plan, tasks, review, accept, merge, mission-review, and retrospective gates.
2. **Given** two missions without a dependency, **When** resources permit, **Then** the program identifies that they may proceed concurrently.
3. **Given** an architectural uncertainty, **When** no evidence yet resolves it, **Then** a proving mission precedes production implementation rather than embedding the assumption in several downstream missions.

### Edge Cases

- A proposed architectural rule conflicts with behavior already frozen by Phase 1–3 evidence.
- Existing documentation describes SQLite as qualified while the new production law requires its complete removal.
- The tracker is unavailable during recovery, but the pinned executable graph must remain resumable.
- A progress percentage appears healthy while the critical dependency path is blocked.
- A required acceptance claim has no provider for the project's stack.
- Evidence passes but belongs to a different candidate, work generation, policy revision, or runner trust domain.
- An optional adapter exposes a command that the normal API or CLI cannot perform.
- A transport task is cancelled after the underlying operation has already produced an external effect.
- A prior runtime process resumes after a new epoch and continues writing its quarantined workspace.
- A program mission grows until its falsification target or review boundary is no longer understandable.
- Documentation diagrams render locally but fail under the repository host's supported syntax.
- Historical documents remain useful as evidence but conflict with the new canonical recommendation.

## Domain Language

| Canonical term | Meaning | Avoid or qualify |
|---|---|---|
| Authority runtime | The host-owned capability that decides whether a governed transition may occur | “database” when referring to product authority |
| Control journal | Durable, ordered evidence of accepted domain transitions and external-effect intent/observation | “logs” when implying disposable diagnostics |
| Mission checkpoint | A versioned materialized mission state used to bound recovery replay | “backup” unless discussing storage backup |
| Playback | Side-effect-free reconstruction or explanation from recorded events and observations | “rerun” when no external effects may execute |
| Reconciliation | Explicit comparison and resolution between restored authority state and current external facts | “sync” when authority consequences are involved |
| Progress projection | Canonical derived view of mission position, active work, blockers, and next actions | Tracker status or Markdown task completion |
| Acceptance claim | A stack-neutral statement that must be proven for a candidate | A particular test or tool command |
| Qualification provider | A local or external capability that produces evidence for one or more claims | “plugin” unless discussing its delivery mechanism |
| Evidence envelope | Typed provenance and outcome record bound to a claim and exact candidate context | Human-formatted tool output |
| Native operation | A resumable, asynchronous application operation exposed through CLI and API | MCP Task, which is one optional projection |
| MCP Task | A transport handle for one agent-native tool request | Mission, work item, run, or lease |

## Requirements

### Functional Requirements

| ID | Title | Requirement | Priority | Status |
|---|---|---|---|---|
| FR-001 | Canonical recommendation | Produce one normative architecture recommendation covering the complete production runtime and clearly superseding conflicting active design statements. | High | Open |
| FR-002 | Delivery truth | Label every described capability as delivered, retained, amended, proposed, or retired, with evidence links for delivered claims. | High | Open |
| FR-003 | Behavioral preservation map | Map the proven Python and Phase 1–3 behavior inventory to preserved product laws, amended behavior, intentional retirements, and later proof obligations. | High | Open |
| FR-004 | Authority ownership map | Define exactly one authority owner and allowed projections for mission, work definition, executable work, execution, workspace, candidate, review, acceptance, integration, and progress state. | High | Open |
| FR-005 | Memory-primary state model | Specify which active state is memory-resident, which transitions require durable evidence before acknowledgement, and which observations may remain ephemeral. | High | Open |
| FR-006 | Resume protocol | Define checkpoint, journal, restore, runtime-epoch, and external reconciliation semantics sufficient to resume interrupted missions safely. | High | Open |
| FR-007 | Playback protocol | Define deterministic side-effect-free playback, event schema versioning, external observation handling, and portable terminal mission evidence. | High | Open |
| FR-008 | Storage adoption boundary | Record the approved production law that the future runtime has no SQLite runtime, build, fallback, or hidden migration dependency and identify the mission that must prove removal. | High | Open |
| FR-009 | Tracker composition | Define Beads as the default first-party work substrate while retaining provider-neutral kernel contracts and preventing tracker state from becoming execution or acceptance authority. | High | Open |
| FR-010 | Process-complete API | Specify a canonical application API that exposes the governed mission lifecycle through typed commands, queries, results, diagnostics, and events. | High | Open |
| FR-011 | Typed mutation surface | Enumerate the required command families and explicitly reject generic state transitions, force-success operations, caller-created authority, and direct store or Git mutation. | High | Open |
| FR-012 | Native asynchronous operations | Define resumable operation handles, progress, input requests, cooperative cancellation, terminal results, and idempotent replay independently of any transport. | High | Open |
| FR-013 | Mission progress | Define list, query, and watch projections for any running mission, including phase, generation, work counts, active runs, review/integration position, critical path, blockers, next actions, drift, and journal revision. | High | Open |
| FR-014 | Client parity | Define how normal CLI, hosted clients, Team Kitty, CI, dashboards, and optional adapters project the same application capabilities without alternate workflow semantics. | High | Open |
| FR-015 | Acceptance claims | Define stack-neutral acceptance claims assembled from organization, project, mission-type, and mission requirements. | High | Open |
| FR-016 | Provider resolution | Define explicit, revision-pinned claim-to-provider binding, suggestion-only stack detection, and actionable behavior when no qualified provider exists. | High | Open |
| FR-017 | Evidence contract | Define a common evidence envelope bound to claim, candidate, executable generation, policy, provider, runner, time, and artifact digests. | High | Open |
| FR-018 | Gate evaluation | Define pass, fail, error, skipped, and waived outcomes; evidence freshness; strict policy union; and language-neutral acceptance decisions. | High | Open |
| FR-019 | Qualification graph | Define dependency-aware concurrent qualification, content-based evidence reuse, invalidation, and progress projection. | Medium | Open |
| FR-020 | Optional MCP packaging | Define the agent-native adapter as an optional, independently packaged module that contributes no unique workflow capability or core dependency. | Medium | Open |
| FR-021 | Stateless MCP contract | Pin the proposed adapter to MCP `2026-07-28` semantics: self-describing requests, explicit handles, discovery, subscriptions, MRTR, and no protocol session authority. | Medium | Open |
| FR-022 | MCP Tasks mapping | Map negotiated MCP Tasks to native operations while preserving mission, work, run, lease, decision, cancellation, TTL, and evidence boundaries. | Medium | Open |
| FR-023 | Architecture enforcement | Specify dependency and mutant-test guards preventing persistence, tracker, ecosystem qualification, or transport adapters from leaking into the authority kernel. | High | Open |
| FR-024 | Program graph | Produce an acyclic, dependency-ordered set of candidate implementation missions with bounded outcomes, contract dependencies, disproof experiments, and independent gates. | High | Open |
| FR-025 | Mission admission rules | Define entry evidence, exit evidence, non-goals, and go/no-go conditions for every proposed implementation mission. | High | Open |
| FR-026 | Documentation migration | Identify active README, project plan, ADR, contract, and operational sections that must be replaced, amended, retained as history, or linked to the new canonical record. | High | Open |
| FR-027 | First executable slice | Select the smallest first implementation mission that can disprove the new architecture's most consequential unproven claim. | High | Open |
| FR-028 | Review brief | Produce an adversarial review brief focused on authority leakage, resumability, evidence admissibility, API completeness, optional-adapter isolation, and program overreach. | Medium | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|---|---|---|---|---|---|
| NFR-001 | Requirement traceability | 100% of functional requirements map to at least one planned artifact and verification method; no functional requirement remains unmapped at task finalization. | Quality | High | Open |
| NFR-002 | Authority clarity | A structured ownership review must find zero state categories with two writable authorities and zero authority categories without an owner. | Correctness | High | Open |
| NFR-003 | Delivery honesty | Automated and human review must find zero statements that describe proposed GapDB, API 2.0, acceptance, or MCP behavior as currently shipped. | Integrity | High | Open |
| NFR-004 | Conflict closure | All normative conflicts found across the README, technical plan, ADRs, and current mission records must be resolved or explicitly retained as historical context. | Consistency | High | Open |
| NFR-005 | Program boundedness | Every proposed implementation mission must have one primary falsification target, no more than three named external contract dependencies, and explicit entry and exit evidence. | Maintainability | High | Open |
| NFR-006 | Program acyclicity | The published mission dependency graph must contain zero cycles and identify every critical-path edge. | Planning | High | Open |
| NFR-007 | Reviewability | A technically informed reviewer must be able to locate current status, normative architecture, next mission, and unresolved risks within five minutes using the documentation index. | Usability | Medium | Open |
| NFR-008 | Diagram portability | Every diagram must render under the repository host's supported Mermaid syntax or use a portable text alternative; automated syntax checks must report zero failures. | Compatibility | Medium | Open |
| NFR-009 | Link integrity | Documentation link validation must report zero broken local links in files created or modified by this mission. | Quality | Medium | Open |
| NFR-010 | Protocol precision | Every proposed external command, event, evidence, and task envelope must state identity, idempotency, authority, and failure semantics; review must find zero unspecified mutating operations. | Correctness | High | Open |
| NFR-011 | Recovery completeness | The recovery design must account for every authority-changing event family and every external system named in the architecture, with zero silent-success recovery branches. | Reliability | High | Open |
| NFR-012 | Acceptance neutrality | Static architecture checks must find zero ecosystem tool or language dependencies in the proposed acceptance-kernel package set. | Portability | High | Open |
| NFR-013 | Optional-adapter isolation | Core build and qualification plans must complete without installing, importing, configuring, or understanding the MCP adapter. | Modularity | High | Open |
| NFR-014 | Evidence economy | Proposed durable envelopes store bounded metadata and digests rather than unbounded logs, transcripts, or artifact bodies; every unbounded payload has an external artifact reference. | Resource safety | Medium | Open |
| NFR-015 | Independent validation | At least one adversarial architecture review must assess the complete recommendation before implementation mission contracts are frozen. | Assurance | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|---|---|---|---|---|---|
| C-001 | Documentation-only mission | This mission may change documentation, architecture contracts, and delivery metadata but must not implement or claim production runtime behavior. | Scope | High | Open |
| C-002 | Preserve concurrency crown jewel | Lease-scoped isolated workspaces, non-transfer to replacement leases, independent review, and serialized integration remain product laws. | Product | High | Open |
| C-003 | GapDB production direction | The target production architecture uses GapDB as the sole control-state substrate; SQLite is not a permitted production fallback or module dependency. | Architecture | High | Open |
| C-004 | Memory-primary durability | Volatile-only authority is forbidden: authoritative success requires durable recovery evidence, while the active projection remains memory-primary. | Reliability | High | Open |
| C-005 | External facts remain bounded | Beads owns mutable work definition and discussion; Git owns code and ref facts; neither may infer or replace mission execution authority. | Architecture | High | Open |
| C-006 | One application boundary | CLI, hosted integrations, automation, Team Kitty, and optional transports must consume the same canonical application capabilities. | Architecture | High | Open |
| C-007 | Host-owned mutation | Remote clients cannot open the control store, create or transfer workspaces, mint authority, mutate refs, or manufacture gate evidence. | Security | High | Open |
| C-008 | No generic transition | The proposed API exposes named legal operations rather than an arbitrary lane or state mutation command. | Correctness | High | Open |
| C-009 | Acceptance kernel neutrality | Language, ecosystem, build-system, CI, and human providers remain outside the acceptance kernel. | Modularity | High | Open |
| C-010 | Explicit qualification binding | Automatic stack detection may suggest bindings but cannot silently establish acceptance policy. | Governance | High | Open |
| C-011 | MCP is optional | Normal CLI and native API operation must remain complete without MCP knowledge, installation, or protocol support. | Adoption | High | Open |
| C-012 | Stateless MCP baseline | The optional adapter targets MCP `2026-07-28`; protocol sessions, legacy HTTP+SSE, deprecated Roots, Sampling, and Logging are not baseline dependencies. | Compatibility | Medium | Open |
| C-013 | MCP Task is not domain state | Task cancellation, expiration, completion, or loss cannot directly revoke leases, erase mission history, or substitute for mission acceptance. | Correctness | High | Open |
| C-014 | Historical evidence integrity | Prior phase evidence remains immutable historical evidence and does not automatically qualify changed production architecture. | Governance | High | Open |
| C-015 | Per-mission gates | Every later mission retains independent specification, planning, implementation, review, acceptance, merge, mission-review, and retrospective gates. | Delivery | High | Open |
| C-016 | No source transliteration | Proposed packages and mission boundaries follow Go-native capabilities, not Python source layout or command structure. | Architecture | High | Open |
| C-017 | Canonical-source discipline | New normative decisions live in one canonical record; other active documentation links to or summarizes it without creating competing specifications. | Documentation | High | Open |
| C-018 | One filesystem authority | The first production control store is owned by one machine/filesystem authority; remote callers use the application boundary. | Deployment | High | Open |

### Key Entities

- **Architecture Decision**: A ratified rule with rationale, status, affected capabilities, evidence basis, and superseded statements.
- **Authority Ownership Entry**: A state category, its single writable owner, allowed readers/projections, and prohibited inferences.
- **Control Event**: Versioned durable evidence of an accepted domain transition or external-effect observation.
- **Mission Checkpoint**: Versioned materialized mission state tied to a journal revision and reducer version.
- **Native Operation**: Explicit asynchronous application request with idempotency identity, progress, input requests, cancellation intent, and terminal outcome.
- **Progress Projection**: Revisioned mission summary derived from authority events and executable dependencies.
- **Acceptance Claim**: Stack-neutral proposition that must be supported by admissible evidence.
- **Qualification Binding**: Revision-pinned association between a claim and an approved evidence provider.
- **Evidence Envelope**: Bounded outcome and provenance record tied to an exact claim and candidate context.
- **Program Mission**: Bounded delivery unit with dependencies, falsification target, entry evidence, exit evidence, and independent governance gates.
- **MCP Task Projection**: Optional transport representation of one native operation, never the underlying domain authority.

## Assumptions

- Phase 1–3 evidence and mission reviews remain valid only for the exact commits and boundaries they attest.
- GapDB's existing public client, durability, snapshot, recovery, and backup capabilities are available as inputs; production adoption in this repository still requires its own qualification mission.
- Beads remains the default first-party tracker choice unless a later explicit product decision changes it.
- Orchestrator API 1.3 consumers require a compatibility path while API 2.0 becomes canonical.
- The normal CLI remains the primary adoption surface; hosted and agent-native surfaces are optional projections.
- The current runtime is single-machine-authority first, while remote agents and clients interact through the host boundary.
- No unresolved product decision is knowingly deferred in this specification; implementation details remain for planning and proving missions.

## Out of Scope

- Implementing the GapDB control-store adapter or migrating live state.
- Removing SQLite code or dependencies during this documentation mission.
- Implementing Orchestrator API 2.0, progress watches, or native asynchronous operations.
- Implementing the acceptance engine or ecosystem qualification providers.
- Implementing or packaging the MCP adapter.
- Dispatching production agents, changing worktree authority, or modifying integration behavior.
- Promising Team Kitty, hosted scaling, or multi-machine control-store ownership.
- Freezing the final number of later implementation missions before the architecture and code inventory have justified it.

## Success Criteria

### Measurable Outcomes

- **SC-001**: Reviewers can trace all 28 functional requirements to canonical architecture sections, contracts, or program entries with zero unmapped requirements.
- **SC-002**: The authority ownership matrix contains zero duplicate writable owners and zero unowned state categories.
- **SC-003**: The canonical documentation distinguishes delivered, proposed, retained, amended, and retired behavior with zero false shipped claims.
- **SC-004**: The program dependency graph is acyclic, identifies its critical path, and gives every proposed mission one bounded falsification target plus entry and exit evidence.
- **SC-005**: A recovery walkthrough demonstrates checkpoint restoration, journal replay, new runtime epoch, and external reconciliation without repeating side effects or trusting stale authority.
- **SC-006**: Progress examples answer where any running mission is, why it is there, what is active or blocked, and what can happen next from one revisioned response.
- **SC-007**: Two materially different project stacks can satisfy the same acceptance claim through different providers while producing evidence judged by one neutral gate contract.
- **SC-008**: The complete normal workflow remains documented and possible without the optional MCP adapter; MCP-specific capabilities introduce zero unique mission semantics.
- **SC-009**: All created or modified diagrams render successfully and all local documentation links validate.
- **SC-010**: An adversarial review returns no unresolved blocker against authority, recovery, acceptance neutrality, API completeness, optional-adapter isolation, or program boundedness before implementation contracts are frozen.
