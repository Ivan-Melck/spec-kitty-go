// Package cutover implements `spec-kitty cutover-guard` (upstream
// cli/commands/cutover_guard.py over status/cutover_eligibility.py).
package cutover

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// Reasons (status.cutover_eligibility).
const (
	PreAcceptExemptNote     = "pre-accept: status_phase stamp deferred to accept/consolidate"
	notFlipped              = "status_phase not flipped despite event-log runtime evidence"
	ReasonTerminalUnstamped = notFlipped + ": mission is accepted, merged or numbered but status_phase is not stamped"
	ReasonTerminalMalformed = notFlipped + ": accepted_at/merged_at in meta.json is not a non-empty string"
	ReasonLegacyFrontmatter = notFlipped + ": WP frontmatter carries legacy runtime to migrate"
	ReasonAbsentMissionID   = "absent mission_id"
	ReasonMetaDuplicateKeys = "meta.json has a duplicate top-level key (ambiguous: the last value would win)"
	ReasonPhaseMalformed    = "status_phase is malformed (not an integer)"
	ReasonLegacyUndecidable = notFlipped + ": legacy WP runtime could not be read to decide the pre-accept exemption"
)

// Verdict is CutOverVerdict.
type Verdict struct {
	Slug    string
	CutOver bool
	Reasons []string
	Exempt  bool
}

// readMeta is _read_meta: load_meta(utf-8-sig, on_malformed="empty").
func readMeta(dir string) *jsonx.Object {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return &jsonx.Object{}
	}
	s, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return &jsonx.Object{}
	}
	s = strings.TrimPrefix(s, "\ufeff")
	v, err := pyjson.Loads(s)
	if err != nil {
		return &jsonx.Object{}
	}
	if o, ok := v.(*jsonx.Object); ok {
		return o
	}
	return &jsonx.Object{}
}

func get(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

func readMissionID(meta *jsonx.Object) string {
	v := get(meta, "mission_id")
	if !pyjson.Truthy(v) {
		return ""
	}
	return strings.TrimSpace(pyjson.Str(v))
}

// carriesEventLogRuntime is mission_carries_event_log_runtime.
func carriesEventLogRuntime(dir string) bool {
	st, err := os.Stat(status.EventsPath(dir))
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	stream, err := status.ReadStream(dir, "")
	if err != nil {
		return false
	}
	if len(stream.Annotations) > 0 {
		return true
	}
	for _, e := range stream.Transitions {
		if e.PolicyMetadata == nil {
			continue
		}
		for _, k := range e.PolicyMetadata.Keys() {
			if k != "lane_head" {
				return true
			}
		}
	}
	return false
}

// rawPhaseState is _raw_phase_state.
func rawPhaseState(meta *jsonx.Object) string {
	raw := get(meta, "status_phase")
	if raw == nil {
		return "absent"
	}
	s := strings.TrimSpace(pyjson.Str(raw))
	if b, ok := raw.(bool); ok {
		s = map[bool]string{true: "True", false: "False"}[b]
	}
	v, err := pyInt(s)
	if err != nil {
		return "malformed"
	}
	if v < 0 {
		return "malformed"
	}
	if v < 1 {
		return "early"
	}
	return "stamped"
}

var intLiteral = regexp.MustCompile(`^[+-]?\d+(_\d+)*$`)

// pyInt is int(str) for base-10 literals (underscores allowed).
func pyInt(s string) (int64, error) {
	if !intLiteral.MatchString(s) {
		return 0, fmt.Errorf("invalid literal")
	}
	v, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 10, 64)
	if err != nil {
		// Huge integers are still integers: only the sign matters here.
		if strings.HasPrefix(s, "-") {
			return -1, nil
		}
		return 1, nil
	}
	return v, nil
}

func nonEmptyString(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != ""
}

func markerPresent(v any) bool {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) != ""
	}
	if v == nil {
		return false
	}
	if l, ok := v.([]any); ok && len(l) == 0 {
		return false
	}
	if o, ok := v.(*jsonx.Object); ok && o.Len() == 0 {
		return false
	}
	return true
}

func terminalEvidence(meta *jsonx.Object) string {
	for _, k := range []string{"accepted_at", "merged_at"} {
		if v := get(meta, k); v != nil && !nonEmptyString(v) {
			return "malformed"
		}
	}
	for _, k := range []string{"merged_at", "accepted_at"} {
		if nonEmptyString(get(meta, k)) {
			return "terminal"
		}
	}
	for _, k := range []string{"accept_commit", "merged_commit", "acceptance_history"} {
		if markerPresent(get(meta, k)) {
			return "terminal"
		}
	}
	if get(meta, "mission_number") != nil {
		return "terminal"
	}
	return "none"
}

// metaHasDuplicateKeys is _meta_has_duplicate_keys (top-level only; an
// unreadable or invalid file counts as duplicated).
func metaHasDuplicateKeys(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return true
	}
	s, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return true
	}
	s = strings.TrimPrefix(s, "\ufeff")
	if _, err := pyjson.Loads(s); err != nil {
		return true
	}
	keys, ok := topLevelKeys(s)
	if !ok {
		return false
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			return true
		}
		seen[k] = true
	}
	return false
}

// topLevelKeys lists the raw keys of a (valid) top-level JSON object in
// order, duplicates included.
func topLevelKeys(s string) ([]string, bool) {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) == 0 || rs[0] != '{' {
		return nil, false
	}
	var keys []string
	depth := 0
	expectKey := false
	for i := 0; i < len(rs); i++ {
		switch c := rs[i]; c {
		case '{', '[':
			depth++
			if depth == 1 && c == '{' {
				expectKey = true
			}
		case '}', ']':
			depth--
		case ',':
			if depth == 1 {
				expectKey = true
			}
		case '"':
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				if rs[j] == '\\' {
					j++
				}
				j++
			}
			if depth == 1 && expectKey {
				v, err := pyjson.Loads(string(rs[i : j+1]))
				if err == nil {
					keys = append(keys, pyjson.Str(v))
				}
				expectKey = false
			}
			i = j
		}
	}
	return keys, true
}

// carriesFrontmatterRuntime is _carries_frontmatter_runtime: claim-time
// runtime (shell_pid, shell_pid_created_at, assignee) left in WP files.
func carriesFrontmatterRuntime(dir string) (bool, string) {
	tasks := filepath.Join(dir, "tasks")
	if st, err := os.Stat(tasks); err == nil {
		if !st.IsDir() {
			return false, fmt.Sprintf("tasks/: NotADirectoryError: [Errno 20] Not a directory: %s", pyjson.ReprString(tasks))
		}
	} else {
		return false, ""
	}
	if b, err := os.ReadFile(filepath.Join(dir, "tasks.md")); err == nil {
		if _, err := pyjson.DecodeUTF8(b); err != nil {
			return false, "tasks.md: UnicodeDecodeError: " + strings.Join(strings.Fields(err.Error()), " ")
		}
	}
	matches, _ := filepath.Glob(filepath.Join(tasks, "WP*.md"))
	sort.Strings(matches)
	for _, m := range matches {
		w, err := wp.Read(m)
		if err != nil {
			return false, "WP files: LegacyRuntimeReadError: " + strings.Join(strings.Fields(fmt.Sprintf("cannot read %s for legacy runtime: %v", filepath.Base(m), err)), " ")
		}
		meta := w.Meta()
		pid := get(meta, "shell_pid")
		switch p := pid.(type) {
		case int:
			if p >= 0 {
				return true, ""
			}
		case string:
			if t := strings.TrimSpace(p); t != "" && strings.Trim(t, "0123456789") == "" {
				return true, ""
			}
		}
		if pyjson.Truthy(get(meta, "shell_pid_created_at")) || pyjson.Truthy(get(meta, "assignee")) {
			return true, ""
		}
	}
	return false, ""
}

// IsCutOver is is_cut_over. The stamped path (status_phase >= 1) skips
// upstream's verify_backfill re-derivation and reports cut over.
func IsCutOver(dir string) Verdict {
	slug := filepath.Base(dir)
	meta := readMeta(dir)
	if readMissionID(meta) == "" {
		return Verdict{Slug: slug, Reasons: []string{ReasonAbsentMissionID}}
	}
	if !carriesEventLogRuntime(dir) {
		return Verdict{Slug: slug, CutOver: true}
	}
	block := ""
	switch rawPhaseState(meta) {
	case "malformed":
		block = ReasonPhaseMalformed
	case "stamped":
		return Verdict{Slug: slug, CutOver: true}
	default:
		if metaHasDuplicateKeys(dir) {
			block = ReasonMetaDuplicateKeys
		} else if t := terminalEvidence(meta); t == "malformed" {
			block = ReasonTerminalMalformed
		} else if t == "terminal" {
			block = ReasonTerminalUnstamped
		} else if carries, failure := carriesFrontmatterRuntime(dir); failure != "" {
			block = ReasonLegacyUndecidable + ": " + failure
		} else if carries {
			block = ReasonLegacyFrontmatter
		}
	}
	if block != "" {
		return Verdict{Slug: slug, Reasons: []string{block}}
	}
	return Verdict{Slug: slug, CutOver: true, Reasons: []string{PreAcceptExemptNote}, Exempt: true}
}
