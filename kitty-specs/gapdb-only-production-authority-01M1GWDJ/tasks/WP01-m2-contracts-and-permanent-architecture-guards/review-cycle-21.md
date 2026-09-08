---
affected_files: []
cycle_number: 21
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T03:18:57Z'
reviewer_agent: renata-gapdb-deadline-dataflow-review
wp_id: WP01
---

# WP01 review feedback — cycle 21

Verdict: **REQUEST CHANGES** for
`2326ed86876dc67c36454b427231f2fc991619ca`.

The prior disconnected-use false-green and cross-file alias false-red are fixed.
Renamed type, field, function, and import aliases also pass. Four semantic
blockers remain; each reproduced 20/20 through the production-facing validator.

## Blocker 1 — same-file lexical shadowing false-red

A file importing `t "time"` and later using a function parameter named `t`
with a `t.Now` field is rejected as ambient `time.Now`. Per-file spelling maps
still do not distinguish Go object identity.

Required repair: resolve imported packages, selectors, and lexical shadowing
with `go/types` object identity. Add this exact valid fixture as a permanent
positive control.

## Blocker 2 — branch-only validation need not dominate the deadline

Validation inside `if enabled { ... }`, followed by an unconditional
duration-backed `context.WithTimeout`, passes even when `enabled == false`
bypasses validation.

Required repair: validation must dominate every admitted path to the deadline.
Add the branch-only/non-dominating fixture as a permanent negative control.

## Blocker 3 — one unused good caller masks an unvalidated caller

The proof is existential across callers. One unused caller that validates
before invoking the deadline helper makes the package pass even when another
caller invokes the helper without validation.

Required repair: prove every reachable/admitted call path to the deadline is
validated, not merely that one matching caller exists. Add the mixed-callers
fixture as a permanent negative control.

## Blocker 4 — an empty admissible duration interval passes

Checks equivalent to `duration <= 0 || duration > 0*time.Minute` count as a
positive bounded range, although no positive duration can pass.

Required repair: evaluate the upper-bound expression and require a genuinely
positive, non-empty, finite interval. Add the zero-upper-bound fixture as a
permanent negative control.

## Evidence that remains valid

- Actual WP04 combined-tree deadline architecture and functional timeout gates
  pass repeatedly and under race.
- The exact restore-only import edge remains narrow.
- The sealed 185-mutant census preserves all prior 184 identities.
- Full architecture, compile, vet, staticcheck, module, formatting, diff, and
  ownership gates pass.
- The change remains inside WP01's owned architecture surface.

Prefer a small semantic model based on `go/types` plus universal/dominating
call-path validation over additional spelling/counting exceptions. Do not
rewrite WP04's sound restore implementation.
