package consolidate

// The merge transaction record (.kittify/runtime/merge/<mission_id>/
// state.json), its pre-mutation branch snapshot and the single rollback
// door (consolidation.state / consolidation.rollback).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
)

const (
	stateFileName   = "state.json"
	postFixMarker   = "reconciliation.post-fix"
	postFixContents = "terminus-reconciliation-gate\n"
)

// MergeState is ConsolidationState (the fields this build tracks; the
// document keeps upstream's full key set and order).
type MergeState struct {
	MissionID, MissionSlug, TargetBranch string
	WPOrder, CompletedWPs                []string
	Strategy                             string
	StartedAt, UpdatedAt                 string
	MissionNumberBaked, PushRequested    bool
	DeleteBranch, RemoveWorktree         bool
	SkipLanes                            bool
	PreTarget, PreCoord, PreCoordRef     string
	LaneTips                             [][2]string // branch -> tip (snapshot order)
	PreRefs                              [][2]string // branch -> sha (snapshot order)
	LaneBranches                         []string
}

func stateDir(root, missionID string) string {
	return filepath.Join(root, ".kittify", "runtime", "merge", missionID)
}

func strList(xs []string) []any {
	out := []any{}
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

func pairsObj(ps [][2]string) *jsonx.Object {
	o := &jsonx.Object{}
	for _, p := range ps {
		o.Set(p[0], p[1])
	}
	return o
}

func optStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Object renders the state document in upstream field order.
func (s *MergeState) Object() *jsonx.Object {
	return jsonx.NewObject("mission_id", s.MissionID, "mission_slug", s.MissionSlug, "target_branch", s.TargetBranch,
		"wp_order", strList(s.WPOrder), "completed_wps", strList(s.CompletedWPs), "current_wp", nil,
		"has_pending_conflicts", false, "strategy", s.Strategy, "workspace_path", nil, "started_at", s.StartedAt,
		"updated_at", s.UpdatedAt, "mission_number_baked", s.MissionNumberBaked, "push_requested", s.PushRequested,
		"delete_branch", s.DeleteBranch, "remove_worktree", s.RemoveWorktree, "pending_coord_reconcile", nil,
		"skip_lanes", s.SkipLanes, "pre_mutation_target_sha", optStr(s.PreTarget), "pre_mutation_coord_sha", optStr(s.PreCoord),
		"pre_mutation_coord_ref", optStr(s.PreCoordRef), "pre_interrupt_lane_tips", pairsObj(s.LaneTips),
		"reconciliation_passed_target_sha", nil, "pre_mutation_refs", pairsObj(s.PreRefs), "post_mutation_refs", &jsonx.Object{},
		"restore_targets", pairsObj(s.PreRefs), "snapshot_lane_branches", strList(s.LaneBranches), "resume_seeded_refs", []any{},
		"unsettled_refs", []any{}, "advance_intents", &jsonx.Object{}, "released_refs", &jsonx.Object{}, "release_reasons", &jsonx.Object{})
}

// Save writes state.json (json.dumps indent=2, no trailing newline) and,
// on first creation, the reconciliation marker.
func (s *MergeState) Save(root string) error {
	dir := stateDir(root, s.MissionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(jsonx.Dumps(s.Object(), jsonx.Pretty)), 0o644); err != nil {
		return err
	}
	marker := filepath.Join(dir, postFixMarker)
	if _, err := os.Stat(marker); err != nil {
		os.WriteFile(marker, []byte(postFixContents), 0o644)
	}
	return nil
}

func strsOf(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func pairsOf(o *jsonx.Object) [][2]string {
	if o == nil {
		return nil
	}
	var out [][2]string
	for _, kv := range o.Pairs() {
		if s, ok := kv.Value.(string); ok {
			out = append(out, [2]string{kv.Key, s})
		}
	}
	return out
}

// LoadState reads the mission's state.json (nil when absent/unreadable).
func LoadState(root, missionID string) *MergeState {
	if missionID == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(stateDir(root, missionID), stateFileName))
	if err != nil {
		return nil
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil || o == nil {
		return nil
	}
	wp, _ := o.Get("wp_order")
	cw, _ := o.Get("completed_wps")
	lb, _ := o.Get("snapshot_lane_branches")
	return &MergeState{MissionID: o.GetString("mission_id"), MissionSlug: o.GetString("mission_slug"),
		TargetBranch: o.GetString("target_branch"), WPOrder: strsOf(wp), CompletedWPs: strsOf(cw),
		Strategy: o.GetString("strategy"), StartedAt: o.GetString("started_at"), UpdatedAt: o.GetString("updated_at"),
		MissionNumberBaked: o.GetBool("mission_number_baked"), PushRequested: o.GetBool("push_requested"),
		DeleteBranch: o.GetBool("delete_branch"), RemoveWorktree: o.GetBool("remove_worktree"), SkipLanes: o.GetBool("skip_lanes"),
		PreTarget: o.GetString("pre_mutation_target_sha"), PreCoord: o.GetString("pre_mutation_coord_sha"),
		PreCoordRef: o.GetString("pre_mutation_coord_ref"), LaneTips: pairsOf(o.GetObject("pre_interrupt_lane_tips")),
		PreRefs: pairsOf(o.GetObject("pre_mutation_refs")), LaneBranches: strsOf(lb)}
}

// ClearState removes the transaction record.
func ClearState(root, missionID string) bool {
	if missionID == "" {
		return false
	}
	dir := stateDir(root, missionID)
	_, err := os.Stat(filepath.Join(dir, stateFileName))
	os.Remove(filepath.Join(dir, stateFileName))
	os.Remove(filepath.Join(dir, postFixMarker))
	os.Remove(filepath.Join(dir, "lock"))
	os.Remove(dir)
	return err == nil
}

// newState captures the pre-mutation snapshot of a fresh run.
func newState(root, slug, missionID, target, strategy string, m *lanes.Manifest, wps []string, ret Retention, push bool, coordRef string) *MergeState {
	now := clock.NowUTCISO()
	s := &MergeState{MissionID: missionID, MissionSlug: slug, TargetBranch: target, WPOrder: wps, Strategy: strategy,
		StartedAt: now, UpdatedAt: now, PushRequested: push, DeleteBranch: ret.DeleteBranch, RemoveWorktree: ret.RemoveWorktree}
	s.PreTarget = revParse(root, "refs/heads/"+target)
	s.PreCoordRef = coordRef
	s.PreCoord = revParse(root, "refs/heads/"+coordRef)
	add := func(b string) {
		if sha := revParse(root, "refs/heads/"+b); sha != "" {
			s.PreRefs = append(s.PreRefs, [2]string{b, sha})
		}
	}
	add(target)
	if m.MissionBranch != target {
		add(m.MissionBranch)
	}
	if coordRef != target && coordRef != m.MissionBranch {
		add(coordRef)
	}
	for _, l := range m.Lanes {
		if l.LaneID == lanes.PlanningLaneID {
			continue
		}
		b := lanes.LaneBranchName(slug, l.LaneID, m.TargetBranch)
		if sha := revParse(root, "refs/heads/"+b); sha != "" {
			s.LaneTips = append(s.LaneTips, [2]string{b, sha})
			s.PreRefs = append(s.PreRefs, [2]string{b, sha})
			s.LaneBranches = append(s.LaneBranches, b)
		}
	}
	return s
}

func (s *MergeState) isLane(b string) bool {
	for _, x := range s.LaneBranches {
		if x == b {
			return true
		}
	}
	return false
}

// worktreeOf returns the checkout of branch ("" when not checked out).
func worktreeOf(root, branch string) string {
	out, err := gitx.Run(root, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	cur := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "worktree ") {
			cur = strings.TrimPrefix(l, "worktree ")
		} else if l == "branch refs/heads/"+branch {
			return cur
		}
	}
	return ""
}

// Rollback is rollback_to_snapshot: restores every run-movable branch to
// its snapshot (lane branches are report-only) and renders the report.
func (s *MergeState) Rollback(root string) string {
	width := 0
	for _, p := range s.PreRefs {
		if len(p[0]) > width {
			width = len(p[0])
		}
	}
	lines := []string{"Rollback to the pre-consolidation snapshot:"}
	for _, p := range s.PreRefs {
		b, snap := p[0], p[1]
		name := b + strings.Repeat(" ", width-len(b))
		live := revParse(root, "refs/heads/"+b)
		switch {
		case live == "":
			lines = append(lines, fmt.Sprintf("  missing    %s  (branch no longer exists)", name))
		case live == snap:
			lines = append(lines, fmt.Sprintf("  unchanged  %s  (already at %s)", name, short7(live)))
		case s.isLane(b):
			lines = append(lines, fmt.Sprintf("  kept       %s  (lane branches are reported, not moved; at %s)", name, short7(live)))
		default:
			if wt := worktreeOf(root, b); wt != "" {
				gitx.Run(wt, "reset", "-q", "--hard", snap)
			} else {
				gitx.Run(root, "update-ref", "refs/heads/"+b, snap, live)
			}
			lines = append(lines, fmt.Sprintf("  restored   %s  %s -> %s", name, short7(live), short7(snap)))
		}
	}
	return strings.Join(lines, "\n")
}

func short7(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// Abort is `consolidate --abort`: roll back to the snapshot and clear the
// record.
func Abort(w io.Writer, root, handle string) error {
	slug, err := ResolveMissionSlug(root, handle)
	if err != nil {
		sayErr(w, err.Error())
		return exit(2)
	}
	mid := missionIDOf(root, slug)
	st := LoadState(root, mid)
	if st == nil {
		say(w, fmt.Sprintf("No active merge state found for %s. Coordination workspace left unchanged.", slug))
		return nil
	}
	fmt.Fprintln(w, st.Rollback(root))
	say(w, fmt.Sprintf("Aborted consolidation for %s. Branches restored to their pre-consolidation commits; state and workspace cleaned up.", slug))
	ClearState(root, mid)
	say(w, "Removed merge lock.")
	return nil
}
