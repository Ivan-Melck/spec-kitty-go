---
work_package_id: WP06
title: Immutable Handoff And Independent Review
dependencies:
- WP03
- WP04
requirement_refs:
- FR-002
- FR-007
- FR-010
- FR-011
- FR-017
- FR-018
- FR-019
- NFR-004
- NFR-006
- NFR-008
- NFR-010
- C-003
- C-007
- C-008
planning_base_branch: mission/phase1-authority-proving-ground
merge_target_branch: mission/phase1-authority-proving-ground
branch_strategy: Planning artifacts for this mission were generated on mission/phase1-authority-proving-ground. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into mission/phase1-authority-proving-ground unless the human explicitly redirects the landing branch.
subtasks:
- T024
- T025
- T026
- T027
history: []
agent_profile: implementer-ivan
create_intent:
- internal/controlstore/sqlite/handoff_review.go
- internal/controlstore/sqlite/handoff_review_test.go
- internal/helpercmd/review.go
- internal/scenario/review_test.go
execution_mode: code_change
authoritative_surface: internal/review/
owned_files:
- internal/handoff/**
- internal/review/**
- internal/controlstore/sqlite/handoff_review.go
- internal/controlstore/sqlite/handoff_review_test.go
- internal/helpercmd/review.go
- internal/scenario/review_test.go
tags: []
tracker_refs: []
---

# Work Package Prompt: WP06 - Immutable Handoff And Independent Review

## Objective

Bind judgment to one exact candidate and enforce reviewer independence at the
ActorID level, with verdict and criterion evidence committed atomically.

## Boundaries

Review does not read the implementer's live workspace or moving branch.
Approval means eligible for integration, not done. Git files and tracker
comments are not verdict authority.

### T024 - Create immutable handoff

- Revalidate lease token/generation and WorkRevision eligibility.
- Verify candidate existence, ancestry, lane, and pinned base.
- Persist exact candidate, contributors, criteria/evidence digests, and
  revision identity immutably.

### T025 - Enforce ActorID independence

- Reject a reviewer ActorID present in the handoff contributor set, even from a
  new ProcessID.
- Attribute every attempt to actor/process; if a test-only policy waiver is
  supported, make it explicit and audited.

### T026 - Commit candidate-bound judgment transactionally

- Persist verdict and every criterion result in one SQLite transaction.
- Enforce the initial one-terminal-verdict-slot policy with explicit conflicts.
- Recheck handoff/revision eligibility before commit.

### T027 - Run the handoff/review fault and contention scenarios

- Change the workspace and branch after handoff; prove review still sees the
  exact candidate.
- Exercise `HR-00` through `HR-07`, same-actor refusal, distinct-actor approval
  and rejection, and competing verdict writers.

## Validation

```bash
go test ./internal/handoff ./internal/review ./internal/controlstore/sqlite
go test ./internal/scenario -run Review -count=100
```

## Definition of Done

Judgment is immutable, independently attributable, transactionally durable,
and unable to turn itself into integration or done state.

## Reviewer Guidance

Reject ProcessID-only independence, reads from live workspaces, moving branch
authority, partial criterion writes, Git-stored verdict authority, or approval
that marks the work done.
