package closeout_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffConsolidateSkipLanes covers --skip-lanes: with lanes.json present
// it is honored as-is; once lanes.json is genuinely absent a no-lane
// planning manifest is synthesized and the planning-artifact-only flow lands
// the done bookkeeping directly on the target.
func TestDiffConsolidateSkipLanes(t *testing.T) {
	p := acceptedPair(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	comparePair(t, p, "consolidate", "--mission", "$M", "--skip-lanes", "--dry-run")
	p.Both(func(s *testkit.Sandbox) {
		s.Git("rm", "-q", filepath.Join("kitty-specs", slugOf(t, s), "lanes.json"))
		s.Git("commit", "-q", "-m", "drop lanes")
	})
	comparePair(t, p, "consolidate", "--mission", "$M", "--skip-lanes", "--dry-run", "--json")
	comparePair(t, p, "consolidate", "--mission", "$M", "--skip-lanes", "--yes")
	compareGit(t, p)
	compareMissionTree(t, p, false)
}
