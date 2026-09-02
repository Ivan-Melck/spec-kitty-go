# Mission Specification: GapDB-Only Production Authority

**Mission Branch**: `feat/gapdb-only-production-authority`  
**Created**: 2026-09-02  
**Status**: Draft  
**Input**: Continue the accepted production-runtime program by completing M2: move every production authority capability onto the adopted M1 GapDB event/CAS substrate and eliminate SQLite completely from production.

## Intent Summary

The primary actor is the local Spec Kitty runtime owner serving concurrent
agents on one machine/filesystem authority. A mission start, resume, or
authority-changing command must rebuild and mutate authoritative state through
the exact M1-adopted journal, checkpoint, runtime-epoch, and sidecar contracts.
Success means every Phase 1–3 behavior and refusal remains available while no
production source, dependency, binary, installation, configuration, fallback,
or migration path can reach SQLite. Unsupported historical SQLite state causes
an explicit offline disposition; it never selects a backend, imports itself,
or becomes a second truth.

The main adversarial exception is disagreement after an ambiguous durable
write or process restart. The runtime must recover the recorded outcome by
stable operation identity, reconstruct an identical projection, rotate its
epoch, invalidate predecessor authority, and perform no external side effect
during playback. The invariant that may never be relaxed is one writable owner
per state category and one conditional durable batch per accepted authority
transition.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Operate a mission without SQL authority (Priority: P1)

As a Spec Kitty operator, I can create, advance, pause, resume, and complete a
mission while every authoritative fact is recorded by the adopted durable
event model and served from the recovered in-memory projection.

**Why this priority**: This is the mission's primary falsification target. If
one authority capability still needs SQL semantics or a second writable store,
the redesign has not crossed its storage boundary.

**Independent Test**: Start from an empty control root, exercise one complete
two-item mission through definition finalization, concurrent execution,
handoff, independent review, acceptance, and serialized integration, restart
between every major phase, and obtain the same terminal facts without any SQL
implementation present.

**Acceptance Scenarios**:

1. **Given** an empty supported control root and the accepted M1 tuple, **When** an operator advances every lifecycle capability, **Then** each accepted transition records one atomic durable event/result/head publication before success is acknowledged.
2. **Given** two dependency-independent work items, **When** two agents claim and execute concurrently, **Then** both progress in distinct lease-scoped workspaces while each work revision has at most one current authority.
3. **Given** completed candidates with independent approvals, **When** integration is requested concurrently, **Then** repository authority serializes publication and records each admitted candidate at most once.

---

### User Story 2 - Resume after crashes and ambiguous responses (Priority: P1)

As an operator, I can restart the control owner after any durable boundary and
know whether a command committed, which epoch is current, and which external
effects still require reconciliation.

**Why this priority**: Fast memory-primary state is safe only if durable facts
recreate it exactly and stale processes cannot regain authority.

**Independent Test**: Inject a crash or response loss before and after every
authority-changing publication family, restart with a fresh runtime epoch, and
compare the restored projection and operation result with an uninterrupted
reference execution.

**Acceptance Scenarios**:

1. **Given** a durable commit whose response was lost, **When** the same operation identity and request digest is retried, **Then** the recorded result is returned without a duplicate transition or external effect.
2. **Given** the same operation identity with a different request digest, **When** it is retried, **Then** the request fails closed as an idempotency conflict.
3. **Given** an owner restart, **When** product restore succeeds, **Then** a new runtime epoch is durably admitted before commands and every predecessor lease/capsule refuses.
4. **Given** corrupt, unknown-version, incomplete, or over-budget history, **When** recovery starts, **Then** authority remains degraded or closed with a bounded actionable diagnosis.

---

### User Story 3 - Preserve semantics across replacement (Priority: P1)

As a maintainer, I can demonstrate that replacing the control-state substrate
did not change the product laws proven in Phases 1–3.

**Why this priority**: A storage port is a regression if it loses rejection
ordering, identity separation, reconciliation semantics, review independence,
workspace quarantine, or Git integration safety.

**Independent Test**: Run the frozen behavioral inventory and capability-level
contracts against the new production composition, including invalid inputs and
mutation controls, and compare typed outcomes with the qualified baseline.

**Acceptance Scenarios**:

1. **Given** each frozen authority capability and refusal case, **When** the same typed command/query sequence runs, **Then** the result kind, authority mutation count, and durable evidence are equivalent.
2. **Given** a stale WorkRevision, lease, candidate, policy revision, reviewer identity, or repository fence, **When** a later transition is attempted, **Then** the earliest authoritative refusal occurs before any external mutation.
3. **Given** tracker, Git, filesystem, process, or human observations that disagree with desired state, **When** reconciliation runs, **Then** it records an explicit disposition or decision requirement rather than inferring authority.

---

### User Story 4 - Detect unsupported historical state honestly (Priority: P1)

As an operator with an old control root, I receive an explicit offline
disposition instead of a silent import, empty mission list, fallback backend,
or partially migrated authority graph.

**Why this priority**: Automatic compatibility is the easiest route to dual
truth and makes rollback or recovery impossible to reason about.

**Independent Test**: Present supported empty/current roots and a matrix of
historical SQLite roots, mixed roots, copied files, malformed roots, and
look-alikes; only supported current roots may admit runtime authority.

**Acceptance Scenarios**:

1. **Given** an identified historical SQLite control root, **When** production startup inspects it, **Then** startup refuses before creating or mutating current authority data and reports a documented offline disposition.
2. **Given** both current and historical state in one control root, **When** startup runs, **Then** it refuses split authority rather than preferring either representation.
3. **Given** an empty supported root, **When** startup runs, **Then** it initializes exactly one current authority without consulting a legacy provider.

---

### User Story 5 - Prove SQLite is structurally absent (Priority: P1)

As a release reviewer, I can verify from source and built artifacts that the
production runtime cannot select, load, call, import, or migrate SQLite.

**Why this priority**: A disabled fallback or unused driver remains a future
authority leak. Removal must be structural, not configurational.

**Independent Test**: Inspect production source reachability, module and
transitive dependency graphs, software bills of materials, clean-install
outputs, binaries, runtime configuration, help text, environment handling, and
fault paths using rejecting mutants for every prohibited edge.

**Acceptance Scenarios**:

1. **Given** the production source graph, **When** structural analysis runs, **Then** no production package reaches an SQLite implementation, driver, SQL migration, or backend selector.
2. **Given** every production binary and clean installation artifact, **When** dependency and string/symbol inspection runs, **Then** no SQLite implementation or runtime configuration surface is present.
3. **Given** mutants that add a hidden driver, fallback, dual write, importer, build-tag edge, reflection edge, generated-file edge, or subprocess escape, **When** qualification runs, **Then** every mutant is rejected.

---

### User Story 6 - Preserve evidence and diagnosis (Priority: P2)

As an operator or later mission, I can inspect a bounded source-bound record of
what recovered, what remains blocked, which revision is current, and why an
operation was refused without opening the control database directly.

**Why this priority**: M3–M5 depend on stable authority evidence rather than
storage-specific inspection or error strings.

**Independent Test**: Rebuild progress and operation history from checkpoint
plus journal, compare snapshots around restart, and verify every refusal and
recovery branch returns a bounded typed diagnostic with immutable identities.

**Acceptance Scenarios**:

1. **Given** a running or recovered mission, **When** a supported query asks for current state, **Then** it returns current journal revision, runtime epoch, work generation, blockers, and legal next actions without granting authority.
2. **Given** an unavailable or dishonest external observer, **When** reconciliation cannot attribute an effect, **Then** the operation remains explicit and unresolved with no blind retry or compensation.
3. **Given** playback of accepted history, **When** projections rebuild, **Then** no provider, Git, filesystem, agent, compensation, or durable-write adapter is invoked.

### Edge Cases

- A durable batch commits but the client sees EOF, timeout, cancellation, or a malformed acknowledgement.
- The owner crashes while admitting a new epoch or while an old process remains alive.
- Two callers race on the same aggregate, unrelated aggregates, or integration authority.
- A checkpoint is valid but its suffix is missing, duplicated, reordered, unknown-version, or over budget.
- The latest checkpoint is corrupt while an earlier compatible checkpoint exists.
- A scan cursor becomes stale during restore and must restart without skipping or duplicating events.
- A legacy SQLite file is renamed, copied, nested, ignored by Git, symlinked, or accompanied by partial current state.
- A build tag, generated file, subprocess, reflection, or test helper makes a forbidden production edge reachable.
- A flag, environment variable, or configuration field tries to select a backend or enable migration.
- External state changes after an effect observation but before compensation.
- A reviewer is the implementation ActorID under a different ProcessID.
- An expired process resumes direct writes in its quarantined workspace.
- The memory projection is lost while the durable owner remains healthy, or the durable owner restarts while clients remain alive.
- Artifact or policy bytes change after a candidate, review, acceptance, or integration decision.
- Evidence publication fails after the product fact is durable.

## Domain Language

- **Authority capability**: A bounded Spec Kitty-owned service that alone may validate and change one category of mission state.
- **Control journal**: Ordered product facts and recorded operation results from which authoritative projections rebuild.
- **Memory-primary**: Current state is served from one in-memory owner for speed while authority transitions are durable before acknowledgement; it does not mean memory-only.
- **Runtime epoch**: Durable identity of one admitted owner lifetime; restart creates a new epoch and makes predecessor authority stale.
- **WorkRevision**: One immutable generation of the executable work graph.
- **Historical state**: Control data produced by the qualified Phase 1–3 SQLite implementation; it remains evidence, not production input.
- **Offline disposition**: An explicit refusal explaining how unsupported historical state must be handled outside production runtime.
- **Structural absence**: A prohibited implementation is unreachable and absent from source, dependencies, binaries, install, configuration, fallback, and migration surfaces—not merely disabled.
- **Playback**: Pure reduction of recorded product facts into projections with no external calls or durable writes.
- **Observation**: A bounded attributed fact read from another authority; it never grants Spec Kitty authority by itself.

Avoid “database row,” “transaction table,” “backend mode,” “migration
fallback,” or “ticket status” as domain authority vocabulary.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Accepted M1 entry tuple | As a program operator, bind M2 admission to the exact accepted M1 source, sidecar, protocol, lifecycle, evidence manifest, archive, and human decision so no mutable label can substitute. | High | Open |
| FR-002 | Single production authority | As an operator, retain exactly one writable owner for every mission-state category so no store, tracker, process, workspace, or transport becomes a second truth. | High | Open |
| FR-003 | Memory-primary restoration | As an operator, rebuild current authority projections from the latest compatible checkpoint plus journal suffix so loss of memory does not lose mission state. | High | Open |
| FR-004 | Atomic authority publication | As a concurrent caller, publish each accepted command's complete events, result, heads, checkpoint metadata, and effect intents as one conditional durable mutation before success. | High | Open |
| FR-005 | Stable operation replay | As a retrying caller, return the recorded result for the same OperationID and request digest with no duplicate transition or effect. | High | Open |
| FR-006 | Idempotency conflict | As an operator, refuse reuse of an OperationID with different canonical input before mutation. | High | Open |
| FR-007 | Mission lifecycle capability | As an operator, preserve mission creation, transitions, pause/resume, decisions, failure, cancellation, and completion as versioned events and typed refusals. | High | Open |
| FR-008 | Artifact revision capability | As a maintainer, preserve artifact identity, revision, digest, provenance, and freshness checks across restart and concurrent updates. | High | Open |
| FR-009 | WorkRevision finalization | As a scheduler, finalize one immutable graph generation with pinned work, dependencies, criteria, provider observations, and content digests. | High | Open |
| FR-010 | WorkRevision reconciliation | As an operator, record continue, supersede, invalidate, or fork semantics and prevent stale generations from executing or crossing later gates. | High | Open |
| FR-011 | Run and lease authority | As an agent host, preserve actor/process/work/generation/epoch/fence coordinates and reject stale or impersonated run, lease, heartbeat, expiry, and outcome operations. | High | Open |
| FR-012 | Lease-scoped workspaces | As a concurrent agent, receive a distinct workspace per lease and a fresh workspace on replacement while expired workspaces remain quarantined evidence. | High | Open |
| FR-013 | Candidate handoff | As an implementer, bind handoff to exact repository observations, contributor ActorIDs, graph generation, and current authority. | High | Open |
| FR-014 | Independent review | As a reviewer, bind criterion evidence and verdict to one candidate and an ActorID independent of all contributors unless explicit mission policy permits otherwise. | High | Open |
| FR-015 | Acceptance decisions | As an operator, cite current policy, work/artifact revisions, reviews, waivers, and candidates and refuse stale evidence. | High | Open |
| FR-016 | Serialized integration | As a repository owner, serialize target-ref publication, preserve conflicts as candidates/repair work, and record observed Git outcomes without trusting caller claims. | High | Open |
| FR-017 | External-effect intent | As an operator, precede every provider mutation with a durable attributable intent and stable operation identity. | High | Open |
| FR-018 | Attribution-safe recovery | As an operator, compare provider revision, value, and operation marker before retry or compensation so human changes are never overwritten blindly. | High | Open |
| FR-019 | Query-only projections | As a client, query and watch mission/work/operation/progress state without minting authority or durable mutations. | High | Open |
| FR-020 | Runtime epoch replacement | As an operator, admit a fresh durable epoch only after byte recovery, product restore, and validation, invalidating all predecessor capsules. | High | Open |
| FR-021 | Checkpoint precedence | As an operator, choose the newest valid compatible bounded checkpoint plus suffix, then bounded full replay, then refusal without skipped history. | High | Open |
| FR-022 | Side-effect-free playback | As an auditor, reduce only recorded facts during playback with zero external or durable-write adapters. | High | Open |
| FR-023 | Behavior inventory parity | As a maintainer, map every frozen Exact or Strengthen behavior to a capability and proof while rejecting inherited Python/Markdown structure that is not product behavior. | High | Open |
| FR-024 | Refusal ordering parity | As a caller, retain defined precedence for stale identity, generation, policy, candidate, repository, and input failures before side effects. | High | Open |
| FR-025 | Per-family crash recovery | As an operator, exercise before/after publication faults and ambiguous responses for every authority-changing event family. | High | Open |
| FR-026 | Historical-state refusal | As an operator, receive a typed offline disposition for identified or suspected historical SQLite roots before current authority is created or mutated. | High | Open |
| FR-027 | Split-root refusal | As an operator, refuse mixed current/historical, shared, symlinked, foreign-owner, or ambiguous control roots rather than choose an authority. | High | Open |
| FR-028 | No automatic importer | As a maintainer, expose no production command, startup hook, library, helper, or subprocess that translates historical SQLite state into current authority. | High | Open |
| FR-029 | No dual write or read fallback | As an operator, use the sole current authority for every mutation and query, with no shadow write, comparison read, rescue read, or fallback. | High | Open |
| FR-030 | No backend selection | As a release reviewer, expose no production flag, environment variable, config, default, build tag, or runtime probe that selects SQLite or changes the authority substrate. | High | Open |
| FR-031 | Structural source absence | As a reviewer, reject every direct, indirect, aliased, generated, tagged, reflective, subprocess, or copied production SQLite edge. | High | Open |
| FR-032 | Structural artifact absence | As a reviewer, find no production SQLite implementation in module/transitive graphs, SBOMs, binaries, clean installs, help/config surfaces, or runtime probes. | High | Open |
| FR-033 | Capability-shaped replacement | As an architect, organize persistence around consumer-owned authority ports and atomic/refusal contracts rather than keys that transliterate old tables. | High | Open |
| FR-034 | Bounded diagnostics | As an operator, receive typed bounded startup, recovery, refusal, and ambiguity diagnostics with immutable identities and no secret or unbounded payload. | Medium | Open |
| FR-035 | Source-bound M2 evidence | As a program reviewer, bind final evidence to source/tree, dependencies, binaries, contracts, behavior inventory, test census, mutants, installation, and authority-family results. | High | Open |
| FR-036 | Successor contract publication | As the owner of M3/M4, consume stable GapDB-backed domain ports, event families, recovery semantics, and structural-absence evidence without storage internals. | Medium | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Atomic fault reliability | Across at least 100 repetitions for each authority-changing family and applicable cut, zero accepted commands expose partial state and every committed operation has exactly one result. | Reliability | High | Open |
| NFR-002 | Idempotency reliability | Across at least 100 lost-response retries per family, identical identities/digests create zero duplicate transitions/effects; changed digests refuse in 100% of trials. | Reliability | High | Open |
| NFR-003 | Concurrent authority | Across at least 100 two-agent trials, independent work progresses concurrently while every observation has at most one current authority per WorkRevision/work item. | Concurrency | High | Open |
| NFR-004 | Same-aggregate contention | Across at least 100 contention trials per mutable aggregate family, exactly one incompatible update wins and all losers receive typed stale/conflict outcomes. | Concurrency | High | Open |
| NFR-005 | Deterministic restore | Across at least 100 restore/playback comparisons covering every event family, canonical projection bytes and digests match in 100% of trials. | Determinism | High | Open |
| NFR-006 | Zero-I/O playback | Every playback qualification reports exactly zero external adapter calls and zero durable writes. | Safety | High | Open |
| NFR-007 | Epoch freshness | Across 100 clean/unclean restarts, every ready owner lifetime has a unique epoch and 100% of predecessor capsules refuse. | Recovery | High | Open |
| NFR-008 | Paged recovery | At least 10,000 accepted operations restore with zero missing/duplicates; every stale traversal restarts a complete bounded scan. | Recovery | High | Open |
| NFR-009 | Recovery time | On the declared reference machine, recovery from the latest checkpoint plus 10,000 subsequent operations completes within 2 seconds at p95 across at least 30 runs. | Performance | High | Open |
| NFR-010 | Query latency | Under the declared bounded local workload, current-state Get/List queries complete within 2 milliseconds p95 across at least 10,000 observations. | Performance | Medium | Open |
| NFR-011 | Durable mutation latency | Under the declared bounded local workload, accepted durable authority mutations complete within 8 milliseconds p95 across at least 10,000 observations. | Performance | Medium | Open |
| NFR-012 | Budget enforcement | One hundred percent of over-budget keys, values, events, batches, checkpoints, scans, watches, diagnostics, and evidence refuse before publication or unbounded allocation. | Resource safety | High | Open |
| NFR-013 | Version refusal | One hundred percent of unknown/incompatible event, reducer, checkpoint, ordering, protocol, policy, or digest fixtures refuse readiness. | Compatibility | High | Open |
| NFR-014 | Structural absence | Automated checks report zero production SQLite edges across all declared surfaces, with at least one killing mutant for every checked surface. | Architecture | High | Open |
| NFR-015 | Behavior traceability | One hundred percent of frozen Exact and Strengthen inventory entries map to a capability, test, and final evidence reference. | Quality | High | Open |
| NFR-016 | Refusal fidelity | Every frozen refusal scenario retains its typed outcome and zero extra authority/external mutations in 100% of comparison trials. | Correctness | High | Open |
| NFR-017 | Race safety | All authority, journal, projection, restart, checkpoint, sidecar, and composition packages pass a bounded race census; stress cases have separate bounded profiles. | Concurrency | High | Open |
| NFR-018 | Evidence completeness | Final qualification covers 100% of declared capability/event families, artifacts, tests, mutants, and install surfaces with verified digests and no unexplained substitution. | Auditability | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Exact M1 admission | M2 consumes only `docs/programs/evidence/m1-adoption.json` and archive tag `m1-gapdb-evidence-v1`; identity drift requires a new decision. | Governance | High | Open |
| C-002 | One host authority | One control store belongs to one machine/filesystem authority; remote clients use application capabilities and never open it directly. | Architecture | High | Open |
| C-003 | Public sidecar boundary | Production uses the admitted project-local public client/Unix-socket sidecar contract and never imports owner internals or discovers an arbitrary executable. | Integration | High | Open |
| C-004 | Kernel purity | Kernel/domain packages import no GapDB, SQLite/SQL, Beads, Git implementation, filesystem allocation, transport, MCP, or ecosystem acceptance provider. | Architecture | High | Open |
| C-005 | Consumer-owned ports | Application capabilities own commands, queries, events, identities, policies, and persistence contracts; adapters implement inward-owned ports. | Architecture | High | Open |
| C-006 | No table translation | Old tables, columns, migrations, and constraints may inform behavior inventory only; they do not define event families, keys, packages, or aggregate ownership. | Design | High | Open |
| C-007 | No compatibility path | There is no production SQLite importer/exporter, dual-write interval, fallback, comparison mode, hidden migration, or backend selector. | Safety | High | Open |
| C-008 | Historical evidence preserved | Qualified Phase 1–3 SQLite source/evidence remain immutable historical records and are not rewritten to imply the new runtime existed earlier. | Governance | Medium | Open |
| C-009 | Tracker authority bounded | Tracker state or assignment never grants execution, review, acceptance, or integration authority. | Product law | High | Open |
| C-010 | Git authority bounded | Git owns commits, trees, and ref outcomes; observed Git state never substitutes for mission authority or candidate/review evidence. | Product law | High | Open |
| C-011 | Workspace replacement | A replacement lease always receives a fresh workspace; an expired workspace is never transferred. | Product law | High | Open |
| C-012 | Reviewer independence | Independence is evaluated by ActorID, not ProcessID; same-actor review is forbidden unless explicit mission policy says otherwise. | Product law | High | Open |
| C-013 | Playback purity | Playback accepts observations as data and has no capability to call external or persistence adapters. | Safety | High | Open |
| C-014 | No transport expansion | API 2.0, normal CLI composition, Team Kitty, and MCP behavior remain later missions except for preserving projectable contracts. | Scope | High | Open |
| C-015 | No execution expansion | Real multi-agent/Beads/Git production composition remains M3; M2 uses bounded adapters and existing contract harnesses. | Scope | High | Open |
| C-016 | No acceptance expansion | Stack-neutral providers remain M4; M2 preserves acceptance state and evidence identities only. | Scope | High | Open |
| C-017 | Fail closed | No force-ready, salvage, ignore, repair-in-place, or automatic authority-selection waiver exists for uncertain control roots. | Safety | High | Open |
| C-018 | Rollback before release | If any M2 gate fails, reject/revert the unshipped composition as a unit; never reactivate SQLite as a production fallback. | Delivery | High | Open |

### Key Entities

- **Authority Aggregate**: One Spec Kitty-owned state category, identity, current logical revision, reducer version, and event history.
- **Control Event**: A bounded versioned fact carrying project, aggregate, operation, epoch, schema, reducer, order, payload, and observations.
- **Operation Result**: Canonical durable result for one OperationID and request digest, including refusal or recovery outcome.
- **Runtime Epoch**: One admitted owner lifetime and predecessor boundary.
- **Checkpoint**: Validated projection bound to an exact journal prefix, heads, registries, epoch, and digest.
- **WorkRevision**: One immutable executable-graph generation plus reconciliation lineage.
- **Lease Capsule**: Current actor, process, work, generation, epoch, fence, workspace, and expiry coordinates.
- **Candidate**: Immutable repository evidence and contributor attribution created by handoff.
- **Review Verdict**: Candidate-bound criterion evidence, reviewer ActorID, policy context, and verdict.
- **External Effect Record**: Intent, attempt, observation, attribution, and reconciliation/compensation outcome.
- **Historical State Disposition**: Evidence that a root is unsupported, ambiguous, mixed, corrupt, or requires offline handling.
- **Qualification Manifest**: Source-bound census and digests proving behavior, recovery, structural absence, installability, and mutants.

## Dependencies and Assumptions

- M1 is accepted by `docs/programs/evidence/m1-adoption.json`; its remote archive was independently reverified before mission creation.
- The Phase 1–3 behavior inventory and capability contracts are the behavioral baseline; their structure and SQL mechanisms are not parity requirements.
- Drift in the admitted GapDB tuple stops M2 for explicit reconciliation.
- The first production boundary is one machine/filesystem authority.
- Historical-state conversion may require a separate offline tool later; M2 neither promises nor embeds one.
- M1 race, fault, mutant, and evidence machinery can gain a distinct M2 census without rewriting M1's immutable verdict.

## Out of Scope

- Importing or converting historical SQLite control data.
- A user-selectable storage backend or compatibility mode.
- Distributed multi-machine store ownership.
- Real-agent/Beads/Git production composition beyond bounded contracts (M3).
- Stack-neutral acceptance providers (M4).
- API 2.0, progress transport, normal CLI packaging, Team Kitty expansion, or MCP Tasks (M5/M7).
- Requalifying or rewriting the accepted M1 evidence tuple.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A complete two-item mission reaches the same terminal facts across uninterrupted and restart-after-every-phase execution with zero duplicate authority transitions.
- **SC-002**: Every authority capability can create, mutate, query, checkpoint, restore, and refuse invalid transitions using one durable history and current projection.
- **SC-003**: At least 100 repetitions of each authority-family crash/ambiguity cut yield zero partial accepted states and exactly one result per committed operation.
- **SC-004**: At least 100 concurrent two-agent trials preserve parallel progress and at most one current authority per work item/generation.
- **SC-005**: At least 100 clean/unclean restart trials reconstruct byte-identical projections and reject every predecessor authority identity.
- **SC-006**: Playback performs exactly zero external calls and zero durable writes in every qualification run.
- **SC-007**: Every frozen Exact or Strengthen behavior has a passing observable proof and no entry is unmapped.
- **SC-008**: Every historical, mixed, malformed, or ambiguous root refuses before current authority changes and returns one actionable disposition.
- **SC-009**: Source, dependency, build, install, configuration, fallback, migration, and runtime inspections find zero reachable legacy implementations, and every prohibited-edge mutant is killed.
- **SC-010**: On the declared reference machine, 10,000-operation recovery completes within 2 seconds p95 over at least 30 runs.
- **SC-011**: On the declared workload, current-state queries complete within 2 milliseconds p95 and durable mutations within 8 milliseconds p95 across at least 10,000 observations each.
- **SC-012**: Final evidence accounts for 100% of authority/event families, artifacts, behaviors, races, faults, mutants, clean-install surfaces, and digests without unexplained substitution.
- **SC-013**: Independent review finds zero unresolved critical/high findings in authority ownership, recovery, refusal parity, structural absence, or evidence integrity before acceptance.
