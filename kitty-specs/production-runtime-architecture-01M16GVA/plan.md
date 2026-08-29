# Implementation Plan: Production Orchestration Architecture

**Branch**: `feat/production-runtime-architecture` | **Date**: 2026-08-29 | **Spec**: [spec.md](spec.md)

**Input**: Ratify the Go-native production architecture and decompose delivery into evidence-backed missions.

## Summary

This documentation mission consolidates the approved redesign into one canonical architecture recommendation and an executable program contract. It records current delivery truth, defines the future authority model around GapDB, Beads, Git, host-owned workspaces, Orchestrator API 2.0, progress projections, stack-neutral acceptance, and optional stateless MCP, then decomposes implementation into bounded missions chosen by falsification value rather than package count.

No production behavior changes in this mission. Repository-level documentation will be amended only after the recommendation, contracts, compatibility dispositions, and program graph pass independent review.

## Engineering Alignment

- **Primary invariant**: active mission state is memory-resident, but no authority-changing success is acknowledged until sufficient recovery evidence is durable.
- **State ownership**: Spec Kitty owns mission and execution authority; Beads owns mutable work definition and discussion; Git owns code and ref facts; GapDB is the sole future production control-state substrate.
- **Recovery**: checkpoints accelerate restore, the control journal explains and replays accepted transitions, a restart creates a new runtime epoch, and external facts reconcile rather than reconstruct the whole mission.
- **Application boundary**: Orchestrator API 2.0 is the canonical process-complete command/query/event seam. The normal CLI, Team Kitty, automation, and optional transports project it.
- **Progress**: progress is a revisioned graph projection with phase, generation, work counts, live authority, critical path, blockers, next actions, drift, and last progress—not a manually maintained percentage.
- **Acceptance**: the gate judges stack-neutral claims using revision-pinned provider evidence. Ecosystem commands and parsers remain outside the kernel.
- **MCP**: MCP `2026-07-28` and `io.modelcontextprotocol/tasks` are an optional independently packaged projection of native operations; no workflow semantic is MCP-only.
- **Delivery**: later implementation is a dependency graph of proving missions. Each keeps its own review, acceptance, merge, mission-review, and retrospective gates.

## Technical Context

**Language/Version**: Go 1.26 application repository; Markdown, Mermaid, JSON Schema 2020-12, and strict JSON examples for this documentation mission

**Primary Dependencies**: Existing Phase 1–3 Go domain packages and evidence; GapDB public contract and evidence; Beads 1.2.2 adapter contract; Git; Orchestrator API 1.3 compatibility record; MCP `2026-07-28` and Tasks extension specifications

**Storage**: Documentation in Git for this mission; proposed runtime uses memory-primary GapDB with durable journal/checkpoint evidence and no SQLite production dependency

**Testing**: Requirement traceability, architecture ownership and dependency guards, mutant controls, JSON Schema validation, Mermaid portability, local-link validation, `git diff --check`, and adversarial architecture review

**Target Platform**: Documentation renders on GitHub; proposed first production runtime is Linux/amd64 with one machine/filesystem authority and remote clients behind the application API

**Project Type**: Documentation-oriented architecture and program mission in an existing single-module Go repository

**Performance Goals**: A reviewer locates current status, normative design, next mission, and risks in under five minutes; progress queries are designed for bounded revisioned responses rather than repository scans

**Constraints**: Documentation-only changes; no false shipped claims; preserve lease-scoped workspaces, review independence, and serialized integration; SQLite forbidden from the target production graph; MCP optional

**Scale/Scope**: 28 functional requirements, 15 non-functional requirements, 18 constraints, six principal architecture contracts, one compatibility/migration inventory, and a seven-mission candidate program graph

## Charter Check

The project charter exists as `.kittify/charter/charter.yaml`; no rendered `charter.md` exists. Compact plan context activates five built-in directives.

| Directive | Plan response | Gate |
|---|---|---|
| 001 Architectural integrity | Publish one owner matrix; isolate adapters; require dependency and mutant guards; forbid generic transition and cross-boundary authority. | Pass |
| 010 Specification fidelity | Map every FR/NFR/constraint to an implementation concern, planned artifact, and validation method. | Pass |
| 024 Locality of change | Limit this mission to canonical documentation, contracts, and mission metadata; runtime code remains untouched. | Pass |
| 028 Search discipline | Use `rg`/`rg --files` inventories for current claims, package boundaries, and documentation conflicts. | Pass |
| 030 Test and typecheck quality | Validate schemas, diagrams, links, architecture tests, formatting, and review evidence before merge. | Pass |

The design adds no unjustified charter violation. PR-bound changes land on `feat/production-runtime-architecture` and are reviewed before a separate PR into `main`.

## Project Structure

### Planning artifacts

```text
kitty-specs/production-runtime-architecture-01M16GVA/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── program-manifest.json
├── quickstart.md
├── checklists/
│   └── requirements.md
└── contracts/
    ├── acceptance-evidence.schema.json
    ├── control-journal.md
    ├── mcp-2026-07-28-projection.md
    ├── orchestrator-api-2.0.md
    ├── program-manifest.schema.json
    └── progress.schema.json
```

### Repository documentation affected during implementation

```text
README.md
docs/
├── TECHNICAL_PROJECT_PLAN.md
├── PRODUCTION_ORCHESTRATION_ARCHITECTURE.md   # new canonical recommendation
├── PRODUCTION_RUNTIME_PROGRAM.md              # new mission dependency roadmap
├── ORCHESTRATOR_API_1_3_PROJECTION.md         # retained compatibility record
├── PYTHON_BEHAVIOR_INVENTORY.md               # retained evidence, linked by disposition
├── MINI_REVIEW_RESOLUTION.md                   # retained evidence
└── adr/
    ├── 0001-phase1-authority-proving-ground-verdict.md
    └── 0002-production-orchestration-architecture.md
```

### Existing production packages inspected, not changed by this mission

```text
internal/
├── mission/                 # mission state, actions, decisions, next
├── workgraph/               # immutable executable generations and reconciliation
├── execution/               # leases, runs, external-effect sagas
├── workspace/               # lease-scoped workspaces and quarantine
├── handoff/                 # immutable candidates
├── review/                  # candidate-bound independent verdicts
├── integration/             # serialized Git integration
├── tracker/                 # provider-neutral contracts
│   ├── beads/               # qualified Beads adapter
│   └── fake/                # tests only
├── controlstore/sqlite/     # current delivered adapter; target removal is later work
├── projection/              # current compatibility projections
└── helpercmd/               # current proving CLI, not the final application boundary
```

**Structure Decision**: The mission owns normative docs and design contracts only. Future packages shown in diagrams are recommendations and are created only by later missions after their proving gates pass.

## Architecture Method

### Current truth before proposal

1. Freeze the exact Phase 1–3 delivered boundary and evidence references.
2. Inventory every current normative statement about SQLite, GapDB, API 1.3, acceptance, MCP, and deferred phases.
3. Classify each statement as retained current truth, amended target law, superseded active guidance, or historical evidence.

### Recommendation synthesis

1. Define the single-owner authority matrix.
2. Define memory/durability, checkpoint/journal, runtime epoch, playback, and reconciliation protocols.
3. Define process-complete API commands, queries, events, native operations, and progress projections.
4. Define acceptance claims, provider resolution, evidence envelopes, waivers, and qualification graph semantics.
5. Define the optional stateless MCP projection and explicit domain/transport identity separation.
6. Define architecture guards that make adapter leakage fail CI.

### GapDB deployment boundary

The proving program uses GapDB's supported public client and a pinned `gapdbd` local sidecar. The adoption tuple binds source revision, binary digest, client/protocol version, lifecycle policy, and signed human decision. The plan does not import GapDB's internal server, assume an unshipped embedding API, or discover an arbitrary binary from `PATH`.

### Program decomposition

1. Inventory retained and replaced production surfaces.
2. Identify the smallest experiment that can falsify each unproven architectural claim.
3. Group only concerns that share one falsification target and one coherent acceptance boundary.
4. Order missions by stable contract dependency; mark truly independent work as parallelizable.
5. Require entry evidence, exit evidence, non-goals, and per-mission governance gates.

## Implementation Concern Map

### IC-01 — Delivery truth and compatibility dispositions

- **Purpose**: Establish exact current/proposed/retired boundaries and prevent historical evidence from becoming a false production claim.
- **Relevant requirements**: FR-001–FR-004, FR-026; NFR-001, NFR-003, NFR-004; C-001, C-014, C-017
- **Affected surfaces**: `README.md`, `docs/TECHNICAL_PROJECT_PLAN.md`, evidence indexes, Phase 3 mission docs, new ADR and recommendation
- **Sequencing/depends-on**: none
- **Risks**: Rewriting history, leaving two normative sources, or claiming future behavior as delivered

### IC-02 — Authority, memory, durability, and recovery

- **Purpose**: Define the product-safe relationship among in-memory projections, durable transition evidence, checkpoints, epochs, playback, and external reconciliation.
- **Relevant requirements**: FR-004–FR-008, FR-023; NFR-002, NFR-010, NFR-011, NFR-014; C-002–C-005, C-018
- **Affected surfaces**: Canonical recommendation, ADR, `data-model.md`, `contracts/control-journal.md`
- **Sequencing/depends-on**: IC-01
- **Risks**: Treating volatile success as authority, replaying side effects, trusting zombie leases, or mistaking GapDB backup for product history

### IC-03 — Complete API, native operations, and progress

- **Purpose**: Define one typed application seam that can drive and explain the full mission lifecycle without generic mutation or storage access.
- **Relevant requirements**: FR-010–FR-014, FR-023; NFR-007, NFR-010; C-006–C-008
- **Affected surfaces**: `contracts/orchestrator-api-2.0.md`, `contracts/progress.schema.json`, recommendation and program docs
- **Sequencing/depends-on**: IC-02
- **Risks**: API vocabulary inheriting Python lanes, progress inferred from Beads, cancellation treated as rollback, or transport-specific semantics entering the kernel

### IC-04 — Stack-neutral acceptance proof system

- **Purpose**: Replace language-shaped gate execution with claims, provider bindings, evidence envelopes, and policy evaluation.
- **Relevant requirements**: FR-015–FR-019, FR-023; NFR-012, NFR-014; C-009, C-010
- **Affected surfaces**: `contracts/acceptance-evidence.schema.json`, recommendation, ADR, program roadmap
- **Sequencing/depends-on**: IC-01; consumes identity conventions from IC-02
- **Risks**: Hidden ecosystem defaults, untrusted human-formatted output, stale evidence reuse, or waivers becoming bypasses

### IC-05 — Optional stateless MCP projection

- **Purpose**: Define MCP `2026-07-28` tools, resources, subscriptions, MRTR, and Tasks as an optional adapter over native operations.
- **Relevant requirements**: FR-020–FR-022; NFR-013; C-011–C-013
- **Affected surfaces**: `contracts/mcp-2026-07-28-projection.md`, recommendation and program roadmap
- **Sequencing/depends-on**: IC-03
- **Risks**: MCP becoming required, protocol sessions carrying authority, task TTL deleting mission history, or cooperative cancellation revoking leases

### IC-06 — Dependency-ordered delivery program

- **Purpose**: Turn architecture into bounded missions with falsification targets, entry/exit evidence, and explicit parallelism.
- **Relevant requirements**: FR-024, FR-025, FR-027; NFR-005, NFR-006; C-015, C-016
- **Affected surfaces**: `docs/PRODUCTION_RUNTIME_PROGRAM.md`, `contracts/program-manifest.schema.json`, `quickstart.md`
- **Sequencing/depends-on**: IC-01–IC-05
- **Risks**: A mega-mission, package-scaffolding progress, circular dependencies, or prematurely freezing mission count

### IC-07 — Adversarial review and publication

- **Purpose**: Attack the complete recommendation, resolve blockers, validate diagrams/links/contracts, and make the next user action obvious.
- **Relevant requirements**: FR-028; NFR-007–NFR-010, NFR-015; SC-001–SC-010
- **Affected surfaces**: Review brief, canonical docs, README index, all mission contracts
- **Sequencing/depends-on**: IC-01–IC-06
- **Risks**: Self-confirming architecture, non-rendering diagrams, weak traceability, or an unreviewable roadmap

## Planned Outputs

| Output | Role | Validation |
|---|---|---|
| `research.md` | Decision and alternative record grounded in current code, GapDB, Beads, MCP, and historical evidence | Every proposed decision has rationale and rejected alternatives |
| `data-model.md` | Conceptual authority, event, operation, progress, acceptance, and program models | Invariants and state transitions are explicit |
| `contracts/control-journal.md` | Resume/playback/reconciliation behavior | Crash walkthrough and side-effect replay refusal |
| `contracts/orchestrator-api-2.0.md` | Process-complete typed application surface | Every mutation names identity, authority, idempotency, and failure |
| `contracts/progress.schema.json` | Bounded canonical mission progress projection | JSON Schema validation and critical-path/blocker examples |
| `contracts/acceptance-evidence.schema.json` | Neutral evidence envelope | Two different provider examples validate against one schema |
| `contracts/mcp-2026-07-28-projection.md` | Optional MCP mapping | No MCP-only capability; Tasks map to native operations |
| `contracts/program-manifest.schema.json` | Future mission dependency/admission contract | Program graph validates and is acyclic |
| `program-manifest.json` | Machine-readable seven-mission candidate program | Schema validation, referential integrity, dependency limit, and acyclicity |
| `quickstart.md` | Reviewer/operator path through the design | Next action found in under five minutes |
| `docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md` | Canonical ratified recommendation | Independent adversarial review and no conflicting active guidance |
| `docs/PRODUCTION_RUNTIME_PROGRAM.md` | Dependency-ordered implementation roadmap | Bounded missions with entry/exit evidence and falsification targets |
| `docs/adr/0002-production-orchestration-architecture.md` | Concise decision and consequences | Links to canonical recommendation and superseded statements |

## Validation Strategy

1. Trace every requirement to at least one concern and planned output.
2. Validate JSON schemas and positive/negative examples with an independent validator.
3. Parse every Mermaid block using the repository's documentation gate; retain text alternatives for unsupported constructs.
4. Validate all changed local links from a clean checkout.
5. Run existing architecture and documentation tests and add mutant controls for new boundary guards during implementation.
6. Verify the repository module graph and current code claims are described honestly; this mission does not assert SQLite removal.
7. Run an adversarial architecture review focused on authority leakage, crash recovery, evidence admissibility, API completeness, optionality, and program size.
8. Resolve all blocker/high findings before ratification; record lower findings as explicit program risks.

## Post-Design Charter Re-check

| Directive | Result |
|---|---|
| Architectural integrity | Single owner matrix, adapter isolation, typed commands, and mutant controls are designed. |
| Specification fidelity | All 28 FRs map to ICs and planned artifacts; task finalization must prove no gaps. |
| Locality of change | Runtime code remains outside this mission; affected active docs are explicitly enumerated. |
| Search discipline | Current-claim and dependency inventories use repository-wide `rg` searches. |
| Test/typecheck quality | Schema, link, Mermaid, architecture, and adversarial gates are explicit. |

No post-design charter violation remains.

## Exit Condition

Planning is complete when all mission-local research, model, contract, and quickstart artifacts are substantive and committed. The next phase may translate IC-01–IC-07 into documentation work packages, but must not merge implementation missions into this mission or modify runtime code.
