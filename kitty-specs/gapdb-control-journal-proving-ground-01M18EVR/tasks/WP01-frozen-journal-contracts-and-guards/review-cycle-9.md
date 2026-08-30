---
affected_files:
  - internal/architecture/m1_dependency_guard_test.go
cycle_number: 9
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reviewed_at: '2026-08-30T10:06:48Z'
reviewer_agent: reviewer-renata-capability-5
verdict: rejected
wp_id: WP01
---

# WP01 capability-boundary review — cycle 9

Verdict: changes requested.

The cycle-8 repair is effective for every defect it targets. Independent replay
confirmed that direct conversion, interface-return conversion, closure
conversion, and slice/map context-elided value literals now produce
`journal-capability-construction`. Named raw-shape conversions and an alias
conversion are also closed. The exact validator body remains admitted, as do
ordinary conversions/calls, direct validator calls, consumer functions and
interfaces, a holder type, and nil pointer/slice/map consumer declarations.

The complete committed cycle-5 through cycle-7 corpus remains green. Root and
child type-check errors fail closed, a three-level pure sibling DAG is admitted,
and missing-sibling and sibling-cycle fixtures produce `journal-type-check`.

The final bounded adjacent construction audit found three direct forms that
still manufacture `ValidatedJournalV1` outside `ValidateJournalV1` without a
construction violation.

## Blocking issue 1 — parenthesized `new` bypasses built-in recognition

This legal Go declaration type-checks and returns no violation:

```go
func ForgeParenthesizedNew() {
    _ = (new)(ValidatedJournalV1)
}
```

The `CallExpr` check recognizes `new` only when `call.Fun` is directly an
`*ast.Ident`. Unwrap parentheses before resolving the built-in, while retaining
the distinction between a real built-in allocation and an ordinary call. Add
the parenthesized form as a permanent mutant beside the existing direct `new`
mutant and ordinary-call control.

## Blocking issue 2 — typed zero initialization is checked only for the exact top-level type

Both declarations type-check and return no violation:

```go
var forgedArrayZero [1]ValidatedJournalV1

var forgedHolderZero struct {
    Journal ValidatedJournalV1
}
```

Each declaration constructs at least one zero-valued capability. The current
`ValueSpec` rule compares only the explicitly written top-level type with the
exact capability, so positive-length arrays and structs containing the
capability remain invisible.

Resolve the declared variable type and recursively identify zero-initialized
value containment. Positive-length arrays, structs, and named holders that
contain `ValidatedJournalV1` must refuse. Nil-only zero values such as
`*ValidatedJournalV1`, `[]ValidatedJournalV1`, and
`map[string]ValidatedJournalV1` do not instantiate an element and must remain
valid consumer declarations. Add both mutant classes and these nil-carrier
controls.

## Blocking issue 3 — pointer-elided composite literals are not exact-type literals

These legal shorthand literals type-check and return no violation:

```go
func ForgePointerElidedSlice() {
    _ = []*ValidatedJournalV1{{}}
}

func ForgePointerElidedMap() {
    _ = map[string]*ValidatedJournalV1{"x": {}}
}
```

In these contexts the resolved inner `CompositeLit` type is
`*ValidatedJournalV1`, not the exact named value type. Nevertheless, the
shorthand allocates and initializes a `ValidatedJournalV1`, just as the already
rejected explicit `&ValidatedJournalV1{}` form does. Recognize pointer-to-exact
capability composite literals without classifying nil pointer declarations or
ordinary pointer literals as construction. Add both forms as permanent mutants
with the existing explicit-address and ordinary pointer controls.

No reflection, `unsafe`, or other speculative mechanism is implicated. These
are ordinary language constructions inside the bounded audit requested for
this cycle.

## Verification

- `go test ./internal/architecture -count=1 -run 'M1.*(Mutant|Capability|Dependency|Budget|Schemas|Evidence)'` — PASS.
- `go test ./internal/architecture -count=1` — PASS (`12.601s`).
- `go vet ./internal/architecture` — PASS.
- `staticcheck ./internal/architecture` — PASS.
- `gofmt -d` over all four WP-owned files — clean.
- `git diff --check kitty/mission-gapdb-control-journal-proving-ground-01M18EVR..HEAD` — PASS.
- `git show --check dd8e7bd` — PASS.
- Independent cycle-8 replay — PASS for all five reported escapes and the
  exact-validator/unrelated controls.
- Independent cycle-5/6/7 replay — PASS through the committed mutant corpus;
  root/child type-check failures and the pure sibling DAG were also replayed
  directly.
- Bounded adjacent construction campaign — FAIL as expected for the five legal
  reproductions above; named conversions, nested/array value literals,
  function return/assignment contexts, direct `new`, explicit addressing, and
  valid consumers behaved correctly.
- The known long `internal/scenario` campaign was not run, as directed, and is
  not treated as WP01 evidence.

The temporary reviewer probe was removed. No implementation file or probe
remains in the lane worktree.

## Required anti-pattern checklist

1. Dead code: **N/A** — WP01 adds no production API or module.
2. Synthetic-fixture test: **FAIL** — the sole-constructor mutant matrix omits
   legal parenthesized allocation, contained zero initialization, and
   pointer-elided literal construction.
3. Silent empty return: **N/A** — no production path was added.
4. FR coverage: **FAIL** — FR-015/FR-016/NFR-013 sole-constructor exclusivity
   remains bypassable.
5. Frozen surface: **PASS** — cycle-9 implementation changes only the WP-owned
   architecture guard; no contract, production package, or module file changed.
6. Locked decision: **FAIL** — non-authoritative code can still construct the
   frozen validation capability contrary to `control-journal-v1.md`.
7. Shared-file ownership: **PASS** — the implementation diff remains within
   WP01's exclusive owned-file surface; this durable review note is the only
   reviewer-authored artifact.
8. Production fragility: **N/A** — no production panic or fail-loud path was
   added.
