package doctrine

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Artifact is one loaded doctrine artifact (raw YAML mapping after the
// built-in -> org -> project field-level merge).
type Artifact struct {
	Kind   Kind
	ID     string
	Data   *jsonx.Object
	Source string // built-in | org | project
	// Path is the on-disk file of the winning layer (for built-ins: the
	// materialised copy under BuiltInRoot()).
	Path string
	// PackPath is the pack-relative slash path for built-ins ("" otherwise).
	PackPath string
}

// Str returns a string field ("" when absent or not a string).
func (a *Artifact) Str(key string) string { return a.Data.GetString(key) }

// Title returns the artifact title (falling back to name / id).
func (a *Artifact) Title() string {
	for _, k := range []string{"title", "name"} {
		if s := a.Data.GetString(k); s != "" {
			return s
		}
	}
	return a.ID
}

// Repository holds the resolved artifacts of one kind.
type Repository struct {
	Kind          Kind
	items         map[string]*Artifact
	ScopeFiltered map[string]bool
}

// Get returns the artifact with id (nil when absent). Directive ids are
// also matched through NormalizeDirectiveID.
func (r *Repository) Get(id string) *Artifact {
	if r == nil {
		return nil
	}
	if a := r.items[id]; a != nil {
		return a
	}
	if r.Kind == Directive {
		return r.items[NormalizeDirectiveID(id)]
	}
	return nil
}

// List returns all artifacts sorted by id.
func (r *Repository) List() []*Artifact {
	if r == nil {
		return nil
	}
	out := make([]*Artifact, 0, len(r.items))
	for _, a := range r.items {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// IDs returns the sorted ids.
func (r *Repository) IDs() []string {
	var out []string
	for _, a := range r.List() {
		out = append(out, a.ID)
	}
	return out
}

// Provenance returns the source layer of id ("" when absent).
func (r *Repository) Provenance(id string) string {
	if a := r.Get(id); a != nil {
		return a.Source
	}
	return ""
}

// Len returns the number of artifacts.
func (r *Repository) Len() int {
	if r == nil {
		return 0
	}
	return len(r.items)
}

// builtInDir returns the pack-relative built-in directory for kind.
func builtInDir(k Kind) string {
	switch k {
	case MissionStepContract:
		return "missions/built_in_step_contracts"
	}
	return k.Plural()
}

// LayerDirs describes where a repository reads its overlays from.
type LayerDirs struct {
	OrgDirs    []string // absolute
	ProjectDir string   // absolute ("" = none)
	// Languages, when non-nil, filters artifacts declaring
	// applies_to_languages to those overlapping the set.
	Languages []string
}

// LoadRepository loads one kind from the built-in pack plus overlays.
func LoadRepository(k Kind, dirs LayerDirs) *Repository {
	r := &Repository{Kind: k, items: map[string]*Artifact{}, ScopeFiltered: map[string]bool{}}
	if k.Suffix() == "" {
		return r
	}
	bi := BuiltIn()
	builtIn := map[string]*Artifact{}
	for _, rel := range bi.Walk(builtInDir(k), k.Suffix(), true) {
		b, err := bi.ReadFile(rel)
		if err != nil {
			continue
		}
		o, err := yamlx.ParseObject(b)
		if err != nil || o.Len() == 0 {
			continue
		}
		id := keyOf(k, o)
		if id == "" {
			continue
		}
		a := &Artifact{Kind: k, ID: id, Data: o, Source: LayerBuiltIn, Path: bi.Path(rel), PackPath: rel}
		if !languageMatch(o, dirs.Languages) {
			r.ScopeFiltered[id] = true
			continue
		}
		builtIn[id] = a
	}
	for id, a := range builtIn {
		r.items[id] = a
	}
	apply := func(dir, layer string) {
		if dir == "" {
			return
		}
		p := DiskPack(dir)
		for _, rel := range p.Walk(".", k.Suffix(), true) {
			b, err := p.ReadFile(rel)
			if err != nil {
				continue
			}
			o, err := yamlx.ParseObject(b)
			if err != nil || o.Len() == 0 {
				continue
			}
			id := o.GetString("id")
			if k == AgentProfile && id == "" {
				id = o.GetString("profile-id")
			}
			if id == "" {
				continue
			}
			data := o
			if base, ok := builtIn[id]; ok {
				if k == AgentProfile {
					data = deepMerge(base.Data, o)
				} else {
					data = base.Data.Clone()
					for _, kv := range o.Pairs() {
						data.Set(kv.Key, kv.Value)
					}
				}
			}
			if !languageMatch(data, dirs.Languages) {
				r.ScopeFiltered[id] = true
				continue
			}
			r.items[id] = &Artifact{Kind: k, ID: id, Data: data, Source: layer, Path: filepath.Join(dir, filepath.FromSlash(rel))}
			delete(r.ScopeFiltered, id)
		}
	}
	for _, d := range dirs.OrgDirs {
		apply(d, LayerOrg)
	}
	apply(dirs.ProjectDir, LayerProject)
	return r
}

func keyOf(k Kind, o *jsonx.Object) string {
	if k == AgentProfile {
		if s := o.GetString("profile-id"); s != "" {
			return s
		}
	}
	if v, ok := o.Get("id"); ok && v != nil {
		return pyStr(v)
	}
	return ""
}

func languageMatch(o *jsonx.Object, active []string) bool {
	if active == nil {
		return true
	}
	raw := o.GetList("applies_to_languages")
	if len(raw) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, l := range active {
		set[strings.ToLower(strings.TrimSpace(l))] = true
	}
	for _, x := range raw {
		if s, ok := x.(string); ok && set[strings.ToLower(strings.TrimSpace(s))] {
			return true
		}
	}
	return false
}

// Offering aggregates the per-kind repositories of a project (upstream
// CharterOfferingService): unfiltered by activation.
type Offering struct {
	ProjectRoot string   // project pack root (".kittify/charter-packs" dir) or ""
	OrgRoots    []string // org pack roots, declaration order
	Languages   []string
	cache       map[Kind]*Repository
}

// NewOffering builds the offering for repoRoot (project pack root
// .kittify/charter-packs, org packs from config). repoRoot "" means
// built-in only.
func NewOffering(repoRoot string, orgRoots []string) *Offering {
	o := &Offering{OrgRoots: orgRoots, cache: map[Kind]*Repository{}}
	if repoRoot != "" {
		o.ProjectRoot = filepath.Join(repoRoot, filepath.FromSlash(ProjectPackRoot))
	}
	return o
}

// ProjectKindDir is the project-layer directory for kind (upstream
// PROJECT_KIND_DIRS: singular for directive/tactic/styleguide/procedure,
// the plural name otherwise).
func ProjectKindDir(k Kind) string {
	switch k {
	case Directive, Tactic, Styleguide, Procedure:
		return string(k)
	}
	return k.Plural()
}

// Repo returns the repository of kind (cached).
func (o *Offering) Repo(k Kind) *Repository {
	if r, ok := o.cache[k]; ok {
		return r
	}
	dirs := LayerDirs{}
	for _, root := range o.OrgRoots {
		dirs.OrgDirs = append(dirs.OrgDirs, filepath.Join(root, k.Plural()))
	}
	if o.ProjectRoot != "" {
		dirs.ProjectDir = filepath.Join(o.ProjectRoot, ProjectKindDir(k))
	}
	switch k {
	case Tactic, Styleguide, Toolguide, Procedure, AgentProfile:
		dirs.Languages = o.Languages
	}
	r := LoadRepository(k, dirs)
	o.cache[k] = r
	return r
}

// BuiltInRel converts an absolute built-in file path back to its
// pack-relative slash path ("" when not inside the built-in pack).
func BuiltInRel(p string) string {
	root := BuiltInRoot()
	rel, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return path.Clean(filepath.ToSlash(rel))
}

// deepMerge recursively merges override into a clone of base (mappings
// merge key-wise, everything else replaces).
func deepMerge(base, override *jsonx.Object) *jsonx.Object {
	out := base.Clone()
	for _, kv := range override.Pairs() {
		if bo, ok := out.GetObject(kv.Key), true; ok && bo != nil {
			if oo, ok := kv.Value.(*jsonx.Object); ok {
				out.Set(kv.Key, deepMerge(bo, oo))
				continue
			}
		}
		out.Set(kv.Key, kv.Value)
	}
	return out
}
