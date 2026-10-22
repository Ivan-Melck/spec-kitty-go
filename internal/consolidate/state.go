package consolidate

// The merge transaction record (.kittify/runtime/merge/<mission_id>/
// state.json), its pre-mutation branch snapshot and the single rollback
// authority (consolidation.state / consolidation.rollback).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/autorebase"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
)

const (
	stateFileName   = "state.json"
	postFixMarker   = "reconciliation.post-fix"
	postFixContents = "terminus-reconciliation-gate\n"
	unsettledAll    = "*"

	// UnexplainedBranchMove is the rollback-anchor-authority refusal code.
	UnexplainedBranchMove = "UNEXPLAINED_BRANCH_MOVE"
)

// smap is an insertion-ordered string map (a Python dict[str, str]).
type smap [][2]string

func (m smap) get(k string) (string, bool) {
	for _, p := range m {
		if p[0] == k {
			return p[1], true
		}
	}
	return "", false
}

func (m *smap) set(k, v string) {
	for i, p := range *m {
		if p[0] == k {
			(*m)[i][1] = v
			return
		}
	}
	*m = append(*m, [2]string{k, v})
}

func (m *smap) del(k string) {
	for i, p := range *m {
		if p[0] == k {
			*m = append((*m)[:i:i], (*m)[i+1:]...)
			return
		}
	}
}

func (m smap) keys() []string {
	out := make([]string, 0, len(m))
	for _, p := range m {
		out = append(out, p[0])
	}
	return out
}

type intentChain struct {
	branch string
	chain  []string
}

// MergeState is ConsolidationState.
type MergeState struct {
	MissionID, MissionSlug, TargetBranch string
	WPOrder, CompletedWPs                []string
	Strategy                             string
	StartedAt, UpdatedAt                 string
	MissionNumberBaked, PushRequested    bool
	DeleteBranch, RemoveWorktree         bool
	SkipLanes                            bool
	PreTarget, PreCoord, PreCoordRef     string
	LaneTips                             smap // pre_interrupt_lane_tips: branch -> tip
	ReconPassed                          string
	PreRefs                              smap // pre_mutation_refs
	PostRefs                             smap // post_mutation_refs
	RestoreTargets                       smap // restore_targets (ADR A2)
	LaneBranches                         []string
	ResumeSeeded                         []string
	Unsettled                            []string
	Intents                              []intentChain
	Released                             smap
	ReleaseReasons                       smap
}

func stateDir(root, missionID string) string {
	return filepath.Join(root, ".kittify", "runtime", "merge", missionID)
}

func strList(xs []string) []any {
	out := []any{}
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

func pairsObj(ps smap) *jsonx.Object {
	o := &jsonx.Object{}
	for _, p := range ps {
		o.Set(p[0], p[1])
	}
	return o
}

func optStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *MergeState) intentsObj() *jsonx.Object {
	o := &jsonx.Object{}
	for _, c := range s.Intents {
		o.Set(c.branch, strList(c.chain))
	}
	return o
}

// Object renders the state document in upstream field order.
func (s *MergeState) Object() *jsonx.Object {
	return jsonx.NewObject("mission_id", s.MissionID, "mission_slug", s.MissionSlug, "target_branch", s.TargetBranch,
		"wp_order", strList(s.WPOrder), "completed_wps", strList(s.CompletedWPs), "current_wp", nil,
		"has_pending_conflicts", false, "strategy", s.Strategy, "workspace_path", nil, "started_at", s.StartedAt,
		"updated_at", s.UpdatedAt, "mission_number_baked", s.MissionNumberBaked, "push_requested", s.PushRequested,
		"delete_branch", s.DeleteBranch, "remove_worktree", s.RemoveWorktree, "pending_coord_reconcile", nil,
		"skip_lanes", s.SkipLanes, "pre_mutation_target_sha", optStr(s.PreTarget), "pre_mutation_coord_sha", optStr(s.PreCoord),
		"pre_mutation_coord_ref", optStr(s.PreCoordRef), "pre_interrupt_lane_tips", pairsObj(s.LaneTips),
		"reconciliation_passed_target_sha", optStr(s.ReconPassed), "pre_mutation_refs", pairsObj(s.PreRefs), "post_mutation_refs", pairsObj(s.PostRefs),
		"restore_targets", pairsObj(s.RestoreTargets), "snapshot_lane_branches", strList(s.LaneBranches), "resume_seeded_refs", strList(s.ResumeSeeded),
		"unsettled_refs", strList(s.Unsettled), "advance_intents", s.intentsObj(), "released_refs", pairsObj(s.Released), "release_reasons", pairsObj(s.ReleaseReasons))
}

// Save writes state.json (json.dumps indent=2, no trailing newline) and,
// on first creation, the reconciliation marker.
func (s *MergeState) Save(root string) error {
	dir := stateDir(root, s.MissionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s.UpdatedAt = clock.NowUTCISO()
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(jsonx.Dumps(s.Object(), jsonx.Pretty)), 0o644); err != nil {
		return err
	}
	marker := filepath.Join(dir, postFixMarker)
	if _, err := os.Stat(marker); err != nil {
		os.WriteFile(marker, []byte(postFixContents), 0o644)
	}
	return nil
}

func strsOf(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func pairsOf(o *jsonx.Object) smap {
	if o == nil {
		return nil
	}
	var out smap
	for _, kv := range o.Pairs() {
		if s, ok := kv.Value.(string); ok {
			out = append(out, [2]string{kv.Key, s})
		} else {
			return nil // malformed: fail closed
		}
	}
	return out
}

func unsettledOf(o *jsonx.Object) []string {
	v, ok := o.Get("unsettled_refs")
	if !ok {
		return nil
	}
	l, ok := v.([]any)
	if !ok {
		return []string{unsettledAll}
	}
	var out []string
	for _, x := range l {
		s, ok := x.(string)
		if !ok {
			return []string{unsettledAll}
		}
		out = append(out, s)
	}
	return out
}

func intentsOf(o *jsonx.Object) []intentChain {
	io := o.GetObject("advance_intents")
	if io == nil {
		return nil
	}
	var out []intentChain
	for _, kv := range io.Pairs() {
		l, ok := kv.Value.([]any)
		if !ok || len(l) < 2 {
			return nil
		}
		ch := strsOf(l)
		if len(ch) != len(l) {
			return nil
		}
		out = append(out, intentChain{kv.Key, ch})
	}
	return out
}

// LoadState reads the mission's state.json (nil when absent/unreadable).
func LoadState(root, missionID string) *MergeState {
	if missionID == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(stateDir(root, missionID), stateFileName))
	if err != nil {
		return nil
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil || o == nil {
		return nil
	}
	wp, _ := o.Get("wp_order")
	cw, _ := o.Get("completed_wps")
	lb, _ := o.Get("snapshot_lane_branches")
	rs, _ := o.Get("resume_seeded_refs")
	return &MergeState{MissionID: o.GetString("mission_id"), MissionSlug: o.GetString("mission_slug"),
		TargetBranch: o.GetString("target_branch"), WPOrder: strsOf(wp), CompletedWPs: strsOf(cw),
		Strategy: o.GetString("strategy"), StartedAt: o.GetString("started_at"), UpdatedAt: o.GetString("updated_at"),
		MissionNumberBaked: o.GetBool("mission_number_baked"), PushRequested: o.GetBool("push_requested"),
		DeleteBranch: o.GetBool("delete_branch"), RemoveWorktree: o.GetBool("remove_worktree"), SkipLanes: o.GetBool("skip_lanes"),
		PreTarget: o.GetString("pre_mutation_target_sha"), PreCoord: o.GetString("pre_mutation_coord_sha"),
		PreCoordRef: o.GetString("pre_mutation_coord_ref"), LaneTips: pairsOf(o.GetObject("pre_interrupt_lane_tips")),
		ReconPassed: o.GetString("reconciliation_passed_target_sha"),
		PreRefs:     pairsOf(o.GetObject("pre_mutation_refs")), PostRefs: pairsOf(o.GetObject("post_mutation_refs")),
		RestoreTargets: pairsOf(o.GetObject("restore_targets")), LaneBranches: strsOf(lb), ResumeSeeded: strsOf(rs),
		Unsettled: unsettledOf(o), Intents: intentsOf(o), Released: pairsOf(o.GetObject("released_refs")),
		ReleaseReasons: pairsOf(o.GetObject("release_reasons"))}
}

// ClearState removes the transaction record.
func ClearState(root, missionID string) bool {
	if missionID == "" {
		return false
	}
	dir := stateDir(root, missionID)
	_, err := os.Stat(filepath.Join(dir, stateFileName))
	os.Remove(filepath.Join(dir, stateFileName))
	os.Remove(filepath.Join(dir, postFixMarker))
	os.Remove(filepath.Join(dir, "lock"))
	os.Remove(dir)
	return err == nil
}

// liveTip is _live_tip.
func liveTip(root, branch string) string {
	return revParse(root, "refs/heads/"+branch+"^{commit}")
}

// candidateBranches is _candidate_branches (target, mission, coord, lanes; deduped).
func candidateBranches(m *lanes.Manifest, coordRef string) []string {
	ordered := []string{m.TargetBranch, m.MissionBranch}
	if coordRef != "" {
		ordered = append(ordered, coordRef)
	}
	for _, l := range m.Lanes {
		ordered = append(ordered, laneBranchFor(m, l.LaneID))
	}
	var out []string
	for _, b := range ordered {
		if b != "" && !contains(out, b) {
			out = append(out, b)
		}
	}
	return out
}

// missingSnapshotBranches is missing_snapshot_branches.
func missingSnapshotBranches(root string, m *lanes.Manifest, coordRef string) []string {
	var out []string
	for _, b := range candidateBranches(m, coordRef) {
		if liveTip(root, b) == "" {
			out = append(out, b)
		}
	}
	return out
}

// newState creates a fresh record (persisted target/coord anchors and the
// pre-interrupt lane tips; the snapshot is captured by captureSnapshot).
func newState(root, slug, missionID, target, strategy string, m *lanes.Manifest, wps []string, ret Retention, push bool, coordRef string, excluded map[string]bool) *MergeState {
	now := clock.NowUTCISO()
	s := &MergeState{MissionID: missionID, MissionSlug: slug, TargetBranch: target, WPOrder: wps, Strategy: strategy,
		StartedAt: now, UpdatedAt: now, PushRequested: push, DeleteBranch: ret.DeleteBranch, RemoveWorktree: ret.RemoveWorktree}
	s.PreTarget = revParse(root, "refs/heads/"+target)
	s.PreCoordRef = coordRef
	s.PreCoord = revParse(root, "refs/heads/"+coordRef)
	for _, l := range m.Lanes {
		if l.LaneID == lanes.PlanningLaneID || laneFullyCanceled(l, excluded) {
			continue
		}
		b := lanes.LaneBranchName(slug, l.LaneID, m.TargetBranch)
		if sha := revParse(root, "refs/heads/"+b+"^{commit}"); sha != "" {
			s.LaneTips.set(b, sha)
		}
	}
	return s
}

// captureSnapshot is capture_pre_mutation_snapshot (once; never recaptured).
func (s *MergeState) captureSnapshot(root string, m *lanes.Manifest, coordRef string, isResume bool) {
	if len(s.PreRefs) > 0 {
		return
	}
	var refs smap
	for _, b := range candidateBranches(m, coordRef) {
		if tip := liveTip(root, b); tip != "" {
			refs.set(b, tip)
		}
	}
	seeded := map[string]bool{}
	if s.PreTarget != "" {
		refs.set(m.TargetBranch, s.PreTarget)
		seeded[m.TargetBranch] = true
	}
	if coordRef != "" && s.PreCoord != "" && (s.PreCoordRef == "" || s.PreCoordRef == coordRef) {
		refs.set(coordRef, s.PreCoord)
		seeded[coordRef] = true
	}
	s.PreRefs = refs
	s.LaneBranches = nil
	movable := map[string]bool{m.TargetBranch: true, m.MissionBranch: true, coordRef: true}
	for _, b := range candidateBranches(m, coordRef) {
		if _, ok := refs.get(b); ok && !movable[b] {
			s.LaneBranches = append(s.LaneBranches, b)
		}
	}
	s.ResumeSeeded = nil
	if isResume {
		for _, b := range refs.keys() {
			if !seeded[b] {
				s.ResumeSeeded = append(s.ResumeSeeded, b)
			}
		}
	}
	s.Save(root)
}

func (s *MergeState) isLane(b string) bool { return contains(s.LaneBranches, b) }

// movable is run_movable_branches.
func (s *MergeState) movable() []string {
	var out []string
	for _, b := range s.PreRefs.keys() {
		if !s.isLane(b) {
			out = append(out, b)
		}
	}
	return out
}

func (s *MergeState) unsettledSet() map[string]bool {
	out := map[string]bool{}
	if contains(s.Unsettled, unsettledAll) {
		for _, b := range s.movable() {
			out[b] = true
		}
		return out
	}
	for _, b := range s.Unsettled {
		out[b] = true
	}
	return out
}

func (s *MergeState) setUnsettled(set map[string]bool) {
	s.Unsettled = nil
	for _, b := range s.movable() {
		if set[b] {
			s.Unsettled = append(s.Unsettled, b)
		}
	}
}

// restoreTarget is _record_restore_target.
func (s *MergeState) restoreTarget(b string) string {
	if t, ok := s.RestoreTargets.get(b); ok {
		return t
	}
	t, _ := s.PreRefs.get(b)
	return t
}

func (s *MergeState) intent(b string) []string {
	for _, c := range s.Intents {
		if c.branch == b {
			return c.chain
		}
	}
	return nil
}

func (s *MergeState) dropIntent(b string) {
	for i, c := range s.Intents {
		if c.branch == b {
			s.Intents = append(s.Intents[:i:i], s.Intents[i+1:]...)
			return
		}
	}
}

func (s *MergeState) expectedTip(b string) string {
	if p, ok := s.PostRefs.get(b); ok {
		return p
	}
	return s.restoreTarget(b)
}

// effectivePost is _effective_post ("" when none).
func (s *MergeState) effectivePost(b, live string) string {
	if ch := s.intent(b); len(ch) > 0 && ch[0] == s.expectedTip(b) && contains(ch[1:], live) {
		return live
	}
	p, _ := s.PostRefs.get(b)
	return p
}

// rollbackWouldRestore is rollback_would_restore.
func (s *MergeState) rollbackWouldRestore(b, live string) bool {
	if live == s.restoreTarget(b) {
		return true
	}
	p := s.effectivePost(b, live)
	return p != "" && live == p
}

type attemptStart struct {
	live, target, post string
	unexplained        bool
}

func (s *MergeState) attemptStarts(root string) smapStarts {
	unsettled := s.unsettledSet()
	var out smapStarts
	for _, p := range s.PreRefs {
		b, snap := p[0], p[1]
		live := liveTip(root, b)
		switch {
		case live == "":
			t, ok := s.RestoreTargets.get(b)
			if !ok {
				t = snap
			}
			out = append(out, startEntry{b, attemptStart{target: t}})
		case s.isLane(b):
			prev, _ := s.PostRefs.get(b)
			t := live
			if live == snap || (prev != "" && live == prev) {
				t = snap
			}
			post := ""
			if prev != "" && live == prev {
				post = prev
			}
			out = append(out, startEntry{b, attemptStart{live: live, target: t, post: post}})
		default:
			prev := s.restoreTarget(b)
			eff := s.effectivePost(b, live)
			st := attemptStart{live: live}
			switch {
			case live == prev || (eff != "" && live == eff):
				st.target = prev
				if eff != "" && live == eff {
					st.post = live
				}
			case !unsettled[b]:
				st.target = live
			default:
				st.target, st.unexplained = prev, true
			}
			out = append(out, startEntry{b, st})
		}
	}
	return out
}

type startEntry struct {
	branch string
	st     attemptStart
}

type smapStarts []startEntry

// unexplainedBranches is unexplained_branches: (branch, restore target, live).
func (s *MergeState) unexplainedBranches(root string) [][3]string {
	var out [][3]string
	for _, e := range s.attemptStarts(root) {
		if e.st.unexplained && e.st.live != "" {
			out = append(out, [3]string{e.branch, e.st.target, e.st.live})
		}
	}
	return out
}

// beginAttempt is begin_attempt: the unexplained branches (nothing changed
// then), else the restore targets are fixed and every movable branch is
// marked unsettled.
func (s *MergeState) beginAttempt(root string) []string {
	starts := s.attemptStarts(root)
	var unexplained []string
	for _, e := range starts {
		if e.st.unexplained {
			unexplained = append(unexplained, e.branch)
		}
	}
	if len(unexplained) > 0 {
		return unexplained
	}
	var post smap
	for _, e := range starts {
		s.RestoreTargets.set(e.branch, e.st.target)
		if e.st.post != "" {
			post.set(e.branch, e.st.post)
		}
	}
	s.PostRefs = post
	s.Intents = nil
	s.Released, s.ReleaseReasons = nil, nil
	s.Unsettled = s.movable()
	s.Save(root)
	return nil
}

// movableTips is movable_branch_tips.
func (s *MergeState) movableTips(root string) smap {
	var out smap
	for _, b := range s.movable() {
		out.set(b, liveTip(root, b))
	}
	return out
}

func (s *MergeState) expectedTips() smap {
	var out smap
	for _, b := range s.movable() {
		out.set(b, s.expectedTip(b))
	}
	return out
}

// noteIntent is note_advance_intent.
func (s *MergeState) noteIntent(root, b, old, new string) {
	if !contains(s.movable(), b) {
		return
	}
	for i, c := range s.Intents {
		if c.branch == b {
			if c.chain[len(c.chain)-1] == old {
				s.Intents[i].chain = append(c.chain, new)
			} else {
				s.Intents[i].chain = []string{old, new}
			}
			s.Save(root)
			return
		}
	}
	s.Intents = append(s.Intents, intentChain{b, []string{old, new}})
	s.Save(root)
}

// recordPostTips is record_post_mutation_tips with phase entry tips.
func (s *MergeState) recordPostTips(root string, entry, expected smap) {
	for _, b := range s.movable() {
		live := liveTip(root, b)
		e, _ := entry.get(b)
		if live == e {
			continue
		}
		s.dropIntent(b)
		x, _ := expected.get(b)
		if live != "" && e == x {
			s.PostRefs.set(b, live)
		}
	}
	s.Save(root)
}

// settle is settle_branch.
func (s *MergeState) settle(root, b string) {
	set := s.unsettledSet()
	delete(set, b)
	s.setUnsettled(set)
	s.dropIntent(b)
	s.Save(root)
}

// release is release_branch.
func (s *MergeState) release(b, live, reason string) {
	s.Released.set(b, live)
	s.ReleaseReasons.set(b, reason)
}

// movedBranches reports whether an earlier attempt moved (or may have
// moved) a run-movable branch.
func (s *MergeState) movedBranches(root string) bool {
	for _, p := range s.movableTips(root) {
		if p[1] == "" || p[1] != s.restoreTarget(p[0]) {
			return true
		}
	}
	return false
}

// earlierMovesSentences is _earlier_moves_sentences.
func (s *MergeState) earlierMovesSentences(root string) string {
	var moved, unrestorable, unknown []string
	for _, p := range s.movableTips(root) {
		b, live := p[0], p[1]
		switch {
		case live == "":
			unknown = append(unknown, b)
		case live != s.restoreTarget(b):
			moved = append(moved, b)
			if !s.rollbackWouldRestore(b, live) {
				unrestorable = append(unrestorable, b)
			}
		}
	}
	sortStrings(moved)
	sortStrings(unrestorable)
	sortStrings(unknown)
	text := ""
	if len(moved) > 0 {
		holds := ""
		if contains(moved, s.TargetBranch) {
			holds = fmt.Sprintf(" (the local target '%s' currently holds content from it)", s.TargetBranch)
		}
		text = fmt.Sprintf(" That attempt already moved %s%s.", strings.Join(moved, ", "), holds)
	}
	if len(unrestorable) > 0 {
		var was []string
		for _, b := range unrestorable {
			was = append(was, fmt.Sprintf("%s (restore target %s)", b, s.restoreTarget(b)))
		}
		text += fmt.Sprintf(" `%s` will report %s as NOT restored: the attempt was interrupted before it recorded where it stopped.", consolidateAbortCommand, strings.Join(was, ", "))
		if contains(unrestorable, s.TargetBranch) {
			text += fmt.Sprintf(" Restore the local target '%s' to %s yourself before re-running.", s.TargetBranch, s.restoreTarget(s.TargetBranch))
		}
	}
	if len(unknown) > 0 {
		text += fmt.Sprintf(" The current tip of %s could not be resolved, so whether that attempt moved it is unknown.", strings.Join(unknown, ", "))
	}
	return text
}

// ---------------------------------------------------------------------------
// rollback

type outcomeKind int

const (
	okRestored outcomeKind = iota
	okAlready
	okUnchangedByRun
	okLaneMissing
	okNotRestored
	okKept
)

type branchOutcome struct {
	branch                string
	kind                  outcomeKind
	snapshot, observed    string
	expected, reason      string
	restoredTo            string
	resumeSeeded, adopted bool
}

// RollbackReport is the rollback authority's report.
type RollbackReport struct {
	outcomes  []branchOutcome
	refusedVL bool
	reason    string
	hasReason bool
	coordNote string
}

func (r *RollbackReport) fullyRestored() bool {
	if r.refusedVL || r.hasReason {
		return false
	}
	for _, o := range r.outcomes {
		if o.kind == okNotRestored {
			return false
		}
	}
	return true
}

func (r *RollbackReport) restoredOffSnapshot() bool {
	for _, o := range r.outcomes {
		if o.kind == okRestored && o.restoredTo != "" && o.restoredTo != o.snapshot {
			return true
		}
	}
	return false
}

const seededNote = "snapshot taken when this record was resumed"

// Render is RollbackReport.render.
func (r *RollbackReport) Render() string {
	if r.refusedVL {
		return strings.TrimRight("Kept the landing verified by an earlier reconciliation; nothing was rolled back. "+r.reason, " ")
	}
	if r.hasReason && len(r.outcomes) == 0 {
		return "Nothing was rolled back: " + r.reason + "."
	}
	header := "Rollback to the pre-consolidation snapshot:"
	seeded := false
	for _, o := range r.outcomes {
		if o.resumeSeeded {
			seeded = true
		}
	}
	if r.restoredOffSnapshot() {
		header = "Rollback, restored to the record's restore targets:"
	} else if seeded {
		header = "Rollback to the snapshot (pre-consolidation unless marked [" + seededNote + "]):"
	}
	width := 0
	for _, o := range r.outcomes {
		if len(o.branch) > width {
			width = len(o.branch)
		}
	}
	lines := []string{header}
	for _, o := range r.outcomes {
		l := renderOutcome(o, width)
		if o.resumeSeeded {
			l += " [" + seededNote + "]"
		}
		lines = append(lines, l)
	}
	if r.coordNote != "" {
		lines = append(lines, "  coordination checkout left as found (its status files may differ from the branch tip): "+r.coordNote)
	}
	return strings.Join(lines, "\n")
}

func renderOutcome(o branchOutcome, width int) string {
	name := o.branch + strings.Repeat(" ", width-len(o.branch))
	switch o.kind {
	case okRestored:
		to := o.restoredTo
		if to == "" {
			to = o.snapshot
		}
		l := fmt.Sprintf("  restored   %s  %s -> %s", name, short7(o.observed), short7(to))
		if o.adopted {
			l += " (adopted interrupted advance)"
		}
		return l
	case okKept:
		return fmt.Sprintf("  kept       %s  (released by operator: %s; at %s; may contain this consolidation's unverified changes)", name, o.reason, short7(o.observed))
	case okAlready:
		return fmt.Sprintf("  unchanged  %s  (already at %s)", name, short7(o.observed))
	case okLaneMissing:
		return fmt.Sprintf("  missing    %s  (%s)", name, o.reason)
	case okUnchangedByRun:
		reason := o.reason
		if reason == "" {
			reason = "lane branch: not moved by consolidation"
		}
		return fmt.Sprintf("  kept       %s  (%s; at %s)", name, reason, short7(o.observed))
	}
	detail := "observed " + short7(o.observed)
	if o.expected != "" {
		detail += ", expected " + short7(o.expected)
	}
	reason := o.reason
	if reason == "" {
		reason = "unknown"
	}
	return fmt.Sprintf("  NOT restored %s  %s -- %s", name, detail, reason)
}

func short7(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// worktreeOf returns the checkout of branch ("" when not checked out).
func worktreeOf(root, branch string) string {
	wts := worktreesOf(root, branch)
	if len(wts) == 0 {
		return ""
	}
	return wts[0]
}

func worktreesOf(root, branch string) []string {
	out, err := gitx.Run(root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	cur := ""
	var wts []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "worktree ") {
			cur = strings.TrimPrefix(l, "worktree ")
		} else if l == "branch refs/heads/"+branch {
			wts = append(wts, cur)
		}
	}
	return wts
}

// residue reports whether a dirty path in a checkout is the run's own
// disposable bookkeeping (the mission's status pair).
func ownStatusFile(slug string) func(string) bool {
	return func(p string) bool {
		parts := strings.Split(p, "/")
		for i, s := range parts {
			if s == "kitty-specs" && i+2 < len(parts) && (parts[i+1] == slug || strings.HasPrefix(parts[i+1], slug+"-")) {
				base := parts[len(parts)-1]
				return i+3 == len(parts) && (base == "status.events.jsonl" || base == "status.json")
			}
		}
		return false
	}
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// runResidue is _RunResidue for the checkout wt: the mission's own status
// pair, or the residue context of the checkout's role.
func runResidue(root, slug, wt string) func(string) bool {
	role := autorebase.RoleLane
	if samePath(wt, root) {
		role = autorebase.RoleRepositoryRoot
	} else if sf := coord.Resolve(root, slug); sf.CoordWorktree != "" && samePath(wt, sf.CoordWorktree) {
		role = autorebase.RoleCoordination
	}
	ctx := autorebase.Residue(root, slug, role)
	own := ownStatusFile(slug)
	return func(p string) bool { return own(p) || (ctx != nil && ctx(p)) }
}

// dirtyForReset lists the entries a `reset --hard` to sha would destroy.
func dirtyForReset(wt, sha string, disposable func(string) bool) []string {
	targetPaths := map[string]bool{}
	if out, err := gitx.Run(wt, "ls-tree", "-r", "--name-only", "-z", sha); err == nil {
		for _, p := range strings.Split(out, "\x00") {
			if p != "" {
				targetPaths[p] = true
			}
		}
	}
	var dirty []string
	for _, e := range porcelainAll(wt) {
		if disposable(e.path) {
			continue
		}
		if e.xy == "??" || e.xy == "!!" {
			p := strings.TrimRight(e.path, "/")
			obstructs := targetPaths[p]
			if !obstructs {
				for tp := range targetPaths {
					if strings.HasPrefix(tp, p+"/") || strings.HasPrefix(p, tp+"/") {
						obstructs = true
						break
					}
				}
			}
			if obstructs {
				dirty = append(dirty, fmt.Sprintf("%s %s (untracked/ignored path would be overwritten by reset to %s)", e.xy, e.path, short(sha, 12)))
			}
			continue
		}
		dirty = append(dirty, e.xy+" "+e.path)
	}
	return dirty
}

func checkoutEquals(wt, sha string) bool {
	return git(wt, "diff", "--quiet", sha, "--").Code == 0 && git(wt, "diff", "--cached", "--quiet", sha, "--").Code == 0
}

// restoreBranchRef is restore_branch_ref with resync_checkouts=True.
func restoreBranchRef(root, slug, branch, to, expected string) error {
	wts := worktreesOf(root, branch)
	for _, wt := range wts {
		d := dirtyForReset(wt, to, runResidue(root, slug, wt))
		if len(d) > 0 && checkoutEquals(wt, to) {
			var kept []string
			for _, x := range d {
				if strings.Contains(x, "would be overwritten") {
					kept = append(kept, x)
				}
			}
			d = kept
		}
		if len(d) > 0 {
			var lines []string
			for _, x := range d {
				lines = append(lines, "    "+x)
			}
			abs, _ := filepath.Abs(wt)
			return fmt.Errorf("Refusing to advance branch %s (%s -> %s): the worktree at %s has it checked out and holds uncommitted local changes that a resync (`git reset --hard`) would destroy (#1826 / NFR-002).\n  Dirty entries:\n%s\n  Commit, stash, or revert these changes in %s, then resume the consolidation (`spec-kitty consolidate --resume`).",
				pyRepr(branch), short(expected, 12), short(to, 12), abs, strings.Join(lines, "\n"), abs)
		}
	}
	var needs []string
	for _, wt := range wts {
		if !checkoutEquals(wt, to) {
			needs = append(needs, wt)
		}
	}
	res := git(root, "update-ref", "refs/heads/"+branch, to, expected)
	if res.Code != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		return fmt.Errorf("Failed to restore %s from %s to %s: %s", pyRepr(branch), short(expected, 12), short(to, 12), msg)
	}
	for _, wt := range needs {
		if r := git(wt, "reset", "--hard", branch); r.Code != 0 {
			msg := strings.TrimSpace(r.Stderr)
			if msg == "" {
				msg = strings.TrimSpace(r.Stdout)
			}
			return fmt.Errorf("Restored %s (%s -> %s) but failed to resync the checked-out worktree at %s: %s. The worktree is behind its own HEAD (#1826); repair with `git -C %s reset --hard` once the cause is fixed.",
				branch, short(expected, 12), short(to, 12), wt, msg, wt)
		}
	}
	return nil
}

func pyRepr(s string) string {
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}

func (s *MergeState) rollbackBranch(root, b, snap string) branchOutcome {
	live := liveTip(root, b)
	if live == "" {
		hint := fmt.Sprintf("no longer exists; snapshot %s; recreate with `git branch %s %s` if you still need it", snap, b, snap)
		if s.isLane(b) {
			return branchOutcome{branch: b, kind: okLaneMissing, snapshot: snap, reason: "lane branch " + b + " " + hint}
		}
		return branchOutcome{branch: b, kind: okNotRestored, snapshot: snap, reason: "branch " + b + " " + hint}
	}
	to := s.restoreTarget(b)
	if live == to {
		return branchOutcome{branch: b, kind: okAlready, snapshot: snap, observed: live}
	}
	if s.isLane(b) {
		return branchOutcome{branch: b, kind: okUnchangedByRun, snapshot: snap, observed: live, reason: "lane branch: not moved by consolidation"}
	}
	post := s.effectivePost(b, live)
	if !s.rollbackWouldRestore(b, live) || post == "" {
		reason := "moved by another actor since this run"
		if post == "" {
			reason = "moved since the snapshot but no post-mutation tip was recorded (interrupted phase?); inspect before re-running"
		}
		o := branchOutcome{branch: b, kind: okNotRestored, snapshot: snap, observed: live, expected: post, reason: reason}
		if rel, ok := s.Released.get(b); ok && rel == live {
			reason, _ := s.ReleaseReasons.get(b)
			o.kind, o.expected, o.reason = okKept, "", reason
		}
		return o
	}
	recorded, _ := s.PostRefs.get(b)
	if err := restoreBranchRef(root, s.MissionSlug, b, to, post); err != nil {
		return branchOutcome{branch: b, kind: okNotRestored, snapshot: snap, observed: live, expected: post, reason: err.Error()}
	}
	return branchOutcome{branch: b, kind: okRestored, snapshot: snap, observed: live, expected: post, restoredTo: to, adopted: post != recorded}
}

// Rollback is rollback_to_snapshot: CAS-restores every run-movable branch
// the record can explain and renders the report.
func (s *MergeState) Rollback(root string) *RollbackReport {
	if len(s.PreRefs) == 0 {
		return &RollbackReport{reason: "no pre-mutation snapshot recorded (pre-fix record)", hasReason: true}
	}
	if live := liveTip(root, s.TargetBranch); live != "" && s.ReconPassed == live {
		return &RollbackReport{refusedVL: true, reason: fmt.Sprintf("target %s is at %s, verified by an earlier reconciliation", s.TargetBranch, short7(live))}
	}
	rep := &RollbackReport{}
	for _, p := range s.PreRefs {
		o := s.rollbackBranch(root, p[0], p[1])
		o.resumeSeeded = contains(s.ResumeSeeded, p[0])
		rep.outcomes = append(rep.outcomes, o)
	}
	if cr := s.PreCoordRef; cr != "" {
		for _, o := range rep.outcomes {
			if o.branch == cr && (o.kind == okNotRestored || o.kind == okKept) && o.observed != "" {
				tip := liveTip(root, cr)
				for _, wt := range worktreesOf(root, cr) {
					if d := dirtyForReset(wt, tip, runResidue(root, s.MissionSlug, wt)); len(d) > 0 {
						abs, _ := filepath.Abs(wt)
						var ls []string
						for _, x := range d {
							ls = append(ls, "    "+x)
						}
						rep.coordNote = fmt.Sprintf("Refusing to advance branch %s (%s -> %s): the worktree at %s has it checked out and holds uncommitted local changes that a resync (`git reset --hard`) would destroy (#1826 / NFR-002).\n  Dirty entries:\n%s\n  Commit, stash, or revert these changes in %s, then resume the consolidation (`spec-kitty consolidate --resume`).",
							pyRepr(cr), short(tip, 12), short(tip, 12), abs, strings.Join(ls, "\n"), abs) +
							"\n  Remedy: commit or stash the listed change in the coordination worktree (`git -C <coordination worktree> stash`), then re-run `spec-kitty consolidate --resume` or `--abort`; the rollback then resyncs its status files to the branch tip."
						break
					}
					if !checkoutEquals(wt, tip) {
						git(wt, "reset", "--hard", cr)
					}
				}
				break
			}
		}
	}
	movable := map[string]bool{}
	for _, b := range s.movable() {
		movable[b] = true
	}
	un := s.unsettledSet()
	for _, o := range rep.outcomes {
		if !movable[o.branch] {
			continue
		}
		switch o.kind {
		case okRestored, okAlready, okKept:
			delete(un, o.branch)
			s.dropIntent(o.branch)
		case okNotRestored:
			un[o.branch] = true
		}
	}
	s.setUnsettled(un)
	for _, o := range rep.outcomes {
		if o.branch == s.TargetBranch && o.kind == okRestored {
			s.ReconPassed = ""
		}
	}
	if rep.fullyRestored() {
		s.MissionNumberBaked = false
		s.CompletedWPs = nil
		s.ReconPassed = ""
		s.PostRefs, s.Intents, s.Unsettled, s.Released, s.ReleaseReasons = nil, nil, nil, nil, nil
	}
	s.Save(root)
	return rep
}

// exitUnexplained is _exit_unexplained_branch_move.
func exitUnexplained(w io.Writer, rows [][3]string) error {
	fmt.Fprintln(w, "\nError: Refusing to continue this consolidation: the merge record cannot explain where these branches are now "+
		"(neither their restore target nor a tip this consolidation recorded or provably wrote):")
	for _, r := range rows {
		fmt.Fprintf(w, "  %s: restore target %s, live %s\n", r[0], r[1], r[2])
	}
	fmt.Fprintln(w, "Nothing was changed and the merge record is kept. To continue:")
	for _, r := range rows {
		fmt.Fprintf(w, "  - inspect the unexplained commits on %s: git log %s..%s\n", r[0], r[1], r[2])
	}
	fmt.Fprintln(w, "  - if these commits should not stay, move the branch yourself; spec-kitty never moves a commit it cannot prove is its own.")
	fmt.Fprintf(w, "  - to keep them, release the branch and clear the record: %s --release-branch <branch> --release-reason \"<why>\" (warning: a release keeps every commit listed above on the branch).\n", consolidateAbortCommand)
	fmt.Fprintf(w, "Error code: %s.\n", UnexplainedBranchMove)
	return exit(1)
}

// Abort is `consolidate --abort`: bind operator releases, roll back to the
// snapshot and clear the record (kept when a branch is not restored).
func Abort(w io.Writer, root, handle string, release []string, reason string) error {
	slug, err := ResolveMissionSlug(root, handle)
	if err != nil {
		sayErr(w, err.Error())
		return exit(2)
	}
	mid := missionIDOf(root, slug)
	st := LoadState(root, mid)
	if len(release) > 0 {
		var movable []string
		if st != nil {
			movable = st.movable()
		}
		var invalid []string
		for _, b := range release {
			if !contains(movable, b) {
				invalid = append(invalid, b)
			}
		}
		if len(invalid) > 0 {
			allowed := strings.Join(movable, ", ")
			if allowed == "" {
				allowed = "none"
			}
			say(w, "[red]Error:[/red] "+fmt.Sprintf("--release-branch names %s, which this consolidation record cannot release (releasable: the record's target, mission or coordination branch: %s).", strings.Join(invalid, ", "), allowed)+" Nothing was changed. Error code: RELEASE_BRANCH_INVALID.")
			return exit(2)
		}
	}
	if st == nil {
		say(w, fmt.Sprintf("[yellow]No active merge state found for %s.[/yellow] Coordination workspace left unchanged.", slug))
		return nil
	}
	if len(release) > 0 {
		tips := st.movableTips(root)
		var unresolved []string
		for _, b := range release {
			if t, _ := tips.get(b); t == "" {
				unresolved = append(unresolved, b)
			}
		}
		if len(unresolved) > 0 {
			say(w, "[red]Error:[/red] "+fmt.Sprintf("--release-branch names %s, which does not resolve to a commit.", strings.Join(unresolved, ", "))+" Nothing was changed. Error code: RELEASE_BRANCH_INVALID.")
			return exit(2)
		}
		for _, b := range release {
			t, _ := tips.get(b)
			st.release(b, t, reason)
		}
		st.Save(root)
	}
	var rep *RollbackReport
	if len(st.PreRefs) == 0 {
		say(w, "[yellow]Notice:[/yellow] no pre-mutation snapshot was recorded for this consolidation (older record); aborting without restoring branches.")
	} else {
		rep = st.Rollback(root)
		fmt.Fprintln(w, rep.Render())
		if !rep.fullyRestored() {
			say(w, "[red]Kept the consolidation record[/red] so nothing is lost; resolve the branches named above, then re-run `spec-kitty consolidate --abort`.")
			return exit(1)
		}
	}
	teardownCoordForAbort(w, root, slug)
	ClearState(root, mid)
	say(w, abortSuccessLine(slug, st, rep))
	say(w, "[green]Removed merge lock.[/green]")
	return nil
}

func abortSuccessLine(slug string, st *MergeState, rep *RollbackReport) string {
	if len(st.PreRefs) == 0 {
		return fmt.Sprintf("[green]Aborted[/green] merge for %s. State and workspace cleaned up.", slug)
	}
	commits := "pre-consolidation commits"
	if len(st.ResumeSeeded) > 0 {
		commits = "snapshot commits (snapshot taken when this record was resumed)"
	}
	restoredTo := "their " + commits
	if rep != nil && rep.restoredOffSnapshot() {
		restoredTo = "the record's restore targets"
	}
	var kept []string
	anyRestored := false
	if rep != nil {
		for _, o := range rep.outcomes {
			if o.kind == okKept {
				kept = append(kept, fmt.Sprintf("%s (kept at %s by operator release)", o.branch, short7(o.observed)))
			}
			if o.kind == okRestored {
				anyRestored = true
			}
		}
	}
	if len(kept) == 0 {
		return fmt.Sprintf("[green]Aborted[/green] consolidation for %s. Branches restored to %s; state and workspace cleaned up.", slug, restoredTo)
	}
	if anyRestored {
		return fmt.Sprintf("[green]Aborted[/green] consolidation for %s. Branches restored to %s, except %s; state and workspace cleaned up.", slug, restoredTo, strings.Join(kept, ", "))
	}
	return fmt.Sprintf("[green]Aborted[/green] consolidation for %s. No branch was restored; %s; state and workspace cleaned up.", slug, strings.Join(kept, ", "))
}

// teardownCoordForAbort is _teardown_coordination_for_abort: remove the
// coordination worktree (never its branch) unless meta.json retains it.
func teardownCoordForAbort(w io.Writer, root, slug string) {
	sf := coord.Resolve(root, slug)
	if strings.TrimSpace(sf.Meta.GetString("mid8")) == "" {
		return
	}
	ret := ResolveRetention(sf.PrimaryDir, nil, nil)
	for _, warning := range ret.Warnings {
		say(w, "[yellow]Warning:[/yellow] "+warning)
	}
	if !ret.RemoveWorktree {
		say(w, "[yellow]Notice:[/yellow] retention honored for worktrees (source: meta.json) — coordination worktree kept during abort.")
		return
	}
	wt := sf.CoordWorktree
	if wt == "" {
		return
	}
	if _, err := os.Stat(wt); err != nil {
		gitx.Run(root, "worktree", "prune")
		return
	}
	gitx.Run(root, "worktree", "remove", "--force", wt)
}
