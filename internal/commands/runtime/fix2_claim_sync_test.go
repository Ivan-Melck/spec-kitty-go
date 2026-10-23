package runtime_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffReviewForceMovedUnimplemented: a WP force-moved to for_review
// without ever being implemented. The review claim commits, then the lane
// sync fails (no lane branch): upstream reverts the claim commit, refuses
// to cut the rows and reports the committed claim.
func TestDiffReviewForceMovedUnimplemented(t *testing.T) {
	p := newAnalyzedPair(t)
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "for_review", "--force", "--mission", "$M", "--agent", "claude", "--note", "forced")
	steps(t, p, true, []string{"agent", "action", "review", "WP02", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	dropAnalysis(t, p)
	compareTree(t, p, "kitty-specs")
}

// coordCommit commits files directly onto the coordination branch (its
// worktree) on both sides.
func coordCommit(t *testing.T, p *testkit.Pair, msg string, files map[string]string) {
	p.Both(func(s *testkit.Sandbox) {
		d := filepath.Join(".worktrees", missionfx.MissionSlug(t, s)+"-coord")
		var names []string
		for f, c := range files {
			s.WriteFile(filepath.Join(d, f), c)
			names = append(names, f)
		}
		s.Exec(d, "git", append([]string{"add"}, names...)...)
		if r := s.Exec(d, "git", "commit", "-q", "-m", msg); r.Code != 0 {
			t.Fatalf("coord commit: %s%s", r.Stdout, r.Stderr)
		}
	})
}

// TestDiffImplementResumeLaneSyncConflict: the liveness refresh of a
// resumed implement commits, then the lane sync halts on a conflict the
// classifier cannot resolve.
func TestDiffImplementResumeLaneSyncConflict(t *testing.T) {
	p := newAnalyzedPair(t)
	steps(t, p, false, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"})
	laneCommit(t, p, "lane-a", "lane readme", map[string]string{"README.md": "lane side\n"})
	coordCommit(t, p, "coord readme", map[string]string{"README.md": "coord side\n"})
	steps(t, p, true, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	compareTree(t, p, "kitty-specs", "analysis-report.md")
	// bare resume: the liveness refresh hits the same refusal
	steps(t, p, true, []string{"agent", "action", "implement", "WP01", "--mission", "$M"})
	compareGit(t, p)
	compareTree(t, p, "kitty-specs", "analysis-report.md")
}

// installFailingHook makes every commit in both sandboxes fail.
func installFailingHook(t *testing.T, p *testkit.Pair) {
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".git/hooks/pre-commit", "#!/bin/sh\necho hook-refused >&2\nexit 1\n")
		s.Exec("", "chmod", "+x", ".git/hooks/pre-commit")
	})
}

// TestDiffClaimCommitRefusedByHook: the implement claim backs a foreign
// pre-commit hook up to the "<hook>.<stamp>" sidecar before installing the
// commit guard; a foreign hook installed later refuses the review claim's
// commits.
func TestDiffClaimCommitRefusedByHook(t *testing.T) {
	p := newAnalyzedPair(t)
	installFailingHook(t, p)
	steps(t, p, true, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	laneCommit(t, p, "lane-a", "feat(WP01): subtract", map[string]string{"src/calc.py": "def subtract(a, b):\n    return a - b\n"})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Ready")
	installFailingHook(t, p)
	steps(t, p, true, []string{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	compareCoordStatus(t, p)
	compareTree(t, p, "kitty-specs", "analysis-report.md")
	compareTree(t, p, ".worktrees", "analysis-report.md")
	// the same transactional door under move-task
	steps(t, p, true, []string{"agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", "$M", "--agent", "claude"})
	compareCoordStatus(t, p)
	compareTree(t, p, ".worktrees", "analysis-report.md")
}

// compareCoordStatus compares the coordination worktree's index/worktree
// status (a refused commit restores the staging it found).
func compareCoordStatus(t *testing.T, p *testkit.Pair) {
	t.Helper()
	st := func(s *testkit.Sandbox) string {
		d := filepath.Join(".worktrees", missionfx.MissionSlug(t, s)+"-coord")
		return norm(s, s.Exec(d, "git", "status", "--porcelain", "--untracked-files=all").Stdout)
	}
	if g, o := st(p.Go), st(p.Oracle); g != o {
		t.Errorf("coord worktree status differs:\n%s", testkit.Diff(o, g))
	}
}
