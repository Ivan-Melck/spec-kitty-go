package agentcore_test

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var staleMinutes = regexp.MustCompile(`("minutes_since_commit": |stale: )[0-9]+\.[0-9]+`)

// TestDiffTasksStatusStaleness covers in_progress staleness (lane worktree
// with an old own commit), the in_review stall label and their human
// rendering. Elapsed minutes are masked (they differ by the seconds between
// the two runs).
func TestDiffTasksStatusStaleness(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--input-file", tdPath("calc/analysis-report.md"), "--json")
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	compare := func(args ...string) {
		t.Helper()
		gr := p.Go.Run(subst(args, g)...)
		or := p.Oracle.RunOracle(subst(args, o)...)
		gs := staleMinutes.ReplaceAllString(p.Go.Normalize(gr.Stdout), "${1}<MIN>")
		os_ := staleMinutes.ReplaceAllString(p.Oracle.Normalize(or.Stdout), "${1}<MIN>")
		if gr.Code != or.Code || gs != os_ {
			t.Errorf("%v: exit go=%d oracle=%d\n%s", args, gr.Code, or.Code, testkit.Diff(os_, gs))
		}
	}
	compare("agent", "tasks", "status", "--mission", "$M", "--json")
	laneWrite(p, t, "src/calc.py", "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n")
	p.Both(func(s *testkit.Sandbox) {
		wt := filepath.Join(s.Dir, ".worktrees", missionSlug(t, s)+"-lane-a")
		s.Exec(wt, "git", "add", "src/calc.py")
		s.Exec(wt, "env", "GIT_COMMITTER_DATE=2020-01-01T00:00:00+00:00", "GIT_AUTHOR_DATE=2020-01-01T00:00:00+00:00",
			"git", "commit", "-q", "-m", "feat(WP01): subtract")
	})
	compare("agent", "tasks", "status", "--mission", "$M", "--json")
	compare("agent", "tasks", "status", "--mission", "$M")
	compare("agent", "tasks", "status", "--mission", "$M", "--json", "--stale-threshold", "99999999")
	compare("agent", "tasks", "status", "--mission", "$M", "--stale-threshold", "99999999")
	// in_review stall: a negative threshold flags any in_review WP.
	p.Both(func(s *testkit.Sandbox) {
		cfg := s.ReadFile(".kittify/config.yaml")
		s.WriteFile(".kittify/config.yaml", cfg+"review:\n  stall_threshold_minutes: -1\n")
	})
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", "$M", "--agent", "rev", "--json"},
	})
	compare("agent", "tasks", "status", "--mission", "$M", "--json")
	compare("agent", "tasks", "status", "--mission", "$M")
}
