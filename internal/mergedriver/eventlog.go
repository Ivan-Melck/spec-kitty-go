package mergedriver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// EventLogMergeError is status.EventLogMergeError.
type EventLogMergeError struct{ Msg string }

func (e *EventLogMergeError) Error() string { return e.Msg }

// forwardRank is _FORWARD_RANK (linear lifecycle lanes in order).
var forwardRank = map[string]int{
	"planned": 0, "claimed": 1, "in_progress": 2, "for_review": 3,
	"in_review": 4, "approved": 5, "done": 6,
}

func isDecisionDestination(from, to string) bool {
	if to == "canceled" {
		return true
	}
	src, ok1 := forwardRank[from]
	dst, ok2 := forwardRank[to]
	if !ok1 || !ok2 {
		return false
	}
	return dst < src
}

// ReadEventLogText is read_event_log_text: parse status.events.jsonl
// content; every non-blank line must be a JSON object with a non-blank
// string event_id.
func ReadEventLogText(text, source string) ([]*jsonx.Object, error) {
	var events []*jsonx.Object
	for i, raw := range splitLines(text) {
		line := i + 1
		stripped := pyStrip(raw)
		if stripped == "" {
			continue
		}
		v, err := pyjson.Loads(stripped)
		if err != nil {
			return nil, &EventLogMergeError{fmt.Sprintf("%s: invalid JSON on line %d: %s", source, line, err)}
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			return nil, &EventLogMergeError{fmt.Sprintf("%s: line %d is not a JSON object", source, line)}
		}
		id, _ := o.Get("event_id")
		s, isStr := id.(string)
		if !isStr || pyStrip(s) == "" {
			return nil, &EventLogMergeError{fmt.Sprintf("%s: line %d is missing a valid event_id", source, line)}
		}
		events = append(events, o)
	}
	return events, nil
}

func readEventFile(path string) ([]*jsonx.Object, error) {
	if !exists(path) {
		return nil, nil
	}
	text, err := readTextCrash(path)
	if err != nil {
		return nil, err
	}
	return ReadEventLogText(text, path)
}

// readJSONL is drivers._read_jsonl: lenient parse for fork detection (a
// side with any undecodable line counts as empty).
func readJSONL(path string) ([]*jsonx.Object, error) {
	if !exists(path) {
		return nil, nil
	}
	text, err := readTextCrash(path)
	if err != nil {
		return nil, err
	}
	var rows []*jsonx.Object
	for _, line := range splitLines(text) {
		stripped := pyStrip(line)
		if stripped == "" {
			continue
		}
		v, err := pyjson.Loads(stripped)
		if err != nil {
			return nil, nil
		}
		if o, ok := v.(*jsonx.Object); ok {
			rows = append(rows, o)
		}
	}
	return rows, nil
}

// DetectConcurrentFork is detect_concurrent_fork (#5967): a description of
// a concurrent decision fork between ours and theirs, or "" when none.
func DetectConcurrentFork(base, ours, theirs []*jsonx.Object) (string, error) {
	baseIDs := map[string]bool{}
	for _, e := range base {
		id, _ := e.Get("event_id")
		k, ok := pyjson.HashKey(id)
		if !ok {
			return "", &Crash{Type: "TypeError", Msg: fmt.Sprintf("unhashable type: '%s'", pyjson.TypeName(id))}
		}
		baseIDs[k] = true
	}
	departures := func(events []*jsonx.Object) (map[string]map[string]map[string]bool, error) {
		by := map[string]map[string]map[string]bool{}
		for _, e := range events {
			id, _ := e.Get("event_id")
			k, ok := pyjson.HashKey(id)
			if !ok {
				return nil, &Crash{Type: "TypeError", Msg: fmt.Sprintf("unhashable type: '%s'", pyjson.TypeName(id))}
			}
			if baseIDs[k] {
				continue
			}
			from, _ := e.Get("from_lane")
			to, _ := e.Get("to_lane")
			wp, _ := e.Get("wp_id")
			if !pyjson.Truthy(from) || !pyjson.Truthy(to) || !pyjson.Truthy(wp) {
				continue
			}
			w, f, t := pyjson.Str(wp), pyjson.Str(from), pyjson.Str(to)
			if by[w] == nil {
				by[w] = map[string]map[string]bool{}
			}
			if by[w][f] == nil {
				by[w][f] = map[string]bool{}
			}
			by[w][f][t] = true
		}
		return by, nil
	}
	o, err := departures(ours)
	if err != nil {
		return "", err
	}
	t, err := departures(theirs)
	if err != nil {
		return "", err
	}
	for _, wp := range sortedCommon(o, t) {
		for _, from := range sortedCommon(o[wp], t[wp]) {
			dest := map[string]bool{}
			for k := range o[wp][from] {
				dest[k] = true
			}
			for k := range t[wp][from] {
				dest[k] = true
			}
			list := make([]string, 0, len(dest))
			decision := false
			for d := range dest {
				list = append(list, d)
				if isDecisionDestination(from, d) {
					decision = true
				}
			}
			sort.Strings(list)
			if len(list) > 1 && decision {
				return fmt.Sprintf("work package %s: concurrent transitions from '%s' to %s were made in two "+
					"clones without a pull between them; a union merge would "+
					"silently drop one. Pull, reconcile the divergence by hand "+
					"(keep the cancel/rejection, redo the other), then re-run.", wp, from, pyjson.ReprStrings(list)), nil
			}
		}
	}
	return "", nil
}

func sortedCommon[V any](a, b map[string]V) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// MergeEventPayloads is merge_event_payloads: union by event_id (identical
// duplicates collapse, differing ones are an error), sorted by
// (at-or-timestamp, event_id).
func MergeEventPayloads(groups ...[]*jsonx.Object) ([]*jsonx.Object, error) {
	index := map[string]int{}
	var merged []*jsonx.Object
	for _, g := range groups {
		for _, e := range g {
			idv, _ := e.Get("event_id")
			id := idv.(string)
			if i, ok := index[id]; ok {
				if !pyjson.Equal(merged[i], e) {
					return nil, &EventLogMergeError{"Conflicting payloads found for event_id " + pyjson.ReprString(id)}
				}
				merged[i] = e
				continue
			}
			index[id] = len(merged)
			merged = append(merged, e)
		}
	}
	type keyed struct {
		at, id string
		e      *jsonx.Object
	}
	ks := make([]keyed, len(merged))
	for i, e := range merged {
		at, _ := e.Get("at")
		if !pyjson.Truthy(at) {
			if ts, ok := e.Get("timestamp"); ok {
				at = ts
			} else {
				at = ""
			}
		}
		id, _ := e.Get("event_id")
		ks[i] = keyed{pyjson.Str(at), id.(string), e}
	}
	sort.SliceStable(ks, func(i, j int) bool {
		if ks[i].at != ks[j].at {
			return ks[i].at < ks[j].at
		}
		return ks[i].id < ks[j].id
	})
	out := make([]*jsonx.Object, len(ks))
	for i, k := range ks {
		out[i] = k.e
	}
	return out, nil
}

// SerializeEvents renders events as JSONL (json.dumps(sort_keys=True) rows).
func SerializeEvents(events []*jsonx.Object) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString(jsonx.Dumps(e, jsonx.Line))
		b.WriteByte('\n')
	}
	return b.String()
}

// MergeEventLogTexts is merge_event_log_texts.
func MergeEventLogTexts(texts ...string) (string, error) {
	var groups [][]*jsonx.Object
	for i, t := range texts {
		evs, err := ReadEventLogText(t, fmt.Sprintf("<merge-stage-%d>", i+1))
		if err != nil {
			return "", err
		}
		groups = append(groups, evs)
	}
	merged, err := MergeEventPayloads(groups...)
	if err != nil {
		return "", err
	}
	return SerializeEvents(merged), nil
}

// MergeEventLogFiles is merge_event_log_files: merge three logs into output
// (ours when output is "").
func MergeEventLogFiles(base, ours, theirs, output string) error {
	var groups [][]*jsonx.Object
	for _, p := range []string{base, ours, theirs} {
		evs, err := readEventFile(p)
		if err != nil {
			return err
		}
		groups = append(groups, evs)
	}
	merged, err := MergeEventPayloads(groups...)
	if err != nil {
		return err
	}
	if output == "" {
		output = ours
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return osCrash(err, output)
	}
	return writeText(output, SerializeEvents(merged))
}

// RunEventLog is run_event_log_driver (merge-driver-event-log).
func RunEventLog(basePath, oursPath, theirsPath string) (Outcome, error) {
	base, ours, theirs, err := resolvePaths(basePath, oursPath, theirsPath)
	if err != nil {
		return Outcome{}, err
	}
	var sides [3][]*jsonx.Object
	for i, p := range []string{base, ours, theirs} {
		if sides[i], err = readJSONL(p); err != nil {
			return Outcome{}, err
		}
	}
	fork, err := DetectConcurrentFork(sides[0], sides[1], sides[2])
	if err != nil {
		return Outcome{}, err
	}
	if fork != "" {
		return Outcome{}, newError(KindGeneric, fork)
	}
	if err := MergeEventLogFiles(base, ours, theirs, ""); err != nil {
		var me *EventLogMergeError
		if errors.As(err, &me) {
			return Outcome{}, newError(KindGeneric, me.Msg)
		}
		return Outcome{}, err
	}
	return Outcome{}, nil
}
