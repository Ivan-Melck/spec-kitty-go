package workflow

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// CommitWorkflowChange is workflow_executor.commit_workflow_change: commit
// the workflow's paths (coordination transaction for coord missions, the
// legacy safe-commit path otherwise), record the receipts and, when asked,
// merge the coordination branch into the WP's lane. rb is the rollback
// point captured before the operation's first status write: a failed
// commit or a refused lane sync cuts only the rows appended since, and a
// rollback that refuses because those rows are already committed reports
// the claim as committed (#5819, #5804).
func CommitWorkflowChange(o *Out, rc *Receipts, root, slug, targetBranch string, paths []string, message, operation, wpID string, autoRebase bool, rb *RollbackPoint) error {
	s := coord.Resolve(root, slug)
	coordBranch := strings.TrimSpace(s.Meta.GetString("coordination_branch"))
	mid := s.Meta.GetString("mission_id")
	mid8 := s.Meta.GetString("mid8")
	if mid8 == "" && len(mid) >= 8 {
		mid8 = mission.ResolveMid8(slug, mid)
	}
	fail := func(err error, ref, prefix string, recoveryNote bool) error {
		return handleCommitFailure(o, rc, root, err, ref, message, wpID, operation, rb, prefix, recoveryNote)
	}
	if coordBranch != "" && mid != "" && mid8 != "" {
		prefix := fmt.Sprintf("Error: Failed to record %s via BookkeepingTransaction", operation)
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
				return fail(err, coordBranch, prefix, false)
			}
			rc.Record(primaryRef, message, "committed", sha, wpID)
		}
		wt := s.CommitRoot()
		sha, committed, err := commitIdempotent(wt, message, coordPaths)
		if err != nil {
			return fail(err, coordBranch, prefix, false)
		}
		rc.Record(coordBranch, message, "committed", sha, wpID)
		if autoRebase {
			var claimIDs []string
			claimOK := false
			if rb != nil {
				claimIDs, claimOK = rb.AppendedEventIDs()
			}
			if err := SyncLaneAfterCoordinationCommit(root, slug, wpID, coordBranch); err != nil {
				syncLaneRefused(o, rc, err, wt, sha, committed, slug, wpID, operation, rb, claimIDs, claimOK)
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
		return fail(err, ref, fmt.Sprintf("Error: Failed to commit workflow status update for %s", wpID), true)
	}
	rc.Record(ref, message, "committed", sha, wpID)
	return nil
}

// reportRefusedRollback is _report_refused_rollback: print the account of
// a rollback that left the files alone; true when the claim was committed.
func reportRefusedRollback(o *Out, out RollbackOutcome, wpID, operation string, exc error, failedStep string) bool {
	if out.Refusal == RefusalTailAlreadyCommitted {
		cause := ""
		if exc != nil {
			cause = ": " + exc.Error()
		}
		step := failedStep
		if step == "" {
			step = "follow-up " + operation + " commit"
		}
		o.Printf("%s claim was committed; the %s failed%s", wpID, step, cause)
		return true
	}
	o.Print(out.Message())
	return false
}

// handleCommitFailure is _handle_commit_failure: roll the status files back
// to rb, record the receipt and print the failure (exit 1).
func handleCommitFailure(o *Out, rc *Receipts, root string, exc error, ref, message, wpID, operation string, rb *RollbackPoint, prefix string, recoveryNote bool) error {
	var out *RollbackOutcome
	if rb != nil {
		r := rb.Restore(o.Err, nil, false)
		out = &r
	}
	if out != nil && out.Refusal == RefusalTailAlreadyCommitted {
		rc.Record(ref, message, "committed", committedReceiptSHA(root, ref), wpID)
		reportRefusedRollback(o, *out, wpID, operation, exc, "")
		return ExitErr(1)
	}
	rc.Record(ref, message, "refused", "", wpID)
	text := fmt.Sprintf("%s: %v", prefix, exc)
	if out != nil && !out.RolledBack {
		text += ". " + out.Message()
	} else if recoveryNote {
		text += ". Event log rolled back to pre-emit state."
	}
	o.Print(text)
	return ExitErr(1)
}

// committedReceiptSHA is _committed_receipt_sha.
func committedReceiptSHA(root, ref string) string {
	return gitOut(root, "rev-parse", "--verify", "--quiet", ref)
}

// revertCoordinationCommit is _revert_coordination_commit: undo the claim's
// coordination commit (nothing when the transaction was a no-op).
func revertCoordinationCommit(wt, sha string, committed bool) error {
	if !committed {
		return nil
	}
	head := git(wt, "rev-parse", "HEAD")
	if head.Code != 0 {
		return fmt.Errorf("could not inspect coordination worktree HEAD before rollback: %s", strings.TrimSpace(firstNonEmpty(head.Stderr, head.Stdout)))
	}
	if h := strings.TrimSpace(head.Stdout); h != sha {
		return fmt.Errorf("refusing to rollback lifecycle commit because coordination branch advanced from %s to %s", sha, h)
	}
	if r := git(wt, "-c", "commit.gpgsign=false", "revert", "--no-edit", sha); r.Code != 0 {
		return fmt.Errorf("failed to rollback lifecycle coordination commit after lane sync refusal: %s", strings.TrimSpace(firstNonEmpty(r.Stderr, r.Stdout)))
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// syncLaneRefused is the refusal arm of _sync_lane_or_revert: revert the
// coordination commit, roll back exactly the claim's rows and render the
// lane-sync failure.
func syncLaneRefused(o *Out, rc *Receipts, exc error, wt, sha string, committed bool, slug, wpID, operation string, rb *RollbackPoint, claimIDs []string, claimOK bool) {
	if err := revertCoordinationCommit(wt, sha, committed); err != nil {
		o.Printf("Error: Failed to rollback lifecycle state after lane sync refusal: %v", err)
		o.Printf("  %s stays claimed and its lane was not synced. Resolve the lane conflict, then run `spec-kitty agent action implement %s` again to re-sync, or move it back with `spec-kitty agent tasks move-task %s --to planned --mission %s`.", wpID, wpID, wpID, slug)
	} else if rb != nil {
		var out RollbackOutcome
		tail, tailOK := rb.AppendedEventIDs()
		reverted := tailOK && len(tail) == 0
		switch {
		case reverted:
			out = rb.Restore(o.Err, nil, false)
		case claimOK:
			out = rb.Restore(o.Err, claimIDs, true)
		default:
			out = RollbackOutcome{Refusal: RefusalTailUnparseable, EventsPath: rb.EventsPath}
		}
		if out.Refusal != RefusalTailAlreadyCommitted {
			rc.MarkRefused(sha)
		}
		if !out.RolledBack {
			reportRefusedRollback(o, out, wpID, operation, exc, "lane sync after the "+operation+" commit")
		}
	} else {
		rc.MarkRefused(sha)
	}
	if se, ok := exc.(*LaneSyncError); ok {
		se.Render(o)
	} else {
		o.Printf("Error: %v", exc)
	}
}
