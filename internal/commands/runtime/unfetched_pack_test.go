package runtime_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffUnfetchedOrgPackMissionFlow drives the mission flow with a
// declared-but-unfetched org pack: the lenient surfaces skip it and log
// resolve_org_dirs' dropped-root WARNING where upstream builds a layered
// repository over it (resolve_mission_type_context, the runtime
// step-contract resolution); the strict ones (gate bindings, the
// step-contract executor) fail closed.
func TestDiffUnfetchedOrgPackMissionFlow(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p)
	p.Both(func(s *testkit.Sandbox) {
		cfg := s.ReadFile(".kittify/config.yaml")
		s.WriteFile(".kittify/config.yaml", cfg+"charter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: packs/acme\n")
		s.Git("add", "-A")
		s.Git("commit", "-qm", "declare acme")
	})
	steps(t, p, true,
		[]string{"agent", "mission", "check-prerequisites", "--mission", "$M"},
		[]string{"agent", "mission", "setup-plan", "--mission", "$M"},
		[]string{"agent", "mission", "record-analysis", "--mission", "$M", "--input-file", missionfx.AnalysisReport()},
		[]string{"next", "--agent", "claude", "--mission", "$M"},
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"},
		[]string{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--force"},
		[]string{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"},
		[]string{"orchestrator-api", "design-context", "--mission", "$M"},
		[]string{"mission", "info", "--mission", "$M"},
		[]string{"agent", "mission", "create", "other-one", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/other"},
	)
}

// TestDiffUnfetchedOrgPackNextEarly: `next` on a freshly created mission
// (specify/plan steps, composed actions through the step-contract runtime).
func TestDiffUnfetchedOrgPackNextEarly(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
		cfg := s.ReadFile(".kittify/config.yaml")
		s.WriteFile(".kittify/config.yaml", cfg+"charter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: packs/acme\n")
		s.Git("add", "-A")
		s.Git("commit", "-qm", "declare acme")
	})
	steps(t, p, true,
		[]string{"next", "--agent", "claude", "--mission", "$M"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
	)
}
