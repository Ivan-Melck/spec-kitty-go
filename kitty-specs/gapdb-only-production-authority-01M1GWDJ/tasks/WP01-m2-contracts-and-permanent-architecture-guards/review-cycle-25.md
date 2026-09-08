---
affected_files: []
cycle_number: 25
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T04:57:58Z'
reviewer_agent: renata-gapdb-duration-state
wp_id: WP01
---

# WP01 Review Cycle 25 — Changes Requested

Reviewed implementation commit: `3e8b393de3742a15c92c0037d0efd58ca3efdce0`

Cycle 24's inferred variable, typed constant, erased initializer, and
transitive result-container defects are repaired. One blocking type-graph gap
remains.

## Blocker: named non-struct duration containers bypass confinement

`m2RestoreDurationStateConfined` invokes the recursive duration walker for
fields only after a package-scope named type's immediate underlying type has
asserted as `*types.Struct`. Consequently the walker is never applied to
top-level named pointers, slices, maps, channels, arrays, signatures,
interfaces, or generic/union forms.

These independent, type-correct shapes are all incorrectly accepted:

```go
type Leaked *BudgetsV2
type LeakedSlice []BudgetsV2
type LeakedMap map[string]BudgetsV2
type LeakedChan chan BudgetsV2
type LeakedArray [1]BudgetsV2
type LeakedFunc func() BudgetsV2
type LeakedInterface interface { Budget() BudgetsV2 }
type LeakedGeneric[T interface { BudgetsV2 | string }] []T
```

Each must produce `ARCH-DEFERRED-SURFACE target=time.Duration.state`.

## Required repair

- Examine every package-scope `TypeName` through the existing cycle-safe
  `m2RestoreTypeReachesDuration` walker, regardless of its immediate
  underlying kind.
- Preserve only the exact permitted coordinates:
  `BudgetsV2.MaximumDuration time.Duration` and
  `InputV2.Budgets BudgetsV2`.
- Add permanent controls covering at least one named container, function
  signature, interface, and generic/union form.
- Preserve the structural-only boundary; do not add CFG, dominance, caller,
  or arbitrary value-flow inference.

The original cycle-24 defects now reject, exact `InputV2.Budgets` still
accepts, renamed/misplaced equivalents reject, odd control flow and lexical
shadowing remain legal, and the sealed 185-mutant census is unchanged.
