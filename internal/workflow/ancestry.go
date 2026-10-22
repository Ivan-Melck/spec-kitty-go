package workflow

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// orphanedPinRecoveryHint is worktree_allocator.ORPHANED_PIN_RECOVERY_HINT.
const orphanedPinRecoveryHint = "spec-kitty agent mission finalize-tasks --refresh-planning-commit --allow-orphaned"

// AncestryResult is implement_support.AncestryCheckResult.
type AncestryResult struct {
	OK bool
	// Missing names the refs that are not ancestors of the workspace HEAD.
	Missing []string
	// DeferredTo is the target branch when the #5296 planning waiver applied.
	DeferredTo string
}

// rootOnTarget is _root_checkout_is_target.
func rootOnTarget(root string, m *lanes.Manifest) bool {
	r := git(root, "symbolic-ref", "--short", "-q", "HEAD")
	return r.Code == 0 && strings.TrimSpace(r.Stdout) == m.TargetBranch
}

func planningWaiver(root string, lane *lanes.Lane, m *lanes.Manifest) bool {
	return lane.LaneID == lanes.PlanningLaneID && rootOnTarget(root, m)
}

// dependencyLaneStatus is _dependency_lane_status (lanes read from the
// status surface).
func dependencyLaneStatus(statusDir string) map[string]string {
	st, err := status.ReadStream(statusDir, "")
	if err != nil || len(st.Transitions) == 0 {
		return map[string]string{}
	}
	snap := status.Reduce(st.Transitions, nil, true)
	out := map[string]string{}
	for _, kv := range snap.WorkPackages.Pairs() {
		lane := string(status.Planned)
		if o, ok := kv.Value.(interface{ GetString(string) string }); ok && o.GetString("lane") != "" {
			lane = o.GetString("lane")
		}
		out[kv.Key] = lane
	}
	return out
}

// approvedDependencyLaneRefs is _approved_dependency_lane_refs: the
// (dep lane, branch) pairs of every fully approved dependency lane whose
// branch still resolves.
func approvedDependencyLaneRefs(root, slug, statusDir string, lane *lanes.Lane, m *lanes.Manifest) [][2]string {
	if planningWaiver(root, lane, m) {
		return nil
	}
	lanesByWP := dependencyLaneStatus(statusDir)
	byID := map[string]*lanes.Lane{}
	for i := range m.Lanes {
		byID[m.Lanes[i].LaneID] = &m.Lanes[i]
	}
	var out [][2]string
	for _, id := range lane.DependsOnLanes {
		dep := byID[id]
		if dep == nil || len(dep.WPIDs) == 0 {
			continue
		}
		all := true
		for _, w := range dep.WPIDs {
			if l := lanesByWP[w]; l != string(status.Approved) && l != string(status.Done) {
				all = false
			}
		}
		if !all {
			continue
		}
		branch := lanes.LaneBranchName(slug, id, m.TargetBranch)
		if !gitx.BranchExists(root, branch) {
			continue
		}
		out = append(out, [2]string{id, branch})
	}
	return out
}

// planningCommitMissing is _planning_commit_missing_diagnostic.
func planningCommitMissing(root string, m *lanes.Manifest) string {
	sha := *m.PlanningCommitSHA
	if tip := branchTip(root, m.TargetBranch); tip != "" && !isAncestorIn(root, sha, tip) && git(root, "cat-file", "-e", sha+"^{commit}").Code == 0 {
		return fmt.Sprintf("recorded planning commit %s is orphaned against the target-branch tip; run '%s' to re-point it", sha, orphanedPinRecoveryHint)
	}
	return "recorded planning commit " + sha
}

// CheckClaimAncestry is check_claim_ancestry: the POST-materialize claim
// predicate over the recorded planning commit and every approved dependency
// lane tip.
func CheckClaimAncestry(root, slug, statusDir, wpID, workspace string) AncestryResult {
	m, err := lanes.Read(project.Specs(root, slug))
	if err != nil || m == nil {
		return AncestryResult{OK: true}
	}
	lane := m.LaneForWP(wpID)
	if lane == nil {
		return AncestryResult{OK: true}
	}
	head := branchTip(workspace, "HEAD")
	if head == "" {
		return AncestryResult{OK: true}
	}
	var missing []string
	if m.PlanningCommitSHA != nil && *m.PlanningCommitSHA != "" && !isAncestorIn(workspace, *m.PlanningCommitSHA, head) {
		missing = append(missing, planningCommitMissing(root, m))
	}
	for _, ref := range approvedDependencyLaneRefs(root, slug, statusDir, lane, m) {
		if !isAncestorIn(workspace, ref[1], head) {
			missing = append(missing, fmt.Sprintf("approved dependency lane %s (%s)", ref[0], ref[1]))
		}
	}
	r := AncestryResult{OK: len(missing) == 0, Missing: missing}
	if planningWaiver(root, lane, m) {
		r.DeferredTo = m.TargetBranch
	}
	return r
}

// reenterLaneSelfHeal is reenter_lane_self_heal: re-run the planning-commit
// and dependency-tip merges (approved tips only for the planning lane).
func reenterLaneSelfHeal(root, slug, statusDir, wpID string) {
	m, err := lanes.Read(project.Specs(root, slug))
	if err != nil || m == nil {
		return
	}
	lane := m.LaneForWP(wpID)
	if lane == nil {
		return
	}
	o := NewOut(os.Stdout, os.Stderr)
	ws := lanes.WorktreePath(root, slug, lane.LaneID)
	branch := lanes.LaneBranchName(slug, lane.LaneID, m.TargetBranch)
	l := *lane
	if lane.LaneID == lanes.PlanningLaneID {
		ws, branch = root, ""
		if CheckClaimAncestry(root, slug, statusDir, wpID, ws).OK {
			return
		}
		if coord.IsProtected(root, slug, gitx.CurrentBranch(root)) || validateWorktreeClean(ws, lane.LaneID) != nil {
			return
		}
		var deps []string
		for _, ref := range approvedDependencyLaneRefs(root, slug, statusDir, lane, m) {
			deps = append(deps, ref[0])
		}
		l.DependsOnLanes = deps
	}
	if st, err := os.Stat(ws); err != nil || !st.IsDir() {
		return
	}
	if mergeRecordedPlanningCommit(root, ws, l.LaneID, m.PlanningCommitSHA, m.TargetBranch) != nil {
		return
	}
	if mergeDependencyLaneTips(o, root, ws, slug, &l, m) != nil {
		return
	}
	if branch != "" {
		lanes.RecordTip(root, branch)
	}
}

// ResolveClaimAncestry is resolve_claim_ancestry_gate: the check, then one
// self-heal re-entry and a re-check before refusing.
func ResolveClaimAncestry(root, slug, statusDir, wpID, workspace string) AncestryResult {
	r := CheckClaimAncestry(root, slug, statusDir, wpID, workspace)
	if r.OK {
		return r
	}
	reenterLaneSelfHeal(root, slug, statusDir, wpID)
	return CheckClaimAncestry(root, slug, statusDir, wpID, workspace)
}
