package lanes

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
)

// Execution modes (WorkProductKind).
const (
	CodeChange       = "code_change"
	PlanningArtifact = "planning_artifact"
)

// Ownership is an OwnershipManifest (the lane-relevant fields).
type Ownership struct {
	ExecutionMode string
	OwnedFiles    []string
}

// ComputationError is LaneComputationError.
type ComputationError struct{ Msg string }

func (e *ComputationError) Error() string { return e.Msg }

// CycleLane names one lane of a detected lane dependency cycle.
type CycleLane struct {
	LaneID string
	WPIDs  []string
}

// DependencyCycleError is LaneDependencyCycleError.
type DependencyCycleError struct {
	CyclePath  []string
	CycleLanes []CycleLane
}

// ErrorCode is LANE_DEPENDENCY_CYCLE.
func (e *DependencyCycleError) ErrorCode() string { return "LANE_DEPENDENCY_CYCLE" }

func (e *DependencyCycleError) Error() string {
	return "Execution-lane dependency cycle detected: " + strings.Join(e.CyclePath, " -> ")
}

// --- frozen membership (#5573) ---------------------------------------------

// Membership conflict reasons in precedence order.
const (
	StartedLanesCollapsed = "started_lanes_collapsed"
	StartedWPRemoved      = "started_wp_removed"
	StartedWPKindChanged  = "started_wp_kind_changed"
	StatusUnreadable      = "status_unreadable"
)

var reasonPrecedence = []string{StartedLanesCollapsed, StartedWPRemoved, StartedWPKindChanged, StatusUnreadable}

var conflictClauses = map[string]string{
	StartedLanesCollapsed: "%s would be merged into one lane",
	StartedWPRemoved:      "%s would be dropped from the plan",
	StartedWPKindChanged:  "%s would cross the lane-planning boundary",
	StatusUnreadable:      "the status log or the recorded lane work tips cannot be read, so started work cannot be determined",
}

var conflictRemedies = map[string]string{
	StartedLanesCollapsed: "Remove the overlap that forces {wps} into one lane (for example, move the shared path into a new work package that depends on them, or drop it from one of them), then re-run finalize-tasks.",
	StartedWPRemoved:      "Restore the task file of {wps}. To retire a started work package instead, cancel it with `spec-kitty agent tasks move-task {cmd_wp} --to canceled --mission <handle>` without clearing its owned files, then re-run finalize-tasks.",
	StartedWPKindChanged:  "Restore the `execution_mode` of {wps}, put the new kind of work in a new work package, then re-run finalize-tasks.",
	StatusUnreadable:      "Repair the status log (`spec-kitty agent status validate --mission <handle>` reports the problem; `spec-kitty agent status doctor` checks status hygiene), then re-run finalize-tasks.",
}

func joinWPIDs(ids []string) string {
	if len(ids) <= 1 {
		if len(ids) == 1 {
			return ids[0]
		}
		return "the named work packages"
	}
	return strings.Join(ids[:len(ids)-1], ", ") + " and " + ids[len(ids)-1]
}

// RemedyFor is remedy_for.
func RemedyFor(reason string, wpIDs []string) string {
	ordered := append([]string(nil), wpIDs...)
	sort.Strings(ordered)
	cmd := "<WP>"
	if len(ordered) == 1 {
		cmd = ordered[0]
	}
	return strings.NewReplacer("{wps}", joinWPIDs(ordered), "{cmd_wp}", cmd).Replace(conflictRemedies[reason])
}

// MembershipConflict is one reason a frozen membership cannot be honoured.
type MembershipConflict struct {
	Reason        string
	WPIDs         []string
	RecordedLanes []string
	Remedy        string
	pairs         [][2]string
}

// Describe renders the human clause.
func (c *MembershipConflict) Describe() string {
	clause := conflictClauses[c.Reason]
	if !strings.Contains(clause, "%s") {
		return clause
	}
	var named string
	if len(c.pairs) > 0 {
		var parts []string
		for _, p := range c.pairs {
			parts = append(parts, fmt.Sprintf("%s (%s)", p[0], p[1]))
		}
		named = joinWPIDs(parts)
	} else {
		named = joinWPIDs(c.WPIDs)
	}
	return fmt.Sprintf(clause, named)
}

// ConflictFor is conflict_for(reason, bindings).
func ConflictFor(reason string, bindings map[string]string) MembershipConflict {
	var ids []string
	lanes := map[string]bool{}
	for wp, l := range bindings {
		ids = append(ids, wp)
		lanes[l] = true
	}
	sort.Strings(ids)
	var ls []string
	for l := range lanes {
		ls = append(ls, l)
	}
	sort.Strings(ls)
	c := MembershipConflict{Reason: reason, WPIDs: ids, RecordedLanes: ls, Remedy: RemedyFor(reason, ids)}
	for _, wp := range ids {
		c.pairs = append(c.pairs, [2]string{wp, bindings[wp]})
	}
	return c
}

// StatusUnreadableConflict is status_unreadable_conflict.
func StatusUnreadableConflict(detail, lead string) MembershipConflict {
	remedy := RemedyFor(StatusUnreadable, nil) + " Cause: " + detail
	if lead != "" {
		remedy = lead + " " + detail
	}
	return MembershipConflict{Reason: StatusUnreadable, WPIDs: []string{}, RecordedLanes: []string{}, Remedy: remedy}
}

// MembershipFrozenError is LaneMembershipFrozenError.
type MembershipFrozenError struct {
	Conflicts []MembershipConflict
	Reason    string
	NextStep  string
	msg       string
}

// ErrorCode is LANE_MEMBERSHIP_FROZEN.
func (e *MembershipFrozenError) ErrorCode() string { return "LANE_MEMBERSHIP_FROZEN" }

func (e *MembershipFrozenError) Error() string { return e.msg }

// NewMembershipFrozenError builds the error from non-empty conflicts.
func NewMembershipFrozenError(conflicts []MembershipConflict) *MembershipFrozenError {
	ordered := append([]MembershipConflict(nil), conflicts...)
	rank := func(r string) int {
		for i, x := range reasonPrecedence {
			if x == r {
				return i
			}
		}
		return len(reasonPrecedence)
	}
	sort.SliceStable(ordered, func(i, j int) bool { return rank(ordered[i].Reason) < rank(ordered[j].Reason) })
	e := &MembershipFrozenError{Conflicts: conflicts, Reason: ordered[0].Reason}
	seen := map[string]bool{}
	var steps, clauses []string
	for _, c := range ordered {
		if !seen[c.Remedy] {
			seen[c.Remedy] = true
			steps = append(steps, c.Remedy)
		}
		clauses = append(clauses, c.Describe())
	}
	e.NextStep = strings.Join(steps, "\n")
	prefix := "Cannot re-finalize: started work packages would change lane. "
	if e.Reason == StatusUnreadable {
		prefix = "Cannot re-finalize: "
	}
	e.msg = prefix + strings.Join(clauses, "; ") + "."
	return e
}

// FrozenMembership is FrozenLaneMembership.
type FrozenMembership struct {
	Bindings    map[string]string // started WP -> recorded lane id
	Retired     map[string]bool
	ReservedIDs map[string]bool
}

func (f *FrozenMembership) reservedLaneIDs() map[string]bool {
	out := map[string]bool{}
	if f == nil {
		return out
	}
	for _, l := range f.Bindings {
		out[l] = true
	}
	for l := range f.ReservedIDs {
		out[l] = true
	}
	return out
}

var startedLanes = map[string]bool{"claimed": true, "in_progress": true, "for_review": true, "in_review": true, "approved": true, "done": true}

// StartedWPIDs is started_wp_ids over (wp_id, to_lane) transition pairs.
func StartedWPIDs(transitions [][2]string) map[string]bool {
	out := map[string]bool{}
	for _, t := range transitions {
		if startedLanes[t[1]] {
			out[t[0]] = true
		}
	}
	return out
}

// BuildFrozenMembership is build_frozen_membership.
func BuildFrozenMembership(previous *Manifest, started, tippedBranches, present, eligible map[string]bool) *FrozenMembership {
	f := &FrozenMembership{Bindings: map[string]string{}, Retired: map[string]bool{}, ReservedIDs: map[string]bool{}}
	if previous == nil {
		return f
	}
	for _, l := range previous.Lanes {
		var members []string
		for _, w := range l.WPIDs {
			if started[w] {
				members = append(members, w)
			}
		}
		if len(members) == 0 && l.LaneID != PlanningLaneID && tippedBranches[LaneBranchName(previous.MissionSlug, l.LaneID, previous.TargetBranch)] {
			members = l.WPIDs
		}
		for _, w := range members {
			f.Bindings[w] = l.LaneID
		}
	}
	for w := range present {
		if !eligible[w] {
			f.Retired[w] = true
		}
	}
	for b := range tippedBranches {
		bp := ParseBranch(b)
		if bp == nil || bp.LaneID == "" || bp.LaneID == PlanningLaneID {
			continue
		}
		if LaneBranchName(previous.MissionSlug, bp.LaneID, previous.TargetBranch) == b {
			f.ReservedIDs[bp.LaneID] = true
		}
	}
	return f
}

// --- helpers -------------------------------------------------------------

// fnmatchRe is fnmatch.translate (case-sensitive, '*' crosses '/').
func fnmatchRe(pat string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString(`(?s)^`)
	rs := []rune(pat)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := i + 1
			if j < len(rs) && rs[j] == '!' {
				j++
			}
			if j < len(rs) && rs[j] == ']' {
				j++
			}
			for j < len(rs) && rs[j] != ']' {
				j++
			}
			if j >= len(rs) {
				b.WriteString(`\[`)
				continue
			}
			stuff := string(rs[i+1 : j])
			if strings.HasPrefix(stuff, "!") {
				stuff = "^" + stuff[1:]
			} else if strings.HasPrefix(stuff, "^") {
				stuff = `\` + stuff
			}
			stuff = strings.ReplaceAll(stuff, `\`, `\\`)
			b.WriteString("[" + stuff + "]")
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`$`)
	re, err := regexp.Compile(b.String())
	if err != nil {
		return regexp.MustCompile(`^` + regexp.QuoteMeta(pat) + `$`)
	}
	return re
}

// Fnmatch is Python fnmatch.fnmatch on POSIX.
func Fnmatch(name, pattern string) bool { return fnmatchRe(pattern).MatchString(name) }

func globPrefix(p string) string {
	for _, s := range []string{"/**", "/*", "**", "*"} {
		if strings.HasSuffix(p, s) {
			return p[:len(p)-len(s)]
		}
	}
	return p
}

// GlobsOverlap is ownership.validation._globs_overlap.
func GlobsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	pa, pb := globPrefix(a), globPrefix(b)
	if pa != "" && pb != "" && (strings.HasPrefix(pb, pa) || strings.HasPrefix(pa, pb)) {
		return true
	}
	return Fnmatch(pb, a) || Fnmatch(pa, b)
}

// TopologicalSort is core.dependency_graph.topological_sort (Kahn, sorted queue).
func TopologicalSort(graph map[string][]string) ([]string, error) {
	in := map[string]int{}
	rev := map[string][]string{}
	for n := range graph {
		rev[n] = nil
	}
	for n, deps := range graph {
		in[n] = len(deps)
		for _, d := range deps {
			if _, ok := rev[d]; ok {
				rev[d] = append(rev[d], n)
			}
		}
	}
	var queue []string
	for n, d := range in {
		if d == 0 {
			queue = append(queue, n)
		}
	}
	sort.Strings(queue)
	var out []string
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		out = append(out, n)
		deps := append([]string(nil), rev[n]...)
		sort.Strings(deps)
		for _, d := range deps {
			in[d]--
			if in[d] == 0 {
				queue = append(queue, d)
				sort.Strings(queue)
			}
		}
	}
	if len(out) != len(graph) {
		return nil, fmt.Errorf("Graph contains a cycle - cannot topologically sort")
	}
	return out, nil
}

var surfaceKeywords = []struct {
	surface  string
	keywords []string
}{
	{"dashboard", []string{"dashboard", "landing page", "landing-page"}},
	{"workspace", []string{"workspace", "mission workspace"}},
	{"app-shell", []string{"app shell", "app-shell", "navigation", "sidebar", "layout"}},
	{"legacy-cleanup", []string{"legacy", "cleanup", "deprecat", "remov"}},
	{"tests", []string{"test suite", "test infrastructure", "e2e test", "integration test"}},
	{"tracker-integration", []string{"tracker", "saas", "sync"}},
	{"artifact-rendering", []string{"artifact", "render", "template"}},
	{"api", []string{"api", "endpoint", "route", "view"}},
}

// InferSurfaces is infer_surfaces(wp_body).
func InferSurfaces(body string) []string {
	low := strings.ToLower(body)
	out := []string{}
	for _, s := range surfaceKeywords {
		for _, k := range s.keywords {
			if strings.Contains(low, k) {
				out = append(out, s.surface)
				break
			}
		}
	}
	return out
}

type unionFind struct {
	parent map[string]string
	rank   map[string]int
	order  []string
}

func newUnionFind(elems []string) *unionFind {
	u := &unionFind{parent: map[string]string{}, rank: map[string]int{}, order: append([]string(nil), elems...)}
	for _, e := range elems {
		u.parent[e] = e
	}
	return u
}

func (u *unionFind) find(x string) string {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if u.rank[ra] < u.rank[rb] {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
	if u.rank[ra] == u.rank[rb] {
		u.rank[ra]++
	}
}

func (u *unionFind) groups() [][]string {
	idx := map[string]int{}
	var out [][]string
	for _, e := range u.order {
		r := u.find(e)
		i, ok := idx[r]
		if !ok {
			i = len(out)
			idx[r] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], e)
	}
	return out
}

func pyListRepr(xs []string) string {
	var parts []string
	for _, x := range xs {
		parts = append(parts, "'"+x+"'")
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func describeOverlap(a, b *Ownership) string {
	var parts []string
	for _, ga := range a.OwnedFiles {
		for _, gb := range b.OwnedFiles {
			if GlobsOverlap(ga, gb) {
				parts = append(parts, fmt.Sprintf("'%s' vs '%s'", ga, gb))
			}
		}
	}
	if len(parts) > 0 {
		return "overlapping globs: " + strings.Join(parts, ", ")
	}
	return "write-scope overlap"
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func disjoint(a, b *Ownership) bool {
	for _, ga := range a.OwnedFiles {
		for _, gb := range b.OwnedFiles {
			if GlobsOverlap(ga, gb) {
				return false
			}
		}
	}
	return true
}

func transitiveDeps(graph map[string][]string) map[string]map[string]bool {
	cache := map[string]map[string]bool{}
	visiting := map[string]bool{}
	var reach func(string) map[string]bool
	reach = func(w string) map[string]bool {
		if r, ok := cache[w]; ok {
			return r
		}
		if visiting[w] {
			return map[string]bool{}
		}
		visiting[w] = true
		res := map[string]bool{}
		for _, d := range graph[w] {
			res[d] = true
		}
		for _, d := range graph[w] {
			for x := range reach(d) {
				res[x] = true
			}
		}
		delete(visiting, w)
		cache[w] = res
		return res
	}
	for w := range graph {
		reach(w)
	}
	return cache
}

func sortedSet(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func findLaneCycle(laneDeps map[string]map[string]bool) []string {
	nodesSet := map[string]bool{}
	for l, ds := range laneDeps {
		nodesSet[l] = true
		for d := range ds {
			nodesSet[d] = true
		}
	}
	nodes := sortedSet(nodesSet)
	state := map[string]int{}
	for _, root := range nodes {
		if state[root] != 0 {
			continue
		}
		path := []string{root}
		index := map[string]int{root: 0}
		type frame struct {
			node string
			deps []string
			next int
		}
		stack := []frame{{root, sortedSet(laneDeps[root]), 0}}
		state[root] = 1
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.next >= len(top.deps) {
				stack = stack[:len(stack)-1]
				path = path[:len(path)-1]
				delete(index, top.node)
				state[top.node] = 2
				continue
			}
			dep := top.deps[top.next]
			top.next++
			switch state[dep] {
			case 0:
				state[dep] = 1
				index[dep] = len(path)
				path = append(path, dep)
				stack = append(stack, frame{dep, sortedSet(laneDeps[dep]), 0})
			case 1:
				cyc := append([]string(nil), path[index[dep]:]...)
				small := 0
				for i, x := range cyc {
					if x < cyc[small] {
						small = i
					}
				}
				norm := append(append([]string(nil), cyc[small:]...), cyc[:small]...)
				return append(norm, norm[0])
			}
		}
	}
	return nil
}

func nextFreeLaneID(used map[string]bool) string {
	for c := 'a'; ; c++ {
		id := "lane-" + string(c)
		if !used[id] {
			return id
		}
	}
}

// ComputeInput carries compute_lanes' inputs.
type ComputeInput struct {
	DependencyGraph map[string][]string
	Ownership       map[string]*Ownership
	MissionSlug     string
	TargetBranch    string // default "main"
	WPBodies        map[string]string
	MissionID       *string
	Previous        *Manifest
	Topology        string // default "lanes"
	MissionBranch   string // single_branch only ("" = target)
	Frozen          *FrozenMembership
}

func missionBranchFor(slug string, id *string) string {
	if id != nil {
		return MissionBranchName(slug, *id)
	}
	return MissionBranchName(slug, "")
}

// Compute is compute_lanes: union-find lane computation producing an
// acyclic manifest.
func Compute(in ComputeInput) (*Manifest, error) {
	if in.TargetBranch == "" {
		in.TargetBranch = "main"
	}
	frozen := in.Frozen
	if frozen == nil || in.Topology == "single_branch" {
		frozen = &FrozenMembership{Bindings: map[string]string{}, Retired: map[string]bool{}, ReservedIDs: map[string]bool{}}
	}
	now := clock.NowUTCISO()
	var all []string
	for w := range in.DependencyGraph {
		all = append(all, w)
	}
	sort.Strings(all)
	empty := func(planning []string) *Manifest {
		if planning == nil {
			planning = []string{}
		}
		return &Manifest{Version: 1, MissionSlug: in.MissionSlug, MissionID: in.MissionID, MissionBranch: missionBranchFor(in.MissionSlug, in.MissionID),
			TargetBranch: in.TargetBranch, Lanes: []Lane{}, ComputedAt: now, ComputedFrom: "dependency_graph+ownership", PlanningArtifactWPs: planning}
	}
	if len(all) == 0 {
		if c := frozenConflicts(frozen, nil, nil); len(c) > 0 {
			return nil, NewMembershipFrozenError(c)
		}
		return empty(nil), nil
	}
	noManifest := func(w string) error {
		return &ComputationError{Msg: fmt.Sprintf("Executable WP '%s' has no ownership manifest. Ensure owned_files and execution_mode are set in WP frontmatter, or run finalize-tasks to infer them.", w)}
	}
	if in.Topology == "single_branch" {
		ordered, err := TopologicalSort(in.DependencyGraph)
		if err != nil {
			return nil, err
		}
		scope := map[string]bool{}
		planning := []string{}
		for _, w := range ordered {
			m := in.Ownership[w]
			if m == nil {
				return nil, noManifest(w)
			}
			if m.ExecutionMode == PlanningArtifact {
				planning = append(planning, w)
			}
			for _, f := range m.OwnedFiles {
				scope[f] = true
			}
		}
		mb := in.MissionBranch
		if mb == "" {
			mb = in.TargetBranch
		}
		return &Manifest{Version: 1, MissionSlug: in.MissionSlug, MissionID: in.MissionID, MissionBranch: mb, TargetBranch: in.TargetBranch,
			Lanes:      []Lane{{LaneID: PlanningLaneID, WPIDs: ordered, WriteScope: sortedSet(scope), PredictedSurfaces: []string{}, DependsOnLanes: []string{}}},
			ComputedAt: now, ComputedFrom: "dependency_graph+ownership", PlanningArtifactWPs: planning}, nil
	}
	var code, planning []string
	for _, w := range all {
		m := in.Ownership[w]
		if m != nil && m.ExecutionMode == PlanningArtifact {
			planning = append(planning, w)
			continue
		}
		if m == nil {
			return nil, noManifest(w)
		}
		code = append(code, w)
	}
	fconf := frozenConflicts(frozen, code, planning)
	if len(code) == 0 && len(planning) == 0 {
		if len(fconf) > 0 {
			return nil, NewMembershipFrozenError(fconf)
		}
		return empty(nil), nil
	}
	if len(code) == 0 {
		if len(fconf) > 0 {
			return nil, NewMembershipFrozenError(fconf)
		}
		pl := planningLane(planning, in.Ownership, nil, 0)
		m := empty(append([]string(nil), pl.WPIDs...))
		m.Lanes = []Lane{pl}
		return m, nil
	}
	uf := newUnionFind(code)
	var events []CollapseEvent
	codeSet := map[string]bool{}
	for _, w := range code {
		codeSet[w] = true
	}
	for i := 0; i < len(code); i++ {
		for j := i + 1; j < len(code); j++ {
			a, b := code[i], code[j]
			ma, mb := in.Ownership[a], in.Ownership[b]
			if ma == nil || mb == nil || disjoint(ma, mb) {
				continue
			}
			if uf.find(a) != uf.find(b) {
				ev := describeOverlap(ma, mb)
				if contains(in.DependencyGraph[a], b) {
					ev += "; " + a + " depends on " + b
				} else if contains(in.DependencyGraph[b], a) {
					ev += "; " + b + " depends on " + a
				}
				events = append(events, CollapseEvent{WPA: a, WPB: b, Rule: "write_scope_overlap", Evidence: ev})
			}
			uf.union(a, b)
		}
	}
	if len(in.WPBodies) > 0 {
		surf := map[string]map[string]bool{}
		for _, w := range code {
			s := map[string]bool{}
			for _, x := range InferSurfaces(in.WPBodies[w]) {
				s[x] = true
			}
			surf[w] = s
		}
		for i := 0; i < len(code); i++ {
			for j := i + 1; j < len(code); j++ {
				a, b := code[i], code[j]
				shared := map[string]bool{}
				for s := range surf[a] {
					if surf[b][s] {
						shared[s] = true
					}
				}
				if len(shared) == 0 {
					continue
				}
				ma, mb := in.Ownership[a], in.Ownership[b]
				if ma != nil && mb != nil && disjoint(ma, mb) {
					continue
				}
				if uf.find(a) != uf.find(b) {
					events = append(events, CollapseEvent{WPA: a, WPB: b, Rule: "surface_heuristic", Evidence: fmt.Sprintf("shared surfaces %s with non-disjoint ownership", pyListRepr(sortedSet(shared)))})
				}
				uf.union(a, b)
			}
		}
	}
	// started lane-mates stay together
	byLane := map[string][]string{}
	var bound []string
	for w := range frozen.Bindings {
		bound = append(bound, w)
	}
	sort.Strings(bound)
	for _, w := range bound {
		if l := frozen.Bindings[w]; codeSet[w] && l != PlanningLaneID {
			byLane[l] = append(byLane[l], w)
		}
	}
	var laneKeys []string
	for l := range byLane {
		laneKeys = append(laneKeys, l)
	}
	sort.Strings(laneKeys)
	for _, l := range laneKeys {
		ms := byLane[l]
		for _, o := range ms[1:] {
			if uf.find(ms[0]) != uf.find(o) {
				events = append(events, CollapseEvent{WPA: ms[0], WPB: o, Rule: "frozen_lane_membership", Evidence: "both started on " + l})
			}
			uf.union(ms[0], o)
		}
	}
	groups := uf.groups()
	for _, g := range groups {
		sort.Strings(g)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i][0] < groups[j][0] })
	ids, err := assignLaneIDs(groups, in.Previous, frozen, fconf)
	if err != nil {
		return nil, err
	}
	var laneList []Lane
	for gi, g := range groups {
		gs := map[string]bool{}
		for _, w := range g {
			gs[w] = true
		}
		sub := map[string][]string{}
		for _, w := range g {
			sub[w] = []string{}
			for _, d := range in.DependencyGraph[w] {
				if gs[d] {
					sub[w] = append(sub[w], d)
				}
			}
		}
		ordered, err := TopologicalSort(sub)
		if err != nil {
			return nil, err
		}
		scope, surfaces := map[string]bool{}, map[string]bool{}
		for _, w := range ordered {
			if m := in.Ownership[w]; m != nil {
				for _, f := range m.OwnedFiles {
					scope[f] = true
				}
			}
			if len(in.WPBodies) > 0 {
				for _, s := range InferSurfaces(in.WPBodies[w]) {
					surfaces[s] = true
				}
			}
		}
		laneList = append(laneList, Lane{LaneID: ids[gi], WPIDs: ordered, WriteScope: sortedSet(scope), PredictedSurfaces: sortedSet(surfaces)})
	}
	wpLane := map[string]string{}
	for _, l := range laneList {
		for _, w := range l.WPIDs {
			wpLane[w] = l.LaneID
		}
	}
	for _, w := range planning {
		wpLane[w] = PlanningLaneID
	}
	laneDeps := map[string]map[string]bool{}
	for _, l := range laneList {
		laneDeps[l.LaneID] = map[string]bool{}
	}
	if len(planning) > 0 {
		laneDeps[PlanningLaneID] = map[string]bool{}
	}
	for _, w := range append(append([]string(nil), code...), planning...) {
		my := wpLane[w]
		if my == "" {
			continue
		}
		for _, d := range in.DependencyGraph[w] {
			if dl := wpLane[d]; dl != "" && dl != my {
				laneDeps[my][dl] = true
			}
		}
	}
	if cyc := findLaneCycle(laneDeps); cyc != nil {
		members := map[string][]string{}
		for w, l := range wpLane {
			members[l] = append(members[l], w)
		}
		var cl []CycleLane
		for _, l := range cyc[:len(cyc)-1] {
			ms := append([]string(nil), members[l]...)
			sort.Strings(ms)
			cl = append(cl, CycleLane{LaneID: l, WPIDs: ms})
		}
		return nil, &DependencyCycleError{CyclePath: cyc, CycleLanes: cl}
	}
	depthIDs := []string{}
	for _, l := range laneList {
		depthIDs = append(depthIDs, l.LaneID)
	}
	if len(planning) > 0 {
		depthIDs = append(depthIDs, PlanningLaneID)
	}
	depth := laneDepths(depthIDs, laneDeps)
	for i := range laneList {
		laneList[i].DependsOnLanes = sortedSet(laneDeps[laneList[i].LaneID])
		laneList[i].ParallelGroup = depth[laneList[i].LaneID]
	}
	all2 := append([]Lane(nil), laneList...)
	if len(planning) > 0 {
		all2 = append(all2, planningLane(planning, in.Ownership, sortedSet(laneDeps[PlanningLaneID]), depth[PlanningLaneID]))
	}
	sort.SliceStable(all2, func(i, j int) bool {
		if all2[i].ParallelGroup != all2[j].ParallelGroup {
			return all2[i].ParallelGroup < all2[j].ParallelGroup
		}
		return all2[i].LaneID < all2[j].LaneID
	})
	m := empty(append([]string{}, planning...))
	m.Lanes = all2
	m.CollapseReport = &CollapseReport{Events: events, IndependentWPsCollapsed: independentCollapses(events, in.DependencyGraph)}
	return m, nil
}

func independentCollapses(events []CollapseEvent, graph map[string][]string) int {
	if len(events) == 0 {
		return 0
	}
	tr := transitiveDeps(graph)
	n := 0
	for _, e := range events {
		if e.Rule == "dependency" || e.Rule == "frozen_lane_membership" {
			continue
		}
		if !tr[e.WPA][e.WPB] && !tr[e.WPB][e.WPA] {
			n++
		}
	}
	return n
}

func planningLane(wps []string, own map[string]*Ownership, deps []string, group int) Lane {
	scope := map[string]bool{}
	for _, w := range wps {
		if m := own[w]; m != nil {
			for _, f := range m.OwnedFiles {
				scope[f] = true
			}
		}
	}
	ids := append([]string(nil), wps...)
	sort.Strings(ids)
	if deps == nil {
		deps = []string{}
	}
	return Lane{LaneID: PlanningLaneID, WPIDs: ids, WriteScope: sortedSet(scope), PredictedSurfaces: []string{"planning"}, DependsOnLanes: deps, ParallelGroup: group}
}

func laneDepths(ids []string, deps map[string]map[string]bool) map[string]int {
	depths := map[string]int{}
	inProgress := map[string]bool{}
	var depth func(string) int
	depth = func(l string) int {
		if d, ok := depths[l]; ok {
			return d
		}
		if inProgress[l] {
			return 0
		}
		inProgress[l] = true
		best, any := 0, false
		for d := range deps[l] {
			if d == l {
				continue
			}
			v := depth(d)
			if !any || v > best {
				best, any = v, true
			}
		}
		delete(inProgress, l)
		if any {
			depths[l] = 1 + best
		} else {
			depths[l] = 0
		}
		return depths[l]
	}
	for _, l := range ids {
		depth(l)
	}
	return depths
}

func frozenConflicts(f *FrozenMembership, code, planning []string) []MembershipConflict {
	cs, ps := map[string]bool{}, map[string]bool{}
	for _, w := range code {
		cs[w] = true
	}
	for _, w := range planning {
		ps[w] = true
	}
	removed, kind := map[string]string{}, map[string]string{}
	for w, l := range f.Bindings {
		if !cs[w] && !ps[w] {
			if !f.Retired[w] {
				removed[w] = l
			}
		} else if ps[w] != (l == PlanningLaneID) {
			kind[w] = l
		}
	}
	var out []MembershipConflict
	if len(removed) > 0 {
		out = append(out, ConflictFor(StartedWPRemoved, removed))
	}
	if len(kind) > 0 {
		out = append(out, ConflictFor(StartedWPKindChanged, kind))
	}
	return out
}

func assignLaneIDs(groups [][]string, previous *Manifest, f *FrozenMembership, prior []MembershipConflict) ([]string, error) {
	assigned := map[int]string{}
	used := map[string]bool{}
	conflicts := append([]MembershipConflict(nil), prior...)
	for i, g := range groups {
		bound := map[string]string{}
		pins := map[string]bool{}
		for _, w := range g {
			if l, ok := f.Bindings[w]; ok && l != PlanningLaneID {
				bound[w] = l
				pins[l] = true
			}
		}
		if len(pins) > 1 {
			conflicts = append(conflicts, ConflictFor(StartedLanesCollapsed, bound))
		} else if len(pins) == 1 {
			for l := range pins {
				assigned[i] = l
				used[l] = true
			}
		}
	}
	if len(conflicts) > 0 {
		return nil, NewMembershipFrozenError(conflicts)
	}
	existing := map[string]map[string]bool{}
	if previous != nil {
		for _, l := range previous.Lanes {
			if l.LaneID == PlanningLaneID {
				continue
			}
			s := map[string]bool{}
			for _, w := range l.WPIDs {
				s[w] = true
			}
			existing[l.LaneID] = s
		}
	}
	var prevIDs []string
	for id := range existing {
		prevIDs = append(prevIDs, id)
	}
	sort.Strings(prevIDs)
	for i, g := range groups {
		if _, ok := assigned[i]; ok {
			continue
		}
		best, bestN := "", 0
		for _, id := range prevIDs {
			if used[id] {
				continue
			}
			n := 0
			for _, w := range g {
				if existing[id][w] {
					n++
				}
			}
			if n > bestN {
				best, bestN = id, n
			}
		}
		if best != "" {
			assigned[i] = best
			used[best] = true
		}
	}
	reserved := f.reservedLaneIDs()
	for i := range groups {
		if _, ok := assigned[i]; ok {
			continue
		}
		all := map[string]bool{}
		for k := range used {
			all[k] = true
		}
		for k := range reserved {
			all[k] = true
		}
		id := nextFreeLaneID(all)
		assigned[i] = id
		used[id] = true
	}
	out := make([]string, len(groups))
	for i := range groups {
		out[i] = assigned[i]
	}
	return out, nil
}
