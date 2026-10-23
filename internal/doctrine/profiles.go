package doctrine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// RoleVerbs is upstream DEFAULT_ROLE_CAPABILITIES[role].canonical_verbs for
// the well-known roles (custom roles have none).
var RoleVerbs = map[string][]string{
	"implementer": {"generate", "refine", "implement"},
	"reviewer":    {"audit", "assess", "review"},
	"architect":   {"audit", "synthesize", "plan"},
	"designer":    {"synthesize", "draft", "design"},
	"planner":     {"plan", "decompose", "prioritize"},
	"researcher":  {"analyze", "investigate", "summarize"},
	"curator":     {"classify", "curate", "validate"},
	"manager":     {"coordinate", "delegate", "monitor"},
}

// Profile is a view over a loaded agent-profile artifact.
type Profile struct {
	*Artifact
}

// AsProfile wraps an artifact (nil-safe).
func AsProfile(a *Artifact) *Profile {
	if a == nil {
		return nil
	}
	return &Profile{a}
}

// Name is the profile display name.
func (p *Profile) Name() string { return p.Data.GetString("name") }

// Role is the primary role (roles[0], or the deprecated scalar role), lowercased.
func (p *Profile) Role() string {
	if l := p.Data.GetList("roles"); len(l) > 0 {
		return strings.ToLower(pyStr(l[0]))
	}
	if s := p.Data.GetString("role"); s != "" {
		return strings.ToLower(s)
	}
	return ""
}

// RoutingPriority is routing-priority (default 50).
func (p *Profile) RoutingPriority() int {
	if n, ok := p.Data.GetInt("routing-priority"); ok {
		return n
	}
	return 50
}

func strList(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		out = append(out, pyStr(x))
	}
	return out
}

// DomainKeywords is specialization-context.domain-keywords.
func (p *Profile) DomainKeywords() []string {
	sc := p.Data.GetObject("specialization-context")
	if sc == nil {
		return nil
	}
	v, _ := sc.Get("domain-keywords")
	return strList(v)
}

// CollabVerbs is collaboration.canonical-verbs.
func (p *Profile) CollabVerbs() []string {
	c := p.Data.GetObject("collaboration")
	if c == nil {
		return nil
	}
	v, _ := c.Get("canonical-verbs")
	return strList(v)
}

// PreferredModel / Effort are the optional routing hints (model:, effort:).
func (p *Profile) PreferredModel() string { return p.Data.GetString("model") }
func (p *Profile) Effort() string         { return p.Data.GetString("effort") }

// ActionDomains is the sorted union of role verbs, collaboration verbs and
// domain keywords (profiles list descriptor).
func (p *Profile) ActionDomains() []string {
	set := map[string]bool{}
	for _, v := range RoleVerbs[p.Role()] {
		set[v] = true
	}
	for _, v := range p.CollabVerbs() {
		set[v] = true
	}
	for _, v := range p.DomainKeywords() {
		set[v] = true
	}
	return sortedKeys(set)
}

// LegacyProfilesDir is the legacy invocation registry (.kittify/profiles).
func LegacyProfilesDir(repoRoot string) string {
	return filepath.Join(repoRoot, ".kittify", "profiles")
}

// ProfileCatalog is the merged profile catalog shown by `profiles list`:
// built-in + legacy .kittify/profiles, overlaid with charter-pack org/project
// profiles (upstream _profile_catalog). Ungated by activation.
type ProfileCatalog struct {
	ByID  map[string]*Profile
	graph *Graph
}

// LoadProfileCatalog builds the catalog for repoRoot.
func LoadProfileCatalog(repoRoot string) *ProfileCatalog {
	dirs := LayerDirs{}
	if st, err := os.Stat(LegacyProfilesDir(repoRoot)); err == nil && st.IsDir() {
		dirs.ProjectDir = LegacyProfilesDir(repoRoot)
	}
	legacy := LoadRepository(AgentProfile, dirs)
	c := &ProfileCatalog{ByID: map[string]*Profile{}}
	for _, a := range legacy.List() {
		c.ByID[a.ID] = AsProfile(a)
	}
	orgRoots := OrgRoots(repoRoot)
	projDir := filepath.Join(repoRoot, filepath.FromSlash(ProjectPackRoot), "agent_profiles")
	if _, err := os.Stat(projDir); err == nil || len(orgRoots) > 0 {
		off := NewOffering(repoRoot, orgRoots)
		for _, a := range off.Repo(AgentProfile).List() {
			if a.Source == LayerProject || a.Source == LayerOrg {
				c.ByID[a.ID] = AsProfile(a)
			}
		}
	}
	c.graph, _ = LoadGraph(repoRoot, orgRoots)
	return c
}

// IDs returns sorted profile ids.
func (c *ProfileCatalog) IDs() []string {
	out := make([]string, 0, len(c.ByID))
	for id := range c.ByID {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// List returns profiles sorted by id.
func (c *ProfileCatalog) List() []*Profile {
	var out []*Profile
	for _, id := range c.IDs() {
		out = append(out, c.ByID[id])
	}
	return out
}

// LineageParent returns the DRG specializes_from parent ("" when none).
func LineageParent(g *Graph, id string) string {
	if g == nil {
		return ""
	}
	var parents []string
	for _, e := range g.EdgesFrom("agent_profile:"+id, RelSpecializesFrom) {
		_, pid := SplitURN(e.Target)
		parents = append(parents, pid)
	}
	if len(parents) == 0 {
		return ""
	}
	sort.Strings(parents)
	return parents[0]
}

// Ancestors returns the lineage chain (immediate parent first).
func (c *ProfileCatalog) Ancestors(id string) []string {
	var out []string
	seen := map[string]bool{id: true}
	cur := id
	for {
		p := LineageParent(c.graph, cur)
		if p == "" || seen[p] {
			return out
		}
		out = append(out, p)
		seen[p] = true
		cur = p
	}
}

var profileListFields = map[string]bool{"capabilities": true, "directive-references": true,
	"canonical-verbs": true, "mode-defaults": true, "tactic-references": true}

func itemKey(v any) string {
	if o, ok := v.(*jsonx.Object); ok {
		if x, ok := o.Get("code"); ok {
			return pyStr(x)
		}
		if x, ok := o.Get("id"); ok {
			return pyStr(x)
		}
		return jsonx.Dumps(o, jsonx.Options{Indent: -1})
	}
	return pyStr(v)
}

func normalizeProfileData(o *jsonx.Object) *jsonx.Object {
	d := o.Clone()
	if v, ok := d.Get("role"); ok && !d.Has("roles") {
		if s, ok := v.(string); ok {
			v = strings.ToLower(s)
		}
		d.Delete("role")
		d.Set("roles", []any{v})
	}
	return d
}

func unionMerge(parent, child *jsonx.Object) *jsonx.Object {
	merged := parent.Clone()
	for _, kv := range child.Pairs() {
		pv, _ := merged.Get(kv.Key)
		pl, pok := pv.([]any)
		cl, cok := kv.Value.([]any)
		po, pobj := pv.(*jsonx.Object)
		co, cobj := kv.Value.(*jsonx.Object)
		switch {
		case profileListFields[kv.Key] && pok && cok:
			seen := map[string]bool{}
			for _, x := range pl {
				seen[itemKey(x)] = true
			}
			out := append([]any(nil), pl...)
			for _, x := range cl {
				if !seen[itemKey(x)] {
					out = append(out, x)
				}
			}
			merged.Set(kv.Key, out)
		case pobj && cobj:
			n := po.Clone()
			for _, ckv := range co.Pairs() {
				n.Set(ckv.Key, ckv.Value)
			}
			merged.Set(kv.Key, n)
		default:
			merged.Set(kv.Key, kv.Value)
		}
	}
	return merged
}

// Resolve composes the lineage of id (root -> leaf union merge, then the
// leaf's `excluding`). Returns nil when id is unknown or a parent is missing.
func (c *ProfileCatalog) Resolve(id string) *Profile {
	p := c.ByID[id]
	if p == nil {
		return nil
	}
	chain := []*Profile{p}
	for _, a := range c.Ancestors(id) {
		pa := c.ByID[a]
		if pa == nil {
			return nil
		}
		chain = append(chain, pa)
	}
	merged := &jsonx.Object{}
	for i := len(chain) - 1; i >= 0; i-- {
		merged = unionMerge(merged, normalizeProfileData(chain[i].Data))
	}
	if ex, ok := p.Data.Get("excluding"); ok && ex != nil {
		switch t := ex.(type) {
		case []any:
			for _, f := range t {
				merged.Delete(pyStr(f))
			}
		case *jsonx.Object:
			for _, kv := range t.Pairs() {
				l, ok := merged.Get(kv.Key)
				ll, isList := l.([]any)
				if !ok || !isList {
					continue
				}
				rm := map[string]bool{}
				for _, x := range strList(kv.Value) {
					rm[x] = true
				}
				var out []any
				for _, x := range ll {
					if !rm[itemKey(x)] {
						out = append(out, x)
					}
				}
				if out == nil {
					out = []any{}
				}
				merged.Set(kv.Key, out)
			}
		}
	}
	a := *p.Artifact
	a.Data = merged
	return &Profile{&a}
}

// ActivatedProfiles returns the three-state activated_agent_profiles set
// (nil = key absent / no config).
func ActivatedProfiles(repoRoot string) (IDSet, error) {
	act, err := LoadActivation(repoRoot)
	if err != nil {
		return nil, err
	}
	allowed := act.Activated(AgentProfile)
	if len(act.Denied[AgentProfile]) > 0 && allowed == nil {
		allowed = IDSet{}
		for token := range AvailableIDs("agent-profile", LayerRoots(repoRoot), OrgRoots(repoRoot)) {
			if !act.Denied[AgentProfile][token] {
				allowed[token] = true
			}
		}
	}
	return allowed, nil
}
