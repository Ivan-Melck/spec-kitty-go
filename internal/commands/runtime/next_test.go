package runtime_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffNextFinalizedBoard replays TRACE 4.2, 4.7, 5.13, 5.14, 5.21 and
// 9.1: query mode across the analyze gate and the finalized task board,
// advancing mode on first contact, unknown handles.
func TestDiffNextFinalizedBoard(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p)
	steps(t, p, true,
		[]string{"next", "--agent", "claude", "--mission", "$M", "--json"},
		[]string{"next", "--mission", "$M"},
		[]string{"next", "--agent", "claude", "--mission", "no-such-mission-01ZZZZZZ", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "no-such-mission-01ZZZZZZ"},
		[]string{"next", "--mission", "$M", "--result", "bogus"},
		[]string{"next", "--mission", "$M", "--result", "success"},
		[]string{"next", "--mission", "$M", "--answer", "x", "--json"},
	)
	oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--input-file", missionfx.AnalysisReport(), "--json")
	steps(t, p, true,
		[]string{"next", "--agent", "claude", "--mission", "$M", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M"},
		[]string{"next", "--json"},
	)
	// advancing on first contact: dispatch implement WP01
	steps(t, p, false, []string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"})
	compareTree(t, p, ".kittify/runtime", "runs/")
	compareTree(t, p, "kitty-ops")
	compareGit(t, p)
	// query with a persisted run
	steps(t, p, false,
		[]string{"next", "--agent", "claude", "--mission", "$M", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M"},
	)
	// WP01 in progress (oracle claim), advancing keeps dispatching it
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	steps(t, p, false,
		[]string{"next", "--agent", "claude", "--mission", "$M", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success"},
	)
	compareTree(t, p, "kitty-ops")
}

// TestDiffNextTornRunJournal: a run journal whose last line is torn refuses
// the next append (#6606), so the advance stops as a blocked "Runtime engine
// error" ending in EVENT_LOG_TORN_TAIL, and the journal is left untouched.
func TestDiffNextTornRunJournal(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
	})
	steps(t, p, false, []string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"})
	journals := map[*testkit.Sandbox]string{}
	p.Both(func(s *testkit.Sandbox) {
		logs, _ := filepath.Glob(filepath.Join(s.Dir, ".kittify", "runtime", "runs", "*", "run.events.jsonl"))
		if len(logs) != 1 {
			t.Fatalf("run journals: %v", logs)
		}
		f, err := os.OpenFile(logs[0], os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(`{"event_type": "NextStepIss`)
		f.Close()
		b, _ := os.ReadFile(logs[0])
		journals[s] = string(b)
	})
	steps(t, p, false,
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success"},
	)
	p.Both(func(s *testkit.Sandbox) {
		logs, _ := filepath.Glob(filepath.Join(s.Dir, ".kittify", "runtime", "runs", "*", "run.events.jsonl"))
		if b, _ := os.ReadFile(logs[0]); string(b) != journals[s] {
			t.Errorf("torn journal changed:\n%s", b)
		}
	})
}

// TestDiffNextFreshMission covers the DAG planner before tasks exist:
// not_started preview, discovery/specify issuance, the specify guard.
func TestDiffNextFreshMission(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
	})
	steps(t, p, false,
		[]string{"next", "--agent", "claude", "--mission", "$M", "--json"},
		[]string{"next", "--mission", "$M"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
	)
	// completing specify composes the specify step contract: one committed
	// Op record per contract step.
	compareTree(t, p, "kitty-ops")
	compareGit(t, p)
}
