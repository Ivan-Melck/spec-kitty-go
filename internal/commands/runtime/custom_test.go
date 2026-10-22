package runtime_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func readTestdata(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// installCustomMissions writes the ERP custom mission plus malformed
// variants into .kittify/missions on both sides and commits them.
func installCustomMissions(t *testing.T, p *testkit.Pair) {
	erp := readTestdata(t, "erp-integration.yaml")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/missions/erp-integration/mission.yaml", erp)
		s.WriteFile(".kittify/missions/no-retro/mission.yaml", "mission:\n  key: no-retro\n  name: No Retro\n  version: 1.0.0\nsteps:\n  - id: a\n    title: A\n    agent_profile: researcher-robbie\n")
		s.WriteFile(".kittify/missions/no-title/mission.yaml", "mission:\n  key: no-title\n  name: No Title\n  version: 1.0.0\nsteps:\n  - id: a\n    agent_profile: researcher-robbie\n  - id: retrospective\n    title: R\n")
		s.WriteFile(".kittify/missions/unbound/mission.yaml", "mission:\n  key: unbound\n  name: Unbound\n  version: 1.0.0\nsteps:\n  - id: a\n    title: A\n  - id: retrospective\n    title: R\n    depends_on: [a]\n")
		s.WriteFile(".kittify/missions/both/mission.yaml", "mission:\n  key: both\n  name: Both\n  version: 1.0.0\nsteps:\n  - id: a\n    title: A\n    agent_profile: researcher-robbie\n    contract_ref: specify\n  - id: retrospective\n    title: R\n")
		s.WriteFile(".kittify/missions/cycle/mission.yaml", "mission:\n  key: cycle\n  name: Cycle\n  version: 1.0.0\nsteps:\n  - id: a\n    title: A\n    agent_profile: researcher-robbie\n    depends_on: [b]\n  - id: b\n    title: B\n    agent_profile: researcher-robbie\n    depends_on: [a]\n  - id: retrospective\n    title: R\n")
		s.WriteFile(".kittify/missions/badref/mission.yaml", "mission:\n  key: badref\n  name: Bad Ref\n  version: 1.0.0\nsteps:\n  - id: a\n    title: A\n    contract_ref: no-such-contract\n  - id: retrospective\n    title: R\n")
		s.WriteFile(".kittify/missions/plan/mission.yaml", "mission:\n  key: plan\n  name: Plan\n  version: 1.0.0\nsteps:\n  - id: a\n    title: A\n    agent_profile: researcher-robbie\n  - id: retrospective\n    title: R\n")
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "add custom missions")
	})
}

// TestDiffCustomMissionRun covers `mission run` validation refusals, the
// MISSION_TYPE_CONFLICT guard, a fresh custom run, and the `next` loop over
// it: composed custom steps (one committed Op each), the decision-required
// gate, `--answer` (with its refusals) and the templateless step prompts.
func TestDiffCustomMissionRun(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	installCustomMissions(t, p)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "erp-rollout", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/erp")
	})
	steps(t, p, true,
		[]string{"mission", "run", "no-such", "--mission", "erp-q3", "--json"},
		[]string{"mission", "run", "no-retro", "--mission", "erp-q3", "--json"},
		[]string{"mission", "run", "no-title", "--mission", "erp-q3", "--json"},
		[]string{"mission", "run", "unbound", "--mission", "erp-q3", "--json"},
		[]string{"mission", "run", "both", "--mission", "erp-q3", "--json"},
		[]string{"mission", "run", "cycle", "--mission", "erp-q3", "--json"},
		[]string{"mission", "run", "badref", "--mission", "erp-q3", "--json"},
		[]string{"mission", "run", "plan", "--mission", "erp-q3", "--json"},
		[]string{"mission", "run", "plan", "--mission", "erp-q3"},
		[]string{"mission", "run", "erp-integration", "--mission", "$M", "--json"},
		[]string{"mission", "run", "erp-integration", "--mission", "$M"},
		[]string{"mission", "run", "erp-integration", "--mission", "erp-q3", "--json"},
		[]string{"mission", "run", "erp-integration", "--mission", "erp-q3"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--answer", "x", "--result", "success", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--answer", "x", "--result", "success"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--result", "success", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--result", "success", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--result", "success", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--result", "success", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--answer", "csv", "--decision-id", "input:nope", "--result", "success", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--answer", "json", "--result", "success", "--json"},
	)
	// human-mode advance (stderr carries upstream's DecisionGitLog traceback)
	steps(t, p, false,
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--result", "success"},
		[]string{"next", "--agent", "claude", "--mission", "erp-q3", "--result", "success", "--json"},
	)
	compareTree(t, p, "kitty-ops")
	compareTree(t, p, "kitty-specs/erp-q3")
	compareTree(t, p, ".kittify/runtime", "lock")
	compareGit(t, p)
}
