---
work_package_id: WP03
title: Immutable Work Graph Generations
dependencies:
- WP01
requirement_refs:
- FR-002
- FR-017
- FR-018
- NFR-004
- NFR-006
- NFR-008
- NFR-010
- C-006
- C-008
planning_base_branch: mission/phase1-authority-proving-ground
merge_target_branch: mission/phase1-authority-proving-ground
branch_strategy: Planning artifacts for this mission were generated on mission/phase1-authority-proving-ground. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into mission/phase1-authority-proving-ground unless the human explicitly redirects the landing branch.
subtasks:
- T010
- T011
- T012
- T013
history: []
agent_profile: implementer-ivan
create_intent:
- internal/controlstore/sqlite/workgraph.go
- internal/controlstore/sqlite/workgraph_test.go
- internal/helpercmd/workgraph.go
- internal/scenario/reconciliation_test.go
execution_mode: code_change
authoritative_surface: internal/workgraph/
owned_files:
- internal/workgraph/**
- internal/controlstore/sqlite/workgraph.go
- internal/controlstore/sqlite/workgraph_test.go
- internal/helpercmd/workgraph.go
- internal/scenario/reconciliation_test.go
tags: []
tracker_refs: []
---

# Work Package Prompt: WP03 - Immutable Work Graph Generations

## Objective

Make the executable work contract immutable and define what happens when
reality changes: `continue`, `supersede`, `invalidate`, or `fork`.

## Boundaries

WorkRevision is a product concept, not a snapshot of Markdown or tracker rows.
It owns stable WorkIDs, criteria, dependency edges, lane lineage, source
revisions, and digests. Do not import tracker or Git implementations.

### T010 - Model and finalize an immutable graph

- Define work, criteria, dependencies, lane lineage, provider/source revision,
  graph digest, and criteria digest.
- Canonicalize digest input deterministically and reject mutation after
  finalization.

### T011 - Validate executable graph invariants

- Reject missing nodes, ambiguous IDs, invalid lane mapping, and cycles.
- Re-run cycle detection after lane-collapse projection.
- Persist numbered immutable revisions and optimistic creation conflicts.

### T012 - Implement explicit reconciliation

- Persist old/new digests, affected WorkIDs, ActorID, rationale, and one of the
  four dispositions.
- Encode eligibility consequences for active, handed-off, approved, and
  integrated work without rewriting history.
- Ensure `fork` creates distinct WorkIDs and explicit edges.

### T013 - Prove generation semantics

- Test equivalent continue, replacement supersede, material invalidate, fork,
  and post-collapse cycle refusal.
- Expose queryable reasons that later execution/handoff packages can enforce.

## Validation

```bash
go test ./internal/workgraph ./internal/controlstore/sqlite
go test ./internal/scenario -run Reconciliation
```

## Definition of Done

All four dispositions have deterministic eligibility effects and immutable
audit evidence. An old run's contract cannot mutate invisibly.

## Reviewer Guidance

Reject status names without enforced consequences, tracker-version identity in
place of WorkRevision identity, non-canonical digests, or any disposition that
rewrites integrated history.
