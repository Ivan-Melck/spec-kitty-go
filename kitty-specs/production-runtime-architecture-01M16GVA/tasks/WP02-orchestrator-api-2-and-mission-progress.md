---
work_package_id: WP02
title: Orchestrator API 2.0 and Mission Progress
dependencies:
- WP01
requirement_refs:
- C-006
- C-007
- C-008
- C-017
- FR-010
- FR-011
- FR-012
- FR-013
- FR-014
- FR-023
- NFR-007
- NFR-010
- NFR-014
planning_base_branch: feat/production-runtime-architecture
merge_target_branch: feat/production-runtime-architecture
branch_strategy: Planning artifacts for this mission were generated on feat/production-runtime-architecture. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/production-runtime-architecture unless the human explicitly redirects the landing branch.
subtasks:
- T007
- T008
- T009
- T010
- T011
- T012
history: []
agent_profile: architect-alphonso
authoritative_surface: docs/
create_intent:
- docs/ORCHESTRATOR_API_2_0.md
execution_mode: code_change
model: ''
owned_files:
- docs/ORCHESTRATOR_API_2_0.md
role: architect
tags: []
tracker_refs: []
---

# Work Package Prompt: WP02 — Orchestrator API 2.0 and Mission Progress

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `architect-alphonso`
- **Role**: `architect`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Publish the process-complete, transport-neutral Orchestrator API 2.0 design, including native asynchronous operations and revisioned mission progress. The document must be implementable without reading Python CLI structure and must make unsafe or lossy compatibility behavior explicitly refuse.

Implementation command:

```bash
spec-kitty agent action implement WP02 --agent codex
```

## Context

WP01 supplies the accepted authority vocabulary. Use the mission-local `contracts/orchestrator-api-2.0.md`, `contracts/progress.schema.json`, and data-model operation/progress sections as the normative detailed inputs. API 1.3 is an outer compatibility contract consumed by existing orchestrators; it does not define API 2.0 commands.

The same application capabilities must serve local CLI, a host service, Team Kitty, CI, dashboards, and optional transports. Clients receive opaque identities and legal actions. They do not receive raw GapDB access, workspace transfer, Git mutation, lease minting, or evidence-manufacturing capabilities.

## Branch Strategy

Use only the WP02 lane allocated from `lanes.json`, based on accepted WP01. Merge accepted work into `feat/production-runtime-architecture`. Own only `docs/ORCHESTRATOR_API_2_0.md`. Do not edit the API 1.3 document; WP07 owns its historical banner and disposition.

## Interface laws

1. Every mutation is a named domain operation with version, OperationID, canonical digest, actor/process, explicit scope, and concurrency preconditions.
2. Same operation and digest replays; same operation and different digest conflicts before effects.
3. Generic `transition`, lane mutation, force-success, raw-event append, and caller-created authority are absent.
4. Queries return observed journal revision and projection freshness and do not silently repair.
5. Native operations outlive transport requests and remain independent of MCP.
6. Cancellation is cooperative intent, not rollback or lease revocation.
7. Progress is graph position plus blockers and next actions; percentage is optional and explained.
8. Watches resume after an explicit application revision and use bounded backpressure/reset behavior.

### Subtask T007: Publish envelopes and command families

Define request/result/diagnostic envelopes and enumerate the complete command families:

- mission definition and lifecycle;
- tracker observation, WorkRevision finalization, and drift reconciliation;
- decisions;
- execution, leases, workspaces, and run observations;
- candidate handoff, independent review, acceptance, and integration;
- qualification claims/bindings/evidence;
- checkpoint, recovery, reconciliation, playback, and terminal export.

For each family, state required identities, authority checks, idempotency, durable boundary, external-effect relationship, and meaningful refusal classes. Avoid endpoint bikeshedding; specify semantic operations strongly enough for later Go types and multiple transports.

### Subtask T008: Specify native asynchronous operations

Document `accepted`, `working`, `input_required`, `cancellation_requested`, `completed`, `failed`, and `cancelled` with legal transitions and races. An input request references a durable DecisionID and authorization policy. Disconnect never implies cancel. Completion may beat cancellation. Operational-result retention never erases mission history or evidence.

Give examples for a qualification run, mission drive-until-boundary, integration attempt, and recovery/reconciliation operation. Distinguish a successful domain refusal returned as a typed result from a protocol/server execution failure.

### Subtask T009: Specify progress snapshots and watches

Publish the required `MissionProgress` fields and a complete bounded JSON example conforming to the mission-local schema. Include phase, WorkRevision generation, work counts, active authority without secrets, ready/critical/blocked paths, review/acceptance/integration position, blockers, decisions, drift, typed next actions, runtime epoch, journal revision, checkpoint staleness, and last progress.

Define List/Get/Watch behavior, resume semantics, watch compaction/reset, slow-consumer bounds, authorization, and freshness. Explain why tracker status and Markdown task boxes are projections rather than inputs.

### Subtask T010: Define API 1.3 compatibility mapping

Create a compatibility table by semantic class:

- lossless safe projection;
- safe projection with explicit additional query;
- deprecated but unambiguous named-command mapping;
- unrepresentable and refused;
- unsafe and permanently refused.

Do not promise that generic `mission transition` remains canonical. If an API 1.3 request has exactly one legal API 2.0 command, map it through the normal authority checks. Otherwise return a stable upgrade diagnostic. Compatibility cannot bypass current policy or import inward.

### Subtask T011: Record clients, host boundary, and diagnostics

Show a conservative component diagram in which every client reaches one application seam. Specify discovery/version negotiation, authentication versus ActorID attribution, authorization, retry classes, deadlines, rate/backpressure bounds, and privacy. List stable error classes from the contract and state when retry is safe with the same operation identity, requires new observation/decision, or is forbidden.

Define conformance expectations for CLI parity, remote host isolation, duplicate requests, stale epoch/fence, slow watchers, and reconnect after host restart.

### Subtask T012: Validate completeness

Cross-check every FR-010–FR-014 scenario and every identity in the data model. Verify that all mutation families state identity, authority, idempotency, durable acknowledgement, and failure. Validate the progress JSON example against `contracts/progress.schema.json` using the repository's Go JSON Schema dependency or another checked, non-production tool. Validate Mermaid and local links.

Add a compact “not API authority” table for GapDB revisions, Beads assignments, Git refs, filesystem paths, ProcessID, transport sessions, MCP Tasks, and compatibility commands.

## Expected document structure

Use an outline close to the following so implementers and transport authors can navigate the contract consistently:

1. Status, compatibility, and terminology.
2. Trust and host boundary.
3. Versioned request/result/diagnostic envelopes.
4. Typed mission/work command families.
5. Execution/workspace/candidate/review/integration commands.
6. Qualification, recovery, and administration commands.
7. Query families and freshness.
8. Native operation lifecycle and decision input.
9. Mission progress snapshot and watch protocol.
10. API 1.3 compatibility disposition.
11. Error/retry taxonomy.
12. Conformance and security proofs.

For at least one mission-driving example, show operation IDs, actor/process IDs, expected revisions, durable acknowledgement, a disconnect, resumed observation, input request, terminal result, and progress revisions. Keep the example bounded and transport-neutral.

## Explicit non-goals

- Choosing HTTP, gRPC, Connect, or Unix-socket wire encoding in this mission.
- Generating Go request/response types.
- Implementing a host daemon or CLI.
- Exposing raw CRUD for internal aggregates.
- Preserving every API 1.3 request regardless of safety.
- Defining MCP Tasks; WP04 owns that projection.
- Solving distributed multi-host authority.

## Evidence to leave for review

The implementation notes should include a command-family coverage table against FR-010–FR-014, a diagnostic/retry matrix, a schema-validation result for the progress example, local-link results, and an explicit list of all deliberately refused operations. Review evidence must name the exact document tree being assessed.

## Definition of Done

- One complete API 2.0 document exists and links to WP01's canonical architecture.
- Every legitimate lifecycle family is named; generic mutation is rejected.
- Native operations and progress are transport-neutral and resumable.
- A valid bounded progress example is included.
- API 1.3 has an explicit safe/refuse disposition model.
- CLI, hosted, Team Kitty, CI, dashboard, and optional transports share semantics.
- No production code or API 1.3 historical file changed.

## Reviewer guidance

Attempt to complete a mission using only the documented capabilities. Reject if any step requires reading Beads, Markdown, worktrees, Git, or GapDB directly; if callers can mint authority; if cancellation implies compensation; or if a progress response cannot explain why the critical path stopped and what legal action follows.
