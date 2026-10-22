package status

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Post-mission lifecycle event types (LOCAL_ONLY_LIFECYCLE_EVENT_TYPES).
const (
	MissionReopenedEvent  = "MissionReopened"
	FollowUpRecordedEvent = "FollowUpRecorded"
)

// PostMissionEvents is lifecycle._collect_post_mission_events: the
// MissionReopened / FollowUpRecorded rows of the mission log sorted by
// (timestamp, event_id).
func PostMissionEvents(featureDir string) []*jsonx.Object {
	b, err := os.ReadFile(EventsPath(featureDir))
	if err != nil {
		return nil
	}
	var out []*jsonx.Object
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		v, err := pyjson.LoadsBytes([]byte(line))
		if err != nil {
			continue
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			continue
		}
		et, _ := o.Get("event_type")
		if s, _ := et.(string); s == MissionReopenedEvent || s == FollowUpRecordedEvent {
			out = append(out, o)
		}
	}
	key := func(o *jsonx.Object) [2]string {
		ts, _ := o.Get("timestamp")
		id, _ := o.Get("event_id")
		a, _ := ts.(string)
		c, _ := id.(string)
		return [2]string{a, c}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ki, kj := key(out[i]), key(out[j])
		if ki[0] != kj[0] {
			return ki[0] < kj[0]
		}
		return ki[1] < kj[1]
	})
	return out
}

func latestEventTime(o *jsonx.Object) (time.Time, bool) {
	if p := o.GetObject("payload"); p != nil {
		for _, k := range []string{"reopened_at", "recorded_at"} {
			if v, ok := p.Get(k); ok {
				if t, ok := parseDT(v); ok {
					return t, true
				}
			}
		}
	}
	v, _ := o.Get("timestamp")
	return parseDT(v)
}

func lastEventAt(events []*jsonx.Object, eventType string) (time.Time, bool) {
	var best time.Time
	found := false
	for _, e := range events {
		if e.GetString("event_type") != eventType {
			continue
		}
		if t, ok := latestEventTime(e); ok && (!found || t.After(best)) {
			best, found = t, true
		}
	}
	return best, found
}

// isReopened is _is_reopened against meta.json's merged_at marker.
func isReopened(featureDir string, events []*jsonx.Object) bool {
	reopen, ok := lastEventAt(events, MissionReopenedEvent)
	if !ok {
		return false
	}
	v, _ := mission.ReadMetaOrEmpty(featureDir).Get("merged_at")
	merged, ok := parseDT(v)
	if !ok {
		return true
	}
	return reopen.After(merged)
}

// FormatPostMissionEvents is views.format_post_mission_events.
func FormatPostMissionEvents(events []any) []string {
	var lines []string
	orStr := func(p *jsonx.Object, k string) (string, bool) {
		v, _ := p.Get(k)
		if v == nil || v == false || v == "" {
			return "", false
		}
		if n, ok := v.(int); ok && n == 0 {
			return "", false
		}
		return PyStr(v), true
	}
	for _, x := range events {
		e, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		p := e.GetObject("payload")
		if p == nil {
			p = jsonx.NewObject()
		}
		when := func(k string) string {
			if s, ok := orStr(p, k); ok {
				return s
			}
			if s, ok := orStr(e, "timestamp"); ok {
				return s
			}
			return ""
		}
		et, _ := e.Get("event_type")
		switch et {
		case MissionReopenedEvent:
			actor, ok := orStr(p, "reopened_by")
			if !ok {
				actor = "unknown"
			}
			suffix := ""
			if r, ok := orStr(p, "reason"); ok {
				suffix = " — " + r
			}
			lines = append(lines, fmt.Sprintf("re-opened by %s%s (%s)", actor, suffix, when("reopened_at")))
		case FollowUpRecordedEvent:
			actor, ok := orStr(p, "recorded_by")
			if !ok {
				actor = "unknown"
			}
			var ref string
			if t, _ := p.Get("follow_up_type"); t == "pr" {
				v, _ := p.Get("pr_number")
				ref = "PR #" + PyStr(v)
			} else {
				v, _ := p.Get("commit_sha")
				ref = "commit " + PyStr(v)
			}
			lines = append(lines, fmt.Sprintf("follow-up %s by %s (%s)", ref, actor, when("recorded_at")))
		}
	}
	return lines
}
