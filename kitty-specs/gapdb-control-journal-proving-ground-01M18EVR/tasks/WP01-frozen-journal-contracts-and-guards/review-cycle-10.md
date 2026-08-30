---
affected_files: []
cycle_number: 10
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command:
reviewed_at: '2026-08-30T10:24:59Z'
reviewer_agent: reviewer-renata-zero-safe
wp_id: WP01
---

# WP01 zero-safe capability review — cycle 10

Verdict: changes requested.

The zero-safe correction is effective. The guard now admits legal zero values,
manual same-package literals, conversions, `new`, arrays, structs, slices,
maps, pointer-elided literals, containers, and ordinary copies. Independent
adjacent probes also confirmed that the exact direct seal/capability fields,
empty value error type, value `Error() string`, error-returning accessors, root
and child type checking, recursive purity, alternate authority census, and
SQLite/GapDB boundaries behave as intended. WP02 remains the owner of runtime
seal validation and refusal semantics.

## Blocking issue — generic free functions satisfy the non-generic frozen signature

`m1RequireFunction` and `m1ExactValidatorFunction` compare receiver,
variadicness, parameters, and results, but neither rejects function type
parameters. This legal source therefore type-checks and produces no
`journal-validator` violation:

```go
func ValidateJournalV1[T any](
    registry EventRegistryV1,
    events []StoredEventV1,
) (ValidatedJournalV1, error) {
    return ValidatedJournalV1{}, nil
}
```

It is not the frozen exact function: callers cannot invoke it through the
ratified non-generic API without supplying or inferring an otherwise unused
type argument. Because `m1ExactValidatorFunction` also accepts it, the callable
authority census incorrectly treats the generic declaration as the
authoritative validator.

Require zero function type parameters in both the exact free-function guard and
the exact-validator resolver. Add permanent mutants for generic
`ValidateJournalV1[T any]` and generic `NewEventRegistryV1[T any]`, retaining
the existing non-generic fixture as the green control. This is a bounded
structural fix; do not restore capability-construction policing or add WP02
runtime semantic probes.

## Verification

- Focused M1 architecture qualification — PASS.
- `go test ./internal/architecture -count=1` — PASS (`12.670s`).
- `go vet ./internal/architecture` — PASS.
- `staticcheck ./internal/architecture` — PASS.
- `gofmt -d` over all four WP-owned files — clean.
- `git diff --check` over the WP diff — PASS.
- Bounded adjacent seal/error/accessor campaign — PASS except for the generic
  validator escape above.
- Legal zero/manual/conversion/container/copy controls — PASS.
- No long `internal/scenario` campaign was run, as directed.
- The temporary reviewer probe was removed; no probe file remains.

## Required anti-pattern checklist

1. Dead code: **N/A** — WP01 adds no production API or module.
2. Synthetic-fixture test: **FAIL** — the exact-signature mutant matrix omits
   generic forms that preserve parameters/results while changing the API.
3. Silent empty return: **N/A** — no production path was added.
4. FR coverage: **FAIL** — the exact frozen validation boundary remains
   bypassable by a generic declaration.
5. Frozen surface: **PASS** — implementation changes remain in WP01-owned
   architecture tests; frozen contracts and module files were not changed by
   the implementation.
6. Locked decision: **FAIL** — the guard accepts a function that is not the
   contract's exact non-generic `ValidateJournalV1` declaration.
7. Shared-file ownership: **PASS** — the lane is exclusive to WP01; prior
   review notes are governed review artifacts.
8. Production fragility: **N/A** — no production panic or fail-loud path was
   added.
