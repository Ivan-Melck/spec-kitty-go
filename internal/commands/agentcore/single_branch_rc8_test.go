package agentcore_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffSingleBranchImplementationRC8(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	g, o := missionfx.OracleCalcMission(t, p, "--topology", "single_branch", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", missionfx.MissionSlug(t, s))
	})
	for _, scenario := range []string{"missing", "no-window", "unreadable", "implemented"} {
		t.Run(scenario, func(t *testing.T) {
			p.Both(func(s *testkit.Sandbox) {
				md := filepath.Join("kitty-specs", missionfx.MissionSlug(t, s))
				p := filepath.Join(md, "lanes.json")
				m, e := jsonx.DecodeObject([]byte(s.ReadFile(p)))
				if e != nil {
					t.Fatal(e)
				}
				switch scenario {
				case "missing":
					s.WriteFile("planning-base.txt", m.GetString("planning_commit_sha"))
				case "no-window":
					m.Delete("planning_commit_sha")
				case "unreadable":
					m.Set("planning_commit_sha", "missing-ref")
				case "implemented":
					m.Set("planning_commit_sha", s.ReadFile("planning-base.txt"))
					s.WriteFile("src/calc.py", "def subtract(a, b):\n    return a - b\n")
				}
				s.WriteFile(p, jsonx.Dumps(m, jsonx.Pretty)+"\n")
				s.Git("add", "-A")
				s.Git("commit", "-qm", "fixture "+scenario)
			})
			comparePairSteps(t, p, g, o, [][]string{{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--json"}})
		})
	}
}
