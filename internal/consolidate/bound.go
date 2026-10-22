package consolidate

// The approved bound (consolidation.approved_bound, #5668): which commit
// review approved for a work package, and the check of one code lane
// against it.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Approved-bound refusal codes (BoundRefusalCode).
const (
	LaneMovedAfterApproval  = "LANE_MOVED_AFTER_APPROVAL"
	ApprovalStampMissing    = "APPROVAL_STAMP_MISSING"
	ApprovalStampNotOnLane  = "APPROVAL_STAMP_NOT_ON_LANE"
	AttestApprovedFlag      = "--attest-approved-reviewed"
	approvedReviewedValue   = "approved_reviewed"
	attestationKey          = "attestation"
	laneHeadKey             = "lane_head"
	maxNamedCommits         = 3
	stampName               = "approval stamp"
	stampDefinition         = stampName + " (the lane commit recorded when review approved it)"
	boundAttestReason       = `--attest-reason "<what you checked>"`
	boundCommandIndentation = "  "
)

// BoundRefusal is one lane the approved bound refuses.
type BoundRefusal struct {
	Code    string
	LaneID  string
	Branch  string
	WPIDs   []string
	Commits []string
	Path    string
	Stamp   string
}

func short7s(s string) string { return short7(s) }

func moveBackCommand(wpID, slug string) string {
	return fmt.Sprintf("spec-kitty agent tasks move-task %s --to in_progress --mission %s", wpID, slug)
}

func attestCommand(wps []string, slug string) string {
	var flags []string
	for _, id := range wps {
		flags = append(flags, AttestApprovedFlag+" "+id)
	}
	return fmt.Sprintf("spec-kitty consolidate --mission %s %s %s", slug, strings.Join(flags, " "), boundAttestReason)
}

type stampTerm struct{ defined bool }

func (t *stampTerm) next() string {
	if t.defined {
		return stampName
	}
	t.defined = true
	return stampDefinition
}

func distinctWPs(group []BoundRefusal) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range group {
		for _, id := range r.WPIDs {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

func sendBackRecovery(wps []string, slug, purpose string) []string {
	pronoun := "it"
	if len(wps) != 1 {
		pronoun = "them"
	}
	out := []string{fmt.Sprintf("Recovery: send %s back for review so %s:", strings.Join(wps, ", "), purpose)}
	for _, id := range wps {
		out = append(out, boundCommandIndentation+moveBackCommand(id, slug))
	}
	return append(out, fmt.Sprintf("Then approve %s again and re-run spec-kitty consolidate.", pronoun))
}

func missingBlock(group []BoundRefusal, slug string, term *stampTerm) []string {
	var findings []string
	for _, r := range group {
		for _, id := range r.WPIDs {
			suffix := ", so the commit review approved cannot be determined."
			if len(findings) > 0 {
				suffix = "."
			}
			findings = append(findings, fmt.Sprintf("%s (%s) has no %s%s", id, r.LaneID, term.next(), suffix))
		}
	}
	wps := distinctWPs(group)
	noun, pron, who := "approval", "it", wps[0]
	if len(wps) != 1 {
		noun, pron, who = "approvals", "them", "each of "+strings.Join(wps, ", ")
	}
	out := []string{ApprovalStampMissing + ": " + findings[0]}
	out = append(out, findings[1:]...)
	out = append(out, fmt.Sprintf("Recovery, either attest the %s after checking by hand that the lane holds only reviewed work:", noun),
		boundCommandIndentation+attestCommand(wps, slug), "or send "+who+" back for review:")
	for _, id := range wps {
		out = append(out, boundCommandIndentation+moveBackCommand(id, slug))
	}
	return append(out, fmt.Sprintf("and, after approving %s again, re-run spec-kitty consolidate.", pron))
}

func notOnLaneBlock(group []BoundRefusal, slug string, term *stampTerm) []string {
	var findings []string
	for _, r := range group {
		findings = append(findings, fmt.Sprintf("%s's %s is %s, which is not on branch '%s' (lane %s): the lane was rewritten after review approved it.",
			r.WPIDs[0], term.next(), short7s(r.Stamp), r.Branch, r.LaneID))
	}
	out := []string{ApprovalStampNotOnLane + ": " + findings[0]}
	out = append(out, findings[1:]...)
	return append(out, sendBackRecovery(distinctWPs(group), slug, "the rewritten lane is reviewed")...)
}

func movedFinding(r BoundRefusal, term *stampTerm) string {
	var named []string
	for i, c := range r.Commits {
		if i == maxNamedCommits {
			break
		}
		named = append(named, short7s(c))
	}
	s := strings.Join(named, ", ")
	if more := len(r.Commits) - maxNamedCommits; more > 0 {
		s += fmt.Sprintf(" and %d more", more)
	}
	return fmt.Sprintf("branch '%s' (%s carries %s) holds content committed after its %s: %s (e.g. '%s'); see one with `git show %s`.",
		r.Branch, r.LaneID, strings.Join(r.WPIDs, ", "), term.next(), s, r.Path, short7s(r.Commits[0]))
}

func movedBlock(group []BoundRefusal, slug string, term *stampTerm) []string {
	var findings []string
	for _, r := range group {
		findings = append(findings, movedFinding(r, term))
	}
	out := []string{LaneMovedAfterApproval + ": " + findings[0]}
	out = append(out, findings[1:]...)
	return append(out, sendBackRecovery(distinctWPs(group), slug, "the new content is reviewed")...)
}

// RenderBoundRefusals is render_refusals: a line per refused lane, one
// recovery block per code (codes in order of first appearance).
func RenderBoundRefusals(refusals []BoundRefusal, slug string) string {
	var order []string
	groups := map[string][]BoundRefusal{}
	for _, r := range refusals {
		if _, ok := groups[r.Code]; !ok {
			order = append(order, r.Code)
		}
		groups[r.Code] = append(groups[r.Code], r)
	}
	term := &stampTerm{}
	var out []string
	for _, code := range order {
		switch code {
		case LaneMovedAfterApproval:
			out = append(out, movedBlock(groups[code], slug, term)...)
		case ApprovalStampMissing:
			out = append(out, missingBlock(groups[code], slug, term)...)
		case ApprovalStampNotOnLane:
			out = append(out, notOnLaneBlock(groups[code], slug, term)...)
		}
	}
	return strings.Join(out, "\n")
}

var boundCodeRe = regexp.MustCompile(`(?m)^(` + LaneMovedAfterApproval + `|` + ApprovalStampMissing + `|` + ApprovalStampNotOnLane + `): `)

// BoundRefusalCodes is refusal_codes: every distinct code leading a line.
func BoundRefusalCodes(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range boundCodeRe.FindAllStringSubmatch(text, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// approval stamps

func isApprovedReviewedAttestation(e *status.Event) bool {
	return e.PolicyMetadata != nil && e.PolicyMetadata.GetString(attestationKey) == approvedReviewedValue
}

func isApprovalTransition(e *status.Event) bool {
	if e.To == status.Approved {
		return true
	}
	return e.To == status.Done && !e.Force && e.From != status.Approved
}

func isForcedApprovedMove(e *status.Event) bool {
	return e.Force && e.To == status.Approved && !isApprovedReviewedAttestation(e)
}

func enteredReviewByClaim(earlier []*status.Event, wpID string) bool {
	for i := len(earlier) - 1; i >= 0; i-- {
		if c := earlier[i]; c.WPID == wpID && c.To == status.InReview {
			return !c.Force
		}
	}
	return false
}

func isArbiterOverrideHistory(events []*status.Event, wpID string, from status.Lane, force bool) bool {
	if !force || from != status.Planned {
		return false
	}
	var last *status.Event
	for _, e := range events {
		if e.WPID == wpID {
			last = e
		}
	}
	return last != nil && last.To == status.Planned && (last.From == status.ForReview || last.From == status.InReview) && last.ReviewRef != nil
}

func isForcedReviewApproval(e *status.Event, earlier []*status.Event) bool {
	if e.From == status.InReview && e.ReviewResult != nil && e.ReviewResult.Verdict == "approved" && enteredReviewByClaim(earlier, e.WPID) {
		return true
	}
	return isArbiterOverrideHistory(earlier, e.WPID, e.From, true)
}

func isApprovalAt(events []*status.Event, i int) bool {
	e := events[i]
	if isApprovedReviewedAttestation(e) {
		return true
	}
	if !isApprovalTransition(e) {
		return false
	}
	return !isForcedApprovedMove(e) || isForcedReviewApproval(e, events[:i])
}

func newestApproval(events []*status.Event, wpID string) *status.Event {
	var newest *status.Event
	for i, e := range events {
		if e.WPID == wpID && !strings.HasPrefix(e.Actor.Identity(), status.MigrationActorPrefix) && isApprovalAt(events, i) {
			newest = e
		}
	}
	return newest
}

func stampOf(e *status.Event) string {
	if e == nil || e.PolicyMetadata == nil {
		return ""
	}
	v, _ := e.PolicyMetadata.Get(laneHeadKey)
	s, _ := v.(string)
	return s
}

// approvalStamp is approval_stamp: the lane_head of wpID's newest approval.
func approvalStamp(events []*status.Event, wpID string) string {
	return stampOf(newestApproval(events, wpID))
}

// ---------------------------------------------------------------------------
// lane check

// commitsBeyond is commits_beyond: commits reachable from tip and from none
// of excluded (newest first).
func commitsBeyond(root, tip string, excluded []string) ([]string, error) {
	beyond, err := commitsInRange(root, excluded[0], tip)
	if err != nil {
		return nil, err
	}
	for _, ref := range excluded[1:] {
		if len(beyond) == 0 {
			break
		}
		reach, err := commitsInRange(root, ref, tip)
		if err != nil {
			return nil, err
		}
		set := map[string]bool{}
		for _, s := range reach {
			set[s] = true
		}
		var kept []string
		for _, s := range beyond {
			if set[s] {
				kept = append(kept, s)
			}
		}
		beyond = kept
	}
	return beyond, nil
}

type contentCommit struct{ sha, path string }

// contentCommits is content_commits: non-merge commits with a non-bookkeeping
// path, with that first path.
func contentCommits(root string, commits []string, isBookkeeping func(string) bool) ([]contentCommit, error) {
	var out []contentCommit
	for _, sha := range commits {
		merge, err := isMergeCommit(root, sha)
		if err != nil {
			return nil, err
		}
		if merge {
			continue
		}
		paths, err := changedPathsOf(root, sha)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			if !isBookkeeping(p) {
				out = append(out, contentCommit{sha, p})
				break
			}
		}
	}
	return out, nil
}

// laneExemptCommits is lane_exempt_commits: the union of base..anchor.
func laneExemptCommits(root, base string, anchors []string) map[string]bool {
	out := map[string]bool{}
	for _, a := range anchors {
		cs, err := commitsInRange(root, base, a)
		if err != nil {
			continue
		}
		for _, c := range cs {
			out[c] = true
		}
	}
	return out
}

// checkLane is check_lane (plan D-2).
func checkLane(root string, events []*status.Event, laneID, branch string, approvedWPs []string, claimBase string,
	anchors []string, isBookkeeping func(string) bool, tip string) (*BoundRefusal, error) {
	if tip == "" {
		tip = branch
	}
	cs, err := commitsInRange(root, claimBase, tip)
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, nil
	}
	approved := append([]string(nil), approvedWPs...)
	sort.Strings(approved)
	stamps := map[string]string{}
	var missing []string
	for _, id := range approved {
		stamps[id] = approvalStamp(events, id)
		if stamps[id] == "" {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return &BoundRefusal{Code: ApprovalStampMissing, LaneID: laneID, Branch: branch, WPIDs: missing}, nil
	}
	var covered []string
	for _, id := range approved {
		if !shaReachableFrom(root, stamps[id], tip) {
			return &BoundRefusal{Code: ApprovalStampNotOnLane, LaneID: laneID, Branch: branch, WPIDs: []string{id}, Stamp: stamps[id]}, nil
		}
		covered = append(covered, stamps[id])
	}
	exempt := laneExemptCommits(root, claimBase, anchors)
	all, err := commitsBeyond(root, tip, append([]string{claimBase}, covered...))
	if err != nil {
		return nil, err
	}
	var beyond []string
	for _, s := range all {
		if !exempt[s] {
			beyond = append(beyond, s)
		}
	}
	content, err := contentCommits(root, beyond, isBookkeeping)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return nil, nil
	}
	r := &BoundRefusal{Code: LaneMovedAfterApproval, LaneID: laneID, Branch: branch, WPIDs: approved, Path: content[0].path}
	for _, c := range content {
		r.Commits = append(r.Commits, c.sha)
	}
	return r, nil
}
