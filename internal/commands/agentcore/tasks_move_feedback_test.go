package agentcore_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffMoveTaskApprovalFeedbackFile covers #6652/#6654: an approval's
// --review-feedback-file is recorded below the "Approved by" line, an empty
// file or a move that writes no new approval record warns on stderr, a
// non-UTF-8 file is refused with a typed error, and the approval replay
// command shell-quotes a free-text reviewer.
func TestDiffMoveTaskApprovalFeedbackFile(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("fb.md", "**Issue**: bad\n")
		s.WriteFile("empty.md", "  \n")
		s.WriteFile("approve.md", "# Review\r\n\r\nDISTINCTIVE-APPROVAL-FEEDBACK-LINE: alpha() checked.\r\n")
		s.WriteFile("latin1.md", "caf\xe9\n")
	})
	toReview := [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", "$M", "--agent", "rev", "--json"},
	}
	comparePairSteps(t, p, g, o, toReview)
	comparePairSteps(t, p, g, o, [][]string{
		// empty file on a first-pass approval: warned, synthesized body kept
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--review-feedback-file", "empty.md", "--mission", "$M", "--agent", "rev", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "planned", "--review-feedback-file", "fb.md", "--mission", "$M", "--agent", "rev", "--json"},
	})
	comparePairSteps(t, p, g, o, toReview[1:])
	comparePairSteps(t, p, g, o, [][]string{
		// non-UTF-8 feedback is refused on the rejection and approval edges
		{"agent", "tasks", "move-task", "WP01", "--to", "planned", "--review-feedback-file", "latin1.md", "--mission", "$M", "--agent", "rev", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "planned", "--review-feedback-file", "latin1.md", "--mission", "$M", "--agent", "rev"},
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--review-feedback-file", "latin1.md", "--mission", "$M", "--agent", "rev", "--json"},
		// real feedback recorded under the approval line, quoted replay reviewer
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--review-feedback-file", "approve.md", "--approval-ref", "PR-42",
			"--reviewer", "Jane Doe (model-x)", "--mission", "$M", "--agent", "rev", "--json"},
		// approved -> done writes no new approval record: warned on stderr
		{"agent", "tasks", "move-task", "WP01", "--to", "done", "--review-feedback-file", "approve.md", "--done-override-reason", "hotfix",
			"--mission", "$M", "--agent", "rev", "--json"},
	})
	// an arbiter override writes no approval record: warned on stderr
	wp02 := [][]string{
		{"agent", "tasks", "move-task", "WP02", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP02", "--to", "in_progress", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "mark-status", "T004", "T005", "--status", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP02", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP02", "--to", "in_review", "--mission", "$M", "--agent", "rev", "--json"},
	}
	comparePairSteps(t, p, g, o, wp02)
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP02", "--to", "planned", "--review-feedback-file", "fb.md", "--mission", "$M", "--agent", "rev", "--json"},
	})
	comparePairSteps(t, p, g, o, wp02)
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP02", "--to", "approved", "--mission", "$M", "--agent", "arb", "--force", "--note", "[pre_existing_failure] flaky",
			"--review-feedback-file", "approve.md", "--json"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
	})
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}
