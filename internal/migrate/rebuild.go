package migrate

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// Legacy event-log rebuild (migration/rebuild_state.py rebuild_event_log,
// reached through mission_state.rebuild_mission_event_log).

const migrationEpoch = "2026-01-01T00:00:00+00:00"

var crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// DeterministicID is rebuild_state._deterministic_id.
func DeterministicID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	v := new(big.Int).SetBytes(sum[:16])
	out := make([]byte, 26)
	mask := big.NewInt(31)
	for i := 25; i >= 0; i-- {
		d := new(big.Int).And(v, mask).Int64()
		out[i] = crockford[d]
		v.Rsh(v, 5)
	}
	return string(out)
}

// RebuildResult is rebuild_state.RebuildResult.
type RebuildResult struct {
	Slug                                  string
	Generated, Kept, Corrected, Conflicts int
	Warnings, Errors                      []string
	Skipped                               bool
}

var laneOrder = []string{"planned", "claimed", "in_progress", "for_review", "done"}

func resolveLegacyAlias(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	switch l {
	case "doing":
		return "in_progress"
	case "review":
		return "for_review"
	case "complete":
		return "done"
	case "cancelled":
		return "canceled"
	}
	return l
}

func pyParseISO(s string) (time.Time, bool) {
	t, err := clock.ParseISO(strings.Replace(s, "Z", "+00:00", 1))
	return t, err == nil
}

func migrationTimestamp(base string, offset int) string {
	t, ok := pyParseISO(base)
	if !ok {
		t, _ = pyParseISO(migrationEpoch)
	}
	return clock.ISO(t.Add(-time.Duration(offset) * time.Second))
}

func readEventRows(dir string) []*jsonx.Object {
	b, err := os.ReadFile(filepath.Join(dir, "status.events.jsonl"))
	if err != nil {
		return nil
	}
	var out []*jsonx.Object
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		v, err := pyjson.Loads(s)
		if err != nil {
			warnf("Skipping corrupt event line in %s: %v", filepath.Join(dir, "status.events.jsonl"), err)
			continue
		}
		if o, ok := v.(*jsonx.Object); ok {
			out = append(out, o)
		}
	}
	return out
}

func readStatusJSON(dir string) *jsonx.Object {
	b, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		return nil
	}
	v, err := pyjson.Loads(string(b))
	if err != nil {
		warnf("Cannot read status.json in %s: %v", dir, err)
		return nil
	}
	o, _ := v.(*jsonx.Object)
	return o
}

var wpCodeRe = regexp.MustCompile(`^(WP\d{2,})`)

// wpFiles lists tasks/WP*.md (sorted) with their wp codes.
func wpFiles(dir string) [][2]string {
	m, _ := filepath.Glob(filepath.Join(dir, "tasks", "WP*.md"))
	sort.Strings(m)
	var out [][2]string
	for _, p := range m {
		stem := strings.TrimSuffix(filepath.Base(p), ".md")
		if c := wpCodeRe.FindStringSubmatch(stem); c != nil {
			out = append(out, [2]string{c[1], p})
		}
	}
	return out
}

func frontmatterLanes(dir string) map[string]string {
	lanes := map[string]string{}
	for _, f := range wpFiles(dir) {
		d, err := fm.Read(f[1])
		if err != nil {
			continue
		}
		raw := "planned"
		if v, ok := d.Meta.Get("lane"); ok && pyjson.Truthy(v) {
			raw = pyjson.Str(v)
		}
		lanes[f[0]] = resolveLegacyAlias(raw)
	}
	return lanes
}

func deriveMigrationTimestamp(dir string) string {
	var cands []string
	for _, e := range readEventRows(dir) {
		if s, ok := valueOf(e, "at").(string); ok && s != "" {
			cands = append(cands, s)
		}
	}
	if st := readStatusJSON(dir); st != nil {
		if s, ok := valueOf(st, "materialized_at").(string); ok && s != "" {
			cands = append(cands, s)
		}
		if wps := st.GetObject("work_packages"); wps != nil {
			for _, kv := range wps.Pairs() {
				if o, ok := kv.Value.(*jsonx.Object); ok {
					if s, ok := valueOf(o, "last_transition_at").(string); ok && s != "" {
						cands = append(cands, s)
					}
				}
			}
		}
	}
	if s, ok := valueOf(mission.ReadMetaOrEmpty(dir), "created_at").(string); ok && s != "" {
		cands = append(cands, s)
	}
	if len(cands) == 0 {
		return migrationEpoch
	}
	sort.Strings(cands)
	t, ok := pyParseISO(cands[len(cands)-1])
	if !ok {
		return migrationEpoch
	}
	return clock.ISO(t.Add(time.Second))
}

func valueOf(o *jsonx.Object, k string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
}

func buildChain(wp, slug string, wpIDs map[string]string, target, ts string) []*jsonx.Object {
	if target == "planned" {
		return nil
	}
	var path []string
	switch {
	case target == "done":
		path = append([]string(nil), laneOrder...)
	default:
		path = []string{"planned"}
		found := false
		for _, l := range laneOrder {
			if l != "planned" {
				path = append(path, l)
			}
			if l == target {
				found = true
				break
			}
		}
		if !found {
			path = []string{"planned", "claimed", "in_progress", target}
		}
	}
	if target == "approved" {
		path = append([]string(nil), laneOrder...)
	}
	var out []*jsonx.Object
	for i := 0; i+1 < len(path); i++ {
		offset := (len(path) - 2 - i) * 300
		e := jsonx.NewObject("event_id", DeterministicID(slug, wp, path[i], path[i+1], "chain", fmt.Sprint(i)),
			"mission_slug", slug, "wp_id", wp, "from_lane", path[i], "to_lane", path[i+1], "at", migrationTimestamp(ts, offset),
			"actor", "migration", "force", true, "execution_mode", "unknown", "reason", "bootstrapped from legacy state",
			"review_ref", nil, "evidence", nil)
		if id := wpIDs[wp]; id != "" {
			e.Set("work_package_id", id)
		}
		out = append(out, e)
	}
	return out
}

// RebuildEventLog is rebuild_state.rebuild_event_log(dir, slug, wpIDs).
func RebuildEventLog(dir, slug string, wpIDs map[string]string) *RebuildResult {
	res := &RebuildResult{Slug: slug}
	ts := deriveMigrationTimestamp(dir)
	existing := readEventRows(dir)
	st := readStatusJSON(dir)
	fmLanes := frontmatterLanes(dir)
	if len(existing) == 0 && len(fmLanes) == 0 {
		res.Skipped = true
		return res
	}
	stLanes := map[string]string{}
	var stTS string
	if st != nil {
		stTS, _ = valueOf(st, "materialized_at").(string)
		if wps := st.GetObject("work_packages"); wps != nil {
			for _, kv := range wps.Pairs() {
				if o, ok := kv.Value.(*jsonx.Object); ok {
					raw := "planned"
					if v, ok := o.Get("lane"); ok {
						raw = pyjson.Str(v)
					}
					stLanes[kv.Key] = resolveLegacyAlias(raw)
				}
			}
		}
	}
	logLanes, logTS := map[string]string{}, map[string]string{}
	perWP := map[string][]*jsonx.Object{}
	var wpOrder []string
	for _, e := range existing {
		if wp := pyjson.Str(valueOf(e, "wp_id")); valueOf(e, "wp_id") != nil && wp != "" {
			if _, ok := perWP[wp]; !ok {
				wpOrder = append(wpOrder, wp)
			}
			perWP[wp] = append(perWP[wp], e)
		}
	}
	for _, wp := range wpOrder {
		var best *jsonx.Object
		bestAt := ""
		for _, e := range perWP[wp] {
			at, _ := valueOf(e, "at").(string)
			if best == nil || at > bestAt {
				best, bestAt = e, at
			}
		}
		to := "planned"
		if v, ok := best.Get("to_lane"); ok {
			to = pyjson.Str(v)
		}
		logLanes[wp] = resolveLegacyAlias(to)
		logTS[wp] = bestAt
	}
	all := map[string]bool{}
	for k := range fmLanes {
		all[k] = true
	}
	for k := range stLanes {
		all[k] = true
	}
	for k := range logLanes {
		all[k] = true
	}
	auth := map[string]string{}
	for _, wp := range sortedKeys(all) {
		type cand struct{ src, lane, ts string }
		var cs []cand
		if l, ok := logLanes[wp]; ok {
			cs = append(cs, cand{"event_log", l, logTS[wp]})
		}
		if l, ok := stLanes[wp]; ok {
			cs = append(cs, cand{"status_json", l, stTS})
		}
		if l, ok := fmLanes[wp]; ok {
			cs = append(cs, cand{"frontmatter", l, ""})
		}
		uniq := map[string]bool{}
		for _, c := range cs {
			uniq[c.lane] = true
		}
		if len(uniq) == 1 {
			auth[wp] = cs[0].lane
			continue
		}
		res.Conflicts++
		var withTS []cand
		for _, c := range cs {
			if c.ts != "" {
				withTS = append(withTS, c)
			}
		}
		var win cand
		if len(withTS) > 0 {
			sort.SliceStable(withTS, func(i, j int) bool { return withTS[i].ts > withTS[j].ts })
			win = withTS[0]
		} else {
			win = cs[0]
		}
		var losers []string
		for _, c := range cs {
			if c.src != win.src {
				losers = append(losers, c.src+"="+c.lane)
			}
		}
		msg := fmt.Sprintf("%s/%s: lane conflict (%s vs %s=%s). Using %s.", slug, wp, strings.Join(losers, ", "), win.src, win.lane, win.src)
		warnf("%s", msg)
		res.Warnings = append(res.Warnings, msg)
		auth[wp] = win.lane
	}
	seen := map[string]bool{}
	var deduped []*jsonx.Object
	dropped := 0
	for _, e := range existing {
		id := pyjson.Str(valueOf(e, "event_id"))
		if valueOf(e, "event_id") == nil {
			id = ""
		}
		if seen[id] {
			dropped++
			continue
		}
		seen[id] = true
		deduped = append(deduped, e)
	}
	if dropped > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Dropped %d duplicate events (same event_id).", dropped))
	}
	var final []*jsonx.Object
	for _, e := range deduped {
		c := e.Clone()
		changed := false
		wp := pyjson.Str(valueOf(e, "wp_id"))
		if !c.Has("work_package_id") {
			if id, ok := wpIDs[wp]; ok {
				c.Set("work_package_id", id)
				changed = true
			}
		}
		if !c.Has("mission_slug") {
			c.Set("mission_slug", slug)
			changed = true
		}
		if changed {
			res.Corrected++
		}
		final = append(final, c)
	}
	res.Kept = len(final)
	for _, wp := range sortedStrKeys(auth) {
		a := auth[wp]
		ll, has := logLanes[wp]
		if has && ll == a {
			continue
		}
		if has {
			msg := fmt.Sprintf("%s/%s: event log says '%s' but authoritative source says '%s'. Emitting corrective synthetic event.", slug, wp, ll, a)
			warnf("%s", msg)
			res.Warnings = append(res.Warnings, msg)
			e := jsonx.NewObject("event_id", DeterministicID(slug, wp, ll, a, "corrective"), "mission_slug", slug, "wp_id", wp,
				"from_lane", ll, "to_lane", a, "at", ts, "actor", "migration", "force", true, "execution_mode", "unknown",
				"reason", "reconciled from authoritative source", "review_ref", nil, "evidence", nil)
			if id := wpIDs[wp]; id != "" {
				e.Set("work_package_id", id)
			}
			final = append(final, e)
			res.Corrected++
			continue
		}
		chain := buildChain(wp, slug, wpIDs, a, ts)
		final = append(final, chain...)
		res.Generated += len(chain)
	}
	if len(final) == 0 {
		res.Skipped = true
		return res
	}
	var sb strings.Builder
	for _, e := range final {
		sb.WriteString(jsonx.Dumps(e, jsonx.Line) + "\n")
	}
	tmp := filepath.Join(dir, "status.events.jsonl.tmp")
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err == nil {
		err = os.Rename(tmp, filepath.Join(dir, "status.events.jsonl"))
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("Failed to write event log for %s: %v", slug, err))
		}
	} else {
		res.Errors = append(res.Errors, fmt.Sprintf("Failed to write event log for %s: %v", slug, err))
	}
	os.Remove(tmp)
	return res
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStrKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// BackfillWPIDs is backfill_identity.backfill_wp_ids: stamp mission_id,
// wp_code and a fresh work_package_id into each WP frontmatter.
func BackfillWPIDs(dir, missionID string) map[string]string {
	out := map[string]string{}
	for _, f := range wpFiles(dir) {
		d, err := fm.Read(f[1])
		if err != nil {
			warnf("Cannot read frontmatter from %s: %v — skipping", filepath.Base(f[1]), err)
			continue
		}
		changed := false
		if pyjson.Str(valueOf(d.Meta, "mission_id")) != missionID || valueOf(d.Meta, "mission_id") == nil {
			d.Meta.Set("mission_id", missionID)
			changed = true
		}
		if !d.Meta.Has("wp_code") {
			d.Meta.Set("wp_code", f[0])
			changed = true
		}
		if !d.Meta.Has("work_package_id") {
			d.Meta.Set("work_package_id", ulid.New())
			changed = true
		}
		if changed {
			fm.Write(f[1], d, fm.WPFieldOrder)
		}
		out[f[0]] = pyjson.Str(valueOf(d.Meta, "work_package_id"))
	}
	return out
}
