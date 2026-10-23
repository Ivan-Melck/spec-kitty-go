package consolidate

// The terminus reconciliation gate (consolidation.reconciliation): the
// approved-WP claim built once before any mutation, and the verifier that
// judges the landed target against it before any teardown.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

const (
	refuseWindowBaseUnresolved = "the excluded-content window base could not be resolved; the excluded/closed-world axes cannot be verified (fail-closed)"
	refuseEmptyAuthoredBlobs   = "the approved-authorship blob and deletion sets are both empty while approved WPs are claimed; " +
		"the squash content axis cannot attribute any target blob or deletion (fail-closed)"
	refuseNoAuthorshipAuthority = "no approved lane resolved any commits and the authored-blob set is empty, " +
		"but the squash window carries non-bookkeeping content; there is no " +
		"authorship authority to attribute it against (fail-closed)"
	restoreInProgress = "Every branch this consolidation moved (target, mission and coordination " +
		"branches) is being restored to its pre-consolidation commit; the rollback " +
		"report below names each branch, and a branch marked NOT restored still " +
		"carries this run's commits."
	approvedContentMissingCode = "APPROVED_CONTENT_MISSING"
	recoveryTail               = "then re-run spec-kitty consolidate"
)

var kittyOpsRootRecord = regexp.MustCompile(`^kitty-ops/[0-9A-HJKMNP-TV-Z]{26}\.jsonl$`)

// pstate is a path state: a blob, or absent (deleted).
type pstate struct {
	blob    string
	present bool
}

func (p pstate) key() string {
	if !p.present {
		return "\x00"
	}
	return p.blob
}

type wpShas struct {
	wp   string
	shas []string
}

type laneContribution struct{ laneID, commit, blob string }

// laneContent is ApprovedLaneContent.
type laneContent struct {
	laneID    string
	wpIDs     []string
	ancestors map[string]bool
	final     map[string]pstate
	tip       string
	forkPoint string
	depForks  map[string]string
}

// claim is ApprovedWpCommitSet (the production claim fields).
type claim struct {
	approved           []wpShas
	excludedShas       []string
	excludedPatchIDs   map[string]bool
	manifestWPs        int
	windowBase         string
	surfaceResolved    bool
	refusal            string
	enforce            bool
	authoredShas       map[string]bool
	authoredPatchIDs   map[string]bool
	authoredBlobs      map[[2]string]bool
	authoredDeletions  map[string]bool
	multiLane          map[string][2]laneContribution
	slug               string
	planningPrefix     string
	verifyReachability bool
	laneContent        []*laneContent
	boundLaneTips      [][2]string
	canceledContent    []canceledPath
	attestedCanceled   map[string]bool
	canceledDependency []canceledDependencyContent
}

func (c *claim) isBookkeeping(p string) bool { return isBookkeepingPath(p, c.slug, c.planningPrefix) }

// isBookkeepingPath is _is_bookkeeping.
func isBookkeepingPath(p, slug, prefix string) bool {
	n := strings.TrimRight(p, "/")
	if slug != "" {
		parts := strings.Split(n, "/")
		for i, s := range parts {
			if s == "kitty-specs" {
				if i+1 < len(parts) && parts[i+1] == slug {
					return true
				}
				break
			}
		}
	}
	if prefix != "" && (n == prefix || strings.HasPrefix(n, prefix+"/")) {
		return true
	}
	if n == ".kittify" || strings.HasPrefix(n, ".kittify/") {
		return true
	}
	if nestedAliasCoordinationFile(n, slug, prefix) {
		return true
	}
	return kittyOpsRootRecord.MatchString(n)
}

func nestedAliasCoordinationFile(p, slug, prefix string) bool {
	if prefix == "" {
		return false
	}
	pp := strings.Split(strings.TrimRight(prefix, "/"), "/")
	if len(pp) < 3 || pp[len(pp)-2] != "kitty-specs" {
		return false
	}
	for _, s := range pp[:len(pp)-2] {
		if s == "" {
			return false
		}
	}
	alias := pp[len(pp)-1]
	parts := strings.Split(p, "/")
	if alias == slug || len(parts) < 3 || parts[0] != "kitty-specs" || parts[1] != alias {
		return false
	}
	return mission.IsCoordResidue("kitty-specs/_mission_/" + strings.Join(parts[2:], "/"))
}

// claimIntegrityRefusal is claim_integrity_refusal.
func (c *claim) claimIntegrityRefusal() string {
	if c.refusal != "" {
		return c.refusal
	}
	if !c.surfaceResolved {
		return "coordination surface is unresolved or unmaterialized"
	}
	if len(c.approved) == 0 && c.manifestWPs > 0 {
		return fmt.Sprintf("derived claim is empty while the manifest lists %d WP(s)", c.manifestWPs)
	}
	return ""
}

// ---------------------------------------------------------------------------
// claim building

type claimInput struct {
	root, featureDir string
	m                *lanes.Manifest
	coordBase        string
	windowBase       string
	excluded         map[string]bool
}

func laneBranchFor(m *lanes.Manifest, laneID string) string {
	return lanes.LaneBranchName(m.MissionSlug, laneID, m.TargetBranch)
}

func isPlanningLane(l lanes.Lane) bool { return l.LaneID == lanes.PlanningLaneID }

func approvedLane(st string) bool { return st == "approved" || st == "done" }

func laneIsApproved(l lanes.Lane, wps map[string]string) bool {
	for _, id := range l.WPIDs {
		if approvedLane(wps[id]) {
			return true
		}
	}
	return false
}

func planningPrefixOf(root, featureDir string) string {
	r, err1 := filepath.EvalSymlinks(root)
	f, err2 := filepath.EvalSymlinks(featureDir)
	if err1 != nil {
		r = root
	}
	if err2 != nil {
		f = featureDir
	}
	rel, err := filepath.Rel(r, f)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return filepath.ToSlash(rel)
}

func dependencyLaneIDs(m *lanes.Manifest, l lanes.Lane) []string {
	byID := map[string]lanes.Lane{}
	for _, c := range m.Lanes {
		byID[c.LaneID] = c
	}
	seen := map[string]bool{}
	var out []string
	pending := append([]string(nil), l.DependsOnLanes...)
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[id] {
			continue
		}
		dep, ok := byID[id]
		if !ok {
			continue
		}
		seen[id] = true
		out = append(out, id)
		pending = append(pending, dep.DependsOnLanes...)
	}
	sort.Strings(out)
	return out
}

func closedWorldAnchors(m *lanes.Manifest, l lanes.Lane, targetBase string, excluded map[string]bool) []string {
	byID := map[string]lanes.Lane{}
	for _, c := range m.Lanes {
		byID[c.LaneID] = c
	}
	var anchors []string
	for _, dep := range dependencyLaneIDs(m, l) {
		d := byID[dep]
		if !isPlanningLane(d) && !laneFullyCanceled(d, excluded) {
			anchors = append(anchors, laneBranchFor(m, dep))
		}
	}
	if targetBase != "" {
		anchors = append(anchors, targetBase)
	}
	return anchors
}

func boundApprovedWPs(l lanes.Lane, wps map[string]string, excluded map[string]bool) []string {
	var out []string
	for _, id := range l.WPIDs {
		if !excluded[id] && approvedLane(wps[id]) {
			out = append(out, id)
		}
	}
	return out
}

func boundLanes(m *lanes.Manifest, wps map[string]string, excluded map[string]bool) []lanes.Lane {
	var out []lanes.Lane
	for _, l := range m.Lanes {
		if !isPlanningLane(l) && !laneFullyCanceled(l, excluded) && len(boundApprovedWPs(l, wps, excluded)) > 0 {
			out = append(out, l)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LaneID < out[j].LaneID })
	return out
}

func boundClaimBase(root, windowBase, coordBase string) string {
	if windowBase != "" && resolvesCommit(root, windowBase) {
		return windowBase
	}
	return coordBase
}

// boundVerdict is _approved_bound_verdict: a refusal text, or the lane tips
// the check validated.
func boundVerdict(in claimInput, wps map[string]string, events []*status.Event, eventsOK bool, prefix string) (string, [][2]string, error) {
	bl := boundLanes(in.m, wps, in.excluded)
	if len(bl) == 0 {
		return "", nil, nil
	}
	if !eventsOK {
		return fmt.Sprintf("the status event log could not be read, so the approvals of lane %s cannot be bounded to what was reviewed. "+
			"Recovery: repair or restore status.events.jsonl, then re-run", bl[0].LaneID), nil, nil
	}
	base := boundClaimBase(in.root, in.windowBase, in.coordBase)
	if !resolvesCommit(in.root, base) {
		return fmt.Sprintf("the commit the approved lanes are measured from ('%s') does not resolve, so their approvals cannot be bounded to what was reviewed. "+
			"Recovery: restore that branch or commit, then re-run", base), nil, nil
	}
	var stampAnchors []string
	for _, l := range bl {
		for _, id := range boundApprovedWPs(l, wps, in.excluded) {
			if s := approvalStamp(events, id); s != "" {
				stampAnchors = append(stampAnchors, s)
			}
		}
	}
	isBK := func(p string) bool { return isBookkeepingPath(p, in.m.MissionSlug, prefix) }
	var refusals []BoundRefusal
	var tips [][2]string
	for _, l := range bl {
		branch := laneBranchFor(in.m, l.LaneID)
		tip, err := resolveCommit(in.root, branch)
		if err != nil {
			continue
		}
		anchors := append(closedWorldAnchors(in.m, l, in.windowBase, in.excluded), stampAnchors...)
		found, err := checkLane(in.root, events, l.LaneID, branch, boundApprovedWPs(l, wps, in.excluded), base, anchors, isBK, tip)
		if err != nil {
			return "", nil, err
		}
		if found != nil {
			refusals = append(refusals, *found)
		}
		tips = append(tips, [2]string{branch, tip})
	}
	if len(refusals) > 0 {
		return RenderBoundRefusals(refusals, in.m.MissionSlug), nil, nil
	}
	return "", tips, nil
}

func snapshotLanes(dir string) (map[string]string, error) {
	snap, err := status.ReduceDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, id := range snap.WorkPackages.Keys() {
		if st := snap.WP(id); st != nil {
			out[id] = st.GetString("lane")
		}
	}
	return out, nil
}

func readEvents(dir string) ([]*status.Event, bool) {
	stream, err := status.ReadStream(dir, "")
	if err != nil {
		return nil, false
	}
	return stream.Transitions, true
}

// ApprovedBoundRefusal is approved_bound_refusal (orchestrator-api
// consolidate-mission): the refusal text when a code lane holds content
// beyond what review approved, "" otherwise.
func ApprovedBoundRefusal(root, featureDir string, m *lanes.Manifest, coordBase, windowBase string, excluded map[string]bool) (string, error) {
	wps, err := snapshotLanes(featureDir)
	if err != nil {
		return "", err
	}
	events, ok := readEvents(featureDir)
	in := claimInput{root: root, featureDir: featureDir, m: m, coordBase: coordBase, windowBase: windowBase, excluded: excluded}
	refusal, _, err := boundVerdict(in, wps, events, ok, planningPrefixOf(root, featureDir))
	return refusal, err
}

func laneTipCommits(root, base, branch string) []string {
	cs, err := commitsInRange(root, base, branch)
	if err != nil {
		return nil
	}
	return cs
}

func laneFirstParentSpine(root, base, branch string) []string {
	cs, err := firstParentCommitsInRange(root, base, branch)
	if err != nil {
		return nil
	}
	return cs
}

// singleBranchWindow is authorship_window: (fork point, mission branch) for
// a protected single_branch repo-root lane.
func singleBranchWindow(root string, m *lanes.Manifest) (string, string, bool) {
	if m.MissionBranch == m.TargetBranch {
		return "", "", false
	}
	meta := mission.ReadMetaOrEmpty(filepath.Join(root, "kitty-specs", m.MissionSlug))
	if meta.GetString("topology") != "single_branch" || !coord.IsProtected(root, m.MissionSlug, m.TargetBranch) {
		return "", "", false
	}
	res := git(root, "merge-base", m.TargetBranch, m.MissionBranch)
	base := strings.TrimSpace(res.Stdout)
	if res.Code != 0 || base == "" {
		return "", "", false
	}
	return base, m.MissionBranch, true
}

func (in claimInput) laneRange(l lanes.Lane) (string, string) {
	if isPlanningLane(l) {
		if base, branch, ok := singleBranchWindow(in.root, in.m); ok {
			return base, branch
		}
	}
	return in.coordBase, laneBranchFor(in.m, l.LaneID)
}

func laneOwnCommits(root, base string, laneCommits []string, anchors []string) map[string]bool {
	exempt := laneExemptCommits(root, base, anchors)
	out := map[string]bool{}
	for _, c := range laneCommits {
		if !exempt[c] {
			out[c] = true
		}
	}
	return out
}

func fullyCanceledLaneCommits(root string, m *lanes.Manifest, excluded map[string]bool, coordBase, targetBase string) map[string]bool {
	out := map[string]bool{}
	for _, l := range m.Lanes {
		if isPlanningLane(l) || !laneFullyCanceled(l, excluded) {
			continue
		}
		tip := laneTipCommits(root, coordBase, laneBranchFor(m, l.LaneID))
		for c := range laneOwnCommits(root, coordBase, tip, closedWorldAnchors(m, l, targetBase, excluded)) {
			out[c] = true
		}
	}
	return out
}

// firstGovernedOpenStamp is first_governed_open_stamp.
func firstGovernedOpenStamp(events []*status.Event, wpIDs []string) string {
	ids := map[string]bool{}
	for _, id := range wpIDs {
		ids[id] = true
	}
	for _, e := range events {
		if !ids[e.WPID] || strings.HasPrefix(e.Actor.Identity(), status.MigrationActorPrefix) {
			continue
		}
		switch e.To {
		case "claimed", "in_progress", "blocked":
			if s := stampOf(e); s != "" {
				return s
			}
		}
	}
	return ""
}

type presenceScope struct {
	base      string
	carried   map[string]bool
	carriedOK bool
	laneBases map[string]string
	canceled  map[string]bool
}

// finalAuthoredWalk is _final_authored_walk.
func finalAuthoredWalk(root string, shas []string) (map[[2]string]bool, map[string]bool, error) {
	seen := map[string]bool{}
	blobs := map[[2]string]bool{}
	dels := map[string]bool{}
	for _, sha := range shas {
		paths, err := changedPathsOf(root, sha)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range paths {
			if seen[p] {
				continue
			}
			seen[p] = true
			if b, err := blobIDAt(root, sha, p); err == nil {
				blobs[[2]string{p, b}] = true
			} else {
				dels[p] = true
			}
		}
	}
	return blobs, dels, nil
}

func dependencyForks(root string, m *lanes.Manifest, branch string, spine []string, ancestors []string) map[string]string {
	forks := map[string]string{}
	for _, dep := range ancestors {
		ahead, err := commitsInRange(root, laneBranchFor(m, dep), branch)
		if err != nil {
			continue
		}
		set := map[string]bool{}
		for _, a := range ahead {
			set[a] = true
		}
		for _, s := range spine {
			if !set[s] {
				forks[dep] = s
				break
			}
		}
	}
	return forks
}

func buildLaneContent(root string, m *lanes.Manifest, l lanes.Lane, wps map[string]string, spine []string, blobs map[[2]string]bool, dels map[string]bool) *laneContent {
	final := map[string]pstate{}
	for p := range dels {
		final[p] = pstate{}
	}
	for k := range blobs {
		final[k[0]] = pstate{k[1], true}
	}
	var approved []string
	for _, id := range l.WPIDs {
		if approvedLane(wps[id]) {
			approved = append(approved, id)
		}
	}
	anc := dependencyLaneIDs(m, l)
	ancSet := map[string]bool{}
	for _, a := range anc {
		ancSet[a] = true
	}
	lc := &laneContent{laneID: l.LaneID, wpIDs: approved, ancestors: ancSet, final: final,
		depForks: dependencyForks(root, m, laneBranchFor(m, l.LaneID), spine, anc)}
	if len(spine) > 0 {
		lc.tip = spine[0]
		lc.forkPoint = spine[len(spine)-1] + "^"
	}
	return lc
}

func presenceSpine(root string, ps *presenceScope, laneID, branch string) ([]string, error) {
	if !ps.carriedOK {
		return nil, probeErr("the commits after '%s' that the mission branch carries could not be read", ps.base)
	}
	var dropped map[string]bool
	if lb := ps.laneBases[laneID]; lb != "" {
		if before, err := commitsInRange(root, ps.base, lb); err == nil {
			dropped = map[string]bool{}
			for _, c := range before {
				if ps.carried[c] {
					dropped[c] = true
				}
			}
		}
	}
	if dropped == nil {
		dropped = ps.carried
	}
	var out []string
	for _, s := range laneFirstParentSpine(root, ps.base, branch) {
		if !dropped[s] {
			out = append(out, s)
		}
	}
	return out, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func without(xs []string, drop map[string]bool) []string {
	var out []string
	for _, x := range xs {
		if !drop[x] {
			out = append(out, x)
		}
	}
	return out
}

func orderLaneContent(cs []*laneContent) []*laneContent {
	remaining := map[string]*laneContent{}
	for _, c := range cs {
		remaining[c.laneID] = c
	}
	var ordered []*laneContent
	for len(remaining) > 0 {
		var ready []string
		for id, c := range remaining {
			blocked := false
			for a := range c.ancestors {
				if _, ok := remaining[a]; ok {
					blocked = true
				}
			}
			if !blocked {
				ready = append(ready, id)
			}
		}
		if len(ready) == 0 {
			for id := range remaining {
				ready = append(ready, id)
			}
		}
		sort.Strings(ready)
		for _, id := range ready {
			ordered = append(ordered, remaining[id])
			delete(remaining, id)
		}
	}
	return ordered
}

// buildClaim is build_approved_wp_set (without the mixed-lane and
// canceled-dependency resolutions of canceled-with-provenance work packages).
func buildClaim(in claimInput) (*claim, error) {
	m := in.m
	c := &claim{manifestWPs: 0, windowBase: in.windowBase, surfaceResolved: true, slug: m.MissionSlug,
		planningPrefix: planningPrefixOf(in.root, in.featureDir), verifyReachability: true}
	seenWP := map[string]bool{}
	for _, l := range m.Lanes {
		for _, id := range l.WPIDs {
			if !seenWP[id] {
				seenWP[id] = true
				c.manifestWPs++
			}
		}
	}
	wps, err := snapshotLanes(in.featureDir)
	if err != nil {
		c.surfaceResolved = false
		c.refusal = "coordination surface could not be materialized: " + err.Error()
		return c, nil
	}
	var unresolvable []string
	sorted := append([]lanes.Lane(nil), m.Lanes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].LaneID < sorted[j].LaneID })
	for _, l := range sorted {
		if isPlanningLane(l) || !laneIsApproved(l, wps) || laneFullyCanceled(l, in.excluded) {
			continue
		}
		b := laneBranchFor(m, l.LaneID)
		if !branchExists(in.root, b) {
			unresolvable = append(unresolvable, fmt.Sprintf("approved lane %s: created branch '%s' does not exist in git; "+
				"the lane's work cannot be attributed. Verify the lane worktree was allocated and "+
				"its branch was not deleted before the merge could attribute its commits.", l.LaneID, b))
		}
	}
	if len(unresolvable) > 0 {
		c.refusal = strings.Join(unresolvable, "; ")
		return c, nil
	}
	events, eventsOK := readEvents(in.featureDir)
	if r := unreadableCanceledDependencyRefusal(in.root, m, in.excluded, in.coordBase, events, eventsOK); r != "" {
		c.refusal = r
		return c, nil
	}
	dep, err := resolveCanceledDependencyLanes(in, wps, c.planningPrefix, events, eventsOK)
	if err != nil {
		return nil, err
	}
	boundOnly := func() (string, error) {
		r, _, err := boundVerdict(in, wps, events, eventsOK, c.planningPrefix)
		return r, err
	}
	if dep.refusal != "" {
		c.refusal = compoundRefusal(dep.refusal, boundOnly, eventsOK)
		return c, nil
	}
	canceledContent, attested, mixedRefusal := resolveMixedLanes(in, wps, c.planningPrefix, events, eventsOK, dep.unapproved)
	if mixedRefusal != "" {
		c.refusal = compoundRefusal(mixedRefusal, boundOnly, eventsOK)
		return c, nil
	}
	c.canceledContent, c.attestedCanceled, c.canceledDependency = canceledContent, attested, dep.content
	refusal, tips, err := boundVerdict(in, wps, events, eventsOK, c.planningPrefix)
	if err != nil {
		return nil, err
	}
	if refusal != "" {
		c.refusal = refusal
		return c, nil
	}
	// approved shas
	if refusal := singleBranchEvidenceRefusal(in.root, in.featureDir, m, wps, in.excluded); refusal != "" {
		c.refusal = refusal
		return c, nil
	}
	for _, l := range m.Lanes {
		base, branch := in.laneRange(l)
		for _, id := range l.WPIDs {
			if st, ok := wps[id]; ok && approvedLane(st) {
				c.approved = append(c.approved, wpShas{id, laneTipCommits(in.root, base, branch)})
			}
		}
	}
	// presence scope
	ps := &presenceScope{base: boundClaimBase(in.root, in.windowBase, in.coordBase), laneBases: map[string]string{}}
	if carried, err := commitsInRange(in.root, ps.base, in.coordBase); err == nil {
		ps.carriedOK = true
		ps.carried = map[string]bool{}
		for _, x := range carried {
			ps.carried[x] = true
		}
	}
	if eventsOK {
		for _, l := range m.Lanes {
			ps.laneBases[l.LaneID] = firstGovernedOpenStamp(events, l.WPIDs)
		}
	}
	ps.canceled = fullyCanceledLaneCommits(in.root, m, in.excluded, ps.base, ps.base)
	if !ps.carriedOK {
		for _, l := range m.Lanes {
			if laneIsApproved(l, wps) && !isPlanningLane(l) {
				c.refusal = fmt.Sprintf("the commits between '%s' and the mission branch ('%s') could not be read, "+
					"so the approved lanes' code cannot be checked for presence on the target. "+
					"Recovery: restore or repair that branch, then re-run", ps.base, in.coordBase)
				return c, nil
			}
		}
	}
	canceledLaneCommits := dep.unapproved
	// authored
	c.authoredShas, c.authoredPatchIDs = map[string]bool{}, map[string]bool{}
	c.authoredBlobs, c.authoredDeletions = map[[2]string]bool{}, map[string]bool{}
	contributions := map[string][]laneContribution{}
	var contents []*laneContent
	for _, l := range m.Lanes {
		if !laneIsApproved(l, wps) {
			continue
		}
		base, branch := in.laneRange(l)
		spine := laneFirstParentSpine(in.root, base, branch)
		full := without(spine, canceledLaneCommits)
		fullBlobs, fullDels, err := finalAuthoredWalk(in.root, full)
		if err != nil {
			return nil, err
		}
		if !isPlanningLane(l) {
			pspine, err := presenceSpine(in.root, ps, l.LaneID, branch)
			if err != nil {
				return nil, err
			}
			canc := map[string]bool{}
			for k := range canceledLaneCommits {
				canc[k] = true
			}
			for k := range ps.canceled {
				canc[k] = true
			}
			authored := without(pspine, canc)
			if equalStrings(pspine, spine) && equalStrings(authored, full) {
				contents = append(contents, buildLaneContent(in.root, m, l, wps, spine, fullBlobs, fullDels))
			} else {
				b, d, err := finalAuthoredWalk(in.root, authored)
				if err != nil {
					return nil, err
				}
				contents = append(contents, buildLaneContent(in.root, m, l, wps, pspine, b, d))
			}
		}
		authored := full
		laneBlobs, laneDels := fullBlobs, fullDels
		if !isPlanningLane(l) && len(l.DependsOnLanes) > 0 {
			if own := ps.laneBases[l.LaneID]; own != "" {
				ownSet := laneOwnCommits(in.root, base, full, []string{own})
				if len(ownSet) != len(full) {
					var kept []string
					for _, s := range full {
						if ownSet[s] {
							kept = append(kept, s)
						}
					}
					authored = kept
					if laneBlobs, laneDels, err = finalAuthoredWalk(in.root, authored); err != nil {
						return nil, err
					}
				}
			}
		}
		for _, s := range authored {
			c.authoredShas[s] = true
			pid, err := patchIDOf(in.root, s)
			if err != nil {
				return nil, err
			}
			if pid != "" {
				c.authoredPatchIDs[pid] = true
			}
		}
		for k := range laneBlobs {
			c.authoredBlobs[k] = true
		}
		for k := range laneDels {
			c.authoredDeletions[k] = true
		}
		if len(laneBlobs) > 0 && len(authored) > 0 {
			keys := make([][2]string, 0, len(laneBlobs))
			for k := range laneBlobs {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool { return keys[i][0] < keys[j][0] })
			for _, k := range keys {
				contributions[k[0]] = append(contributions[k[0]], laneContribution{l.LaneID, authored[0], k[1]})
			}
		}
	}
	c.multiLane = map[string][2]laneContribution{}
	for p, cs := range contributions {
		if len(cs) == 2 {
			c.multiLane[p] = [2]laneContribution{cs[0], cs[1]}
		}
	}
	c.laneContent = orderLaneContent(contents)
	// excluded
	c.excludedPatchIDs = map[string]bool{}
	exShas := map[string]bool{}
	for _, l := range m.Lanes {
		any := false
		for _, id := range l.WPIDs {
			if in.excluded[id] {
				any = true
			}
		}
		if !any {
			continue
		}
		for _, s := range laneTipCommits(in.root, in.coordBase, laneBranchFor(m, l.LaneID)) {
			pid, err := patchIDOf(in.root, s)
			if err != nil {
				return nil, err
			}
			if !c.authoredShas[s] {
				exShas[s] = true
			}
			if pid != "" && !c.authoredPatchIDs[pid] {
				c.excludedPatchIDs[pid] = true
			}
		}
	}
	for s := range exShas {
		c.excludedShas = append(c.excludedShas, s)
	}
	sort.Strings(c.excludedShas)
	c.enforce = true
	c.boundLaneTips = tips
	return c, nil
}

func branchExists(root, b string) bool {
	return git(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+b).Code == 0
}

// ---------------------------------------------------------------------------
// verification

type missingContent struct {
	wpIDs, laneIDs []string
	path           string
	expected       pstate
	found          string // "absent" | "unchanged"
}

type divergence struct {
	missingApproved   [][2]string
	reachableExcluded [][2]string
	unattrContent     [][2]string
	unattrBlobs       [][2]string
	unattrDeletions   []string
	canceledContent   []canceledPath
	contentMissing    []missingContent
	canceledReachable []canceledDependencyContent
}

func (d *divergence) empty() bool {
	return len(d.missingApproved)+len(d.reachableExcluded)+len(d.unattrContent)+len(d.unattrBlobs)+len(d.unattrDeletions)+
		len(d.canceledContent)+len(d.contentMissing)+len(d.canceledReachable) == 0
}

func short(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (d *divergence) describe() string {
	var parts []string
	for _, x := range d.missingApproved {
		parts = append(parts, fmt.Sprintf("approved %s commit %s is NOT reachable from the target — approved work would be dropped", x[0], short(x[1], 10)))
	}
	for _, x := range d.reachableExcluded {
		ident := short(x[0], 10)
		if x[0] == "" {
			ident = "patch-id " + short(x[1], 12)
		}
		parts = append(parts, fmt.Sprintf("excluded (canceled/removed) commit %s IS reachable from the target — removed code would ship", ident))
	}
	for _, x := range d.unattrContent {
		ident := short(x[0], 10)
		if x[0] == "" {
			ident = "patch-id " + short(x[1], 12)
		}
		parts = append(parts, fmt.Sprintf("content commit %s (patch-id %s) IS reachable from the target but belongs to NO approved WP — un-attributable (removed/canceled work would ship)", ident, short(x[1], 12)))
	}
	for _, x := range d.unattrBlobs {
		parts = append(parts, fmt.Sprintf("file '%s' (blob %s) IS reachable on the target but belongs to NO approved WP — un-attributable (removed/canceled work would ship)", x[0], short(x[1], 10)))
	}
	for _, p := range d.unattrDeletions {
		parts = append(parts, fmt.Sprintf("file '%s' was DELETED from the target but that deletion is NOT attributable to any approved WP's own authorship — un-attributable (data loss: approved content may have been silently removed)", p))
	}
	for _, e := range d.canceledContent {
		parts = append(parts, describeCanceledContent(e))
	}
	for _, e := range d.contentMissing {
		who, ls := strings.Join(e.wpIDs, ", "), strings.Join(e.laneIDs, ", ")
		var what string
		switch {
		case !e.expected.present:
			what = fmt.Sprintf("its deletion of '%s' is not applied to the target", e.path)
		case e.found == "absent":
			what = fmt.Sprintf("file '%s' is absent from the target", e.path)
		default:
			what = fmt.Sprintf("file '%s' on the target still holds its pre-consolidation content", e.path)
		}
		situation := fmt.Sprintf("%s: approved %s (lane %s) %s and no later approved WP superseded it — approved work would be dropped", approvedContentMissingCode, who, ls, what)
		recovery := fmt.Sprintf("restore %s's change to '%s' on the mission branch — the target branch did not change this path, so revert nothing there "+
			"(if you committed the staged changes of a lagging coordination or mission worktree, revert that commit on the mission branch, "+
			"and never commit such changes — discard them with `git reset --hard HEAD`), %s", who, e.path, recoveryTail)
		parts = append(parts, situation+"; "+recovery)
	}
	for _, e := range d.canceledReachable {
		parts = append(parts, describeCanceledReachable(e))
	}
	if len(parts) == 0 {
		return "no divergence"
	}
	return strings.Join(parts, "; ")
}

// verifyResult is VerifyResult.
type verifyResult struct {
	status  string // pass | fail | refuse
	div     *divergence
	refusal string
}

func passed() verifyResult              { return verifyResult{status: "pass"} }
func refused(r string) verifyResult     { return verifyResult{status: "refuse", refusal: r} }
func failed(d *divergence) verifyResult { return verifyResult{status: "fail", div: d} }

// guidance is VerifyResult.recovery_guidance.
func (v verifyResult) guidance() string {
	if v.status == "refuse" {
		reason := strings.TrimRight(v.refusal, ".")
		return fmt.Sprintf("Reconciliation refused (fail-closed): %s. %s no teardown ran. Resolve the issue above, then re-run the merge.", reason, restoreInProgress)
	}
	detail := "unknown divergence"
	if v.div != nil {
		detail = v.div.describe()
	}
	return fmt.Sprintf("Reconciliation FAILED: %s. %s nothing was torn down. Inspect the target branch and the lane tips, then re-run the merge.", strings.TrimRight(detail, "."), restoreInProgress)
}

func stateAt(root, ref, path string) (pstate, error) {
	b, ok, err := pathStateAt(root, ref, path)
	return pstate{b, ok}, err
}

// verify is MergeOutcomeVerifier.verify.
func (c *claim) verify(root, target string) verifyResult {
	if r := c.claimIntegrityRefusal(); r != "" {
		return refused(r)
	}
	canceledFail, cref := c.canceledContentDivergence(root, target)
	if cref != "" {
		return refused(cref)
	}
	missing, mref := c.approvedContentDivergence(root, target)
	if mref != "" {
		return refused(mref)
	}
	carried, dref := c.canceledDependencyDivergence(root, target)
	if dref != "" {
		return refused(dref)
	}
	var res verifyResult
	switch {
	case !c.verifyReachability:
		res = c.verifySquashContent(root, target)
	case c.enforce && c.windowBase == "":
		res = refused(refuseWindowBaseUnresolved)
	default:
		res = c.verifyMergeReachability(root, target)
	}
	if res.status == "refuse" || len(canceledFail)+len(missing)+len(carried) == 0 {
		return res
	}
	d := res.div
	if d == nil {
		d = &divergence{}
	}
	d.canceledContent = append(d.canceledContent, canceledFail...)
	d.contentMissing = append(d.contentMissing, missing...)
	d.canceledReachable = append(d.canceledReachable, carried...)
	return failed(d)
}

// canceledContentDivergence is _canceled_content_divergence (#5046).
func (c *claim) canceledContentDivergence(root, target string) ([]canceledPath, string) {
	if len(c.canceledContent) == 0 {
		return nil, ""
	}
	if c.windowBase == "" {
		return nil, refuseWindowBaseUnresolved
	}
	entries := append([]canceledPath(nil), c.canceledContent...)
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.laneID != b.laneID {
			return a.laneID < b.laneID
		}
		if a.wpID != b.wpID {
			return a.wpID < b.wpID
		}
		return a.path < b.path
	})
	var fail []canceledPath
	for _, e := range entries {
		ts, err := stateAt(root, target, e.path)
		if err != nil {
			return nil, "a git probe failed while verifying the canceled-content window: " + err.Error()
		}
		ws, err := stateAt(root, c.windowBase, e.path)
		if err != nil {
			return nil, "a git probe failed while verifying the canceled-content window: " + err.Error()
		}
		if ts.key() == e.canceled.key() {
			if ws.key() != e.canceled.key() || e.preStateBySurvivor {
				fail = append(fail, e)
			}
			continue
		}
		if ts.key() == e.pre.key() || ts.key() == ws.key() {
			continue
		}
		if c.attestedCanceled[e.wpID] {
			continue
		}
		return fail, refuseMergedIndependentChange(e)
	}
	return fail, ""
}

// canceledDependencyDivergence is _canceled_dependency_divergence (#5569/#5613).
func (c *claim) canceledDependencyDivergence(root, target string) ([]canceledDependencyContent, string) {
	if len(c.canceledDependency) == 0 {
		return nil, ""
	}
	if c.windowBase == "" {
		return nil, refuseWindowBaseUnresolved
	}
	entries := append([]canceledDependencyContent(nil), c.canceledDependency...)
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.carrierLane != b.carrierLane {
			return a.carrierLane < b.carrierLane
		}
		if a.canceledLane != b.canceledLane {
			return a.canceledLane < b.canceledLane
		}
		return a.path < b.path
	})
	var hits []canceledDependencyContent
	for _, e := range entries {
		ts, err := stateAt(root, target, e.path)
		if err != nil {
			return nil, "a git probe failed while verifying canceled dependency content: " + err.Error()
		}
		if ts.key() != e.state.key() {
			continue
		}
		ws, err := stateAt(root, c.windowBase, e.path)
		if err != nil {
			return nil, "a git probe failed while verifying canceled dependency content: " + err.Error()
		}
		if ws.key() != e.state.key() {
			hits = append(hits, e)
		}
	}
	return hits, ""
}

func (c *claim) verifyMergeReachability(root, target string) verifyResult {
	d := &divergence{}
	for _, a := range c.approved {
		for _, sha := range a.shas {
			if !shaReachableFrom(root, sha, target) {
				d.missingApproved = append(d.missingApproved, [2]string{a.wp, sha})
			}
		}
	}
	already := map[string]bool{}
	for _, sha := range c.excludedShas {
		if shaReachableFrom(root, sha, target) {
			pid, err := patchIDOf(root, sha)
			if err != nil {
				return refused("a git probe failed while verifying the merge window: " + err.Error())
			}
			d.reachableExcluded = append(d.reachableExcluded, [2]string{sha, pid})
			already[pid] = true
		}
	}
	if len(c.excludedPatchIDs) > 0 && c.windowBase != "" {
		window := map[string]bool{}
		cs, err := commitsInRange(root, c.windowBase, target)
		if err != nil {
			return refused("a git probe failed while verifying the merge window: " + err.Error())
		}
		for _, s := range cs {
			pid, err := patchIDOf(root, s)
			if err != nil {
				return refused("a git probe failed while verifying the merge window: " + err.Error())
			}
			if pid != "" {
				window[pid] = true
			}
		}
		var pids []string
		for p := range c.excludedPatchIDs {
			pids = append(pids, p)
		}
		sort.Strings(pids)
		for _, p := range pids {
			if window[p] && !already[p] {
				d.reachableExcluded = append(d.reachableExcluded, [2]string{"", p})
			}
		}
	}
	if c.enforce && c.windowBase != "" {
		cs, err := commitsInRange(root, c.windowBase, target)
		if err != nil {
			return refused("a git probe failed while verifying the merge window: " + err.Error())
		}
		for _, sha := range cs {
			pid, err := patchIDOf(root, sha)
			if err != nil {
				return refused("a git probe failed while verifying the merge window: " + err.Error())
			}
			if pid == "" {
				continue
			}
			paths, err := changedPathsOf(root, sha)
			if err != nil {
				return refused("a git probe failed while verifying the merge window: " + err.Error())
			}
			content := false
			for _, p := range paths {
				if !c.isBookkeeping(p) {
					content = true
				}
			}
			if !content || c.authoredShas[sha] || c.authoredPatchIDs[pid] {
				continue
			}
			d.unattrContent = append(d.unattrContent, [2]string{sha, pid})
		}
	}
	if d.empty() {
		return passed()
	}
	return failed(d)
}

func (c *claim) verifySquashContent(root, target string) verifyResult {
	if !c.enforce {
		return passed()
	}
	if len(c.authoredBlobs) == 0 && len(c.authoredDeletions) == 0 {
		for _, a := range c.approved {
			if len(a.shas) > 0 {
				return refused(refuseEmptyAuthoredBlobs)
			}
		}
		if c.windowBase == "" {
			return refused(refuseWindowBaseUnresolved)
		}
		entries, err := changedPathsInRange(root, c.windowBase, target)
		if err != nil {
			return refused("a git probe failed while verifying the squash window: " + err.Error())
		}
		for _, e := range entries {
			if c.isBookkeeping(e.path) {
				continue
			}
			if strings.HasPrefix(e.status, "D") && c.authoredDeletions[e.path] {
				continue
			}
			return refused(refuseNoAuthorshipAuthority)
		}
		return passed()
	}
	if c.windowBase == "" {
		return refused(refuseWindowBaseUnresolved)
	}
	entries, err := changedPathsInRange(root, c.windowBase, target)
	if err != nil {
		return refused("a git probe failed while verifying the squash window: " + err.Error())
	}
	d := &divergence{}
	for _, e := range entries {
		if c.isBookkeeping(e.path) {
			continue
		}
		if strings.HasPrefix(e.status, "D") {
			if !c.authoredDeletions[e.path] {
				d.unattrDeletions = append(d.unattrDeletions, e.path)
			}
			continue
		}
		blob, err := blobIDAt(root, target, e.path)
		if err != nil {
			return refused("a git probe failed while verifying the squash window: " + err.Error())
		}
		if c.authoredBlobs[[2]string{e.path, blob}] {
			continue
		}
		ok, err := c.legitimateThreeWay(root, e.path, blob)
		if err != nil {
			return refused("a git probe failed while verifying the squash window: " + err.Error())
		}
		if ok {
			continue
		}
		d.unattrBlobs = append(d.unattrBlobs, [2]string{e.path, blob})
	}
	if d.empty() {
		return passed()
	}
	return failed(d)
}

// legitimateThreeWay is is_legitimate_three_way_resolution.
func (c *claim) legitimateThreeWay(root, path, target string) (bool, error) {
	cs, ok := c.multiLane[path]
	if !ok || !mergeTreeAvailable(root) {
		return false, nil
	}
	for _, x := range cs {
		b, err := blobIDAt(root, x.commit, path)
		if err != nil {
			return false, err
		}
		if b != x.blob {
			return false, nil
		}
	}
	resolved, err := threeWayMergeBlob(root, cs[0].commit, cs[1].commit, path)
	if err != nil {
		return false, err
	}
	return resolved != "" && resolved == target, nil
}

// approvedContentDivergence is _approved_content_divergence (#5571).
func (c *claim) approvedContentDivergence(root, target string) ([]missingContent, string) {
	if !c.enforce || len(c.laneContent) == 0 {
		return nil, ""
	}
	if c.windowBase == "" {
		return nil, refuseWindowBaseUnresolved
	}
	var perr error
	at := func(ref, path string) pstate {
		s, err := stateAt(root, ref, path)
		if err != nil && perr == nil {
			perr = err
		}
		return s
	}
	missing := unmetApprovedContent(c.laneContent, func(p string) pstate { return at(target, p) },
		func(p string) pstate { return at(c.windowBase, p) }, func(p string) bool {
			for _, e := range c.canceledContent {
				if e.path == p {
					return true
				}
			}
			return c.isBookkeeping(p)
		}, at)
	if perr != nil {
		return nil, "a git probe failed while verifying approved content presence: " + perr.Error()
	}
	return missing, ""
}

func unsuperseded(holders []*laneContent) []*laneContent {
	var out []*laneContent
	for _, h := range holders {
		sup := false
		for _, o := range holders {
			if o != h && o.ancestors[h.laneID] {
				sup = true
			}
		}
		if !sup {
			out = append(out, h)
		}
	}
	return out
}

func stateSet(holders []*laneContent, path string) map[string]pstate {
	out := map[string]pstate{}
	for _, h := range holders {
		s := h.final[path]
		out[s.key()] = s
	}
	return out
}

func startedFrom(path string, holders []*laneContent, base func() pstate, at func(string, string) pstate) map[string]bool {
	out := map[string]bool{base().key(): true}
	for _, h := range holders {
		for _, fork := range h.depForks {
			out[at(fork, path).key()] = true
		}
	}
	return out
}

func presenceGap(states map[string]pstate, target pstate, started func() map[string]bool) string {
	if _, ok := states[target.key()]; ok {
		return ""
	}
	if !target.present {
		return "absent"
	}
	if started()[target.key()] {
		return "unchanged"
	}
	return ""
}

func ownsChange(h *laneContent, path string, holders []*laneContent, at func(string, string) pstate) bool {
	carried := h.final[path]
	for _, o := range holders {
		if o == h {
			continue
		}
		if fork, ok := h.depForks[o.laneID]; ok && at(fork, path).key() == carried.key() {
			return false
		}
	}
	return true
}

func droppedOwnChange(h *laneContent, path string, base pstate, at func(string, string) pstate) bool {
	final := h.final[path]
	if h.tip == "" || h.forkPoint == "" {
		return final.key() != base.key()
	}
	atFork := at(h.forkPoint, path)
	return atFork.key() == base.key() && final.key() != atFork.key() && at(h.tip, path).key() == final.key()
}

// unmetApprovedContent is unmet_approved_content.
func unmetApprovedContent(contents []*laneContent, target, baseState func(string) pstate, skip func(string) bool, at func(string, string) pstate) []missingContent {
	byPath := map[string][]*laneContent{}
	for _, c := range contents {
		for p := range c.final {
			byPath[p] = append(byPath[p], c)
		}
	}
	// keep holder order = contents order
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []missingContent
	for _, p := range paths {
		if skip(p) {
			continue
		}
		holders := byPath[p]
		var cached *pstate
		base := func() pstate {
			if cached == nil {
				s := baseState(p)
				cached = &s
			}
			return *cached
		}
		tgt := target(p)
		un := unsuperseded(holders)
		if presenceGap(stateSet(un, p), tgt, func() map[string]bool { return startedFrom(p, un, base, at) }) == "" {
			continue
		}
		var authors []*laneContent
		for _, h := range holders {
			if ownsChange(h, p, holders, at) {
				authors = append(authors, h)
			}
		}
		live := unsuperseded(authors)
		found := presenceGap(stateSet(live, p), tgt, func() map[string]bool { return startedFrom(p, live, base, at) })
		if found == "" {
			continue
		}
		b := base()
		unmet := map[string]pstate{}
		for _, h := range live {
			if droppedOwnChange(h, p, b, at) {
				s := h.final[p]
				unmet[s.key()] = s
			}
		}
		keys := make([]pstate, 0, len(unmet))
		for _, s := range unmet {
			keys = append(keys, s)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].present != keys[j].present {
				return !keys[i].present
			}
			return keys[i].blob < keys[j].blob
		})
		for _, s := range keys {
			var wps, ls []string
			seen := map[string]bool{}
			for _, h := range authors {
				if h.final[p].key() == s.key() {
					ls = append(ls, h.laneID)
					for _, w := range h.wpIDs {
						if !seen[w] {
							seen[w] = true
							wps = append(wps, w)
						}
					}
				}
			}
			out = append(out, missingContent{wpIDs: wps, laneIDs: ls, path: p, expected: s, found: found})
		}
	}
	return out
}

// laneTipsMovedRefusal is lane_tips_moved_refusal (#5668): content that
// reached a lane after the claim-time check validated its tip.
func laneTipsMovedRefusal(root string, m *lanes.Manifest, validated [][2]string, anchors []string, prefix string, approvedByLane map[string][]string) (string, error) {
	isBK := func(p string) bool { return isBookkeepingPath(p, m.MissionSlug, prefix) }
	val := map[string]string{}
	for _, v := range validated {
		val[v[0]] = v[1]
	}
	sorted := append([]lanes.Lane(nil), m.Lanes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].LaneID < sorted[j].LaneID })
	var refusals []BoundRefusal
	for _, l := range sorted {
		branch := laneBranchFor(m, l.LaneID)
		v, ok := val[branch]
		if !ok || !branchExists(root, branch) {
			continue
		}
		excl := []string{v}
		for _, o := range validated {
			if o[0] != branch {
				excl = append(excl, o[1])
			}
		}
		excl = append(excl, anchors...)
		beyond, err := commitsBeyond(root, branch, excl)
		if err != nil {
			return "", err
		}
		content, err := contentCommits(root, beyond, isBK)
		if err != nil {
			return "", err
		}
		if len(content) == 0 {
			continue
		}
		wps := approvedByLane[l.LaneID]
		if len(wps) == 0 {
			wps = l.WPIDs
		}
		wps = append([]string(nil), wps...)
		sort.Strings(wps)
		r := BoundRefusal{Code: LaneMovedAfterApproval, LaneID: l.LaneID, Branch: branch, WPIDs: wps, Path: content[0].path}
		for _, x := range content {
			r.Commits = append(r.Commits, x.sha)
		}
		refusals = append(refusals, r)
	}
	if len(refusals) == 0 {
		return "", nil
	}
	return RenderBoundRefusals(refusals, m.MissionSlug), nil
}

var _ = os.Stat
