package workflow

// Seams used by the orchestrator-api verbs (area I). Additive only: they
// reuse this package's allocator/claim helpers without changing the host
// commands.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// AllocateLaneWorktreeFor is allocate_lane_worktree called with a raw
// mission handle (orchestrator-api start-implementation): lane naming
// (worktree dir, lane branch, workspace context) keys on handle, while the
// coordination-branch discovery and the status sparse-checkout patterns use
// the canonical mission directory name.
func AllocateLaneWorktreeFor(o *Out, root, handle, canonical, wpID string, m *lanes.Manifest) (string, string, error) {
	if handle == canonical {
		return AllocateLaneWorktree(o, root, canonical, wpID, m)
	}
	lane := m.LaneForWP(wpID)
	if lane == nil {
		return "", "", fmt.Errorf("%s is not assigned to any execution lane in lanes.json", wpID)
	}
	wt := lanes.WorktreePath(root, handle, lane.LaneID)
	branch := lanes.LaneBranchName(handle, lane.LaneID, m.TargetBranch)
	InstallLaneTipRecorder(o, root)
	ExcludeFromGitIndex(root, []string{".spec-kitty/"})
	coordBranch := coordinationBranch(root, canonical)
	finish := func() (string, string, error) {
		if err := mergeRecordedPlanningCommit(root, wt, lane.LaneID, m.PlanningCommitSHA, m.TargetBranch); err != nil {
			return "", "", err
		}
		if err := mergeDependencyLaneTips(o, root, wt, handle, lane, m); err != nil {
			return "", "", err
		}
		lanes.RecordTip(root, branch)
		backfillContextIfMissing(root, handle, wpID, lane, wt, branch, m)
		return wt, branch, nil
	}
	if _, err := os.Stat(wt); err == nil {
		if err := validateWorktreeClean(wt, lane.LaneID); err != nil {
			return "", "", err
		}
		return finish()
	}
	if gitx.BranchExists(root, branch) {
		git(root, "worktree", "prune")
		os.MkdirAll(filepath.Dir(wt), 0o755)
		if r := git(root, "worktree", "add", wt, branch); r.Code != 0 {
			return "", "", fmt.Errorf("Failed to recover worktree at %s: %s", wt, strings.TrimSpace(r.Stderr))
		}
		if err := validateWorktreeClean(wt, lane.LaneID); err != nil {
			return "", "", err
		}
		if coordBranch != "" {
			if err := registerSparseCheckout(wt, canonical); err != nil {
				return "", "", err
			}
		}
		return finish()
	}
	parent := m.MissionBranch
	if coordBranch != "" {
		parent = coordBranch
		if !gitx.BranchExists(root, coordBranch) {
			if r := git(root, "branch", coordBranch, m.TargetBranch); r.Code != 0 {
				return "", "", fmt.Errorf("Failed to create branch %s from %s: %s", coordBranch, m.TargetBranch, strings.TrimSpace(r.Stderr))
			}
		}
	} else if !gitx.BranchExists(root, m.MissionBranch) {
		if r := git(root, "branch", m.MissionBranch, m.TargetBranch); r.Code != 0 {
			return "", "", fmt.Errorf("Failed to create mission branch %s from %s: %s", m.MissionBranch, m.TargetBranch, strings.TrimSpace(r.Stderr))
		}
	}
	if origin := "refs/remotes/origin/" + branch; branchTip(root, origin) != "" {
		parent = origin
	}
	os.MkdirAll(filepath.Dir(wt), 0o755)
	if r := git(root, "worktree", "add", "-b", branch, wt, parent); r.Code != 0 {
		return "", "", fmt.Errorf("Failed to create lane worktree at %s: %s", wt, strings.TrimSpace(r.Stderr))
	}
	if coordBranch != "" {
		if err := registerSparseCheckout(wt, canonical); err != nil {
			return "", "", err
		}
	}
	if err := mergeRecordedPlanningCommit(root, wt, lane.LaneID, m.PlanningCommitSHA, m.TargetBranch); err != nil {
		git(root, "worktree", "remove", "--force", wt)
		return "", "", err
	}
	if err := mergeDependencyLaneTips(o, root, wt, handle, lane, m); err != nil {
		return "", "", err
	}
	lanes.RecordTip(root, branch)
	backfillContextIfMissing(root, handle, wpID, lane, wt, branch, m)
	return wt, branch, nil
}

// RefreshReusedLaneContext is implement_support.refresh_reused_lane_context:
// point an existing <slug>-<lane> workspace context at wpID. No-op when the
// lane has no context.
func RefreshReusedLaneContext(root, slug, laneID, wpID string, deps []string) bool {
	if laneID == "" {
		return false
	}
	c := loadLaneContext(root, slug, laneID)
	if c == nil {
		return false
	}
	c.WPID = wpID
	cur := wpID
	c.CurrentWP = &cur
	if deps == nil {
		deps = []string{}
	}
	c.Dependencies = deps
	lanes.SaveContext(root, c)
	return true
}

// StartReviewRef is start_review_status with an explicit review_ref (nil =
// None, as orchestrator-api start-review passes it through).
func StartReviewRef(s *coord.Surface, wpID string, actor status.Actor, wsCtx, execMode string, pm *jsonx.Object, reviewRef *string) (*StartResult, error) {
	lane, curActor, curRole := currentState(s, wpID)
	switch lane {
	case string(status.ForReview):
		reason := "Started review via action command"
		c := wsCtx
		req := &status.TransitionRequest{WPID: wpID, ToLane: "in_review", Actor: actor, Reason: &reason, ReviewRef: reviewRef,
			WorkspaceContext: &c, ExecutionMode: execMode, RepoRoot: s.Root, PolicyMetadata: pm}
		res, err := s.EmitTransition(req)
		if err != nil {
			return nil, wrapTransition(err)
		}
		return &StartResult{From: lane, To: "in_review", Events: []*status.Event{res.Event}, ClaimedBy: actor.Identity()}, nil
	case string(status.InReview):
		holder := strings.TrimSpace(curActor)
		if holder != "" && strings.TrimSpace(curRole) == "reviewer" && holder != status.ActorKey(actor) {
			return nil, &ClaimConflict{fmt.Sprintf("WP %s is already claimed for review by '%s'", wpID, holder)}
		}
		return &StartResult{From: lane, To: lane, NoOp: true, ClaimedBy: curActor}, nil
	}
	return nil, &StartRejected{fmt.Sprintf("WP %s is in '%s', cannot start review", wpID, lane)}
}
