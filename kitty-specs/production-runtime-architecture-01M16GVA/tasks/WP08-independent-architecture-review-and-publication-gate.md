---
work_package_id: WP08
title: Independent Architecture Review and Publication Gate
dependencies:
- WP01
- WP02
- WP03
- WP04
- WP05
- WP06
- WP07
requirement_refs:
- C-001
- C-002
- C-003
- C-004
- C-005
- C-006
- C-007
- C-008
- C-009
- C-010
- C-011
- C-012
- C-013
- C-014
- C-015
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
- FR-010
- FR-011
- FR-012
- FR-013
- FR-014
- FR-015
- FR-016
- FR-017
- FR-018
- FR-019
- FR-020
- FR-021
- FR-022
- FR-023
- FR-024
- FR-025
- FR-026
- FR-027
- FR-028
- NFR-001
- NFR-002
- NFR-003
- NFR-004
- NFR-005
- NFR-006
- NFR-007
- NFR-008
- NFR-009
- NFR-010
- NFR-011
- NFR-012
- NFR-013
- NFR-014
- NFR-015
planning_base_branch: feat/production-runtime-architecture
merge_target_branch: feat/production-runtime-architecture
branch_strategy: Planning artifacts were generated on feat/production-runtime-architecture. Implementation uses the lane worktree assigned by lanes.json and merges accepted work back into feat/production-runtime-architecture.
subtasks:
- T043
- T044
- T045
- T046
- T047
- T048
- T049
history: []
agent_profile: reviewer-renata
authoritative_surface: kitty-specs/production-runtime-architecture-01M16GVA/
create_intent:
- kitty-specs/production-runtime-architecture-01M16GVA/requirement-traceability.md
- kitty-specs/production-runtime-architecture-01M16GVA/documentation-conflict-ledger.md
- kitty-specs/production-runtime-architecture-01M16GVA/architecture-review-brief.md
- kitty-specs/production-runtime-architecture-01M16GVA/architecture-review-report.md
execution_mode: planning_artifact
model: ''
owned_files:
- kitty-specs/production-runtime-architecture-01M16GVA/requirement-traceability.md
- kitty-specs/production-runtime-architecture-01M16GVA/documentation-conflict-ledger.md
- kitty-specs/production-runtime-architecture-01M16GVA/architecture-review-brief.md
- kitty-specs/production-runtime-architecture-01M16GVA/architecture-review-report.md
role: reviewer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP08 — Independent Architecture Review and Publication Gate

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `reviewer-renata`
- **Role**: `reviewer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Independently attack the fully integrated architecture documentation, prove requirement traceability and conflict closure, and issue an exact-source ratification verdict. This WP is not a prose polish pass: it is the terminal architecture falsification and publication gate required before any production implementation mission is admitted.

Implementation command:

```bash
spec-kitty agent action implement WP08 --agent codex
```

## Independence requirement

The implementation ActorID for WP08 must differ from the ActorID that authored the architecture WPs unless the mission policy records an explicit exception. A different ProcessID for the same ActorID is not independent. Record the review actor/process and the accepted source lineages in the report.

The normal WP review after this work must also be performed by an actor independent of this report author. If the runtime cannot satisfy both layers, stop with a visible governance decision rather than self-approving.

## Context

Review accepted WP01–WP07 together. Their combined claims span ownership, durability, recovery, trackers, workspaces, Git, API, progress, acceptance, optional MCP, historical compatibility, active docs, and a seven-mission program. Planning contracts provide an independent baseline; frozen Phase 1–3 evidence provides delivery truth.

Mini's prior findings remain high-value attack themes:

- fencing cannot fence direct filesystem writes;
- behavior characterization must challenge architecture;
- WorkRevision drift needs explicit generation outcomes;
- reviewer independence is ActorID-based;
- compensation must compare current attributable state;
- control store has one machine/filesystem authority; and
- the two-agent crash experiment should happen before broad implementation.

## Branch Strategy

Use only the WP08 terminal lane after all seven dependencies are accepted and integrated into its resolved base. Merge into `feat/production-runtime-architecture`. Own only the four mission-local review artifacts. Route required fixes back to the WP that owns the affected canonical file; do not edit another WP's documents from the review lane.

## Severity and verdict rules

- **Blocker**: architecture cannot enforce a product law, has multiple authorities, or relies on impossible semantics.
- **High**: recovery, evidence, compatibility, or program behavior is materially underspecified and likely to force redesign.
- **Medium**: bounded ambiguity or operational risk can be scheduled explicitly.
- **Low**: clarity/polish issue without architectural effect.

Ratification requires zero unresolved blocker or high findings. Medium/low findings need explicit disposition and program owner. A report attached to a different source/tree or missing dependency lineage is non-admissible.

### Subtask T043: Build complete traceability

Create `requirement-traceability.md` mapping every FR-001–FR-028, NFR-001–NFR-015, C-001–C-018, and SC-001–SC-010 to:

- canonical document and exact heading;
- supporting contract/model/evidence;
- verification method;
- delivered/proposed/historical state; and
- review verdict.

Every functional requirement needs at least one substantive output and objective check. Detect duplicated requirements whose two mappings conflict. Do not mark a proposed design as delivered merely because it has a document.

### Subtask T044: Close the documentation conflict ledger

Create `documentation-conflict-ledger.md` from repository-wide searches for SQLite, GapDB, API 1.3, generic transition, acceptance commands, MCP, future phases, source of truth, and authority. For every active conflict, record location, old claim, disposition, canonical successor, owner WP, and closure evidence.

Historical evidence may retain old claims only when scope/time/status makes them non-normative. The ledger must distinguish immutable evidence from active guidance and prove no second canonical architecture remains.

### Subtask T045: Attack authority, concurrency, recovery, and compensation

Write `architecture-review-brief.md` with concrete falsification questions and scenarios, then execute them against the integrated docs:

- two agents claim the same work;
- a stale process writes after lease expiry;
- host crashes before/after durable acknowledgement;
- GapDB response is ambiguous;
- checkpoint is corrupt;
- tracker is stale/dishonest/human-edited;
- candidate Git refs move;
- same ActorID uses a new ProcessID to review;
- integration races/conflicts; and
- recovery compensation encounters human reassignment.

For each, identify authoritative state before/after, durable evidence, legal operations, prohibited inference, and recovery/decision outcome. Confirm playback invokes zero adapters.

### Subtask T046: Attack API, progress, acceptance, and MCP

Attempt to drive and observe a complete mission using only API 2.0. Identify any missing mutation/query/diagnostic. Test disconnect/resume, input, cancellation/completion race, watch compaction, blockers, and next actions conceptually against the contracts.

Apply neutral acceptance to unrelated stacks and a non-code mission. Try stale evidence, unknown provider, unauthorized waiver, deceptive formatted output, and provider drift. Verify the kernel remains neutral.

Remove the hypothetical MCP module and prove no core capability disappears. Test multi-instance stateless requests, Task TTL loss, MRTR input, and cancellation after external effect.

### Subtask T047: Attack program and delivery truth

Verify program schema, manifest conformance, IDs, dependency references, maximum three dependencies, acyclicity, critical path, entry/exit evidence, rollback, and parallel groups. Ask whether each mission has exactly one primary falsification target and can be reviewed in isolation.

Try to disprove M1's value: if it passes, what hard claim is established; if it fails, what later work is prevented? Reject a roadmap that could spend significant code before testing zombie/recovery/playback authority.

Search active docs for future behavior in present tense and for current delivered behavior described as globally incomplete. Delivery truth must be both honest and confident.

### Subtask T048: Record findings and verdict

Write `architecture-review-report.md` with:

- source commit/tree and dependency lineage;
- reviewer ActorID/ProcessID and independence result;
- methods and validation commands;
- findings by severity/location with evidence;
- resolutions or routed rework;
- requirement/ownership/conflict summaries;
- residual risks assigned to program missions; and
- `pass`, `fail`, or `non_admissible` verdict.

Do not issue pass before dependent fixes are accepted and the exact reviewed tree is reconstructed. Later documentation commits do not inherit the verdict automatically.

### Subtask T049: Run publication gate and verify next action

Run bounded checks for JSON Schema compilation/examples, manifest conformance, graph acyclicity, local links/anchors, Mermaid portability, documented commands, encoding, whitespace, architecture documentation tests, and mission status/decision hygiene.

Confirm frozen evidence/payload checksums remain valid and no runtime code changed. Verify README routes a reviewer to current status, canonical architecture, program, risks, and evidence in under five minutes. The next governed action after mission completion must be to create/admit M1 through a fresh full mission—not to implement GapDB opportunistically inside this documentation mission.

## Definition of Done

- All four owned review artifacts exist and are substantive.
- Every requirement and success criterion has a checked mapping.
- The conflict ledger has no unresolved active normative conflict.
- Authority review finds one owner per state category and enforceable stale-workspace isolation.
- API/acceptance/MCP/program attacks have explicit results.
- Zero blocker/high findings remain for a pass.
- Verdict is exact-source, independent, and does not qualify unimplemented runtime behavior.
- Publication checks pass and M1 is the explicit next mission.

## Reviewer guidance

The reviewer of WP08 should verify the report author's independence and reproduce a sample from every matrix rather than trusting the summary. Reject a self-authored pass, vague “looks good” evidence, unresolved high findings, mutable branch-bound verdict, or any claim that architecture ratification equals GapDB/API/MCP production qualification.
