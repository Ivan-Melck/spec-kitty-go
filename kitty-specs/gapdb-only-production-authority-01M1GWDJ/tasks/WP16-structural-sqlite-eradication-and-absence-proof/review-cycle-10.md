---
affected_files: []
cycle_number: 10
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T05:19:03Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 10

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

1. Same-statement `case` actions are discarded when the command-position model
   sees `case` and skips the remaining pattern/action tokens. A tracked
   forbidden command in `case native in native) "$variable" ... ;; esac`
   therefore escapes.
2. POSIX utility-dispatch builtins are not modeled. `command "$variable" ...`
   treats the utility name as a data argument, so the tracked forbidden command
   escapes. Equivalent supported dispatch positions must be explicit or fail
   closed.

Preserve tokens through `case` header/pattern close into action command state.
Model the utility-name positions for supported `command`, `exec`, and
environment/wrapper forms, including bounded option/assignment prefixes; any
unclassified wrapper that can dispatch a tracked variable must yield typed
ambiguity.

## Preserve

- The complete reserved-word/list/pipeline/grouping command-position matrix.
- Static word resolution, ordered assignment state, and unresolved-command
  ambiguity.
- Data-only arguments, case patterns, comments, horizons, and budgets remain
  non-executable unless they genuinely reach command position.
- Exact sidecar admission, 23 observed runtime receipts, closed 35-artifact
  inventory, source identity, and all per-class digests remain verifiable.
- All prior architecture/race/parity/build/tidy/vet/static evidence stays green.

## Required re-review evidence

- Kill exact same-line and multiline `case ... pattern) "$variable"` actions.
- Kill `command "$variable"`, `exec "$variable"`, and supported wrapper
  utility-name positions, including options/leading environment assignments.
- Preserve plain argument uses and non-selected case-pattern data as clean.
- Qualify the exact final integrated tree and recompute the observed runtime and
  complete manifest/inventory tuple.
