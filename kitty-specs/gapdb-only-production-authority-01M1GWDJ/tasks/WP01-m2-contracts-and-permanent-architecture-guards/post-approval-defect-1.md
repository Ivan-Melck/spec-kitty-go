# WP01 post-approval architecture defect

The first downstream production packages exposed a gap in WP01's permanent
architecture guard. `m2ClosedPolicyValue` classified the closed M2 package set
only in a test-local policy overlay, while the repository-wide
`DefaultPolicy` remained unaware of those packages. Consequently the ordinary
architecture and helper-command gates reject valid `internal/controljournal/v2`
and `internal/controlroot` packages with `ARCH-CLASS-UNKNOWN`.

This is a genuine WP01 defect, not a reason to weaken the fail-closed rule or
grant downstream WPs overlapping ownership of the global registry. Reopen WP01
for one bounded correction:

- extend WP01 ownership to `internal/architecture/policy.go` and
  `internal/architecture/policy_test.go`;
- move the exact, slash-bounded M2 package classification into
  `DefaultPolicy` before downstream packages exist;
- retain unknown-package, look-alike, direction, adapter-isolation, and mutant
  controls; and
- prove both current repository admission and rejection of unregistered
  production packages through the ordinary architecture gate.

Downstream implementation remains unchanged while WP01 is repaired and
independently re-reviewed.
