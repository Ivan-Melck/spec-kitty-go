---
work_package_id: WP01
title: Canonical Production Authority Architecture
dependencies: []
requirement_refs:
- C-002
- C-003
- C-004
- C-005
- C-014
- C-016
- C-017
- C-018
- FR-001
- FR-002
- FR-003
- FR-004
- FR-005
- FR-006
- FR-007
- FR-008
- FR-009
- FR-023
- NFR-001
- NFR-002
- NFR-003
- NFR-011
- NFR-014
planning_base_branch: feat/production-runtime-architecture
merge_target_branch: feat/production-runtime-architecture
branch_strategy: Planning artifacts were generated on feat/production-runtime-architecture. Implementation uses the lane worktree assigned by lanes.json and merges accepted work back into feat/production-runtime-architecture.
subtasks:
- T001
- T002
- T003
- T004
- T005
- T006
history: []
agent_profile: architect-alphonso
authoritative_surface: docs/
create_intent:
- docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md
- docs/adr/0002-production-orchestration-architecture.md
execution_mode: planning_artifact
model: ''
owned_files:
- docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md
- docs/adr/0002-production-orchestration-architecture.md
role: architect
tags: []
tracker_refs: []
---

# Work Package Prompt: WP01 — Canonical Production Authority Architecture

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `architect-alphonso`
- **Role**: `architect`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Author the one normative production architecture and its concise superseding ADR. Preserve the product laws that make Spec Kitty valuable—missions, immutable executable generations, isolated concurrent workspaces, independent review, and serialized Git integration—while removing Python-shaped representations and the current SQLite-shaped production recommendation.

Implementation command:

```bash
spec-kitty agent action implement WP01 --agent codex
```

## Context

This is a documentation-only WP. Phase 1–3 behavior and evidence remain true for their exact attested commits. The target architecture is approved direction, not shipped behavior. Use the mission-local specification, research, data model, control-journal contract, and quickstart as primary inputs. Use `docs/PYTHON_BEHAVIOR_INVENTORY.md`, `docs/MINI_REVIEW_RESOLUTION.md`, ADR 0001, and evidence indexes as historical sources, not as text to overwrite.

The canonical ownership summary must remain:

```text
Beads       -> what mutable work exists and its discussion
Spec Kitty  -> whether work may execute, how it is reviewed and accepted
Git         -> code, commits, trees, and ref outcomes
GapDB       -> target ordered control-state substrate, not product semantics
```

## Branch Strategy

Work only in the lane worktree allocated to WP01 by `lanes.json`. The planning/base and merge target are both `feat/production-runtime-architecture`. Do not edit the root checkout, merge another lane, or touch files outside the two owned paths. Later WPs depend on the terminology and links introduced here, so stabilize headings and anchors before review.

## Locked architecture laws

1. One machine/filesystem authority owns the first production control runtime.
2. Remote callers use application capabilities and never open GapDB or mutate host workspaces/Git directly.
3. A fencing token can fence application operations but cannot fence POSIX writes; expired workspaces are quarantined and never transferred.
4. `ActorID`, not `ProcessID`, is the default review-independence boundary.
5. WorkRevision is an immutable executable-graph generation; drift yields explicit reconciliation.
6. Durable success precedes authoritative acknowledgement; active projections remain memory-primary.
7. Playback reduces recorded events and observations without executing side effects.
8. Compensation acts only after current external state is demonstrably attributable.
9. Beads assignment and status never grant execution, review, acceptance, or integration authority.
10. SQLite is not permitted in the target production graph, but its frozen evidence remains historical truth.

### Subtask T001: Freeze delivery truth and dispositions

Build a compact current-versus-target inventory covering storage, tracker, Git/workspaces, execution composition, API, acceptance, MCP, packaging, and evidence. Each row must use a controlled status such as `delivered`, `retained`, `amended`, `proposed`, `retired`, or `historical` and link delivered claims to exact repository evidence.

Do not say that GapDB, API 2.0, neutral acceptance, the production host, or MCP ships today. State that the current SQLite implementation is qualified for its historical Phase 1–3 boundary and is scheduled for complete production removal only after M1/M2 evidence.

### Subtask T002: Publish behavioral preservation and retirement map

Map the Python behavior inventory and Phase 1–3 gains into four explicit classes:

- preserved product laws;
- strengthened behavior;
- amended representation or boundary;
- intentional retirement.

Preserve missions, decisions, immutable candidate/review evidence, concurrency, stale-authority refusal, review independence, and integration serialization. Retire Markdown checkbox authority, Python lane vocabulary as product state, generic transition commands, ecosystem-specific acceptance, and filesystem projections as canonical state.

Every intentional retirement must name the replacement behavior and later proof obligation. Do not translate Python modules or command names into Go package recommendations.

### Subtask T003: Define ownership and concurrency boundaries

Publish a complete state-ownership table for mission intent/lifecycle, mutable work definition, executable graph, run/lease, workspace, candidate, review, acceptance, integration, native operation, progress, and external effects. Each row needs exactly one writable authority, durable evidence, allowed projections, and forbidden inference.

Include a portable Mermaid or text diagram showing application/kernel/adapters and the one-way dependency direction. Include the zombie-writer sequence using simple GitHub-compatible Mermaid syntax or a text fallback. Explicitly explain why a replacement lease receives W2 rather than W1.

### Subtask T004: Specify memory, durability, recovery, and playback

Describe the authority-changing command transaction: canonical request digest, idempotency lookup, epoch/fence verification, pure reduction, one durable GapDB atomic mutation, in-memory publication, then asynchronous effect dispatch.

Separate:

- GapDB WAL/snapshot recovery;
- Spec Kitty control journal and mission checkpoint;
- runtime-epoch replacement;
- reconciliation with Git, Beads, workspaces, artifacts, and outbox state; and
- side-effect-free historical playback.

Record the pinned `gapdbd` sidecar decision and explain that GapDB currently publishes a client, not an embeddable owner lifecycle. The first proving mission must qualify the public Unix-socket boundary and signed adoption tuple.

### Subtask T005: Author canonical recommendation and ADR

Write `docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md` as the complete normative record. It must contain scope/status, delivery truth, principles, ownership, component boundaries, state/durability, recovery/playback, workspace fencing, tracker/Git roles, review/acceptance/integration, application boundary summary, architecture guards, compatibility, risks, and links to the program and specialized documents that later WPs will create.

Write ADR 0002 as a concise decision record with status, context, decision, consequences, superseded active guidance, non-goals, and proof obligations. It may link to future owned document paths, but must not duplicate their full contracts.

### Subtask T006: Validate the record

Verify:

- every ownership row has one owner;
- all proposed behavior is labeled;
- local links resolve or intentionally target a path created by a dependent WP;
- Mermaid uses GitHub-supported conservative syntax;
- canonical terminology matches `spec.md`;
- no frozen evidence payload changed;
- `git diff --check` passes; and
- a reviewer can trace FR-001 through FR-009 and FR-023 to exact headings.

If a dependent future document link cannot yet resolve in this isolated lane, list it explicitly for integration review rather than weakening the link contract.

## Validation strategy

Run documentation-oriented checks only. Inspect the diff, validate local anchors, and exercise any existing bounded documentation guard that does not mutate frozen evidence. Do not change Go production/tests to make documentation pass.

Record a short validation appendix naming the reviewed Phase 1–3 evidence commits, GapDB source tuple used as design input, schema/contract versions, link/diagram checks, and any links that become resolvable only after dependent WPs integrate. This appendix is provenance for the architecture decision, not runtime qualification.

## Non-goals

- Implementing a GapDB adapter or sidecar supervisor.
- Changing current persistence interfaces or Go dependencies.
- Creating API, acceptance, MCP, or program details owned by later WPs.
- Requalifying Phase 1–3 evidence against a new source tree.
- Designing multi-machine control-store ownership.
- Selecting exact performance thresholds for later missions.
- Editing historical artifacts to reduce apparent inconsistency.

## Definition of Done

- The two owned files are substantive and internally consistent.
- Delivered, target, and historical claims are unmistakable.
- The ownership matrix covers every state category in the mission data model.
- GapDB and recovery semantics do not collapse storage recovery into product playback.
- Workspace quarantine and ActorID reviewer independence are product laws.
- The ADR names this document as canonical and identifies superseded active guidance.
- No runtime code or frozen evidence file changed.

## Risks and reviewer guidance

Reject the WP if it quietly preserves SQLite as fallback, treats Beads as scheduler authority, assumes fencing prevents arbitrary writes, lets same ActorID self-review through another ProcessID, or describes an unimplemented capability as production-ready. Review should compare every diagram arrow with the ownership matrix and challenge any state that appears writable from two places.
