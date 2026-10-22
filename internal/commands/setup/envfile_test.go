package setup_test

import (
	"regexp"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var reInitSite = regexp.MustCompile(`(?m)^\S*/specify_cli/__init__\.py:`)

// MAINT-06: a SPEC_KITTY_HOME= line in .kitty.env is dropped with a
// UserWarning on stderr (reported at the import site).
func TestKittyEnvHomeLineWarnsLikeOracle(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) {
		if r := s.RunOracle("init", ".", "--ai", "claude", "--non-interactive"); r.Code != 0 {
			t.Fatalf("oracle init: %s%s", r.Stdout, r.Stderr)
		}
		s.WriteFile(".kittify/.kitty.env", "FOO=bar\nSPEC_KITTY_HOME=/tmp/elsewhere\n")
	})
	g, o := p.Run("--version")
	if g.Code != o.Code || g.Stdout != o.Stdout {
		t.Errorf("--version differs: go=%d %q oracle=%d %q", g.Code, g.Stdout, o.Code, o.Stdout)
	}
	ge := reInitSite.ReplaceAllString(p.Go.Normalize(g.Stderr), "<SITE>:")
	oe := reInitSite.ReplaceAllString(p.Oracle.Normalize(o.Stderr), "<SITE>:")
	if ge != oe {
		t.Errorf("stderr differs:\n%s", testkit.Diff(oe, ge))
	}
}
