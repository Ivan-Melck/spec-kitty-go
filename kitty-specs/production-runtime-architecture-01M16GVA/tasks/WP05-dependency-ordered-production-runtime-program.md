---
work_package_id: WP05
title: Dependency-Ordered Production Runtime Program
dependencies:
- WP01
requirement_refs:
- C-003
- C-015
- C-016
- C-017
- C-018
- FR-024
- FR-025
- FR-027
- NFR-005
- NFR-006
- NFR-007
planning_base_branch: feat/production-runtime-architecture
merge_target_branch: feat/production-runtime-architecture
branch_strategy: Planning artifacts were generated on feat/production-runtime-architecture. Implementation uses the lane worktree assigned by lanes.json and merges accepted work back into feat/production-runtime-architecture.
subtasks:
- T025
- T026
- T027
- T028
- T029
- T030
- T031
history: []
agent_profile: planner-priti
authoritative_surface: docs/
create_intent:
- docs/PRODUCTION_RUNTIME_PROGRAM.md
execution_mode: planning_artifact
model: ''
owned_files:
- docs/PRODUCTION_RUNTIME_PROGRAM.md
role: planner
tags: []
tracker_refs: []
---

# Work Package Prompt: WP05 — Dependency-Ordered Production Runtime Program

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `planner-priti`
- **Role**: `planner`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Publish the dependency-ordered implementation program that carries the architecture from proof to production without a mega-mission. The readable roadmap must agree exactly with `program-manifest.json`, preserve independent Spec Kitty governance for every mission, and make the smallest disproof experiment the first admitted work.

Implementation command:

```bash
spec-kitty agent action implement WP05 --agent codex
```

## Context

WP01 supplies the ratified authority model. The planning manifest defines seven candidate missions:

```text
M1 GapDB journal proof
 -> M2 GapDB-only authority
    -> M3 production execution --+
    -> M4 neutral acceptance ----+-> M5 API 2.0 and native UX
                                      -> M6 core qualification/release
                                      -> M7 optional MCP
```

M3 and M4 are parallel after M2. M7 is parallel with M6 after M5 and off the core critical path. Candidate count can change only through explicit program reconciliation, not informal scope growth.

## Branch Strategy

Use only the WP05 lane based on accepted WP01 and merge into `feat/production-runtime-architecture`. Own only `docs/PRODUCTION_RUNTIME_PROGRAM.md`. Treat mission-local `program-manifest.json` and its schema as source contracts; if you discover a contradiction, report it rather than silently changing them outside owned files.

## Program laws

1. Every mission has one primary falsification target.
2. Every mission has at most three named dependencies and explicit immutable entry/exit evidence.
3. Every mission retains specify, plan, tasks, implement/review, accept, merge, mission-review, and retrospective gates.
4. Package scaffolding is not progress; an executable vertical proof is.
5. Mission admission is a governed decision, not Beads status or MCP task state.
6. A failed predecessor cannot be bypassed by starting its consumers.
7. SQLite is removed only after M1 proves the alternative and M2 proves complete parity/eradication.
8. Optional MCP never delays the core release unless maintainers explicitly change the program.

### Subtask T025: Translate the manifest into a roadmap

Create an operator-readable document with status, scope, graph, critical path, parallel groups, and a table summarizing outcome, falsification target, dependencies, entry evidence, exit evidence, non-goals, risk, and rollback for M1–M7.

Preserve manifest wording where precision matters, but add navigational prose and clear admission/stop decisions. Link the schema/manifest and canonical architecture. Label all missions `proposed`; none is delivered merely because this roadmap exists.

### Subtask T026: Specify M1 GapDB proving ground

Make M1 the smallest experiment most likely to disprove the storage/recovery recommendation. It must use a pinned external `gapdbd` sidecar/public client tuple and the two-agent zombie torture scenario.

Required proof includes durable event/idempotency/projection/outbox atomic batch, checkpoint/replay digest, runtime epoch replacement, fresh replacement workspace, response-loss injection, zero-side-effect playback, all fourteen GapDB adoption scenarios through the real adapter, and a signed human adoption record. State that M1 does not migrate the full store.

Include entry gating for an immutable GapDB tuple even though the repository currently has no tag. Define source revision, binary digest, client/protocol version, lifecycle policy, and signer as the minimum tuple.

### Subtask T027: Specify M2 GapDB-only authority

Describe the capability-shaped migration of mission, artifacts, WorkRevision, execution, workspace, external effects, handoff, review, integration, idempotency, checkpoints, and recovery. Explicitly forbid table-to-key transliteration, dual writes, fallback, hidden migration mode, or indefinite compatibility.

Exit evidence must prove no SQLite in production source, `go.mod`/transitives, SBOM, built binaries, install, fallback, or runtime configuration. Unsupported historical SQLite state fails honestly rather than importing silently. Preserve applicable refusal, race, fault, and mutant evidence against GapDB.

### Subtask T028: Specify parallel M3 and M4

M3 proves real production composition among GapDB, Beads, Git, workspaces, agents, process lifetime, review, and serialized integration. Include same-work claim race, same-ActorID review refusal, zombie quarantine, dishonest/stale/unavailable tracker, human reassignment, compare-before-compensate, repository fence, and conflict repair work.

M4 proves the neutral acceptance kernel using materially different provider fixtures, strict policy union, missing-provider decisions, exact freshness/invalidation, attributed waivers, and architecture mutants rejecting ecosystem imports/output parsing.

Explain why they can run concurrently after M2 and what stable evidence M5 consumes from both.

### Subtask T029: Specify M5, M6, and optional M7

M5 delivers API 2.0, native operations, progress, normal CLI/host service, and bounded API 1.3 compatibility. Its black-box proof must complete, disconnect, resume, and explain a mission without direct external-state inspection.

M6 qualifies clean installation and sustained multi-mission production composition: load, soak, crashes across every command family, backup/restore digest, security/path/socket tests, operator doctor/recovery, supported platforms, reproducible binaries, and absence of Python/SQLite/MCP from the core requirement.

M7 separately qualifies stateless MCP and Tasks. Make core-absence, multi-instance requests, TTL loss, input, cancellation races, progress resume, and CLI/MCP terminal evidence equivalence explicit.

### Subtask T030: Define admission, reconciliation, gates, and rollback

For each mission, explain who/what admits it, how dependency evidence is resolved by immutable identity, what happens when upstream evidence is superseded, and how `proposed`, `admitted`, `active`, `blocked`, `complete`, `superseded`, and `cancelled` differ.

Program reconciliation may add, split, supersede, or reorder candidate missions only through an explicit decision and an acyclic revised manifest. Preserve completed mission evidence. Rollback means withholding/removing unqualified composition—not enabling an architecture-forbidden fallback.

### Subtask T031: Validate graph and first experiment

Validate manifest schema, unique IDs, dependency references, maximum three dependencies, acyclicity, and critical-path edges. Compare readable tables to machine fields. Include the exact M1 torture-chamber sequence and injections as a text block or portable diagram.

State the stop rule: if M1 cannot preserve authority and reproduce its replay digest through the two-agent zombie scenario, do not begin the six-thousand-line storage replacement in M2.

## Expected document structure

1. Program status and relationship to this documentation mission.
2. Machine manifest/schema references.
3. Dependency graph and critical path.
4. Program-wide admission and reconciliation law.
5. M1–M7 mission cards.
6. Parallel execution groups and shared contract boundaries.
7. Stop/go decisions and rollback rules.
8. Exact M1 torture chamber.
9. Program risks and ownership.
10. How to create the next governed mission.

Each mission card should state outcome, falsification target, dependencies, entry evidence, scope, exit evidence, non-goals, rollback, and what successors consume. Use the same stable IDs as the JSON manifest.

## Explicit non-goals

- Creating the seven Spec Kitty mission directories now.
- Estimating calendar dates or agent counts without evidence.
- Freezing package boundaries before vertical proofs.
- Allowing a failed M1/M2 to be bypassed.
- Treating external release tuples as mutable branch names.
- Moving MCP onto the core critical path.
- Implementing any production behavior in this roadmap WP.

## Review evidence

Leave schema-validation output, dependency-reference and `tsort`/acyclicity checks, a human/machine field comparison, and a list of parallel groups. Record why M1 is the highest-value falsification experiment rather than merely the smallest coding task.

## Definition of Done

- The roadmap and manifest agree on seven candidate missions and all dependency edges.
- M1 is a vertical proof with a pinned sidecar/adoption boundary.
- M2 proves complete SQLite eradication only after M1 passes.
- M3/M4 and M6/M7 parallelism is explicit.
- Every mission has bounded entry/exit evidence, non-goals, risks, rollback, and independent gates.
- Graph validation and the stop rule are reproducible.
- No implementation mission was created or started by this WP.

## Reviewer guidance

Reject if any mission has multiple unrelated falsification targets, hidden dependencies, or package-scaffolding exit criteria. Challenge whether M1 can actually fail the architecture early, whether M2 has a disguised fallback, and whether M7 can disappear without affecting core release.
