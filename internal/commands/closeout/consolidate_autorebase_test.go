package closeout_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// teammateCommit commits files to the mission (coordination) branch through
// its worktree, as a teammate's direct push would land.
func teammateCommit(t *testing.T, p *testkit.Pair, files map[string]string, msg string) {
	p.Both(func(s *testkit.Sandbox) {
		wt := filepath.Join(".worktrees", slugOf(t, s)+"-coord")
		var names []string
		for rel, body := range files {
			if body == "" {
				os.Remove(filepath.Join(s.Dir, wt, rel))
			} else {
				s.WriteFile(filepath.Join(wt, rel), body)
			}
			names = append(names, rel)
		}
		if r := s.Exec(wt, "git", append([]string{"add", "-A", "--"}, names...)...); r.Code != 0 {
			t.Fatalf("git add: %s", r.Stderr)
		}
		if r := s.Exec(wt, "git", "commit", "-q", "-m", msg); r.Code != 0 {
			t.Fatalf("git commit: %s%s", r.Stdout, r.Stderr)
		}
	})
}

// TestDiffConsolidateAutoRebaseManual covers a stale lane whose conflict no
// classifier rule resolves (a source file): the auto-rebase aborts, the lane
// stays stale, and the failure inside the mutation span prints the rollback
// report. (The rule-resolved path is covered through orchestrator-api
// consolidate-mission, which has no reconciliation verifier.)
func TestDiffConsolidateAutoRebaseManual(t *testing.T) {
	p := missionfxCalc(t)
	teammateCommit(t, p, map[string]string{"src/calc.py": "def add(a, b):\n    return b + a\n"}, "teammate: calc")
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes", "--strategy", "merge")
	compareGit(t, p)
	// attestation / release flag validation (CLI boundary, record kept)
	for _, c := range [][]string{
		{"consolidate", "--mission", "$M", "--attest-canceled-superseded", "WP01"},
		{"consolidate", "--mission", "$M", "--attest-approved-reviewed", "WP01"},
		{"consolidate", "--mission", "$M", "--dry-run", "--attest-reason", "checked"},
		{"consolidate", "--mission", "$M", "--dry-run", "--attest-approved-reviewed", "WP01", "--attest-canceled-superseded", "WP02", "--attest-reason", "checked"},
		{"consolidate", "--mission", "$M", "--release-branch", "main", "--release-reason", "r"},
		{"consolidate", "--mission", "$M", "--abort", "--release-branch", "main"},
		{"consolidate", "--mission", "$M", "--abort", "--release-branch", "kitty/mission-$M-lane-a", "--release-reason", "r"},
	} {
		comparePair(t, p, c...)
	}
	comparePair(t, p, "consolidate", "--mission", "$M", "--abort")
	compareGit(t, p)
}

// TestDiffConsolidateAutoRebaseGuardedAbort covers guarded_merge_abort: a
// failed auto-rebase whose lane worktree holds an operator edit outside the
// merge leaves the merge in progress instead of discarding the edit.
func TestDiffConsolidateAutoRebaseGuardedAbort(t *testing.T) {
	p := missionfxCalc(t)
	teammateCommit(t, p, map[string]string{"src/calc.py": "def add(a, b):\n    return b + a\n"}, "teammate: calc")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(filepath.Join(".worktrees", slugOf(t, s)+"-lane-a", "tests", "test_subtract.py"), "# operator edit\n")
	})
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes", "--keep-worktree")
	compareGit(t, p)
	laneState := func(s *testkit.Sandbox) string {
		wt := filepath.Join(".worktrees", slugOf(t, s)+"-lane-a")
		st := s.Exec(wt, "git", "status", "--porcelain=v1", "--untracked-files=all")
		mh := s.Exec(wt, "git", "rev-parse", "-q", "--verify", "MERGE_HEAD")
		return s.Normalize(st.Stdout) + "\nMERGE_HEAD present: " + map[bool]string{true: "yes", false: "no"}[mh.Code == 0]
	}
	if g, o := laneState(p.Go), laneState(p.Oracle); g != o {
		t.Errorf("lane worktree state differs:\n%s", testkit.Diff(o, g))
	}
}

// missionfxCalc is acceptedPair whose lane-a also adds a pyproject
// dependency and a package __init__.
func missionfxCalc(t *testing.T) *testkit.Pair {
	t.Helper()
	p := approvedPairExtra(t, "approved", map[string]string{
		"pyproject.toml":  "[project]\nname = \"calc\"\ndependencies = [\n    \"click>=8\",\n]\n",
		"src/__init__.py": "from .calc import add\n",
	})
	recordVerdictsAndDocs(t, p, true)
	oracleBoth(t, p, "agent", "mission", "accept", "--mission", "$M", "--json")
	return p
}
