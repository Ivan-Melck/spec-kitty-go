package originfresh

import (
	"fmt"
	"strings"
)

// Review-lane action kinds (ReviewLaneKind).
const (
	ReviewKeep        = "keep"
	ReviewWarn        = "warn"
	ReviewRefuse      = "refuse"
	ReviewFastForward = "fast_forward"
	ReviewCreateFrom  = "create_from"
)

// ReviewLaneAction is origin_freshness.ReviewLaneAction.
type ReviewLaneAction struct {
	Kind      string
	Verdict   Verdict
	RemoteSHA string
	RemoteRef string // refs/remotes/<remote>/<branch> ("" when unknown)
	Code      string
	Message   string
}

// CheckBranch is check_branches(root, [branch]) for one branch.
func CheckBranch(root, branch string) Verdict {
	return checkProbes(root, []probe{{branch: branch}})[0]
}

// Headline is _headline (the first line of a refusal).
func Headline(code string, v Verdict) string { return headline(code, v) }

const reviewOptOutLine = "Opt out (not recommended): " + Env + "=warn."

// LaneDivergedText is lane_diverged_text.
func LaneDivergedText(head, remote, branch string) string {
	lines := []string{head,
		fmt.Sprintf("  Inspect: git log %s...%s/%s", branch, remote, branch),
		fmt.Sprintf("  If the remote lane has work you need: git -C <lane worktree> merge %s/%s", remote, branch),
		fmt.Sprintf("  If the local lane is the truth: merge or rebase it onto %s/%s first, then git push %s %s", remote, branch, remote, branch),
		fmt.Sprintf("    (git push --force-with-lease would instead discard the commits on %s/%s: use it only if you mean to lose them.)", remote, branch),
		rerunLine, reviewOptOutLine}
	return strings.Join(lines, "\n")
}

// ReviewWarningText is review_warning_text.
func ReviewWarningText(head string, s Setting) string {
	return fmt.Sprintf("%s (origin check is %s, source: %s)", head, s.Mode, s.Source)
}

// PlanReviewLane is plan_review_lane: map the lane's freshness verdict to
// the review-prep action.
func PlanReviewLane(root, branch string, s Setting) ReviewLaneAction {
	if s.Mode == "off" {
		return ReviewLaneAction{Kind: ReviewWarn, Verdict: Verdict{Branch: branch, State: "not_checked"}, Message: NotCheckedWarning(s)}
	}
	v := CheckBranch(root, branch)
	ref := ""
	if v.Remote != "" {
		ref = trackingRef(v.Remote, branch)
	}
	switch v.State {
	case "behind":
		return ReviewLaneAction{Kind: ReviewFastForward, Verdict: v, RemoteSHA: v.RemoteSHA, RemoteRef: ref}
	case "local_missing":
		return ReviewLaneAction{Kind: ReviewCreateFrom, Verdict: v, RemoteSHA: v.RemoteSHA, RemoteRef: ref}
	case "diverged":
		head := headline(LaneDiverged, v)
		if s.Mode == "warn" {
			return ReviewLaneAction{Kind: ReviewWarn, Verdict: v, Code: LaneDiverged, Message: ReviewWarningText(head, s)}
		}
		return ReviewLaneAction{Kind: ReviewRefuse, Verdict: v, RemoteRef: ref, Code: LaneDiverged, Message: LaneDivergedText(head, v.Remote, branch)}
	case "unreachable":
		return ReviewLaneAction{Kind: ReviewWarn, Verdict: v, Code: Unreachable, Message: headline(Unreachable, v)}
	case "remote_ambiguous":
		return ReviewLaneAction{Kind: ReviewWarn, Verdict: v, Code: RemoteAmbig, Message: headline(RemoteAmbig, v)}
	case "compare_failed":
		return ReviewLaneAction{Kind: ReviewWarn, Verdict: v, Code: CompareFailed, Message: headline(CompareFailed, v)}
	case "remote_missing":
		if v.Detail != "" {
			return ReviewLaneAction{Kind: ReviewWarn, Verdict: v, Message: v.Detail}
		}
	}
	return ReviewLaneAction{Kind: ReviewKeep, Verdict: v}
}
