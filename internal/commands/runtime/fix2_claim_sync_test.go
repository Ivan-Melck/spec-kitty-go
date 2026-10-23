package runtime_test

import "testing"

// TestDiffReviewForceMovedUnimplemented: a WP force-moved to for_review
// without ever being implemented. The review claim commits, then the lane
// sync fails (no lane branch): upstream reverts the claim commit, refuses
// to cut the rows and reports the committed claim.
func TestDiffReviewForceMovedUnimplemented(t *testing.T) {
	p := newAnalyzedPair(t)
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "for_review", "--force", "--mission", "$M", "--agent", "claude", "--note", "forced")
	steps(t, p, true, []string{"agent", "action", "review", "WP02", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	dropAnalysis(t, p)
	compareTree(t, p, "kitty-specs")
}
