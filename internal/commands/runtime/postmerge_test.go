package runtime_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// mergedPair drives the calc mission through implement / review / approve
// / consolidate with the oracle on both sides.
func mergedPair(t *testing.T) *testkit.Pair {
	p := newAnalyzedPair(t)
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	laneCommit(t, p, "lane-a", "feat(WP01): subtract", map[string]string{
		"src/calc.py":            "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n",
		"tests/test_subtract.py": "from src.calc import subtract\n",
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Ready")
	oracleBoth(t, p, "agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--note", "ok")
	oracleBoth(t, p, "agent", "action", "implement", "WP02", "--agent", "claude", "--mission", "$M")
	laneCommit(t, p, "lane-b", "feat(WP02): multiply", map[string]string{
		"src/calc_multiply.py":   "from src.calc import add\n\n\ndef multiply(a, b):\n    return a * b\n",
		"tests/test_multiply.py": "from src.calc_multiply import multiply\n",
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T004", "T005", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Ready")
	oracleBoth(t, p, "agent", "action", "review", "WP02", "--agent", "claude", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "approved", "--mission", "$M", "--agent", "claude", "--note", "ok")
	// all approved: next query -> accept (TRACE 5.21)
	steps(t, p, false, []string{"next", "--agent", "claude", "--mission", "$M", "--json"})
	oracleBoth(t, p, "consolidate", "--mission", "$M", "--yes")
	return p
}

// TestDiffPostMerge replays TRACE 8.2 and 9.10 and the post-merge mission
// lifecycle leaves (close / follow-up / reopen).
func TestDiffPostMerge(t *testing.T) {
	p := mergedPair(t)
	steps(t, p, true,
		[]string{"next", "--agent", "claude", "--mission", "$M", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
	)
	// 9.10: implement an already-done WP (re-creates the lane, then refuses)
	steps(t, p, false, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	compareTree(t, p, ".kittify/workspaces")
	steps(t, p, false,
		[]string{"mission", "follow-up", "$M", "--pr", "7", "--json"},
		[]string{"mission", "follow-up", "$M", "--pr", "7"},
		[]string{"mission", "close", "--mission", "$M", "--json"},
		[]string{"mission", "reopen", "$M", "--reason", "regression", "--json"},
		[]string{"mission", "reopen", "$M", "--reason", "again"},
		[]string{"mission", "close", "--mission", "$M"},
	)
	compareTree(t, p, "kitty-specs", "analysis-report.md")
}
