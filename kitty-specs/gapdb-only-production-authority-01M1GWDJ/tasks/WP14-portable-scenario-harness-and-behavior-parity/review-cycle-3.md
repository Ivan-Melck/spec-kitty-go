---
affected_files: []
cycle_number: 3
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-05T23:56:28Z'
reviewer_agent: user
wp_id: WP14
---

# Post-approval defect 10: legacy scenario authority remains compiled

## Finding

WP14 added the portable GapDB scenario harness but did not retire the original
SQLite-driven scenario files. `internal/scenario/harness.go` and multiple test
files still import `internal/controlstore/sqlite`, invoke the retired helper
surface with `--db`, and decode legacy `helpercmd.Response` types.

WP15 correctly removes that legacy helper API in favor of the closed native
GapDB command surface. At that boundary, `go test ./...` fails to compile the
remaining WP14-owned scenario package. Preserving legacy response types or a
test-only SQLite command path would conceal the defect and violate the M2
production-authority boundary.

## Required repair

Reopen WP14 and make `internal/scenario/**` one coherent portable GapDB test
surface:

1. remove or replace every compiled legacy SQLite/helper scenario;
2. retain the behavior-parity census and executable invariant coverage already
   accepted for WP14;
3. ensure the package has no SQLite import, SQL execution, raw `--db` helper
   invocation, or legacy helper response dependency;
4. prove `go test ./internal/scenario` and the repository package graph compile
   against the WP15 native command surface; and
5. do not weaken the permanent behavioral/mutant controls to obtain a green
   build.

This is a dependency repair, not a compatibility waiver. WP15 must not carry
the removed Python/SQLite-shaped helper protocol forward, and WP16 must not
delete SQLite while a compiled test consumer remains.
