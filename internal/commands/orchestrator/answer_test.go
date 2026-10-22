package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffAnswerDecisionNative covers answer-decision against a real
// pending decision: the ERP custom mission's requires_inputs gate (native
// `next --answer` under the hood), plus its refusals.
func TestDiffAnswerDecisionNative(t *testing.T) {
	erp, err := os.ReadFile(filepath.Join("..", "runtime", "testdata", "erp-integration.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/missions/erp-integration/mission.yaml", string(erp))
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "add erp mission")
		missionfx.RunOracle(t, s, "mission", "run", "erp-integration", "--mission", "erp-q3", "--json")
	})
	api := "orchestrator-api"
	step(t, p, api, "answer-decision", "--mission", "erp-q3", "--agent", "claude", "--answer", "json", "--result", "success", "--policy", testPolicy)
	p.Both(func(s *testkit.Sandbox) {
		for i := 0; i < 3; i++ {
			missionfx.RunOracle(t, s, "next", "--agent", "claude", "--mission", "erp-q3", "--result", "success", "--json")
		}
	})
	step(t, p, api, "answer-decision", "--mission", "erp-q3", "--agent", "claude", "--answer", "json", "--result", "success",
		"--decision-id", "input:nope", "--policy", testPolicy)
	step(t, p, api, "answer-decision", "--mission", "erp-q3", "--agent", "claude", "--answer", "json", "--result", "success", "--policy", testPolicy)
	step(t, p, api, "next", "--mission", "erp-q3", "--agent", "claude", "--result", "success", "--policy", testPolicy)
	p.CompareTree("kitty-specs/erp-q3", true)
}
