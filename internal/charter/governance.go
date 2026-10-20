package charter

import (
	"fmt"
	"os"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// DefaultTemplateSet is the fallback template set.
const DefaultTemplateSet = "software-dev-default"

// DefaultToolRegistry is the runtime tool registry fallback.
var DefaultToolRegistry = []string{"git", "spec-kitty"}

// LoadBundle parses charter.yaml (nil when absent or invalid).
func LoadBundle(root string) *jsonx.Object {
	b, err := os.ReadFile(Path(root, CharterYAML))
	if err != nil {
		return nil
	}
	o, err := yamlx.ParseObject(b)
	if err != nil {
		return nil
	}
	return o
}

// Selection is the governance.charter block of charter.yaml (upstream
// GovernanceCharterConfig).
type Selection struct {
	Paradigms, Directives, Tactics, Styleguides, Toolguides, Procedures []string
	AvailableTools                                                     []string
	TemplateSet                                                        string
	AuthorityPaths, GovernanceReferences                               []any
}

func strList(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

// LoadSelection reads the charter selection (empty when no charter).
func LoadSelection(root string) Selection {
	var s Selection
	b := LoadBundle(root)
	if b == nil {
		return s
	}
	g := b.GetObject("governance")
	if g == nil {
		return s
	}
	c := g.GetObject("charter")
	if c == nil {
		return s
	}
	get := func(k string) []string { v, _ := c.Get(k); return strList(v) }
	s.Paradigms, s.Directives, s.Tactics = get("selected_paradigms"), get("selected_directives"), get("selected_tactics")
	s.Styleguides, s.Toolguides, s.Procedures = get("selected_styleguides"), get("selected_toolguides"), get("selected_procedures")
	s.AvailableTools = get("available_tools")
	s.TemplateSet = c.GetString("template_set")
	s.AuthorityPaths = c.GetList("authority_paths")
	s.GovernanceReferences = c.GetList("governance_references")
	return s
}

// LocalDirective is one charter.yaml `directives:` entry.
type LocalDirective struct {
	ID, Title, Description string
}

// LoadLocalDirectives reads charter.yaml directives.directives.
func LoadLocalDirectives(root string) []LocalDirective {
	b := LoadBundle(root)
	if b == nil {
		return nil
	}
	d := b.GetObject("directives")
	if d == nil {
		return nil
	}
	var out []LocalDirective
	for _, x := range d.GetList("directives") {
		if o, ok := x.(*jsonx.Object); ok {
			out = append(out, LocalDirective{ID: o.GetString("id"), Title: o.GetString("title"), Description: o.GetString("description")})
		}
	}
	return out
}

// Governance is upstream GovernanceResolution.
type Governance struct {
	Paradigms, Directives, Tools []string
	TemplateSet                  string
	DirectivesSource             string
	Diagnostics                  []string
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// ResolveGovernance is upstream resolve_project_governance.
func ResolveGovernance(root string) (*Governance, error) {
	act, err := dct.LoadActivation(root)
	if err != nil {
		return nil, err
	}
	sel := LoadSelection(root)
	off := dct.NewOffering("", nil)
	g := &Governance{}
	// paradigms
	var base []string
	if a := act.Activated(dct.Paradigm); a == nil {
		base = off.Repo(dct.Paradigm).IDs()
		g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("No activated paradigm set configured; using built-in catalog default (%d paradigms).", len(base)))
		g.Paradigms = base
	} else {
		base = a.Sorted()
		g.Paradigms = base
	}
	if len(sel.Paradigms) > 0 {
		src := "catalog_fallback"
		if act.Activated(dct.Paradigm) != nil {
			src = "activation"
		}
		bs := map[string]bool{}
		for _, p := range base {
			bs[p] = true
		}
		var added []string
		for _, p := range sel.Paradigms {
			if !bs[p] {
				added = append(added, p)
			}
		}
		if len(added) > 0 {
			g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("Charter selected %d paradigm(s) beyond the %s base: %s.", len(added), src, strings.Join(added, ", ")))
		} else {
			g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("All %d charter-selected paradigm(s) already present in the %s base; none added: %s.", len(sel.Paradigms), src, strings.Join(sel.Paradigms, ", ")))
		}
		g.Paradigms = dedupe(append(append([]string(nil), base...), sel.Paradigms...))
	}
	// tools
	reg := map[string]bool{}
	for _, t := range DefaultToolRegistry {
		reg[t] = true
	}
	if len(sel.AvailableTools) > 0 {
		u := map[string]bool{}
		var added []string
		for t := range reg {
			u[t] = true
		}
		for _, t := range sel.AvailableTools {
			if !reg[t] && !u[t] {
				added = append(added, t)
			}
			u[t] = true
		}
		sort.Strings(added)
		if len(added) > 0 {
			g.Diagnostics = append(g.Diagnostics, "Charter declared additional tool(s) beyond the runtime registry: "+strings.Join(added, ", ")+".")
		}
		g.Tools = dct.SortedKeys(u)
	} else {
		g.Diagnostics = append(g.Diagnostics, "No available_tools selection provided; using runtime tool registry fallback.")
		g.Tools = dct.SortedKeys(reg)
	}
	// directives
	var dbase []string
	src := "activation"
	if a := act.Activated(dct.Directive); a == nil {
		set := map[string]bool{}
		for _, id := range off.Repo(dct.Directive).IDs() {
			set[dct.NormalizeDirectiveID(id)] = true
		}
		dbase = dct.SortedKeys(set)
		g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("No activated directive set configured; using built-in catalog default (%d directives).", len(dbase)))
		src = "catalog_fallback"
	} else {
		set := map[string]bool{}
		for id := range a {
			set[dct.NormalizeDirectiveID(id)] = true
		}
		dbase = dct.SortedKeys(set)
	}
	g.Directives = dbase
	if len(sel.Directives) > 0 {
		var selected []string
		for _, d := range sel.Directives {
			selected = append(selected, dct.NormalizeDirectiveID(d))
		}
		selected = dedupe(selected)
		bs := map[string]bool{}
		for _, d := range dbase {
			bs[d] = true
		}
		var added []string
		for _, d := range selected {
			if !bs[d] {
				added = append(added, d)
			}
		}
		if len(added) > 0 {
			g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("Charter selected %d directive(s) beyond the %s base: %s.", len(added), src, strings.Join(added, ", ")))
		} else {
			g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("All %d charter-selected directive(s) already present in the %s base; none added: %s.", len(selected), src, strings.Join(selected, ", ")))
		}
		g.Directives = dedupe(append(append([]string(nil), dbase...), selected...))
		src += "+charter"
	}
	if local := LoadLocalDirectives(root); len(local) > 0 {
		var ids, added []string
		bs := map[string]bool{}
		for _, d := range g.Directives {
			bs[d] = true
		}
		for _, d := range local {
			ids = append(ids, d.ID)
			if !bs[d.ID] {
				added = append(added, d.ID)
			}
		}
		if len(added) > 0 {
			g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("Merged %d project-local directive(s) onto the %s set: %s.", len(added), src, strings.Join(added, ", ")))
		} else {
			g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("All %d project-local directive(s) already present in the %s set; none added: %s.", len(ids), src, strings.Join(ids, ", ")))
		}
		g.Directives = dedupe(append(g.Directives, ids...))
		src += "+project_local"
	}
	g.DirectivesSource = src
	if sel.TemplateSet != "" {
		g.TemplateSet = sel.TemplateSet
	} else {
		g.TemplateSet = DefaultTemplateSet
		g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("Template set not selected in charter; fallback '%s' applied.", DefaultTemplateSet))
	}
	return g, nil
}
