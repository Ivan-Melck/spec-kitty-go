# Work Packages: Calculator Operations

**Inputs**: kitty-specs/calc-ops-01M54Y16/spec.md, plan.md
**Branch**: planning base `feat/calc-ops`, merge target `feat/calc-ops`

## Subtask Index

| ID | Description | WP | Parallel |
|----|-------------|----|----------|
| T001 | Add `_apply` helper to src/calc.py | WP01 | |
| T002 | Add `subtract` to src/calc.py | WP01 | |
| T003 | Add tests/test_subtract.py | WP01 | [P] |
| T004 | Add src/calc_multiply.py with `multiply` using `_apply` | WP02 | |
| T005 | Add tests/test_multiply.py | WP02 | [P] |

## Work Package WP01: Subtraction and shared helper (Priority: P1)

**Goal**: FR-001. **Independent test**: `subtract(5, 3) == 2`.
**Prompt**: `tasks/WP01-subtract-helper.md`

T001 Add `_apply` helper to src/calc.py (WP01)
T002 Add `subtract` to src/calc.py (WP01)
T003 Add tests/test_subtract.py (WP01)

**Dependencies**: none.

## Work Package WP02: Multiplication (Priority: P2)

**Goal**: FR-002. **Independent test**: `multiply(4, 3) == 12`.
**Prompt**: `tasks/WP02-multiply.md`

T004 Add src/calc_multiply.py with `multiply` using `_apply` (WP02)
T005 Add tests/test_multiply.py (WP02)

**Dependencies**: Depends on WP01 (uses `_apply`).
