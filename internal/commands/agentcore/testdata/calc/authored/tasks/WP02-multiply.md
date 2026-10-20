---
work_package_id: "WP02"
subtasks: ["T004", "T005"]
title: "Multiplication"
task_type: "implement"
phase: "Phase 1 - Core"
dependencies: ["WP01"]
requirement_refs: ["FR-002"]
execution_mode: "code_change"
owned_files: ["src/calc_multiply.py", "tests/test_multiply.py"]
authoritative_surface: "src/calc_multiply.py"
create_intent: ["src/calc_multiply.py", "tests/test_multiply.py"]
agent_profile: "python-pedro"
role: "implementer"
agent: "claude"
model: ""
assignee: ""
shell_pid: ""
history:
  - at: "2026-10-17T12:10:21Z"
    actor: "system"
    action: "Prompt generated via /spec-kitty.tasks"
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
