package closeout_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// rejectedAcceptedPair reproduces the TRACE phase 5 history on the coord
// topology (WP01 rejected twice with reviewer feedback drafts left untracked
// in the coord worktree, then approved; WP02 approved), then records verdicts
// and accepts with the oracle on both sides.
func rejectedAcceptedPair(t *testing.T) *testkit.Pair {
	t.Helper()
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p)
	oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--input-file", missionfx.AnalysisReport(), "--json")
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	p.Both(func(s *testkit.Sandbox) {
		laneCommit(t, s, "lane-a", map[string]string{
			"src/calc.py":            "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n",
			"tests/test_subtract.py": "from src.calc import subtract\n",
		}, "feat(WP01): subtract")
	})
	feedback := func(s *testkit.Sandbox, n string) string {
		slug := slugOf(t, s)
		rel := filepath.Join(".worktrees", slug+"-coord", "kitty-specs", slug, "tasks", "WP01-subtract-helper", "review-feedback-"+n+".md")
		s.WriteFile(rel, "# Review feedback WP01 (cycle "+n+")\n\n1. Add a docstring to subtract.\n")
		return filepath.Join(s.Dir, rel)
	}
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Ready for review")
	oracleBoth(t, p, "agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M")
	// Cycle 1: in_review -> planned with feedback.
	p.Both(func(s *testkit.Sandbox) {
		fb := feedback(s, "1")
		missionfx.RunOracle(t, s, "agent", "tasks", "move-task", "WP01", "--to", "planned", "--review-feedback-file", fb, "--mission", slugOf(t, s), "--agent", "claude", "--json")
	})
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	p.Both(func(s *testkit.Sandbox) {
		laneCommit(t, s, "lane-a", map[string]string{
			"src/calc.py": "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    \"\"\"Subtract.\"\"\"\n    return a - b\n",
		}, "fix(WP01): docstring")
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Fixed review feedback", "--json")
	oracleBoth(t, p, "agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M")
	// Cycle 2: in_review -> in_progress with feedback.
	p.Both(func(s *testkit.Sandbox) {
		fb := feedback(s, "2")
		missionfx.RunOracle(t, s, "agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--review-feedback-file", fb, "--mission", slugOf(t, s), "--agent", "claude", "--json")
	})
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Docstring added", "--json")
	oracleBoth(t, p, "agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--note", "Review passed: subtract + helper OK", "--json")
	oracleBoth(t, p, "agent", "action", "implement", "WP02", "--agent", "claude", "--mission", "$M")
	p.Both(func(s *testkit.Sandbox) {
		laneCommit(t, s, "lane-b", map[string]string{
			"src/calc_multiply.py":   "from src.calc import add\n\n\ndef multiply(a, b):\n    return a * b\n",
			"tests/test_multiply.py": "from src.calc_multiply import multiply\n",
		}, "feat(WP02): multiply")
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T004", "T005", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Ready for review", "--json")
	oracleBoth(t, p, "agent", "action", "review", "WP02", "--agent", "claude", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "approved", "--mission", "$M", "--agent", "claude", "--note", "Review passed: multiply OK", "--json")
	recordVerdictsAndDocs(t, p, true)
	oracleBoth(t, p, "agent", "mission", "accept", "--mission", "$M", "--json")
	return p
}

// TestDiffConsolidateTrace replays TRACE 7.1-7.4 after a rejection history:
// dry-run, refusal on the untracked reviewer feedback drafts in the coord
// worktree, safe-commit of the residue onto the mission branch, and the
// retried consolidate (lane merges, squash, done transitions, retrospective,
// teardown, flatten), comparing stdout and git effects with the oracle.
func TestDiffConsolidateTrace(t *testing.T) {
	p := rejectedAcceptedPair(t)
	compareGit(t, p)
	comparePair(t, p, "consolidate", "--mission", "$M", "--dry-run", "--json")
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	compareGit(t, p)
	// TRACE 7.3: commit the residue from inside the coord worktree.
	p.Both(func(s *testkit.Sandbox) {
		slug := slugOf(t, s)
		wt := filepath.Join(".worktrees", slug+"-coord")
		base := filepath.Join("kitty-specs", slug, "tasks", "WP01-subtract-helper")
		r := s.Exec(wt, testkit.Oracle(t), "safe-commit",
			filepath.Join(base, "review-feedback-1.md"), filepath.Join(base, "review-feedback-2.md"),
			"-m", "chore("+slug+"): commit runtime mission events and reviewer feedback drafts",
			"--to-branch", "kitty/mission-"+slug, "--json")
		if r.Code != 0 {
			t.Fatalf("safe-commit: %d\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
	})
	compareGit(t, p)
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	compareGit(t, p)
	compareMissionTree(t, p, false)
	p.CompareTree(".kittify/derived", true)
	p.CompareTree(".kittify/workspaces", false)
	comparePair(t, p, "agent", "tasks", "status", "--mission", "$M")
	comparePair(t, p, "review", "--mission", "$M")
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	compareGit(t, p)
}

// TestDiffConsolidateStrategies covers the merge strategy for the
// mission→target step on both topologies (coord through `agent mission
// merge`, whose default strategy is merge). Visible lane history also proves
// the stale-lane auto-rebase (mission merged into the lane worktree, then a
// fast-forward of the mission branch).
func TestDiffConsolidateStrategies(t *testing.T) {
	for _, tc := range []struct{ name, strategy, topo string }{
		{"coord-agent-merge", "", "coord"},
		{"lanes-merge", "merge", "lanes"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var p *testkit.Pair
			if tc.topo == "lanes" {
				p = acceptedPair(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
			} else {
				p = acceptedPair(t)
			}
			if tc.strategy == "" {
				comparePair(t, p, "agent", "mission", "merge", "--mission", "$M", "--dry-run")
				comparePair(t, p, "agent", "mission", "merge", "--mission", "$M")
			} else {
				comparePair(t, p, "consolidate", "--mission", "$M", "--strategy", tc.strategy, "--dry-run", "--json")
				comparePair(t, p, "consolidate", "--mission", "$M", "--strategy", tc.strategy, "--yes")
			}
			compareGit(t, p)
			compareMissionTree(t, p, false)
			p.CompareTree(".kittify/derived", true)
		})
	}
}
