package closeout_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffConsolidateReconcile covers the reconciliation claim and gate:
// a lane that moved after its approval refuses at claim time
// (LANE_MOVED_AFTER_APPROVAL, before any change), and content that rides
// the mission branch onto the target without belonging to an approved WP
// fails the gate (squash blob attribution, merge/rebase closed world), rolls
// every moved branch back and keeps the record for --abort.
func TestDiffConsolidateReconcile(t *testing.T) {
	for _, sc := range []string{"lane-moved", "lane-moved-merge", "teammate", "teammate-merge", "teammate-rebase", "protected"} {
		sc := sc
		t.Run(sc, func(t *testing.T) {
			t.Parallel()
			if sc == "protected" {
				// #5385: a lanes mission whose recorded target is protected
				// refuses before any branch moves (and in the dry-run forecast).
				p := acceptedPair(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
				p.Both(func(s *testkit.Sandbox) {
					cfg := s.ReadFile(".kittify/config.yaml")
					s.WriteFile(".kittify/config.yaml", strings.TrimRight(cfg, "\n")+"\nprotection:\n  protected_branches:\n  - feat/calc-ops\n")
					s.Git("add", ".kittify/config.yaml")
					s.Git("commit", "-q", "-m", "protect target")
				})
				comparePair(t, p, "consolidate", "--mission", "$M", "--dry-run")
				comparePair(t, p, "consolidate", "--mission", "$M", "--dry-run", "--json")
				comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
				compareGit(t, p)
				return
			}
			p := acceptedPair(t)
			args := []string{"consolidate", "--mission", "$M", "--yes"}
			if strings.HasSuffix(sc, "-merge") {
				args = append(args, "--strategy", "merge")
			} else if strings.HasSuffix(sc, "-rebase") {
				args = append(args, "--strategy", "rebase")
			}
			if strings.HasPrefix(sc, "lane-moved") {
				p.Both(func(s *testkit.Sandbox) {
					laneCommit(t, s, "lane-a", map[string]string{"src/extra.py": "X = 1\n"}, "late: extra")
				})
				comparePair(t, p, args...)
				compareGit(t, p)
				return
			}
			teammateCommit(t, p, map[string]string{"src/teammate.py": "T = 1\n"}, "teammate: new file")
			comparePair(t, p, args...)
			compareGit(t, p)
			comparePair(t, p, "consolidate", "--mission", "$M", "--abort")
			compareGit(t, p)
		})
	}
}
