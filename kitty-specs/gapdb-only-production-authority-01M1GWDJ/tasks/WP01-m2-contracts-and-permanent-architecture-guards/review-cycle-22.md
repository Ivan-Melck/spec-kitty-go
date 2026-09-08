---
affected_files: []
cycle_number: 22
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T03:43:55Z'
reviewer_agent: renata-gapdb-deadline-semantic-review
wp_id: WP01
---

# WP01 review feedback — cycle 22

Verdict: **REQUEST CHANGES** for
`76f43ac8c6351d523a23a8bdbe81cb6d26425138`.

The object-identity, constant-bound, prior alias, disconnected-use, branch-only,
mixed-caller, and zero-upper repairs are effective. Four localized
control-flow cases remain; each reproduced 20/20 through the production-facing
validator.

## Blocker 1 — compound boolean masking false-green

`if validate(limits) != nil && enabled { return }` is treated as an exact
validation gate. When `enabled` is false, invalid limits fall through to the
attempt, yet the validator reports success.

Required repair: recognize only an exact/simple validator-failure condition,
or prove implication. A validator comparison nested in `&&` or `||` must not
mark fallthrough as validated. Add the masking fixture as a permanent negative
control.

## Blocker 2 — function-valued calls escape universal analysis

One validated direct caller plus another caller using `invoke := attempt;
invoke(ctx, limits)` passes because the indirect call resolves to a variable
rather than the deadline function.

Required repair: analyze function-value references to deadline helpers, or
fail closed when such a reference is reachable but not analyzed. Add the
direct-good/indirect-bad fixture as a permanent negative control.

## Blocker 3 — validator-success state is not propagated into `else`

A valid `if validate(limits) != nil { return } else { attempt(ctx, limits) }`
shape is rejected even though the attempt is dominated by successful
validation.

Required repair: propagate validator-success state into the appropriate else
branch. Add the valid else-dominated shape as a permanent positive control.

## Blocker 4 — inline dominating guards are falsely rejected

Safe top-level lower/upper invalid-bound returns followed by the deadline in
the same function are rejected. Exported and recursive self-validating helpers
share the same root failure.

Required repair: recognize direct top-level inline lower/upper guards as
dominating validation before the deadline. Add positive controls for inline,
exported self-validating, and recursive self-validating shapes.

## Evidence that remains valid

- The real WP04 combined tree and timeout behavior pass repeatedly and under
  race.
- The exact restore-only edge and sealed 185-mutant census remain intact.
- Full architecture, compile, vet, staticcheck, module, formatting, diff, and
  ownership gates pass.
- Only WP01's architecture guard changed.

Keep this repair inside the existing semantic walker. Do not weaken object
identity, positive interval evaluation, universal callers, or rewrite WP04.
