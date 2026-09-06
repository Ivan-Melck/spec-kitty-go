# Post-approval defect 12 — executable validation still constructs SQLite

WP16's mandatory deletion preflight found that the approved portable-scenario
surface is not yet substrate-independent:

- `internal/tracker/beads/mission_method_integration_test.go` imports and
  constructs `internal/controlstore/sqlite`;
- `internal/tracker/beads/real_integration_test.go` imports and constructs the
  same package; and
- `Makefile` names `./internal/controlstore/sqlite` in the ordinary race gate.

These are executable/test dependency edges, not historical documentation.
Deleting the SQLite package or `modernc.org/sqlite` while they remain would
break the repository and would falsify WP14's portable behavior-parity claim.

## Required repair

1. Port both Beads integration tests to the admitted GapDB-backed production or
   domain authority seam without weakening their tracker/provider assertions.
2. Remove the SQLite package from the Makefile race target and replace it with
   the corresponding GapDB/native authority packages where coverage would
   otherwise be lost.
3. Prove `go list -deps -test ./...` has no test import of
   `internal/controlstore/sqlite` outside the package that WP16 will delete.
4. Re-run the affected Beads tests, relevant race slice, package compilation,
   vet, staticcheck, and the WP14 parity/architecture guards.
5. Independently review the repair before WP16 resumes deletion.

This repair is deliberately prior to WP16 deletion. No exemption, stub,
test-only driver, or disabled fallback is acceptable.
