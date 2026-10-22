package closeout_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffAgentMergeAutoRetry covers `agent mission merge --auto-retry`:
// without --mission it refuses; with --mission it re-runs the merge in the
// mission's lane worktree (each side's own `spec-kitty` on PATH).
func TestDiffAgentMergeAutoRetry(t *testing.T) {
	p := acceptedPair(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	for _, side := range []struct {
		s   *testkit.Sandbox
		exe string
	}{{p.Go, testkit.Binary(t)}, {p.Oracle, testkit.Oracle(t)}} {
		bin := filepath.Join(side.s.Base, "bin")
		os.MkdirAll(bin, 0o755)
		if err := os.Symlink(side.exe, filepath.Join(bin, "spec-kitty")); err != nil {
			t.Fatal(err)
		}
		side.s.Env = append(side.s.Env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	comparePair(t, p, "agent", "mission", "merge", "--auto-retry", "--dry-run")
	comparePair(t, p, "agent", "mission", "merge", "--mission", "$M", "--auto-retry", "--dry-run")
	comparePair(t, p, "agent", "mission", "merge", "--mission", "$M", "--auto-retry", "--dry-run", "--keep-branch", "--remove-worktree")
}
