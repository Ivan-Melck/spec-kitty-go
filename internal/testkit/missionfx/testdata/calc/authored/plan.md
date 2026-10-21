# Implementation Plan: Calculator Operations

**Branch**: `feat/calc-ops` | **Date**: 2026-10-17 | **Spec**: kitty-specs/calc-ops-01M54Y16/spec.md
**Input**: Mission specification from `kitty-specs/calc-ops-01M54Y16/spec.md`

## Summary

Add `subtract` (with a shared `_apply` helper) and `multiply` (built on `_apply`) to `src/calc.py`.

## Technical Context

**Language/Version**: Python 3.11+
**Primary Dependencies**: none (stdlib only)
**Storage**: N/A
**Testing**: plain `python -c` assertions / pytest-compatible test file `tests/test_calc.py`
**Target Platform**: Linux, any CPython
**Project Type**: single
**Performance Goals**: each op < 1 ms
**Constraints**: stdlib only
**Scale/Scope**: one module, two functions

## Charter Check

Skipped: no charter present (`.kittify/charter/charter.md` absent).

## Project Structure

### Documentation (this mission)

```
kitty-specs/calc-ops-01M54Y16/
├── plan.md
├── research.md
├── quickstart.md
├── contracts/calc-api.md
└── tasks.md
```

### Source Code (repository root)

```
src/
└── calc.py
tests/
└── test_calc.py
```

**Structure Decision**: single project; all code in `src/calc.py`.

## Implementation Concern Map

- **IC-01 Subtraction + helper**: introduce `_apply(op, a, b)` and `subtract`.
- **IC-02 Multiplication**: add `multiply` using `_apply`.
