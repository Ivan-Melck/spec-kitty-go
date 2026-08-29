# Research: Production Orchestration Architecture

## Evidence basis

This record synthesizes:

- the delivered Phase 1–3 Go packages and frozen qualification evidence;
- `docs/PYTHON_BEHAVIOR_INVENTORY.md` and `docs/MINI_REVIEW_RESOLUTION.md`;
- the qualified Beads 1.2.2 adapter and provider evidence;
- GapDB's public API, MVP mission review, WAL/snapshot design, recovery, and performance evidence;
- the Orchestrator API 1.3 projection and its deferred gaps;
- MCP `2026-07-28` and the independently versioned Tasks extension.

Historical evidence remains valid only for its attested source and boundary. The decisions below describe the target architecture; they do not transfer qualification to unimplemented code.

## Decision 1: Memory-primary authority with durable acceptance evidence

**Decision**: Active control state is served from one memory-resident owner. Every authority-changing operation uses durable acknowledgement before success is reported. Reconstructible telemetry may use memory acknowledgement under an explicit loss contract.

**Rationale**: The workload is a high-frequency coordination plane of claims, leases, decisions, handoffs, reviews, and integration reservations. Multiple embedded database connections would turn filesystem writer-lock scheduling into application scheduling. One ordered memory owner makes contention, revision order, CAS, overload, and durability explicit.

**Alternatives considered**:

- Keep SQLite WAL: rejected as the target production model because independent agent processes contend through one file-writer lock and busy/retry behavior becomes hidden orchestration.
- Volatile process memory only: rejected because missions would not resume or explain accepted authority after a crash.
- Reconstruct everything from Beads and Git: rejected because neither system contains complete lease, review-independence, decision, reconciliation, or operation-attribution history.

## Decision 2: GapDB is the sole target control-state substrate

**Decision**: Future production composition uses GapDB and contains no SQLite runtime, driver, fallback, or embedded migration dependency. Adoption requires a dedicated external-adapter comparison and torture campaign before current code is removed.

**Rationale**: GapDB already provides one owner process, serialized conditional mutations, database-wide revisions, atomic batches, bounded scans and watches, WAL-before-publication, explicit memory/durable acknowledgements, checksummed snapshots, and strict recovery. Those primitives directly match Spec Kitty's control-plane needs.

**Alternatives considered**:

- Hide SQLite behind the existing port indefinitely: rejected because interface substitution does not eliminate the incompatible concurrency model.
- Embed GapDB internals into the Spec Kitty process: deferred and not assumed. The released public boundary is a client over a Unix socket; adoption must prove lifecycle and packaging without importing internal packages.
- Run both stores during migration: rejected as a production authority model because dual writes create two failure domains and ambiguous authority. Any migration is offline/export-based and absent from the normal module graph.

## Decision 3: Product journal and checkpoint sit above storage recovery

**Decision**: GapDB snapshot plus WAL restores database records. A versioned mission checkpoint plus subsequent domain events restores and explains product state. Playback applies recorded events and observations without performing external effects.

**Rationale**: Database recovery answers “what bytes committed?” Product recovery must answer “what transition was authorized, what did it mean, and what external effect may have applied?” These are separate contracts.

**Alternatives considered**:

- Persist only current aggregate documents: rejected because audit and deterministic playback disappear.
- Persist only events and replay from genesis: rejected because unbounded restart work is unnecessary; versioned checkpoints bound recovery.
- Replay commands: rejected because command handling can depend on current policy and would risk repeating side effects. Playback reduces accepted events.

## Decision 4: Runtime restart creates a new authority epoch

**Decision**: Opening a recovered authority runtime creates a new durable epoch. Pre-restart run leases are non-current until explicit reconciliation; their workspaces remain permanently bound to their old lease generation and are quarantined from replacement authority.

**Rationale**: A recovered database cannot prove that an old agent process is dead. Epoch invalidation plus workspace non-transfer turns an unpreventable POSIX zombie write into an isolated stale workspace rather than current authority.

**Alternatives considered**:

- Restore leases as live from their previous expiry: rejected because a zombie could continue under authority the new owner cannot safely distinguish.
- Hand the old workspace to a replacement: rejected by the workspace fencing finding; database tokens cannot fence arbitrary filesystem writes.

## Decision 5: Orchestrator API 2.0 is the canonical application seam

**Decision**: All supported clients consume one typed command/query/event surface. The API exposes the complete governed lifecycle and native asynchronous operations. API 1.3 remains a compatibility projection, not the canonical domain vocabulary.

**Rationale**: A shared application seam prevents CLI, Team Kitty, automation, dashboards, and MCP from implementing competing workflow rules. It also establishes the only supported remote boundary around the one-machine authority owner.

**Alternatives considered**:

- Keep host CLI mutations separate from external API mutations: rejected because behavior and audit requirements drift.
- Expose CRUD and a generic `transition`: rejected because callers could skip legal state transitions and manufacture authority.
- Make Beads the orchestration API: rejected because ticket readiness, assignment, and comments are not execution, review, or integration authority.

## Decision 6: Progress is a canonical graph projection

**Decision**: `ListMissions`, `GetMissionProgress`, and `WatchMissionProgress` return a revisioned read model derived from the finalized work graph and authority events. Percentage is optional secondary information; critical path, blockers, current work, review/integration position, and next actions are primary.

**Rationale**: Concurrent graph-shaped work can report a high percentage while its only critical path is blocked. Users need an honest answer to where the mission is and why.

**Alternatives considered**:

- Parse Markdown task boxes: rejected as inherited presentation-state authority.
- Use Beads ticket status: rejected because tracker state is advisory after finalization.
- Show only a scalar percentage: rejected as misleading under dependencies and parallelism.

## Decision 7: Acceptance judges claims and evidence, not ecosystems

**Decision**: The kernel knows `AcceptanceClaim`, `QualificationBinding`, `EvidenceEnvelope`, policy, and decision outcomes. Project/org/mission configuration binds claims to providers. Stack detection only proposes bindings. Providers may be local processes, CI attestations, human decisions, or future external systems.

**Rationale**: Spec Kitty cannot know the language, build system, organization, objective, or compliance environment of an arbitrary project. Standardizing evidence allows local proof without embedding Python, Go, Node, Maven, or other ecosystems into the gate.

**Alternatives considered**:

- Hard-code popular stack commands: rejected as the same structural porting error in a new language.
- Treat one project script such as `make quality` as universal: useful as a provider binding, rejected as kernel law.
- Accept provider exit code alone: rejected because evidence must be tied to the exact claim, candidate, work generation, provider revision, runner, and policy.

## Decision 8: Qualification forms its own dependency graph

**Decision**: Independent claims run concurrently, dependencies are explicit, evidence is reused only by validated input digest and policy, and a candidate/context change invalidates affected evidence. Missing required providers yield `decision_required`; they never silently pass or skip.

**Rationale**: This makes acceptance fast for unchanged evidence while retaining exact provenance and allowing non-code objectives.

**Alternatives considered**:

- One monolithic gate command: retained only as a possible provider, not as the product model.
- Re-run every check on every transition: rejected as unnecessary work with no stronger proof when inputs are unchanged.

## Decision 9: MCP is optional and independently packaged

**Decision**: The normal CLI and native API remain complete. An optional `spec-kitty-mcp` module projects resources, tools, prompts, subscriptions, MRTR, and Tasks over the API. Core builds and tests do not import the MCP SDK.

**Rationale**: MCP gives compatible agents a natural integration without forcing existing users and agents to learn a new protocol or making protocol churn part of the runtime supply chain.

**Alternatives considered**:

- Make MCP the internal application protocol: rejected because the product would inherit a transport's lifecycle and vocabulary.
- Link MCP into the core binary but disable it by default: rejected as weaker modularity; unused dependencies and attack surface remain.
- Exclude MCP entirely: rejected because the process-complete API maps cleanly to agent tools and resources.

## Decision 10: Target stateless MCP `2026-07-28`

**Decision**: The optional adapter targets self-describing stateless requests with explicit handles, optional `server/discover`, header routing, cacheable lists, MRTR, and `subscriptions/listen`. It does not depend on `initialize`, `Mcp-Session-Id`, legacy HTTP+SSE, Roots, Sampling, or Logging.

**Rationale**: Stateless MCP aligns with the host boundary: transport instances hold no mission authority, while explicit Spec Kitty identities carry continuity. The official Go SDK supports the revision.

**Primary source**: [MCP 2026-07-28 release](https://blog.modelcontextprotocol.io/posts/2026-07-28/)

**Alternatives considered**:

- Implement the earlier stateful session protocol first: rejected because it would create immediate migration work and encourage hidden transport authority.
- Require only stdio: rejected as insufficient for authenticated remote clients; stdio may remain a local transport.

## Decision 11: MCP Tasks project native operations

**Decision**: A negotiated MCP Task may represent one long-running native operation such as drive-mission-until-boundary, qualification, integration, recovery, or reconciliation. Task IDs, TTL, status, input, and cancellation are transport projections. Domain missions, work, runs, leases, decisions, and evidence remain authoritative.

**Rationale**: Tasks provide server-directed asynchronous results, `working`/`input_required`/terminal states, polling, status notifications, and cooperative cancellation. These are exceptionally useful for agent orchestration when kept above the domain model.

**Primary source**: [MCP Tasks extension](https://tasks.extensions.modelcontextprotocol.io/specification/draft/tasks)

**Alternatives considered**:

- One MCP Task equals one mission: rejected because task TTL or client capability cannot define mission lifetime.
- Map `tasks/cancel` directly to lease revocation: rejected because cancellation is cooperative and can race with completion or external effects.
- Create Tasks for every query: rejected; short calls return ordinary results and the server chooses task augmentation only where useful.

## Decision 12: Deliver by falsification-oriented missions

**Decision**: Begin with the smallest vertical mission that proves GapDB-backed authoritative transition, recovery, epoch invalidation, and playback under a real two-agent crash scenario. Build API, acceptance, and optional transport on that proven substrate. Keep Beads/Git/workspace interaction in every relevant vertical proof rather than scaffolding isolated packages.

**Rationale**: The architecture's hardest claim is safe authority under disagreement among process lifetime, memory state, durable state, Git, tracker observations, and filesystem writes. Proving it early is more valuable than broad package scaffolding.

**Alternatives considered**:

- One production-runtime mega-mission: rejected as too broad to review or falsify.
- Implement all interfaces before a vertical scenario: rejected because it can produce a beautiful airport without proving the airplane flies.
- Freeze the final mission count now: rejected; the ratified program records candidate missions and admission rules, then adjusts through explicit program decisions as evidence arrives.

## Decision 13: Package GapDB as a pinned local sidecar

**Decision**: The first production experiment treats `gapdbd` as a separately owned, project-local sidecar reached through GapDB's public Unix-socket client. Spec Kitty packages, discovers, starts or connects to, health-checks, and reconnects to an exact GapDB release tuple. It does not import `internal/server` or make an unshipped embedding API a program assumption.

**Rationale**: The current GapDB module publishes its client and types, while owner/server lifecycle remains internal and the repository has no immutable release tag. A sidecar preserves GapDB's single memory-owner model, keeps its failure boundary explicit, and lets the first mission qualify the real supported public contract. That qualification must identify the GapDB source revision, binary digest, client/protocol version, lifecycle policy, and signed human adoption decision.

**Alternatives considered**:

- Import GapDB's internal owner: rejected because Go's package boundary forbids it and the program must not depend on private implementation.
- Require GapDB to publish an embedding API first: not currently justified; it expands two products before the external adapter is proven.
- Launch an unpinned `gapdbd` found on `PATH`: rejected because protocol skew and binary identity would make recovery evidence non-reproducible.
- Maintain both embedded and sidecar modes: rejected for the first production release because it doubles lifecycle and fault semantics.

## Research risks carried into review

- GapDB's public client/server lifecycle and release packaging must be proven without importing internal packages or assuming an unshipped embedding API.
- GapDB's default value and batch bounds require an event/checkpoint key design that never creates an unbounded aggregate record.
- API 1.3 compatibility must not preserve unsafe caller-created worktree or generic transition semantics.
- The current Tasks extension versions independently from the MCP core; the adapter must pin and negotiate its exact extension contract.
- Architecture guard tests must distinguish documentation examples from forbidden production dependencies without becoming brittle word bans.
- Existing Phase 1–3 tests directly inspect SQLite; migration must replace their evidence strategy rather than delete the crown-jewel torture scenarios.
