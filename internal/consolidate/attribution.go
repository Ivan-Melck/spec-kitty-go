package consolidate

// Per-WP commit attribution inside a lane (consolidation.wp_attribution):
// work windows reconstructed from the status log's lane_head stamps, the
// canceled-with-provenance WP resolution of a mixed lane, and what a
// fully-canceled dependency lane leaves on an approved carrier lane.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Unattributable reasons (UnattributableReason).
const (
	reasonNoStamp             = "no_stamp"
	reasonOpenWindow          = "open_window"
	reasonStampNotAncestor    = "stamp_not_ancestor_of_lane_tip"
	reasonContestedCommit     = "contested_commit"
	reasonEventsUnreadable    = "events_unreadable"
	reasonSpineUnreadable     = "spine_unreadable"
	reasonCommitOutside       = "commit_outside_windows"
	reasonCanceledLaneContent = "canceled_lane_content"
)

// overridableReasons is canceled_attestation.OVERRIDABLE_REASONS.
var overridableReasons = map[string]bool{reasonNoStamp: true, reasonOpenWindow: true, reasonStampNotAncestor: true, reasonContestedCommit: true}

var detailTemplates = map[string]string{
	reasonNoStamp: "{wp_id} in {lane_id} entered implementation but its lifecycle events carry no " +
		"commit attribution (missions created before this change, or a transition made " +
		"outside the governed workflow)",
	reasonOpenWindow: "{wp_id} in {lane_id} has a work window that never closed before it was canceled; its commit set cannot be bounded",
	reasonStampNotAncestor: "{wp_id} in {lane_id} carries a lane-head stamp that is not an ancestor of the " +
		"lane's current tip (the lane's history was rewritten after the stamp was recorded)",
	reasonContestedCommit: "{wp_id} in {lane_id} shares commit(s) ({commits}) with another WP's work window in " +
		"the same lane — lane WPs ran concurrently, so per-WP attribution cannot be resolved",
	reasonEventsUnreadable: "{wp_id} in {lane_id}: the mission's lifecycle event log could not be read",
	reasonSpineUnreadable:  "{wp_id} in {lane_id}: the lane's git history could not be read to resolve commit attribution",
	reasonCommitOutside: "{lane_id} carries commit(s) {commits} (touching '{path}') that lie outside every WP's " +
		"recorded work window — no governed WP owns them, so canceled {wp_id}'s content " +
		"cannot be told apart from approved work",
	reasonCanceledLaneContent: "{lane_id} carries commit(s) {commits} (touching '{path}') that a fully-canceled lane " +
		"authored and {lane_id} inherited — canceled work that must not ship (found while " +
		"attributing canceled {wp_id})",
}

func shortShas(commits []string) string {
	if len(commits) == 0 {
		return "(none recorded)"
	}
	var s []string
	for i, c := range commits {
		if i == 3 {
			break
		}
		s = append(s, short(c, 10))
	}
	out := strings.Join(s, ", ")
	if rest := len(commits) - 3; rest > 0 {
		out += fmt.Sprintf(" and %d more", rest)
	}
	return out
}

func detailFor(reason, laneID, wpID string, commits []string, path string) string {
	if path == "" {
		path = "(unknown path)"
	}
	r := strings.NewReplacer("{wp_id}", wpID, "{lane_id}", laneID, "{commits}", shortShas(commits), "{path}", path)
	return r.Replace(detailTemplates[reason])
}

// unattributable is Unattributable.
type unattributable struct{ reason, detail string }

func unattributableFor(reason, laneID, wpID string) *unattributable {
	return &unattributable{reason, detailFor(reason, laneID, wpID, nil, "")}
}

// canceledPath is CanceledPathState.
type canceledPath struct {
	wpID, laneID, path string
	canceled, pre      pstate
	preStateBySurvivor bool
}

func isMigration(e *status.Event) bool {
	return strings.HasPrefix(e.Actor.Identity(), status.MigrationActorPrefix)
}

const (
	kindImpl   = "implementation"
	kindReview = "review"
)

func windowClass(lane status.Lane) string {
	switch lane {
	case "claimed", "in_progress", "blocked":
		return kindImpl
	case "for_review", "in_review":
		return kindReview
	}
	return ""
}

func enteredImplementation(events []*status.Event, wpID string) bool {
	for _, e := range events {
		if e.WPID == wpID && (e.To == "claimed" || e.To == "in_progress") && !isMigration(e) {
			return true
		}
	}
	return false
}

type workWindow struct {
	wpID, kind          string
	openHead, closeHead string
	stillOpen           bool
}

// windows is _windows.
func windows(events []*status.Event, wpIDs map[string]bool) map[string][]workWindow {
	type open struct{ kind, head string }
	opened := map[string]*open{}
	out := map[string][]workWindow{}
	for id := range wpIDs {
		out[id] = nil
	}
	for _, e := range events {
		if !wpIDs[e.WPID] || isMigration(e) {
			continue
		}
		to := windowClass(e.To)
		stamp := stampOf(e)
		if cur := opened[e.WPID]; cur != nil {
			if to == cur.kind {
				continue
			}
			out[e.WPID] = append(out[e.WPID], workWindow{e.WPID, cur.kind, cur.head, stamp, false})
			delete(opened, e.WPID)
		}
		if to != "" {
			opened[e.WPID] = &open{to, stamp}
		}
	}
	for id, o := range opened {
		out[id] = append(out[id], workWindow{id, o.kind, o.head, "", true})
	}
	return out
}

// lacksLaneHeadStamps is lacks_lane_head_stamps.
func lacksLaneHeadStamps(events []*status.Event, wpID string) bool {
	if !enteredImplementation(events, wpID) {
		return false
	}
	for _, w := range windows(events, map[string]bool{wpID: true})[wpID] {
		if !w.stillOpen && (w.openHead == "" || w.closeHead == "") {
			return true
		}
	}
	return false
}

type mergeCache map[string]bool

func (mc mergeCache) isMerge(root, sha string) (bool, error) {
	if v, ok := mc[sha]; ok {
		return v, nil
	}
	m, err := isMergeCommit(root, sha)
	if err != nil {
		return false, err
	}
	mc[sha] = m
	return m, nil
}

func windowCommits(root string, w workWindow, branch string, spine map[string]bool, mc mergeCache) (map[string]bool, string) {
	if w.stillOpen {
		return nil, reasonOpenWindow
	}
	if w.openHead == "" || w.closeHead == "" {
		return nil, reasonNoStamp
	}
	if !shaReachableFrom(root, w.openHead, branch) || !shaReachableFrom(root, w.closeHead, branch) {
		return nil, reasonStampNotAncestor
	}
	raw, err := firstParentCommitsInRange(root, w.openHead, w.closeHead)
	if err != nil {
		return nil, reasonSpineUnreadable
	}
	out := map[string]bool{}
	for _, sha := range raw {
		if !spine[sha] {
			continue
		}
		m, err := mc.isMerge(root, sha)
		if err != nil {
			return nil, reasonSpineUnreadable
		}
		if !m {
			out[sha] = true
		}
	}
	return out, ""
}

func resolveWPWindows(root string, ws []workWindow, branch string, spine map[string]bool, mc mergeCache) (map[string]map[string]bool, string) {
	by := map[string]map[string]bool{kindImpl: {}, kindReview: {}}
	for _, w := range ws {
		cs, reason := windowCommits(root, w, branch, spine, mc)
		if reason != "" {
			return nil, reason
		}
		for c := range cs {
			by[w.kind][c] = true
		}
	}
	return by, ""
}

type contentStep struct {
	idx   int
	sha   string
	paths []string
}

func contentSteps(root string, spine []string, isBK func(string) bool, mc mergeCache) ([]contentStep, error) {
	var out []contentStep
	for i, sha := range spine {
		m, err := mc.isMerge(root, sha)
		if err != nil {
			return nil, err
		}
		if m {
			continue
		}
		paths, err := changedPathsOf(root, sha)
		if err != nil {
			return nil, err
		}
		var content []string
		for _, p := range paths {
			if !isBK(p) {
				content = append(content, p)
			}
		}
		out = append(out, contentStep{i, sha, content})
	}
	return out, nil
}

type spineWalk struct {
	canceled []canceledPath
	outside  [][2]string // (sha, first content path)
}

func canceledContentWalk(root string, spine []string, canceled, covered map[string]bool, isBK func(string) bool, wpID, laneID string, mc mergeCache) (*spineWalk, error) {
	steps, err := contentSteps(root, spine, isBK, mc)
	if err != nil {
		return nil, err
	}
	newest := map[string]string{}
	var order []string
	oldestCIdx := map[string]int{}
	oldestCSha := map[string]string{}
	oldestS := map[string]int{}
	w := &spineWalk{}
	for _, st := range steps {
		isC := canceled[st.sha]
		if len(st.paths) > 0 && !covered[st.sha] {
			w.outside = append(w.outside, [2]string{st.sha, st.paths[0]})
		}
		for _, p := range st.paths {
			if _, ok := newest[p]; !ok {
				newest[p] = st.sha
				order = append(order, p)
			}
			if isC {
				oldestCIdx[p] = st.idx
				oldestCSha[p] = st.sha
			} else {
				oldestS[p] = st.idx
			}
		}
	}
	for _, p := range order {
		toucher := newest[p]
		oc, ok := oldestCSha[p]
		if !ok {
			continue
		}
		if !canceled[toucher] {
			continue
		}
		cs, err := stateAt(root, toucher, p)
		if err != nil {
			return nil, err
		}
		pre, err := stateAt(root, oc+"^", p)
		if err != nil {
			return nil, err
		}
		if cs.key() == pre.key() {
			continue
		}
		bySurvivor := false
		if s, ok := oldestS[p]; ok && s > oldestCIdx[p] {
			bySurvivor = true
		}
		w.canceled = append(w.canceled, canceledPath{wpID, laneID, p, cs, pre, bySurvivor})
	}
	return w, nil
}

func outsideAfterAnchors(root, base string, outside [][2]string, anchors []string, neverExempt map[string]bool) [][2]string {
	if len(outside) == 0 {
		return outside
	}
	exempt := laneExemptCommits(root, base, anchors)
	var out [][2]string
	for _, o := range outside {
		if !exempt[o[0]] || neverExempt[o[0]] {
			out = append(out, o)
		}
	}
	return out
}

func closedWorldRefusal(w *spineWalk, laneID, wpID string, neverExempt map[string]bool) *unattributable {
	var inherited [][2]string
	for _, o := range w.outside {
		if neverExempt[o[0]] {
			inherited = append(inherited, o)
		}
	}
	reason, items := reasonCommitOutside, w.outside
	if len(inherited) > 0 {
		reason, items = reasonCanceledLaneContent, inherited
	}
	var shas []string
	for _, o := range items {
		shas = append(shas, o[0])
	}
	return &unattributable{reason, detailFor(reason, laneID, wpID, shas, items[0][1])}
}

// resolveCanceledWP is resolve_canceled_wp: (canceled content, nil) when
// attributed, else the unattributable outcome.
func resolveCanceledWP(root string, events []*status.Event, laneID string, laneWPs []string, canceledWP, branch, base string,
	isBK func(string) bool, anchors []string, neverExempt map[string]bool) ([]canceledPath, *unattributable) {
	ids := map[string]bool{canceledWP: true}
	for _, id := range laneWPs {
		ids[id] = true
	}
	entered := enteredImplementation(events, canceledWP)
	var siblings []string
	for id := range ids {
		if id != canceledWP {
			siblings = append(siblings, id)
		}
	}
	sort.Strings(siblings)
	anyEntered := false
	for _, s := range siblings {
		if enteredImplementation(events, s) {
			anyEntered = true
		}
	}
	if !entered && !anyEntered {
		return nil, nil
	}
	spine, err := firstParentCommitsInRange(root, base, branch)
	if err != nil {
		return nil, unattributableFor(reasonSpineUnreadable, laneID, canceledWP)
	}
	spineSet := map[string]bool{}
	for _, s := range spine {
		spineSet[s] = true
	}
	ws := windows(events, ids)
	mc := mergeCache{}
	canceledBy := map[string]map[string]bool{kindImpl: {}, kindReview: {}}
	if entered {
		by, reason := resolveWPWindows(root, ws[canceledWP], branch, spineSet, mc)
		if reason != "" {
			return nil, unattributableFor(reason, laneID, canceledWP)
		}
		canceledBy = by
	}
	sibImpl, sibReview := map[string]bool{}, map[string]bool{}
	for _, s := range siblings {
		if !enteredImplementation(events, s) {
			continue
		}
		by, reason := resolveWPWindows(root, ws[s], branch, spineSet, mc)
		if reason != "" {
			if !entered {
				return nil, nil
			}
			return nil, unattributableFor(reason, laneID, s)
		}
		for c := range by[kindImpl] {
			sibImpl[c] = true
		}
		for c := range by[kindReview] {
			sibReview[c] = true
		}
	}
	var contested []string
	for c := range canceledBy[kindImpl] {
		if sibImpl[c] {
			contested = append(contested, c)
		}
	}
	for c := range canceledBy[kindReview] {
		if (sibImpl[c] || sibReview[c]) && !contains(contested, c) {
			contested = append(contested, c)
		}
	}
	if len(contested) > 0 {
		sort.Strings(contested)
		return nil, &unattributable{reasonContestedCommit, detailFor(reasonContestedCommit, laneID, canceledWP, contested, "")}
	}
	canceled := map[string]bool{}
	for c := range canceledBy[kindImpl] {
		canceled[c] = true
	}
	for c := range canceledBy[kindReview] {
		canceled[c] = true
	}
	covered := map[string]bool{}
	for _, set := range []map[string]bool{canceled, sibImpl, sibReview} {
		for c := range set {
			covered[c] = true
		}
	}
	walk, err := canceledContentWalk(root, spine, canceled, covered, isBK, canceledWP, laneID, mc)
	if err != nil {
		return nil, unattributableFor(reasonSpineUnreadable, laneID, canceledWP)
	}
	if len(walk.outside) > 0 {
		var all []string
		var laneIDs []string
		for id := range ids {
			laneIDs = append(laneIDs, id)
		}
		if a := firstGovernedOpenStamp(events, laneIDs); a != "" {
			all = append(all, a)
		}
		all = append(all, anchors...)
		outside := outsideAfterAnchors(root, base, walk.outside, all, neverExempt)
		if len(outside) > 0 {
			return nil, closedWorldRefusal(&spineWalk{outside: outside}, laneID, canceledWP, neverExempt)
		}
	}
	return walk.canceled, nil
}

// canceledSpineContent is canceled_spine_content: (superseded canceled
// commits, live path -> (newest canceled toucher, final state)).
func canceledSpineContent(root string, spine []string, canceled map[string]bool, isBK func(string) bool) (map[string]bool, map[string][2]any, []string, error) {
	oldest := -1
	for i, s := range spine {
		if canceled[s] {
			oldest = i
		}
	}
	if oldest < 0 {
		return map[string]bool{}, map[string][2]any{}, nil, nil
	}
	steps, err := contentSteps(root, spine[:oldest+1], isBK, mergeCache{})
	if err != nil {
		return nil, nil, nil, err
	}
	survivor := map[string]bool{}
	superseded := map[string]bool{}
	newest := map[string]string{}
	var order []string
	for _, st := range steps {
		if !canceled[st.sha] {
			for _, p := range st.paths {
				survivor[p] = true
			}
			continue
		}
		var un []string
		for _, p := range st.paths {
			if !survivor[p] {
				un = append(un, p)
				continue
			}
		}
		if len(st.paths) > 0 && len(un) == 0 {
			superseded[st.sha] = true
		}
		for _, p := range un {
			if _, ok := newest[p]; !ok {
				newest[p] = st.sha
				order = append(order, p)
			}
		}
	}
	live := map[string][2]any{}
	for _, p := range order {
		s, err := stateAt(root, newest[p], p)
		if err != nil {
			return nil, nil, nil, err
		}
		live[p] = [2]any{newest[p], s}
	}
	return superseded, live, order, nil
}
