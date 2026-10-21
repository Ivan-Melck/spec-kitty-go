package closeout_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var verdictCases = [][]string{
	{"agent", "acceptance-verdict", "--mission", "$M", "--criterion", "FR-001", "--result", "pass", "--verification-method", "automated_test", "--actor", "claude", "--evidence", "tests/test_subtract.py", "--json"},
	{"agent", "acceptance-verdict", "--mission", "$M", "--criterion", "FR-001", "--result", "pass", "--verification-method", "automated_test", "--actor", "claude", "--evidence", "tests/test_subtract.py", "--json"},
	{"agent", "mission", "acceptance-verdict", "--mission", "$M", "--criterion", "FR-002", "--result", "fail"},
	{"agent", "acceptance-verdict", "--mission", "$M", "--criterion", "FR-009", "--result", "pass"},
	{"agent", "acceptance-verdict", "--mission", "$M", "--criterion", "FR-009", "--result", "bogus", "--json"},
	{"agent", "acceptance-verdict", "--mission", "$M", "--json"},
	{"agent", "acceptance-verdict", "--mission", "$M", "--criterion", "FR-001", "--negative-invariant", "NI-1"},
	{"agent", "acceptance-verdict", "--mission", "$M", "--negative-invariant", "NI-1", "--description", "no zap", "--verification-method", "grep_absence", "--verification-command", "XYZZY_NOPE", "--scope", "src", "--json"},
	{"agent", "acceptance-verdict", "--mission", "$M", "--negative-invariant", "NI-3", "--description", "cmd", "--verification-method", "custom_command", "--verification-command", "true", "--no-execute"},
	{"agent", "acceptance-verdict", "--mission", "no-such-mission-01ZZZZZZ", "--criterion", "FR-001", "--result", "pass", "--json"},
	{"agent", "acceptance-verdict", "--mission", "nope", "--criterion", "FR-001", "--result", "pass"},
}

// TestDiffAcceptanceVerdict covers TRACE 6.2/6.3 on coord and lanes topologies.
func TestDiffAcceptanceVerdict(t *testing.T) {
	for _, topo := range []string{"coord", "lanes"} {
		topo := topo
		t.Run(topo, func(t *testing.T) {
			t.Parallel()
			var p *testkit.Pair
			if topo == "lanes" {
				p = approvedPair(t, "approved", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
			} else {
				p = approvedPair(t, "approved")
			}
			for _, c := range verdictCases {
				comparePair(t, p, c...)
			}
			compareGit(t, p)
			compareMissionTree(t, p, true)
		})
	}
}

// TestDiffAccept covers TRACE 5.F5, 6.1, 6.4, 6.6 and 9.3.
func TestDiffAccept(t *testing.T) {
	t.Run("for-review-dirty", func(t *testing.T) {
		t.Parallel()
		p := approvedPair(t, "for_review")
		p.Both(func(s *testkit.Sandbox) {
			s.WriteFile("kitty-ops/lifecycle.jsonl", "{}\n")
			s.WriteFile("README.md", "# demo\nchanged\n")
		})
		comparePair(t, p, "agent", "mission", "accept", "--mission", "$M", "--json")
		comparePair(t, p, "agent", "mission", "accept", "--mission", "$M")
		comparePair(t, p, "agent", "mission", "accept", "--mission", "$M", "--diagnose")
		comparePair(t, p, "accept", "--mission", "$M", "--lenient", "--json")
		comparePair(t, p, "accept", "--mission", "$M", "--mode", "pr", "--json")
	})
	for _, topo := range []string{"coord", "lanes"} {
		topo := topo
		t.Run(topo, func(t *testing.T) {
			t.Parallel()
			var p *testkit.Pair
			if topo == "lanes" {
				p = approvedPair(t, "approved", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
			} else {
				p = approvedPair(t, "approved")
			}
			for _, c := range [][]string{
				{"agent", "mission", "accept", "--mission", "$M", "--json"},
				{"agent", "mission", "accept", "--mission", "$M"},
				{"agent", "mission", "accept", "--mission", "calc-ops", "--diagnose", "--json"},
				{"agent", "mission", "accept", "--mission", "$M", "--diagnose"},
				{"accept", "--mission", "$M", "--no-commit", "--json"},
				{"accept", "--json"},
				{"accept"},
				{"accept", "--mission", "no-such-mission-01ZZZZZZ", "--json"},
				{"agent", "mission", "accept", "--mission", "no-such-mission-01ZZZZZZ"},
				{"accept", "--mission", "$M", "--mode", "checklist", "--json"},
				{"accept", "--mission", "$M", "--merge-commit", "abc", "--json"},
			} {
				comparePair(t, p, c...)
			}
			recordVerdictsAndDocs(t, p, false)
			comparePair(t, p, "agent", "mission", "accept", "--mission", "$M", "--diagnose", "--json")
			comparePair(t, p, "agent", "mission", "accept", "--mission", "$M", "--json")
			compareGit(t, p)
			compareMissionTree(t, p, false)
		})
	}
}

// TestDiffConsolidate covers TRACE 7.1-7.4 and 8.1 on both topologies.
func TestDiffConsolidate(t *testing.T) {
	for _, topo := range []string{"coord", "lanes"} {
		topo := topo
		t.Run(topo, func(t *testing.T) {
			t.Parallel()
			var p *testkit.Pair
			if topo == "lanes" {
				p = acceptedPair(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
			} else {
				p = acceptedPair(t)
			}
			comparePair(t, p, "consolidate", "--mission", "$M", "--dry-run", "--json")
			comparePair(t, p, "consolidate", "--mission", "$M", "--dry-run")
			comparePair(t, p, "consolidate", "--mission", "$M", "--dry-run", "--keep-branch", "--json")
			comparePair(t, p, "consolidate", "--mission", "nope", "--yes")
			comparePair(t, p, "consolidate", "--mission", "$M", "--json")
			comparePair(t, p, "consolidate", "--yes")
			comparePair(t, p, "consolidate", "--skip-review-artifact-check")
			if topo == "coord" {
				// TRACE 7.2/7.3: untracked coordination residue refuses, commit, retry.
				p.Both(func(s *testkit.Sandbox) {
					slug := slugOf(t, s)
					s.WriteFile(filepath.Join(".worktrees", slug+"-coord", "kitty-specs", slug, "mission-events.jsonl"), "{}\n")
				})
				comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
				p.Both(func(s *testkit.Sandbox) {
					slug := slugOf(t, s)
					wt := filepath.Join(".worktrees", slug+"-coord")
					s.Exec(wt, "git", "add", "-A")
					s.Exec(wt, "git", "commit", "-q", "-m", "commit coord residue")
				})
			}
			p.Both(func(s *testkit.Sandbox) { s.WriteFile("README.md", "# demo\nedit\n") })
			comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
			p.Both(func(s *testkit.Sandbox) { s.Git("checkout", "--", "README.md") })
			comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
			compareGit(t, p)
			compareMissionTree(t, p, false)
			comparePair(t, p, "agent", "tasks", "status", "--mission", "$M")
		})
	}
}

// TestDiffConsolidateNotReady covers the merge-ready refusal.
func TestDiffConsolidateNotReady(t *testing.T) {
	p := approvedPair(t, "for_review")
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	comparePair(t, p, "agent", "mission", "merge", "--mission", "calc-ops", "--dry-run")
	comparePair(t, p, "agent", "mission", "merge", "--mission", "$M", "--dry-run")
	compareGit(t, p)
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
