# Work Packages: Production Orchestration Architecture

## Mission outcome

Publish one honest, technically precise production architecture and a machine-checkable delivery program without changing runtime behavior. The work packages distinguish delivered Phase 1–3 evidence from the approved target, preserve historical attestations, and make M1—the GapDB control-journal proving ground—the unambiguous next implementation mission.

## Execution and branch contract

- Planning/base branch: `feat/production-runtime-architecture`
- Final merge target: `feat/production-runtime-architecture`
- Execution worktrees are allocated per computed lane from `lanes.json` after finalization.
- WP01–WP07 use `execution_mode: code_change` because repository documentation is shipped product surface; WP08 uses `planning_artifact` for mission-local review records. Runtime Go code and frozen evidence payloads remain out of scope.
- Subtask completion is event-sourced through `spec-kitty agent tasks mark-status`; rows below are references, not checkboxes.

## Work package graph

```text
WP01 Canonical authority record
  ├─ WP02 API 2.0 and progress
  │    └─ WP04 Optional MCP projection
  ├─ WP03 Stack-neutral acceptance
  ├─ WP05 Production mission program
  └─ WP07 Historical dispositions

WP01 + WP02 + WP03 + WP04 + WP05 + WP07
  └─ WP06 Active documentation migration

WP01..WP07
  └─ WP08 Independent adversarial review and publication gate
```

WP02, WP03, WP05, and WP07 can proceed concurrently after WP01. WP04 begins after WP02. WP06 waits for all documents it indexes. WP08 is the terminal independent review.

## Subtask index

| ID | Description | WP | Parallel |
|---|---|---|---|
| T001 | Freeze the delivered/proposed/retired truth inventory | WP01 | |
| T002 | Publish the behavioral preservation and retirement map | WP01 | [P] |
| T003 | Define the complete authority ownership matrix and concurrency laws | WP01 | [P] |
| T004 | Specify memory-primary GapDB durability, recovery, epoch, and playback | WP01 | |
| T005 | Author the canonical architecture and superseding ADR | WP01 | |
| T006 | Validate terminology, diagrams, traceability, and local links | WP01 | |
| T007 | Publish API 2.0 envelopes and typed command families | WP02 | |
| T008 | Specify native asynchronous operation semantics | WP02 | [P] |
| T009 | Specify mission progress snapshots, watches, and examples | WP02 | [P] |
| T010 | Define the API 1.3 compatibility projection table | WP02 | |
| T011 | Record client parity, host authority, diagnostics, and conformance | WP02 | |
| T012 | Validate API/progress coverage against mission-local contracts | WP02 | |
| T013 | Define layered stack-neutral acceptance claims and policy union | WP03 | |
| T014 | Define qualification provider resolution and explicit binding | WP03 | [P] |
| T015 | Define evidence envelopes, freshness, admissibility, and waivers | WP03 | [P] |
| T016 | Define the qualification DAG, reuse, invalidation, and progress | WP03 | |
| T017 | Specify acceptance-kernel isolation and mutant controls | WP03 | |
| T018 | Publish cross-stack examples and validate the evidence contract | WP03 | |
| T019 | Specify optional MCP packaging and dependency direction | WP04 | |
| T020 | Map API capabilities to MCP discovery, tools, and resources | WP04 | [P] |
| T021 | Map MCP Tasks to native operations without identity collapse | WP04 | [P] |
| T022 | Define stateless authorization, subscriptions, resume, and bounds | WP04 | |
| T023 | Define MRTR input and cooperative cancellation race semantics | WP04 | |
| T024 | Publish MCP conformance and core-absence qualification matrix | WP04 | |
| T025 | Translate the candidate manifest into a readable program roadmap | WP05 | |
| T026 | Specify M1 GapDB sidecar, journal proof, and adoption evidence | WP05 | |
| T027 | Specify M2 GapDB-only authority and SQLite eradication proof | WP05 | [P] |
| T028 | Specify parallel M3 production composition and M4 acceptance missions | WP05 | [P] |
| T029 | Specify M5 API, M6 release, and optional M7 MCP missions | WP05 | [P] |
| T030 | Define mission admission, reconciliation, gates, and rollback | WP05 | |
| T031 | Validate graph acyclicity, bounds, critical path, and first experiment | WP05 | |
| T032 | Rewrite README delivery truth and reviewer navigation | WP06 | |
| T033 | Rewrite README architecture, installation, and adoption boundaries | WP06 | |
| T034 | Classify every active technical-plan section by disposition | WP06 | [P] |
| T035 | Replace technical-plan storage, API, acceptance, MCP, and phase guidance | WP06 | |
| T036 | Add canonical indexes and eliminate competing active guidance | WP06 | |
| T037 | Validate active-document claims, Mermaid, links, and commands | WP06 | |
| T038 | Mark API 1.3 as a bounded historical compatibility projection | WP07 | |
| T039 | Add a non-destructive supersession note to ADR 0001 | WP07 | [P] |
| T040 | Publish a disposition index for immutable Phase 1–3 evidence | WP07 | |
| T041 | Classify Python inventory and Mini review findings without rewriting history | WP07 | [P] |
| T042 | Prove frozen payloads are unchanged and historical links remain valid | WP07 | |
| T043 | Build the complete requirement-to-document traceability matrix | WP08 | |
| T044 | Close the normative documentation conflict ledger | WP08 | [P] |
| T045 | Adversarially review authority, concurrency, recovery, and compensation | WP08 | |
| T046 | Adversarially review API, progress, acceptance, and MCP boundaries | WP08 | [P] |
| T047 | Attack program boundedness, delivery honesty, and first-slice value | WP08 | [P] |
| T048 | Record findings, resolutions, residual risks, and ratification verdict | WP08 | |
| T049 | Run the publication gate and verify the next governed action | WP08 | |

## Work Package WP01: Canonical Production Authority Architecture

**Goal**: Establish the single normative architecture and ADR that every later document may summarize but not redefine.

**Priority**: P1

**Independent test**: A reviewer identifies exactly one owner for every state category, distinguishes current delivery from target behavior, traces preserved/retired behavior, and explains crash recovery without finding competing authority.

**Dependencies**: None

**Prompt**: `tasks/WP01-canonical-production-authority-architecture.md`

**Included subtasks**:

T001 Freeze the delivered/proposed/retired truth inventory (WP01)
T002 Publish the behavioral preservation and retirement map (WP01)
T003 Define the complete authority ownership matrix and concurrency laws (WP01)
T004 Specify memory-primary GapDB durability, recovery, epoch, and playback (WP01)
T005 Author the canonical architecture and superseding ADR (WP01)
T006 Validate terminology, diagrams, traceability, and local links (WP01)

**Implementation sketch**: Start from frozen Phase 1–3 evidence and the mission-local ownership/data contracts. Write the canonical recommendation before its concise ADR. Treat GapDB adoption as approved direction but unshipped behavior, retain lease-scoped workspaces and reviewer independence as product laws, and make side-effect-free playback distinct from storage recovery.

**Parallel opportunities**: T002 and T003 can be drafted independently after T001, then reconciled into T005.

**Risks**: Rewriting historical evidence, presenting proposals as shipped, or allowing GapDB, Beads, Git, or a process identity to become product authority.

**Estimated prompt size**: approximately 300 lines.

## Work Package WP02: Orchestrator API 2.0 and Mission Progress

**Goal**: Publish the complete typed application boundary, native asynchronous operations, truthful progress, and safe API 1.3 compatibility behavior.

**Priority**: P1

**Independent test**: Every legitimate mutation and query has explicit identity, authority, idempotency, and failure semantics; progress explains graph position and blockers; no generic transition or ambient-directory authority remains.

**Dependencies**: WP01

**Prompt**: `tasks/WP02-orchestrator-api-2-and-mission-progress.md`

**Included subtasks**:

T007 Publish API 2.0 envelopes and typed command families (WP02)
T008 Specify native asynchronous operation semantics (WP02)
T009 Specify mission progress snapshots, watches, and examples (WP02)
T010 Define the API 1.3 compatibility projection table (WP02)
T011 Record client parity, host authority, diagnostics, and conformance (WP02)
T012 Validate API/progress coverage against mission-local contracts (WP02)

**Implementation sketch**: Project the mission-local API and progress contracts into one public design document. Enumerate named application commands and query families, define durable operations independent of transport, and classify every API 1.3 operation as safe projection or typed refusal.

**Parallel opportunities**: T008 and T009 can proceed in parallel after the envelope vocabulary in T007.

**Risks**: Reintroducing lane-shaped generic mutation, conflating client cancellation with rollback, or making progress a tracker/Markdown percentage.

**Estimated prompt size**: approximately 290 lines.

## Work Package WP03: Stack-Neutral Acceptance Proof System

**Goal**: Publish a language- and ecosystem-neutral acceptance architecture based on claims, explicit providers, bounded evidence, and policy.

**Priority**: P1

**Independent test**: Two materially different providers can satisfy the same claim contract, while stale evidence, missing providers, or unauthorized waivers cannot pass.

**Dependencies**: WP01

**Prompt**: `tasks/WP03-stack-neutral-acceptance-proof-system.md`

**Included subtasks**:

T013 Define layered stack-neutral acceptance claims and policy union (WP03)
T014 Define qualification provider resolution and explicit binding (WP03)
T015 Define evidence envelopes, freshness, admissibility, and waivers (WP03)
T016 Define the qualification DAG, reuse, invalidation, and progress (WP03)
T017 Specify acceptance-kernel isolation and mutant controls (WP03)
T018 Publish cross-stack examples and validate the evidence contract (WP03)

**Implementation sketch**: Separate what must be proven from how local tools prove it. Make stack detection suggestion-only, bind providers by revision, evaluate strict policy union, and define admissible reuse and explicit waiver authority.

**Parallel opportunities**: T014 and T015 can be developed concurrently after T013.

**Risks**: Smuggling Go commands into kernel law, parsing human-formatted output, or allowing missing proof to become an implicit skip.

**Estimated prompt size**: approximately 290 lines.

## Work Package WP04: Optional Stateless MCP Tasks Projection

**Goal**: Publish an independently packaged MCP `2026-07-28` adapter design over API 2.0 with Tasks as native-operation projections.

**Priority**: P2

**Independent test**: Removing the MCP module leaves the full core workflow intact, and task loss/cancellation cannot mutate mission, lease, evidence, or integration authority.

**Dependencies**: WP02

**Prompt**: `tasks/WP04-optional-stateless-mcp-tasks-projection.md`

**Included subtasks**:

T019 Specify optional MCP packaging and dependency direction (WP04)
T020 Map API capabilities to MCP discovery, tools, and resources (WP04)
T021 Map MCP Tasks to native operations without identity collapse (WP04)
T022 Define stateless authorization, subscriptions, resume, and bounds (WP04)
T023 Define MRTR input and cooperative cancellation race semantics (WP04)
T024 Publish MCP conformance and core-absence qualification matrix (WP04)

**Implementation sketch**: Define a one-way dependency from the optional adapter to API 2.0, map tools/resources/tasks precisely, and explicitly prevent session, TTL, task status, or cancellation from becoming domain authority.

**Parallel opportunities**: T020 and T021 can proceed concurrently once T019 fixes the package boundary.

**Risks**: MCP becoming mandatory, protocol lifecycle replacing mission lifecycle, or transport metadata leaking secrets and unbounded payloads.

**Estimated prompt size**: approximately 280 lines.

## Work Package WP05: Dependency-Ordered Production Runtime Program

**Goal**: Publish the seven-mission program, with M1 as the smallest falsification experiment and explicit gates for every successor.

**Priority**: P1

**Independent test**: The manifest and readable roadmap agree, contain no cycles or unknown dependencies, bound every mission to one falsification target, and identify M3/M4 and M6/M7 parallelism.

**Dependencies**: WP01

**Prompt**: `tasks/WP05-dependency-ordered-production-runtime-program.md`

**Included subtasks**:

T025 Translate the candidate manifest into a readable program roadmap (WP05)
T026 Specify M1 GapDB sidecar, journal proof, and adoption evidence (WP05)
T027 Specify M2 GapDB-only authority and SQLite eradication proof (WP05)
T028 Specify parallel M3 production composition and M4 acceptance missions (WP05)
T029 Specify M5 API, M6 release, and optional M7 MCP missions (WP05)
T030 Define mission admission, reconciliation, gates, and rollback (WP05)
T031 Validate graph acyclicity, bounds, critical path, and first experiment (WP05)

**Implementation sketch**: Expand the checked-in manifest into an operator-readable roadmap without changing its semantics. Preserve independent Spec Kitty gates for every implementation mission and state precisely which immutable evidence admits each successor.

**Parallel opportunities**: T027, T028, and T029 can be drafted in parallel after M1 is fixed by T026.

**Risks**: A mega-mission, hidden cross-mission dependencies, treating admission as ticket status, or putting optional MCP on the core release path.

**Estimated prompt size**: approximately 320 lines.

## Work Package WP06: Active README and Technical Plan Migration

**Goal**: Make the active repository entry points tell one honest story and link to the canonical documents rather than retaining SQLite/API 1.3 as future doctrine.

**Priority**: P1

**Independent test**: A new reviewer can find current delivery, approved target, next mission, installation truth, and historical evidence within five minutes, with no competing normative production guidance.

**Dependencies**: WP01, WP02, WP03, WP04, WP05, WP07

**Prompt**: `tasks/WP06-active-readme-and-technical-plan-migration.md`

**Included subtasks**:

T032 Rewrite README delivery truth and reviewer navigation (WP06)
T033 Rewrite README architecture, installation, and adoption boundaries (WP06)
T034 Classify every active technical-plan section by disposition (WP06)
T035 Replace technical-plan storage, API, acceptance, MCP, and phase guidance (WP06)
T036 Add canonical indexes and eliminate competing active guidance (WP06)
T037 Validate active-document claims, Mermaid, links, and commands (WP06)

**Implementation sketch**: Retain exact current build/install instructions and frozen evidence claims while clearly labeling the target. Replace the obsolete future roadmap and active SQLite rationale with links and concise summaries of the ratified architecture.

**Parallel opportunities**: T034 can inventory the technical plan while T032 begins the README truth table; final rewrites remain one lane because both are active entry points.

**Risks**: Claiming GapDB or API 2.0 already ships, breaking current installation instructions, or editing historical evidence to make active docs look consistent.

**Estimated prompt size**: approximately 300 lines.

## Work Package WP07: Historical Evidence and Compatibility Dispositions

**Goal**: Preserve Phase 1–3 evidence and prior decisions verbatim while making their current compatibility and supersession status unmistakable.

**Priority**: P1

**Independent test**: Frozen payload hashes remain unchanged, API 1.3 and ADR 0001 retain their observed facts, and an index explains which statements are historical, compatible, superseded, or still load-bearing.

**Dependencies**: WP01, WP02

**Prompt**: `tasks/WP07-historical-evidence-and-compatibility-dispositions.md`

**Included subtasks**:

T038 Mark API 1.3 as a bounded historical compatibility projection (WP07)
T039 Add a non-destructive supersession note to ADR 0001 (WP07)
T040 Publish a disposition index for immutable Phase 1–3 evidence (WP07)
T041 Classify Python inventory and Mini review findings without rewriting history (WP07)
T042 Prove frozen payloads are unchanged and historical links remain valid (WP07)

**Implementation sketch**: Add contextual banners and a new disposition index outside frozen payloads. Never rewrite observed SQLite or API facts; explain that they remain evidence for exact commits while future guidance is superseded by the new architecture.

**Parallel opportunities**: T039 and T041 can proceed in parallel after the disposition vocabulary is agreed.

**Risks**: Invalidating source-bound evidence, erasing the reason earlier decisions were correct, or letting compatibility vocabulary define API 2.0.

**Estimated prompt size**: approximately 250 lines.

## Work Package WP08: Independent Architecture Review and Publication Gate

**Goal**: Independently attack the complete recommendation, close every normative conflict, prove traceability, and issue a source-specific ratification verdict.

**Priority**: P1

**Independent test**: A Reviewer Renata-profile actor, distinct from the architecture authors, records no unresolved blocker/high finding, maps all 28 FRs, validates all active links/diagrams/schemas, and identifies M1 as the next governed action.

**Dependencies**: WP01, WP02, WP03, WP04, WP05, WP06, WP07

**Prompt**: `tasks/WP08-independent-architecture-review-and-publication-gate.md`

**Included subtasks**:

T043 Build the complete requirement-to-document traceability matrix (WP08)
T044 Close the normative documentation conflict ledger (WP08)
T045 Adversarially review authority, concurrency, recovery, and compensation (WP08)
T046 Adversarially review API, progress, acceptance, and MCP boundaries (WP08)
T047 Attack program boundedness, delivery honesty, and first-slice value (WP08)
T048 Record findings, resolutions, residual risks, and ratification verdict (WP08)
T049 Run the publication gate and verify the next governed action (WP08)

**Implementation sketch**: Review the integrated accepted lineages, not isolated drafts. Use the review brief to attempt falsification, require concrete locations and severities, route fixes back to owning WPs when necessary, and bind the final verdict to exact source and documentation tree identities.

**Parallel opportunities**: T044, T046, and T047 can be researched in parallel within the review lane; the final verdict waits for all findings and fixes.

**Risks**: Self-review, blessing unresolved authority ambiguity, accepting non-rendering documentation, or treating proposed runtime behavior as qualified implementation.

**Estimated prompt size**: approximately 340 lines.

## MVP recommendation

WP01 is the minimum coherent delivery slice because every other document depends on its authority and delivery-truth vocabulary. It is not independently merge-complete for the mission: WP08 remains the ratification gate, and no production implementation mission should start before the full documentation mission terminates.

## Finalization gates

- All 49 subtasks appear exactly once.
- Every FR maps to at least one WP; WP08 independently verifies the complete mapping.
- No WP owns runtime Go code or a frozen evidence payload; repository-documentation WPs still receive isolated normal execution lanes.
- Owned files do not overlap across WPs.
- Every planned-new file appears in `create_intent`.
- All prompt files are flat under `tasks/`, contain the required profile-loading section, and remain under 700 lines.
- Dependency finalization creates `lanes.json` and commits the task artifacts to `feat/production-runtime-architecture`.
