package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func opposedPack(s *testkit.Sandbox) {
	s.WriteFile("pack/directives/d24.directive.yaml", "id: DIRECTIVE_024\ntitle: Twenty four\nopposed_by:\n- type: directive\n  id: DIRECTIVE_025\n  reason: competing goals\n- type: paradigm\n  id: big-ball-of-mud\n  reason: avoid it\nscope: all\n")
	s.WriteFile("pack/directives/d25.directive.yaml", "id: DIRECTIVE_025\ntitle: Twenty five\n")
	s.WriteFile("pack/tactics/t1.tactic.yaml", "id: brownfield-onboarding\n# keep me\nopposed_by:\n- type: tactic\n  id: big-ball-of-mud\n")
	s.WriteFile("pack/tactics/bad.tactic.yaml", "id: shared\nopposed_by:\n- type: tactic\n  id: DIRECTIVE_024\n- 5\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "pack")
}

func TestDiffMigrateRewriteOpposedBy(t *testing.T) {
	p := newOraclePair(t)
	p.Both(opposedPack)
	compareSteps(t, p, "", "",
		step{"migrate", "rewrite-opposed-by", "--pack", "nope"},
		step{"migrate", "rewrite-opposed-by", "--pack", "pack", "--dry-run", "--json"},
		step{"migrate", "rewrite-opposed-by", "--pack", "pack", "--dry-run"},
		step{"migrate", "rewrite-opposed-by", "--pack", "pack"},
	)
	p.CompareTree("pack", true)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("pack/tactics/bad.tactic.yaml", "id: shared\n") })
	compareSteps(t, p, "", "",
		step{"migrate", "rewrite-opposed-by", "--pack", "pack", "--json"},
	)
	p.CompareTree("pack", true)
}
