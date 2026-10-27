package closeout_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffAcceptWPWithoutFrontmatter covers an otherwise accept-ready
// mission whose WP02 task file has no frontmatter. Upstream's flat-layout
// scan reads it as empty frontmatter: the id falls back to the file stem
// and the title is empty. Kept as WP02-multiply.md the stem has no
// canonical state (accept fails on it); renamed to WP02.md it matches the
// approved WP and accept commits.
func TestDiffAcceptWPWithoutFrontmatter(t *testing.T) {
	for _, name := range []string{"", "WP02.md"} {
		name := name
		t.Run("stem"+name, func(t *testing.T) {
			t.Parallel()
			p := approvedPair(t, "approved")
			recordVerdictsAndDocs(t, p, true)
			p.Both(func(s *testkit.Sandbox) {
				m, _ := filepath.Glob(filepath.Join(s.Dir, "kitty-specs", "*", "tasks", "WP02-*.md"))
				if len(m) != 1 {
					t.Fatalf("WP02 file: %v", m)
				}
				b, _ := os.ReadFile(m[0])
				_, body, _ := fm.Split(string(b))
				dst := m[0]
				if name != "" {
					os.Remove(m[0])
					dst = filepath.Join(filepath.Dir(m[0]), name)
				}
				os.WriteFile(dst, []byte(body), 0o644)
				s.Git("add", "-A", "kitty-specs")
				s.Git("commit", "-q", "-m", "strip WP02 frontmatter")
			})
			for _, args := range [][]string{
				{"agent", "mission", "accept", "--mission", "$M", "--diagnose", "--json"},
				{"agent", "mission", "accept", "--mission", "$M", "--diagnose"},
				{"accept", "--mission", "$M", "--no-commit", "--json"},
				{"agent", "mission", "accept", "--mission", "$M", "--json"},
				// list-tasks walks the same split_frontmatter path.
				{"agent", "tasks", "list-tasks", "--mission", "$M", "--json"},
			} {
				comparePair(t, p, args...)
			}
			compareGit(t, p)
			compareMissionTree(t, p, false)
			if name != "" {
				// The merge's dependency gate reads WP frontmatter strictly
				// (FrontmatterManager): it reports the check unavailable.
				comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
				compareGit(t, p)
			}
		})
	}
}
