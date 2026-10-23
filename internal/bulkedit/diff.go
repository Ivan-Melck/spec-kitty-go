package bulkedit

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/acceptance"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

type Assessment struct {
	Path, Category, Source, Action, Reason string
	Violation                              bool
	Pins                                   []string
}
type DiffResult struct {
	Passed           bool
	Assessments      []Assessment
	Errors, Warnings []string
}

func ClassifyPath(value string) string {
	for _, rule := range pathRules {
		if rule.pattern.MatchString(path.Clean(value)) {
			return rule.category
		}
	}
	return ""
}

func glob(value, pattern string) bool {
	if lanes.Fnmatch(value, pattern) {
		return true
	}
	if !strings.Contains(pattern, "**") {
		return false
	}
	p := strings.ReplaceAll(pattern, "**", "\x00DOUBLESTAR\x00")
	p = strings.ReplaceAll(p, ".", `\.`)
	p = strings.ReplaceAll(p, "*", "[^/]*")
	p = strings.ReplaceAll(p, "?", "[^/]")
	p = strings.ReplaceAll(p, "\x00DOUBLESTAR\x00", ".*")
	re, err := regexp.Compile("^(?:" + p + ")$")
	return err == nil && re.MatchString(value)
}

func pathMatches(value, declared string) bool {
	if strings.TrimSpace(declared) == "" {
		return false
	}
	p := strings.TrimRight(path.Clean(strings.TrimSpace(declared)), "/")
	if strings.ContainsAny(p, "*?") {
		return glob(value, p)
	}
	return value == p || strings.HasPrefix(value, p+"/")
}

func rawObjects(m *Map, key string) []*jsonx.Object {
	var result []*jsonx.Object
	for _, v := range m.Raw.GetList(key) {
		if o, ok := v.(*jsonx.Object); ok {
			result = append(result, o)
		}
	}
	return result
}

func defaultString(o *jsonx.Object, key, fallback string) string {
	if o.Has(key) {
		v, _ := o.Get(key)
		return py(v)
	}
	return fallback
}

func Assess(value string, m *Map, anchor, status string) Assessment {
	a := classify(value, m, anchor, status)
	pins := map[string]bool{}
	for _, e := range rawObjects(m, "exceptions") {
		if field := e.GetString("field_path"); field != "" && glob(path.Clean(value), e.GetString("path")) {
			pins[field] = true
		}
	}
	a.Pins = []string{}
	for p := range pins {
		a.Pins = append(a.Pins, p)
	}
	sort.Strings(a.Pins)
	return a
}

func classify(value string, m *Map, anchor, status string) Assessment {
	posix := path.Clean(value)
	base := path.Base(posix)
	a := Assessment{Path: value, Source: "path-heuristic"}
	if anchor != "" && (posix == strings.Trim(anchor, "/") || strings.HasPrefix(posix, strings.Trim(anchor, "/")+"/")) && (acceptance.IsSelfBookkeeping(posix) || mission.IsCoordResidue(posix) || base == "notes.md" || glob(base, "review-cycle-*.md")) {
		a.Source = "runtime-state"
		a.Reason = fmt.Sprintf("'%s' is the mission's own runtime-state bookkeeping file (FR-007 allowlist) — exempt from occurrence classification.", base)
		return a
	}
	parts := strings.Split(posix, "/")
	foreign := len(parts) >= 2 && parts[0] == "kitty-specs" && (anchor == "" || parts[1] != path.Base(strings.Trim(anchor, "/")))
	if status == "A" && !foreign {
		a.Source = "added"
		a.Reason = "File is CREATED by this diff — it has no pre-existing occurrence for the map to protect"
		return a
	}
	for _, e := range rawObjects(m, "exceptions") {
		if e.GetString("field_path") != "" || e.GetString("path") == "" || !glob(posix, e.GetString("path")) {
			continue
		}
		a.Source = "exception"
		a.Action = e.GetString("action")
		reason := defaultString(e, "reason", "matched exception")
		if a.Action == "do_not_change" {
			a.Violation = true
			a.Reason = fmt.Sprintf("Exception '%s' marks this file do_not_change: %s", e.GetString("path"), reason)
		} else {
			a.Reason = fmt.Sprintf("Exception '%s' allows %s: %s", e.GetString("path"), mission.PyRepr(a.Action), reason)
		}
		return a
	}
	for _, e := range rawObjects(m, "moves") {
		role := ""
		for _, s := range e.GetList("from") {
			if pathMatches(posix, py(s)) {
				role = "move-source"
				break
			}
		}
		if role == "" && pathMatches(posix, e.GetString("to")) {
			role = "move-destination"
		}
		if role == "" {
			continue
		}
		reason := e.GetString("reason")
		if reason == "" {
			reason = "declared " + strings.ReplaceAll(role, "-", " ")
		}
		a.Source = "move"
		a.Action = "move"
		a.Reason = "Declared structural " + role + ": " + reason
		return a
	}
	for _, e := range rawObjects(m, "structural_targets") {
		p := e.GetString("path")
		if !NarrowStructuralPath(p) || !pathMatches(posix, p) {
			continue
		}
		reason := e.GetString("reason")
		if reason == "" {
			reason = "declared structural target"
		}
		a.Source = "structural-target"
		a.Action = "structural_edit"
		a.Reason = "Declared structural target: " + reason
		return a
	}
	nonOccurrence := ""
	if placeholderNames[base] {
		nonOccurrence = "empty-directory marker (zero bytes, no occurrences)"
	} else if status == "A" && lockfileNames[base] {
		nonOccurrence = fmt.Sprintf("'%s' is a generated dependency lockfile — resolver output, regenerated rather than edited", base)
	} else {
		segments := strings.Split(strings.TrimLeft(base, "."), ".")
		for _, suffix := range segments[1:] {
			if opaqueSuffixes["."+strings.ToLower(suffix)] {
				nonOccurrence = fmt.Sprintf("opaque binary payload ('.%s') — carries no reviewable textual occurrence", suffix)
				break
			}
		}
	}
	if nonOccurrence != "" {
		a.Source = "non-occurrence"
		a.Reason = nonOccurrence
		return a
	}
	a.Category = ClassifyPath(posix)
	if a.Category == "" {
		a.Violation = true
		a.Reason = "File path does not match any standard occurrence category (FR-008: unclassified surface touched). Add an exception in occurrence_map.yaml if this file is expected."
		return a
	}
	category := m.Raw.GetObject("categories").GetObject(a.Category)
	if category == nil {
		a.Violation = true
		a.Reason = fmt.Sprintf("File classified as '%s' but that category is not present in the occurrence map (FR-008).", a.Category)
		return a
	}
	a.Action = category.GetString("action")
	if a.Action == "do_not_change" {
		a.Violation = true
		a.Reason = fmt.Sprintf("File classified as '%s' which is marked do_not_change (FR-007). Update the occurrence map or add an exception if this file is legitimately out of scope.", a.Category)
	} else {
		a.Reason = fmt.Sprintf("Category '%s' action '%s' permits modification.", a.Category, a.Action)
	}
	return a
}

func CheckDiff(files []string, m *Map, anchor string, statuses map[string]string) DiffResult {
	r := DiffResult{Passed: true, Assessments: []Assessment{}, Errors: []string{}, Warnings: []string{}}
	for _, p := range files {
		a := Assess(p, m, anchor, statuses[p])
		r.Assessments = append(r.Assessments, a)
		if a.Violation {
			r.Errors = append(r.Errors, a.Path+": "+a.Reason)
		}
		if a.Action == "manual_review" {
			cat := "None"
			if a.Category != "" {
				cat = a.Category
			}
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: category '%s' requires manual_review — document justification", a.Path, cat))
		}
	}
	for _, phase := range []string{"pins", "structural-target", "added", "non-occurrence"} {
		for _, a := range r.Assessments {
			switch {
			case phase == "pins" && len(a.Pins) > 0:
				r.Warnings = append(r.Warnings, a.Path+": field-path exception pins "+strings.Join(a.Pins, ", ")+" as do_not_change — verify only those fields were left untouched")
			case phase == "structural-target" && a.Source == phase:
				r.Warnings = append(r.Warnings, a.Path+": structural-target exemption applied ("+a.Reason+") — do_not_change bypass; verify this is a genuine structural edit, not a bulk-occurrence change")
			case phase == "added" && a.Source == phase:
				r.Warnings = append(r.Warnings, a.Path+": created by this diff — exempt from occurrence classification (no pre-existing occurrence); review the new file on its merits")
			case phase == "non-occurrence" && a.Source == phase:
				r.Warnings = append(r.Warnings, a.Path+": "+a.Reason+" — exempt from occurrence classification")
			}
		}
	}
	r.Passed = len(r.Errors) == 0
	return r
}
