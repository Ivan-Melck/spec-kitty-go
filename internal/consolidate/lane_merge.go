package consolidate

// Lane-based merge used by `orchestrator-api consolidate-mission`
// (orchestrator_api.consolidation._execute_lane_merge). Additive (area I):
// it reuses this package's merge helpers but follows the orchestrator path:
// lane -> mission merges keyed on the raw mission handle, the mission ->
// target integration with the requested strategy, per-WP done transitions,
// and a cleanup that never tears down the coordination triple.

import (
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/originfresh"
	"io"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// LaneMergeOptions configures RunLaneMerge.
type LaneMergeOptions struct {
	Handle   string // raw --mission handle (lane naming, event stamping)
	Slug     string // canonical mission directory name
	Target   string // resolved target branch
	Strategy string // merge | squash | rebase
	Push     bool
	Log      io.Writer // warnings (stderr)
	Console  io.Writer // console warnings (stdout in upstream)
}

// LaneMergeError is a RuntimeError refusal of the lane merge.
type LaneMergeError struct {
	Msg          string
	TeardownCode string // COORD_MOVED_AFTER_LANDING, when the landing succeeded
}

func (e *LaneMergeError) Error() string { return e.Msg }

// ApprovedBoundError is ApprovedBoundRefused: a lane holding content
// committed after review approved it (#5668), with its refusal codes.
type ApprovedBoundError struct {
	Msg   string
	Codes []string
}

func (e *ApprovedBoundError) Error() string { return e.Msg }

// refusePostApprovalContent is _refuse_post_approval_lane_content.
func (lm *laneMerge) refusePostApprovalContent(canceled map[string]bool) error {
	tip, err := resolveCommit(lm.root, lm.m.TargetBranch)
	if err != nil {
		return &LaneMergeError{Msg: err.Error()}
	}
	refusal, err := ApprovedBoundRefusal(lm.root, lm.sf.StatusDir(), lm.m, tip, tip, canceled)
	if err != nil {
		return &LaneMergeError{Msg: fmt.Sprintf("The approved lanes could not be checked against what review approved (%s); no lane was merged.", err.Error())}
	}
	if refusal == "" {
		return nil
	}
	codes := BoundRefusalCodes(refusal)
	if strings.Contains(refusal, AttestApprovedFlag) {
		refusal += "\nconsolidate-mission has no attestation flag: attestation is done with `spec-kitty consolidate` (CLI only), not with this command."
	}
	return &ApprovedBoundError{Msg: refusal, Codes: codes}
}

// DestructiveRefusal is DestructiveOpRefused.
type DestructiveRefusal struct {
	Code        string
	Worktree    string
	Entries     []string
	Remediation string
}

func (e *DestructiveRefusal) Error() string {
	lines := []string{fmt.Sprintf("Refusing destructive operation (%s).", e.Code), "  Worktree: " + e.Worktree}
	if len(e.Entries) > 0 {
		var shown []string
		for i, x := range e.Entries {
			if i == 20 {
				shown = append(shown, fmt.Sprintf("    ... and %d more", len(e.Entries)-20))
				break
			}
			shown = append(shown, "    "+x)
		}
		lines = append(lines, "  Dirty entries:\n"+strings.Join(shown, "\n"))
	}
	lines = append(lines, "  Remediation: "+e.Remediation)
	return strings.Join(lines, "\n")
}

// MergePreflight is _build_merge_preflight: the resolved target branch and
// the machine-readable preflight errors.
func MergePreflight(root, slug, explicitTarget string) (string, []string) {
	primary := filepath.Join(root, "kitty-specs", slug)
	target := strings.TrimSpace(explicitTarget)
	if target == "" {
		meta := mission.ReadMetaOrEmpty(primary)
		target = strings.TrimSpace(meta.GetString("merge_target_branch"))
		if target == "" {
			target = strings.TrimSpace(meta.GetString("target_branch"))
		}
	}
	if target == "" {
		target = primaryBranch(root)
	}
	var errs []string
	if dirExists(filepath.Join(root, ".git")) || gitx.IsRepo(root) {
		if _, err := gitx.Run(root, "worktree", "list", "--porcelain"); err != nil {
			errs = append(errs, "git worktree list failed: "+err.Error())
		}
		if !gitx.OK(root, "rev-parse", "--verify", "refs/heads/"+target) && !gitx.OK(root, "rev-parse", "--verify", "refs/remotes/origin/"+target) {
			errs = append(errs, fmt.Sprintf("Target branch '%s' does not exist locally or on origin.", target))
		}
	}
	if _, msg := requireLanes(primary); msg != "" {
		errs = append(errs, msg)
	}
	return target, errs
}

type laneMerge struct {
	root string
	o    LaneMergeOptions
	sf   *coord.Surface
	m    *lanes.Manifest
}

// RunLaneMerge executes the orchestrator lane merge. Failures are
// *LaneMergeError or *DestructiveRefusal.
func RunLaneMerge(root string, o LaneMergeOptions) error {
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.Console == nil {
		o.Console = io.Discard
	}
	sf := coord.Resolve(root, o.Slug)
	m, msg := requireLanes(sf.PrimaryDir)
	if m == nil {
		return &LaneMergeError{Msg: msg}
	}
	m.TargetBranch = o.Target
	lm := &laneMerge{root: root, o: o, sf: sf, m: m}
	if pr := ProtectedStatusTargetRefusal(root, o.Slug, m, lm.canceledWPs()); pr != nil {
		return &LaneMergeError{Msg: fmt.Sprintf("%s: %s Next step: %s", pr.Code, pr.Message, pr.NextStep)}
	}
	if planningOnly(m) {
		var buf strings.Builder
		ret := ResolveRetention(sf.PrimaryDir, nil, nil)
		r := &run{w: &buf, root: root, slug: o.Slug, sf: sf, target: o.Target, strategy: o.Strategy, ret: ret, originGated: true}
		if err := r.execute(); err != nil {
			if e, ok := err.(*Exit); ok {
				return &LaneMergeError{Msg: fmt.Sprintf("Planning-artifact closeout failed with exit code %d", e.Code)}
			}
			return &LaneMergeError{Msg: err.Error()}
		}
		return nil
	}
	ret := ResolveRetention(sf.PrimaryDir, nil, nil)
	isCoord := strings.TrimSpace(sf.Meta.GetString("coordination_branch")) != "" || sf.Meta.Has("coordination_branch")
	missionDeletable := ret.DeleteBranch
	if isCoord {
		missionDeletable = ret.DeleteBranch && ret.RemoveWorktree
	}
	canceled := lm.canceledWPs()
	var gateWPs []string
	for _, l := range m.Lanes {
		for _, id := range l.WPIDs {
			gateWPs = append(gateWPs, id)
		}
	}
	var blocking []string
	for _, g := range EvaluateGates(root, sf.PrimaryDir, sf.StatusDir(), sf.StatusDir(), gateWPs, LoadGatePolicy(root)) {
		if g.Blocking {
			blocking = append(blocking, g.Details)
		}
	}
	if len(blocking) > 0 {
		return &LaneMergeError{Msg: strings.Join(blocking, "; ")}
	}
	if err := lm.refusePostApprovalContent(canceled); err != nil {
		return err
	}
	if err := lm.preflight(ret); err != nil {
		return err
	}
	restore := activateDrivers(root)
	defer restore()
	for _, l := range m.Lanes {
		if err := lm.consolidateLane(l); err != nil {
			return err
		}
	}
	if err := lm.integrate(); err != nil {
		return err
	}
	for _, l := range m.Lanes {
		for _, id := range l.WPIDs {
			lm.markDone(id)
		}
	}
	if o.Push && remoteExists(root) {
		gitx.Run(root, "push", "origin", m.TargetBranch)
	}
	return lm.cleanup(ret, missionDeletable)
}

func planningOnly(m *lanes.Manifest) bool {
	if len(m.Lanes) == 0 {
		return false
	}
	for _, l := range m.Lanes {
		if l.LaneID != lanes.PlanningLaneID {
			return false
		}
	}
	return true
}

func remoteExists(root string) bool {
	out, err := gitx.Run(root, "remote")
	return err == nil && strings.TrimSpace(out) != ""
}

// canceledWPs is acceptably_canceled_wp_ids: canceled with operator provenance.
func (lm *laneMerge) canceledWPs() map[string]bool {
	out := map[string]bool{}
	snap, err := status.ReduceDir(lm.sf.StatusDir())
	if err != nil {
		return out
	}
	for _, id := range snap.WorkPackages.Keys() {
		st := snap.WP(id)
		if st.GetString("lane") == string(status.Canceled) && st.GetString("reason_source") == "operator" {
			out[id] = true
		}
	}
	return out
}

func laneFullyCanceled(l lanes.Lane, canceled map[string]bool) bool {
	if len(l.WPIDs) == 0 {
		return false
	}
	for _, id := range l.WPIDs {
		if !canceled[id] {
			return false
		}
	}
	return true
}

// preflight is pre_mutation_safety_preflight(teardown_coordination=False).
func (lm *laneMerge) preflight(ret Retention) error {
	if !ret.RemoveWorktree {
		return nil
	}
	for _, l := range lm.m.Lanes {
		if l.LaneID == lanes.PlanningLaneID {
			continue
		}
		wt := lanes.WorktreePath(lm.root, lm.o.Handle, l.LaneID)
		if !dirExists(wt) {
			continue
		}
		var entries []string
		for _, e := range porcelainAll(wt) {
			if laneDisposable(e.path) || e.xy == "!!" {
				continue
			}
			if e.xy == "??" {
				entries = append(entries, "?? "+e.path+" (untracked local file would be discarded by worktree removal)")
				continue
			}
			entries = append(entries, e.xy+" "+e.path)
		}
		if len(entries) > 0 {
			return &DestructiveRefusal{Code: "MERGE_UNSAFE_WORKTREE_DIRTY", Worktree: wt, Entries: entries,
				Remediation: fmt.Sprintf("Commit, stash, or revert the local changes in %s, %s.", wt, resumeNote)}
		}
	}
	return nil
}

// consolidateLane is consolidate_lane_into_mission.
func (lm *laneMerge) consolidateLane(l lanes.Lane) error {
	if l.LaneID == lanes.PlanningLaneID {
		return nil
	}
	branch := lanes.LaneBranchName(lm.o.Handle, l.LaneID, lm.m.TargetBranch)
	mb := lm.m.MissionBranch
	if !gitx.BranchExists(lm.root, branch) {
		return &LaneMergeError{Msg: fmt.Sprintf("Lane branch %s does not exist", branch)}
	}
	if ov := staleOverlap(lm.root, branch, mb); len(ov) > 0 {
		wt := lanes.WorktreePath(lm.root, lm.o.Handle, l.LaneID)
		if dirExists(wt) && autoRebase(lm.root, l.LaneID, wt, branch, mb) {
			ov = staleOverlap(lm.root, branch, mb)
		}
		if len(ov) > 0 {
			return &LaneMergeError{Msg: fmt.Sprintf("Lane %s is stale: overlapping files %s. %s", l.LaneID, pyjson.ReprStrings(ov), staleRemediation(l, branch, mb))}
		}
	}
	if err := (&run{root: lm.root}).mergeInto(branch, mb); err != nil {
		return &LaneMergeError{Msg: err.Error()}
	}
	return nil
}

// integrate is integrate_mission_into_target with the requested strategy.
func (lm *laneMerge) integrate() error {
	mb, target := lm.m.MissionBranch, lm.m.TargetBranch
	if !gitx.BranchExists(lm.root, mb) {
		return &LaneMergeError{Msg: fmt.Sprintf("Mission branch %s does not exist", mb)}
	}
	dir, cleanup, err := scratchWorktree(lm.root, target, "kitty-merge-")
	if err != nil {
		return &LaneMergeError{Msg: err.Error()}
	}
	defer cleanup()
	gitMsg := func(res gitx.Result) string {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		return msg
	}
	noop := fmt.Sprintf("Merge of %s into %s produced no changes; target may already contain this tree. Retry with merge resume if recovering an interrupted merge.", mb, target)
	switch lm.o.Strategy {
	case "rebase":
		if res, _ := gitEnv(dir, mergeEnv(false), "checkout", "--detach", mb); res.Code != 0 {
			return &LaneMergeError{Msg: fmt.Sprintf("Failed to check out %s in merge worktree: %s", mb, gitMsg(res))}
		}
		if res, _ := gitEnv(dir, mergeEnv(false), "rebase", target); res.Code != 0 {
			gitEnv(dir, nil, "rebase", "--abort")
			return &LaneMergeError{Msg: fmt.Sprintf("Rebase of %s onto %s failed: %s", mb, target, gitMsg(res))}
		}
	case "squash":
		res, _ := gitEnv(dir, mergeEnv(true), "merge", "--squash", mb)
		if res.Code != 0 {
			gitx.Run(dir, "reset", "-q", "--hard")
			return &LaneMergeError{Msg: fmt.Sprintf("Squash merge of %s into %s failed: %s", mb, target, gitMsg(res))}
		}
		if !gitx.HasStagedChanges(dir) {
			return &LaneMergeError{Msg: fmt.Sprintf("Squash merge of %s into %s produced no changes; target may already contain this tree. Retry with merge resume if recovering an interrupted merge.", mb, target)}
		}
		if _, err := gitx.Run(dir, "commit", "-q", "-m", fmt.Sprintf("feat(%s): squash merge of mission", mb)); err != nil {
			return &LaneMergeError{Msg: "Squash commit into " + target + " failed: " + err.Error()}
		}
	default:
		pre := revParse(dir, "HEAD")
		res, _ := gitEnv(dir, mergeEnv(true), "merge", mb, "--no-edit", "-m", fmt.Sprintf("Merge %s into %s", mb, target))
		if res.Code != 0 {
			gitEnv(dir, nil, "merge", "--abort")
			return &LaneMergeError{Msg: fmt.Sprintf("Merge of %s into %s failed: %s", mb, target, gitMsg(res))}
		}
		if revParse(dir, "HEAD") == pre {
			return &LaneMergeError{Msg: noop}
		}
	}
	if err := advanceBranch(lm.root, target, revParse(dir, "HEAD")); err != nil {
		return &LaneMergeError{Msg: err.Error()}
	}
	return nil
}

// markDone is _mark_wp_merged_done (events stamped with the raw handle).
func (lm *laneMerge) markDone(wpID string) {
	if p, _ := wp.Find(lm.sf.PrimaryDir, wpID); p == "" {
		rich.Print(lm.o.Console, fmt.Sprintf("[yellow]Warning:[/yellow] Could not locate WP file for %s; skipping merge-complete status update.", wpID))
		return
	}
	sf := coord.Resolve(lm.root, lm.o.Slug)
	sf.Slug = lm.o.Handle
	snap, err := status.ReduceDir(sf.StatusDir())
	if err != nil {
		return
	}
	st := snap.WP(wpID)
	lane := string(status.Planned)
	if st != nil {
		lane = status.ResolveLaneAlias(st.GetString("lane"))
	}
	if lane == string(status.Done) {
		return
	}
	var ev *status.DoneEvidence
	if st != nil {
		if rv := st.GetObject("review"); rv != nil {
			if a := strings.TrimSpace(rv.GetString("actor")); a != "" {
				ev = &status.DoneEvidence{Review: status.ReviewApproval{Reviewer: a, Verdict: "approved", Reference: "snapshot-review:" + wpID}}
			}
		}
	}
	if ev == nil {
		if lane != string(status.Approved) {
			rich.Print(lm.o.Console, fmt.Sprintf("[yellow]Warning:[/yellow] %s has no recorded approval metadata; skipping automatic move to done after merge.", wpID))
			return
		}
		reviewer := "unknown"
		if st != nil {
			if a := strings.TrimSpace(st.GetString("agent")); a != "" {
				reviewer = a
			}
		}
		ev = &status.DoneEvidence{Review: status.ReviewApproval{Reviewer: reviewer, Verdict: "approved", Reference: "lane-approved:" + wpID}}
	}
	if lane != string(status.Approved) {
		rich.Print(lm.o.Console, fmt.Sprintf("[yellow]Warning:[/yellow] %s is in lane '%s', not approved; skipping automatic move to done after merge.", wpID, lane))
		return
	}
	reason := fmt.Sprintf("Merged %s into %s", wpID, lm.m.TargetBranch)
	wsc := "merge:" + lm.root
	req := &status.TransitionRequest{WPID: wpID, ToLane: "done", Actor: status.StrActor("merge"), Reason: &reason, Evidence: ev,
		WorkspaceContext: &wsc, RepoRoot: lm.root,
		PolicyMetadata: jsonx.NewObject("merge_phase", "lane_integrated", "target_branch", lm.m.TargetBranch)}
	if _, err := sf.EmitTransition(req); err != nil {
		rich.Print(lm.o.Console, fmt.Sprintf("[yellow]Warning:[/yellow] Failed to mark %s done after merge: %v", wpID, err))
	}
}

// cleanup is _apply_lane_merge_cleanup.
func (lm *laneMerge) cleanup(ret Retention, missionDeletable bool) error {
	mb := lm.m.MissionBranch
	tip := ""
	if missionDeletable {
		tip = revParse(lm.root, "refs/heads/"+mb)
	}
	if ret.RemoveWorktree {
		for _, l := range lm.m.Lanes {
			wt := lanes.WorktreePath(lm.root, lm.o.Handle, l.LaneID)
			if !dirExists(wt) {
				continue
			}
			if err := (&laneMerge{}).dirtyRefusal(wt); err != nil {
				return err
			}
			gitx.Run(lm.root, "worktree", "remove", "--force", wt)
		}
	}
	if ret.DeleteBranch {
		for _, l := range lm.m.Lanes {
			if l.LaneID == lanes.PlanningLaneID {
				continue
			}
			b := lanes.LaneBranchName(lm.o.Handle, l.LaneID, lm.m.TargetBranch)
			if !gitx.BranchExists(lm.root, b) {
				continue
			}
			if !reachableElsewhere(lm.root, b) || worktreeFor(lm.root, b) != "" {
				rich.LogWarning(lm.o.Log, fmt.Sprintf("Lane branch %s was kept: it holds commits no other ref reaches, or git refused the delete", b))
				continue
			}
			if _, err := gitx.Run(lm.root, "branch", "-D", b); err != nil {
				rich.LogWarning(lm.o.Log, fmt.Sprintf("Lane branch %s was kept: it holds commits no other ref reaches, or git refused the delete", b))
				continue
			}
			gitx.Run(lm.root, "update-ref", "-d", "refs/spec-kitty/lane-tip/"+b)
		}
	}
	if missionDeletable && tip != "" && mb != lm.m.TargetBranch {
		cur := revParse(lm.root, "refs/heads/"+mb)
		switch {
		case cur == "":
		case cur != tip:
			return &LaneMergeError{TeardownCode: "COORD_MOVED_AFTER_LANDING",
				Msg: fmt.Sprintf("Merge landed, but mission branch %s moved to %s during cleanup (it was at %s); it was NOT deleted. Review the commit(s) with `git log %s..%s`, land any that belong on the target, then delete the branch yourself. (COORD_MOVED_AFTER_LANDING)",
					pyjson.ReprString(mb), cur[:12], tip[:12], lm.m.TargetBranch, mb)}
		default:
			if wt := worktreeFor(lm.root, mb); wt != "" {
				rich.LogWarning(lm.o.Log, fmt.Sprintf("Mission branch %s was not deleted: Refusing to delete branch '%s': it is checked out in the worktree at %s.", mb, mb, wt))
			} else if _, err := gitx.Run(lm.root, "branch", "-D", mb); err != nil {
				rich.LogWarning(lm.o.Log, fmt.Sprintf("Mission branch %s was not deleted: %v", mb, err))
			}
		}
	}
	return nil
}

func (lm *laneMerge) dirtyRefusal(wt string) error {
	var entries []string
	for _, e := range porcelainAll(wt) {
		if laneDisposable(e.path) || e.xy == "!!" {
			continue
		}
		if e.xy == "??" {
			entries = append(entries, "?? "+e.path+" (untracked local file would be discarded by worktree removal)")
			continue
		}
		entries = append(entries, e.xy+" "+e.path)
	}
	if len(entries) == 0 {
		return nil
	}
	return &DestructiveRefusal{Code: "MERGE_UNSAFE_WORKTREE_DIRTY", Worktree: wt, Entries: entries,
		Remediation: fmt.Sprintf("Commit, stash, or revert the local changes in %s, %s.", wt, resumeNote)}
}

// laneDisposable is the LANE checkout residue context: self-bookkeeping and
// coordination-partition residue (status files) never block lane teardown.
func laneDisposable(p string) bool {
	return (&run{}).disposable(p) || (strings.HasPrefix(p, "kitty-specs/") && mission.IsCoordResidue(p))
}

// reachableElsewhere reports whether branch's tip is reachable from another
// ref (guarded_branch_delete with no creation base).
func reachableElsewhere(root, branch string) bool {
	tip := revParse(root, "refs/heads/"+branch)
	if tip == "" {
		return false
	}
	out, err := gitx.Run(root, "for-each-ref", "--format=%(refname)", "--contains", tip, "refs/heads/", "refs/remotes/")
	if err != nil {
		return false
	}
	for _, ref := range strings.Fields(out) {
		if ref != "refs/heads/"+branch {
			return true
		}
	}
	return false
}

// OriginGateInputs is origin_lane_branches(seam, resume=False) plus
// merge_record_may_exist(seam): the lane branches and record flag the
// orchestrator consolidate-mission origin gate checks.
func OriginGateInputs(root, slug string) ([]string, bool) {
	sf := coord.Resolve(root, slug)
	var branches []string
	if m, err := lanes.Read(sf.PrimaryDir); err == nil && m != nil {
		branches = originfresh.ApprovedLaneBranches(root, slug, m, map[string]bool{})
	}
	return branches, LoadState(root, missionIDOf(root, slug)) != nil
}
