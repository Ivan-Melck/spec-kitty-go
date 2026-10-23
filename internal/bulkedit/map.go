// Package bulkedit enforces the occurrence map used by bulk-edit missions.
package bulkedit

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

type Validation struct {
	Valid            bool
	Errors, Warnings []string
}

type Map struct{ Raw *jsonx.Object }

var vocabulary = sync.OnceValue(func() map[string][]string {
	o, err := yamlx.ParseObject(assets.MustRead("charter/offering/schemas/occurrence-map.schema.yaml"))
	if err != nil {
		panic(err)
	}
	result := map[string][]string{}
	for _, name := range []string{"action", "operation", "standard_category"} {
		for _, item := range o.GetObject("definitions").GetObject(name).GetList("enum") {
			result[name] = append(result[name], item.(string))
		}
		sort.Strings(result[name])
	}
	return result
})

func has(values []string, value any) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}

func py(v any) string {
	if v == nil {
		return "None"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func listRepr(values []string) string {
	var parts []string
	for _, v := range values {
		parts = append(parts, mission.PyRepr(v))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
func nonempty(v any) bool { s, ok := v.(string); return ok && strings.TrimSpace(s) != "" }

func Load(dir string) (*Map, error) {
	p := filepath.Join(dir, "occurrence_map.yaml")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := yamlx.ParseWith(b, yamlx.ParseOptions{Safe: true, TextFile: p})
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("occurrence map must be a mapping")
	}
	if target, present := o.Get("target"); present {
		if _, ok := target.(*jsonx.Object); !ok {
			typ := "NoneType"
			switch target.(type) {
			case string:
				typ = "str"
			case []any:
				typ = "list"
			case bool:
				typ = "bool"
			case int:
				typ = "int"
			case float64:
				typ = "float64"
			}
			return nil, fmt.Errorf("'%s' object has no attribute 'get'", typ)
		}
	}
	return &Map{Raw: o}, nil
}

func NarrowStructuralPath(value string) bool {
	p := strings.TrimSpace(value)
	if p == "" || strings.HasSuffix(p, "/") || strings.Contains(p, "**") {
		return false
	}
	base := path.Base(p)
	dot := strings.LastIndex(base, ".")
	return dot > 0 && dot < len(base)-1 && !strings.ContainsAny(base[dot+1:], "*?")
}

func (m *Map) Validate() Validation {
	r := Validation{Valid: true, Errors: []string{}, Warnings: []string{}}
	add := func(s string) { r.Errors = append(r.Errors, s) }
	if value, present := m.Raw.Get("target"); !present {
		add("Missing required 'target' section")
	} else if target, ok := value.(*jsonx.Object); !ok {
		add("'target' must be a mapping")
	} else {
		if value, _ := target.Get("term"); value == nil {
			add("Missing required 'target.term'")
		} else if !nonempty(value) {
			add("'target.term' must be a non-empty string")
		}
		if value, _ := target.Get("operation"); value != nil && !has(vocabulary()["operation"], value) {
			add(fmt.Sprintf("Invalid target.operation '%s'; must be one of %s", py(value), listRepr(vocabulary()["operation"])))
		}
	}
	if value, present := m.Raw.Get("categories"); !present {
		add("Missing required 'categories' section")
	} else if cats, ok := value.(*jsonx.Object); !ok || cats.Len() == 0 {
		add("'categories' must be a non-empty mapping")
	} else {
		for _, cat := range cats.Pairs() {
			obj, ok := cat.Value.(*jsonx.Object)
			if !ok {
				add(fmt.Sprintf("Category '%s' must be a mapping", cat.Key))
				continue
			}
			value, _ := obj.Get("action")
			if value == nil {
				add(fmt.Sprintf("Category '%s' missing required 'action' key", cat.Key))
			} else if !has(vocabulary()["action"], value) {
				add(fmt.Sprintf("Category '%s' has invalid action '%s'; must be one of %s", cat.Key, py(value), listRepr(vocabulary()["action"])))
			}
		}
	}
	for _, section := range []string{"exceptions", "moves", "structural_targets"} {
		value, _ := m.Raw.Get(section)
		if value == nil {
			continue
		}
		entries, ok := value.([]any)
		if !ok {
			add(fmt.Sprintf("'%s' must be a list when present", section))
			continue
		}
		for i, raw := range entries {
			label := fmt.Sprintf("%s[%d]", section, i)
			obj, ok := raw.(*jsonx.Object)
			if !ok {
				keys := "'path' and 'action'"
				if section == "moves" {
					keys = "'from' and 'to'"
				}
				if section == "structural_targets" {
					keys = "'path' and 'reason'"
				}
				add(label + " must be a mapping with " + keys)
				continue
			}
			if section == "moves" {
				v, _ := obj.Get("from")
				sources, ok := v.([]any)
				if v == nil {
					add(label + " missing required 'from' key")
				} else if !ok || len(sources) == 0 {
					add(label + ".from must be a non-empty list of paths")
				} else {
					for _, s := range sources {
						if !nonempty(s) {
							add(label + ".from entries must be non-empty strings")
							break
						}
					}
				}
				v, _ = obj.Get("to")
				if v == nil {
					add(label + " missing required 'to' key")
				} else if !nonempty(v) {
					add(label + ".to must be a non-empty string")
				}
				continue
			}
			v, _ := obj.Get("path")
			if v == nil {
				add(label + " missing required 'path' key")
			} else if !nonempty(v) {
				add(label + ".path must be a non-empty string")
			} else if section == "structural_targets" && !NarrowStructuralPath(v.(string)) {
				add(fmt.Sprintf("%s.path %s is too broad for a structural-target exemption: it must be a concrete file path with an extension (e.g. 'src/foo/bar.py') or an extension-bounded glob (e.g. 'src/foo/*.py', '**/bar_*.py'). Bare directories, directory-prefixes, and unbounded globs ('*', '**', '**/*') are rejected — a structural-target exemption must name specific file(s), never a whole directory tree.", label, mission.PyRepr(v.(string))))
			}
			if section == "structural_targets" {
				v, _ = obj.Get("reason")
				if v == nil {
					add(label + " missing required 'reason' key")
				} else if !nonempty(v) {
					add(label + ".reason must be a non-empty string")
				}
			} else {
				v, _ = obj.Get("action")
				if v == nil {
					add(label + " missing required 'action' key")
				} else if !has(vocabulary()["action"], v) {
					add(fmt.Sprintf("%s has invalid action '%s'; must be one of %s", label, py(v), listRepr(vocabulary()["action"])))
				}
				v, _ = obj.Get("field_path")
				if v != nil && !nonempty(v) {
					add(label + ".field_path must be a non-empty string when present")
				}
			}
		}
	}
	for _, kv := range m.Raw.Pairs() {
		if !has([]string{"target", "categories", "exceptions", "moves", "structural_targets", "status"}, kv.Key) {
			r.Warnings = append(r.Warnings, fmt.Sprintf("Unknown top-level key '%s'", kv.Key))
		}
	}
	r.Valid = len(r.Errors) == 0
	return r
}

func (m *Map) Admissibility() Validation {
	r := Validation{Valid: true, Errors: []string{}, Warnings: []string{}}
	term := m.Raw.GetObject("target").GetString("term")
	if has([]string{"TODO", "TBD", "FIXME", "XXX", "PLACEHOLDER", ""}, strings.ToUpper(strings.TrimSpace(term))) {
		r.Errors = append(r.Errors, fmt.Sprintf("target.term '%s' is a placeholder; provide a real term before execution", term))
	}
	categories := m.Raw.GetObject("categories")
	if categories.Len() < 3 {
		r.Errors = append(r.Errors, fmt.Sprintf("Need at least 3 categories, got %d", categories.Len()))
	}
	var missing []string
	for _, cat := range vocabulary()["standard_category"] {
		if !categories.Has(cat) {
			missing = append(missing, cat)
		}
	}
	if len(missing) > 0 {
		r.Errors = append(r.Errors, "Occurrence map is missing required standard categories: "+listRepr(missing)+". Every category must be present — use action 'do_not_change' for risk surfaces that must not be modified.")
	}
	r.Valid = len(r.Errors) == 0
	return r
}

type GateResult struct {
	Passed, BulkEdit bool
	Errors, Warnings []string
}

func Ready(dir string) (GateResult, error) {
	r := GateResult{Passed: true, Errors: []string{}, Warnings: []string{}}
	meta, err := ownedcheckout.LoadMetaFailClosed(dir)
	if err != nil {
		return r, err
	}
	if meta == nil || meta.GetString("change_mode") != "bulk_edit" {
		return r, nil
	}
	r.BulkEdit = true
	m, err := Load(dir)
	if err != nil {
		return r, err
	}
	if m == nil {
		r.Passed = false
		r.Errors = append(r.Errors, "Occurrence map required for bulk_edit missions. Create "+dir+"/occurrence_map.yaml with target, categories, and actions.")
		return r, nil
	}
	v := m.Validate()
	if !v.Valid {
		r.Passed = false
		r.Errors = v.Errors
		r.Warnings = v.Warnings
		return r, nil
	}
	a := m.Admissibility()
	if !a.Valid {
		r.Passed = false
		r.Errors = a.Errors
		r.Warnings = a.Warnings
		return r, nil
	}
	r.Warnings = append(v.Warnings, a.Warnings...)
	return r, nil
}
