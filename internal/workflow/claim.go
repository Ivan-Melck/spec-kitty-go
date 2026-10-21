package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// ClaimConflict is WorkPackageClaimConflict.
type ClaimConflict struct{ Msg string }

func (e *ClaimConflict) Error() string { return e.Msg }

// StartRejected is WorkPackageStartRejected.
type StartRejected struct{ Msg string }

func (e *StartRejected) Error() string { return e.Msg }

// StartResult is WorkPackageStartResult.
type StartResult struct {
	From, To  string
	Events    []*status.Event
	NoOp      bool
	ClaimedBy string
}

// ShellPID is os.getppid() (the shell running the command).
func ShellPID() int { return os.Getppid() }

// ClaimPolicyMetadata is status.emit.claim_policy_metadata.
func ClaimPolicyMetadata(pid int, agent string) *jsonx.Object {
	o := jsonx.NewObject("shell_pid", pid)
	if b, ok := status.CreationTimeBaseline(pid); ok {
		o.Set("shell_pid_created_at", b)
	}
	o.Set("agent", agent)
	return o
}

// currentState reads (lane, actor identity, role) of wpID on the write surface.
func currentState(s *coord.Surface, wpID string) (string, string, string) {
	dir := s.StatusDir()
	stream, err := status.ReadStream(dir, "")
	if err != nil {
		return string(status.Genesis), "", ""
	}
	snap := status.Reduce(stream.Transitions, stream.Annotations, true)
	st := snap.WP(wpID)
	if st == nil {
		return string(status.Genesis), "", ""
	}
	return st.GetString("lane"), st.GetString("actor"), st.GetString("role")
}

func actorsCompatible(existing string, requested status.Actor, allowGeneric bool) bool {
	ek := status.ActorKeyStr(existing)
	rk := status.ActorKey(requested)
	if ek == "" || rk == "" || ek == rk {
		return true
	}
	return allowGeneric && status.GenericImplementationActors[ek]
}

func latestImplementer(s *coord.Surface, wpID string) string {
	stream, err := status.ReadStream(s.StatusDir(), "")
	if err != nil {
		return ""
	}
	return status.LatestImplementer(stream.Transitions, wpID)
}

var reviewLanes = map[string]bool{"for_review": true, "in_review": true, "approved": true}

// StartImplementation is start_implementation_status.
func StartImplementation(s *coord.Surface, wpID string, actor status.Actor, wsCtx, execMode string,
	pm *jsonx.Object, delta *status.Delta, reviewLaneExit bool, forceNote *string) (*StartResult, error) {
	lane, curActor, _ := currentState(s, wpID)
	if lane == string(status.Genesis) {
		return nil, &StartRejected{fmt.Sprintf("WP %s is not finalized; run `spec-kitty agent mission finalize-tasks`", wpID)}
	}
	if forceNote != nil {
		if strings.TrimSpace(*forceNote) == "" {
			return nil, &StartRejected{status.ForceNoteRequired}
		}
		if !reviewLaneExit || !reviewLanes[lane] {
			return nil, &StartRejected{fmt.Sprintf("--force only applies to a WP in for_review, in_review or approved; %s is in '%s'", wpID, lane)}
		}
	}
	mk := func(to string, withCtx bool, d *status.Delta) *status.TransitionRequest {
		r := &status.TransitionRequest{WPID: wpID, ToLane: to, Actor: actor, ExecutionMode: execMode, RepoRoot: s.Root, PolicyMetadata: pm}
		if withCtx {
			c := wsCtx
			r.WorkspaceContext = &c
			r.AnnotationDelta = d
		}
		return r
	}
	switch lane {
	case string(status.Planned):
		evs, err := s.EmitTransitionBatch([]*status.TransitionRequest{mk("claimed", false, nil), mk("in_progress", true, delta)})
		if err != nil {
			return nil, wrapTransition(err)
		}
		return &StartResult{From: lane, To: "in_progress", Events: evs, ClaimedBy: actor.Identity()}, nil
	case string(status.Claimed):
		if !actorsCompatible(curActor, actor, true) {
			return nil, &ClaimConflict{fmt.Sprintf("WP %s is already claimed for implementation by '%s'", wpID, orUnknown(curActor))}
		}
		evs, err := s.EmitTransitionBatch([]*status.TransitionRequest{mk("in_progress", true, delta)})
		if err != nil {
			return nil, wrapTransition(err)
		}
		return &StartResult{From: lane, To: "in_progress", Events: evs, ClaimedBy: actor.Identity()}, nil
	case string(status.InProgress):
		if !actorsCompatible(curActor, actor, true) {
			if !status.IsLatestImplementer(latestImplementer(s, wpID), actor.Identity()) {
				return nil, &ClaimConflict{fmt.Sprintf("WP %s is already claimed for implementation by '%s'", wpID, orUnknown(curActor))}
			}
			return &StartResult{From: lane, To: lane, NoOp: true, ClaimedBy: actor.Identity()}, nil
		}
		return &StartResult{From: lane, To: lane, NoOp: true, ClaimedBy: curActor}, nil
	}
	if reviewLaneExit && reviewLanes[lane] {
		var reason string
		switch {
		case forceNote != nil:
			reason = "Operator force: " + strings.TrimSpace(*forceNote)
		case lane == "in_review":
			return nil, &ClaimConflict{fmt.Sprintf("WP %s is already claimed for review by '%s'", wpID, orUnknown(curActor))}
		case lane == "approved":
			return nil, &StartRejected{fmt.Sprintf("WP %s is approved; implementation cannot resume it. To rework it run `spec-kitty agent tasks move-task %s --to planned --review-feedback-file <file>`, or, as an operator, %s", wpID, wpID, status.ForceNoteHint)}
		default:
			impl := latestImplementer(s, wpID)
			if !status.IsLatestImplementer(impl, actor.Identity()) {
				holder := impl
				if holder == "" || holder == "unknown" {
					holder = "none on record"
				}
				return nil, &ClaimConflict{fmt.Sprintf("WP %s is in for_review; only its implementer of record (%s) may withdraw it, or an operator with %s", wpID, holder, status.ForceNoteHint)}
			}
			reason = fmt.Sprintf("Implementer of record withdrew %s from for_review to continue implementation", wpID)
		}
		req := mk("in_progress", true, delta)
		req.Force = true
		req.Reason = &reason
		res, err := s.EmitTransition(req)
		if err != nil {
			return nil, wrapTransition(err)
		}
		return &StartResult{From: lane, To: "in_progress", Events: []*status.Event{res.Event}, ClaimedBy: actor.Identity()}, nil
	}
	return nil, &StartRejected{fmt.Sprintf("WP %s is in '%s', cannot start implementation", wpID, lane)}
}

// StartReview is start_review_status.
func StartReview(s *coord.Surface, wpID string, actor status.Actor, wsCtx, execMode string, pm *jsonx.Object, delta *status.Delta) (*StartResult, error) {
	lane, curActor, curRole := currentState(s, wpID)
	switch lane {
	case string(status.ForReview):
		reason := "Started review via action command"
		rr := "action-review-claim"
		c := wsCtx
		req := &status.TransitionRequest{WPID: wpID, ToLane: "in_review", Actor: actor, Reason: &reason, ReviewRef: &rr,
			WorkspaceContext: &c, ExecutionMode: execMode, RepoRoot: s.Root, PolicyMetadata: pm, AnnotationDelta: delta}
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

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func wrapTransition(err error) error {
	if te, ok := err.(*status.TransitionError); ok {
		return &StartRejected{te.Msg}
	}
	return err
}

// statusPair returns the status.events.jsonl / status.json paths in dir
// (existing ones only).
func statusPair(dir string) []string {
	var out []string
	for _, f := range []string{status.EventsFile, status.SnapshotFile} {
		p := filepath.Join(dir, f)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// isPrimaryPartition reports a PRIMARY-partition mission path (WP task
// files, spec/plan/tasks, meta, lanes) — everything except the status pair.
func isPrimaryPartition(slug, abs string) bool {
	base := filepath.Base(abs)
	if base == status.EventsFile || base == status.SnapshotFile {
		return false
	}
	return strings.Contains(filepath.ToSlash(abs), "/kitty-specs/"+slug+"/") && !strings.Contains(filepath.ToSlash(abs), "/.worktrees/")
}

func relTo(dir, p string) string {
	r, err := filepath.Rel(dir, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}

// commitIdempotent stages and commits rel paths in dir; returns
// (sha, committed). A no-op returns the HEAD sha.
func commitIdempotent(dir, message string, paths []string) (string, bool, error) {
	var existing []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			existing = append(existing, p)
		}
	}
	if len(existing) == 0 {
		h, _ := gitx.HeadSHA(dir)
		return h, false, nil
	}
	sha, err := coord.CommitPaths(dir, message, existing...)
	if err != nil {
		return "", false, err
	}
	if sha == "" {
		h, _ := gitx.HeadSHA(dir)
		return h, false, nil
	}
	return sha, true, nil
}

// CommitWorkflowChange is workflow_executor.commit_workflow_change: commit
// the workflow's paths (coordination transaction for coord missions, the
// legacy safe-commit path otherwise), record the receipts and, when asked,
// merge the coordination branch into the WP's lane.
func CommitWorkflowChange(o *Out, rc *Receipts, root, slug, targetBranch string, paths []string, message, operation, wpID string, autoRebase bool) error {
	s := coord.Resolve(root, slug)
	coordBranch := strings.TrimSpace(s.Meta.GetString("coordination_branch"))
	mid := s.Meta.GetString("mission_id")
	mid8 := s.Meta.GetString("mid8")
	if mid8 == "" && len(mid) >= 8 {
		mid8 = mission.ResolveMid8(slug, mid)
	}
	if coordBranch != "" && mid != "" && mid8 != "" {
		var primary, coordPaths []string
		for _, p := range paths {
			if isPrimaryPartition(slug, p) {
				primary = append(primary, p)
			} else {
				coordPaths = append(coordPaths, p)
			}
		}
		if len(primary) > 0 {
			primaryRef := s.TargetBranch()
			if primaryRef == "" {
				primaryRef = targetBranch
			}
			sha, _, err := commitIdempotent(root, message, primary)
			if err != nil {
				rc.Record(primaryRef, message, "refused", "", wpID)
				o.Printf("Error: Failed to record %s via BookkeepingTransaction: %v", operation, err)
				return ExitErr(1)
			}
			rc.Record(primaryRef, message, "committed", sha, wpID)
		}
		wt := s.CommitRoot()
		sha, committed, err := commitIdempotent(wt, message, coordPaths)
		if err != nil {
			rc.Record(coordBranch, message, "refused", "", wpID)
			o.Printf("Error: Failed to record %s via BookkeepingTransaction: %v", operation, err)
			return ExitErr(1)
		}
		rc.Record(coordBranch, message, "committed", sha, wpID)
		if autoRebase {
			if err := SyncLaneAfterCoordinationCommit(root, slug, wpID, coordBranch); err != nil {
				if committed {
					if gitOut(wt, "rev-parse", "HEAD") == sha {
						git(wt, "-c", "commit.gpgsign=false", "revert", "--no-edit", sha)
					}
					rc.MarkRefused(sha)
				}
				if se, ok := err.(*LaneSyncError); ok {
					se.Render(o)
				} else {
					o.Printf("Error: %v", err)
				}
				return ExitErr(1)
			}
		}
		return nil
	}
	if coordBranch != "" {
		o.Printf("Error: coord-commit misroute prevented for %s: mission '%s' declares coordination_branch '%s' but its identity triple is incomplete (mission_id/mid8 unresolved). Refusing to commit coordination artifacts from the repository root. Repair meta.json (mission_id/mid8) and retry.", wpID, slug, coordBranch)
		return ExitErr(1)
	}
	ref := s.TargetBranch()
	if ref == "" {
		ref = targetBranch
	}
	porcelain := root
	if s.CoordWorktree != "" {
		if _, err := os.Stat(s.CoordWorktree); err == nil {
			porcelain = s.CoordWorktree
		}
	}
	var rels []string
	for _, p := range paths {
		rels = append(rels, relTo(porcelain, p))
	}
	if r := git(porcelain, append([]string{"status", "--porcelain", "--"}, rels...)...); r.Code == 0 && strings.TrimSpace(r.Stdout) == "" {
		rc.Record(ref, message, "committed", alreadyPresentSHA(porcelain, ref, rels), wpID)
		return nil
	}
	sha, _, err := commitIdempotent(root, message, paths)
	if err != nil {
		rc.Record(ref, message, "refused", "", wpID)
		o.Printf("Error: Failed to commit workflow status update for %s: %v. Event log rolled back to pre-emit state.", wpID, err)
		return ExitErr(1)
	}
	rc.Record(ref, message, "committed", sha, wpID)
	return nil
}

// alreadyPresentSHA is _already_present_receipt_sha.
func alreadyPresentSHA(dir, target string, rels []string) string {
	sha := gitOut(dir, append([]string{"log", "-1", "--format=%H", "HEAD", "--"}, rels...)...)
	if sha == "" {
		return ""
	}
	for _, r := range rels {
		if git(dir, "cat-file", "-e", sha+":"+r).Code != 0 {
			return ""
		}
	}
	if !isAncestorIn(dir, sha, target) {
		return ""
	}
	return sha
}
