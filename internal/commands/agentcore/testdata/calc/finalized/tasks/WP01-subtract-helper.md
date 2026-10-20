---
work_package_id: WP01
title: Subtraction and shared helper
dependencies: []
requirement_refs:
- FR-001
- SC-001
planning_base_branch: feat/calc-ops
merge_target_branch: feat/calc-ops
branch_strategy: Planning artifacts for this mission were generated on feat/calc-ops. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/calc-ops unless the human explicitly redirects the landing branch.
subtasks:
- T001
- T002
- T003
phase: Phase 1 - Core
history:
- at: '2026-10-17T12:10:21Z'
  actor: system
  action: Prompt generated via /spec-kitty.tasks
agent_profile: python-pedro
authoritative_surface: src/calc.py
create_intent:
- tests/test_subtract.py
execution_mode: code_change
model: ''
owned_files:
- src/calc.py
- tests/test_subtract.py
role: implementer
tags: []
task_type: implement
tracker_refs: []
---

# Work Package Prompt: WP01 – Subtraction and shared helper

## ⚡ Do This First: Load Agent Profile

Use the `/spk-charter-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `python-pedro`
- **Role**: `implementer`
- **Agent/tool**: `claude`

## Objectives & Success Criteria

Add `_apply(op, a, b)` and `subtract(a, b)` to src/calc.py; add tests/test_subtract.py.

## Branch Strategy

- **Strategy**: lanes (computed by finalize-tasks, see lanes.json)
- **Planning base branch**: feat/calc-ops
- **Merge target branch**: feat/calc-ops

## Implementation command

```bash
spec-kitty agent action implement WP01 --agent claude --mission calc-ops-01M54Y16
```

## Definition of Done

- Code and tests committed in the lane worktree.
- Tests pass with `python -m pytest -q` or plain asserts.
