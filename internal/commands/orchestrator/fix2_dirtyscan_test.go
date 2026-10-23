package orchestrator

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffSingleBranchDirtyScanGitFailure: `git status` fails in the
// single_branch dirty scan (a corrupt index) — upstream raises the
// RuntimeError out of start-implementation.
func TestDiffSingleBranchDirtyScanGitFailure(t *testing.T) {
	t.Parallel()
	p := singleBranchPair(t)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".git/index", "not an index\n") })
	g, o := step(t, p, api, "start-implementation", "--mission", "calc-ops", "--wp", "WP01", "--actor", "claude", "--policy", testPolicy)
	if gl, ol := lastLine(normEnvelope(p.Go, g.Stderr)), lastLine(normEnvelope(p.Oracle, o.Stderr)); gl != ol {
		t.Errorf("stderr tail differs\ngo:     %s\noracle: %s", gl, ol)
	}
}
