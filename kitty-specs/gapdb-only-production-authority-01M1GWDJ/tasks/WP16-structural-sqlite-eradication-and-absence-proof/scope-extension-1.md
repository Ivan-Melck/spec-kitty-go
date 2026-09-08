# WP16 scope extension 1: retire stale architecture fixtures

## Trigger

Deleting `internal/controlstore/sqlite` and removing SQLite from the production
architecture policy exposed frozen architecture tests that still treated that
package and `modernc.org/sqlite` as an allowed adapter. Leaving those fixtures
unchanged would make the intended structural-absence invariant impossible to
state honestly.

## Authorized extension

WP16 may make the smallest necessary changes to:

- `internal/architecture/m2_dependency_guard_test.go`
- `internal/architecture/m2_mutant_control_test.go`
- `internal/architecture/m2_registry_test.go`, if its frozen registry digest
  changes as a direct result

The extension is limited to removing the retired SQLite allowance, replacing
stale positive fixtures with GapDB-native equivalents, and rebaselining frozen
digests whose covered source changed for that reason.

## Required preservation

- No unrelated architecture rule may be weakened or removed.
- The architecture suite must contain a positive mutant proving that a
  resurrected SQLite adapter or import is rejected.
- Frozen census counts remain unchanged unless the reviewed mutation registry
  itself gains or loses a deliberate case.
- All existing architecture tests and mutant controls must pass, not merely the
  new WP16 absence tests.

## Review expectation

Independent review must inspect this extension specifically for exemption
broadening, weakened dependency directions, stale digest masking, and a mutant
that could survive while SQLite executable authority is present.
