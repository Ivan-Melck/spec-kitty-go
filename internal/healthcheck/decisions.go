package healthcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/placement"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Decision Moment index reconciliation (cli/commands/_decisions_doctor.py
// + decisions/fork.py + decisions/index_fold.py).

const (
	decisionOpened   = "DecisionPointOpened"
	decisionResolved = "DecisionPointResolved"
)

// SurfaceLog is fork.SurfaceLog.
type SurfaceLog struct {
	Source, Path string
	Ref          *string
	EventIDs     []string
}

func (s *SurfaceLog) obj() any {
	if s == nil {
		return nil
	}
	var ref any
	if s.Ref != nil {
		ref = *s.Ref
	}
	return jsonx.NewObject("source", s.Source, "ref", ref, "path", s.Path, "event_count", len(s.EventIDs))
}

// StreamFinding is fork.StreamForkFinding.
type StreamFinding struct {
	Stream, State                  string
	Primary, Coordination          *SurfaceLog
	OnlyPrimary, OnlyCoord, OnBoth []string
}

// ForkReport is fork.DecisionsForkReport.
type ForkReport struct {
	Streams                         []StreamFinding
	LedgerState                     string
	LedgerOnlyEntries, LedgerOnlyDM []string
	Forked                          bool
	ReconcileSteps                  []string
}

// LedgerCoordinationOnly is ledger_is_coordination_only.
func (f *ForkReport) LedgerCoordinationOnly() bool {
	return f.LedgerState == "coordination_only" || (f.LedgerState == "both" && (len(f.LedgerOnlyEntries) > 0 || len(f.LedgerOnlyDM) > 0))
}

func strs(x []string) []any {
	out := []any{}
	for _, s := range x {
		out = append(out, s)
	}
	return out
}

// StreamsJSON renders the streams list.
func (f *ForkReport) StreamsJSON() []any {
	out := []any{}
	for _, s := range f.Streams {
		out = append(out, jsonx.NewObject("stream", s.Stream, "state", s.State, "primary", s.Primary.obj(), "coordination", s.Coordination.obj(),
			"decisions_only_on_primary", strs(s.OnlyPrimary), "decisions_only_on_coordination", strs(s.OnlyCoord), "decisions_on_both", strs(s.OnBoth)))
	}
	return out
}

type coordSide struct{ dir, branch, subdir string }

func parseJSONL(text string) ([]*jsonx.Object, int) {
	var rows []*jsonx.Object
	bad := 0
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		v, err := pyjson.Loads(l)
		if err != nil {
			bad++
			continue
		}
		if o, ok := v.(*jsonx.Object); ok {
			rows = append(rows, o)
		} else {
			bad++
		}
	}
	return rows, bad
}

func decisionIDFor(stream string, row *jsonx.Object) string {
	p, ok := valueOf(row, "payload").(*jsonx.Object)
	if stream == "status.events.jsonl" {
		et := valueOf(row, "event_type")
		if et != decisionOpened && et != decisionResolved {
			return ""
		}
		if !ok {
			return ""
		}
		if v := valueOf(p, "decision_point_id"); pyjson.Truthy(v) {
			return pyjson.Str(v)
		}
		return ""
	}
	if !ok {
		return ""
	}
	if v := valueOf(p, "decision_id"); pyjson.Truthy(v) {
		return pyjson.Str(v)
	}
	return ""
}

func surfaceText(dir, root string, ref *string, subdir, name string) (string, string) {
	p := filepath.Join(dir, name)
	if b, err := os.ReadFile(p); err == nil {
		return string(b), "worktree"
	}
	if ref != nil && *ref != "" {
		if out, err := gitx.Run(root, "show", *ref+":kitty-specs/"+subdir+"/"+name); err == nil {
			return out + "\n", "ref"
		}
	}
	return "", "worktree"
}

func buildSurface(stream, dir, root string, ref *string, subdir string) (*SurfaceLog, []string, int) {
	text, src := surfaceText(dir, root, ref, subdir, stream)
	rows, bad := parseJSONL(text)
	log := &SurfaceLog{Source: src, Ref: ref}
	if src == "worktree" {
		log.Path = filepath.Join(dir, stream)
	} else {
		log.Path = "kitty-specs/" + subdir + "/" + stream
	}
	var ids []string
	for _, r := range rows {
		id := decisionIDFor(stream, r)
		if id == "" {
			continue
		}
		if ev, ok := r.Get("event_id"); ok && ev != nil {
			log.EventIDs = append(log.EventIDs, pyjson.Str(ev))
		}
		ids = append(ids, id)
	}
	return log, ids, bad
}

func isPrefix(a, b []string) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func setDiff(a, b []string) []string {
	m := map[string]bool{}
	for _, x := range b {
		m[x] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, x := range a {
		if !m[x] && !seen[x] {
			out = append(out, x)
			seen[x] = true
		}
	}
	sort.Strings(out)
	return out
}

func ledgerPrimary(dir string) ([]string, []string) {
	var entries, dm []string
	if b, err := os.ReadFile(filepath.Join(dir, "decisions", "index.json")); err == nil {
		entries = indexIDsFromText(string(b))
	}
	m, _ := filepath.Glob(filepath.Join(dir, "decisions", "DM-*.md"))
	for _, p := range m {
		dm = append(dm, filepath.Base(p))
	}
	return entries, dm
}

func indexIDsFromText(text string) []string {
	v, err := pyjson.Loads(text)
	if err != nil {
		return nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil
	}
	var ids []string
	for _, e := range o.GetList("entries") {
		if eo, ok := e.(*jsonx.Object); ok && eo.Has("decision_id") {
			ids = append(ids, pyjson.Str(valueOf(eo, "decision_id")))
		}
	}
	return ids
}

// DetectDecisionForks is fork.detect_decision_forks.
func DetectDecisionForks(root, slug string) *ForkReport {
	primary := project.Specs(root, slug)
	meta := mission.ReadMetaOrEmpty(primary)
	target := strings.TrimSpace(meta.GetString("target_branch"))
	var side *coordSide
	mid8 := strings.TrimSpace(meta.GetString("mid8"))
	branch := strings.TrimSpace(meta.GetString("coordination_branch"))
	if branch != "" && mid8 != "" && coord.RoutesThroughCoordination(coord.TopologyFromMeta(meta, primary)) {
		side = &coordSide{dir: lanes.CoordMissionDir(root, slug, mid8), branch: branch, subdir: mission.CoordMissionDirName(slug, mid8)}
	}
	rep := &ForkReport{}
	for _, stream := range []string{"status.events.jsonl", "decisions.events.jsonl"} {
		var tref *string
		if target != "" {
			tref = &target
		}
		pl, pids, pbad := buildSurface(stream, primary, root, tref, slug)
		if pbad > 0 {
			rep.ReconcileSteps = append(rep.ReconcileSteps, fmt.Sprintf("%s: %d malformed line(s) ignored on PRIMARY -- inspect and fix by hand.", pl.Path, pbad))
		}
		f := StreamFinding{Stream: stream, Primary: pl}
		var cids []string
		if side != nil {
			b := side.branch
			cl, ids, cbad := buildSurface(stream, side.dir, root, &b, side.subdir)
			if cbad > 0 {
				rep.ReconcileSteps = append(rep.ReconcileSteps, fmt.Sprintf("%s: %d malformed line(s) ignored on COORDINATION -- inspect and fix by hand.", cl.Path, cbad))
			}
			f.Coordination = cl
			cids = ids
		}
		switch {
		case f.Coordination == nil:
			f.State = map[bool]string{true: "single_home", false: "absent"}[len(pl.EventIDs) > 0]
		case len(pl.EventIDs) == 0 && len(f.Coordination.EventIDs) == 0:
			f.State = "absent"
		case len(pl.EventIDs) == 0 || len(f.Coordination.EventIDs) == 0:
			f.State = "single_home"
		case isPrefix(f.Coordination.EventIDs, pl.EventIDs) || isPrefix(pl.EventIDs, f.Coordination.EventIDs):
			f.State = "prefix"
		default:
			f.State = "forked"
		}
		f.OnlyPrimary = setDiff(pids, cids)
		f.OnlyCoord = setDiff(cids, pids)
		var both []string
		cm := map[string]bool{}
		for _, x := range cids {
			cm[x] = true
		}
		seen := map[string]bool{}
		for _, x := range pids {
			if cm[x] && !seen[x] {
				both = append(both, x)
				seen[x] = true
			}
		}
		sort.Strings(both)
		f.OnBoth = both
		if f.State == "forked" {
			op, oc := strings.Join(f.OnlyPrimary, ", "), strings.Join(f.OnlyCoord, ", ")
			if op == "" {
				op = "none"
			}
			if oc == "" {
				oc = "none"
			}
			rep.ReconcileSteps = append(rep.ReconcileSteps, fmt.Sprintf("%s is forked: decisions only on PRIMARY = [%s]; decisions only on COORDINATION = [%s]. This is never auto-merged (C-003) -- inspect both copies by hand, decide which decisions to keep, and record the kept ones yourself on the correct surface (never run --repair expecting it to merge or re-sequence a fork).", stream, op, oc))
			rep.Forked = true
		}
		rep.Streams = append(rep.Streams, f)
	}
	pe, pd := ledgerPrimary(primary)
	var ce, cd []string
	if side != nil && gitx.OK(root, "rev-parse", "--verify", "--quiet", side.branch+"^{commit}") {
		if out, err := gitx.Run(root, "show", side.branch+":kitty-specs/"+side.subdir+"/decisions/index.json"); err == nil {
			ce = indexIDsFromText(out)
		}
		if out, err := gitx.Run(root, "ls-tree", "--name-only", side.branch, "kitty-specs/"+side.subdir+"/decisions/"); err == nil {
			for _, l := range strings.Split(out, "\n") {
				n := filepath.Base(strings.TrimSpace(l))
				if strings.HasPrefix(n, "DM-") && strings.HasSuffix(n, ".md") {
					cd = append(cd, n)
				}
			}
		}
	}
	ph, ch := len(pe)+len(pd) > 0, len(ce)+len(cd) > 0
	switch {
	case !ph && !ch:
		rep.LedgerState = "absent"
	case ch && !ph:
		rep.LedgerState = "coordination_only"
	case ph && !ch:
		rep.LedgerState = "primary"
	default:
		rep.LedgerState = "both"
	}
	rep.LedgerOnlyEntries = setDiff(ce, pe)
	rep.LedgerOnlyDM = setDiff(cd, pd)
	if rep.LedgerCoordinationOnly() {
		note := ""
		if side != nil {
			note = fmt.Sprintf(" (branch %s)", pyjson.ReprString(side.branch))
		}
		rep.ReconcileSteps = append(rep.ReconcileSteps, fmt.Sprintf("decisions ledger exists only on the coordination branch%s for %s: run `spec-kitty doctor decisions --mission %s --repair` to copy decisions/index.json entries and DM-*.md files into the PRIMARY ledger (kitty-specs/%s/decisions/); it creates no commit -- commit the copy yourself with `spec-kitty spec-commit -m \"...\" kitty-specs/%s/decisions/` or `spec-kitty accept`.", note, slug, slug, slug, slug))
	}
	return rep
}

// DecisionsReport is DecisionsReconciliationReport.
type DecisionsReport struct {
	MissionSlug                                                  string
	LogIDs, IndexIDs, MissingFromIndex, Orphaned, MalformedFolds []string
	LossyAttribution                                             []string
	StatusMismatch                                               []*jsonx.Object
	Repaired, LedgerCopied                                       bool
	Fork                                                         *ForkReport
	Findings                                                     []string
}

// Clean reports no divergence.
func (r *DecisionsReport) Clean() bool {
	return len(r.MissingFromIndex) == 0 && len(r.Orphaned) == 0 && len(r.MalformedFolds) == 0 && len(r.StatusMismatch) == 0 && len(r.Findings) == 0
}

// foldStatus folds one decision's envelopes to its status (FoldError → "").
func foldEntry(events []*jsonx.Object) (*jsonx.Object, error) {
	var opened, resolved []*jsonx.Object
	for _, e := range events {
		switch valueOf(e, "event_type") {
		case decisionOpened:
			opened = append(opened, e)
		case decisionResolved:
			resolved = append(resolved, e)
		default:
			return nil, fmt.Errorf("unknown event type(s)")
		}
	}
	if len(opened) != 1 {
		return nil, fmt.Errorf("opened count")
	}
	p, _ := valueOf(opened[0], "payload").(*jsonx.Object)
	if p == nil {
		return nil, fmt.Errorf("payload")
	}
	need := func(o *jsonx.Object, k string) (any, error) {
		v, ok := o.Get(k)
		if !ok {
			return nil, fmt.Errorf("missing %s", k)
		}
		return v, nil
	}
	ent := &jsonx.Object{}
	for _, k := range []string{"decision_point_id", "origin_flow", "step_id", "input_key", "question", "state_entered_at", "actor_id", "mission_id", "mission_slug"} {
		if _, err := need(p, k); err != nil {
			return nil, err
		}
	}
	created, ok := pyParseAware(pyjson.Str(valueOf(p, "state_entered_at")))
	if !ok {
		return nil, fmt.Errorf("created_at")
	}
	opts := []any{}
	if l, ok := valueOf(p, "options").([]any); ok {
		opts = l
	}
	ent.Set("created_at", created)
	ent.Set("decision_id", pyjson.Str(valueOf(p, "decision_point_id")))
	ent.Set("final_answer", nil)
	ent.Set("input_key", pyjson.Str(valueOf(p, "input_key")))
	ent.Set("mission_id", pyjson.Str(valueOf(p, "mission_id")))
	ent.Set("mission_slug", pyjson.Str(valueOf(p, "mission_slug")))
	ent.Set("opened_by", pyjson.Str(valueOf(p, "actor_id")))
	ent.Set("options", opts)
	ent.Set("origin_flow", pyjson.Str(valueOf(p, "origin_flow")))
	ent.Set("other_answer", false)
	ent.Set("question", pyjson.Str(valueOf(p, "question")))
	ent.Set("rationale", nil)
	ent.Set("resolved_at", nil)
	ent.Set("resolved_by", nil)
	ent.Set("slot_key", nil)
	ent.Set("status", "open")
	ent.Set("step_id", pyjson.Str(valueOf(p, "step_id")))
	ent.Set("summary_json", nil)
	var term *jsonx.Object
	statusOf := func(e *jsonx.Object) string {
		rp, _ := valueOf(e, "payload").(*jsonx.Object)
		return pyjson.Str(valueOf(rp, "terminal_outcome"))
	}
	switch len(resolved) {
	case 0:
	case 1:
		term = resolved[0]
	case 2:
		a, b := statusOf(resolved[0]), statusOf(resolved[1])
		switch {
		case a == "deferred" && b == "resolved":
			term = resolved[1]
		case b == "deferred" && a == "resolved":
			term = resolved[0]
		default:
			return nil, fmt.Errorf("terminal pair")
		}
	default:
		return nil, fmt.Errorf("too many resolved")
	}
	if term != nil {
		rp, _ := valueOf(term, "payload").(*jsonx.Object)
		st := statusOf(term)
		if st != "resolved" && st != "deferred" && st != "canceled" {
			return nil, fmt.Errorf("outcome")
		}
		at, ok := pyParseAware(pyjson.Str(valueOf(rp, "state_entered_at")))
		if !ok || !rp.Has("resolved_by") {
			return nil, fmt.Errorf("resolved payload")
		}
		ent.Set("status", st)
		ent.Set("final_answer", valueOf(rp, "final_answer"))
		ent.Set("other_answer", pyjson.Truthy(valueOf(rp, "other_answer")))
		ent.Set("rationale", valueOf(rp, "rationale"))
		ent.Set("resolved_at", at)
		ent.Set("resolved_by", pyjson.Str(valueOf(rp, "resolved_by")))
	}
	return ent, nil
}

// pyParseAware renders a pydantic-JSON datetime ("...Z" for UTC).
func pyParseAware(s string) (string, bool) {
	t, err := clock.ParseISO(strings.Replace(s, "Z", "+00:00", 1))
	if err != nil {
		return "", false
	}
	iso := clock.ISO(t)
	if strings.HasSuffix(iso, "+00:00") {
		iso = strings.TrimSuffix(iso, "+00:00") + "Z"
	}
	return iso, true
}

func decisionEvents(eventsPath string) (map[string][]*jsonx.Object, []string, error) {
	grouped := map[string][]*jsonx.Object{}
	var order []string
	b, err := os.ReadFile(eventsPath)
	if err != nil {
		return grouped, nil, nil
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		v, err := pyjson.Loads(l)
		if err != nil {
			return nil, nil, err
		}
		o, _ := v.(*jsonx.Object)
		if et := valueOf(o, "event_type"); et != decisionOpened && et != decisionResolved {
			continue
		}
		p, _ := valueOf(o, "payload").(*jsonx.Object)
		id := valueOf(p, "decision_point_id")
		if !pyjson.Truthy(id) {
			continue
		}
		s := pyjson.Str(id)
		if _, ok := grouped[s]; !ok {
			order = append(order, s)
		}
		grouped[s] = append(grouped[s], o)
	}
	return grouped, order, nil
}

func loadIndex(ledger string) (*jsonx.Object, []*jsonx.Object) {
	b, err := os.ReadFile(filepath.Join(ledger, "decisions", "index.json"))
	if err != nil {
		return nil, nil
	}
	v, err := pyjson.Loads(string(b))
	if err != nil {
		return nil, nil
	}
	o, _ := v.(*jsonx.Object)
	var entries []*jsonx.Object
	for _, e := range o.GetList("entries") {
		if eo, ok := e.(*jsonx.Object); ok {
			entries = append(entries, eo)
		}
	}
	return o, entries
}

// eventsDirFor is _resolve_events_dir_for_doctor:
// placement_seam(...).read_dir(STATUS_STATE), degrading to a deliberately
// nonexistent placeholder on StatusReadPathNotFound (an unmaterialized or
// deleted coordination surface).
func eventsDirFor(root, slug string) string {
	d, err := placement.ReadDir(root, slug, placement.Coord)
	if err != nil {
		return filepath.Join(root, ".spec-kitty-doctor-unmaterialized-coord~"+slug)
	}
	return d
}

// DiagnoseDecisions is _diagnose (with the fork report).
func DiagnoseDecisions(root, slug string) (*DecisionsReport, error) {
	events := eventsDirFor(root, slug)
	ledger := project.Specs(root, slug)
	grouped, _, err := decisionEvents(filepath.Join(events, "status.events.jsonl"))
	if err != nil {
		return nil, err
	}
	_, entries := loadIndex(ledger)
	rep := &DecisionsReport{MissionSlug: slug}
	byID := map[string]*jsonx.Object{}
	idx := map[string]bool{}
	for _, e := range entries {
		id := pyjson.Str(valueOf(e, "decision_id"))
		byID[id] = e
		idx[id] = true
		rep.IndexIDs = append(rep.IndexIDs, id)
	}
	sort.Strings(rep.IndexIDs)
	for id := range grouped {
		rep.LogIDs = append(rep.LogIDs, id)
	}
	sort.Strings(rep.LogIDs)
	for _, id := range rep.LogIDs {
		f, ferr := foldEntry(grouped[id])
		if ferr != nil {
			rep.MalformedFolds = append(rep.MalformedFolds, id)
			continue
		}
		if ex, ok := byID[id]; ok && pyjson.Str(valueOf(ex, "status")) != f.GetString("status") {
			rep.StatusMismatch = append(rep.StatusMismatch, jsonx.NewObject("decision_id", id, "index_status", pyjson.Str(valueOf(ex, "status")), "folded_status", f.GetString("status")))
		}
		if !idx[id] {
			rep.MissingFromIndex = append(rep.MissingFromIndex, id)
		}
	}
	rep.Fork = DetectDecisionForks(root, slug)
	universe := map[string]bool{}
	for _, id := range rep.LogIDs {
		universe[id] = true
	}
	for _, s := range rep.Fork.Streams {
		for _, l := range [][]string{s.OnlyPrimary, s.OnlyCoord, s.OnBoth} {
			for _, id := range l {
				universe[id] = true
			}
		}
	}
	for _, id := range rep.IndexIDs {
		if !universe[id] {
			rep.Orphaned = append(rep.Orphaned, id)
		}
	}
	if rep.Fork.Forked {
		rep.Findings = append(rep.Findings, "DECISION_LOG_FORKED")
	}
	if rep.Fork.LedgerCoordinationOnly() {
		rep.Findings = append(rep.Findings, "DECISION_LEDGER_ONLY_ON_COORDINATION")
	}
	return rep, nil
}

// RepairDecisions rebuilds decisions/index.json from the event log (the
// non-forked --repair arm). Returns the malformed decision ids.
func RepairDecisions(root, slug string, universe map[string]bool) ([]string, error) {
	events := eventsDirFor(root, slug)
	ledger := project.Specs(root, slug)
	grouped, _, err := decisionEvents(filepath.Join(events, "status.events.jsonl"))
	if err != nil {
		return nil, err
	}
	doc, entries := loadIndex(ledger)
	byID := map[string]*jsonx.Object{}
	for _, e := range entries {
		byID[pyjson.Str(valueOf(e, "decision_id"))] = e
	}
	var ids []string
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var malformed []string
	var rebuilt []*jsonx.Object
	have := map[string]bool{}
	for _, id := range ids {
		ex := byID[id]
		f, ferr := foldEntry(grouped[id])
		if ferr != nil {
			malformed = append(malformed, id)
			if ex != nil {
				rebuilt = append(rebuilt, ex)
				have[id] = true
			}
			continue
		}
		if ex != nil {
			// keep wire-agreeing entries verbatim; otherwise take wire fields
			// from the fold and carry summary_json / slot_key.
			agree := true
			for _, k := range []string{"status", "final_answer", "other_answer", "resolved_at", "question", "input_key", "options", "origin_flow", "mission_id", "mission_slug", "opened_by", "created_at", "step_id"} {
				if !pyjson.Equal(valueOf(ex, k), valueOf(f, k)) {
					agree = false
				}
			}
			if !agree {
				f.Set("summary_json", valueOf(ex, "summary_json"))
				if pyjson.Str(valueOf(ex, "step_id")) == f.GetString("step_id") {
					f.Set("slot_key", valueOf(ex, "slot_key"))
				}
				ex = f
			}
			rebuilt = append(rebuilt, ex)
		} else {
			rebuilt = append(rebuilt, f)
		}
		have[id] = true
	}
	for _, e := range entries {
		id := pyjson.Str(valueOf(e, "decision_id"))
		if universe[id] && !have[id] {
			rebuilt = append(rebuilt, e)
		}
	}
	mid := ""
	if len(rebuilt) > 0 {
		mid = pyjson.Str(valueOf(rebuilt[0], "mission_id"))
	} else if doc != nil {
		mid = pyjson.Str(valueOf(doc, "mission_id"))
	}
	sort.SliceStable(rebuilt, func(i, j int) bool {
		ai, aj := pyjson.Str(valueOf(rebuilt[i], "created_at")), pyjson.Str(valueOf(rebuilt[j], "created_at"))
		if ai != aj {
			return ai < aj
		}
		return pyjson.Str(valueOf(rebuilt[i], "decision_id")) < pyjson.Str(valueOf(rebuilt[j], "decision_id"))
	})
	list := []any{}
	for _, e := range rebuilt {
		list = append(list, e)
	}
	out := jsonx.NewObject("entries", list, "mission_id", mid, "version", 1)
	os.MkdirAll(filepath.Join(ledger, "decisions"), 0o755)
	if err := os.WriteFile(filepath.Join(ledger, "decisions", "index.json"), []byte(jsonx.Dumps(out, jsonx.SortedPretty)+"\n"), 0o644); err != nil {
		return nil, err
	}
	return malformed, nil
}
