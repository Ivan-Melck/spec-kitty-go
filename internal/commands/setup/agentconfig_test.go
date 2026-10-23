package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestAgentConfigMatchesOracle(t *testing.T) {
	p := testkit.NewPair(t)
	p.Compare(false, "agent", "config", "list")
	compareInit(p, "init", ".", "--ai", "claude,codex", "--non-interactive")
	for _, args := range [][]string{
		{"list"}, {"status"},
		{"add", "gemini", "vibe", "codex"},
		{"add", "bogus", "claude"},
		{"set", "auto_commit", "no"},
		{"set", "auto_commit", "maybe"},
		{"set", "lint_on_edit", "true"},
		{"set", "foo", "bar"},
		{"sync", "--sync-hooks"},
		{"sync", "--create-missing", "--json"},
		{"remove", "vibe", "gemini", "cursor"},
		{"list"},
		{"set", "lint_on_edit", "off"},
		{"sync", "--sync-hooks", "--keep-orphaned"},
		{"remove", "codex", "--keep-config"},
		{"sync", "--create-missing"},
		{"status"},
		{"add", "letta", "pi", "cursor"},
		{"remove", "pi", "letta", "codex"},
		{"sync"},
	} {
		p.Compare(false, append([]string{"agent", "config"}, args...)...)
	}
	for _, f := range []string{".kittify/config.yaml", ".kittify/command-skills-manifest.json", ".claude/settings.json", ".cursor/hooks.json"} {
		g, _ := os.ReadFile(filepath.Join(p.Go.Dir, f))
		o, _ := os.ReadFile(filepath.Join(p.Oracle.Dir, f))
		if gn, on := p.Go.Normalize(string(g)), p.Oracle.Normalize(string(o)); gn != on {
			t.Errorf("%s differs:\n%s", f, testkit.Diff(on, gn))
		}
	}
	p.CompareTree(".agents", false)
}

func TestAgentConfigOrphansMatchOracle(t *testing.T) {
	p := testkit.NewPair(t)
	compareInit(p, "init", ".", "--ai", "claude", "--non-interactive")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".gemini/commands/spec-kitty.plan.md", "user file\n")
		s.WriteFile(".kittify/x", "")
	})
	p.Compare(false, "agent", "config", "status")
	p.Compare(false, "agent", "config", "sync")
	p.Compare(false, "agent", "config", "remove", "gemini", "copilot")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/config.yaml", "agents: [\n")
	})
	// The YAML parser's error text differs (PyYAML vs yaml.v3): exit codes
	// and the error envelope only.
	for _, args := range [][]string{{"list"}, {"sync", "--json"}, {}} {
		g, o := p.Run(append([]string{"agent", "config"}, args...)...)
		if g.Code != o.Code || strings.Contains(o.Stdout, `"ok": false`) != strings.Contains(g.Stdout, `"ok": false`) {
			t.Errorf("agent config %v: go=%d oracle=%d\n%s\n%s", args, g.Code, o.Code, g.Stdout, o.Stdout)
		}
	}
}

func TestAgentConfigWithoutOracle(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	s.MustRun("init", ".", "--ai", "claude", "--non-interactive")
	r := s.MustRun("agent", "config", "add", "codex")
	if !strings.Contains(testkit.StripANSI(r.Stdout), "Registered codex (14 command skills in .agents/skills/)") {
		t.Fatalf("add:\n%s", r.Stdout)
	}
	if !strings.Contains(s.ReadFile(".kittify/config.yaml"), "  - codex\n") {
		t.Fatal("codex not saved")
	}
	r = s.MustRun("agent", "config", "remove", "codex")
	if !strings.Contains(testkit.StripANSI(r.Stdout), "Removed skills for codex (14 files deleted)") || s.Exists(".agents/skills/spec-kitty-plan/SKILL.md") {
		t.Fatalf("remove:\n%s", r.Stdout)
	}
	s.MustRun("agent", "config", "set", "lint_on_edit", "true")
	s.MustRun("agent", "config", "sync", "--sync-hooks")
	if !strings.Contains(s.ReadFile(".claude/settings.json"), `"matcher": "Edit|Write"`) {
		t.Fatal("lint hook not registered")
	}
	if r := s.Run("agent", "config", "set", "auto_commit", "maybe"); r.Code != 1 {
		t.Fatalf("bad value exit %d", r.Code)
	}
}

// RC8's guarded agent reader reports file-stream YAML diagnostics and a
// recovery command. An absent agent list must survive unrelated setting edits.
func TestAgentConfigGuardedReadRC8(t *testing.T) {
	p := testkit.NewPair(t)
	compareInit(p, "init", ".", "--ai", "claude", "--non-interactive")
	for _, config := range []string{
		"agents: [\n", "[]\n", "agents: nope\n",
		"agents: {available: null}\n", "agents: {available: bogus}\n",
		"agents: \"quoted\"\n", "'quoted root'\n",
	} {
		p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/config.yaml", config) })
		p.Compare(true, "agent", "config", "list")
		p.Compare(true, "agent", "config", "sync", "--json")
	}
	for _, config := range []string{"agents: {auto_commit: true}\n", "agents: {available: [], auto_commit: true}\n"} {
		p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/config.yaml", config) })
		p.Compare(true, "agent", "config", "set", "auto_commit", "false")
		compareFiles(t, p, ".kittify/config.yaml")
	}
}
