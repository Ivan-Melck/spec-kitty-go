package setup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestDoctorSetupLeavesMatchOracle(t *testing.T) {
	p := testkit.NewPair(t)
	for _, c := range [][]string{{"skills"}, {"skills", "--json"}, {"env-file", "--json"}, {"command-files"}} {
		p.Compare(false, append([]string{"doctor"}, c...)...)
	}
	compareInit(p, "init", ".", "--ai", "claude,codex,gemini", "--non-interactive")
	for _, c := range [][]string{{"skills"}, {"skills", "--json"}, {"command-files"}, {"command-files", "--json"},
		{"env-file"}, {"env-file", "--json"}, {"channel"}, {"channel", "--json"}} {
		p.Compare(false, append([]string{"doctor"}, c...)...)
	}
	g, o := p.Run("doctor")
	if g.Code != o.Code || g.Stderr != o.Stderr {
		t.Errorf("bare doctor: go=%d %q oracle=%d %q", g.Code, g.Stderr, o.Code, o.Stderr)
	}
	p.Both(func(s *testkit.Sandbox) {
		os.RemoveAll(filepath.Join(s.Dir, ".agents/skills/spec-kitty-plan"))
		os.Chmod(filepath.Join(s.Dir, ".agents/skills/spec-kitty-tasks/SKILL.md"), 0o644)
		s.WriteFile(".agents/skills/spec-kitty-tasks/SKILL.md", "edited\n")
		s.WriteFile(".agents/skills/spec-kitty.extra/SKILL.md", "x\n")
		os.Remove(filepath.Join(s.Home, ".claude/commands/spec-kitty.plan.md"))
		s.WriteFile(".kittify/.kitty.env", "SPEC_KITTY_NO_BANNER=1\nexport SPEC_KITTY_ORG_TOKEN=\"abc\"\n")
	})
	p.Compare(false, "doctor", "skills")
	p.Compare(false, "doctor", "skills", "--json")
	p.Compare(false, "doctor", "env-file")
	p.Compare(false, "doctor", "env-file", "--json")
	p.Both(func(s *testkit.Sandbox) { os.RemoveAll(filepath.Join(s.Dir, ".agents/skills/spec-kitty.extra")) })
	p.Compare(false, "doctor", "skills", "--fix")
	p.Both(func(s *testkit.Sandbox) {
		os.Chmod(filepath.Join(s.Dir, ".agents/skills/spec-kitty-tasks/SKILL.md"), 0o644)
		os.Remove(filepath.Join(s.Dir, ".agents/skills/spec-kitty-tasks/SKILL.md"))
	})
	p.Compare(false, "doctor", "skills", "--fix", "--json")
	p.Compare(false, "doctor", "skills")
	p.CompareTree(".agents", true)

	p.Compare(false, "doctor", "state-roots")
	p.Compare(false, "doctor", "state-roots", "--json")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".gitignore", "")
		s.WriteFile(".kittify/merge-state.json", "{}\n")
		s.WriteFile("kitty-specs/001-x/meta.json", "{}\n")
	})
	p.Compare(false, "doctor", "state-roots")
	p.Compare(false, "doctor", "state-roots", "--json")
	p.Compare(false, "regen", "--check")
}
