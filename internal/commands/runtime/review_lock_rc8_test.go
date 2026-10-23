package runtime_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffReviewLockPreclaimRC8(t *testing.T) {
	p := newAnalyzedPair(t)
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "for_review", "--force", "--mission", "$M", "--agent", "claude", "--note", "forced")
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		ws, err := lanes.ResolveWorkspace(s.Dir, slug, "WP02")
		if err != nil {
			t.Fatal(err)
		}
		s.Git("worktree", "add", "-q", "-b", ws.BranchName, ws.WorktreePath)
		s.WriteFile(filepath.Join(ws.WorktreePath, ".spec-kitty/review-lock.json"), "{}")
	})
	review := []string{"agent", "action", "review", "WP02", "--mission", "$M", "--agent", "claude"}
	for _, scenario := range []string{"unreadable", "tracked", "tracked-deleted", "missing-workspace"} {
		t.Run(scenario, func(t *testing.T) {
			p.Both(func(s *testkit.Sandbox) {
				ws, _ := lanes.ResolveWorkspace(s.Dir, missionfx.MissionSlug(t, s), "WP02")
				switch scenario {
				case "tracked":
					missionfx.GitIn(t, s, ws.WorktreePath, "add", "-f", ".spec-kitty/review-lock.json")
					missionfx.GitIn(t, s, ws.WorktreePath, "commit", "-qm", "tracked lock")
				case "tracked-deleted":
					os.Remove(filepath.Join(ws.WorktreePath, ".spec-kitty/review-lock.json"))
				case "missing-workspace":
					s.Git("worktree", "remove", "--force", ws.WorktreePath)
				}
			})
			before := p.Go.Git("rev-parse", "HEAD")
			steps(t, p, true, review)
			if after := p.Go.Git("rev-parse", "HEAD"); before != after {
				t.Error("refusal committed a review claim")
			}
		})
	}
}
