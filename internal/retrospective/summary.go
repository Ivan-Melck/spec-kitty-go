package retrospective

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// MissionDirs is iter_mission_instance_dirs: kitty-specs/* dirs carrying a
// meta.json or retrospective.yaml.
func MissionDirs(project string) []string {
	entries, err := os.ReadDir(filepath.Join(project, "kitty-specs"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		d := filepath.Join(project, "kitty-specs", e.Name())
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		if exists(filepath.Join(d, "meta.json")) || exists(filepath.Join(d, FileName)) {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// LoadRecord reads a generator-shaped record (nil, reason on failure).
func LoadRecord(path string) (*jsonx.Object, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "OSError: " + err.Error()
	}
	v, err := pyyaml.SafeLoad(string(b))
	if err != nil {
		return nil, err.Error()
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, "record is not a mapping"
	}
	for _, k := range []string{"mission_slug", "findings_status", "helped", "not_helpful", "gaps", "proposals"} {
		if !o.Has(k) {
			return nil, "missing required field: " + k
		}
	}
	return o, ""
}

func recordPath(project, dir string) string {
	meta := mission.ReadMetaOrEmpty(dir)
	if s := meta.GetString("mission_slug"); s != "" {
		if p := filepath.Join(project, "kitty-specs", s, FileName); exists(p) {
			return p
		}
	} else if s := meta.GetString("slug"); s != "" {
		if p := filepath.Join(project, "kitty-specs", s, FileName); exists(p) {
			return p
		}
	}
	in := filepath.Join(dir, FileName)
	if exists(in) {
		return in
	}
	if id := meta.GetString("mission_id"); id != "" {
		if p := filepath.Join(project, ".kittify", "missions", id, FileName); exists(p) {
			return p
		}
	}
	return in
}

var trancheCutoff = time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)

type counter map[string]int

func (c counter) top(keyName string, limit int) []any {
	var ks []string
	for k := range c {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool {
		if c[ks[i]] != c[ks[j]] {
			return c[ks[i]] > c[ks[j]]
		}
		return ks[i] < ks[j]
	})
	out := []any{}
	for i, k := range ks {
		if i >= limit {
			break
		}
		out = append(out, jsonx.NewObject(keyName, k, "count", c[k]))
	}
	return out
}

// BuildSummary is build_summary (generator-shaped records).
func BuildSummary(project string, since *time.Time, limit int) *jsonx.Object {
	generated := clock.NowUTCISO()
	var missionCount, completed, inFlight, legacy, terminus int
	malformed := []any{}
	nh, terms, under := counter{}, counter{}, counter{}
	total := 0
	for _, dir := range MissionDirs(project) {
		path := recordPath(project, dir)
		if !exists(path) {
			meta := mission.ReadMetaOrEmpty(dir)
			started := ""
			for _, k := range []string{"mission_started_at", "created_at", "started_at"} {
				if s := meta.GetString(k); s != "" {
					started = s
					break
				}
			}
			st := meta.GetString("status")
			if st == "" {
				st = meta.GetString("current_lane")
			}
			isLegacy := false
			if started != "" {
				if t, err := clock.ParseISO(started); err == nil && t.Before(trancheCutoff) {
					isLegacy = true
				}
			}
			switch {
			case isLegacy:
				legacy++
			case st != "" && st != "done" && st != "canceled" && st != "failed" && st != "completed":
				inFlight++
			default:
				terminus++
			}
			missionCount++
			continue
		}
		rec, reason := LoadRecord(path)
		if rec == nil {
			malformed = append(malformed, jsonx.NewObject("mission_id", nil, "path", path, "reason", reason))
			missionCount++
			continue
		}
		if since != nil {
			if t, err := clock.ParseISO(rec.GetString("created_at")); err == nil {
				d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
				if d.Before(*since) {
					continue
				}
			}
		}
		missionCount++
		completed++
		for _, f := range rec.GetList("not_helpful") {
			if fo, ok := f.(*jsonx.Object); ok {
				nh["retrospective:not_helpful:"+fo.GetString("category")]++
			}
		}
		for _, f := range rec.GetList("gaps") {
			if fo, ok := f.(*jsonx.Object); ok {
				switch fo.GetString("category") {
				case "doc":
					terms[fo.GetString("summary")]++
				case "spec_quality":
					under["retrospective:gaps:spec_quality"]++
				}
			}
		}
		total += len(rec.GetList("proposals"))
	}
	return jsonx.NewObject("project_path", project, "generated_at", generated, "mission_count", missionCount,
		"completed_count", completed, "skipped_count", 0, "failed_count", 0, "in_flight_count", inFlight,
		"legacy_no_retro_count", legacy, "terminus_no_retro_count", terminus, "malformed", malformed,
		"not_helpful_top", nh.top("urn", limit), "missing_terms_top", terms.top("key", limit), "missing_edges_top", []any{},
		"over_inclusion_top", []any{}, "under_inclusion_top", under.top("urn", limit),
		"proposal_acceptance", jsonx.NewObject("total", total, "accepted", 0, "rejected", 0, "applied", 0, "pending", total, "superseded", 0),
		"skip_reasons_top", []any{})
}

func latestEvent(dir, typ string) *jsonx.Object {
	var best *jsonx.Object
	bestLp := -1
	for _, e := range loadEvents(dir) {
		if e.GetString("type") != typ {
			continue
		}
		lp, ok := e.GetInt("lamport")
		if !ok {
			lp = 0
		}
		if best == nil || lp >= bestLp {
			best, bestLp = e, lp
		}
	}
	return best
}

// ClassifyRecord is classify_mission_record.
func ClassifyRecord(dir string) string {
	p := filepath.Join(dir, FileName)
	if exists(p) {
		b, _ := os.ReadFile(p)
		if v, err := pyyaml.SafeLoad(string(b)); err == nil {
			if o, ok := v.(*jsonx.Object); ok {
				st := "ran_no_findings"
				if raw, ok := o.Get("findings_status"); ok && raw != nil {
					st = strings.TrimSpace(asString(raw))
				}
				if st == "has_findings" || st == "ran_no_findings" {
					return st
				}
			}
		}
		return "has_findings"
	}
	failed := latestEvent(dir, "RetrospectiveCaptureFailed")
	captured := latestEvent(dir, "RetrospectiveCaptured")
	if failed != nil {
		f, _ := failed.GetInt("lamport")
		c := 0
		if captured != nil {
			c, _ = captured.GetInt("lamport")
		}
		if f > c {
			return "failed"
		}
	}
	return "missing"
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return jsonx.Dumps(v, jsonx.Options{Indent: -1})
}

// MissionStates is summary_cmd's per-mission classification.
func MissionStates(project, filter string) ([]any, *jsonx.Object) {
	missions := []any{}
	agg := jsonx.NewObject("has_findings", 0, "ran_no_findings", 0, "missing", 0, "failed", 0)
	for _, dir := range MissionDirs(project) {
		meta := mission.ReadMetaOrEmpty(dir)
		id := meta.GetString("mission_id")
		slug := meta.GetString("mission_slug")
		if slug == "" {
			slug = meta.GetString("slug")
		}
		cdir := dir
		if slug != "" {
			if k := filepath.Join(project, "kitty-specs", slug); exists(k) {
				cdir = k
			}
		}
		state := ClassifyRecord(cdir)
		if state == "missing" && id != "" {
			reg := filepath.Join(project, ".kittify", "missions", id)
			if exists(filepath.Join(reg, FileName)) {
				state = ClassifyRecord(reg)
			}
		}
		n, _ := agg.GetInt(state)
		agg.Set(state, n+1)
		var ps any
		if ev := latestEvent(cdir, "RetrospectiveCaptured"); ev != nil {
			if o := ev.GetObject("policy_source"); o != nil {
				ps = o
			}
		}
		if id == "" {
			id = filepath.Base(dir)
		}
		if filter == "" || filter == state {
			missions = append(missions, jsonx.NewObject("mission_id", id, "mission_slug", slug, "findings_status", state, "policy_source", ps))
		}
	}
	return missions, agg
}
