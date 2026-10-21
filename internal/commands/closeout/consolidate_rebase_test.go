package closeout_test

import "testing"

// TestDiffConsolidateRebase covers --strategy rebase when the target moved
// after the lanes forked (the docs/contracts commit lands on the target after
// the lane worktrees were cut): the rebase rewrites approved lane commits,
// the reconciliation refuses and the branches are rolled back.
func TestDiffConsolidateRebase(t *testing.T) {
	p := acceptedPair(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	comparePair(t, p, "consolidate", "--mission", "$M", "--strategy", "rebase", "--yes")
	compareGit(t, p)
	comparePair(t, p, "consolidate", "--mission", "$M", "--strategy", "rebase", "--yes")
	compareGit(t, p)
}
