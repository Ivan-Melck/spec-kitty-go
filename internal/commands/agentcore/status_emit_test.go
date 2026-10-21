package agentcore_test

import "testing"

func TestDiffStatusEmit(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "status", "emit", "WP01", "--to", "claimed", "--actor", "claude", "--mission", "$M", "--json"},
		{"agent", "status", "emit", "--wp", "wp01", "--to", "in_progress", "--actor", "claude", "--mission", "$M"},
		{"agent", "status", "emit", "WP01", "--wp", "WP02", "--to", "done", "--actor", "claude", "--mission", "$M", "--json"},
		{"agent", "status", "emit", "--to", "done", "--actor", "claude", "--mission", "$M", "--json"},
		{"agent", "status", "emit", "WPX", "--to", "done", "--actor", "claude", "--mission", "$M"},
		{"agent", "status", "emit", "WP01", "--to", "for_review", "--actor", "claude", "--mission", "$M", "--json"},
		{"agent", "status", "emit", "WP01", "--to", "for_review", "--actor", "claude", "--mission", "$M"},
		{"agent", "status", "emit", "WP01", "--to", "for_review", "--actor", "claude", "--mission", "$M", "--force", "--reason", "nothing to commit", "--json"},
		{"agent", "status", "emit", "WP01", "--to", "in_review", "--actor", "rev", "--mission", "$M"},
		{"agent", "status", "emit", "WP01", "--to", "approved", "--actor", "rev", "--mission", "$M", "--json"},
		{"agent", "status", "emit", "WP01", "--to", "approved", "--actor", "rev", "--mission", "$M", "--review-result-json", "{bad", "--json"},
		{"agent", "status", "emit", "WP01", "--to", "approved", "--actor", "rev", "--mission", "$M", "--review-result-json", `{"reviewer": "alice", "verdict": "maybe", "reference": "PR#1"}`},
		{"agent", "status", "emit", "WP01", "--to", "approved", "--actor", "rev", "--mission", "$M", "--review-result-json", `{"reviewer": "alice", "verdict": "approved", "reference": "PR#1"}`, "--json"},
		{"agent", "status", "emit", "WP01", "--to", "done", "--actor", "rev", "--mission", "$M", "--evidence-json", "{bad"},
		{"agent", "status", "emit", "WP01", "--to", "done", "--actor", "rev", "--mission", "$M", "--evidence-json", `{"review": {"reviewer": "alice"}}`, "--json"},
		{"agent", "status", "emit", "WP01", "--to", "done", "--actor", "rev", "--mission", "$M", "--evidence-json", `{"review": {"reviewer": "alice", "verdict": "approved", "reference": "PR#1"}}`, "--json"},
		{"agent", "status", "emit", "WP02", "--to", "bogus", "--actor", "x", "--mission", "$M", "--json"},
		{"agent", "status", "emit", "WP02", "--to", "claimed", "--actor", "x", "--mission", "nonexist", "--json"},
		{"agent", "status", "emit", "WP02", "--to", "doing", "--actor", "x", "--mission", "$M", "--json"},
		{"agent", "status", "emit", "WP02", "--to", "blocked", "--actor", "x", "--mission", "$M", "--json"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
	})
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	p.CompareTree(".kittify/derived", true)
	compareGit(t, p)
}
