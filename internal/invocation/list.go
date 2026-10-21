package invocation

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

func readFirstLine(p string) *jsonx.Object {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	first, _, _ := strings.Cut(string(b), "\n")
	first = strings.TrimSpace(first)
	if first == "" {
		return nil
	}
	o, err := decodeLine(first)
	if err != nil {
		return nil
	}
	return o
}

func readCompletedRaw(p, id string) *jsonx.Object {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
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
		if o.GetString("event") == "completed" && o.GetString("invocation_id") == id {
			return o
		}
	}
	return nil
}

// applyCompletion is _apply_completion_status; false = legacy (skip).
func applyCompletion(rec *jsonx.Object, p, id string, legacy *int) bool {
	raw := readCompletedRaw(p, id)
	if raw == nil {
		rec.Set("status", "open")
		return true
	}
	c, err := ParseCompleted(raw)
	if err != nil {
		var le *LegacyRecordError
		if errors.As(err, &le) {
			*legacy++
		}
		return false
	}
	rec.Set("completed_at", c.CompletedAt)
	rec.Set("outcome", c.Outcome)
	rec.Set("closed_by", c.ClosedBy)
	if c.hasEvidence {
		rec.Set("evidence_ref", c.EvidenceRef)
	} else {
		rec.Set("evidence_ref", nil)
	}
	rec.Set("status", "closed")
	return true
}

func parseStartedCounting(o *jsonx.Object, legacy *int) *Started {
	s, err := ParseStarted(o)
	if err != nil {
		var le *LegacyRecordError
		if errors.As(err, &le) {
			*legacy++
		}
		return nil
	}
	return s
}

// ListRecords is `invocations list` record iteration (newest first). It
// returns the records and the number of skipped legacy lines.
func ListRecords(root, profileFilter string, limit int) ([]*jsonx.Object, int) {
	dir := filepath.Join(root, EventsDir)
	idx := filepath.Join(dir, IndexFile)
	legacy := 0
	var out []*jsonx.Object
	if b, err := os.ReadFile(idx); err == nil {
		lines := strings.Split(string(b), "\n")
		seen := map[string]bool{}
		for i := len(lines) - 1; i >= 0; i-- {
			if len(out) >= limit {
				break
			}
			l := strings.TrimSpace(lines[i])
			if l == "" {
				continue
			}
			entry, err := decodeLine(l)
			if err != nil || entry == nil {
				continue
			}
			id := entry.GetString("invocation_id")
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if profileFilter != "" && entry.GetString("profile_id") != profileFilter {
				continue
			}
			p := filepath.Join(dir, id+".jsonl")
			rec := entry.Clone()
			if !applyCompletion(rec, p, id, &legacy) {
				continue
			}
			raw := readFirstLine(p)
			if raw == nil {
				continue
			}
			st := parseStartedCounting(raw, &legacy)
			if st == nil {
				continue
			}
			if !rec.Has("action") {
				rec.Set("action", st.Action)
			}
			if !rec.Has("event") {
				rec.Set("event", "started")
			}
			out = append(out, rec)
		}
		return out, legacy
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0
	}
	var all []*jsonx.Object
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		raw := readFirstLine(p)
		if raw == nil {
			continue
		}
		if profileFilter != "" && raw.GetString("profile_id") != profileFilter {
			continue
		}
		st := parseStartedCounting(raw, &legacy)
		if st == nil {
			continue
		}
		rec := st.Object()
		if !applyCompletion(rec, p, st.InvocationID, &legacy) {
			continue
		}
		all = append(all, rec)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].GetString("started_at") > all[j].GetString("started_at") })
	if limit < 0 {
		limit = 0
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, legacy
}
