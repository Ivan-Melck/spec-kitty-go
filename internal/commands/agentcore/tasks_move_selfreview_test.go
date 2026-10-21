package agentcore_test

import "testing"

// TestDiffMoveTaskSelfReviewFallback covers the --self-review-fallback
// option validation and its ReviewerSelfApproval lifecycle event.
func TestDiffMoveTaskSelfReviewFallback(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--self-review-fallback", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--self-review-fallback", "--intended-reviewer", "codex", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--self-review-fallback", "--force", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--self-review-fallback", "--force", "--intended-reviewer", "codex", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--self-review-fallback", "--intended-reviewer", "codex", "--reviewer-failure-reason", "quota exhausted", "--force", "--note", "reviewer down", "--json"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
		{"agent", "status", "validate", "--mission", "$M", "--json"},
	})
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}
