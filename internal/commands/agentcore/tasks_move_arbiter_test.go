package agentcore_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffMoveTaskArbiter covers the arbiter override (forced planned ->
// approved over a review rejection) and the --skip-review-artifact-check
// override persistence.
func TestDiffMoveTaskArbiter(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("fb.md", "**Issue**: bad\n") })
	var steps [][]string
	for _, wpID := range []string{"WP01", "WP02"} {
		steps = append(steps,
			[]string{"agent", "tasks", "move-task", wpID, "--to", "claimed", "--mission", "$M", "--agent", "claude", "--json"},
			[]string{"agent", "tasks", "move-task", wpID, "--to", "in_progress", "--mission", "$M", "--agent", "claude", "--json"},
		)
	}
	steps = append(steps,
		[]string{"agent", "tasks", "mark-status", "T001", "T002", "T003", "T004", "T005", "--status", "done", "--mission", "$M", "--json"},
	)
	for _, wpID := range []string{"WP01", "WP02"} {
		steps = append(steps,
			[]string{"agent", "tasks", "move-task", wpID, "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
			[]string{"agent", "tasks", "move-task", wpID, "--to", "in_review", "--mission", "$M", "--agent", "rev", "--json"},
			[]string{"agent", "tasks", "move-task", wpID, "--to", "planned", "--review-feedback-file", "fb.md", "--mission", "$M", "--agent", "rev", "--json"},
		)
	}
	steps = append(steps,
		[]string{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "arb", "--json"},
		[]string{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "arb", "--skip-review-artifact-check", "--json"},
		[]string{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "arb", "--force", "--note", "[pre_existing_failure] flaky upstream test"},
		[]string{"agent", "tasks", "status", "--mission", "$M", "--json"},
		[]string{"agent", "tasks", "move-task", "WP02", "--to", "approved", "--mission", "$M", "--force", "--skip-review-artifact-check", "--note", "[cross_scope]", "--json"},
		[]string{"agent", "tasks", "status", "--mission", "$M", "--json"},
		[]string{"agent", "tasks", "status", "--mission", "$M"},
		[]string{"agent", "tasks", "move-task", "WP01", "--to", "done", "--mission", "$M", "--done-override-reason", "hotfix", "--json"},
		[]string{"agent", "tasks", "status", "--mission", "$M"},
		[]string{"agent", "status", "emit", "WP02", "--to", "approved", "--actor", "x", "--force", "--reason", "op", "--mission", "$M", "--json"},
	)
	comparePairSteps(t, p, g, o, steps)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	p.CompareTree(".kittify/derived", true)
	compareGit(t, p)
}
