---
work_package_id: WP04
title: Optional Stateless MCP Tasks Projection
dependencies:
- WP02
requirement_refs:
- C-011
- C-012
- C-013
- C-017
- FR-020
- FR-021
- FR-022
- FR-023
- NFR-010
- NFR-013
- NFR-014
planning_base_branch: feat/production-runtime-architecture
merge_target_branch: feat/production-runtime-architecture
branch_strategy: Planning artifacts for this mission were generated on feat/production-runtime-architecture. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/production-runtime-architecture unless the human explicitly redirects the landing branch.
subtasks:
- T019
- T020
- T021
- T022
- T023
- T024
history: []
agent_profile: architect-alphonso
authoritative_surface: docs/
create_intent:
- docs/MCP_2026_07_28_ADAPTER.md
execution_mode: code_change
model: ''
owned_files:
- docs/MCP_2026_07_28_ADAPTER.md
role: architect
tags: []
tracker_refs: []
---

# Work Package Prompt: WP04 — Optional Stateless MCP Tasks Projection

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `architect-alphonso`
- **Role**: `architect`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Publish the optional stateless MCP `2026-07-28` adapter design over accepted API 2.0. MCP adds ergonomic agent-native tools, resources, subscriptions, MRTR, and Tasks, but contributes no unique workflow semantic and no core dependency.

Implementation command:

```bash
spec-kitty agent action implement WP04 --agent codex
```

## Context

WP02 supplies the canonical application capabilities and native-operation lifecycle. Use `contracts/mcp-2026-07-28-projection.md` and the cited official MCP core/Tasks sources. The Tasks extension versions independently from MCP core and must be explicitly pinned/negotiated when implementation begins.

The adapter design targets the stateless revision: requests are self-describing; explicit application identities/revisions carry continuity; protocol session state is not authority. Older initialize/session, legacy SSE, Roots, Sampling, and Logging semantics are not baseline requirements.

## Branch Strategy

Use only the WP04 lane based on accepted WP02, then merge into `feat/production-runtime-architecture`. Own only `docs/MCP_2026_07_28_ADAPTER.md`. Do not add an MCP Go module, SDK dependency, server, generated schema, or runtime code in this mission.

## Optionality laws

1. The core module and normal CLI build, install, qualify, and complete missions without MCP.
2. Dependency direction is MCP adapter -> API 2.0 client, never inward.
3. Every MCP capability has a normal API/CLI equivalent.
4. A protocol request, connection, session, or header cannot mint domain authority.
5. One MCP Task projects one native operation, never a mission, work item, run, lease, or acceptance record.
6. Task TTL/expiry affects retrieval only and cannot erase durable mission history.
7. Task cancellation records cooperative intent and never directly performs compensation.
8. Reauthorization occurs for every stateless request.

### Subtask T019: Specify packaging and dependency direction

Define the adapter as a separate module and binary with its own release/version matrix. Show the package/component boundary from MCP transport/SDK to adapter to API 2.0 client to host application. State which configuration, authentication, discovery, and deployment responsibilities belong to the adapter and which remain host-owned.

List core-absence proof obligations: no MCP SDK in core `go.mod`, dependency graph, binary, install, tests, or complete CLI workflow. The optional adapter may be installed later or deployed separately.

### Subtask T020: Map discovery, tools, and resources

Map API 2.0 capability discovery to MCP server discovery, tool lists, and resource lists. Define representative tools for mission, work, run/handoff, review, acceptance, integration, decision, recovery, and operation cancellation. Define resources for progress, active runs, decisions, candidate/review/acceptance/integration, operation state, and health.

Schemas use opaque IDs and bounded typed objects. Never infer scope from the adapter's current directory or accept shell strings. The adapter advertises only capabilities the host and current actor authorize.

### Subtask T021: Map Tasks to native operations

Publish the exact mapping for task ID, `working`, `input_required`, `completed`, `failed`, `cancelled`, TTL, polling, progress notifications, and terminal result retrieval. Explain domain refusal versus JSON-RPC execution failure.

Cover adapter restart, task-handle loss, duplicate tool requests, operation replay, task retrieval expiry, and multiple adapter instances. The underlying NativeOperationID remains queryable through native identity subject to authorization.

### Subtask T022: Define stateless auth, subscriptions, resume, and bounds

Define self-describing request context, routing/auth headers, actor attribution, authorization, discovery caching, and per-request validation. Protocol continuity must use explicit mission/operation/journal revision handles.

Map subscriptions/listen to API 2.0 watch semantics. Resume from application revision, not adapter delivery cursor. Define reset after compaction, slow-consumer backpressure, maximum response/task metadata sizes, retention, and redaction. Do not expose secrets, environment dumps, transcript bodies, raw logs, or artifact payloads.

### Subtask T023: Define MRTR input and cancellation races

MRTR and task input may carry an answer only for an already-recorded DecisionID. Document typed choices/context, responding ActorID, authorization, expiration, duplicate answers, and refusal of arbitrary model output as authority.

For cancellation, show races where completion wins, cancellation is confirmed, an external effect already happened, or a decision is pending. `tasks/cancel` never directly expires a lease, deletes a workspace, resets Git, clears Beads assignment, or removes evidence. Any compensation follows the application's attribution-safe protocol.

### Subtask T024: Publish conformance matrix

Create a matrix covering:

- core works with adapter absent;
- API/CLI and MCP terminal evidence equivalence;
- requests can land on different adapter instances;
- duplicate/replayed requests are safe;
- task expiry leaves mission resumable;
- `input_required` resolves a real decision;
- cancellation/completion races preserve committed outcomes;
- progress reconnects from journal revision;
- adapter cannot mint leases/evidence/Git effects; and
- bounded privacy/backpressure behavior.

Label MCP core and Tasks versions as proposed pins requiring re-verification at M7 admission. Link primary sources without excessive quotation.

## Expected document structure

1. Status, optionality, and pinned protocol assumptions.
2. Separate-module deployment and dependency diagram.
3. API 2.0 capability discovery projection.
4. Tool catalog and authorization rules.
5. Resource catalog and bounded representations.
6. Native operation to Task mapping.
7. Stateless continuity and multi-instance routing.
8. Subscription/listen resume behavior.
9. MRTR/DecisionID input handling.
10. Cancellation, completion, effect, and compensation races.
11. Privacy, retention, TTL, and backpressure.
12. Core-absence and parity qualification.

Include two worked sequences: a long-running qualification that reaches `input_required`, and a cancellation request issued after an external effect intent has been durably recorded. Every arrow must identify whether it is transport state, native operation state, or domain state.

## Explicit non-goals

- Supporting older session-oriented MCP as the baseline.
- Adding MCP SDK dependencies to the core module.
- Defining domain state in MCP schemas.
- Treating task TTL as mission retention.
- Automatically granting authority to an MCP client or model.
- Parsing model prose as an accepted decision.
- Implementing the M7 adapter in this mission.

## Review evidence

Leave a capability parity table from API/CLI to MCP, a core dependency absence checklist, a task/domain identity table, a cancellation race table, and primary-source revision links. Clearly label any protocol behavior that must be reverified at M7 admission because the extension is independently versioned.

## Definition of Done

- The adapter is clearly optional, separate, and one-way dependent.
- Tools/resources/tasks cover no capability absent from API 2.0.
- Stateless continuity, auth, subscriptions, and resource bounds are explicit.
- Task/domain identities and cancellation semantics cannot be confused.
- Core-absence and equivalence qualification are executable future gates.
- No SDK dependency, module, binary, or runtime code was added.

## Reviewer guidance

Reject if the normal CLI cannot do something MCP can, if a task equals a mission, if connection loss changes authority, if cancellation implies rollback, or if the core must understand an MCP type. Attack the design with task expiry after an external effect and with requests routed to different adapter instances.
