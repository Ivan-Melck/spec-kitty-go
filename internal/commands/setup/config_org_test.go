package setup_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// MAINT-10: the org-pack tier of config --show-origin (and the legacy-tier
// stderr nudge).
func TestConfigShowOriginOrgPackTier(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) {
		if r := s.RunOracle("init", ".", "--ai", "claude", "--non-interactive"); r.Code != 0 {
			t.Fatalf("oracle init: %s%s", r.Stdout, r.Stderr)
		}
		s.WriteFile(".kittify/templates/spec-template.md", "# legacy\n")
		s.WriteFile("orgpack/missions/software-dev/templates/plan-template.md", "# org\n")
		s.WriteFile("orgpack/missions/software-dev/command-templates/specify.md", "# org\n")
		s.WriteFile("orgpack/missions/research/mission.yaml", "name: research\n")
		s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+"charter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: orgpack\n")
	})
	g, o := p.Run("config", "--show-origin")
	if g.Code != o.Code || originCols(g.Stdout) != originCols(o.Stdout) {
		t.Errorf("config --show-origin:\n%s", testkit.Diff(originCols(o.Stdout), originCols(g.Stdout)))
	}
	if !strings.Contains(g.Stdout, "orgpack/missions/software-dev/templates/plan-template.md") {
		t.Errorf("org path not shown:\n%s", g.Stdout)
	}
	if gs, os_ := p.Go.Normalize(g.Stderr), p.Oracle.Normalize(o.Stderr); gs != os_ {
		t.Errorf("stderr differs:\n%s", testkit.Diff(os_, gs))
	}
}
