package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// WPEntry is one wps.yaml work package (core.wps_manifest.WorkPackageEntry).
type WPEntry struct {
	ID              string
	Title           string
	Dependencies    []string
	OwnedFiles      []string
	RequirementRefs []string
	Subtasks        []string
	PromptFile      *string
	PlanConcernRefs []string
	CrossCutting    bool

	// DependenciesExplicit is dependencies_are_explicit(entry): the
	// 'dependencies' key was present in the source YAML.
	DependenciesExplicit bool
}

// WpsManifest is the parsed wps.yaml outline.
type WpsManifest struct {
	WorkPackages []*WPEntry
	// ConcernTrackingRequired is _concern_tracking_required: plan.md declares
	// IC-## concern headings (set by LoadWpsManifest; true otherwise).
	ConcernTrackingRequired bool
}

// ErrManifestInvalid marks an unreadable / schema-invalid wps.yaml. Every
// error LoadWpsManifest / ParseWpsManifest return matches it (errors.Is).
var ErrManifestInvalid = errors.New("invalid wps.yaml")

// ManifestError is a wps.yaml load failure; Error() is upstream's str(exc)
// (the WpsManifestReadError reason, the AttributeError text or the
// pydantic ValidationError rendering).
type ManifestError struct{ Msg string }

func (e *ManifestError) Error() string        { return e.Msg }
func (e *ManifestError) Is(target error) bool { return target == ErrManifestInvalid }

var (
	wpIDRe      = regexp.MustCompile(`^WP\d{2}$`)
	concernRefR = regexp.MustCompile(`^IC-[0-9]{2}$`)
	icHeadingRe = regexp.MustCompile(`(?m)^#{2,4}[ \t\n\r\f\v]+IC-[0-9]{2}\b`)
)

// LoadWpsManifest is load_wps_manifest(dir): nil when wps.yaml is absent.
func LoadWpsManifest(dir string) (*WpsManifest, error) {
	path := filepath.Join(dir, "wps.yaml")
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, &ManifestError{osErrText(err, path)}
	}
	text, derr := pyjson.DecodeUTF8(b)
	if derr != nil {
		return nil, &ManifestError{derr.Error()}
	}
	m, err := ParseWpsManifest([]byte(text))
	if err != nil {
		return nil, err
	}
	m.ConcernTrackingRequired = planHasConcerns(dir)
	return m, nil
}

func osErrText(err error, path string) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		switch {
		case errors.Is(pe.Err, os.ErrPermission):
			return "[Errno 13] Permission denied: " + pyjson.ReprString(path)
		case strings.Contains(pe.Err.Error(), "is a directory"):
			return "[Errno 21] Is a directory: " + pyjson.ReprString(path)
		}
	}
	return err.Error()
}

func planHasConcerns(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "plan.md"))
	if err != nil {
		return false
	}
	return icHeadingRe.Match(b)
}

// ParseWpsManifest validates wps.yaml content against the canonical schema.
func ParseWpsManifest(b []byte) (*WpsManifest, error) {
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, &ManifestError{err.Error()}
	}
	o, isObj := v.(*jsonx.Object)
	if !isObj || o == nil {
		return nil, &ManifestError{fmt.Sprintf("'%s' object has no attribute 'get'", pyjson.TypeName(v))}
	}
	pv := &pdValidator{}
	m := &WpsManifest{ConcernTrackingRequired: true}
	raw, ok := o.Get("work_packages")
	if !ok {
		pv.add([]any{"work_packages"}, "Field required", "missing", o)
		return nil, pv.err()
	}
	list, isList := raw.([]any)
	if !isList {
		pv.add([]any{"work_packages"}, "Input should be a valid list", "list_type", raw)
		return nil, pv.err()
	}
	for i, item := range list {
		loc := []any{"work_packages", i}
		eo, isO := item.(*jsonx.Object)
		if !isO || eo == nil {
			pv.add(loc, "Input should be a valid dictionary or instance of WorkPackageEntry", "model_type", item)
			continue
		}
		e := &WPEntry{}
		if id, ok := pv.reqStr(loc, eo, "id"); ok {
			if !wpIDRe.MatchString(id) {
				pv.add(with(loc, "id"), "Value error, WP id must be WPnn (e.g. WP01), got: "+pyjson.ReprString(id), "value_error", id)
			}
			e.ID = id
		}
		if t, ok := pv.reqStr(loc, eo, "title"); ok {
			e.Title = t
		}
		if deps, ok := pv.strList(loc, eo, "dependencies"); ok {
			for _, d := range deps {
				if !wpIDRe.MatchString(d) {
					dv, _ := eo.Get("dependencies")
					pv.add(with(loc, "dependencies"), "Value error, Dependency must be WPnn (e.g. WP01), got: "+pyjson.ReprString(d), "value_error", dv)
					break
				}
			}
			e.Dependencies = deps
		}
		e.OwnedFiles, _ = pv.strList(loc, eo, "owned_files")
		e.RequirementRefs, _ = pv.strList(loc, eo, "requirement_refs")
		e.Subtasks, _ = pv.strList(loc, eo, "subtasks")
		if pf, ok := eo.Get("prompt_file"); ok && pf != nil {
			if s, isS := pf.(string); isS {
				e.PromptFile = &s
			} else {
				pv.add(with(loc, "prompt_file"), "Input should be a valid string", "string_type", pf)
			}
		}
		if refs, ok := pv.strList(loc, eo, "plan_concern_refs"); ok {
			for _, r := range refs {
				if !concernRefR.MatchString(r) {
					rv, _ := eo.Get("plan_concern_refs")
					pv.add(with(loc, "plan_concern_refs"), "Value error, plan_concern_ref must match IC-## (e.g. IC-01), got: "+pyjson.ReprString(r), "value_error", rv)
					break
				}
			}
			e.PlanConcernRefs = refs
		}
		if cv, ok := eo.Get("cross_cutting"); ok {
			e.CrossCutting = pv.boolean(with(loc, "cross_cutting"), cv)
		}
		e.DependenciesExplicit = eo.Has("dependencies")
		m.WorkPackages = append(m.WorkPackages, e)
	}
	if len(pv.errs) > 0 {
		return nil, pv.err()
	}
	return m, nil
}

// CheckConcernRefsCoverage is check_concern_refs_coverage(manifest).
func CheckConcernRefsCoverage(m *WpsManifest) []string {
	out := []string{}
	if m == nil || !m.ConcernTrackingRequired {
		return out
	}
	for _, wp := range m.WorkPackages {
		if len(wp.PlanConcernRefs) == 0 && !wp.CrossCutting {
			out = append(out, fmt.Sprintf("%s (%s): missing plan_concern_refs and cross_cutting is not set — add IC-## refs or set cross_cutting: true", wp.ID, pyjson.ReprString(wp.Title)))
		}
	}
	return out
}

// GenerateTasksMD is generate_tasks_md_from_manifest(manifest, name,
// resolved_dependencies=resolved); a nil resolved map keeps the manifest
// values, WPs absent from it keep theirs.
func GenerateTasksMD(m *WpsManifest, featureName string, resolved map[string][]string) string {
	lines := []string{"# Work Packages: " + featureName, "", "_Generated by finalize-tasks from wps.yaml. Do not edit directly._", "", "---", ""}
	for _, wp := range m.WorkPackages {
		lines = append(lines, fmt.Sprintf("## Work Package %s: %s", wp.ID, wp.Title), "")
		deps := wp.Dependencies
		if resolved != nil {
			if d, ok := resolved[wp.ID]; ok {
				deps = d
			}
		}
		if len(deps) > 0 {
			lines = append(lines, "**Dependencies**: "+strings.Join(deps, ", "))
		} else {
			lines = append(lines, "**Dependencies**: None")
		}
		if len(wp.RequirementRefs) > 0 {
			lines = append(lines, "**Requirement Refs**: "+strings.Join(wp.RequirementRefs, ", "))
		}
		if len(wp.PlanConcernRefs) > 0 {
			lines = append(lines, "**Plan Concerns**: "+strings.Join(wp.PlanConcernRefs, ", "))
		}
		if len(wp.OwnedFiles) > 0 {
			lines = append(lines, "**Owned Files**: "+strings.Join(wp.OwnedFiles, ", "))
		}
		if len(wp.Subtasks) > 0 {
			lines = append(lines, "**Subtasks**: "+strings.Join(wp.Subtasks, ", "))
		}
		if wp.PromptFile != nil && *wp.PromptFile != "" {
			lines = append(lines, "**Prompt**: `"+*wp.PromptFile+"`")
		}
		lines = append(lines, "", "---", "")
	}
	return strings.Join(lines, "\n")
}

// ── pydantic ValidationError rendering (pydantic 2.14) ─────────────────────

type pdError struct {
	loc   []any
	msg   string
	typ   string
	input any
}

type pdValidator struct{ errs []pdError }

func with(loc []any, more ...any) []any { return append(append([]any(nil), loc...), more...) }

func (v *pdValidator) add(loc []any, msg, typ string, input any) {
	v.errs = append(v.errs, pdError{loc, msg, typ, input})
}

func (v *pdValidator) reqStr(loc []any, o *jsonx.Object, k string) (string, bool) {
	x, ok := o.Get(k)
	if !ok {
		v.add(with(loc, k), "Field required", "missing", o)
		return "", false
	}
	s, isS := x.(string)
	if !isS {
		v.add(with(loc, k), "Input should be a valid string", "string_type", x)
		return "", false
	}
	return s, true
}

// strList validates an optional list[str] field (default []).
func (v *pdValidator) strList(loc []any, o *jsonx.Object, k string) ([]string, bool) {
	x, ok := o.Get(k)
	if !ok {
		return []string{}, true
	}
	l, isL := x.([]any)
	if !isL {
		v.add(with(loc, k), "Input should be a valid list", "list_type", x)
		return []string{}, false
	}
	out := make([]string, 0, len(l))
	good := true
	for i, it := range l {
		s, isS := it.(string)
		if !isS {
			v.add(with(loc, k, i), "Input should be a valid string", "string_type", it)
			good = false
			continue
		}
		out = append(out, s)
	}
	return out, good
}

func (v *pdValidator) boolean(loc []any, in any) bool {
	const parsing = "Input should be a valid boolean, unable to interpret input"
	switch t := in.(type) {
	case bool:
		return t
	case int:
		if t == 0 || t == 1 {
			return t == 1
		}
		v.add(loc, parsing, "bool_parsing", in)
		return false
	case float64:
		if t == 0 || t == 1 {
			return t == 1
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "0", "off", "f", "false", "n", "no":
			return false
		case "1", "on", "t", "true", "y", "yes":
			return true
		}
		v.add(loc, parsing, "bool_parsing", in)
		return false
	}
	v.add(loc, "Input should be a valid boolean", "bool_type", in)
	return false
}

func (v *pdValidator) err() error {
	var b strings.Builder
	n := len(v.errs)
	plural := "s"
	if n == 1 {
		plural = ""
	}
	fmt.Fprintf(&b, "%d validation error%s for WpsManifest", n, plural)
	for _, e := range v.errs {
		b.WriteString("\n")
		if len(e.loc) > 0 {
			parts := make([]string, len(e.loc))
			for i, l := range e.loc {
				parts[i] = fmt.Sprint(l)
			}
			b.WriteString(strings.Join(parts, ".") + "\n")
		}
		fmt.Fprintf(&b, "  %s [type=%s, input_value=%s, input_type=%s]\n", e.msg, e.typ, truncateRepr(pyjson.Repr(e.input)), pyjson.TypeName(e.input))
		fmt.Fprintf(&b, "    For further information visit https://errors.pydantic.dev/2.14/v/%s", e.typ)
	}
	return &ManifestError{b.String()}
}

// truncateRepr is pydantic-core's input_value truncation.
func truncateRepr(s string) string {
	r := []rune(s)
	if len(r) <= 50 {
		return s
	}
	return string(r[:25]) + "..." + string(r[len(r)-24:])
}
