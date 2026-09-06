# WP16 allocation recovery

The first implementation claim was marked blocked when Spec Kitty could not
automatically merge approved dependency lane `lane-n` into `lane-p`. The only
conflict was derived mission state in `status.json`; the dependency product
changes themselves were compatible.

Recovery completed without bypassing a guard:

- merged approved WP14 lane `lane-n` into `lane-p`;
- resolved derived status from the authoritative planning branch;
- merged approved WP15 lane `lane-o` into `lane-p`;
- restored the complete `kitty-specs/` tree from the planning branch so no
  dependency-lane orchestration state became WP16 product input; and
- verified the combined repository with `go test ./... -run '^$'`.

The allocation blocker is resolved. WP16 may return to `planned` and retry the
normal implementation claim.
