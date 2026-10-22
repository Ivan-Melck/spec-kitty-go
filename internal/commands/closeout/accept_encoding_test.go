package closeout_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// corruptArtifact rewrites a mission artifact on both sides: tail is
// appended as raw (non-UTF-8) bytes.
func corruptArtifact(t *testing.T, p *testkit.Pair, name, tail string) {
	t.Helper()
	p.Both(func(s *testkit.Sandbox) {
		path := filepath.Join(s.Dir, "kitty-specs", slugOf(t, s), name)
		b, err := os.ReadFile(path)
		if err != nil {
			b = []byte("# " + name + "\n")
		}
		if err := os.WriteFile(path, append(b, []byte(tail)...), 0o644); err != nil {
			t.Fatal(err)
		}
	})
}

func frameLines(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, ".py:") || strings.Contains(l, "Error") {
			out = append(out, strings.TrimSpace(strings.Trim(l, "│ ")))
		}
	}
	return strings.Join(out, "\n")
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func compareArtifact(t *testing.T, p *testkit.Pair, rel string) {
	t.Helper()
	g := p.Go.Normalize(p.Go.ReadFile(filepath.Join("kitty-specs", slugOf(t, p.Go), rel)))
	o := p.Oracle.Normalize(p.Oracle.ReadFile(filepath.Join("kitty-specs", slugOf(t, p.Oracle), rel)))
	if g != o {
		t.Errorf("%s differs:\n%s", rel, testkit.Diff(o, g))
	}
}

// TestDiffAcceptNormalizeEncoding covers ArtifactEncodingError and
// accept --normalize-encoding over the charset_normalizer port: a
// recoverable cp1252 artifact is rewritten (with a .bak), an ambiguous one
// is refused and left untouched.
func TestDiffAcceptNormalizeEncoding(t *testing.T) {
	t.Run("cp1252", func(t *testing.T) {
		t.Parallel()
		p := approvedPair(t, "approved")
		corruptArtifact(t, p, "spec.md", "\nThe team\x92s \x93principles\x94 are simple \x97 ship small, review everything, and keep the build green.\n")
		comparePair(t, p, "accept", "--mission", "$M", "--json")
		comparePair(t, p, "accept", "--mission", "$M", "--no-commit")
		comparePair(t, p, "accept", "--mission", "$M", "--no-commit", "--json", "--normalize-encoding")
		compareArtifact(t, p, "spec.md")
		compareArtifact(t, p, "spec.md.bak")
		comparePair(t, p, "accept", "--mission", "$M", "--no-commit", "--json", "--normalize-encoding")
	})
	t.Run("ambiguous", func(t *testing.T) {
		t.Parallel()
		p := approvedPair(t, "approved")
		corruptArtifact(t, p, "plan.md", "\nplain ascii text with one stray byte \xe9 here\n")
		comparePair(t, p, "accept", "--mission", "$M", "--no-commit", "--normalize-encoding")
		comparePair(t, p, "accept", "--mission", "$M", "--no-commit", "--json", "--normalize-encoding")
		compareArtifact(t, p, "plan.md")
		if _, err := os.Stat(filepath.Join(p.Go.Dir, "kitty-specs", slugOf(t, p.Go), "plan.md.bak")); err == nil {
			t.Errorf("ambiguous artifact was backed up/rewritten")
		}
	})
	t.Run("collision", func(t *testing.T) {
		t.Parallel()
		p := approvedPair(t, "approved")
		p.Both(func(s *testkit.Sandbox) {
			matches, _ := filepath.Glob(filepath.Join(s.Dir, "kitty-specs", slugOf(t, s), "tasks", "WP01*.md"))
			if len(matches) != 1 {
				t.Fatalf("WP01 file: %v", matches)
			}
			b, _ := os.ReadFile(matches[0])
			os.WriteFile(matches[0], append(b, []byte("\nThe team\x92s \x93principles\x94 are simple \x97 ship small and review everything.\n")...), 0o644)
			os.WriteFile(matches[0]+".bak", []byte("old"), 0o644)
		})
		comparePair(t, p, "accept", "--mission", "$M", "--no-commit", "--json")
		comparePair(t, p, "accept", "--mission", "$M", "--no-commit", "--json", "--normalize-encoding")
		comparePair(t, p, "accept", "--mission", "$M", "--no-commit", "--normalize-encoding")
	})
}
