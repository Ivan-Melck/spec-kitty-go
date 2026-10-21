package runtime_test

import (
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
	// Op records of composed step contracts (kitty-ops/<ULID>.jsonl) are not
	// written by this build: compare the lifecycle store only.
	compareTree(t, p, "kitty-ops", "01", "ops-index")
}
