package doctrine_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var reBuiltinRoot = regexp.MustCompile(`/[^"\s]*?/packs/(?:[0-9a-zA-Z]+/)?built-in`)

// maskBuiltin replaces the built-in pack root (site-packages for the
// oracle, the materialised cache for Go) with a placeholder.
func maskBuiltin(s string) string { return reBuiltinRoot.ReplaceAllString(s, "<BUILTIN>") }

func compareCharter(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", strings.Join(args, " "), g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	gs, os_ := p.Go.Normalize(maskBuiltin(g.Stdout)), p.Oracle.Normalize(maskBuiltin(o.Stdout))
	if gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
}

func TestCharterActivationGoOnly(t *testing.T) {
	s := newProject(t)
	r := s.MustRun("charter", "activate", "agent-profile", "python-pedro", "--no-compile")
	if !strings.Contains(r.Stdout, "Activated: python-pedro") || !strings.Contains(r.Stdout, "referenced directive/001-architectural-integrity-standard was not activated") {
		t.Fatalf("activate: %s", r.Stdout)
	}
	if !strings.Contains(s.ReadFile(".kittify/config.yaml"), "activated_agent_profiles:\n- analyst-annie\n") {
		t.Fatalf("config: %s", s.ReadFile(".kittify/config.yaml"))
	}
	r = s.MustRun("charter", "list", "--json")
	if !strings.Contains(r.Stdout, `"python-pedro"`) {
		t.Fatalf("list: %s", r.Stdout)
	}
	r = s.MustRun("charter", "activate", "--preset", "minimal", "--force", "--json")
	if !strings.Contains(r.Stdout, `"activated_directives"`) {
		t.Fatalf("preset: %s", r.Stdout)
	}
	if r := s.Run("charter", "activate", "directive", "nope"); r.Code != 1 || !strings.Contains(r.Stdout, "Unknown directive ID 'nope'") {
		t.Fatalf("unknown id: %d %s", r.Code, r.Stdout)
	}
	if r := s.Run("charter", "pack", "path", "nope", "--json"); r.Code != 1 || !strings.Contains(r.Stdout, "PACK_NOT_FOUND") {
		t.Fatalf("pack path: %d %s", r.Code, r.Stdout)
	}
}

func TestParityCharterActivation(t *testing.T) {
	p := newOraclePair(t)
	for _, args := range [][]string{
		{"charter", "list"},
		{"charter", "list", "--show-available", "--json"},
		{"charter", "list", "--all", "--json"},
		{"charter", "pack", "list", "--json"},
		{"charter", "pack", "path", "built-in", "--preset", "minimal"},
		{"charter", "pack", "path", "nope", "--json"},
		{"charter", "pack", "asset", "list", "--json"},
		{"charter", "activate", "agent-profile", "python-pedro"},
		{"charter", "activate", "tactic", "acceptance-test-first"},
		{"charter", "activate", "paradigm", "domain-driven-design", "--cascade", "all"},
		{"charter", "activate", "mission-type", "research"},
		{"charter", "activate", "procedure", "nope"},
		{"charter", "activate", "bogus", "x"},
		{"charter", "activate", "directive", "x", "--json"},
		{"charter", "deactivate", "directive", "010-specification-fidelity-requirement"},
		{"charter", "deactivate", "agent-profile", "researcher-robbie"},
		{"charter", "deactivate", "agent-profile", "python-pedro", "--cascade", "all"},
		{"charter", "deactivate", "styleguide", "x"},
		{"charter", "list", "--json"},
		{"charter", "activate", "--preset", "default", "--json"},
		{"charter", "activate", "--preset", "minimal", "--force"},
		{"charter", "activate", "--preset", "minimal"},
		{"charter", "activate", "--preset", "nope", "--json"},
		{"charter", "list", "--json"},
	} {
		compareCharter(t, p, args...)
	}
	if g, o := p.Go.ReadFile(".kittify/config.yaml"), p.Oracle.ReadFile(".kittify/config.yaml"); p.Go.Normalize(g) != p.Oracle.Normalize(o) {
		t.Errorf("config.yaml differs:\n%s", testkit.Diff(p.Oracle.Normalize(o), p.Go.Normalize(g)))
	}
}
