package doctrine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func editFile(t *testing.T, s *testkit.Sandbox, rel string, fn func(string) string) {
	t.Helper()
	p := filepath.Join(s.Dir, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(fn(string(b))), 0o644); err != nil {
		t.Fatal(err)
	}
}

func compareFileText(t *testing.T, p *testkit.Pair, rel, step string) {
	t.Helper()
	g, o := p.Go.Normalize(p.Go.ReadFile(rel)), p.Oracle.Normalize(p.Oracle.ReadFile(rel))
	if g != o {
		t.Errorf("%s: %s differs\n%s", step, rel, testkit.Diff(o, g))
	}
}

// TestParityCommentedConfigRoundTrip covers the activation and charter
// pointer writers on a hand-commented .kittify/config.yaml (ruamel round
// trip through render_yaml_document): comments before, on and inside a
// changed entry, untouched entries byte-for-byte, then the charter.yaml
// activation target behind the `charter:` pointer.
func TestParityCommentedConfigRoundTrip(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		editFile(t, s, ".kittify/config.yaml", func(c string) string {
			c = "# Project config (hand-edited)\n" + strings.Replace(c, "vcs:", "# version control\nvcs:  # vcs block\n", 1)
			c = strings.Replace(c, "vcs:  # vcs block\n:", "vcs:  # vcs block\n", 1)
			return c + "\n# trailing notes\nactivated_directives:  # pinned\n  # keep these sorted\n  - 001-architectural-integrity-standard\n" +
				"  - 010-specification-fidelity-requirement\nextra_block:\n  # inner comment\n  a: 1   # one\n  b: 'two'\n# end of file\n"
		})
	})
	cfg := ".kittify/config.yaml"
	steps := [][]string{
		{"charter", "activate", "directive", "024-locality-of-change", "--no-compile"},
		{"charter", "deactivate", "directive", "001-architectural-integrity-standard", "--no-compile"},
		{"charter", "activate", "tactic", "tdd-red-green-refactor", "--no-compile"},
		{"charter", "interview", "--defaults"},
		{"charter", "generate"},
	}
	for _, c := range steps {
		g, o := p.Run(c...)
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d\n%s%s\n%s%s", c, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
		}
		compareFileText(t, p, cfg, strings.Join(c, " "))
	}
	// Behind the pointer: activation writes only the flat keys of charter.yaml.
	p.Both(func(s *testkit.Sandbox) {
		editFile(t, s, ".kittify/charter/charter.yaml", func(c string) string { return "# generated, then annotated\n" + c })
	})
	for _, c := range [][]string{
		{"charter", "activate", "directive", "025-boy-scout-rule", "--no-compile"},
		{"charter", "deactivate", "directive", "025-boy-scout-rule", "--no-compile"},
	} {
		g, o := p.Run(c...)
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d\n%s%s\n%s%s", c, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
		}
		compareFileText(t, p, cfg, strings.Join(c, " "))
		compareFileText(t, p, ".kittify/charter/charter.yaml", strings.Join(c, " "))
	}
}
