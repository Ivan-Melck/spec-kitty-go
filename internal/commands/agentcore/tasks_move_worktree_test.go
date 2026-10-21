package agentcore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// oracleBoth runs an oracle command (mission handle substituted) in both
// sandboxes as test setup.
func oracleBoth(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	p.Both(func(s *testkit.Sandbox) { runOracle(t, s, subst(args, missionSlug(t, s))...) })
}

// laneWrite writes rel inside the WP01 lane worktree (lane-a) of each side.
func laneWrite(p *testkit.Pair, t *testing.T, rel, content string) {
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(filepath.Join(".worktrees", missionSlug(t, s)+"-lane-a", rel), content)
	})
}

// TestDiffMoveTaskWorktree replays the trace's implement/review loop
// (TRACE 5.1-5.12, F2/F3/F4) with the oracle doing the implement/review
// claims and both sides running move-task.
func TestDiffMoveTaskWorktree(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--input-file", tdPath("calc/analysis-report.md"), "--json")
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	laneWrite(p, t, "src/calc.py", "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n")
	laneWrite(p, t, "tests/test_subtract.py", "from src.calc import subtract\n")
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--note", "Ready for review", "--agent", "claude"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
	})
	compareGit(t, p)
	oracleBoth(t, p, "agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("fb1.md", "**Issue**: missing docstring\n") })
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "planned", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "planned", "--review-feedback-file", "fb1.md", "--mission", "$M", "--agent", "claude", "--json"},
	})
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	laneWrite(p, t, "src/calc.py", "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    \"\"\"Subtract.\"\"\"\n    return a - b\n")
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Fixed", "--no-auto-commit"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Fixed", "--no-auto-commit", "--json"},
	})
	p.Both(func(s *testkit.Sandbox) {
		wt := filepath.Join(s.Dir, ".worktrees", missionSlug(t, s)+"-lane-a")
		s.Exec(wt, "git", "add", "src/calc.py")
		s.Exec(wt, "git", "commit", "-q", "-m", "feat(WP01): docstring")
	})
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Fixed review feedback", "--json"},
	})
	oracleBoth(t, p, "agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("fb2.md", "**Issue**: rename\n") })
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--review-feedback-file", "fb2.md", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "again", "--json"},
	})
	oracleBoth(t, p, "agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M")
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--note", "Review passed", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "status", "--mission", "$M"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
	})
	// analysis-report.md is written by the oracle on both sides with a
	// nondeterministic issue_counts key order: drop it before comparing.
	p.Both(func(s *testkit.Sandbox) {
		os.Remove(filepath.Join(s.Dir, "kitty-specs", missionSlug(t, s), "analysis-report.md"))
	})
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	p.CompareTree(".kittify/derived", true)
	compareGit(t, p)
}
