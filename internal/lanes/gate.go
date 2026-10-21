package lanes

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// LaneTipRefPrefix is the hidden lane work-tip ref namespace.
const LaneTipRefPrefix = "refs/spec-kitty/lane-tip/"

// ClaimBaseRefPrefix is the per-WP claim-base ref namespace.
const ClaimBaseRefPrefix = "refs/spec-kitty/wp-base"

// RecordTip writes refs/spec-kitty/lane-tip/<branch> at branch's head
// (no-op when the branch does not resolve). Returns the recorded sha.
func RecordTip(root, branch string) string {
	sha, err := gitx.Run(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil || strings.TrimSpace(sha) == "" {
		return ""
	}
	sha = strings.TrimSpace(sha)
	gitx.Run(root, "update-ref", LaneTipRefPrefix+branch, sha)
	return sha
}

// RecordTipForWP is record_tip_for_wp: best-effort tip record of the WP's
// code lane.
func RecordTipForWP(root, missionSlug, wpID string) {
	m, err := Read(project.Specs(root, missionSlug))
	if err != nil || m == nil {
		return
	}
	l := m.LaneForWP(wpID)
	if l == nil || l.LaneID == PlanningLaneID {
		return
	}
	RecordTip(root, LaneBranchName(m.MissionSlug, l.LaneID, m.TargetBranch))
}

// ReadClaimBase reads refs/spec-kitty/wp-base/<mission>/<wp> ("" when absent).
func ReadClaimBase(root, missionSlug, wpID string) string {
	sha, err := gitx.Run(root, "rev-parse", "--verify", "--quiet", fmt.Sprintf("%s/%s/%s", ClaimBaseRefPrefix, missionSlug, wpID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(sha)
}

// HasCommitBeyondBase is lane_has_commit_beyond_base.
func HasCommitBeyondBase(worktree, base string) bool {
	out, err := gitx.Run(worktree, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	return err == nil && n > 0
}

// GateDecision is the for_review commit gate verdict.
type GateDecision struct {
	Passed     bool
	Reason     string
	LaneID     string
	LaneBranch string
	BaseRef    string
}

// LaneBaseRef is resolve_lane_base_ref: the STATUS_STATE placement ref
// (coordination branch under coord topology, else the target branch).
func LaneBaseRef(root, missionSlug string, m *Manifest) string {
	meta := mission.ReadMetaOrEmpty(project.Specs(root, missionSlug))
	if cb := strings.TrimSpace(meta.GetString("coordination_branch")); cb != "" {
		return cb
	}
	if t := strings.TrimSpace(meta.GetString("target_branch")); t != "" {
		return t
	}
	if m != nil && m.MissionBranch != "" {
		return m.MissionBranch
	}
	return "main"
}

// EvaluateForReviewGate is evaluate_for_review_gate.
func EvaluateForReviewGate(root, missionSlug, wpID string, force bool) GateDecision {
	if force {
		return GateDecision{Passed: true}
	}
	m, err := Read(project.Specs(root, missionSlug))
	if err != nil || m == nil {
		return GateDecision{Passed: true}
	}
	l := m.LaneForWP(wpID)
	if l == nil {
		return GateDecision{Passed: true}
	}
	if l.LaneID == PlanningLaneID {
		base := ReadClaimBase(root, missionSlug, wpID)
		checkout := root
		if ws, err := ResolveWorkspace(root, missionSlug, wpID); err == nil {
			checkout = ws.WorktreePath
		}
		if base != "" && qualifyingCommitSince(checkout, base, missionSlug) {
			return GateDecision{Passed: true, LaneID: l.LaneID, LaneBranch: m.TargetBranch, BaseRef: base}
		}
		shown := base
		if shown == "" {
			shown = "<no recorded claim base>"
		}
		return GateDecision{Reason: fmt.Sprintf("%s cannot move to for_review: no implementation commit since claim in the repository root checkout beyond %s. Commit the work first, or pass --force if there is genuinely nothing to commit.", wpID, shown),
			LaneID: l.LaneID, LaneBranch: m.TargetBranch, BaseRef: base}
	}
	wt := WorktreePath(root, missionSlug, l.LaneID)
	branch := LaneBranchName(missionSlug, l.LaneID, m.TargetBranch)
	base := LaneBaseRef(root, missionSlug, m)
	if isDir(wt) && HasCommitBeyondBase(wt, base) {
		return GateDecision{Passed: true, LaneID: l.LaneID, LaneBranch: branch, BaseRef: base}
	}
	return GateDecision{Reason: fmt.Sprintf("%s cannot move to for_review: no implementation commit on lane %s (%s) beyond %s. Commit the work in the lane worktree first, or pass --force if there is genuinely nothing to commit.", wpID, l.LaneID, branch, base),
		LaneID: l.LaneID, LaneBranch: branch, BaseRef: base}
}

func qualifyingCommitSince(checkout, base, slug string) bool {
	out, err := gitx.Run(checkout, "log", "--no-renames", "--format=", "--name-only", base+"..HEAD")
	if err != nil {
		return false
	}
	excluded := []string{"kitty-specs/" + slug + "/status.", "kitty-specs/" + slug + "/issue-matrix.", "kitty-specs/" + slug + "/meta.json", ".kittify/"}
	for _, p := range strings.Split(out, "\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		hit := false
		for _, e := range excluded {
			if strings.HasPrefix(p, e) {
				hit = true
			}
		}
		if !hit {
			return true
		}
	}
	return false
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
