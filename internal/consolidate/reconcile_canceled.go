package consolidate

// The canceled-with-provenance parts of the reconciliation claim
// (consolidation.reconciliation): fully-canceled dependency lanes whose
// commits an approved lane carries (#5569/#5613), mixed lanes holding a
// canceled WP beside a surviving one (#5046), and the verifier axes that
// judge what they left on the target.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

const (
	canceledReachableCode = "CANCELED_REACHABLE_VIA_DEPENDENCY"
	alsoHas               = "This Mission also has:"
	bothNeeded            = "Both parts need their own fix, in either order: the recovery in the first part does not approve the new content, " +
		"and approving the work package again does not clear the first part. Re-run spec-kitty consolidate after both."
)

// canceledDependencyContent is CanceledDependencyContent.
type canceledDependencyContent struct {
	wpIDs                     []string
	canceledLane, carrierLane string
	path                      string
	state                     pstate
}

func attestTail(wpID string) string {
	return fmt.Sprintf(`re-run spec-kitty consolidate with %s %s %s "<what you checked>"`, attestCanceledFlag, wpID, attestReasonFlag)
}

func mixedLaneRecovery(reason, wpID string) string {
	switch reason {
	case reasonEventsUnreadable:
		return "Recovery: repair the mission's status event log (status.events.jsonl) so it reads cleanly, " + recoveryTail + "; this refusal cannot be overridden"
	case reasonSpineUnreadable:
		return "Recovery: repair the lane branch so its git history reads from the coordination base, " + recoveryTail + "; this refusal cannot be overridden"
	case reasonCanceledLaneContent:
		return "Re-running alone cannot clear this. " +
			"Recovery: remove those commits from the dependent lane by reverting them on it " +
			"(do not rebuild the lane: a rebuilt lane trips the resume lane-tip check), " +
			recoveryTail + "; this refusal cannot be overridden"
	case reasonCommitOutside:
		return "No governed WP window will ever cover those commits, so re-running alone cannot clear this. " +
			fmt.Sprintf("Recovery: verify by hand that they carry no canceled work and that %s's canceled content is "+
				"absent or superseded, then %s", wpID, attestTail(wpID))
	}
	return "This attribution evidence cannot appear later (the event log is append-only), so re-running alone " +
		fmt.Sprintf("cannot clear it. Recovery: verify by hand that %s's canceled content is absent or superseded on "+
			"the lane, then %s", wpID, attestTail(wpID))
}

func mixedLaneRefusal(laneID, wpID string, u *unattributable) string {
	return fmt.Sprintf("mixed lane %s: canceled %s cannot be attributed — %s. %s.", laneID, wpID, u.detail, mixedLaneRecovery(u.reason, wpID))
}

func refuseMergedIndependentChange(e canceledPath) string {
	return fmt.Sprintf("mixed lane %s: canceled %s's change to '%s' was merged with an "+
		"independent change; the gate cannot prove it is absent. Recovery: revert %s's change to "+
		"'%s' on the lane through a surviving WP's governed work, %s; or, after "+
		"verifying by hand that none of %s's canceled change remains in '%s', "+
		"%s.", e.laneID, e.wpID, e.path, e.wpID, e.path, recoveryTail, e.wpID, e.path, attestTail(e.wpID))
}

func describeCanceledContent(e canceledPath) string {
	who := "canceled " + e.wpID
	recovery := fmt.Sprintf("revert %s's change to '%s' on the lane through a surviving WP's governed work, %s", e.wpID, e.path, recoveryTail)
	var situation string
	if !e.canceled.present {
		situation = fmt.Sprintf("file '%s' was deleted by %s (lane %s) and that deletion is on the target — approved content would be lost", e.path, who, e.laneID)
	} else {
		situation = fmt.Sprintf("file '%s' carries %s's change (lane %s) on the target — canceled work would ship", e.path, who, e.laneID)
	}
	return situation + "; " + recovery
}

func describeCanceledReachable(e canceledDependencyContent) string {
	who := "canceled " + strings.Join(e.wpIDs, ", ")
	effect := "its change"
	if !e.state.present {
		effect = "its deletion"
	}
	situation := fmt.Sprintf("%s: file '%s' carries %s's work (lane %s), which reached "+
		"approved lane %s through a dependency lane, and %s IS on the target — canceled work would ship",
		canceledReachableCode, e.path, who, e.canceledLane, e.carrierLane, effect)
	recovery := fmt.Sprintf("undo %s to '%s' on lane %s through a surviving WP's governed work "+
		"(do not rebuild the lane, and no attestation lifts this while the content ships), %s", effect, e.path, e.carrierLane, recoveryTail)
	return situation + "; " + recovery
}

// isCanceledSupersededAttestation / attestationStamps (canceled_attestation).
func isCanceledSupersededAttestation(e *status.Event) bool {
	rs := ""
	if e.ReasonSource != nil {
		rs = *e.ReasonSource
	}
	return e.From == status.Canceled && e.To == status.Canceled && rs == operatorReasonSrc &&
		e.PolicyMetadata != nil && e.PolicyMetadata.GetString(attestationKey) == canceledSuperseded && !isMigration(e)
}

func attestationStamps(events []*status.Event) map[string]string {
	out := map[string]string{}
	for _, e := range events {
		if isCanceledSupersededAttestation(e) {
			out[e.WPID] = stampOf(e)
		} else if _, ok := out[e.WPID]; ok && !isMigration(e) {
			delete(out, e.WPID)
		}
	}
	return out
}

func fullyCanceledLaneOwnCommits(root string, m *lanes.Manifest, excluded map[string]bool, base, targetBase string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, l := range m.Lanes {
		if isPlanningLane(l) || !laneFullyCanceled(l, excluded) {
			continue
		}
		tip := laneTipCommits(root, base, laneBranchFor(m, l.LaneID))
		out[l.LaneID] = laneOwnCommits(root, base, tip, closedWorldAnchors(m, l, targetBase, excluded))
	}
	return out
}

func liveDependencies(m *lanes.Manifest, excluded map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, l := range m.Lanes {
		if isPlanningLane(l) || laneFullyCanceled(l, excluded) {
			continue
		}
		for _, d := range dependencyLaneIDs(m, l) {
			out[d] = true
		}
	}
	return out
}

// unreadableCanceledDependencyRefusal is _unreadable_canceled_dependency_lanes
// rendered (""= none).
func unreadableCanceledDependencyRefusal(root string, m *lanes.Manifest, excluded map[string]bool, base string, events []*status.Event, eventsOK bool) string {
	live := liveDependencies(m, excluded)
	sorted := append([]lanes.Lane(nil), m.Lanes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].LaneID < sorted[j].LaneID })
	var clauses []string
	for _, l := range sorted {
		if !live[l.LaneID] || isPlanningLane(l) || !laneFullyCanceled(l, excluded) {
			continue
		}
		branch := laneBranchFor(m, l.LaneID)
		refuse := false
		if !branchExists(root, branch) {
			refuse = true
		} else if _, err := commitsInRange(root, base, branch); err != nil {
			refuse = true
		}
		if !refuse {
			continue
		}
		clauses = append(clauses, fmt.Sprintf("fully-canceled lane %s (branch '%s') is a dependency of a live lane but its commits cannot be read "+
			"(the branch is missing or its history is unreadable): its canceled content, which the dependent lane inherited, "+
			"cannot be told apart from approved work. Recovery: restore '%s' (for example "+
			"`git branch %s refs/spec-kitty/lane-tip/%s` when a lane-tip ref was recorded, or from `git reflog`), "+
			"then re-run `spec-kitty consolidate`; if the branch was rebuilt, run `spec-kitty consolidate --abort` before re-running",
			l.LaneID, branch, branch, branch, branch))
	}
	return strings.Join(clauses, "; ")
}

type canceledDependencyResolution struct {
	unapproved map[string]bool
	content    []canceledDependencyContent
	refusal    string
}

// resolveCanceledDependencyLanes is _resolve_canceled_dependency_lanes.
func resolveCanceledDependencyLanes(in claimInput, wps map[string]string, prefix string, events []*status.Event, eventsOK bool) (canceledDependencyResolution, error) {
	m := in.m
	ownBy := fullyCanceledLaneOwnCommits(in.root, m, in.excluded, in.coordBase, in.windowBase)
	own := map[string]bool{}
	for _, s := range ownBy {
		for c := range s {
			own[c] = true
		}
	}
	res := canceledDependencyResolution{unapproved: own}
	if len(own) == 0 {
		return res, nil
	}
	carriers := map[string][]string{}
	var carrierOrder []string
	for _, l := range m.Lanes {
		if !laneIsApproved(l, wps) || laneFullyCanceled(l, in.excluded) {
			continue
		}
		base, branch := in.laneRange(l)
		spine := laneFirstParentSpine(in.root, base, branch)
		for _, s := range spine {
			if own[s] {
				carriers[l.LaneID] = spine
				carrierOrder = append(carrierOrder, l.LaneID)
				break
			}
		}
	}
	if len(carriers) == 0 {
		return res, nil
	}
	// unstamped carried refusal
	var laneIDs []string
	for id := range ownBy {
		laneIDs = append(laneIDs, id)
	}
	sort.Strings(laneIDs)
	wpsOf := map[string][]string{}
	for _, l := range m.Lanes {
		ids := append([]string(nil), l.WPIDs...)
		sort.Strings(ids)
		wpsOf[l.LaneID] = ids
	}
	type carried struct {
		lane     string
		carriers []string
	}
	var carriedList []carried
	for _, id := range laneIDs {
		var cs []string
		for cid, spine := range carriers {
			for _, s := range spine {
				if ownBy[id][s] {
					cs = append(cs, cid)
					break
				}
			}
		}
		sort.Strings(cs)
		if len(cs) > 0 {
			carriedList = append(carriedList, carried{id, cs})
		}
	}
	carriedRefusal := func(laneID, wpID string, carrierIDs []string, u *unattributable) string {
		return fmt.Sprintf("canceled %s (lane %s) is carried by approved lane(s) %s but cannot be attributed — %s. %s.",
			wpID, laneID, strings.Join(carrierIDs, ", "), u.detail, mixedLaneRecovery(u.reason, wpID))
	}
	if !eventsOK {
		c := carriedList[0]
		w := wpsOf[c.lane][0]
		res.refusal = carriedRefusal(c.lane, w, c.carriers, unattributableFor(reasonEventsUnreadable, c.lane, w))
		return res, nil
	}
	liftable := attestationStamps(events)
	var refusals []string
	for _, c := range carriedList {
		for _, w := range wpsOf[c.lane] {
			if _, ok := liftable[w]; ok {
				continue
			}
			if lacksLaneHeadStamps(events, w) {
				refusals = append(refusals, carriedRefusal(c.lane, w, c.carriers, unattributableFor(reasonNoStamp, c.lane, w)))
			}
		}
	}
	if len(refusals) > 0 {
		res.refusal = strings.Join(refusals, "\n")
		return res, nil
	}
	isBK := func(p string) bool { return isBookkeepingPath(p, m.MissionSlug, prefix) }
	laneOf := map[string]string{}
	for id, s := range ownBy {
		for c := range s {
			laneOf[c] = id
		}
	}
	superseded, liveCommits := map[string]bool{}, map[string]bool{}

	for _, cid := range carrierOrder {
		spine := carriers[cid]
		sup, live, order, err := canceledSpineContent(in.root, spine, own, isBK)
		if err != nil {
			for _, s := range spine {
				if own[s] {
					liveCommits[s] = true
				}
			}
			continue
		}
		for s := range sup {
			superseded[s] = true
		}
		for _, s := range spine {
			if own[s] && !sup[s] {
				liveCommits[s] = true
			}
		}
		for _, p := range order {
			v := live[p]
			sha := v[0].(string)
			res.content = append(res.content, canceledDependencyContent{wpsOf[laneOf[sha]], laneOf[sha], cid, p, v[1].(pstate)})
		}
	}
	res.unapproved = map[string]bool{}
	for c := range own {
		if !(superseded[c] && !liveCommits[c]) {
			res.unapproved[c] = true
		}
	}
	return res, nil
}

// mixedLanes is _mixed_lanes.
func mixedLanes(m *lanes.Manifest, wps map[string]string, excluded map[string]bool) []lanes.Lane {
	var out []lanes.Lane
	for _, l := range m.Lanes {
		hasC := false
		for _, id := range l.WPIDs {
			if excluded[id] {
				hasC = true
			}
		}
		if laneIsApproved(l, wps) && hasC && !laneFullyCanceled(l, excluded) {
			out = append(out, l)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LaneID < out[j].LaneID })
	return out
}

// resolveMixedLanes is _resolve_mixed_lane_canceled_content.
func resolveMixedLanes(in claimInput, wps map[string]string, prefix string, events []*status.Event, eventsOK bool, canceledLaneCommits map[string]bool) ([]canceledPath, map[string]bool, string) {
	mixed := mixedLanes(in.m, wps, in.excluded)
	if len(mixed) == 0 {
		return nil, nil, ""
	}
	if !eventsOK {
		l := mixed[0]
		ids := append([]string(nil), l.WPIDs...)
		sort.Strings(ids)
		for _, id := range ids {
			if in.excluded[id] {
				return nil, nil, mixedLaneRefusal(l.LaneID, id, unattributableFor(reasonEventsUnreadable, l.LaneID, id))
			}
		}
	}
	stamps := attestationStamps(events)
	isBK := func(p string) bool { return isBookkeepingPath(p, in.m.MissionSlug, prefix) }
	var content []canceledPath
	var refusals []string
	for _, l := range mixed {
		base, branch := in.coordBase, laneBranchFor(in.m, l.LaneID)
		if isPlanningLane(l) {
			if b, br, ok := singleBranchWindow(in.root, in.m); ok {
				base, branch = b, br
			}
		}
		var canceled []string
		for _, id := range l.WPIDs {
			if in.excluded[id] {
				canceled = append(canceled, id)
			}
		}
		sort.Strings(canceled)
		anchors := closedWorldAnchors(in.m, l, in.windowBase, in.excluded)
		for _, id := range canceled {
			if s := stamps[id]; s != "" {
				anchors = append(anchors, s)
			}
		}
		for _, id := range canceled {
			cp, u := resolveCanceledWP(in.root, events, l.LaneID, l.WPIDs, id, branch, base, isBK, anchors, canceledLaneCommits)
			if u == nil {
				content = append(content, cp...)
				continue
			}
			if _, ok := stamps[id]; ok && overridableReasons[u.reason] {
				continue
			}
			refusals = append(refusals, mixedLaneRefusal(l.LaneID, id, u))
		}
	}
	if len(refusals) > 0 {
		return nil, nil, strings.Join(refusals, "\n")
	}
	attested := map[string]bool{}
	for id := range stamps {
		attested[id] = true
	}
	return content, attested, ""
}

// compoundRefusal is _compound_refusal.
func compoundRefusal(first string, bound func() (string, error), eventsOK bool) string {
	if !eventsOK {
		return first
	}
	r, err := bound()
	if err != nil || r == "" {
		return first
	}
	return first + "\n\n" + alsoHas + "\n" + r + "\n\n" + bothNeeded
}
