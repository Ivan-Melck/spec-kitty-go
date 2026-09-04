---
affected_files: []
cycle_number: 20
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T02:55:28Z'
reviewer_agent: renata-gapdb-deadline-guard-review
wp_id: WP01
---

# WP01 review feedback — cycle 20

Verdict: **REQUEST CHANGES** for `67a0a3082a773ed53ca3e2bf3604a86b093fa806`.

## Blocker 1 — disconnected deadline evidence is accepted

`m2RestoreDeadlineSourceViolations` counts `time.Duration` selectors and
`context.WithTimeout` calls independently. It does not prove that the timeout
used by the restore attempt is derived from the validated elapsed-attempt
budget.

A type-correct fixture passed the real `m2EvaluateClosedGraph` validator 20/20
while its restore attempt discarded `Budgets.Maximum`, an unrelated function
called `context.WithTimeout`, and a decorative variable supplied the only
`time.Duration` use. This violates the defect-7 requirement that deleting the
elapsed-attempt budget must fail closed.

Required repair:

- resolve the actual `context.WithTimeout` call used by the restore attempt;
- require its duration argument to depend on the bounded restore-attempt
  duration input; and
- add a permanent negative control reproducing the disconnected-use fixture.

## Blocker 2 — file-scoped aliases are treated as package-scoped

The validator accumulates import aliases across the package, but Go aliases are
file-scoped. A valid two-file fixture imported `t "time"` in `restore.go`, then
used an unrelated local `t.Now` field selector in `diagnostic.go`. The validator
falsely emitted an `ARCH-DEFERRED-SURFACE` violation for `time.Now`. This
reproduced 20/20 and makes harmless identifier trivia affect acceptance.

Required repair:

- resolve `time` and `context` references independently for each file,
  preferably using typed object identity rather than package-wide spelling
  maps; and
- add a permanent positive control for cross-file alias shadowing.

## Evidence that remains valid

- The exact `restore -> time` edge is narrow; foundation, checkpoint, playback,
  nested restore, and sibling v2 packages are rejected.
- The actual WP04 combined tree and its functional deadline behavior pass
  focused tests 20 times and race tests 5 times.
- The sealed census contains 185 mutants, preserves the previous 184 IDs, and
  appends only `M-PURE-TIME-RESTORE-OWNER-WIDENING`.
- Full architecture, mutant race, compile, vet, staticcheck, module,
  formatting, diff, and ownership gates pass.
- Only the two WP01-owned architecture files changed.

The repair should remain confined to WP01's architecture guard and tests. Do
not rewrite WP04's currently sound restore implementation.
