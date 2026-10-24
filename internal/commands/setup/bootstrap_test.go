package setup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/commands/setup"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestSkipsBootstrap(t *testing.T) {
	cases := map[string]bool{
		"next --agent x":        true,
		"--help next":           false,
		"session-start":         true,
		"live-work hook claude": true,
		"upgrade --dry-run":     true,
		"migrate":               true,
		"init . --ai claude":    false,
		"doctor skills":         false,
	}
	for argv, want := range cases {
		if got := setup.SkipsBootstrap(splitArgs(argv)); got != want {
			t.Errorf("%q: got %v want %v", argv, got, want)
		}
	}
}

func splitArgs(s string) []string {
	var out []string
	cur := ""
	for _, r := range s + " " {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	return out
}

func TestBootstrapPopulatesHome(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	s.MustRun()
	for _, rel := range []string{
		".kittify/missions/software-dev/mission.yaml",
		".kittify/cache/version.lock",
		".claude/commands/spec-kitty.specify.md",
		".gemini/commands/spec-kitty.plan.toml",
		".claude/skills/spec-kitty/SKILL.md",
		".agents/skills/spk-start-here/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(s.Home, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	// Warm run must be a no-op on content.
	before := treeEntries(s.Home, nil)
	s.MustRun()
	after := treeEntries(s.Home, nil)
	if len(before) != len(after) {
		t.Fatalf("warm bootstrap changed HOME: %d -> %d entries", len(before), len(after))
	}
}

// TestBootstrapHomeMatchesOracle compares the user-global tree written by
// the startup bootstrap (bare invocation) byte for byte.
func TestBootstrapHomeMatchesOracle(t *testing.T) {
	p := testkit.NewPair(t)
	g, o := p.Run()
	if g.Code != o.Code {
		t.Fatalf("exit go=%d oracle=%d", g.Code, o.Code)
	}
	// The freshness stamp hashes upstream's Python renderer sources and the
	// cold-install sentinel name hashes the sandbox path.
	compareDirs(t, p, p.Go.Home, p.Oracle.Home, ".spec-kitty-cold-install", ".kittify/cache/agent-commands-freshness.lock")
}
