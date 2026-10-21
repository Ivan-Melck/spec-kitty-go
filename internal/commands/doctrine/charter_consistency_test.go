package doctrine_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// reDoctrineRoot masks the built-in doctrine root paths (site-packages vs
// the Go pack cache) inside UnknownArtifactIdError messages.
var reDoctrineRoot = regexp.MustCompile(`doctrine root \S+?\. Searched layers: [^.]*?(\.[^ .]+)*?\. Check`)

func compareConsistency(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%v: exit go=%d oracle=%d\n%s\n%s", args, g.Code, o.Code, g.Stdout, o.Stdout)
	}
	mask := func(s string) string { return reDoctrineRoot.ReplaceAllString(s, "doctrine root <ROOT>. Check") }
	if gs, os_ := mask(p.Go.Normalize(g.Stdout)), mask(p.Oracle.Normalize(o.Stdout)); gs != os_ {
		t.Errorf("%v: stdout differs\n%s", args, testkit.Diff(os_, gs))
	}
}

func TestParityCharterConsistencyCheck(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	compareConsistency(t, p, "charter", "consistency-check")
	compareConsistency(t, p, "charter", "consistency-check", "--json")
	p.Both(func(s *testkit.Sandbox) {
		missionfxOracle(t, s, "charter", "activate", "directive", "010-specification-fidelity-requirement")
		missionfxOracle(t, s, "charter", "activate", "tactic", "acceptance-test-first")
	})
	compareConsistency(t, p, "charter", "consistency-check")
	compareConsistency(t, p, "charter", "consistency-check", "--json")
	p.Both(func(s *testkit.Sandbox) {
		c := s.ReadFile(".kittify/config.yaml")
		c = strings.Replace(c, "activated_tactics:\n", "activated_tactics:\n- bogus-tactic\n- 010-specification-fidelity-requirement\n- bogus-tactic\n", 1)
		s.WriteFile(".kittify/config.yaml", c)
	})
	compareConsistency(t, p, "charter", "consistency-check", "--json")
	// Catalog parity against a charter.yaml pointer target.
	p.Both(func(s *testkit.Sandbox) {
		c := strings.Replace(s.ReadFile(".kittify/config.yaml"), "- bogus-tactic\n- 010-specification-fidelity-requirement\n- bogus-tactic\n", "", 1)
		s.WriteFile(".kittify/config.yaml", c+"charter: .kittify/charter/charter.yaml\n")
		s.WriteFile(".kittify/charter/charter.yaml", "activated_paradigms:\n- domain-driven-design\nactivated_tactics:\n- acceptance-test-first\ncatalog:\n  references:\n  - kind: paradigm\n    id: paradigm:atomic-design\n")
	})
	compareConsistency(t, p, "charter", "consistency-check")
	compareConsistency(t, p, "charter", "consistency-check", "--json")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter/charter.yaml", "activated_tactics: [acceptance-test-first]\ncatalog: nope\n")
	})
	compareConsistency(t, p, "charter", "consistency-check", "--json")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/config.yaml", strings.Replace(s.ReadFile(".kittify/config.yaml"), "charter: .kittify/charter/charter.yaml", "charter: .kittify/charter/nope.yaml", 1))
	})
	compareConsistency(t, p, "charter", "consistency-check")
	compareConsistency(t, p, "charter", "consistency-check", "--json")
}

func missionfxOracle(t *testing.T, s *testkit.Sandbox, args ...string) {
	t.Helper()
	if r := s.RunOracle(args...); r.Code != 0 {
		t.Fatalf("oracle %v: %d %s %s", args, r.Code, r.Stdout, r.Stderr)
	}
}

func TestCharterConsistencyCheckGoOnly(t *testing.T) {
	s := newProject(t)
	r := s.Run("charter", "consistency-check", "--json")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"coherent": true`) {
		t.Fatalf("consistency: %d %s", r.Code, r.Stdout)
	}
	s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+"activated_tactics:\n- bogus\n- bogus\n")
	r = s.Run("charter", "consistency-check")
	if r.Code != 1 || !strings.Contains(r.Stdout, "Unknown reference: tactic/bogus") || !strings.Contains(r.Stdout, "Duplicate entry") {
		t.Fatalf("consistency: %d %s", r.Code, r.Stdout)
	}
}
