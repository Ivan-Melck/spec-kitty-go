package setup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestDoctorToolSurfacesMatchOracle(t *testing.T) {
	for _, sel := range []string{"claude,codex,gemini", "qwen,cursor,vibe,kiro,copilot,opencode,auggie"} {
		sel := sel
		t.Run(sel, func(t *testing.T) {
			p := testkit.NewPair(t)
			p.Compare(false, "init", ".", "--ai", sel, "--non-interactive")
			p.Compare(false, "doctor", "tool-surfaces")
			p.Compare(false, "doctor", "tool-surfaces", "--json")
			p.Compare(false, "doctor", "tool-surfaces", "--kind", "command-skill", "--kind", "hook", "--json")
			p.Compare(false, "doctor", "tool-surfaces", "--tool", "codex", "--json")
			p.Compare(false, "doctor", "tool-surfaces", "--kind", "bogus")
			p.Both(func(s *testkit.Sandbox) {
				os.RemoveAll(filepath.Join(s.Dir, ".agents/skills/spec-kitty-git-workflow"))
				os.RemoveAll(filepath.Join(s.Dir, ".agents/skills/spec-kitty.plan"))
				os.Remove(filepath.Join(s.Dir, ".claude/agents/analyst-annie.md"))
				os.Chmod(filepath.Join(s.Dir, ".agents/skills/spec-kitty.tasks/SKILL.md"), 0o644)
				os.Remove(filepath.Join(s.Dir, ".agents/skills/spec-kitty.tasks/SKILL.md"))
				s.WriteFile(".agents/skills/spec-kitty.tasks/SKILL.md", "edited\n")
			})
			p.Compare(false, "doctor", "tool-surfaces")
			p.Compare(false, "doctor", "tool-surfaces", "--json")
			// --fix: the repair block carries upstream-internal effect ids and
			// plugin-bundle staging is not ported; compare the repaired states.
			g, o := p.Run("doctor", "tool-surfaces", "--fix", "--json")
			if g.Code != o.Code {
				t.Errorf("--fix exit go=%d oracle=%d", g.Code, o.Code)
			}
			p.Compare(false, "doctor", "tool-surfaces", "--json", "--kind", "charter-skill", "--kind", "command-skill", "--kind", "agent-profile",
				"--kind", "context-file", "--kind", "rule", "--kind", "hook", "--kind", "native-config", "--kind", "command-file")
		})
	}
}
