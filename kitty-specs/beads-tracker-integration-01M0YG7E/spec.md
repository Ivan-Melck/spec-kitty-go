# Mission Specification: Beads Tracker Integration

**Mission Branch**: `feat/beads-tracker-integration`  
**Created**: 2026-08-26  
**Status**: Draft  
**Input**: Make Beads the default first-party work catalog through a narrow, provider-neutral adapter while preserving Spec Kitty's exclusive authority over execution, workspaces, review, acceptance, and Git integration.

## Mission Intent

Phase 1 proved that two actors can work concurrently without sharing mutable workspaces and without confusing tracker state with execution authority. This mission replaces the durable fake tracker in one production-shaped vertical slice with Beads while retaining the fake as the provider-neutral reference implementation.

The governing separation is:

```text
Beads:       what work exists, what blocks it, and what humans discuss
Spec Kitty: whether work may run, where it runs, and how it is accepted
Git:         what actually happened
```

Research against Beads v1.2.2 found no supported public Go consumer package: its reusable domain and storage packages are internal. The supported automation surface is the `bd` executable's documented `--json` protocol with `BD_JSON_ENVELOPE=1`, schema version 1. The original project plan's preference for a published Go consumer API is therefore superseded for this release. The adapter must not import Beads internal packages or query its Dolt schema directly.

Beads v1.2.2 provides atomic claim but does not expose an arbitrary compare-and-swap update or conditional compensation command. The adapter must report this capability accurately. It may never claim atomicity for a read-then-write sequence, and it may not clear or rewrite provider state automatically when attribution cannot be proven at the mutation boundary. Such cases become explicit reconciliation conflicts; local execution authority remains valid and inspectable.

## Definitions

- **Authority kernel**: The execution, workspace, handoff, review, integration, workgraph, and control-store capabilities that decide whether an action is allowed.
- **Tracker port**: Provider-neutral types and operations through which application services observe or project work facts.
- **Beads adapter**: Provider-specific translation code under `internal/tracker/beads`; it may invoke `bd` but may not be imported by the authority kernel.
- **ExternalWorkRef**: A stable mapping from a Spec Kitty `WorkID` to a Beads issue ID without making that issue ID the internal identity.
- **Provider revision**: An opaque observation token derived from all provider-owned work fields relevant to the operation, not a promise of provider-side CAS.
- **Operation marker**: Namespaced metadata identifying the Spec Kitty operation responsible for a provider projection.
- **Reconciliation conflict**: A typed, durable result requiring explicit continuation or repair because provider state is unavailable, ambiguous, stale, unsupported, or no longer attributable to an operation.
- **Canonical Beads workspace**: The one configured Beads database location shared by all worktrees for a project.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Safely bind and inspect Beads (Priority: P1)

As a project operator, I can bind Spec Kitty to a compatible local Beads workspace and receive a typed health and capability report without Spec Kitty installing hooks, editing agent instruction files, initializing an unrelated database in each worktree, or parsing human-formatted output.

**Why this priority**: Every later operation depends on knowing that the executable, JSON contract, database location, concurrency mode, and side-effect policy are safe before mutation.

**Independent Test**: Run discovery against temporary compatible, missing, malformed, unsupported-schema, and unavailable `bd` processes from two Git worktrees; verify one canonical binding, exact typed outcomes, and no repository mutation.

**Acceptance Scenarios**:

1. **Given** a configured Beads v1.2.2 executable and canonical stealth-mode workspace, **When** the operator runs provider preflight, **Then** Spec Kitty reports provider version, JSON schema, workspace identity, storage/concurrency mode, supported capabilities, and recovery guidance.
2. **Given** two worktrees of one repository, **When** each resolves the Beads binding, **Then** both resolve the same canonical workspace and observe the same work items.
3. **Given** a missing executable, incompatible provider version, unsupported JSON schema, malformed response, or timeout, **When** preflight runs, **Then** it returns a stable typed diagnostic before any mutation.
4. **Given** a repository without Beads configuration, **When** discovery runs, **Then** it does not run interactive initialization, install Git hooks, or modify `AGENTS.md`, `CLAUDE.md`, or other agent files.

---

### User Story 2 - Use Beads as a portable work catalog (Priority: P1)

As a mission author or agent, I can create and read work descriptions, structured acceptance criteria, blocking dependencies, ready work, visible assignment, and durable comments through provider-neutral ports, with stable Spec Kitty identity mappings and no dependence on Beads-specific workflow concepts.

**Why this priority**: This is the smallest useful replacement for the Markdown-inspired task list and the core product value of the integration.

**Independent Test**: Execute the same catalog/graph/discussion conformance suite against the durable fake provider and a temporary real Beads v1.2.2 database, including concurrent creation and reads from separate worktrees.

**Acceptance Scenarios**:

1. **Given** a provider-neutral work definition, **When** it is created through the Beads adapter, **Then** the issue contains the human brief and acceptance rendering while namespaced metadata preserves the stable `WorkID`, criteria IDs, metadata schema, and originating operation.
2. **Given** two work items and a blocking dependency, **When** the graph is read through the tracker port, **Then** readiness and dependency facts are translated without adopting Beads Molecules, custom statuses, or merge slots.
3. **Given** two processes creating distinct work concurrently, **When** Beads assigns hash-style issue IDs, **Then** both mappings are durable, distinct, and visible from both project worktrees.
4. **Given** an existing mapped issue, **When** an agent adds a durable discussion entry, **Then** the exact comment is visible through both providers and replay with the same operation ID does not duplicate it.
5. **Given** unknown optional fields in a schema-1 response, **When** the adapter decodes it, **Then** the known contract remains usable and the unknown fields are ignored.

---

### User Story 3 - Project assignment without leaking authority (Priority: P1)

As an execution service, I can project visible ownership to Beads around a Spec Kitty run while the execution lease, fencing token, workspace generation, review verdict, and integration decision remain exclusively controlled by Spec Kitty.

**Why this priority**: Assignment is where tracker convenience most strongly pressures the authority boundary and distributed-saga safety.

**Independent Test**: Run the existing start/recovery saga and universal authority invariant suite against an adversarial Beads command harness and a real temporary Beads workspace, injecting slow, unavailable, ambiguous, stale, and human-modified results.

**Acceptance Scenarios**:

1. **Given** a valid Spec Kitty execution lease and unchanged work observation, **When** assignment is projected, **Then** Beads shows the mapped actor and namespaced operation marker while the tracker receives no power to mint or extend the lease.
2. **Given** Beads is slow or unavailable after the local lease commits, **When** start recovery runs, **Then** mission/lane state remains valid, no workspace is transferred, and a typed provider debt or conflict is durable.
3. **Given** Beads applies a mutation but the command result is lost, **When** the operation is retried, **Then** observation of assignee plus operation marker distinguishes replay from conflict without duplicating the external effect.
4. **Given** a human changes assignment after a Spec Kitty projection, **When** recovery considers compensation, **Then** the human assignment remains untouched and an attribution-lost conflict records the observed provider state.
5. **Given** the provider exposes no conditional update capable of proving attribution at mutation time, **When** automatic compensation is requested, **Then** the adapter refuses the mutation and returns an unsupported-safe-compensation diagnostic rather than performing read-then-write compensation.
6. **Given** Beads readiness or status disagrees with the pinned `WorkRevision`, **When** scheduling or acceptance is evaluated, **Then** Spec Kitty uses the pinned graph and records the disagreement diagnostically.

---

### User Story 4 - Operate and qualify the adapter (Priority: P2)

As a maintainer, I can reproduce the supported Beads qualification in CI and locally, diagnose version or database failures, export a backup, and prove that provider-specific code remains outside the authority kernel.

**Why this priority**: A production adapter must survive dependency skew and operational failure without turning a one-time hand-run campaign into permanent folklore.

**Independent Test**: Run bounded PR gates plus an opt-in or scheduled real-provider qualification that installs the checksum-verified pinned Beads binary, uses temporary isolated data, performs export/restore checks, and exercises the architecture mutant controls.

**Acceptance Scenarios**:

1. **Given** a clean checkout, **When** the documented qualification command runs, **Then** it obtains or uses the pinned Beads v1.2.2 binary, verifies its identity, creates only temporary provider data, and emits machine-readable evidence.
2. **Given** provider adapter code imports or names Beads, **When** architecture tests run, **Then** the classified adapter tier is accepted; the same import or provider semantic in any kernel package fails with an actionable error.
3. **Given** an initialized temporary Beads workspace, **When** backup/export qualification runs without network access, **Then** its work, dependencies, criteria metadata, and comments can be recovered or the tool reports a precise unsupported recovery condition.
4. **Given** the full provider fault campaign is too slow for the PR loop, **When** CI runs, **Then** bounded contract and architecture gates run on pull requests and the full real-provider/fault campaign remains scheduled and manually runnable.

### Edge Cases

- `bd` exits nonzero with a JSON error on stderr and unrelated warning text on stderr.
- stdout is empty, truncated, contains multiple JSON values, exceeds the configured size limit, or reports a future schema version.
- the process ignores cancellation, is killed after timeout, or exits after applying a mutation but before returning JSON.
- Beads v2 changes envelope defaults while retaining or changing the declared schema.
- two worktrees point at different Beads directories or one worktree discovers a nested database.
- embedded single-writer mode is selected while two Spec Kitty processes attempt provider writes.
- a user runs `bd` directly between the adapter's observation and mutation.
- two timestamps are equal despite different provider content; revision comparison must still detect the changed canonical content.
- metadata is absent, malformed, owned by a different Spec Kitty project, or contains an unknown newer metadata schema.
- a mapped Beads issue is deleted, renamed, imported with stale timestamps, or rebound to another `WorkID`.
- dependency creation would introduce a cycle or references an unknown external issue.
- a comment or create response is ambiguous and replay occurs after process restart.
- Beads is read-only, its Dolt server is unreachable, or an embedded database lock is contended.
- backup/export succeeds but verification or restore inspection fails.
- actor mapping is missing or maps two durable `ActorID` values to the same human-visible assignee.
- a stale process resumes after its lease expires and continues writing its quarantined workspace; provider state must not grant it renewed authority.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Provider-neutral capabilities | As an application service, I can use catalog, graph, discussion, assignment, health, and export capabilities without importing Beads types. | High | Open |
| FR-002 | Supported automation protocol | As an operator, I can use Beads through `bd --json` with `BD_JSON_ENVELOPE=1` and schema version 1; no human-formatted output is parsed. | High | Open |
| FR-003 | Version negotiation | As an operator, I receive the executable version, JSON schema version, supported version range, and a typed refusal before mutation when incompatible. | High | Open |
| FR-004 | Safe discovery | As an operator, I can discover an explicit canonical Beads workspace without interactive prompts, hook installation, agent-file edits, or implicit per-worktree initialization. | High | Open |
| FR-005 | Canonical workspace identity | As a concurrent project, all linked worktrees resolve one configured Beads workspace identity and divergent resolution fails preflight. | High | Open |
| FR-006 | Capability report | As an application service, I can distinguish read, create, dependency, comment, assignment, atomic-claim, guarded-update, safe-compensation, concurrency, and export capabilities. | High | Open |
| FR-007 | Stable work mapping | As a mission, I retain a stable `WorkID` mapped one-to-one to a Beads ID with project identity, metadata schema, and operation provenance. | High | Open |
| FR-008 | Work catalog | As a mission author, I can create, retrieve, and update provider-owned title, description, design context, and acceptance rendering through the tracker port. | High | Open |
| FR-009 | Structured criteria | As a reviewer, stable criterion IDs, text, verifier kind, required flag, and metadata schema survive a Beads round trip without requiring checkbox mutation. | High | Open |
| FR-010 | Dependency graph | As a scheduler, I can read and write blocking edges and query provider readiness as a diagnostic projection without replacing pinned-graph readiness. | High | Open |
| FR-011 | Durable discussion | As an agent or human, I can append and read comments with operation attribution and idempotent replay behavior. | High | Open |
| FR-012 | Visible assignment | As an execution service, I can project mapped actor ownership and operation metadata without treating Beads assignment or status as an execution lease. | High | Open |
| FR-013 | Opaque revisions | As a saga, I receive a deterministic observation revision over all relevant provider-owned fields so equal timestamps cannot hide content drift. | High | Open |
| FR-014 | Honest mutation guarantees | As a saga, I am never told an arbitrary Beads update is atomic or compare-and-set when the CLI does not provide that primitive. | High | Open |
| FR-015 | Attribution-safe compensation | As recovery, I undo provider state only through a primitive that proves expected revision/value/marker at mutation time; otherwise I preserve provider state and record a conflict. | High | Open |
| FR-016 | Ambiguous-result recovery | As recovery, I can observe post-timeout state and classify applied replay, unchanged retry, external conflict, unavailable, or indeterminate outcomes. | High | Open |
| FR-017 | Typed diagnostics | As an operator, unavailable, timeout, malformed output, schema skew, version skew, workspace mismatch, lock contention, attribution loss, and unsupported safety guarantees have stable error kinds and recovery guidance. | High | Open |
| FR-018 | Adversarial conformance | As a maintainer, I can run the same portable invariants against the fake provider, a dishonest command harness, and real Beads for the capabilities each declares. | High | Open |
| FR-019 | Concurrency policy | As an operator, preflight rejects unsafe concurrent writer use or routes writes through an adapter-owned serialization mechanism whose limits are explicit and tested. | High | Open |
| FR-020 | Backup and diagnostics | As an operator, I can run a non-destructive provider diagnostic and checksum-verifiable export/backup workflow with no network dependency after installation. | Medium | Open |
| FR-021 | Kernel isolation | As an architect, adapter code may depend on the `bd` protocol while no authority-kernel package imports the adapter or contains Beads workflow semantics. | High | Open |
| FR-022 | Universal authority invariants | As a reviewer, every adversarial provider scenario also proves lease uniqueness, fence enforcement, workspace non-transfer, reviewer independence, and serialized integration where applicable. | High | Open |
| FR-023 | Evidence production | As a maintainer, qualification records provider version, binary digest, schema, mode, test selection, result, and source revision in machine-readable evidence. | Medium | Open |
| FR-024 | Documentation correction | As a maintainer, project documentation records that v1.2.2 exposes a JSON CLI contract rather than a public Go consumer API and explains the resulting safety boundary. | High | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Preflight latency | Against a healthy local provider, 95% of 100 sequential preflight operations complete within 1 second after process-start overhead is characterized; the measured distribution and host are recorded. | Performance | Medium | Open |
| NFR-002 | Read latency | Against a warm local provider containing 100 work items, 95% of 200 single-item reads complete within 500 ms on the qualification host. | Performance | Medium | Open |
| NFR-003 | Bounded commands | Every provider invocation has context cancellation, a configurable timeout no greater than 30 seconds by default, bounded stdout/stderr capture, and forced process termination after its grace period. | Reliability | High | Open |
| NFR-004 | Zero authority corruption | Across every injected provider failure, zero scenarios may violate universal local authority invariants or transfer an expired workspace. | Reliability | High | Open |
| NFR-005 | Human-change preservation | Across at least 100 race/fault repetitions involving a human reassignment, zero automatic compensations may overwrite the human's value. | Safety | High | Open |
| NFR-006 | Concurrent identity | Across at least 1,000 concurrent work creations in the supported multi-writer configuration, there are zero Beads ID collisions or duplicate Spec Kitty mappings. | Reliability | High | Open |
| NFR-007 | Protocol robustness | Parser tests cover object, array, error, warning, unknown-field, malformed, truncated, oversized, and future-schema outputs with 100% branch coverage in the protocol decoder package. | Quality | High | Open |
| NFR-008 | Architecture enforcement | CI contains positive and mutant-negative tests proving all kernel-to-Beads dependency paths are rejected and the classified adapter path is accepted. | Maintainability | High | Open |
| NFR-009 | Race safety | All adapter, conformance, saga, and relevant scenario packages pass `go test -race`; no known race finding is waived. | Reliability | High | Open |
| NFR-010 | PR feedback | The bounded pull-request gate completes within 10 minutes on GitHub-hosted runners; longer real-provider and fault campaigns run on schedule and on demand. | Delivery | Medium | Open |
| NFR-011 | Offline operation | After the pinned binary is installed, all catalog, graph, discussion, assignment, health, and export qualification scenarios pass with network access disabled. | Operability | High | Open |
| NFR-012 | Secret hygiene | Diagnostics and evidence contain zero environment secrets, credentials, arbitrary comment bodies, or unredacted command transcripts. | Security | High | Open |
| NFR-013 | Reproducible provider | Automated qualification verifies an allowlisted Beads version and SHA-256 digest before execution and records both in evidence. | Supply chain | High | Open |
| NFR-014 | Portable reference | The fake-provider conformance suite remains runnable without Beads, Dolt, network access, or provider-specific environment variables. | Portability | High | Open |
| NFR-015 | Documentation fidelity | Every documented command is exercised by a documentation or smoke test, and no README claim grants Beads mission, execution, review, or integration authority. | Documentation | Medium | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Authority separation | Beads owns mutable work facts and visible coordination only; Spec Kitty retains mission, run, workspace, review, acceptance, and integration authority. | Architecture | High | Open |
| C-002 | No internal imports | Production code must not import Beads `internal` packages, copy their domain structs into the kernel, or issue direct SQL against the Beads/Dolt schema. | Technical | High | Open |
| C-003 | Pinned initial compatibility | The initial real-provider qualification target is Beads v1.2.2 with JSON envelope schema 1; broader support requires explicit evidence. | Compatibility | High | Open |
| C-004 | No false CAS | Read-before-write plus local locking must not be represented as provider-side compare-and-swap against direct human or external writes. | Safety | High | Open |
| C-005 | One filesystem authority | One Spec Kitty control store and canonical Beads workspace are owned by one machine/filesystem authority; remote agents use a future host API rather than opening them directly. | Deployment | High | Open |
| C-006 | No unsolicited setup | The adapter must not install hooks, edit agent instruction files, run interactive initialization, or enable Beads Git/version-control automation. | Product | High | Open |
| C-007 | Provider portability | The fake provider remains the reference contract; a capability cannot become required solely because Beads happens to offer it. | Architecture | High | Open |
| C-008 | No Beads workflow authority | Molecules, formulas, agent identity, merge slots, custom mission statuses, compaction, and provider-side Git hooks remain outside this mission. | Scope | High | Open |
| C-009 | No external product surfaces | Team Kitty, Orchestrator API serving, MCP, hosted sync, and alternative production trackers remain outside this mission. | Scope | Medium | Open |
| C-010 | No full mission engine | This mission does not replace the existing Markdown task artifact or implement specification, planning, decomposition, acceptance, or migration workflows; it establishes the tracker substrate they will use. | Scope | High | Open |
| C-011 | Gapdb deferred | The existing control store is not migrated to Gapdb in this mission; tracker integration evidence must not be conflated with control-store adoption approval. | Scope | Medium | Open |
| C-012 | Existing laws preserved | Lease-scoped fresh workspaces, stale-process quarantine, actor-level reviewer independence, immutable `WorkRevision`, explicit reconciliation dispositions, and serialized Git integration may not be weakened. | Governance | High | Open |

### Key Entities

- **TrackerBinding**: Project-scoped provider name, canonical workspace identity, executable identity, supported protocol/schema range, concurrency mode, and health state.
- **ProviderCapabilities**: Explicit booleans or typed levels for catalog, graph, discussion, assignment, atomic claim, guarded mutation, safe compensation, export, and concurrent writers.
- **ExternalWorkRef**: `ProjectID`, stable `WorkID`, provider, provider issue ID, mapping metadata schema, and observed provider revision.
- **WorkRecord**: Provider-neutral title, description, design context, structured criteria, dependency refs, visible assignee, comments, and opaque observation revision.
- **ProviderObservation**: Canonical known fields, content-derived revision, operation marker, provider timestamp, and raw compatibility metadata required for safe retry diagnostics.
- **ProviderCommandResult**: Command identity, exit status, bounded stdout/stderr classification, JSON schema, decoded payload, timing, and ambiguity classification.
- **ReconciliationConflict**: Operation, work identity, expected observation, actual observation, provider capability limitation, recommended action, and durable status.
- **QualificationEvidence**: Source revision, provider version/digest, protocol schema, workspace mode, scenario results, timing distribution, and redacted diagnostics.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A real Beads v1.2.2 temporary workspace passes the catalog, dependency, criteria, discussion, assignment-observation, health, and export scenarios supported by its declared capabilities.
- **SC-002**: The durable fake provider and dishonest command harness pass the same universal conformance assertions without importing or emulating Beads workflow semantics.
- **SC-003**: Two processes operating from separate Git worktrees observe one canonical Beads workspace, create distinct mapped work concurrently, and never acquire two authoritative Spec Kitty leases for the same work.
- **SC-004**: One hundred adversarial repetitions covering slow, unavailable, timeout-before-apply, timeout-after-apply, stale response, malformed response, direct human reassignment, and provider lock contention produce zero local authority invariant violations.
- **SC-005**: Automatic compensation never changes provider state unless the adapter can prove expected revision, value, and operation marker at the mutation boundary; v1.2.2's missing primitive yields a durable safe refusal.
- **SC-006**: Architecture mutant controls prove `internal/tracker/beads` is allowed to contain provider protocol knowledge while every kernel-to-adapter import or Beads semantic reference fails CI.
- **SC-007**: All production and test code parses only the documented JSON envelope/error contract; repository search and protocol tests find zero human-output parsing paths.
- **SC-008**: Incompatible executable versions and JSON schemas fail preflight before mutation with stable diagnostics and documented recovery instructions.
- **SC-009**: The pull-request gate finishes within 10 minutes, and the scheduled/manual real-provider qualification emits reproducible evidence including Beads version, binary digest, schema, mode, and source revision.
- **SC-010**: Full tests, race detector, vet, static analysis, dependency verification, vulnerability scan, formatting, binary builds, and applicable provider qualification gates pass with no unexplained waiver.
- **SC-011**: README and technical project plan accurately describe the v1.2.2 JSON CLI integration, provider capability limits, canonical workspace rule, and the continuing authority separation.
- **SC-012**: An operator can remove or disable Beads and still run the provider-neutral fake conformance suite and inspect all existing Spec Kitty mission/control state.

## Assumptions

- Beads v1.2.2 remains the first qualification target even though later releases may restore features accidentally present in v1.2.0/v1.2.1; support follows tested contracts, not version-number optimism.
- A local `bd` binary is an external runtime dependency and is not required for ordinary fake-provider unit tests.
- Beads embedded mode is single-writer. Concurrent writes require Beads server mode or explicit adapter serialization; serialization protects Spec Kitty processes only and does not create CAS against direct external writers.
- Provider timestamps assist diagnostics but are insufficient as sole revisions because equal timestamps can accompany different content.
- Human-visible assignment may remain stale after a safe compensation refusal. This is preferable to overwriting a human change or claiming an unavailable atomic guarantee.
- The existing SQLite control-store package remains in place for this mission; Gapdb adoption is a separate signed decision.

## Non-Goals

- Replacing the Spec Kitty control store with Beads, Dolt, or Gapdb.
- Making Beads issues, readiness, statuses, claims, or comments authoritative for mission progression.
- Implementing Beads Molecules, formulas, agent identities, merge slots, gates, convoys, wisps, or Git synchronization.
- Supporting every Beads release or storage topology in the first adapter.
- Parsing legacy non-envelope JSON or any human-formatted `bd` output in production.
- Implementing GitHub Issues, Jira, Team Kitty, Orchestrator API, MCP, or bidirectional mirror synchronization.
- Replacing the full mission task artifact before the Phase 3 mission-method work.
- Solving distributed multi-machine control-store access.
