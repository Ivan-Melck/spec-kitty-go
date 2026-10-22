package glossary

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// This file ports glossary.semantic_events.iter_semantic_conflicts and
// glossary.observation.ObservationSurface (the inline drift notices
// `dispatch` prints after a human-mode run).

// SemanticConflict is SemanticConflictRecord.
type SemanticConflict struct {
	Term, TermID, Severity, ConflictType string
}

func eventStr(o *jsonx.Object, k string) string {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return pyjson.Str(v)
}

func extractTermID(term string, finding, event *jsonx.Object) string {
	for _, c := range []string{eventStr(finding, "term_id"), eventStr(event, "term_id")} {
		if s := strings.TrimSpace(c); s != "" {
			return s
		}
	}
	if strings.HasPrefix(term, "glossary:") {
		return term
	}
	if term != "" {
		return "glossary:" + term
	}
	return ""
}

// IterSemanticConflicts is iter_semantic_conflicts(repo_root, invocation_id=...).
func IterSemanticConflicts(root, invocationID string) []SemanticConflict {
	files, _ := filepath.Glob(filepath.Join(root, ".kittify", "events", "glossary", "*.events.jsonl"))
	sort.Strings(files)
	var out []SemanticConflict
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			v, err := pyjson.Loads(line)
			if err != nil {
				continue
			}
			ev, ok := v.(*jsonx.Object)
			if !ok {
				continue
			}
			if invocationID != "" {
				match := false
				for _, k := range []string{"invocation_id", "step_id", "run_id"} {
					if s := eventStr(ev, k); s != "" && s == invocationID {
						match = true
					}
				}
				if !match {
					continue
				}
			}
			switch eventStr(ev, "event_type") {
			case "SemanticCheckEvaluated":
				for _, x := range ev.GetList("findings") {
					fo, ok := x.(*jsonx.Object)
					if !ok {
						continue
					}
					term := ""
					if tv, ok := fo.Get("term"); ok {
						if to, isObj := tv.(*jsonx.Object); isObj {
							term = eventStr(to, "surface_text")
							if term == "" {
								term = eventStr(to, "surface")
							}
						} else if tv != nil {
							term = pyjson.Str(tv)
						}
					}
					term = strings.TrimSpace(term)
					id := extractTermID(term, fo, ev)
					if term == "" || id == "" {
						continue
					}
					sev := eventStr(fo, "severity")
					if sev == "" {
						sev = eventStr(ev, "severity")
					}
					if sev == "" {
						sev = eventStr(ev, "overall_severity")
					}
					out = append(out, SemanticConflict{term, id, strings.ToLower(sev), eventStr(fo, "conflict_type")})
				}
			case "semantic_check_evaluated":
				term := strings.TrimSpace(eventStr(ev, "term"))
				id := extractTermID(term, ev, ev)
				if term == "" || id == "" {
					continue
				}
				out = append(out, SemanticConflict{term, id, strings.ToLower(eventStr(ev, "severity")), eventStr(ev, "conflict_type")})
			}
		}
	}
	return out
}

// DriftNotice is observation.InlineNotice.
type DriftNotice struct{ Term, TermID, Severity, ConflictType string }

// CollectNotices is ObservationSurface.collect_notices: high/critical
// conflicts of the invocation, last-seen per term id (first-seen order).
func CollectNotices(root, invocationID string) []DriftNotice {
	var order []string
	seen := map[string]DriftNotice{}
	for _, c := range IterSemanticConflicts(root, invocationID) {
		if c.Severity != "high" && c.Severity != "critical" {
			continue
		}
		if _, ok := seen[c.TermID]; !ok {
			order = append(order, c.TermID)
		}
		seen[c.TermID] = DriftNotice{c.Term, c.TermID, c.Severity, c.ConflictType}
	}
	out := make([]DriftNotice, len(order))
	for i, id := range order {
		out[i] = seen[id]
	}
	return out
}

// NoticeLines is ObservationSurface.render_notices (plain text lines,
// starting with the blank separator).
func NoticeLines(ns []DriftNotice) []string {
	if len(ns) == 0 {
		return nil
	}
	lines := []string{""}
	for _, n := range ns {
		lines = append(lines, fmt.Sprintf("⚠ Glossary drift [%s]: \"%s\" — %s detected", n.Severity, n.Term, n.ConflictType),
			"  Suggest: run `spec-kitty glossary conflicts --unresolved`")
	}
	return lines
}
