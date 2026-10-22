package workflow

// `implement --recover` (specify_cli.lanes.recovery + implement_recover):
// scan a mission's lane branches against worktrees, workspace contexts and
// the status log, then recreate worktrees/contexts and emit the catch-up
// transitions (never past in_progress, actor "recovery").

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// RecoveryState is one WP/lane recovery record.
type RecoveryState struct {
	WPID, LaneID, BranchName                    string
	BranchExists, WorktreeExists, ContextExists bool
	StatusLane                                  string
	HasCommits                                  bool
	RecoveryAction, ResolutionNote              string
}

// RecoveryReport summarizes a recovery run.
type RecoveryReport struct {
	RecoveredWPs                                              []string
	WorktreesRecreated, ContextsRecreated, TransitionsEmitted int
	Errors                                                    []string
}

const recoveryActor = "recovery"

// recoveryTransitions is _get_recovery_transitions: planned -> claimed ->
// in_progress from the current lane.
func recoveryTransitions(cur string) []string {
	prog := []string{"planned", "claimed", "in_progress"}
	for i, l := range prog {
		if l == cur {
			return append([]string{}, prog[i+1:]...)
		}
	}
	return nil
}

func listMissionBranches(root, slug string) []string {
	r := git(root, "branch", "--list", "kitty/mission-"+slug+"*", "--format=%(refname:short)")
	if r.Code != 0 {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(r.Stdout), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func worktreeForBranch(root, branch string) string {
	r := git(root, "worktree", "list", "--porcelain")
	if r.Code != 0 {
		return ""
	}
	cur := ""
	for _, l := range strings.Split(r.Stdout, "\n") {
		if strings.HasPrefix(l, "worktree ") {
			cur = strings.TrimPrefix(l, "worktree ")
		} else if strings.HasPrefix(l, "branch refs/heads/") && strings.TrimPrefix(l, "branch refs/heads/") == branch && cur != "" {
			return cur
		}
	}
	return ""
}

func recoveryMissionBranch(primary, slug string) string {
	if m, err := lanes.Read(primary); err == nil && m != nil && m.MissionBranch != "" {
		return m.MissionBranch
	}
	return lanes.MissionBranchName(slug, mission.ReadMetaOrEmpty(primary).GetString("mission_id"))
}

func laneWPIDs(primary, laneID string) []string {
	m, err := lanes.Read(primary)
	if err != nil || m == nil {
		return nil
	}
	for _, l := range m.Lanes {
		if l.LaneID == laneID {
			return append([]string{}, l.WPIDs...)
		}
	}
	return nil
}

var reTaskWP = regexp.MustCompile(`(?i)^(WP\d{2,})`)

// ScanRecoveryState is scan_recovery_state (status events consulted).
func ScanRecoveryState(root, slug string) []*RecoveryState {
	s := coord.Resolve(root, slug)
	primary := s.PrimaryDir
	snap := statusSnapshot(s)
	laneOf := func(id string) string {
		if snap.WP(id) == nil {
			return "planned"
		}
		return string(snap.Lane(id))
	}
	missionBranch := recoveryMissionBranch(primary, slug)
	ctxs := map[string]*lanes.WorkspaceContext{}
	for _, c := range lanes.ListContexts(root) {
		if c.MissionSlug == slug {
			ctxs[c.LaneID] = c
		}
	}
	var states []*RecoveryState
	for _, b := range listMissionBranches(root, slug) {
		p := lanes.ParseBranch(b)
		if p == nil || p.LaneID == "" {
			continue
		}
		wt := worktreeForBranch(root, b)
		_, statErr := os.Stat(lanes.WorktreePath(root, slug, p.LaneID))
		wtExists := wt != "" || statErr == nil
		c := ctxs[p.LaneID]
		hasCommits := strings.TrimSpace(git(root, "log", "--oneline", "-1", "--end-of-options", missionBranch+".."+b).Stdout) != ""
		ids := laneWPIDs(primary, p.LaneID)
		if len(ids) == 0 {
			if c != nil && len(c.LaneWPIDs) > 0 {
				ids = c.LaneWPIDs
			} else {
				ids = []string{"unknown"}
			}
		}
		for _, id := range ids {
			sl := laneOf(id)
			action := "no_action"
			switch {
			case !wtExists:
				action = "recreate_worktree"
			case c == nil:
				action = "recreate_context"
			case hasCommits && len(recoveryTransitions(sl)) > 0:
				action = "emit_transitions"
			}
			states = append(states, &RecoveryState{WPID: id, LaneID: p.LaneID, BranchName: b, BranchExists: true,
				WorktreeExists: wtExists, ContextExists: c != nil, StatusLane: sl, HasCommits: hasCommits, RecoveryAction: action})
		}
	}
	represented := map[string]bool{}
	for _, st := range states {
		represented[st.WPID] = true
	}
	var all []string
	if ents, err := os.ReadDir(filepath.Join(primary, "tasks")); err == nil {
		var names []string
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), "WP") && strings.HasSuffix(e.Name(), ".md") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			if m := reTaskWP.FindStringSubmatch(n); m != nil {
				all = append(all, strings.ToUpper(m[1]))
			}
		}
	}
	if m, err := lanes.Read(primary); err == nil && m != nil {
		for _, l := range m.Lanes {
			for _, id := range l.WPIDs {
				if !contains(all, id) {
					all = append(all, id)
				}
			}
		}
	}
	for _, id := range all {
		if !represented[id] && snap.WP(id) != nil && laneOf(id) == "done" {
			states = append(states, &RecoveryState{WPID: id, StatusLane: "done", RecoveryAction: "no_action", ResolutionNote: "merged_and_deleted"})
		}
	}
	represented = map[string]bool{}
	done := map[string]bool{}
	for _, st := range states {
		represented[st.WPID] = true
		if st.StatusLane == "done" || st.ResolutionNote == "merged_and_deleted" {
			done[st.WPID] = true
		}
	}
	for _, id := range snap.WorkPackages.Keys() {
		if laneOf(id) == "done" {
			done[id] = true
		}
	}
	if len(done) > 0 {
		for _, id := range all {
			if done[id] {
				continue
			}
			if represented[id] {
				var ex *RecoveryState
				for _, st := range states {
					if st.WPID == id {
						ex = st
						break
					}
				}
				if ex != nil && ex.ResolutionNote != "merged_and_deleted" && ex.ResolutionNote != "ready_to_start_from_target" && ex.ResolutionNote != "" {
					continue
				}
			}
			deps := wpDeps(primary, id)
			ok := len(deps) > 0
			for _, d := range deps {
				ok = ok && done[d]
			}
			if !ok {
				continue
			}
			dup := false
			for _, st := range states {
				dup = dup || (st.WPID == id && st.ResolutionNote == "ready_to_start_from_target")
			}
			if !dup {
				states = append(states, &RecoveryState{WPID: id, StatusLane: laneOf(id), RecoveryAction: "no_action", ResolutionNote: "ready_to_start_from_target"})
			}
		}
	}
	return states
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func wpDeps(primary, id string) []string {
	f, err := wp.Find(primary, id)
	if err != nil || f == "" {
		return nil
	}
	w, err := wp.Read(f)
	if err != nil {
		return nil
	}
	return w.Dependencies()
}

func recoverContext(root, slug string, st *RecoveryState) error {
	primary := project.Specs(root, slug)
	wt := lanes.WorktreePath(root, slug, st.LaneID)
	ids := laneWPIDs(primary, st.LaneID)
	if len(ids) == 0 {
		ids = []string{st.WPID}
	}
	mb := recoveryMissionBranch(primary, slug)
	var bc *string
	if r := git(root, "rev-parse", "--verify", "--end-of-options", mb); r.Code == 0 {
		v := strings.TrimSpace(r.Stdout)
		bc = &v
	}
	rel, _ := filepath.Rel(root, wt)
	cur := st.WPID
	_, err := lanes.SaveContext(root, &lanes.WorkspaceContext{WPID: st.WPID, MissionSlug: slug, WorktreePath: rel,
		BranchName: st.BranchName, BaseBranch: mb, BaseCommit: bc, Dependencies: []string{}, CreatedAt: clock.NowUTCISO(),
		CreatedBy: "recovery", VCSBackend: "git", LaneID: st.LaneID, LaneWPIDs: ids, CurrentWP: &cur})
	return err
}

func reconcileRecoveryStatus(root, slug string, st *RecoveryState) int {
	target := ""
	switch {
	case st.HasCommits:
		target = "in_progress"
	case st.ContextExists:
		target = "claimed"
	default:
		return 0
	}
	s := coord.Resolve(root, slug)
	n := 0
	for _, next := range recoveryTransitions(st.StatusLane) {
		reason := fmt.Sprintf("Recovered after crash -- branch %s exists", st.BranchName)
		if st.HasCommits {
			reason += " with commits"
		}
		if _, err := s.EmitTransition(&status.TransitionRequest{WPID: st.WPID, ToLane: next, Actor: status.StrActor(recoveryActor),
			Reason: &reason, ExecutionMode: "worktree", RepoRoot: root}); err != nil {
			break
		}
		n++
		if next == target {
			break
		}
	}
	return n
}

// RunRecovery is run_recovery.
func RunRecovery(root, slug string) *RecoveryReport {
	rep := &RecoveryReport{RecoveredWPs: []string{}, Errors: []string{}}
	states := ScanRecoveryState(root, slug)
	doneWT, doneCtx := map[string]bool{}, map[string]bool{}
	for _, st := range states {
		if st.RecoveryAction == "no_action" {
			continue
		}
		err := func() error {
			if st.RecoveryAction == "recreate_worktree" && !doneWT[st.LaneID] {
				wt := lanes.WorktreePath(root, slug, st.LaneID)
				git(root, "worktree", "prune")
				os.MkdirAll(filepath.Dir(wt), 0o755)
				if r := git(root, "worktree", "add", wt, st.BranchName); r.Code != 0 {
					return fmt.Errorf("Failed to recover worktree at %s: %s", wt, strings.TrimSpace(r.Stderr))
				}
				rep.WorktreesRecreated++
				doneWT[st.LaneID] = true
				if !st.ContextExists && !doneCtx[st.LaneID] {
					if err := recoverContext(root, slug, st); err != nil {
						return err
					}
					rep.ContextsRecreated++
					doneCtx[st.LaneID] = true
				}
			} else if st.RecoveryAction == "recreate_context" && !doneCtx[st.LaneID] {
				if err := recoverContext(root, slug, st); err != nil {
					return err
				}
				rep.ContextsRecreated++
				doneCtx[st.LaneID] = true
			}
			if st.HasCommits && len(recoveryTransitions(st.StatusLane)) > 0 {
				rep.TransitionsEmitted += reconcileRecoveryStatus(root, slug, st)
			}
			if !contains(rep.RecoveredWPs, st.WPID) {
				rep.RecoveredWPs = append(rep.RecoveredWPs, st.WPID)
			}
			return nil
		}()
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s (%s): %v", st.WPID, st.LaneID, err))
		}
	}
	return rep
}
