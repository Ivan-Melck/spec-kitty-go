package runtime_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffDirtyVerdicts covers the ref_advance dirty verdicts behind the
// review-lane fast-forward (untracked and gitignored paths that the resync
// `reset --hard` would overwrite refuse; others survive) and behind the
// discard's guarded lane-worktree removal (every untracked file listed one
// by one).
func TestDiffDirtyVerdicts(t *testing.T) {
	t.Run("review-untracked-obstruction", func(t *testing.T) {
		t.Parallel()
		p := reviewRemotePair(t, false)
		p.Both(func(s *testkit.Sandbox) {
			wt := laneDir(t, s, "lane-a")
			s.WriteFile(filepath.Join(wt, "TEAM.md"), "my own TEAM notes\n")
			s.WriteFile(filepath.Join(wt, "scratch.txt"), "survives the reset\n")
		})
		steps(t, p, false, []string{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"})
		compareGit(t, p)
	})
	t.Run("review-ignored-obstruction", func(t *testing.T) {
		t.Parallel()
		p := reviewRemotePair(t, false)
		p.Both(func(s *testkit.Sandbox) {
			wt := laneDir(t, s, "lane-a")
			ex := filepath.Join(s.Dir, ".git", "info", "exclude")
			b, _ := os.ReadFile(ex)
			os.WriteFile(ex, append(b, []byte("TEAM.md\nbuild/\n")...), 0o644)
			s.WriteFile(filepath.Join(wt, "TEAM.md"), "ignored local copy\n")
			s.WriteFile(filepath.Join(wt, "build", "out.bin"), "artifact\n")
		})
		steps(t, p, false, []string{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"})
		compareGit(t, p)
	})
	t.Run("review-ignored-survives", func(t *testing.T) {
		t.Parallel()
		p := reviewRemotePair(t, false)
		p.Both(func(s *testkit.Sandbox) {
			wt := laneDir(t, s, "lane-a")
			ex := filepath.Join(s.Dir, ".git", "info", "exclude")
			b, _ := os.ReadFile(ex)
			os.WriteFile(ex, append(b, []byte("build/\n")...), 0o644)
			s.WriteFile(filepath.Join(wt, "build", "out.bin"), "artifact\n")
		})
		steps(t, p, false, []string{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"})
		compareGit(t, p)
	})
	t.Run("discard-untracked-files", func(t *testing.T) {
		t.Parallel()
		p := newAnalyzedPair(t)
		oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
		p.Both(func(s *testkit.Sandbox) {
			wt := laneDir(t, s, "lane-a")
			s.WriteFile(filepath.Join(wt, "notes", "a.txt"), "a\n")
			s.WriteFile(filepath.Join(wt, "notes", "b.txt"), "b\n")
		})
		steps(t, p, false,
			[]string{"mission", "close", "--mission", "$M", "--discard", "--force"},
			[]string{"mission", "close", "--mission", "$M", "--discard", "--force", "--json"},
		)
		compareGit(t, p)
	})
}
