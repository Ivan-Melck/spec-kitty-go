package runtime_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffImplementBaseRecover covers `implement --base` (unresolved ref,
// unhonorable reuse/dependency routes, an honored fresh base) and
// `implement --recover` after a simulated crash (lane worktree and
// workspace context lost).
func TestDiffImplementBaseRecover(t *testing.T) {
	p := newAnalyzedPair(t)
	steps(t, p, true,
		[]string{"implement", "WP01", "--mission", "$M", "--base", "no-such-ref"},
		[]string{"implement", "WP01", "--mission", "$M", "--base", "no-such-ref", "--json"},
		[]string{"implement", "WP01", "--mission", "$M", "--recover"},
		[]string{"implement", "WP01", "--mission", "$M", "--recover", "--json"},
	)
	steps(t, p, false, []string{"implement", "WP01", "--mission", "$M", "--base", "feat/calc-ops"})
	compareGit(t, p)
	compareTree(t, p, ".kittify/workspaces")
	steps(t, p, true,
		[]string{"implement", "WP01", "--mission", "$M", "--base", "feat/calc-ops"},
		[]string{"implement", "WP02", "--mission", "$M", "--base", "feat/calc-ops"},
	)
	laneCommit(t, p, "lane-a", "feat(WP01): subtract", map[string]string{
		"src/calc.py": "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n",
	})
	// crash: the lane worktree directory and its workspace context vanish
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		os.RemoveAll(filepath.Join(s.Dir, laneDir(t, s, "lane-a")))
		os.Remove(filepath.Join(s.Dir, ".kittify", "workspaces", slug+"-lane-a.json"))
	})
	steps(t, p, true,
		[]string{"implement", "WP01", "--mission", "$M", "--recover"},
		[]string{"implement", "WP01", "--mission", "$M", "--recover", "--json"},
	)
	compareGit(t, p)
	compareTree(t, p, ".kittify/workspaces")
	dropAnalysis(t, p)
	compareTree(t, p, "kitty-specs")
}
