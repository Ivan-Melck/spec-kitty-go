package workflow

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestCheckClaimAncestry exercises the claim-ancestry predicate: a recorded
// planning commit missing from the workspace HEAD is reported with
// upstream's wording, an ancestor passes, and the self-heal re-entry merges
// it in.
func TestCheckClaimAncestry(t *testing.T) {
	s := missionfx.NewLocalMission(t)
	root := s.Dir
	slug := missionfx.LocalSlug
	// the lane worktree forks before a newer commit becomes the pin
	wt := lanes.WorktreePath(root, slug, "lane-a")
	m, err := lanes.Read(project.Specs(root, slug))
	if err != nil || m == nil {
		t.Fatalf("lanes: %v", err)
	}
	branch := lanes.LaneBranchName(slug, "lane-a", m.TargetBranch)
	s.Git("worktree", "add", "-q", "-b", branch, wt)
	s.WriteFile("notes.md", "planning note\n")
	s.Git("add", "notes.md")
	s.Git("commit", "-q", "-m", "planning artifact")
	pin := s.Git("rev-parse", "HEAD")
	m.PlanningCommitSHA = &pin
	if err := lanes.Write(project.Specs(root, slug), m); err != nil {
		t.Fatal(err)
	}
	statusDir := project.Specs(root, slug)
	r := CheckClaimAncestry(root, slug, statusDir, "WP01", wt)
	if r.OK || len(r.Missing) != 1 || r.Missing[0] != "recorded planning commit "+pin {
		t.Fatalf("unexpected %+v", r)
	}
	if r := CheckClaimAncestry(root, slug, statusDir, "WP01", root); !r.OK {
		t.Fatalf("root has the pin: %+v", r)
	}
	if r := ResolveClaimAncestry(root, slug, statusDir, "WP01", wt); !r.OK {
		t.Fatalf("self-heal should merge the pin: %+v", r)
	}
	if !isAncestorIn(filepath.Clean(wt), pin, "HEAD") || strings.TrimSpace(pin) == "" {
		t.Fatal("expected the self-heal to merge the pin")
	}
}
