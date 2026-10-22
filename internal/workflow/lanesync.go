package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/autorebase"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// LaneSyncError is lifecycle_sync.LaneAutoRebaseSyncError.
type LaneSyncError struct {
	LaneID, LaneBranch, WorktreePath, CoordBranch, CoordHead, HaltReason string
}

func (e *LaneSyncError) Error() string {
	return fmt.Sprintf("LANE_AUTO_REBASE_FAILED: auto-rebase refused for %s: %s", e.LaneID, e.HaltReason)
}

// Render prints _render_lane_auto_rebase_failure.
func (e *LaneSyncError) Render(o *Out) {
	o.Printf("Error: LANE_AUTO_REBASE_FAILED: %s", e.HaltReason)
	o.Printf("  lane_id: %s", e.LaneID)
	o.Printf("  lane_worktree_path: %s", e.WorktreePath)
	o.Printf("  coordination_branch: %s", e.CoordBranch)
	head := "None"
	if e.CoordHead != "" {
		head = e.CoordHead
	}
	o.Printf("  coordination_head: %s", head)
}

// SyncLaneAfterCoordinationCommit is lifecycle_sync.sync_lane_after_coordination_commit:
// merge the coordination branch into the WP's lane worktree.
func SyncLaneAfterCoordinationCommit(root, slug, wpID, coordBranch string) error {
	m, err := lanes.Read(project.Specs(root, slug))
	if err != nil || m == nil {
		return nil
	}
	lane := m.LaneForWP(wpID)
	if lane == nil || lane.LaneID == lanes.PlanningLaneID {
		return nil
	}
	wt := lanes.WorktreePath(root, m.MissionSlug, lane.LaneID)
	branch := lanes.LaneBranchName(m.MissionSlug, lane.LaneID, m.TargetBranch)
	head := branchTip(root, coordBranch)
	fail := func(reason string) error {
		return &LaneSyncError{LaneID: lane.LaneID, LaneBranch: branch, WorktreePath: wt, CoordBranch: coordBranch, CoordHead: head, HaltReason: reason}
	}
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		os.MkdirAll(filepath.Dir(wt), 0o755)
		if r := git(root, "worktree", "add", wt, branch); r.Code != 0 {
			return fail("could not create lane worktree for auto-rebase: " + strings.TrimSpace(r.Stderr+r.Stdout))
		}
	}
	rep := autorebase.Attempt(autorebase.Options{Root: root, LaneID: lane.LaneID, Branch: branch, MissionBranch: coordBranch,
		Worktree: wt, Env: autorebase.MergeEnv(), EnsureDrivers: func() { autorebase.EnsureDriverConfig(root) }})
	if rep.Succeeded {
		return nil
	}
	reason := rep.HaltReason
	if reason == "" {
		reason = "auto-rebase failed"
	}
	return fail(reason)
}
