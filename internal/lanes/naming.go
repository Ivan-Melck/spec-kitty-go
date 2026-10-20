// Package lanes implements lane/branch/worktree naming
// (lanes/branch_naming.py) and the lanes.json manifest
// (lanes/models.py, persistence.py, compute.py).
package lanes

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// MissionPrefix is the branch namespace for mission branches.
const MissionPrefix = "kitty/mission-"

// PlanningLaneID is the reserved planning lane.
const PlanningLaneID = "lane-planning"

// CoordSuffix is the coordination worktree directory suffix.
const CoordSuffix = "-coord"

var (
	newLaneRe    = regexp.MustCompile(`^kitty/mission-(.+)-([0-9A-HJKMNP-TV-Z]{8})-(lane-[a-z]+)$`)
	newMissionRe = regexp.MustCompile(`^kitty/mission-(.+)-([0-9A-HJKMNP-TV-Z]{8})$`)
	legacyLaneRe = regexp.MustCompile(`^kitty/mission-(\d{3}-.+)-(lane-[a-z]+)$`)
	legacyRe     = regexp.MustCompile(`^kitty/mission-(\d{3}-.+)$`)
	plainLaneRe  = regexp.MustCompile(`^kitty/mission-(.+)-(lane-[a-z]+)$`)
	// LaneIDPattern validates lane ids.
	LaneIDPattern = regexp.MustCompile(`^lane-[a-z]+$`)
)

func humanSlugForMid8(slug, missionID string) string {
	h := mission.StripNumericPrefix(slug)
	suffix := "-" + missionID[:8]
	return strings.TrimSuffix(h, suffix)
}

func idempotentLegacyBody(slug string) string {
	if mission.Mid8FromSlug(slug) != "" {
		return mission.StripNumericPrefix(slug)
	}
	return slug
}

// MissionBranchName is mission_branch_name(slug, mission_id).
func MissionBranchName(slug, missionID string) string {
	if len(missionID) >= 8 {
		return MissionPrefix + humanSlugForMid8(slug, missionID) + "-" + missionID[:8]
	}
	return MissionPrefix + idempotentLegacyBody(slug)
}

// CoordBranchName is the canonical coordination branch (== mission branch).
func CoordBranchName(slug, missionID string) string { return MissionBranchName(slug, missionID) }

// CoordReconstructBranch rebuilds an existing coordination branch verbatim.
func CoordReconstructBranch(slug, mid8 string) string {
	return MissionPrefix + mission.CoordMissionDirName(slug, mid8)
}

// LaneBranchName is lane_branch_name; the planning lane resolves to the
// target branch itself.
func LaneBranchName(missionSlug, laneID, targetBranch string) string {
	if laneID == PlanningLaneID {
		return targetBranch
	}
	return MissionPrefix + idempotentLegacyBody(missionSlug) + "-" + laneID
}

// WorktreeDirName is "<mission_slug>-<lane_id>".
func WorktreeDirName(missionSlug, laneID string) string { return missionSlug + "-" + laneID }

// WorktreePath is <root>/.worktrees/<mission_slug>-<lane_id>.
func WorktreePath(root, missionSlug, laneID string) string {
	return filepath.Join(root, project.WorktreesDir, WorktreeDirName(missionSlug, laneID))
}

// CoordDirName is "<slug>-<mid8>-coord" (verbatim, mid8 dedup'd).
func CoordDirName(slug, mid8 string) string {
	return mission.CoordMissionDirName(slug, mid8) + CoordSuffix
}

// CoordWorktreePath is <root>/.worktrees/<slug>-<mid8>-coord.
func CoordWorktreePath(root, slug, mid8 string) string {
	return filepath.Join(root, project.WorktreesDir, CoordDirName(slug, mid8))
}

// CoordMissionDir is the mission dir inside the coordination worktree.
func CoordMissionDir(root, slug, mid8 string) string {
	return filepath.Join(CoordWorktreePath(root, slug, mid8), project.SpecsDir, mission.CoordMissionDirName(slug, mid8))
}

// BranchParse is the result of ParseBranch.
type BranchParse struct {
	MissionSlug string
	Mid8        string
	LaneID      string
}

// ParseBranch parses a kitty/mission-* branch name (nil when not one).
func ParseBranch(branch string) *BranchParse {
	if m := newLaneRe.FindStringSubmatch(branch); m != nil {
		return &BranchParse{MissionSlug: m[1] + "-" + m[2], Mid8: m[2], LaneID: m[3]}
	}
	if m := newMissionRe.FindStringSubmatch(branch); m != nil {
		return &BranchParse{MissionSlug: m[1] + "-" + m[2], Mid8: m[2]}
	}
	if m := legacyLaneRe.FindStringSubmatch(branch); m != nil {
		return &BranchParse{MissionSlug: m[1], LaneID: m[2]}
	}
	if m := legacyRe.FindStringSubmatch(branch); m != nil {
		return &BranchParse{MissionSlug: m[1]}
	}
	if m := plainLaneRe.FindStringSubmatch(branch); m != nil {
		return &BranchParse{MissionSlug: m[1], LaneID: m[2]}
	}
	return nil
}

// IsLaneBranch reports a kitty/mission-*-lane-x branch.
func IsLaneBranch(branch string) bool {
	p := ParseBranch(branch)
	return p != nil && p.LaneID != ""
}
