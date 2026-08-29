---
work_package_id: WP06
title: Active README and Technical Plan Migration
dependencies:
- WP01
- WP02
- WP03
- WP04
- WP05
- WP07
requirement_refs:
- C-001
- C-003
- C-011
- C-014
- C-017
- FR-001
- FR-002
- FR-008
- FR-010
- FR-014
- FR-020
- FR-026
- NFR-003
- NFR-004
- NFR-007
- NFR-008
- NFR-009
planning_base_branch: feat/production-runtime-architecture
merge_target_branch: feat/production-runtime-architecture
branch_strategy: Planning artifacts were generated on feat/production-runtime-architecture. Implementation uses the lane worktree assigned by lanes.json and merges accepted work back into feat/production-runtime-architecture.
subtasks:
- T032
- T033
- T034
- T035
- T036
- T037
history: []
agent_profile: curator-carla
authoritative_surface: README.md
create_intent: []
execution_mode: planning_artifact
model: ''
owned_files:
- README.md
- docs/TECHNICAL_PROJECT_PLAN.md
role: curator
tags: []
tracker_refs: []
---

# Work Package Prompt: WP06 — Active README and Technical Plan Migration

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `curator-carla`
- **Role**: `curator`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Update the repository's active entry points so they present one honest story: what Phase 1–3 actually delivers today, what architecture has been ratified for production, which historical evidence remains valid, and which governed mission comes next. Remove obsolete active guidance without erasing history.

Implementation command:

```bash
spec-kitty agent action implement WP06 --agent codex
```

## Context

This WP depends on all documents it must index. WP01 is canonical; WP02–WP05 specialize API, acceptance, MCP, and the delivery program; WP07 supplies historical dispositions. README currently describes qualified SQLite Phase 3 behavior correctly but repeatedly frames it as the ongoing production direction. The technical plan contains the prior SQLite/API 1.3/future-phase recommendation across many sections.

The rewrite must maintain a two-column truth discipline:

- **Delivered now**: exact Phase 1–3 code/evidence, Beads adapter, proving commands, current build/install behavior.
- **Approved target**: GapDB-only authority, production composition, API 2.0/progress, neutral acceptance, optional MCP, and M1–M7 program.

## Branch Strategy

Use only the WP06 integration lane after WP01–WP05 and WP07 are accepted. Merge into `feat/production-runtime-architecture`. Own only `README.md` and `docs/TECHNICAL_PROJECT_PLAN.md`. Do not edit canonical specialized documents, historical evidence, checksums, mission records, Go code, Makefiles, or workflows.

## Documentation laws

1. Active docs may summarize canonical decisions but link rather than redefine them.
2. Current compile/install commands must remain accurate for the current repository.
3. Proposed commands, modules, or binaries cannot appear as currently runnable.
4. SQLite may appear as delivered history/current implementation but not as target production fallback.
5. GapDB adoption direction does not imply M1/M2 evidence already exists.
6. API 1.3 remains compatibility history; API 2.0 is target application doctrine.
7. MCP is optional and absent from the core requirement.
8. Historical evidence files remain untouched.

### Subtask T032: Rewrite README delivery truth and navigation

Start README with a compact status table separating delivered Phase 1–3 and the ratified production target. Keep exact evidence links and current maturity claims bounded to their source. Add a five-minute reviewer path to canonical architecture, API, acceptance, program, historical dispositions, evidence, build/install, and next mission.

Remove repeated hedging that makes the whole repository sound uniformly incomplete. Prefer precise states: “delivered and qualified for boundary X,” “ratified target,” “proposed mission,” and “not yet implemented.” The Go port's completed concurrency/domain work should be described confidently without claiming production composition.

### Subtask T033: Rewrite README architecture, install, and adoption boundaries

Replace the active architecture summary and diagrams with the current/target split. Explain Beads/Spec Kitty/Git ownership and GapDB's target role. Preserve the one-machine/filesystem authority law.

Verify compile/install/run commands against the current module and binaries. If the production `spec-kitty`/`gapdbd` packaging does not yet exist, do not write commands that imply it does. Provide a clearly labeled future deployment shape and point to M1/M2.

Rewrite the adoption boundary to state the approved direction plus remaining proof: external adapter comparison, pinned tuple, signed human record, then complete SQLite removal. This is a gated implementation sequence, not indecision.

### Subtask T034: Classify the technical plan

Inventory every normative section involving source of truth, storage, events, recovery, API, acceptance, MCP, repository layout, phases, validation, risks, and definition of done. Classify each as retain, amend, replace, move to historical context, or link-only summary.

Use this inventory while editing; do not publish a second hidden architecture. Preserve useful rationale and exact historical evidence, but mark obsolete future guidance as superseded by ADR 0002.

### Subtask T035: Replace active technical guidance

Restructure the technical plan around:

- delivery truth and compatibility;
- product laws and authority owners;
- GapDB sidecar/control journal/checkpoint/epoch/recovery;
- production execution and Beads/Git/workspace sagas;
- API 2.0/native operations/progress;
- stack-neutral acceptance;
- optional MCP projection;
- seven-mission program and qualification strategy.

The technical plan should defer precise contracts to the canonical/specialized documents. Remove future-phase text that says SQLite remains the chosen production endpoint, generic API 1.3 transition drives later transports, or acceptance is a fixed Go command ladder.

### Subtask T036: Add canonical indexes and eliminate conflicts

Ensure both files identify one canonical normative architecture and one delivery program. Link to API 2.0, acceptance, MCP, API 1.3 compatibility, historical dispositions, behavior inventory, Mini review resolution, and phase evidence.

Search both active files for conflicting statements. Where historical description is retained, add explicit scope/time qualifiers. Do not solve conflicts by deleting evidence links or vague words such as “eventually.”

### Subtask T037: Validate active docs

Run local-link checks, Mermaid portability checks, documented-command validation, encoding, and `git diff --check`. Verify every runnable command against the current checkout. Search for proposed capabilities phrased in present tense and for active SQLite fallback/API 1.3 canonicality language.

Confirm no checksummed/frozen evidence file changed. Compare required headings and links with NFR-007's five-minute review goal. Record any deliberately retained historical statement and why it is not normative.

## Required README navigation

The final README should make these answers findable without reading the entire technical plan:

1. What is Spec Kitty and what makes this port distinct?
2. What Phase 1–3 behavior is actually delivered and qualified?
3. What remains proving scaffolding rather than production composition?
4. Why is GapDB the approved target and why is adoption still gated?
5. Who owns work, execution authority, and Git facts?
6. Where are API 2.0, neutral acceptance, MCP, and program details?
7. How does a contributor compile and run the current repository today?
8. What is the next mission and what would make it stop?

## Explicit non-goals

- Rewriting historical evidence results.
- Advertising a future `spec-kitty` binary as currently shipped.
- Adding build or install automation.
- Copying every specialized contract into README.
- Hiding current limitations behind aspirational language.
- Keeping obsolete future phases for sentimental continuity.
- Changing the license or unrelated project prose.

## Review evidence

Provide before/after searches for competing active SQLite, API 1.3, fixed acceptance-command, and mandatory-MCP guidance. Record current documented commands and their check results. List every historical occurrence deliberately retained and its scope qualifier. A clean local-link/diagram/whitespace result is required.

## Definition of Done

- README gives a confident, honest delivered/target split and accurate installation path.
- The technical plan no longer competes with ADR 0002/canonical architecture.
- SQLite is historical/current implementation, never target fallback.
- API 2.0, neutral acceptance, optional MCP, and the program are linked and correctly labeled.
- All current commands, Mermaid blocks, and local links validate.
- Historical evidence and runtime code remain untouched.

## Reviewer guidance

Read only README for five minutes and answer: what works now, what is approved, what remains, where is proof, and what happens next? Then compare every active future claim with the canonical docs. Reject false present-tense claims, broken current install instructions, duplicate normative definitions, or edits that rewrite frozen evidence.
