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
	AvailableTools                                                      []string
	TemplateSet                                                         string
	AuthorityPaths, GovernanceReferences                                []any
	AgentProfiles, MissionStepContracts                                 []string
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
	s.AgentProfiles, s.MissionStepContracts = get("selected_agent_profiles"), get("selected_mission_step_contracts")
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
	identity := dct.NewIdentityIndex(root)
	g := &Governance{}
	resolveSelected := func(kind dct.Kind, tokens []string, local map[string]bool) []string {
		var result []string
		catalog := map[string]bool{}
		for _, id := range off.Repo(kind).IDs() {
			catalog[id] = true
			if kind == dct.Directive {
				catalog[dct.NormalizeDirectiveID(id)] = true
			}
		}
		for _, token := range tokens {
			id := identity.SelectionID(kind, token)
			if id == "" {
				key := token
				if kind == dct.Directive {
					key = dct.NormalizeDirectiveID(token)
				}
				if catalog[key] || local[key] {
					id = key
				}
			}
			if id == "" {
				g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("Unknown selected %s '%s' dropped: not found in built-in, org packs (charter_packs.org.packs) or .kittify/charter-packs/; see 'spec-kitty charter list'.", kind, token))
			} else {
				result = append(result, id)
			}
		}
		return dedupe(result)
	}
	activationBase := func(kind dct.Kind) ([]string, string) {
		allow := act.Activated(kind)
		if allow == nil {
			set := map[string]bool{}
			for _, id := range off.Repo(kind).IDs() {
				if kind == dct.Directive {
					id = dct.NormalizeDirectiveID(id)
				}
				set[id] = true
			}
			base := dct.SortedKeys(set)
			g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("No activated %s set configured; using built-in catalog default (%d %s).", kind, len(base), kind.Plural()))
			return base, "catalog_fallback"
		}
		set := map[string]bool{}
		for _, token := range allow.Sorted() {
			id := identity.DeclaredID(kind, token)
			if id == "" {
				id = identity.LookupKey(kind, token, false)
				warnLog("unresolved %s activation token %s; best-effort resolved to %s", kind, dct.PyRepr(token), dct.PyRepr(id))
			}
			set[id] = true
		}
		return dct.SortedKeys(set), "activation"
	}
	report := func(noun string, selected, base []string, source string) {
		if len(selected) == 0 {
			return
		}
		have := map[string]bool{}
		for _, id := range base {
			have[id] = true
		}
		var added []string
		for _, id := range selected {
			if !have[id] {
				added = append(added, id)
			}
		}
		if len(added) > 0 {
			g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("Charter selected %d %s(s) beyond the %s base: %s.", len(added), noun, source, strings.Join(added, ", ")))
		} else {
			g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("All %d charter-selected %s(s) already present in the %s base; none added: %s.", len(selected), noun, source, strings.Join(selected, ", ")))
		}
	}
	selectedParadigms := resolveSelected(dct.Paradigm, sel.Paradigms, nil)
	base, source := activationBase(dct.Paradigm)
	report("paradigm", selectedParadigms, base, source)
	g.Paradigms = dedupe(append(base, selectedParadigms...))
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

	localIDs := map[string]bool{}
	local := LoadLocalDirectives(root)
	for _, d := range local {
		localIDs[dct.NormalizeDirectiveID(d.ID)] = true
	}
	dbase, source := activationBase(dct.Directive)
	selectedDirectives := resolveSelected(dct.Directive, sel.Directives, localIDs)
	report("directive", selectedDirectives, dbase, source)
	g.Directives = dedupe(append(dbase, selectedDirectives...))
	if len(selectedDirectives) > 0 {
		source += "+charter"
	}
	if len(local) > 0 {
		var ids, added []string
		have := map[string]bool{}
		for _, id := range g.Directives {
			have[id] = true
		}
		for _, d := range local {
			ids = append(ids, d.ID)
			if !have[d.ID] {
				added = append(added, d.ID)
			}
		}
		if len(added) > 0 {
			g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("Merged %d project-local directive(s) onto the %s set: %s.", len(added), source, strings.Join(added, ", ")))
		} else {
			g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("All %d project-local directive(s) already present in the %s set; none added: %s.", len(ids), source, strings.Join(ids, ", ")))
		}
		g.Directives = dedupe(append(g.Directives, ids...))
		source += "+project_local"
	}
	g.DirectivesSource = source
	keep := func(kind dct.Kind, ids []string) []string {
		var result []string
		for _, id := range ids {
			if !act.Denied[kind][id] {
				result = append(result, id)
			}
		}
		return result
	}
	g.Paradigms = keep(dct.Paradigm, g.Paradigms)
	g.Directives = keep(dct.Directive, g.Directives)
	if sel.TemplateSet != "" {
		available := map[string]bool{}
		for _, rel := range dct.BuiltIn().Walk("missions", "mission.yaml", true) {
			parts := strings.Split(rel, "/")
			if len(parts) == 3 {
				available[parts[1]+"-default"] = true
			}
		}
		if dct.BuiltIn().Exists("missions") && !available[sel.TemplateSet] {
			choices := strings.Join(dct.SortedKeys(available), ", ")
			if choices == "" {
				choices = "(none)"
			}
			return nil, fmt.Errorf("Governance resolution failed:\n- Charter selected unavailable template_set: '%s'\n- Available template sets: %s\n- Update charter template_set to one of the available template sets, or provide it through a project override (see 'spec-kitty charter list --show-available').", sel.TemplateSet, choices)
		}
		g.TemplateSet = sel.TemplateSet
	} else {
		g.TemplateSet = DefaultTemplateSet
		g.Diagnostics = append(g.Diagnostics, fmt.Sprintf("Template set not selected in charter; fallback '%s' applied.", DefaultTemplateSet))
	}
	return g, nil
}
