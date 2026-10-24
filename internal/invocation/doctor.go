package invocation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// This file ports the doctor surfaces over the Op trail:
// specify_cli/doctor/ops.py (orphan Ops, the stale sweep closing them on the
// op-closures spine) and invocation/lifecycle.py's doctor_orphan_report
// (kitty-ops/lifecycle.jsonl pairing).

var nonOpJSONL = map[string]bool{IndexFile: true, "lifecycle.jsonl": true, "propagation-errors.jsonl": true, ClosuresFile: true}

func hasCompletedEvent(p string) bool {
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		o, err := decodeLine(l)
		if err != nil || o == nil {
			continue
		}
		if v, _ := o.Get("event"); v == "completed" {
			return true
		}
	}
	return false
}

// ListOrphanOps is list_orphan_ops: open Op records (absolute paths, sorted)
// with no `completed` event and no closure on the spine.
func ListOrphanOps(root string) []string {
	dir := filepath.Join(root, EventsDir)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil
	}
	m, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	sort.Strings(m)
	closed := ClosedIDs(root)
	var out []string
	for _, p := range m {
		name := filepath.Base(p)
		if nonOpJSONL[name] || closed[strings.TrimSuffix(name, ".jsonl")] || hasCompletedEvent(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// SweepEntry is SweepOpEntry.
type SweepEntry struct {
	InvocationID string
	ProfileID    string
	StartedAt    string
	AgeHours     *float64
	ActionTaken  string // none | closed_abandoned | already_closed
	ParseWarning string
	Error        string
	Commit       string // committed | not_committed:<refusal>
}

// Object is SweepOpEntry.to_dict.
func (e *SweepEntry) Object() *jsonx.Object {
	var age any
	if e.AgeHours != nil {
		age = jsonx.Float(*e.AgeHours)
	}
	o := jsonx.NewObject("invocation_id", e.InvocationID, "profile_id", e.ProfileID, "started_at", e.StartedAt,
		"age_hours", age, "action_taken", e.ActionTaken)
	if e.ParseWarning != "" {
		o.Set("parse_warning", e.ParseWarning)
	}
	if e.Error != "" {
		o.Set("error", e.Error)
	}
	if e.Commit != "" {
		o.Set("commit", e.Commit)
	}
	return o
}

// SweepReport is the doctor ops --close-stale report.
type SweepReport struct {
	OpenOps        []*SweepEntry
	Swept          int
	SkippedFresh   int
	ThresholdHours float64
	// Warnings are the logger.warning records the sweep emitted (auto-commit
	// warnings / refusals), in order; the CLI logs them in human mode.
	Warnings []string
}

// HasErrors reports per-op failures.
func (r *SweepReport) HasErrors() bool {
	for _, e := range r.OpenOps {
		if e.Error != "" {
			return true
		}
	}
	return false
}

// ClosureSpine is OP_CLOSURES_RELATIVE_PATH.
const ClosureSpine = EventsDir + "/" + ClosuresFile

// Object is SweepReport.to_dict.
func (r *SweepReport) Object() *jsonx.Object {
	ops := []any{}
	for _, e := range r.OpenOps {
		ops = append(ops, e.Object())
	}
	return jsonx.NewObject("open_ops", ops, "swept", r.Swept, "skipped_fresh", r.SkippedFresh,
		"threshold_hours", jsonx.Float(r.ThresholdHours), "closure_spine", ClosureSpine)
}

func readStartedFields(p string) (string, string) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", ""
	}
	lines := splitLinesPy(string(b))
	if len(lines) == 0 {
		return "", ""
	}
	var v any
	if json.Unmarshal([]byte(lines[0]), &v) != nil {
		return "", ""
	}
	o, err := decodeLine(lines[0])
	if err != nil || o == nil {
		return "", ""
	}
	return pyTruthStr(o, "profile_id"), pyTruthStr(o, "started_at")
}

// pyTruthStr is str(event.get(key) or "").
func pyTruthStr(o *jsonx.Object, key string) string {
	v, ok := o.Get(key)
	if !ok || v == nil || v == false || v == "" {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return reprAny(v)
}

// splitLinesPy is str.splitlines() for \n / \r\n / \r.
func splitLinesPy(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func ageHours(startedAt string, now time.Time) *float64 {
	t, err := clock.ParseISO(startedAt)
	if err != nil {
		return nil
	}
	us := now.Sub(t).Microseconds()
	h := float64(us) / 1e6 / 3600.0
	return &h
}

// errRepr renders repr(exc) for the sweep's per-op error column.
func errRepr(err error) string {
	switch e := err.(type) {
	case *Error:
		return "InvocationError(" + pyRepr(e.Msg) + ")"
	case *WriteError:
		return "InvocationWriteError(" + pyRepr(e.Msg) + ")"
	}
	return "Exception(" + pyRepr(err.Error()) + ")"
}

// closeOnSpine is ProfileInvocationExecutor._close_on_spine.
func closeOnSpine(root, id string, c *Completed) error {
	p, err := RecordPath(root, id)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return &Error{"Invocation record is unreadable: " + id}
	}
	for _, l := range splitLinesPy(string(b)) {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var v any
		if json.Unmarshal([]byte(l), &v) != nil {
			return &Error{"Invocation record is unreadable: " + id}
		}
		if m, ok := v.(map[string]any); ok && m["event"] == "completed" {
			return &AlreadyClosedError{id}
		}
	}
	sp := filepath.Join(root, EventsDir, ClosuresFile)
	if err := os.MkdirAll(filepath.Dir(sp), 0o755); err != nil {
		return &WriteError{err.Error()}
	}
	if err := appendNoFollow(sp, c.Line()+"\n"); err != nil {
		return &WriteError{"Failed to append op closure: " + err.Error()}
	}
	return nil
}

// sweepClose is complete_invocation(outcome="abandoned",
// closed_by="doctor_sweep") with the per-executor spine snapshot.
func sweepClose(root, id string, closed map[string]bool) (*CommitOutcome, error) {
	p, err := RecordPath(root, id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, &Error{"Invocation record not found: " + id}
	}
	lines := splitLinesPy(string(b))
	if len(lines) == 0 {
		return nil, errors.New("list index out of range")
	}
	var v any
	if json.Unmarshal([]byte(lines[0]), &v) != nil {
		return nil, &Error{"Invalid invocation record: " + id}
	}
	if closed[id] {
		return nil, &AlreadyClosedError{id}
	}
	c := &Completed{InvocationID: id, CompletedAt: clock.NowUTCISO(), Outcome: "abandoned", ClosedBy: "doctor_sweep"}
	if err := closeOnSpine(root, id, c); err != nil {
		return nil, err
	}
	closed[id] = true
	first, err := readStarted(root, id)
	if err != nil {
		return &CommitOutcome{Refusal: errRepr(err)}, nil
	}
	return commitOp(root, id, first, true), nil
}

// CloseStaleOps is close_stale_ops: close open Ops older than
// thresholdHours (0 = all) as abandoned on the op-closures spine.
func CloseStaleOps(root string, thresholdHours float64, now time.Time) *SweepReport {
	rep := &SweepReport{ThresholdHours: thresholdHours}
	orphans := ListOrphanOps(root)
	if len(orphans) == 0 {
		return rep
	}
	var closed map[string]bool
	for _, p := range orphans {
		id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		profile, started := readStartedFields(p)
		age := ageHours(started, now)
		e := &SweepEntry{InvocationID: id, ProfileID: profile, StartedAt: started, AgeHours: age, ActionTaken: "none"}
		if age == nil {
			e.ParseWarning = "unparseable started_at " + pyRepr(started) + "; treated as stale"
		}
		stale := thresholdHours == 0 || age == nil || *age > thresholdHours
		if !stale {
			rep.SkippedFresh++
			rep.OpenOps = append(rep.OpenOps, e)
			continue
		}
		if closed == nil {
			closed = ClosedIDs(root)
		}
		out, err := sweepClose(root, id, closed)
		var ac *AlreadyClosedError
		switch {
		case errors.As(err, &ac):
			e.ActionTaken = "already_closed"
		case err != nil:
			e.Error = errRepr(err)
		default:
			e.ActionTaken = "closed_abandoned"
			rep.Swept++
			if out != nil && out.Warning != "" {
				rep.Warnings = append(rep.Warnings, out.Warning)
			}
			if out != nil && !out.Committed && out.Refusal != "not a git worktree" {
				rep.Warnings = append(rep.Warnings, "Op record auto-commit failed for "+id+": "+out.Refusal)
			}
			if out != nil && out.Committed {
				e.Commit = "committed"
			} else if out != nil {
				r := out.Refusal
				if r == "" {
					r = "unknown refusal"
				}
				e.Commit = "not_committed:" + r
			}
		}
		rep.OpenOps = append(rep.OpenOps, e)
	}
	return rep
}

// LifecycleRecord is ProfileInvocationRecord (kitty-ops/lifecycle.jsonl).
type LifecycleRecord struct {
	CanonicalActionID string
	Phase             string
	At                time.Time
	Agent             string
	MissionID         string
	WPID              *string
}

// ReadLifecycleRecords is read_lifecycle_records (malformed lines skipped).
func ReadLifecycleRecords(root string) []LifecycleRecord {
	b, err := os.ReadFile(filepath.Join(root, EventsDir, "lifecycle.jsonl"))
	if err != nil {
		return nil
	}
	var out []LifecycleRecord
	for _, l := range splitLinesPy(string(b)) {
		if strings.TrimSpace(l) == "" {
			continue
		}
		o, err := decodeLine(l)
		if err != nil || o == nil {
			continue
		}
		at, _ := o.Get("at")
		as, ok := at.(string)
		if !ok || as == "" {
			continue
		}
		t, err := clock.ParseISO(as)
		if err != nil {
			continue
		}
		phase, _ := o.Get("phase")
		if phase != "started" && phase != "completed" && phase != "failed" {
			continue
		}
		var vals [3]string
		missing := false
		for i, k := range []string{"canonical_action_id", "agent", "mission_id"} {
			v, ok := o.Get(k)
			if !ok {
				missing = true
				break
			}
			vals[i] = pyStrAny(v)
		}
		if missing {
			continue
		}
		r := LifecycleRecord{CanonicalActionID: vals[0], Phase: phase.(string), At: t, Agent: vals[1], MissionID: vals[2]}
		if v, ok := o.Get("wp_id"); ok && v != nil {
			s := pyStrAny(v)
			r.WPID = &s
		}
		out = append(out, r)
	}
	return out
}

func pyStrAny(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	}
	return jsonx.Dumps(v, jsonx.Options{Indent: -1})
}

type lifecycleGroup struct {
	key     [2]string
	records []LifecycleRecord
}

func groupByAction(recs []LifecycleRecord) []*lifecycleGroup {
	idx := map[[2]string]*lifecycleGroup{}
	var order []*lifecycleGroup
	for _, r := range recs {
		k := [2]string{r.MissionID, r.CanonicalActionID}
		g := idx[k]
		if g == nil {
			g = &lifecycleGroup{key: k}
			idx[k] = g
			order = append(order, g)
		}
		g.records = append(g.records, r)
	}
	return order
}

// DoctorOrphanReport is doctor_orphan_report (JSON keys sorted by the caller).
func DoctorOrphanReport(root string) *jsonx.Object {
	recs := ReadLifecycleRecords(root)
	orphans := []any{}
	for _, g := range groupByAction(recs) {
		var started []LifecycleRecord
		completions := 0
		for _, r := range g.records {
			if r.Phase == "started" {
				started = append(started, r)
			} else {
				completions++
			}
		}
		deficit := len(started) - completions
		if deficit <= 0 {
			continue
		}
		for _, r := range started[len(started)-deficit:] {
			var wp any
			if r.WPID != nil {
				wp = *r.WPID
			}
			orphans = append(orphans, jsonx.NewObject("canonical_action_id", r.CanonicalActionID, "agent", r.Agent,
				"mission_id", r.MissionID, "wp_id", wp, "started_at", clock.ISO(r.At)))
		}
	}
	startedN, completions := 0, 0
	for _, r := range recs {
		if r.Phase == "started" {
			startedN++
		} else {
			completions++
		}
	}
	var rate any = jsonx.Float(1.0)
	if startedN > 0 {
		rate = jsonx.Float(float64(min(startedN, completions)) / float64(startedN))
	}
	return jsonx.NewObject("orphan_count", len(orphans), "orphans", orphans,
		"total_groups", len(groupByAction(recs)), "pairing_rate", rate)
}
