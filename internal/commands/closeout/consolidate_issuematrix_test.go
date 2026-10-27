package closeout_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffConsolidateIssueMatrixTerminality covers #6653/#6711: with the
// merge gates on, a present issue-matrix is judged whole at done even with
// zero gating references (an uncited in-mission row blocks), and a PASS
// names the rows it judged.
func TestDiffConsolidateIssueMatrixTerminality(t *testing.T) {
	p := acceptedPair(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	p.Both(func(s *testkit.Sandbox) {
		cfg := strings.TrimRight(s.ReadFile(".kittify/config.yaml"), "\n") + "\npolicy:\n  merge_gates:\n    mode: block\n"
		s.WriteFile(".kittify/config.yaml", cfg)
		s.Git("add", ".kittify/config.yaml")
		s.Git("commit", "-q", "-m", "gate policy block")
	})
	oracleBoth(t, p, "agent", "issue-verdict", "--mission", "$M", "--issue", "#12", "--verdict", "in-mission", "--actor", "claude", "--json")
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	compareGit(t, p)
	oracleBoth(t, p, "agent", "issue-verdict", "--mission", "$M", "--issue", "#12", "--verdict", "fixed", "--evidence-ref", "abc123", "--actor", "claude", "--json")
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	compareGit(t, p)
}
