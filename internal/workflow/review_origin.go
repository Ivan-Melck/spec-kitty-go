package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/originfresh"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// reviewLaneRefusal is _refuse_review_lane: the (rich-escaped) refusal
// printed before exit 1.
type reviewLaneRefusal struct{ msg string }

func (e *reviewLaneRefusal) Error() string { return e.msg }

// foreignReviewLockMessage is _foreign_review_lock_message.
func foreignReviewLockMessage(ws *lanes.Workspace, agent string) string {
	if !ws.Exists() {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(ws.WorktreePath, ".spec-kitty", "review-lock.json"))
	if err != nil {
		return ""
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil || o == nil {
		return ""
	}
	pid, _ := o.GetInt("pid")
	if pid <= 0 || !status.IsProcessAlive(pid) || o.GetString("agent") == agentOr(agent, "unknown") {
		return ""
	}
	return fmt.Sprintf("Refusing to update the lane: %s has an active review by agent '%s' on %s (PID %d). Wait for it to finish, then re-run.",
		ws.WorktreePath, o.GetString("agent"), o.GetString("wp_id"), pid)
}

// advanceLaneRef is advance_branch_ref for the review fast-forward: refuse
// a checkout holding the branch with local changes, CAS-move the ref, then
// resync every checkout that has it.
func advanceLaneRef(root, branch, oldSHA, newSHA string) error {
	wts, _ := gitx.Worktrees(root)
	var holders []string
	for _, w := range wts {
		if w.Branch == branch || w.Branch == "refs/heads/"+branch {
			holders = append(holders, w.Path)
		}
	}
	for _, wt := range holders {
		r := git(wt, "status", "--porcelain", "--untracked-files=no")
		var dirty []string
		for _, l := range strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n") {
			if strings.TrimSpace(l) != "" {
				dirty = append(dirty, "    "+l)
			}
		}
		if len(dirty) > 0 {
			return &reviewLaneRefusal{fmt.Sprintf("Refusing to advance branch '%s' (%s -> %s): the worktree at %s has it checked out and holds uncommitted local changes that a resync (`git reset --hard`) would destroy (#1826 / NFR-002).\n  Dirty entries:\n%s\n  Commit, stash, or revert these changes in %s, then resume the consolidation (`spec-kitty consolidate --resume`).\n  Then re-run the review.",
				branch, oldSHA[:12], newSHA[:12], wt, strings.Join(dirty, "\n"), wt)}
		}
	}
	if r := git(root, "update-ref", "refs/heads/"+branch, newSHA, oldSHA); r.Code != 0 {
		return &reviewLaneRefusal{fmt.Sprintf("Could not update %s (it changed while it was being updated): %s", branch, strings.TrimSpace(r.Stderr))}
	}
	for _, wt := range holders {
		if r := git(wt, "reset", "--hard", newSHA); r.Code != 0 {
			lanes.RecordTip(root, branch)
			return &reviewLaneRefusal{fmt.Sprintf("%s was advanced to the remote tip, but a checkout could not be resynced: %s\n  Fix that checkout (for example `git -C <worktree> reset --hard HEAD` once it holds no work you need), then re-run the review.", branch, strings.TrimSpace(r.Stderr))}
		}
	}
	return nil
}

// reconcileReviewLane is _reconcile_review_lane (#5758): bring the WP's lane
// to the remote's view before the review claim; returns the remote-tracking
// ref a missing lane branch is cut from ("" = HEAD).
func reconcileReviewLane(o *Out, ws *lanes.Workspace, root, wpID, agent string) (string, error) {
	branch := ws.BranchName
	husk := false
	if !ws.Exists() {
		if st, err := os.Stat(ws.WorktreePath); err == nil && st.IsDir() {
			husk = true
		}
	}
	if ws.RunsInCheckoutRoot() || husk || branch == "" {
		return "", nil
	}
	s := originfresh.ResolveMode(nil)
	if s.Warning != "" {
		o.Printf("Warning: %s", s.Warning)
	}
	a := originfresh.PlanReviewLane(root, branch, s)
	switch a.Kind {
	case originfresh.ReviewRefuse:
		msg := a.Message
		if msg == "" {
			msg = branch + " diverged from its remote"
		}
		return "", &reviewLaneRefusal{msg}
	case originfresh.ReviewWarn:
		o.Printf("Warning: %s; reviewing the last-known %s (%s).", a.Message, branch, wpID)
	case originfresh.ReviewFastForward:
		return "", fastForwardReviewLane(o, ws, root, branch, a, agent)
	case originfresh.ReviewCreateFrom:
		if !ws.Exists() {
			return a.RemoteRef, nil
		}
	}
	return "", nil
}

func fastForwardReviewLane(o *Out, ws *lanes.Workspace, root, branch string, a originfresh.ReviewLaneAction, agent string) error {
	remoteRef := strings.TrimPrefix(a.RemoteRef, "refs/remotes/")
	local := branchTip(root, "refs/heads/"+branch)
	present := a.RemoteSHA != "" && git(root, "cat-file", "-e", a.RemoteSHA+"^{commit}").Code == 0
	descends := present && local != "" && isAncestorIn(root, local, a.RemoteSHA)
	diverged := func(head string) error {
		s := originfresh.ResolveMode(nil)
		if s.Mode == "warn" {
			o.Printf("Warning: %s; reviewing the last-known %s.", originfresh.ReviewWarningText(head, s), branch)
			return nil
		}
		return &reviewLaneRefusal{originfresh.LaneDivergedText(head, a.Verdict.Remote, branch)}
	}
	if !descends {
		return diverged(fmt.Sprintf("%s: %s cannot be fast-forwarded to %s (the remote changed while it was read)", originfresh.LaneDiverged, branch, remoteRef))
	}
	if msg := foreignReviewLockMessage(ws, agent); msg != "" {
		return &reviewLaneRefusal{msg}
	}
	if err := advanceLaneRef(root, branch, local, a.RemoteSHA); err != nil {
		return err
	}
	lanes.RecordTip(root, branch)
	count := strings.TrimSpace(git(root, "rev-list", "--count", local+".."+a.RemoteSHA).Stdout)
	if count == "" {
		count = "?"
	}
	noun := "commits"
	if count == "1" {
		noun = "commit"
	}
	o.Printf("Updated %s from %s (%s %s)", branch, remoteRef, count, noun)
	return nil
}
