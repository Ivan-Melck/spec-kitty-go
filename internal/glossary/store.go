package glossary

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// AllScopes is the GlossaryScope enum order.
var AllScopes = []string{ScopeMissionLocal, ScopeTeamDomain, ScopeAudienceDomain, ScopeSpecKittyCore}

// ValidScope reports whether s is a GlossaryScope value.
func ValidScope(s string) bool {
	for _, x := range AllScopes {
		if x == s {
			return true
		}
	}
	return false
}

// SeedError is one SeedValidationError.
type SeedError struct {
	TermIndex   *int
	TermSurface *string
	Field       *string
	Message     string
}

// Loc renders "term[i] 'surface' → field" (or "file").
func (e SeedError) Loc() string {
	var parts []string
	if e.TermIndex != nil {
		label := ""
		if e.TermSurface != nil && *e.TermSurface != "" {
			label = " '" + *e.TermSurface + "'"
		}
		parts = append(parts, fmt.Sprintf("term[%d]%s", *e.TermIndex, label))
	}
	if e.Field != nil && *e.Field != "" {
		parts = append(parts, *e.Field)
	}
	if len(parts) == 0 {
		return "file"
	}
	return strings.Join(parts, " → ")
}

// Object is the --json error entry.
func (e SeedError) Object() *jsonx.Object {
	var ti, ts, f any
	if e.TermIndex != nil {
		ti = *e.TermIndex
	}
	if e.TermSurface != nil {
		ts = *e.TermSurface
	}
	if e.Field != nil {
		f = *e.Field
	}
	return jsonx.NewObject("term_index", ti, "term_surface", ts, "field", f, "message", e.Message)
}

// SeedFileError is SeedFileValidationError.
type SeedFileError struct {
	Path   string
	Errors []SeedError
}

func (e *SeedFileError) Error() string {
	lines := []string{fmt.Sprintf("%d validation error(s) in %s:", len(e.Errors), e.Path)}
	for _, x := range e.Errors {
		lines = append(lines, "  - "+x.Loc()+": "+x.Message)
	}
	return strings.Join(lines, "\n")
}

func sp(s string) *string { return &s }
func ip(i int) *int       { return &i }

func pyFloatRepr(v any) string {
	switch n := v.(type) {
	case int:
		return jsonx.FormatFloat(float64(n))
	case int64:
		return jsonx.FormatFloat(float64(n))
	case float64:
		return jsonx.FormatFloat(n)
	}
	return fmt.Sprint(v)
}

// pyRepr is Python repr(str).
func pyRepr(s string) string {
	q := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = `"`
	}
	r := strings.ReplaceAll(s, `\`, `\\`)
	if q == "'" {
		r = strings.ReplaceAll(r, "'", `\'`)
	}
	r = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(r, "\n", `\n`), "\r", `\r`), "\t", `\t`)
	return q + r + q
}

var termFields = []string{"surface", "definition", "confidence", "status", "see_also", "synonyms_to_avoid", "introduced_in_mission"}

func isNum(v any) bool {
	switch v.(type) {
	case int, int64, float64:
		return true
	}
	return false
}

// ValidateSeedData is validate_seed_file_data (GlossarySeedFile pydantic
// model): the term count on success, else the structured errors.
func ValidateSeedData(data any) (int, []SeedError) {
	o, ok := data.(*jsonx.Object)
	if !ok {
		return 0, []SeedError{{Message: "Input should be a valid dictionary or instance of GlossarySeedFile"}}
	}
	var errs []SeedError
	tv, has := o.Get("terms")
	if !has {
		errs = append(errs, SeedError{Field: sp("terms"), Message: "must have 'terms' key"})
	}
	var terms []any
	if has && tv != nil {
		l, isList := tv.([]any)
		if !isList {
			errs = append(errs, SeedError{Field: sp("terms"), Message: "Input should be a valid list"})
		} else {
			terms = l
		}
	}
	for i, t := range terms {
		to, isObj := t.(*jsonx.Object)
		if !isObj {
			errs = append(errs, SeedError{TermIndex: ip(i), Message: "Input should be a valid dictionary or instance of GlossarySeedTerm"})
			continue
		}
		var surf *string
		if v, ok := to.Get("surface"); ok && v != nil {
			s := fmt.Sprint(v)
			surf = &s
		}
		add := func(field, msg string) {
			if msg == "Field required" {
				msg = "must have '" + field + "' key"
			}
			errs = append(errs, SeedError{TermIndex: ip(i), TermSurface: surf, Field: sp(field), Message: msg})
		}
		// surface
		if v, ok := to.Get("surface"); !ok {
			add("surface", "Field required")
		} else if s, isStr := v.(string); !isStr {
			add("surface", "Input should be a valid string")
		} else if s == "" {
			add("surface", "Value error, surface must not be empty")
		} else if s != strings.TrimSpace(strings.ToLower(s)) {
			add("surface", fmt.Sprintf("Value error, surface must be normalized (lowercase, trimmed): got %s, expected %s", pyRepr(s), pyRepr(strings.TrimSpace(strings.ToLower(s)))))
		}
		// definition
		if v, ok := to.Get("definition"); !ok {
			add("definition", "Field required")
		} else if s, isStr := v.(string); !isStr {
			add("definition", "Input should be a valid string")
		} else if strings.TrimSpace(s) == "" {
			add("definition", "Value error, definition must not be empty")
		}
		// confidence
		if v, ok := to.Get("confidence"); ok {
			if _, isBool := v.(bool); isBool || !isNum(v) {
				add("confidence", "Value error, confidence must be a number")
			} else {
				var f float64
				switch n := v.(type) {
				case int:
					f = float64(n)
				case int64:
					f = float64(n)
				case float64:
					f = n
				}
				if f < 0 || f > 1 {
					add("confidence", "Value error, confidence must be 0.0..1.0, got "+jsonx.FormatFloat(f))
				}
			}
		}
		// status
		if v, ok := to.Get("status"); ok {
			if s, isStr := v.(string); !isStr || (s != "active" && s != "draft" && s != "deprecated") {
				add("status", "Input should be 'active', 'draft' or 'deprecated'")
			}
		}
		if v, ok := to.Get("see_also"); ok && v != nil {
			if _, isList := v.([]any); !isList {
				add("see_also", "Input should be a valid list")
			}
		}
		if v, ok := to.Get("synonyms_to_avoid"); ok && v != nil {
			if _, isList := v.([]any); !isList {
				add("synonyms_to_avoid", "Input should be a valid list")
			}
		}
		if v, ok := to.Get("introduced_in_mission"); ok && v != nil {
			if _, isStr := v.(string); !isStr {
				add("introduced_in_mission", "Input should be a valid string")
			}
		}
		known := map[string]bool{}
		for _, f := range termFields {
			known[f] = true
		}
		for _, k := range to.Keys() {
			if !known[k] {
				add(k, "Extra inputs are not permitted")
			}
		}
	}
	for _, k := range o.Keys() {
		if k != "terms" {
			errs = append(errs, SeedError{Field: sp(k), Message: "Extra inputs are not permitted"})
		}
	}
	if len(errs) > 0 {
		return 0, errs
	}
	return len(terms), nil
}

// ParseSeedFile reads and validates a seed file (YAML parse errors become a
// single "YAML parse error" entry).
func ParseSeedFile(path string) (*jsonx.Object, int, *SeedFileError) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, &SeedFileError{path, []SeedError{{Message: "YAML parse error: " + err.Error()}}}
	}
	data, err := yamlx.Parse(b)
	if err != nil {
		return nil, 0, &SeedFileError{path, []SeedError{{Message: "YAML parse error: " + err.Error()}}}
	}
	n, errs := ValidateSeedData(data)
	if errs != nil {
		return nil, 0, &SeedFileError{path, errs}
	}
	o, _ := data.(*jsonx.Object)
	return o, n, nil
}

// Term is one stored TermSense (list/show view).
type Term struct {
	Surface, Scope, Definition, Status string
	Confidence                         any // int or float64, as written
}

func parseStatus(v any) string {
	if s, ok := v.(string); ok && (s == "active" || s == "deprecated" || s == "draft") {
		return s
	}
	return "draft"
}

// ReadEvents reads one glossary JSONL event log (malformed lines skipped).
func ReadEvents(path string) []*jsonx.Object {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []*jsonx.Object
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || !json.Valid([]byte(l)) {
			continue
		}
		if o, err := jsonx.DecodeObject([]byte(l)); err == nil && o != nil {
			out = append(out, o)
		}
	}
	return out
}

// AllEvents reads every .kittify/events/glossary/*.events.jsonl (sorted).
func AllEvents(root string) []*jsonx.Object {
	files, _ := filepath.Glob(filepath.Join(root, ".kittify", "events", "glossary", "*.events.jsonl"))
	sort.Strings(files)
	var out []*jsonx.Object
	for _, f := range files {
		out = append(out, ReadEvents(f)...)
	}
	return out
}

// LoadStore is _load_store_from_seeds: every scope seed (validated) then
// the GlossarySenseUpdated / GlossaryClarificationResolved event replay.
func LoadStore(root string) ([]Term, error) {
	var terms []Term
	for _, sc := range AllScopes {
		p := SeedPath(root, sc)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		data, _, serr := ParseSeedFile(p)
		if serr != nil {
			return nil, serr
		}
		for _, x := range data.GetList("terms") {
			to, _ := x.(*jsonx.Object)
			conf, ok := to.Get("confidence")
			if !ok {
				conf = 1.0
			}
			st, _ := to.Get("status")
			terms = append(terms, Term{Surface: to.GetString("surface"), Scope: sc, Definition: to.GetString("definition"),
				Status: parseStatus(st), Confidence: conf})
		}
	}
	normalized := func(s string) bool { return s != "" && s == strings.TrimSpace(strings.ToLower(s)) }
	for _, ev := range AllEvents(root) {
		switch ev.GetString("event_type") {
		case "GlossarySenseUpdated":
			ns := ev.GetObject("new_sense")
			if ns == nil {
				ns = &jsonx.Object{}
			}
			surface := strOr(ns, "surface", ev.GetString("term_surface"))
			if surface == "" || !normalized(surface) {
				continue
			}
			conf, ok := ns.Get("confidence")
			if !ok {
				conf = 1.0
			}
			st, _ := ns.Get("status")
			terms = append(terms, Term{Surface: surface, Scope: strOr(ns, "scope", strOr(ev, "scope", "team_domain")),
				Definition: ns.GetString("definition"), Status: parseStatus(st), Confidence: conf})
		case "GlossaryClarificationResolved":
			sel := ev.GetObject("selected_sense")
			if sel == nil {
				sel = &jsonx.Object{}
			}
			surface := strOr(sel, "surface", ev.GetString("term_surface"))
			def := sel.GetString("definition")
			if surface == "" || def == "" || !normalized(surface) {
				continue
			}
			conf, ok := sel.Get("confidence")
			if !ok {
				conf = 1.0
			}
			terms = append(terms, Term{Surface: surface, Scope: strOr(sel, "scope", "team_domain"), Definition: def,
				Status: "active", Confidence: conf})
		}
	}
	return terms, nil
}

func strOr(o *jsonx.Object, k, def string) string {
	if v, ok := o.Get(k); ok {
		if s, isStr := v.(string); isStr {
			return s
		}
	}
	return def
}

// ListTerms filters (scope "" = all valid scopes) and sorts by (scope, surface).
func ListTerms(terms []Term, scope, status string) []Term {
	var out []Term
	for _, t := range terms {
		if scope != "" && t.Scope != scope {
			continue
		}
		if scope == "" && !ValidScope(t.Scope) {
			continue
		}
		if status != "" && t.Status != status {
			continue
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Surface < out[j].Surface
	})
	return out
}

// Lookup returns the senses for surface in scope precedence order.
func Lookup(terms []Term, surface string) []Term {
	var out []Term
	for _, sc := range AllScopes {
		for _, t := range terms {
			if t.Scope == sc && t.Surface == surface {
				out = append(out, t)
			}
		}
	}
	return out
}

// ConfidenceFloat returns the confidence as float64.
func (t Term) ConfidenceFloat() float64 {
	switch n := t.Confidence.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	}
	return 0
}

var _ = pyFloatRepr
