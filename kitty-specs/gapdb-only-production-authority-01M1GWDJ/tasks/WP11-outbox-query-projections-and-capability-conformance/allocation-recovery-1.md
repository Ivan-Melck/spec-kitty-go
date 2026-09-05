# WP11 allocation recovery

**Issue:** The allocator marked WP11 blocked after dependency merges conflicted
only on generated mission-state files. The dependency product commits were
merged into lane-k and the current coordination authority was retained for the
generated files.

**Recovery:** Return WP11 to planned and retry the normal implementation action.
Do not discard or recreate lane-k; its explicit dependency merge commits are
the evidence that the prerequisite product histories were admitted.
