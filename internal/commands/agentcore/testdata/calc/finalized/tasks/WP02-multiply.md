---
work_package_id: WP02
title: Multiplication
dependencies:
- WP01
requirement_refs:
- FR-002
planning_base_branch: feat/calc-ops
merge_target_branch: feat/calc-ops
branch_strategy: Planning artifacts for this mission were generated on feat/calc-ops. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/calc-ops unless the human explicitly redirects the landing branch.
subtasks:
- T004
- T005
phase: Phase 1 - Core
history:
- at: '2026-10-17T12:10:21Z'
  actor: system
  action: Prompt generated via /spec-kitty.tasks
agent_profile: python-pedro
authoritative_surface: src/calc_multiply.py
create_intent:
- src/calc_multiply.py
- tests/test_multiply.py
execution_mode: code_change
model: ''
owned_files:
- src/calc_multiply.py
- tests/test_multiply.py
role: implementer
tags: []
task_type: implement
tracker_refs: []
---

# Work Package Prompt: WP02 – Multiplication

## ⚡ Do This First: Load Agent Profile

Use the `/spk-charter-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `python-pedro`
- **Role**: `implementer`
- **Agent/tool**: `claude`

## Objectives & Success Criteria

Add src/calc_multiply.py with `multiply(a, b)` built on `calc._apply`; add tests/test_multiply.py.

## Branch Strategy

- **Strategy**: lanes (computed by finalize-tasks, see lanes.json)
- **Planning base branch**: feat/calc-ops
- **Merge target branch**: feat/calc-ops

## Implementation command

```bash
spec-kitty agent action implement WP02 --agent claude --mission calc-ops-01M54Y16
```

## Definition of Done

- Code and tests committed in the lane worktree.
- Tests pass with `python -m pytest -q` or plain asserts.
