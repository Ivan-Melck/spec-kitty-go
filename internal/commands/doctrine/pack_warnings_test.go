package doctrine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// Compare complete warning records: only the oracle's installation prefix
// and Rich's line wrapping are removed. Message, source, count and order stay.
func TestParityBadPacksRootWarnings(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.Env = append(s.Env, "SPEC_KITTY_PACKS_ROOT=/nonexistent-packs-root")
	})
	for _, args := range [][]string{
		{"--version"}, {"--help"}, {"mission", "list"},
		{"doctor", "provenance"}, {"doctor", "skills"},
		{"doctor", "tool-surfaces"}, {"doctor", "charter-packs"},
		{"agent", "config", "list"}, {"agent", "config", "status"}, {"charter", "status"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			g, o := p.Run(args...)
			gr := strings.Join(logRecords(p.Go.Normalize(g.Stderr)), "\n")
			or := strings.Join(logRecords(p.Oracle.Normalize(o.Stderr)), "\n")
			if g.Code != o.Code || gr != or {
				t.Errorf("exit go=%d oracle=%d; stderr records:\n%s", g.Code, o.Code, testkit.Diff(or, gr))
			}
		})
	}
	for _, args := range [][]string{
		{"doctor", "provenance", "--json"},
		{"doctor", "charter-packs", "--json"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) { p.Compare(true, args...) })
	}
	// The provenance doctor resolves a mission token even with no matching
	// template-set reference, and repeated references deduplicate warnings.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter/charter.yaml", "catalog:\n  mission: software-dev\n  template_set: default\n  references:\n"+
			"  - id: DIRECTIVE:x\n    kind: directive\n    source_path: /nonexistent-packs-root/built-in/directives/x.yaml\n"+
			"  - id: DIRECTIVE:y\n    kind: directive\n    source_path: /nonexistent-packs-root/built-in/directives/y.yaml\n")
	})
	compareDoctorHuman(t, p, "doctor", "provenance")
	p.Compare(true, "doctor", "provenance", "--json")
}

func TestParityPackRootWarningPaths(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"missing", "file", "valid", "parent", "double-slash"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			p := testkit.NewPair(t)
			p.Both(func(s *testkit.Sandbox) {
				// Relative paths, spaces, backslashes and both quote types test
				// Path.__str__ and Python repr independently of root masking.
				root := "packs 'quoted\" \\ path"
				switch kind {
				case "parent":
					root = "missing//./parent/../packs"
				case "double-slash":
					root = "//nonexistent-packs-root///./"
				case "file":
					s.WriteFile(filepath.Join(root, "built-in"), "not a directory\n")
				case "valid":
					if _, err := assets.CopyTree(assets.BuiltInPackRoot, filepath.Join(s.Dir, root, "built-in"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				s.Env = append(s.Env, "SPEC_KITTY_PACKS_ROOT="+root)
			})
			g, o := compareLogRecords(t, p, "--version")
			if g.Stdout != o.Stdout {
				t.Errorf("stdout:\n%s", testkit.Diff(o.Stdout, g.Stdout))
			}
		})
	}
}
