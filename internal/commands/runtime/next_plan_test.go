package runtime_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

const planMD = "# Implementation Plan\n\n## Technical Context\nLanguage/Version: Python 3.11\nPrimary Dependencies: typer, rich\nStorage: filesystem\n\n## Architecture\nA filled plan.\n"

// newPlanPair creates a built-in plan mission (oracle) with its planning
// artifacts in both sandboxes; omit names an artifact left out.
func newPlanPair(t *testing.T, omit string) *testkit.Pair {
	t.Helper()
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("docs/decision.md", "# Decision workspace\n")
		missionfx.RunOracle(t, s, "agent", "mission", "create", "plan-demo", "--mission-type", "plan", "--json")
		dir := filepath.Join("kitty-specs", missionfx.MissionSlug(t, s))
		for name, body := range map[string]string{
			"spec.md":     "# Scope\n\n- **FR-001**: Decide an integration order.\n",
			"research.md": "# Research\n\nCompared verified alternatives.\n",
			"plan.md":     planMD,
		} {
			if name != omit {
				s.WriteFile(filepath.Join(dir, name), body)
			}
		}
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "planning artifacts")
	})
	return p
}

// TestDiffPlanMissionEndToEnd (#6707): an opted-in plan mission composes its
// four actions with the built-in default profiles, and a completed plan
// runtime is accepted without fabricated work packages.
func TestDiffPlanMissionEndToEnd(t *testing.T) {
	next := []string{"next", "--agent", "codex", "--mission", "$M", "--result", "success", "--json"}
	accept := []string{"accept", "--mission", "$M", "--mode", "pr", "--actor", "fixture", "--no-commit", "--json"}
	t.Run("complete", func(t *testing.T) {
		p := newPlanPair(t, "")
		steps(t, p, false, next, next, next, next)
		// review is issued, not completed: acceptance refuses the runtime.
		steps(t, p, false, accept)
		steps(t, p, false, next)
		compareTree(t, p, "kitty-ops")
		p.Both(func(s *testkit.Sandbox) {
			s.Git("add", "-A")
			s.Git("commit", "-q", "-m", "runtime")
		})
		steps(t, p, false, accept)
	})
	t.Run("plan-missing", func(t *testing.T) {
		p := newPlanPair(t, "plan.md")
		steps(t, p, false, next, next, next, next, next)
		p.Both(func(s *testkit.Sandbox) {
			s.Git("add", "-A")
			s.Git("commit", "-q", "-m", "runtime")
		})
		steps(t, p, false, accept)
	})
}
