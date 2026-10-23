package consolidate

// Operator attestations recorded before the claim (executor
// _record_operator_attestations): --attest-canceled-superseded (FR-012,
// canceled_attestation) and --attest-approved-reviewed (#5668,
// approved_attestation). Each is a forced operator transition from a work
// package's current lane to itself, written through the transactional
// status door so it lands in the event log the claim reads.

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

const (
	attestCanceledFlag = "--attest-canceled-superseded"
	attestReasonFlag   = "--attest-reason"
	canceledSuperseded = "canceled_superseded"
	nothingRecorded    = "Nothing was recorded."
	approvedReasonPfx  = "operator attests approved content reviewed: "
	canceledReasonPfx  = "operator attests canceled content absent or superseded: "
	attestReferencePfx = "operator-attestation:"
	operatorReasonSrc  = "operator"
)

// attestError is AttestationError.
type attestError struct{ msg string }

func (e *attestError) Error() string { return e.msg }

// mergeActor is _resolve_merge_actor.
func mergeActor(root string) string {
	if v := strings.TrimSpace(os.Getenv("SPEC_KITTY_AGENT")); v != "" {
		return v
	}
	if out, err := gitx.Run(root, "config", "user.name"); err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	for _, k := range []string{"GIT_AUTHOR_NAME", "USER", "USERNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "<unknown>"
}

func dedupe(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func approvalIsAttested(events []*status.Event, wpID string) bool {
	n := newestApproval(events, wpID)
	return n != nil && isApprovedReviewedAttestation(n)
}

// planApprovedAttestations is plan_approved_attestations: WP -> current lane.
func (r *run) planApprovedAttestations(wps []string, reason string, excluded map[string]bool) ([][2]string, error) {
	if len(wps) == 0 {
		return nil, nil
	}
	stream, err := status.ReadStream(r.sf.StatusDir(), "")
	if err != nil {
		return nil, &attestError{fmt.Sprintf("%s could not read the status log: %s. %s", AttestApprovedFlag, err, nothingRecorded)}
	}
	events := stream.Transitions
	cur, err := snapshotLanes(r.sf.StatusDir())
	if err != nil {
		return nil, &attestError{fmt.Sprintf("%s could not read the status log: %s. %s", AttestApprovedFlag, err, nothingRecorded)}
	}
	claimLanes := map[string]string{}
	for _, l := range r.m.Lanes {
		for _, id := range l.WPIDs {
			if !excluded[id] && approvedLane(cur[id]) {
				claimLanes[id] = cur[id]
			}
		}
	}
	requested := dedupe(wps)
	if strings.TrimSpace(reason) == "" {
		return nil, &attestError{fmt.Sprintf(`%s requires %s "<why the lane content is the reviewed content>".`, AttestApprovedFlag, attestReasonFlag)}
	}
	var notApproved, stamped []string
	for _, id := range requested {
		if _, ok := claimLanes[id]; !ok {
			notApproved = append(notApproved, id)
		}
	}
	if len(notApproved) > 0 {
		return nil, &attestError{fmt.Sprintf("%s applies only to an approved work package; not approved: %s. %s", AttestApprovedFlag, strings.Join(notApproved, ", "), nothingRecorded)}
	}
	for _, id := range requested {
		if approvalStamp(events, id) != "" && !approvalIsAttested(events, id) {
			stamped = append(stamped, id)
		}
	}
	if len(stamped) > 0 {
		return nil, &attestError{fmt.Sprintf("%s applies only to an approval with no recorded lane head; %s already has one. Move it back for review instead. %s", AttestApprovedFlag, strings.Join(stamped, ", "), nothingRecorded)}
	}
	if err := r.refuseMovedReattestations(requested, events, cur, excluded); err != nil {
		return nil, err
	}
	var plan [][2]string
	for _, id := range requested {
		plan = append(plan, [2]string{id, claimLanes[id]})
	}
	return plan, nil
}

func (r *run) refuseMovedReattestations(wps []string, events []*status.Event, cur map[string]string, excluded map[string]bool) error {
	re := map[string]bool{}
	for _, id := range wps {
		if approvalIsAttested(events, id) && approvalStamp(events, id) != "" {
			re[id] = true
		}
	}
	if len(re) == 0 {
		return nil
	}
	prefix := planningPrefixOf(r.root, r.sf.StatusDir())
	isBK := func(p string) bool { return isBookkeepingPath(p, r.m.MissionSlug, prefix) }
	bl := boundLanes(r.m, cur, excluded)
	var stampAnchors []string
	for _, l := range bl {
		for _, id := range boundApprovedWPs(l, cur, excluded) {
			if s := approvalStamp(events, id); s != "" {
				stampAnchors = append(stampAnchors, s)
			}
		}
	}
	for _, l := range bl {
		approved := boundApprovedWPs(l, cur, excluded)
		var hit []string
		var stampedApproved []string
		for _, id := range approved {
			if re[id] {
				hit = append(hit, id)
			}
			if approvalStamp(events, id) != "" {
				stampedApproved = append(stampedApproved, id)
			}
		}
		if len(hit) == 0 {
			continue
		}
		sortStrings(hit)
		branch := laneBranchFor(r.m, l.LaneID)
		base, err := resolveCommit(r.root, r.m.TargetBranch)
		var refusal *BoundRefusal
		if err == nil {
			refusal, err = checkLane(r.root, events, l.LaneID, branch, stampedApproved, base,
				append(closedWorldAnchors(r.m, l, "", excluded), stampAnchors...), isBK, "")
		}
		if err != nil {
			return &attestError{fmt.Sprintf("%s could not read lane %s to check the earlier attestation of %s: %s. %s", AttestApprovedFlag, l.LaneID, strings.Join(hit, ", "), err, nothingRecorded)}
		}
		if refusal != nil {
			return &attestError{fmt.Sprintf("%s cannot be repeated for %s: lane %s (branch '%s') moved past the earlier attestation, so its new content would otherwise read as approved. "+
				"Move %s back for review (%s) so the new content is reviewed, approve it again, then re-run spec-kitty consolidate. %s",
				AttestApprovedFlag, strings.Join(hit, ", "), l.LaneID, branch, hit[0], moveBackCommand(hit[0], r.m.MissionSlug), nothingRecorded)}
		}
	}
	return nil
}

// acceptablyCanceled is acceptably_canceled_wp_ids: canceled with operator
// provenance.
func (r *run) acceptablyCanceled() map[string]bool { return acceptablyCanceledIn(r.sf) }

func acceptablyCanceledIn(sf *coord.Surface) map[string]bool {
	out := map[string]bool{}
	snap, err := status.ReduceDir(sf.StatusDir())
	if err != nil {
		return out
	}
	for _, id := range snap.WorkPackages.Keys() {
		st := snap.WP(id)
		if st.GetString("lane") == string(status.Canceled) && st.GetString("reason_source") == operatorReasonSrc {
			out[id] = true
		}
	}
	return out
}

// recordAttestations is _record_operator_attestations: validates both
// requests, then records them; returns the attested WP ids.
func (r *run) recordAttestations(canceledWPs, approvedWPs []string, reason string) ([]string, error) {
	if len(canceledWPs) == 0 && len(approvedWPs) == 0 {
		return nil, nil
	}
	excluded := r.acceptablyCanceled()
	fail := func(err error) ([]string, error) {
		say(r.w, "[red]Error:[/red] "+err.Error())
		return nil, exit(1)
	}
	plan, err := r.planApprovedAttestations(approvedWPs, reason, excluded)
	if err != nil {
		return fail(err)
	}
	actor := mergeActor(r.root)
	var recorded []string
	if len(canceledWPs) > 0 {
		requested := dedupe(canceledWPs)
		if strings.TrimSpace(reason) == "" {
			return fail(&attestError{fmt.Sprintf(`%s requires %s "<why the canceled content is absent or superseded>".`, attestCanceledFlag, attestReasonFlag)})
		}
		var not []string
		for _, id := range requested {
			if !excluded[id] {
				not = append(not, id)
			}
		}
		if len(not) > 0 {
			return fail(&attestError{fmt.Sprintf("%s applies only to a WP canceled with operator provenance; not canceled: %s. Nothing was recorded.", attestCanceledFlag, strings.Join(not, ", "))})
		}
		for _, id := range requested {
			rs, rsn := operatorReasonSrc, canceledReasonPfx+strings.TrimSpace(reason)
			req := &status.TransitionRequest{WPID: id, ToLane: string(status.Canceled), Actor: status.StrActor(actor), Force: true,
				Reason: &rsn, ReasonSource: &rs, RepoRoot: r.root, PolicyMetadata: jsonx.NewObject(attestationKey, canceledSuperseded)}
			if _, err := r.sf.EmitTransition(req); err != nil {
				return fail(err)
			}
			say(r.w, fmt.Sprintf("[yellow]⚠️  Operator attestation recorded for canceled %s by %s:[/yellow] %s", id, actor, strings.TrimSpace(reason)))
			recorded = append(recorded, id)
		}
	}
	for _, p := range plan {
		id, lane := p[0], p[1]
		rs, rsn := operatorReasonSrc, approvedReasonPfx+strings.TrimSpace(reason)
		req := &status.TransitionRequest{WPID: id, ToLane: lane, Actor: status.StrActor(actor), Force: true, Reason: &rsn, ReasonSource: &rs,
			RepoRoot: r.root, PolicyMetadata: jsonx.NewObject(attestationKey, approvedReviewedValue),
			Evidence: &status.DoneEvidence{Review: status.ReviewApproval{Reviewer: actor, Verdict: "approved", Reference: attestReferencePfx + id}}}
		if _, err := r.sf.EmitTransition(req); err != nil {
			return fail(err)
		}
		say(r.w, fmt.Sprintf("[yellow]⚠️  Operator attestation recorded for approved %s by %s:[/yellow] %s", id, actor, strings.TrimSpace(reason)))
		recorded = append(recorded, id)
	}
	return recorded, nil
}
