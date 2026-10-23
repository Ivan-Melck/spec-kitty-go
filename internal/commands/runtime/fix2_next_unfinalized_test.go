package runtime_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffNextAuthoredUnfinalized: `next` (query and advancing) on a
// mission whose spec, plan, tasks and WP files are authored but whose
// tasks were never finalized.
func TestDiffNextAuthoredUnfinalized(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
		missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s))
	})
	steps(t, p, true,
		[]string{"next", "--agent", "claude", "--mission", "$M"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--json"},
	)
	steps(t, p, true, []string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"})
	compareGit(t, p)
	steps(t, p, true, []string{"next", "--agent", "claude", "--mission", "$M", "--result", "success"})
	compareGit(t, p)
	compareTree(t, p, "kitty-specs")
}
