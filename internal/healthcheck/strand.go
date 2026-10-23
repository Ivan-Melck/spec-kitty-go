package healthcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/placement"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Stranded coordination-revert markers (#2786 / #2367-B, FR-007):
// _coordination_doctor._check_stranded_coord_revert and the --fix heal over
// coordination.coherence (coord_incoherent_done_wps, repair_coord_strand).

const (
	strandedCode          = "COORDINATION_STRANDED_COORD_REVERT"
	strandedHint          = "Run `spec-kitty doctor coordination --fix` to revert the stranded coordination `done` commit(s) and clear the reconcile marker."
	strandedStuckCode     = "COORDINATION_STRANDED_COORD_REVERT_STUCK"
	strandedStuckHint     = "The coordination worktree recorded for this strand no longer exists, so `--fix` cannot revert it. Recreate the coordination worktree (see the worktree hints from `spec-kitty doctor coordination`) then re-run `--fix`, or clear the stale `pending_coord_reconcile` marker after manually reconciling the coordination ref."
	strandedMismatchCode  = "COORDINATION_STRANDED_COORD_REVERT_BRANCH_MISMATCH"
	strandedMismatchHint  = "The coordination worktree recorded for this strand is checked out on the wrong branch, so `--fix` refuses to revert there (it would mutate the wrong branch instead of the coordination ref). Switch the worktree back to the coordination branch (see `" + workspaceRecoveryCmd + "`), then re-run `--fix`."
	strandedUnrecorded    = "COORDINATION_STRANDED_COORD_REVERT_UNRECORDED"
	strandedUnrecordedFmt = "`--fix` reverts only the strand commits recorded when the failed consolidation wrote its marker, and will not guess. Reconcile the coordination status log manually: list the status-log commits in the recorded range, revert only the stranded `done` commit(s) yourself (keeping any later reopen), then clear the stale `pending_coord_reconcile` marker in `.kittify/runtime/merge/%s/state.json`."
	markerUnparseableCode = "COORDINATION_RECONCILE_MARKER_UNPARSEABLE"
	markerUnparseableHint = "A `pending_coord_reconcile` marker could not be parsed into repair inputs (missing coord_ref/captured_sha/coord_worktree or an empty strand). Inspect `.kittify/runtime/merge/<mission_id>/state.json` and clear or repair the marker."
	markerUnresolvedCode  = "COORDINATION_RECONCILE_MISSION_UNRESOLVABLE"
	markerUnresolvedHint  = "A `pending_coord_reconcile` marker names a mission that could not be resolved to a planning directory (unsafe or ambiguous handle). Run `spec-kitty doctor identity --json` and disambiguate before re-running `--fix`."
	revertFailedMsg       = "git revert failed"
)

// consolidationStateFields is ConsolidationState's dataclass field order with
// each field's default (nil = required positional field).
var consolidationStateFields = []struct {
	name string
	def  func() any
}{
	{"mission_id", nil}, {"mission_slug", nil}, {"target_branch", nil}, {"wp_order", nil},
	{"completed_wps", func() any { return []any{} }}, {"current_wp", func() any { return nil }},
	{"has_pending_conflicts", func() any { return false }}, {"strategy", func() any { return "merge" }},
	{"workspace_path", func() any { return nil }}, {"started_at", func() any { return clock.NowUTCISO() }},
	{"updated_at", func() any { return clock.NowUTCISO() }}, {"mission_number_baked", func() any { return false }},
	{"push_requested", func() any { return false }}, {"delete_branch", func() any { return nil }},
	{"remove_worktree", func() any { return nil }}, {"pending_coord_reconcile", func() any { return nil }},
	{"skip_lanes", func() any { return false }}, {"pre_mutation_target_sha", func() any { return nil }},
	{"pre_mutation_coord_sha", func() any { return nil }}, {"pre_mutation_coord_ref", func() any { return nil }},
	{"pre_interrupt_lane_tips", func() any { return &jsonx.Object{} }}, {"reconciliation_passed_target_sha", func() any { return nil }},
	{"pre_mutation_refs", func() any { return &jsonx.Object{} }}, {"post_mutation_refs", func() any { return &jsonx.Object{} }},
	{"restore_targets", func() any { return &jsonx.Object{} }}, {"snapshot_lane_branches", func() any { return []any{} }},
	{"resume_seeded_refs", func() any { return []any{} }}, {"unsettled_refs", func() any { return []any{} }},
	{"advance_intents", func() any { return &jsonx.Object{} }}, {"released_refs", func() any { return &jsonx.Object{} }},
	{"release_reasons", func() any { return &jsonx.Object{} }},
}

// loadConsolidationState is _load_state_file: the state document rebuilt in
// dataclass field order with defaults (ok=false: missing, unreadable, or a
// schema mismatch).
func loadConsolidationState(path string) (*jsonx.Object, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	text, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return nil, false
	}
	v, err := pyjson.Loads(text)
	if err != nil {
		return nil, false
	}
	data, ok := v.(*jsonx.Object)
	if !ok {
		return nil, false
	}
	out := jsonx.NewObject()
	for _, f := range consolidationStateFields {
		if x, ok := data.Get(f.name); ok {
			out.Set(f.name, x)
			continue
		}
		if f.def == nil {
			return nil, false
		}
		out.Set(f.name, f.def())
	}
	return out, true
}

type reconcileMarker struct {
	state                          *jsonx.Object
	coordRef, capturedSHA, coordWT string
	candidates                     []string
	parsed                         bool
	missionID, missionSlug         any
	strandSHAs                     []string
	strandSHAsRecorded             bool
	stateDir                       string
	stateFile                      string
}

// pendingReconcileMarkers is iter_pending_coord_reconcile_markers.
func pendingReconcileMarkers(root string) []*reconcileMarker {
	base := project.Kittify(root, "runtime", "merge")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var out []*reconcileMarker
	for _, n := range names {
		file := filepath.Join(base, n, "state.json")
		st, ok := loadConsolidationState(file)
		if !ok {
			continue
		}
		raw, _ := st.Get("pending_coord_reconcile")
		if raw == nil {
			continue
		}
		m := &reconcileMarker{state: st, stateDir: filepath.Join(base, n), stateFile: file}
		m.missionID, _ = st.Get("mission_id")
		m.missionSlug, _ = st.Get("mission_slug")
		if mo, ok := raw.(*jsonx.Object); ok && mo.Len() > 0 {
			s := func(k string) string { x, _ := mo.Get(k); v, _ := x.(string); return v }
			m.coordRef, m.capturedSHA, m.coordWT = s("coord_ref"), s("captured_sha"), s("coord_worktree")
			l, _ := valueOf(mo, "stranded_wp_ids").([]any)
			for _, x := range l {
				m.candidates = append(m.candidates, pyjson.Str(x))
			}
			m.parsed = m.coordRef != "" && m.capturedSHA != "" && m.coordWT != "" && len(l) > 0
			if sl, ok := valueOf(mo, "strand_shas").([]any); ok {
				all := true
				for _, x := range sl {
					s, isStr := x.(string)
					if !isStr {
						all = false
						break
					}
					m.strandSHAs = append(m.strandSHAs, s)
				}
				m.strandSHAsRecorded = all
				if !all {
					m.strandSHAs = nil
				}
			}
		}
		out = append(out, m)
	}
	return out
}

func pyStrList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = pyjson.ReprString(x)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// planningReadDir is resolve_planning_read_dir(..., WORK_PACKAGE_TASK) for a
// marker's mission slug (a bare handle folds to its <slug>-<mid8>
// directory). ok=false is the unsafe/ambiguous refusal.
func planningReadDir(root, slug string) (string, bool) {
	if slug == "" {
		return "", false
	}
	dir, err := placement.PlanningReadDir(root, slug)
	if err != nil {
		return "", false
	}
	return dir, true
}

// coordIncoherentDoneWPs is coord_incoherent_done_wps: the candidates still
// reducing to done on the committed ref.
func coordIncoherentDoneWPs(root, ref string, candidates []string, featureDir string) []string {
	if len(candidates) == 0 {
		return nil
	}
	res, err := gitx.Exec(root, nil, nil, "show", ref+":kitty-specs/"+filepath.Base(featureDir)+"/status.events.jsonl")
	if err != nil || res.Code != 0 {
		return nil
	}
	var rows []*jsonx.Object
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		v, err := pyjson.Loads(strings.TrimSpace(line))
		if err != nil {
			return nil
		}
		if o, ok := v.(*jsonx.Object); ok {
			rows = append(rows, o)
		}
	}
	st, err := status.ParseRows(rows)
	if err != nil || len(st.Transitions) == 0 {
		return nil
	}
	snap := status.Reduce(st.Transitions, nil, false)
	var out []string
	for _, wp := range candidates {
		if snap.Lane(wp) == status.Done {
			out = append(out, wp)
		}
	}
	return out
}

func markerBaseExtra(m *reconcileMarker) *jsonx.Object {
	return jsonx.NewObject("mission_id", m.missionID, "mission_slug", m.missionSlug)
}

// strandFinding is _finding_for_reconcile_marker (nil for a stale marker).
func strandFinding(root string, m *reconcileMarker) *CoordFinding {
	if !m.parsed {
		f := cf("warning", fmt.Sprintf("Mission %s carries a `pending_coord_reconcile` marker that could not be parsed into repair inputs.", pyjson.Repr(m.missionSlug)),
			sp(markerUnparseableHint), sp(markerUnparseableCode))
		f.Extra = markerBaseExtra(m)
		return &f
	}
	slug, isStr := m.missionSlug.(string)
	if m.missionSlug == nil {
		f := cf("warning", "A `pending_coord_reconcile` marker is present but its mission carries no `mission_slug` to resolve a planning directory.",
			sp(markerUnresolvedHint), sp(markerUnresolvedCode))
		f.Extra = markerBaseExtra(m)
		return &f
	}
	featureDir, ok := planningReadDir(root, slug)
	if !isStr || !ok {
		f := cf("warning", fmt.Sprintf("Mission %s on a `pending_coord_reconcile` marker could not be resolved to a planning directory.", pyjson.Repr(m.missionSlug)),
			sp(markerUnresolvedHint), sp(markerUnresolvedCode))
		f.Extra = markerBaseExtra(m)
		return &f
	}
	remaining := coordIncoherentDoneWPs(root, m.coordRef, m.candidates, featureDir)
	if len(remaining) == 0 {
		return nil
	}
	var shas any
	if m.strandSHAsRecorded {
		shas = toAnyList(m.strandSHAs)
	}
	extra := jsonx.NewObject("mission_id", m.missionID, "mission_slug", m.missionSlug, "coord_ref", m.coordRef,
		"captured_sha", m.capturedSHA, "coord_worktree", m.coordWT, "candidate_wps", toAnyList(m.candidates),
		"stranded_wp_ids", toAnyList(remaining), "strand_shas", shas)
	if _, err := os.Stat(m.coordWT); err != nil {
		f := cf("error", fmt.Sprintf("Coordination ref %s for mission %s still strands WP(s) %s at `done`, but its coordination worktree %s no longer exists — `--fix` cannot revert it.",
			pyjson.ReprString(m.coordRef), pyjson.ReprString(slug), pyStrList(remaining), pyjson.ReprString(m.coordWT)),
			sp(strandedStuckHint), sp(strandedStuckCode))
		f.Extra = extra
		return &f
	}
	f := cf("error", fmt.Sprintf("Coordination ref %s for mission %s still reduces WP(s) %s to `done` after a merge rollback (expected `approved`).",
		pyjson.ReprString(m.coordRef), pyjson.ReprString(slug), pyStrList(remaining)), sp(strandedHint), sp(strandedCode))
	f.Extra = extra
	return &f
}

func toAnyList(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

func strandedMarkerFindings(root string) []CoordFinding {
	var out []CoordFinding
	for _, m := range pendingReconcileMarkers(root) {
		if f := strandFinding(root, m); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// strandRepair is CoordRepairOutcome.
type strandRepair struct {
	healed, worktreeMissing, headAdvanced, branchMismatch, legacy, mismatch bool
	foreign                                                                 []string
	err                                                                     string
}

func gitIn(dir string, args ...string) (string, bool) {
	res, err := gitx.Exec(dir, nil, nil, append([]string{"-C", dir}, args...)...)
	if err != nil || res.Code != 0 {
		return "", false
	}
	return res.Stdout, true
}

// statusLogCommitsInRange is status_log_commits_in_range (ok=false when git
// cannot enumerate the range).
func statusLogCommitsInRange(dir, base, tip, featureDir string) ([]string, bool) {
	out, ok := gitIn(dir, "rev-list", base+".."+tip, "--", "kitty-specs/"+filepath.Base(featureDir)+"/status.events.jsonl")
	if !ok {
		return nil, false
	}
	var shas []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			shas = append(shas, l)
		}
	}
	return shas, true
}

// repairCoordStrand is coordination.coherence.repair_coord_strand.
func repairCoordStrand(root string, m *reconcileMarker, featureDir string) strandRepair {
	wt := m.coordWT
	if _, err := os.Stat(wt); err != nil {
		return strandRepair{worktreeMissing: true}
	}
	if len(coordIncoherentDoneWPs(root, m.coordRef, m.candidates, featureDir)) == 0 {
		return strandRepair{}
	}
	headOut, ok := gitIn(wt, "rev-parse", "HEAD")
	head := strings.TrimSpace(headOut)
	if !ok || head == "" || head == m.capturedSHA {
		return strandRepair{}
	}
	if _, ok := gitIn(wt, "merge-base", "--is-ancestor", m.capturedSHA, head); !ok || len(coordIncoherentDoneWPs(root, head, m.candidates, featureDir)) == 0 {
		return strandRepair{headAdvanced: true}
	}
	branchOut, ok := gitIn(wt, "symbolic-ref", "-q", "HEAD")
	if !ok || strings.TrimPrefix(strings.TrimSpace(branchOut), "refs/heads/") != strings.TrimPrefix(m.coordRef, "refs/heads/") {
		return strandRepair{branchMismatch: true}
	}
	if !m.strandSHAsRecorded {
		return strandRepair{legacy: true}
	}
	inRange, ok := statusLogCommitsInRange(wt, m.capturedSHA, "HEAD", featureDir)
	if !ok {
		return strandRepair{mismatch: true}
	}
	recorded := map[string]bool{}
	for _, s := range m.strandSHAs {
		recorded[s] = true
	}
	var foreign []string
	inSet := map[string]bool{}
	for _, s := range inRange {
		inSet[s] = true
		if !recorded[s] {
			foreign = append(foreign, s)
		}
	}
	same := len(inSet) == len(recorded)
	for s := range recorded {
		if !inSet[s] {
			same = false
		}
	}
	if len(foreign) > 0 || !same {
		return strandRepair{mismatch: true, foreign: foreign}
	}
	slug := filepath.Base(featureDir)
	for _, f := range []string{"status.events.jsonl", "status.json"} {
		gitIn(wt, "checkout", "HEAD", "--", "kitty-specs/"+slug+"/"+f)
	}
	if len(inRange) == 0 {
		return strandRepair{}
	}
	for _, sha := range inRange {
		res, err := gitx.Exec(wt, nil, nil, "-C", wt, "revert", "--no-edit", sha)
		if err == nil && res.Code == 0 {
			continue
		}
		gitIn(wt, "revert", "--abort")
		msg := ""
		if err == nil {
			msg = strings.TrimSpace(res.Stderr)
			if msg == "" {
				msg = strings.TrimSpace(res.Stdout)
			}
		}
		if msg == "" {
			msg = revertFailedMsg
		}
		return strandRepair{err: msg}
	}
	return strandRepair{healed: true}
}

// clearPendingMarker is _clear_pending_marker: save_state with the marker
// cleared (updated_at refreshed, json.dumps indent=2).
func clearPendingMarker(m *reconcileMarker) {
	st, ok := loadConsolidationState(m.stateFile)
	if !ok {
		return
	}
	st.Set("pending_coord_reconcile", nil)
	st.Set("updated_at", clock.NowUTCISO())
	tmp := m.stateFile + ".tmp"
	if os.WriteFile(tmp, []byte(jsonx.Dumps(st, jsonx.Pretty)), 0o644) == nil {
		os.Rename(tmp, m.stateFile)
	}
}

// markerFromExtra is _parse_reconcile_marker over a live-strand finding's
// extra (its stranded_wp_ids are the strand the check derived) plus
// marker_strand_shas(extra).
func markerFromExtra(root string, ex *jsonx.Object) *reconcileMarker {
	m := &reconcileMarker{}
	s := func(k string) string { v, _ := valueOf(ex, k).(string); return v }
	m.coordRef, m.capturedSHA, m.coordWT = s("coord_ref"), s("captured_sha"), s("coord_worktree")
	l, _ := valueOf(ex, "stranded_wp_ids").([]any)
	for _, x := range l {
		m.candidates = append(m.candidates, pyjson.Str(x))
	}
	m.parsed = m.coordRef != "" && m.capturedSHA != "" && m.coordWT != "" && len(l) > 0
	if sl, ok := valueOf(ex, "strand_shas").([]any); ok {
		m.strandSHAsRecorded = true
		for _, x := range sl {
			v, isStr := x.(string)
			if !isStr {
				m.strandSHAsRecorded, m.strandSHAs = false, nil
				break
			}
			m.strandSHAs = append(m.strandSHAs, v)
		}
	}
	return m
}

// fixStrandedReverts is _apply_stranded_revert_fix: heal each live strand;
// returns the healed mission slugs and the findings for strands that could
// not be healed.
func fixStrandedReverts(root string, findings []CoordFinding) ([]string, []CoordFinding) {
	var healed []string
	var extra []CoordFinding
	for _, f := range findings {
		if f.Code == nil || *f.Code != strandedCode || f.Extra == nil {
			continue
		}
		missionID, idOK := valueOf(f.Extra, "mission_id").(string)
		slug, slugOK := valueOf(f.Extra, "mission_slug").(string)
		m := markerFromExtra(root, f.Extra)
		if !idOK || !slugOK || !m.parsed {
			w := cf("warning", "A live-strand finding carried an unparseable reconcile marker.", sp(markerUnparseableHint), sp(markerUnparseableCode))
			w.Extra = jsonx.NewObject("mission_id", valueOf(f.Extra, "mission_id"), "mission_slug", valueOf(f.Extra, "mission_slug"))
			extra = append(extra, w)
			continue
		}
		m.stateFile = project.Kittify(root, "runtime", "merge", missionID, "state.json")
		featureDir, ok := planningReadDir(root, slug)
		if !ok {
			w := cf("warning", fmt.Sprintf("Mission %s on a live-strand finding could not be resolved to a planning directory; skipping its heal.", pyjson.ReprString(slug)),
				sp(markerUnresolvedHint), sp(markerUnresolvedCode))
			w.Extra = jsonx.NewObject("mission_id", missionID, "mission_slug", slug)
			extra = append(extra, w)
			continue
		}
		out := repairCoordStrand(root, m, featureDir)
		ids := jsonx.NewObject("mission_id", missionID, "mission_slug", slug)
		switch {
		case out.healed:
			clearPendingMarker(m)
			healed = append(healed, slug)
		case out.legacy || out.mismatch:
			var detail string
			switch {
			case out.legacy:
				detail = "its reconcile marker recorded no strand commits (written by an older release, or the range was unreadable when it was written)"
			case len(out.foreign) > 0:
				short := make([]string, len(out.foreign))
				for i, s := range out.foreign {
					if len(s) > 12 {
						s = s[:12]
					}
					short[i] = s
				}
				detail = fmt.Sprintf("the status log holds commit(s) the marker did not record (%s), e.g. a later reopen", strings.Join(short, ", "))
			default:
				detail = "the status log no longer matches the commits the marker recorded"
			}
			w := cf("error", fmt.Sprintf("Refusing to revert the stranded `done` on %s for mission %s: %s. Nothing was reverted; the marker is kept.",
				pyjson.ReprString(m.coordRef), pyjson.ReprString(slug), detail), sp(fmt.Sprintf(strandedUnrecordedFmt, missionID)), sp(strandedUnrecorded))
			w.Extra = ids
			extra = append(extra, w)
		case out.worktreeMissing:
			w := cf("error", fmt.Sprintf("Coordination worktree %s for mission %s no longer exists — `--fix` cannot revert its strand.",
				pyjson.ReprString(m.coordWT), pyjson.ReprString(slug)), sp(strandedStuckHint), sp(strandedStuckCode))
			w.Extra = ids
			extra = append(extra, w)
		case out.branchMismatch:
			actual := "<detached>"
			if b, ok := gitIn(m.coordWT, "symbolic-ref", "HEAD"); ok {
				actual = strings.TrimSpace(b)
			}
			actual = strings.TrimPrefix(actual, "refs/heads/")
			w := cf("error", fmt.Sprintf("Coordination worktree %s for mission %s is on %s, not %s — `--fix` refuses to revert its strand there.",
				pyjson.ReprString(m.coordWT), pyjson.ReprString(slug), pyjson.ReprString(actual), pyjson.ReprString(m.coordRef)),
				sp(strandedMismatchHint), sp(strandedMismatchCode))
			w.Extra = ids
			extra = append(extra, w)
		}
	}
	return healed, extra
}
