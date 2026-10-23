package maintenance_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffPlacementCoordWorktreeWrongBranch: a materialized coordination
// worktree checked out on another branch makes placement_seam.read_dir of
// a coordination kind raise CoordinationWorkspaceBranchMismatch
// (require_coord_worktree_branch); the read-only doctors that read through
// the seam surface it.
func TestDiffPlacementCoordWorktreeWrongBranch(t *testing.T) {
	p := newOraclePair(t)
	goSlug, orSlug := missionfx.OracleCalcMission(t, p)
	p.Both(func(s *testkit.Sandbox) {
		wt := filepath.Join(".worktrees", missionfx.MissionSlug(t, s)+"-coord")
		if r := s.Exec(wt, "git", "switch", "-q", "-c", "operator-side"); r.Code != 0 {
			t.Fatalf("switch: %s", r.Stderr)
		}
	})
	compareSeamSteps(t, p, map[string]string{"$M": goSlug}, map[string]string{"$M": orSlug},
		step{"doctor", "decisions", "--mission", "$M"},
		step{"doctor", "decisions", "--mission", "$M", "--json"},
		step{"doctor", "review-cycle-reconcile", "--mission", "$M"},
		step{"doctor", "review-cycle-reconcile", "--mission", "$M", "--json"},
	)
}
