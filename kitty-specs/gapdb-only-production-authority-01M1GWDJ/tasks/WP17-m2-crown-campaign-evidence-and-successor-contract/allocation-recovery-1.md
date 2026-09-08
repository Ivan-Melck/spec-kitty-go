# WP17 allocation recovery 1

## Blocker

The first WP17 allocation attempt could not automatically merge approved
dependency lane-p into lane-q because the generated `status.json` projection
conflicted. Spec Kitty recorded WP17 as blocked before any implementation work
began.

## Repair

The approved lane-p branch was manually merged into lane-q. The only unresolved
path was the derived mission status projection; it was restored from the
authoritative feature branch with sparse-checkout-aware Git operations. The
merge was committed as `4a1eed0b0f7a3d4223fbb06b15bd48872bffb2d5`.

Lane-q is clean and contains the complete approved WP16 ancestry. No product
file was manually resolved, no guard was disabled, and no WP17 implementation
had started.

## Authorized recovery

Return WP17 from allocator-created `blocked` state to `planned`, preserving
this record as the required rationale, then rerun the same
`spec-kitty agent action implement WP17` command.
