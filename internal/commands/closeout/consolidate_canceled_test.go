package closeout_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffConsolidateCanceledLane covers a mission whose WP02 was canceled
// with operator provenance (move-task --to canceled --note): the canceled
// WP is excluded from the merge-ready precondition, the gates and the done
// bookkeeping, and its fully-canceled lane is not integrated.
func TestDiffConsolidateCanceledLane(t *testing.T) {
	t.Run("canceled-leaf", func(t *testing.T) {
		t.Parallel()
		p := approvedPair(t, "for_review")
		oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "canceled", "--mission", "$M", "--agent", "claude", "--note", "dropped: out of scope", "--force")
		recordVerdictsAndDocs(t, p, true)
		p.Both(func(s *testkit.Sandbox) {
			s.Exec("", "git", "add", "-A")
			s.Exec("", "git", "commit", "-q", "-m", "wip")
		})
		comparePair(t, p, "consolidate", "--mission", "$M", "--dry-run")
		comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
		compareGit(t, p)
		compareMissionTree(t, p, false)
	})
	// A canceled dependency lane (WP01 canceled after WP02 was built on it):
	// its content rides lane-b onto the target (#5569/#5613).
	t.Run("canceled-dependency", func(t *testing.T) {
		t.Parallel()
		p := approvedPair(t, "approved")
		oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "canceled", "--mission", "$M", "--agent", "claude", "--note", "superseded by WP02", "--force")
		recordVerdictsAndDocs(t, p, true)
		p.Both(func(s *testkit.Sandbox) {
			s.Exec("", "git", "add", "-A")
			s.Exec("", "git", "commit", "-q", "-m", "wip")
		})
		comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
		compareGit(t, p)
		compareMissionTree(t, p, false)
	})
}
