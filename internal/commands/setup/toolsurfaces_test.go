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
			compareInit(p, "init", ".", "--ai", sel, "--non-interactive")
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
			// --fix: repair block (effect ids, plugin-bundle staging) and the
			// repaired states.
			compareHashed(t, p, "doctor", "tool-surfaces", "--fix", "--json")
			p.Compare(false, "doctor", "tool-surfaces", "--json", "--kind", "charter-skill", "--kind", "command-skill", "--kind", "agent-profile",
				"--kind", "context-file", "--kind", "rule", "--kind", "hook", "--kind", "native-config", "--kind", "command-file")
		})
	}
}
