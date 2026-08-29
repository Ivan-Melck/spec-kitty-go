---
work_package_id: WP07
title: Historical Evidence and Compatibility Dispositions
dependencies:
- WP01
- WP02
requirement_refs:
- C-001
- C-014
- C-017
- FR-002
- FR-003
- FR-008
- FR-010
- FR-026
- NFR-003
- NFR-004
- NFR-009
planning_base_branch: feat/production-runtime-architecture
merge_target_branch: feat/production-runtime-architecture
branch_strategy: Planning artifacts for this mission were generated on feat/production-runtime-architecture. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/production-runtime-architecture unless the human explicitly redirects the landing branch.
subtasks:
- T038
- T039
- T040
- T041
- T042
history: []
agent_profile: curator-carla
authoritative_surface: docs/
create_intent:
- docs/HISTORICAL_ARCHITECTURE_DISPOSITIONS.md
execution_mode: code_change
model: ''
owned_files:
- docs/ORCHESTRATOR_API_1_3_PROJECTION.md
- docs/adr/0001-phase1-authority-proving-ground-verdict.md
- docs/HISTORICAL_ARCHITECTURE_DISPOSITIONS.md
role: curator
tags: []
tracker_refs: []
---

# Work Package Prompt: WP07 — Historical Evidence and Compatibility Dispositions

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `curator-carla`
- **Role**: `curator`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Preserve the integrity of Phase 1–3 evidence and prior architecture decisions while making their present disposition explicit. Add context around API 1.3 and ADR 0001, and publish one index that distinguishes retained laws, observed historical implementations, bounded compatibility, and superseded future guidance.

Implementation command:

```bash
spec-kitty agent action implement WP07 --agent codex
```

## Context

WP01 defines the new canonical architecture; WP02 defines API 2.0 and safe API 1.3 mapping. Earlier documents were correct at their time and boundary. Their source-bound evidence must remain auditable. In particular, do not edit:

- `docs/phase1-evidence/**`;
- `docs/phase2-evidence/**`;
- `docs/phase3-evidence/**`;
- checksummed qualification payloads;
- completed mission task/review history; or
- machine evidence to make new claims.

Contextual annotations belong in the three owned files and active README/plan links, not inside frozen payloads.

## Branch Strategy

Use the WP07 lane based on accepted WP01 and WP02. Merge into `feat/production-runtime-architecture`. Touch only the three owned paths. Preserve the body of ADR 0001 and API 1.3 wherever it reports observed facts; use prominent status/supersession sections instead of retrospective rewriting.

## Disposition vocabulary

- **Retained law**: still normative, such as one machine/filesystem authority or isolated workspaces.
- **Observed implementation**: true for an attested source, such as modernc SQLite 1.57.0.
- **Compatibility projection**: supported only through a bounded outer adapter.
- **Superseded guidance**: no longer directs future implementation but remains historical rationale.
- **Unchanged evidence**: immutable payload whose verdict remains source-bound.
- **New proof obligation**: target behavior requiring a later mission.

### Subtask T038: Bound API 1.3 compatibility

Add a prominent status section to `docs/ORCHESTRATOR_API_1_3_PROJECTION.md` stating that:

- the document remains the historical/current compatibility inventory for existing consumers;
- API 2.0 is the canonical target application vocabulary;
- generic lane-oriented transition semantics are not carried forward as authority;
- safe representable requests delegate to named API 2.0 operations;
- lossy/unsafe requests receive typed upgrade refusal; and
- compatibility code remains outside the application/kernel tiers.

Preserve the detailed 1.3 inventory. Link to the canonical architecture and API 2.0 documents. Do not claim the compatibility adapter is already implemented if it is only documented.

### Subtask T039: Annotate ADR 0001 without rewriting it

Add a supersession/context note near the top of ADR 0001. State that its Phase 1 proving-ground verdict and observed SQLite/Git facts remain valid for the attested source, while ADR 0002 supersedes its future production storage/API direction. Identify retained product laws and new proof obligations.

Do not edit observed versions, tests, verdict, or evidence interpretation to make them match the new target. ADR 0001 should still explain why its decision was reasonable before GapDB and the production program existed.

### Subtask T040: Publish the historical disposition index

Create `docs/HISTORICAL_ARCHITECTURE_DISPOSITIONS.md` with an indexed table for README, technical plan, ADRs, concurrency scenarios, Python inventory, Mini brief/resolution, API 1.3, Phase 1–3 evidence indexes, and completed missions. For each, record original role, mutability class, current disposition, canonical successor where applicable, and forbidden edit/claim.

Explain the evidence-layer model: source commit/tree, machine payload, publication commit, documentation attestation, later commits. A later architecture document does not inherit or invalidate an older exact-source verdict.

### Subtask T041: Classify behavior inventory and review findings

Record which Python inventory decisions remain valuable product behavior, which SQLite representation recommendations are superseded, and which API/remote-host constraints are strengthened. Similarly classify Mini's resolved findings: workspace quarantine, early behavior characterization, WorkRevision generations, ActorID reviewer independence, attribution-safe compensation, one-machine authority, and early torture test remain load-bearing.

Do not edit `PYTHON_BEHAVIOR_INVENTORY.md` or `MINI_REVIEW_RESOLUTION.md`. The disposition index interprets them from the new decision point and links precise sections.

### Subtask T042: Prove historical integrity

Before editing, record Git object/diff evidence for the frozen Phase 1–3 directories. After editing, prove no frozen payload or completed mission history changed. Validate all new links and anchors.

Search owned files for wording that incorrectly transfers old qualification to GapDB/API 2.0 or treats new decisions as invalidating historical facts. Confirm the three owned files contain no duplicate canonical specification; they should route readers outward.

## Required disposition index columns

For each artifact or artifact family, include:

- path/title;
- original purpose and source boundary;
- mutability class;
- observed implementation facts;
- retained product laws;
- superseded active guidance;
- canonical successor link;
- new proof obligation;
- forbidden inference; and
- checksum/source-verification method where applicable.

At minimum cover README, technical plan, concurrency scenarios, ADR 0001, API 1.3, Python behavior inventory, Mini brief/resolution, all three evidence directories, and completed Phase 1–3 mission records.

## Explicit non-goals

- Reissuing old evidence under the new architecture.
- Changing checksums or qualification payloads.
- Declaring old decisions mistakes merely because inputs changed.
- Promising permanent support for every API 1.3 operation.
- Making the disposition index canonical architecture.
- Editing completed mission task history.
- Adding compatibility runtime code.

## Review evidence

Capture the before/after path set for every frozen evidence directory and verify no diff. Check links and headings after integration. Search for unqualified “current,” “production,” “canonical,” and “supported” claims in the owned files and explain their time/source boundary.

## Definition of Done

- API 1.3 is unmistakably bounded compatibility, not API 2.0 doctrine.
- ADR 0001 retains its original facts/verdict with a non-destructive supersession note.
- One disposition index covers active and historical documentation/evidence.
- Python/Mini findings are classified without changing source documents.
- Frozen Phase 1–3 payloads and completed mission histories are unchanged.
- Links to canonical architecture/API/program documents resolve after integration.

## Reviewer guidance

Compare the diff against original Git objects. Reject if historical facts are rewritten, checksums or payloads change, old evidence is used to qualify the new architecture, or the status banners are vague enough that a reader could still implement future work from API 1.3 or ADR 0001.
