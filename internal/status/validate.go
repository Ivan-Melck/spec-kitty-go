package status

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// ValidationResult aggregates status validate findings.
type ValidationResult struct {
	Errors   []string
	Warnings []string
}

// Passed reports no errors.
func (r *ValidationResult) Passed() bool { return len(r.Errors) == 0 }

var uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{12}$`)

func validEventID(s string) bool {
	return ULIDPattern.MatchString(strings.ToUpper(s)) || uuidRe.MatchString(s)
}

func validISO(s string) bool {
	_, err := clock.ParseISO(strings.Replace(s, "Z", "+00:00", 1))
	return err == nil
}

// ValidateEventSchema is validate_event_schema over a to_dict() row.
func ValidateEventSchema(e *jsonx.Object) []string {
	var out []string
	for _, f := range []string{"event_id", "wp_id", "from_lane", "to_lane", "at", "actor", "force", "execution_mode"} {
		if !e.Has(f) {
			out = append(out, "Missing required field: "+f)
		}
	}
	if !e.Has("mission_slug") && !e.Has("feature_slug") {
		out = append(out, "Missing required field: mission_slug (or legacy mission_slug)")
	}
	id := PyStr(valueOr(e, "event_id", ""))
	if id != "" && !validEventID(id) {
		out = append(out, "Invalid event ID format: "+id)
	}
	if v, ok := e.Get("from_lane"); ok && v != nil {
		s := PyStr(v)
		if !IsDisplay(Lane(s)) && s != string(Genesis) {
			out = append(out, "from_lane is not canonical: "+s)
		}
	}
	if v, ok := e.Get("to_lane"); ok && v != nil {
		if s := PyStr(v); !IsDisplay(Lane(s)) {
			out = append(out, "to_lane is not canonical: "+s)
		}
	}
	if v, ok := e.Get("at"); ok && v != nil && !validISO(PyStr(v)) {
		out = append(out, fmt.Sprintf("Event %s: invalid ISO 8601 timestamp: %s", id, PyStr(v)))
	}
	if v, ok := e.Get("force"); ok && v != nil {
		if _, isBool := v.(bool); !isBool {
			out = append(out, fmt.Sprintf("Event %s: force must be boolean, got %s", id, pyTypeName(v)))
		}
	}
	if v, ok := e.Get("execution_mode"); ok && v != nil {
		if s := PyStr(v); s != "worktree" && s != "direct_repo" {
			out = append(out, fmt.Sprintf("Event %s: execution_mode must be 'worktree' or 'direct_repo', got '%s'", id, s))
		}
	}
	if f, _ := e.Get("force"); f == true && !truthyAny(valueOr(e, "reason", nil)) {
		out = append(out, fmt.Sprintf("Event %s: force=true without reason", id))
	}
	if e.GetString("from_lane") == string(ForReview) && e.GetString("to_lane") == string(InProgress) && !truthyAny(valueOr(e, "review_ref", nil)) {
		out = append(out, fmt.Sprintf("Event %s: for_review->in_progress without review_ref", id))
	}
	return out
}

func pyTypeName(v any) string {
	switch v.(type) {
	case string:
		return "str"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case []any:
		return "list"
	case *jsonx.Object:
		return "dict"
	}
	return fmt.Sprintf("%T", v)
}

func sortByAtID(events []*jsonx.Object) []*jsonx.Object {
	s := append([]*jsonx.Object(nil), events...)
	sort.SliceStable(s, func(i, j int) bool {
		ai, aj := PyStr(valueOr(s[i], "at", "")), PyStr(valueOr(s[j], "at", ""))
		if ai != aj {
			return ai < aj
		}
		return PyStr(valueOr(s[i], "event_id", "")) < PyStr(valueOr(s[j], "event_id", ""))
	})
	return s
}

// ValidateTransitionLegality replays unforced transitions through the FSM edges.
func ValidateTransitionLegality(events []*jsonx.Object) []string {
	var out []string
	for _, e := range sortByAtID(events) {
		f, okf := e.Get("from_lane")
		t, okt := e.Get("to_lane")
		if !okf || !okt || f == nil || t == nil {
			continue
		}
		if b, _ := e.Get("force"); truthyAny(b) {
			continue
		}
		from, to := ResolveLaneAlias(PyStr(f)), ResolveLaneAlias(PyStr(t))
		if !IsLane(from) || !IsLane(to) || !MayTransition(Lane(from), Lane(to)) {
			out = append(out, fmt.Sprintf("Event %s: illegal transition %s -> %s", PyStr(valueOr(e, "event_id", "unknown")), PyStr(f), PyStr(t)))
		}
	}
	return out
}

// ValidateTimestampContinuity flags clock-skew ordering anomalies (warnings).
func ValidateTimestampContinuity(events []*jsonx.Object) []string {
	var out []string
	current := map[string]string{}
	for _, e := range sortByAtID(events) {
		f, okf := e.Get("from_lane")
		t, okt := e.Get("to_lane")
		w, okw := e.Get("wp_id")
		if !okf || !okt || !okw || f == nil || t == nil || w == nil {
			continue
		}
		if strings.HasPrefix(PyStr(valueOr(e, "actor", "")), MigrationActorPrefix) {
			continue
		}
		rf, rt, wp := ResolveLaneAlias(PyStr(f)), ResolveLaneAlias(PyStr(t)), PyStr(w)
		prior, seen := current[wp]
		force, _ := e.Get("force")
		if !seen || truthyAny(force) {
			current[wp] = rt
			continue
		}
		if rf != prior {
			out = append(out, fmt.Sprintf("Event %s: from_lane '%s' is not the lane the preceding events produce ('%s') -- a clock-skew ordering anomaly; the reducer re-orders it, but this log's timestamps are out of causal order (see spec-kitty#4941)",
				PyStr(valueOr(e, "event_id", "unknown")), PyStr(f), prior))
			continue
		}
		current[wp] = rt
	}
	return out
}

// ValidateDoneEvidence checks unforced done transitions carry evidence.
func ValidateDoneEvidence(events []*jsonx.Object) []string {
	var out []string
	for _, e := range events {
		if e.GetString("to_lane") != string(Done) {
			continue
		}
		id := PyStr(valueOr(e, "event_id", "unknown"))
		if f, _ := e.Get("force"); truthyAny(f) {
			continue
		}
		ev, _ := e.Get("evidence")
		if !truthyAny(ev) {
			out = append(out, fmt.Sprintf("Event %s: done without evidence (not forced)", id))
			continue
		}
		eo, ok := ev.(*jsonx.Object)
		if !ok {
			out = append(out, fmt.Sprintf("Event %s: done evidence is not a dict", id))
			continue
		}
		rv, _ := eo.Get("review")
		if !truthyAny(rv) {
			out = append(out, fmt.Sprintf("Event %s: done evidence missing review section", id))
			continue
		}
		ro, ok := rv.(*jsonx.Object)
		if !ok {
			out = append(out, fmt.Sprintf("Event %s: done evidence review is not a dict", id))
			continue
		}
		if !truthyAny(valueOr(ro, "reviewer", nil)) {
			out = append(out, fmt.Sprintf("Event %s: done evidence missing reviewer identity", id))
		}
		if !truthyAny(valueOr(ro, "verdict", nil)) {
			out = append(out, fmt.Sprintf("Event %s: done evidence missing verdict", id))
		}
		if !truthyAny(valueOr(ro, "reference", nil)) {
			out = append(out, fmt.Sprintf("Event %s: done evidence missing approval reference", id))
		}
	}
	return out
}

func canonical(v any) string { return jsonx.Dumps(v, jsonx.Options{SortKeys: true}) }

// ValidateMaterializationDrift compares status.json with the reducer output.
func ValidateMaterializationDrift(dir string) ([]string, error) {
	statusPath, eventsPath := filepath.Join(dir, SnapshotFile), EventsPath(dir)
	_, se := os.Stat(statusPath)
	_, ee := os.Stat(eventsPath)
	if ee != nil {
		if se == nil {
			return []string{"status.json exists but status.events.jsonl is missing"}, nil
		}
		return nil, nil
	}
	if se != nil {
		return []string{"status.events.jsonl exists but status.json is missing (run 'spec-kitty agent status materialize' to generate)"}, nil
	}
	b, err := os.ReadFile(statusPath)
	if err != nil {
		return nil, err
	}
	disk, err := jsonx.DecodeObject(b)
	if err != nil {
		return nil, fmt.Errorf("Cannot read %s: %v", statusPath, err)
	}
	snap, err := MaterializeSnapshot(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	diskWPs := disk.GetObject("work_packages")
	if diskWPs == nil {
		diskWPs = &jsonx.Object{}
	}
	if canonical(diskWPs) != canonical(snap.WorkPackages) {
		ids := map[string]bool{}
		for _, k := range diskWPs.Keys() {
			ids[k] = true
		}
		for _, k := range snap.WorkPackages.Keys() {
			ids[k] = true
		}
		var sorted []string
		for k := range ids {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, id := range sorted {
			d, x := diskWPs.GetObject(id), snap.WorkPackages.GetObject(id)
			switch {
			case d == nil:
				l := "?"
				if x != nil && x.Has("lane") {
					l = x.GetString("lane")
				}
				out = append(out, fmt.Sprintf("Materialization drift: %s missing from status.json (expected lane=%s)", id, l))
			case x == nil:
				out = append(out, fmt.Sprintf("Materialization drift: %s in status.json but not in reducer output", id))
			case d.GetString("lane") != x.GetString("lane"):
				out = append(out, fmt.Sprintf("Materialization drift: %s lane=%s in status.json but reducer says lane=%s", id, PyStr(valueOr(d, "lane", nil)), PyStr(valueOr(x, "lane", nil))))
			case canonical(d) != canonical(x):
				out = append(out, fmt.Sprintf("Materialization drift: %s state differs between status.json and reducer output", id))
			}
		}
	}
	dr, _ := disk.Get("retrospective")
	var er any
	if snap.Retrospective != nil {
		er = snap.Retrospective
	}
	if canonical(dr) != canonical(er) {
		out = append(out, "Materialization drift: retrospective snapshot differs between status.json and reducer output")
	}
	if ec, _ := disk.Get("event_count"); canonical(ec) != canonical(snap.EventCount) {
		out = append(out, fmt.Sprintf("Materialization drift: event_count=%s in status.json but reducer counted %d", PyStr(ec), snap.EventCount))
	}
	var last any
	if snap.LastEventID != nil {
		last = *snap.LastEventID
	}
	if le, _ := disk.Get("last_event_id"); canonical(le) != canonical(last) {
		out = append(out, "Materialization drift: last_event_id mismatch between status.json and reducer output")
	}
	return out, nil
}

// Validate runs every status validation check over dir; hasEvents=false
// when the log carries no rows.
func Validate(dir string) (res *ValidationResult, hasEvents bool) {
	res = &ValidationResult{Errors: []string{}, Warnings: []string{}}
	rows, err := ReadRows(dir)
	if err != nil {
		res.Errors = append(res.Errors, "status.events.jsonl is corrupt: "+err.Error())
		return res, true
	}
	if len(rows) == 0 {
		return res, false
	}
	stream, err := ReadStream(dir, "")
	if err != nil {
		res.Errors = append(res.Errors, "status.events.jsonl is corrupt: "+err.Error())
		return res, true
	}
	var events []*jsonx.Object
	for _, e := range stream.Transitions {
		events = append(events, e.ToObject())
	}
	for _, e := range events {
		res.Errors = append(res.Errors, ValidateEventSchema(e)...)
	}
	res.Errors = append(res.Errors, ValidateTransitionLegality(events)...)
	res.Errors = append(res.Errors, ValidateDoneEvidence(events)...)
	res.Warnings = append(res.Warnings, ValidateTimestampContinuity(events)...)
	drift, err := ValidateMaterializationDrift(dir)
	if err != nil {
		res.Errors = append(res.Errors, "status.events.jsonl is corrupt: "+err.Error())
		return res, true
	}
	res.Errors = append(res.Errors, drift...)
	return res, true
}
