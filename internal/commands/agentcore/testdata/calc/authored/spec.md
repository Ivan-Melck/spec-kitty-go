# Mission Specification: Calculator Operations

**Mission Branch**: `feat/calc-ops`
**Created**: 2026-10-17
**Status**: Draft
**Input**: User description: "Add subtract and multiply to the calc module"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Subtract two numbers (Priority: P1)

A library user calls `calc.subtract(a, b)` and receives `a - b`.

**Why this priority**: Subtraction is the most-requested missing operation.

**Independent Test**: `python -c "from src.calc import subtract; assert subtract(5, 3) == 2"`.

**Acceptance Scenarios**:

1. **Given** the calc module, **When** `subtract(5, 3)` is called, **Then** it returns `2`.

---

### User Story 2 - Multiply two numbers (Priority: P2)

A library user calls `calc.multiply(a, b)` and receives `a * b`; implemented on top of a shared helper introduced alongside subtract.

**Why this priority**: Depends on the helper delivered with subtraction.

**Independent Test**: `python -c "from src.calc import multiply; assert multiply(4, 3) == 12"`.

**Acceptance Scenarios**:

1. **Given** the calc module, **When** `multiply(4, 3)` is called, **Then** it returns `12`.

### Edge Cases

- Non-numeric arguments follow plain Python semantics (decision 01M54W7J7Z83PVME73HT0KM6XC: python-semantics).

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status | Delivery | No-op passable? |
|----|-------|------------|----------|--------|----------|-----------------|
| FR-001 | Subtract operation | As a library user, I want `subtract(a, b)` so that I can compute differences. | High | Open | [build] | no |
| FR-002 | Multiply operation | As a library user, I want `multiply(a, b)` so that I can compute products. | Medium | Open | [build] | no |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Fast | Each operation completes in under 1 ms for int inputs | Performance | Low | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Stdlib only | No third-party dependencies | Technical | High | Open |

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Both operations return correct results for the acceptance scenarios — [build] · no-op passable: no
