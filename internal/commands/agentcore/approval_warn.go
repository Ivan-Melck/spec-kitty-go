package agentcore

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// approvalWarnings is consolidation.approved_bound.approval_warnings: the
// advisory lines a shell prints (stderr) after persisting an approval-ish
// event. primaryDir locates lanes.json; statusDir is the event log home.
func approvalWarnings(e *status.Event, primaryDir, statusDir string) []string {
	if e == nil {
		return nil
	}
	man, err := lanes.Read(primaryDir)
	if err != nil || man == nil {
		return nil
	}
	if l := man.LaneForWP(e.WPID); l == nil || l.LaneID == lanes.PlanningLaneID {
		return nil
	}
	if isForcedApprovedMove(e) {
		var log []*status.Event
		if stream, err := status.ReadStream(statusDir, ""); err == nil {
			log = stream.Transitions
			for i, c := range log {
				if c.EventID == e.EventID {
					log = log[:i]
					break
				}
			}
		}
		if !isForcedReviewApproval(e, log) {
			return []string{fmt.Sprintf("Warning: the forced approval of %s is not a review approval; `spec-kitty consolidate` still bounds %s at its last review approval (commits made since refuse with LANE_MOVED_AFTER_APPROVAL), and refuses it as unstamped when it has none.", e.WPID, e.WPID)}
		}
	}
	if !isApprovalTransition(e) || (e.PolicyMetadata != nil && e.PolicyMetadata.Has("lane_head")) {
		return nil
	}
	return []string{fmt.Sprintf("Warning: no lane head could be recorded for %s's approval; `spec-kitty consolidate` will refuse it (APPROVAL_STAMP_MISSING) until it is approved again or attested.", e.WPID)}
}

func isApprovedReviewedAttestation(e *status.Event) bool {
	return e.PolicyMetadata != nil && e.PolicyMetadata.GetString("attestation") == "approved_reviewed"
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

// isForcedReviewApproval is _is_forced_review_approval over the log strictly
// before e: a claimed review's approved verdict, or an arbiter override.
func isForcedReviewApproval(e *status.Event, earlier []*status.Event) bool {
	if e.From == status.InReview && e.ReviewResult != nil && e.ReviewResult.Verdict == "approved" {
		for i := len(earlier) - 1; i >= 0; i-- {
			c := earlier[i]
			if c.WPID == e.WPID && c.To == status.InReview {
				if !c.Force {
					return true
				}
				break
			}
		}
	}
	if e.From != status.Planned {
		return false
	}
	var last *status.Event
	for _, c := range earlier {
		if c.WPID == e.WPID {
			last = c
		}
	}
	return last != nil && last.To == status.Planned && (last.From == status.ForReview || last.From == status.InReview) && last.ReviewRef != nil
}
