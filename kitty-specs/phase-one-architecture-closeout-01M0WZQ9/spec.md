# Mission Specification: Phase One Architecture Closeout

**Mission Branch**: `feat/phase-one-architecture-closeout`  
**Created**: 2026-08-25  
**Status**: Draft  
**Input**: Harden the completed Phase 1 authority kernel before adding Gapdb, Beads, or external product surfaces.

## Purpose and Scope

Phase 1 proved that two agents can claim, implement, review, recover, and integrate work concurrently without sharing mutable workspaces or confusing tracker state with execution authority. The next provider integration will pressure those boundaries immediately. This closeout mission converts Phase 1's temporary scope restrictions into durable architectural laws, makes the validation loop sustainable in continuous integration, closes the specific regression gaps found in external review, and establishes the smallest governance-provenance and external-contract seams that must exist before persistent state expands.

This mission does not implement Beads, replace the control store with Gapdb, expose an Orchestrator API, add Team Kitty or MCP, or build the full mission engine.

## Domain Language

- **Authority kernel**: Provider-neutral capabilities that decide whether work may run, which workspace a lease owns, whether a handoff or review is admissible, and when integration may occur.
- **Adapter tier**: Replaceable packages that translate an external provider such as Beads into provider-neutral contracts. An adapter may depend on its provider; the authority kernel may not depend on the adapter.
- **Composition root**: The outermost wiring surface allowed to connect kernel capabilities to adapters.
- **Rejecting mutant**: A deliberately introduced architectural or behavioral violation that must make its guard fail and must implicate the mutated rule.
- **Governance context**: The immutable, content-addressed set of configuration, doctrine, profile, and mission-type revisions that governed a run.
- **Projection**: A non-authoritative representation of kernel state for a human or external contract.

## User Scenarios & Testing

### User Story 1 - Add a Provider Without Weakening the Kernel (Priority: P1)

As a maintainer preparing a tracker integration, I can add a provider-specific adapter in an explicitly permitted tier while automated architecture checks reject any provider dependency or provider semantics that leak into the authority kernel.

**Why this priority**: The first Phase 2 commit must not require deleting the strongest architectural guard in the repository.

**Independent Test**: Add a harmless provider-named fixture under the adapter tier and observe acceptance; then introduce the equivalent import from a kernel package and observe a specific rejection.

**Acceptance Scenarios**:

1. **Given** all production packages are classified, **when** a provider adapter imports its provider dependency, **then** the architecture guard accepts that dependency while continuing to inspect the package.
2. **Given** the same provider adapter exists, **when** an authority-kernel package imports it, **then** the guard fails and names the forbidden dependency edge.
3. **Given** a new production package appears outside every declared tier, **when** the guard runs, **then** it fails closed and names the unclassified package.
4. **Given** a guard or implementation satisfies tests but is absent from every production entry path, **when** the focused reachability check runs, **then** the orphan is reported rather than silently accepted.

---

### User Story 2 - Receive Fast, Trustworthy CI Feedback (Priority: P1)

As a contributor, I receive a bounded pull-request gate for every change and a separately scheduled full fault campaign, with evidence that each selected test actually ran and architectural mutants were rejected.

**Why this priority**: A validation campaign that takes more than ten minutes for a routine edit will be skipped, while a flaky first failure teaches contributors to ignore the gate.

**Independent Test**: Run the pull-request gate locally and in CI, verify its recorded test census, and run the full campaign independently without changing which invariants it asserts.

**Acceptance Scenarios**:

1. **Given** an ordinary pull request, **when** CI runs, **then** the short contract, architecture, race-relevant, static, dependency, and build checks finish within the declared budget.
2. **Given** the full fault and soak campaign, **when** its scheduled or manually dispatched workflow runs, **then** it executes the complete scenario set and publishes an inspectable result.
3. **Given** a test selector matches no tests, **when** the gate evaluates its transcript, **then** the gate fails rather than treating the zero-match process exit as success.
4. **Given** the SQLite contention diagnostic test runs alone repeatedly, **when** scheduling variance changes elapsed time, **then** it asserts the diagnostic contract without relying on an unstable minimum wall-clock duration.

---

### User Story 3 - Preserve Authority at the Earliest Refusal Point (Priority: P1)

As a maintainer investigating an inadmissible integration, I can prove that refusal occurs before repository-fence acquisition and that no rejected attempt obtains or mutates repository authority.

**Why this priority**: A late refusal can produce the correct visible error while still acquiring authority it was never allowed to hold.

**Independent Test**: Remove or move the pre-fence refusal in a rejecting mutant and verify that the regression test fails on the acquisition observation.

**Acceptance Scenarios**:

1. **Given** an integration request that is already inadmissible, **when** integration evaluates it, **then** it refuses before acquiring the repository fence.
2. **Given** the early refusal is deliberately removed, **when** the rejecting mutant runs, **then** the test detects the forbidden fence acquisition.
3. **Given** the Phase 1 acceptance policy has no implemented waiver mechanism, **when** current documentation is evaluated, **then** it states that no waiver exists and links a visible erratum for the immutable historical acceptance artifact rather than promising an unavailable bypass.

---

### User Story 4 - Pin the Governance That Authorized a Run (Priority: P2)

As a reviewer, I can identify the immutable governance context that authorized a run without treating each lease as a separate or mutable policy snapshot.

**Why this priority**: Adding provenance after provider-backed runs exist would require a more hazardous migration and could leave historical authority unexplained.

**Independent Test**: Create and reload a run with a governance-context reference, verify digest validation, and confirm every lease resolves through its owning run rather than carrying duplicated mutable revisions.

**Acceptance Scenarios**:

1. **Given** a run is created, **when** its authority record is persisted, **then** it names one immutable governance-context reference containing algorithm-qualified content commitments.
2. **Given** multiple leases belong to the run, **when** their governance is inspected, **then** all resolve to the run-owned context without duplicated doctrine or profile columns.
3. **Given** a stored context has a missing algorithm, blank digest, or altered entry ordering, **when** it is validated, **then** it is rejected as incomplete or inconsistent.
4. **Given** an existing Phase 1 database is opened, **when** the schema is migrated, **then** old records remain readable and the absence of pre-feature provenance is represented explicitly rather than fabricated.

---

### User Story 5 - Preserve a Cheap External Contract Path (Priority: P2)

As an orchestrator maintainer, I can map the kernel's typed commands, results, errors, identities, and authority boundaries onto Orchestrator API 1.3 without requiring external callers to open the control store or infer filesystem placement.

**Why this priority**: The command/result types being stabilized now determine whether Phase 6 is an adapter or a redesign.

**Independent Test**: Review a version-pinned projection document that accounts for every relevant 1.3 command and result category and identifies any unprojectable field as an explicit gap.

**Acceptance Scenarios**:

1. **Given** the current kernel command and result set, **when** it is compared with Orchestrator API 1.3, **then** each contract operation is classified as directly projectable, projectable with translation, intentionally deferred, or a blocking gap.
2. **Given** remote orchestration, **when** the boundary is described, **then** host-owned storage, Git mutation, secret handling, and workspace allocation remain inside the local host.
3. **Given** no external API is implemented in this mission, **when** the scope guard runs, **then** production API/HTTP packages remain absent.

### Edge Cases

- A new package is nested below an allowed directory but belongs to an undeclared tier.
- A provider name appears in documentation or a conformance fixture but not in production kernel code.
- A type asserts that it implements a port but has only test callers.
- A critical guard has a production caller that is removed during refactoring.
- A short-suite selector matches zero tests while the underlying test command exits successfully.
- CI jobs overlap on the same branch or attempt to share mutable caches, temporary directories, or worktrees.
- A run created before governance provenance exists is migrated without a false claim that historical context was known.
- Configuration or governance entries contain the same bytes in a different order or use an ambiguous digest construction.
- External contract terminology resembles an internal type but carries different authority semantics.

## Requirements

### Functional Requirements

| ID | Title | Requirement | Priority | Status |
|----|-------|-------------|----------|--------|
| FR-001 | Layered production classification | Every production package must be classified as authority kernel, provider contract, adapter, composition root, or an explicitly documented auxiliary tier. Unknown production packages must fail closed. | High | Open |
| FR-002 | Directional dependency law | The architecture guard must enforce allowed dependency directions and prohibit authority-kernel imports of provider adapters or provider-specific dependencies. | High | Open |
| FR-003 | Adapter allowance | The provider-adapter tier must be allowed to name and depend on its provider without weakening the kernel-tier prohibition. | High | Open |
| FR-004 | Architectural mutant controls | The suite must include rejecting mutants for a kernel-to-adapter import, an unclassified production root, removal of a load-bearing allow rule, and an adapter dependency that should remain accepted. | High | Open |
| FR-005 | Production reachability | Selected authority guards and concrete port implementations must have an inspectable production call path; a fabricated implementation or guard reachable only from tests must be rejected. | High | Open |
| FR-006 | Tiered validation workflows | The repository must provide a bounded pull-request gate and a distinct full fault/soak campaign with explicit commands, triggers, and result evidence. | High | Open |
| FR-007 | Stable contention diagnostic | The isolated contention test must assert diagnostic kind and a safe upper bound without an empirically unstable minimum-duration assertion. | High | Open |
| FR-008 | Pre-fence refusal proof | Integration tests must prove that known-inadmissible work is refused before repository-fence acquisition and include a rejecting mutant for the missing early check. | High | Open |
| FR-009 | Repository hygiene | Generated local binaries must be ignored, and living documentation must publish a visible erratum stating truthfully that the immutable Phase 1 acceptance record mentions a waiver mechanism that does not exist. | Medium | Open |
| FR-010 | Run-owned governance context | Persistent run authority must support one immutable governance-context reference with deterministic, algorithm-qualified commitments; leases must inherit that reference through the run. | High | Open |
| FR-011 | Forward-compatible migration | The control store must migrate existing databases forward without fabricating historical governance provenance or making existing authority records unreadable. | High | Open |
| FR-012 | Orchestrator 1.3 projection record | A version-pinned technical record must map kernel commands, results, errors, identity fields, and host-only authority boundaries onto Orchestrator API 1.3 and enumerate gaps. | Medium | Open |
| FR-013 | Test-execution evidence | Test selection gates must verify that the intended named tests ran; an empty or mismatched selection must fail. | High | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Pull-request feedback budget | The standard pull-request gate must complete within 8 minutes at p95 on the repository's configured CI runner over ten representative executions. | Performance | High | Open |
| NFR-002 | Isolated timing stability | The contention diagnostic test must pass 20 consecutive isolated executions on the development host without relying on unrelated suite contention. | Reliability | High | Open |
| NFR-003 | Mutant effectiveness | Every declared architectural and authority mutant must be killed in 100% of ten consecutive gate executions and must implicate the mutated rule rather than an unrelated failure. | Test quality | High | Open |
| NFR-004 | Existing invariant preservation | All existing universal authority invariants, including stale-process probes, must pass unchanged in both short and full campaigns. | Reliability | High | Open |
| NFR-005 | Race safety | All changed production packages and their focused integration tests must pass the race detector with zero reported races. | Concurrency | High | Open |
| NFR-006 | Deterministic provenance | Identical governance-context inputs must produce identical commitments in 100 repeated evaluations; changing any committed entry identity, version, digest, or order must change or invalidate the result. | Integrity | High | Open |
| NFR-007 | Migration preservation | Schema migration tests must preserve 100% of pre-migration Phase 1 authority records and distinguish unknown historical provenance from verified provenance. | Data integrity | High | Open |
| NFR-008 | Dependency containment | The mission must add zero production dependencies on Beads, Team Kitty, MCP, HTTP servers, Python runtimes, or Markdown parsers. | Maintainability | High | Open |
| NFR-009 | Full-campaign observability | Every scheduled or manual full campaign must retain a test census, terminal verdict, duration, and failing scenario identity for at least 14 days in CI artifacts. | Operability | Medium | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | No Phase 2 provider | Beads and other production tracker providers remain outside this mission. | Scope | High | Open |
| C-002 | No control-store replacement | Gapdb adoption and removal of the current SQLite adapter remain separate missions. | Scope | High | Open |
| C-003 | No external runtime surface | Orchestrator API, MCP, Team Kitty, hosted sync, and HTTP implementation remain deferred; only the 1.3 projection record is in scope. | Scope | High | Open |
| C-004 | Preserve concurrency laws | No change may weaken lease-scoped workspace isolation, zombie quarantine, fence validation, independent review, compare-before-compensate, or serialized integration. | Architecture | High | Open |
| C-005 | Capability-oriented architecture | New boundaries must remain organized around product capabilities; the donor port's universal ledger, protobuf-first core, SQLite work tracker, and local in-memory worktree lifecycle must not be transplanted. | Architecture | High | Open |
| C-006 | Donor licensing boundary | No source or test code may be copied from the unlicensed donor repository without an explicit compatible license grant; useful concepts must be independently implemented. | Legal/project policy | High | Open |
| C-007 | One-machine authority | A control store remains owned by one machine/filesystem authority; remote actors must not open it directly. | Deployment | High | Open |
| C-008 | No hidden waiver | The mission must not invent a general bypass flag or undocumented waiver path to reconcile obsolete acceptance wording. | Governance | High | Open |
| C-009 | PR-only delivery | Changes must remain on the PR-bound feature branch and must not be pushed or merged directly to remote `main`. | Delivery | High | Open |

### Key Entities

- **Architecture Tier**: A named production-package class with an explicit set of allowed dependency targets.
- **Dependency Edge**: A directed import from one classified production package to another or to an external provider.
- **Reachability Target**: A selected guard or concrete implementation that must have at least one production caller.
- **Governance Context**: An immutable ordered set of context entries plus an algorithm-qualified aggregate commitment.
- **Governance Context Entry**: A kind, stable identity, revision, and content commitment participating in the aggregate context.
- **Run**: The durable execution authority record that owns a governance-context reference.
- **Validation Campaign**: A named set of checks, selection evidence, trigger policy, and retained result.

## Assumptions

- The existing Phase 1 behavior and evidence campaign remain the baseline and are changed only to remove flakiness or split execution tiers without weakening assertions.
- The repository's minimal charter pack is the appropriate governance baseline until a project-specific charter is authored.
- The current external contract target is Orchestrator API 1.3; implementation remains deferred.
- Governance-context provenance records what the host resolved and used to authorize a run. Proving exact bytes delivered to a model is a later, stronger capability.
- Removing the obsolete waiver promise is preferable to adding a bypass before a concrete waiver policy exists.

## Success Criteria

### Measurable Outcomes

- **SC-001**: A provider-specific adapter fixture is accepted while kernel-to-provider and kernel-to-adapter mutants are rejected in all ten repeated architecture-gate runs.
- **SC-002**: An unclassified production package and a test-only shadow implementation are rejected with diagnostics naming the offending package or symbol.
- **SC-003**: The standard pull-request gate meets the 8-minute p95 budget over ten representative executions, while the complete scenario campaign remains available on schedule and manual dispatch.
- **SC-004**: The isolated contention regression passes 20 consecutive runs and the early-refusal mutant is detected before any repository-fence acquisition is recorded.
- **SC-005**: Existing Phase 1 universal invariants, focused race tests, static analysis, dependency verification, formatting, and binary builds all pass.
- **SC-006**: Governance-context commitments are deterministic over 100 identical evaluations, reject incomplete commitments, detect reordered or altered entries, and survive an existing-database migration without data loss.
- **SC-007**: A reviewer can trace every relevant kernel command and result to an explicit Orchestrator API 1.3 projection classification with zero unexplained contract operations.
- **SC-008**: Repository documentation consistently states that no Phase 1 waiver exists, preserves the original mission artifact as immutable history with a linked erratum, leaves no generated binary unignored, and admits no Phase 2 production integration into the dependency graph.

## Out of Scope

- Implementing or vendoring Beads.
- Adopting Gapdb as the production control-store backend.
- Replacing or removing the SQLite adapter.
- Implementing Orchestrator API, MCP, Team Kitty, hosted synchronization, or migration from Python Spec Kitty.
- Building the complete mission language, durable waits, cancellation engine, or external issue projection service.
- Copying implementation code from `spec-kitty-go`.
