package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestVerifySetupMatchesOracle(t *testing.T) {
	p := testkit.NewPair(t)
	p.Compare(false, "verify-setup", "--json")
	p.Compare(false, "verify-setup", "--check-tools")
	p.Compare(false, "init", ".", "--ai", "claude,codex", "--non-interactive")
	p.Compare(false, "verify-setup")
	p.Compare(false, "verify-setup", "--json")
	p.Compare(false, "verify-setup", "--diagnostics")
	p.Compare(false, "verify-setup", "--diagnostics", "--json")
	p.Compare(false, "verify-setup", "--mission", "nope", "--json")
	p.Compare(false, "verify-setup", "--mission", "nope")
	p.Both(func(s *testkit.Sandbox) {
		os.Remove(filepath.Join(s.Dir, ".claude/skills/spec-kitty-git-workflow/SKILL.md"))
		os.Remove(filepath.Join(s.Dir, ".claude/skills/spec-kitty-runtime-next/SKILL.md"))
		s.WriteFile(".claude/skills/spec-kitty-git-workflow/SKILL.md", "---\nname: x\n---\nedited\n")
		s.WriteFile("kitty-specs/001-demo/meta.json", `{"mission_slug": "001-demo", "mission_number": "001", "mission_type": "software-dev"}`)
		s.WriteFile("kitty-specs/001-demo/spec.md", "# spec\n")
		s.Git("branch", "kitty/mission-001-demo")
	})
	p.Compare(false, "verify-setup")
	p.Compare(false, "verify-setup", "--json", "--mission", "001-demo")
	p.Compare(false, "verify-setup", "--mission", "001-demo")
	p.Compare(false, "verify-setup", "--diagnostics", "--mission", "001-demo")
	p.Compare(false, "verify-setup", "--diagnostics", "--json", "--mission", "001-demo")
}

func TestConfigAndSessionsMatchOracle(t *testing.T) {
	p := testkit.NewPair(t)
	p.Compare(false, "config")
	p.Compare(false, "session-start")
	p.Compare(false, "session-stop")
	p.Compare(false, "init", ".", "--ai", "claude", "--non-interactive")
	p.Compare(false, "session-start")
	p.Compare(false, "session-stop")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-ops/01KAAAAAAAAAAAAAAAAAAAAAAA.jsonl", `{"event": "started", "invocation_id": "01KAAAAAAAAAAAAAAAAAAAAAAA", "profile_id": "implementer-ivan", "started_at": "2020-01-01T00:00:00+00:00"}`+"\n")
		s.WriteFile("kitty-ops/01KBBBBBBBBBBBBBBBBBBBBBBB.jsonl", `{"event": "started", "profile_id": "x"}`+"\n"+`{"event": "completed"}`+"\n")
		s.WriteFile("kitty-ops/ops-index.jsonl", "{}\n")
	})
	g, o := p.Run("session-start")
	if strings.Count(g.Stdout, "Open Ops (1)") != 1 || strings.Count(o.Stdout, "Open Ops (1)") != 1 {
		t.Errorf("open ops section:\ngo:\n%s\noracle:\n%s", g.Stdout, o.Stdout)
	}
	g, o = p.Run("session-stop")
	gl, ol := strings.Split(g.Stdout, "\n"), strings.Split(o.Stdout, "\n")
	if len(gl) != len(ol) || gl[0] != ol[0] || !strings.HasPrefix(gl[3], "  01KAAAAAAAAAAAAAAAAAAAAAAA (implementer-ivan, ") {
		t.Errorf("session-stop:\ngo:\n%s\noracle:\n%s", g.Stdout, o.Stdout)
	}
	p.Compare(false, "config")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/overrides/templates/spec-template.md", "x\n")
		s.WriteFile(".kittify/command-templates/plan.md", "x\n")
	})
	// Package-default paths differ by design (embedded assets vs
	// site-packages): compare the type/name/tier columns only.
	g, o = p.Run("config", "--show-origin")
	if g.Code != o.Code || originCols(g.Stdout) != originCols(o.Stdout) {
		t.Errorf("config --show-origin:\n%s", testkit.Diff(originCols(o.Stdout), originCols(g.Stdout)))
	}
	g, o = p.Run("config", "--show-origin", "-m", "research")
	if g.Code != o.Code || originCols(g.Stdout) != originCols(o.Stdout) {
		t.Errorf("config --show-origin -m research:\n%s", testkit.Diff(originCols(o.Stdout), originCols(g.Stdout)))
	}
}

func originCols(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(s, "\n") {
		f := strings.Split(l, "│")
		if len(f) < 4 {
			continue
		}
		b.WriteString(strings.TrimSpace(f[1]) + "|" + strings.TrimSpace(f[2]) + "|" + strings.TrimSpace(f[3]) + "\n")
	}
	return b.String()
}
