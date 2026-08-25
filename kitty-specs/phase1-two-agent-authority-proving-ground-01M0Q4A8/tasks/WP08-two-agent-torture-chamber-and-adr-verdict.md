---
work_package_id: WP08
title: Two-Agent Torture Chamber And ADR Verdict
dependencies:
- WP02
- WP03
- WP05
- WP06
- WP07
requirement_refs:
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
- C-001
- C-002
- C-003
- C-004
- C-005
- C-006
- C-007
- C-008
planning_base_branch: mission/phase1-authority-proving-ground
merge_target_branch: mission/phase1-authority-proving-ground
branch_strategy: Planning artifacts for this mission were generated on mission/phase1-authority-proving-ground. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into mission/phase1-authority-proving-ground unless the human explicitly redirects the landing branch.
subtasks:
- T033
- T034
- T035
- T036
history: []
agent_profile: implementer-ivan
create_intent:
- internal/scenario/universal_assertions.go
- internal/scenario/phase1_test.go
- internal/scenario/repetition_test.go
execution_mode: code_change
authoritative_surface: internal/scenario/
owned_files:
- internal/scenario/universal_assertions.go
- internal/scenario/phase1_test.go
- internal/scenario/repetition_test.go
- docs/adr/**
- docs/phase1-evidence/**
tags: []
tracker_refs: []
---

# Work Package Prompt: WP08 - Two-Agent Torture Chamber And ADR Verdict

## Objective

Run Mini's deliberately ugly two-agent experiment as one end-to-end torture
chamber, apply universal invariants to every fault, repeat it, and record whether
the authority architecture is ratified, revised, or rejected.

## Boundaries

This package may compose existing public commands and add assertions/evidence.
It may not patch earlier services to force green results without rejecting the
owning WP back through review. A red safety scenario is valid architecture
evidence, not permission to skip it.

### T033 - Implement universal safety assertions

- Assert primary isolation, one valid lease, immutable workspace binding,
  stale-token refusal, target-ref serialization, candidate reachability, and
  attribution-safe compensation after every named fault.
- Make failures print stable IDs, SHAs, states, and recovery advice.

### T034 - Run the complete two-agent narrative

- Two work items, two agents, one fake tracker, one real SQLite control DB, and
  one real Git repository.
- Claim concurrently; allocate worktrees; crash one at every boundary; expire
  and replace it; resume the zombie; finish both; independently review; and
  serialize integration.
- Include manual branch movement, tracker outage, simultaneous integration, and
  merge conflict.

### T035 - Produce repeatability and scope evidence

- Run `go test ./...`, `go test -race ./...`, and every Phase 1 scenario 100
  times (sharding families only if documented).
- Record versions, commands, timestamps, and machine-readable results.
- Assert the production dependency/source graph contains no Beads, Team Kitty,
  API, MCP, Python, or Markdown compatibility kernel.

### T036 - Record the ADR verdict

- For authority, lane/workspace separation, review independence, and
  integration, write `ratify`, `revise`, or `reject` with supporting scenario
  IDs and observed limitations.
- Treat any invariant revision as a prerequisite for later architecture, not an
  invisible follow-up.

## Validation

```bash
go test ./...
go test -race ./...
go test ./internal/scenario -run Phase1 -count=100
```

## Definition of Done

The mission has a reproducible empirical verdict. Passing proves only the
stated single-machine boundary; failing usefully changes the architecture.

## Reviewer Guidance

Audit scenario completeness before looking at green output. Reject sleeps in
place of barriers, direct table/ref mutation by fixtures, skipped faults,
unrecorded failures, or any claim broader than the tested environment.
