package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// This file ports the org-tier DRG: charter.offering.drg.org_pack_loader
// (load_org_pack: fragment parsing/validation, file-discovered nodes,
// augmentation projection edges, governance-profile scope edges),
// charter.activation.drg_activation.load_org_drg and
// charter.offering.drg.merge.merge_three_layers (conflict records, endpoint
// resolution, provenance), plus validator.validate_dangling_references.

// OrgNode is a fragment node (id + canonical plural kind).
type OrgNode struct {
	ID, Kind   string
	Title      *string
	BodyPath   *string
	Discovered bool // file-discovered, not author-declared
}

// Dump is model_dump().
func (n OrgNode) Dump() *jsonx.Object {
	return jsonx.NewObject("id", n.ID, "kind", n.Kind, "title", optAny(n.Title), "body_path", optAny(n.BodyPath))
}

// OrgEdge is a fragment edge.
type OrgEdge struct {
	Source, Target, Relation string
	Reason                   *string
	GeneratedReason          string // projection edges only
	Projected                bool
}

// Dump is model_dump().
func (e OrgEdge) Dump() *jsonx.Object {
	o := jsonx.NewObject("source", e.Source, "target", e.Target, "relation", e.Relation, "reason", optAny(e.Reason))
	if e.Projected {
		o.Set("generated_reason", e.GeneratedReason)
	}
	return o
}

func optAny(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// OrgFragment is OrgDRGFragment.
type OrgFragment struct {
	PackName, SourceKind, SourceRef string
	LayerIndex                      int
	Nodes                           []OrgNode
	Edges                           []OrgEdge
	// Warnings are the UserWarnings the loader emitted (PACK_LEVEL_MISSING).
	Warnings []string
}

// OrgPackMissingError is raised when a pack root or its fragment is absent.
type OrgPackMissingError struct{ Pack, Path string }

func (e *OrgPackMissingError) Error() string {
	return fmt.Sprintf("Org pack %s configured at %s not found. Either fetch the pack (`spec-kitty charter fetch --pack <name>`) or remove the entry from `.kittify/config.yaml`.", dct.PyRepr(e.Pack), dct.PyRepr(e.Path))
}

// OrgPackParseError is a fragment YAML fault.
type OrgPackParseError struct{ Msg, SourceFile string }

func (e *OrgPackParseError) Error() string { return e.Msg }

// OrgPackSchemaError is a fragment / sibling schema fault.
type OrgPackSchemaError struct{ Msg, SourceFile string }

func (e *OrgPackSchemaError) Error() string { return e.Msg }

// orgKindAliases is _ORG_DRG_KIND_ALIASES (input form -> canonical plural).
var orgKindAliases = map[string]string{
	"directives": "directives", "tactics": "tactics", "styleguides": "styleguides", "toolguides": "toolguides",
	"paradigms": "paradigms", "procedures": "procedures", "agent_profiles": "agent_profiles", "templates": "templates",
	"assets": "assets", "glossary_packs": "glossary_packs", "skills": "skills", "mission_steps": "mission_steps",
	"mission_step_contracts": "mission_steps", "mission_types": "mission_types", "mission_type": "mission_types",
}

// OrgPluralToSingular is ORG_PLURAL_TO_SINGULAR_KIND.
var OrgPluralToSingular = map[string]string{
	"agent_profiles": "agent_profile", "assets": "asset", "directives": "directive", "glossary_packs": "glossary_pack",
	"mission_steps": "mission_step_contract", "mission_types": "mission_type", "paradigms": "paradigm",
	"procedures": "procedure", "skills": "skill", "styleguides": "styleguide", "tactics": "tactic",
	"templates": "template", "toolguides": "toolguide",
}

// augmentationKinds is AUGMENTATION_ELIGIBLE_KINDS in declaration order.
var augmentationKinds = [][2]string{{"directive", "directives"}, {"tactic", "tactics"}, {"styleguide", "styleguides"},
	{"toolguide", "toolguides"}, {"paradigm", "paradigms"}, {"procedure", "procedures"}, {"agent_profile", "agent_profiles"},
	{"mission_step_contract", "mission_step_contracts"}, {"glossary_pack", "glossary_packs"}, {"skill", "skills"},
	{"mission_type", "mission_types"}}

var augmentationGlobs = map[string]string{"directives": ".directive.yaml", "tactics": ".tactic.yaml", "styleguides": ".styleguide.yaml",
	"toolguides": ".toolguide.yaml", "paradigms": ".paradigm.yaml", "procedures": ".procedure.yaml", "agent_profiles": ".agent.yaml",
	"mission_step_contracts": ".step-contract.yaml", "glossary_packs": ".glossary-pack.yaml", "skills": ".skill.yaml"}

// AugmentationPluralKinds is augmentation_plural_kinds().
func AugmentationPluralKinds() map[string]bool {
	out := map[string]bool{}
	for _, k := range augmentationKinds {
		out[k[1]] = true
	}
	return out
}

// Relations is the canonical Relation enum order.
var Relations = []string{"requires", "suggests", "applies", "scope", "vocabulary", "instantiates", "replaces", "delegates_to",
	"specializes_from", "enhances", "overrides", "refines", "in_tension_with", "reconciles_tension", "rejects"}

// NodeKinds is the NodeKind enum order.
var NodeKinds = []string{"directive", "tactic", "paradigm", "styleguide", "toolguide", "procedure", "agent_profile",
	"mission_step_contract", "template", "asset", "action", "glossary_scope", "glossary", "glossary_pack", "mission_type",
	"anti_pattern", "skill"}

var urnRe = regexp.MustCompile(`^[a-z_]+:[A-Za-z0-9_/.\-]+$`)

// IsValidURN is is_valid_urn.
func IsValidURN(s string) bool { return urnRe.MatchString(s) }

func sortedOrgKinds() []string {
	out := make([]string, 0, len(orgKindAliases))
	for k := range orgKindAliases {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// readYAMLFile is yaml.safe_load(path.read_text()).
func readYAMLFile(p string) (any, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return pyyaml.SafeLoad(string(b))
}

func loadArtifactData(p string) *jsonx.Object {
	v, err := readYAMLFile(p)
	if err != nil {
		return jsonx.NewObject()
	}
	if o, ok := v.(*jsonx.Object); ok {
		return o
	}
	return jsonx.NewObject()
}

func rglob(dir, suffix string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), suffix) && len(d.Name()) > len(suffix) {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func globSuffix(dir, suffix string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "*"+suffix))
	sort.Strings(m)
	var out []string
	for _, p := range m {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// checkOrgPackLevel is _check_org_pack_level (simplified: absent pack.yaml
// or absent pack_level is a warning; a non-org level is a schema error).
func checkOrgPackLevel(packRoot string) (string, error) {
	py := filepath.Join(packRoot, "pack.yaml")
	if st, err := os.Stat(py); err != nil || st.IsDir() {
		return fmt.Sprintf("PACK_LEVEL_MISSING: %s (org pack) has no 'pack.yaml'; add one declaring 'pack_level: org' (required once the deprecation window closes)", packRoot), nil
	}
	v, err := readYAMLFile(py)
	if err != nil {
		return "", &OrgPackSchemaError{fmt.Sprintf("Org pack %s: invalid pack descriptor: %v", py, err), py}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return "", &OrgPackSchemaError{fmt.Sprintf("Org pack %s: invalid pack descriptor: pack.yaml must be a mapping", py), py}
	}
	lv, ok := o.Get("pack_level")
	if !ok {
		return fmt.Sprintf("PACK_LEVEL_MISSING: %s has no 'pack_level'; add 'pack_level: org' (required once the deprecation window closes)", py), nil
	}
	if s, _ := lv.(string); s != "org" {
		return "", &OrgPackSchemaError{fmt.Sprintf("PACK_LEVEL_MISMATCH: %s declares pack_level %s but it is configured as a 'org' org pack", py, pyReprAny(lv)), py}
	}
	return "", nil
}

func pyReprAny(v any) string {
	if s, ok := v.(string); ok {
		return dct.PyRepr(s)
	}
	return pyValueRepr(v)
}

// LoadOrgPack is load_org_pack.
func LoadOrgPack(name, packRoot string, layerIndex int) (*OrgFragment, error) {
	if st, err := os.Stat(packRoot); err != nil || !st.IsDir() {
		return nil, &OrgPackMissingError{name, packRoot}
	}
	frag := &OrgFragment{PackName: name, SourceKind: "local_path", SourceRef: packRoot, LayerIndex: layerIndex}
	warn, err := checkOrgPackLevel(packRoot)
	if err != nil {
		return nil, err
	}
	if warn != "" {
		frag.Warnings = append(frag.Warnings, warn)
	}
	fy := filepath.Join(packRoot, "drg", "fragment.yaml")
	if _, err := os.Stat(fy); err != nil {
		return nil, &OrgPackMissingError{name, fy}
	}
	b, err := os.ReadFile(fy)
	if err != nil {
		return nil, err
	}
	v, err := pyyaml.SafeLoad(string(b))
	if err != nil {
		return nil, &OrgPackParseError{fmt.Sprintf("Org pack %s: YAML parse error in %s: %v", dct.PyRepr(name), fy, err), fy}
	}
	if v == nil {
		v = jsonx.NewObject()
	}
	data, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &OrgPackSchemaError{fmt.Sprintf("Org pack %s: schema validation error in %s: fragment must be a mapping", dct.PyRepr(name), fy), fy}
	}
	rawEdges, _ := data.Get("edges")
	if rawEdges == nil {
		rawEdges = []any{}
	}
	edgeList, ok := rawEdges.([]any)
	if !ok {
		return nil, &OrgPackSchemaError{fmt.Sprintf("Org pack %s: schema validation error in %s: edges must be a list or null", dct.PyRepr(name), fy), fy}
	}
	scope, err := collectGovernanceScopeEdges(packRoot)
	if err != nil {
		return nil, err
	}
	schemaFail := func(msg string) error {
		return &OrgPackSchemaError{fmt.Sprintf("Org pack %s: schema validation error in %s: %s", dct.PyRepr(name), fy, msg), fy}
	}
	if verr := validateFragmentShape(data, edgeList); verr != "" {
		return nil, schemaFail(verr)
	}
	rawNodes, _ := data.Get("nodes")
	nodeList, _ := rawNodes.([]any)
	for _, rn := range nodeList {
		o := rn.(*jsonx.Object)
		n := OrgNode{ID: o.GetString("id"), Kind: orgKindAliases[o.GetString("kind")]}
		if t, ok := o.Get("title"); ok && t != nil {
			s := t.(string)
			n.Title = &s
		}
		if t, ok := o.Get("body_path"); ok && t != nil {
			s := t.(string)
			n.BodyPath = &s
		}
		frag.Nodes = append(frag.Nodes, n)
	}
	for _, re := range edgeList {
		o := re.(*jsonx.Object)
		e := OrgEdge{Source: o.GetString("source"), Target: o.GetString("target"), Relation: o.GetString("relation")}
		if r, ok := o.Get("reason"); ok && r != nil {
			s := r.(string)
			e.Reason = &s
		}
		frag.Edges = append(frag.Edges, e)
	}
	frag.Edges = append(frag.Edges, collectAugmentationEdges(packRoot)...)
	frag.Edges = append(frag.Edges, scope...)
	seen := map[[2]string]bool{}
	for _, n := range frag.Nodes {
		seen[[2]string{n.Kind, n.ID}] = true
	}
	for _, n := range collectArtifactNodes(packRoot) {
		k := [2]string{n.Kind, n.ID}
		if !seen[k] {
			frag.Nodes = append(frag.Nodes, n)
			seen[k] = true
		}
	}
	return frag, nil
}

// validateFragmentShape emulates OrgDRGFragment.model_validate for the
// fields an author writes; returns pydantic's error text ("" when valid).
func validateFragmentShape(data *jsonx.Object, edges []any) string {
	var errs []pydErr
	known := map[string]bool{"pack_name": true, "source_kind": true, "source_ref": true, "layer_index": true,
		"provenance_marker": true, "nodes": true, "edges": true}
	if pm, ok := data.Get("provenance_marker"); ok {
		if s, _ := pm.(string); s != "org" {
			errs = append(errs, pydErr{"provenance_marker", "Input should be 'org'", "literal_error", pm})
		}
	}
	if rn, ok := data.Get("nodes"); ok {
		list, isList := rn.([]any)
		if !isList {
			errs = append(errs, pydErr{"nodes", "Input should be a valid list", "list_type", rn})
		}
		for i, item := range list {
			loc := fmt.Sprintf("nodes.%d", i)
			o, ok := item.(*jsonx.Object)
			if !ok {
				errs = append(errs, pydErr{loc, "Input should be a valid dictionary or instance of _OrgDRGNode", "model_type", item})
				continue
			}
			nodeKnown := map[string]bool{"id": true, "kind": true, "title": true, "body_path": true}
			for _, f := range []string{"id", "kind"} {
				v, ok := o.Get(f)
				if !ok {
					errs = append(errs, pydErr{loc + "." + f, "Field required", "missing", o})
					continue
				}
				s, isStr := v.(string)
				if !isStr {
					errs = append(errs, pydErr{loc + "." + f, "Input should be a valid string", "string_type", v})
					continue
				}
				if f == "kind" {
					if _, ok := orgKindAliases[s]; !ok {
						q := make([]string, 0)
						for _, k := range sortedOrgKinds() {
							q = append(q, dct.PyRepr(k))
						}
						errs = append(errs, pydErr{loc + ".kind", fmt.Sprintf("Value error, unknown kind %s: not in the canonical org-pack kind universe (C-009 binding, FR-032 mission-type expansion): [%s]", dct.PyRepr(s), strings.Join(q, ", ")), "value_error", v})
					}
				}
			}
			for _, f := range []string{"title", "body_path"} {
				if v, ok := o.Get(f); ok && v != nil {
					if _, isStr := v.(string); !isStr {
						errs = append(errs, pydErr{loc + "." + f, "Input should be a valid string", "string_type", v})
					}
				}
			}
			for _, kv := range o.Pairs() {
				if !nodeKnown[kv.Key] {
					errs = append(errs, pydErr{loc + "." + kv.Key, "Extra inputs are not permitted", "extra_forbidden", kv.Value})
				}
			}
		}
	}
	for i, item := range edges {
		loc := fmt.Sprintf("edges.%d", i)
		o, ok := item.(*jsonx.Object)
		if !ok {
			errs = append(errs, pydErr{loc, "Input should be a valid dictionary or instance of _OrgDRGEdge", "model_type", item})
			continue
		}
		edgeKnown := map[string]bool{"source": true, "target": true, "relation": true, "reason": true}
		for _, f := range []string{"source", "target", "relation"} {
			v, ok := o.Get(f)
			if !ok {
				errs = append(errs, pydErr{loc + "." + f, "Field required", "missing", o})
				continue
			}
			if _, isStr := v.(string); !isStr {
				errs = append(errs, pydErr{loc + "." + f, "Input should be a valid string", "string_type", v})
			}
		}
		if v, ok := o.Get("reason"); ok && v != nil {
			if _, isStr := v.(string); !isStr {
				errs = append(errs, pydErr{loc + ".reason", "Input should be a valid string", "string_type", v})
			}
		}
		for _, kv := range o.Pairs() {
			if !edgeKnown[kv.Key] {
				errs = append(errs, pydErr{loc + "." + kv.Key, "Extra inputs are not permitted", "extra_forbidden", kv.Value})
			}
		}
	}
	for _, kv := range data.Pairs() {
		if !known[kv.Key] {
			errs = append(errs, pydErr{kv.Key, "Extra inputs are not permitted", "extra_forbidden", kv.Value})
		}
	}
	if len(errs) == 0 {
		return ""
	}
	return formatPydErrors("OrgDRGFragment", errs)
}

// formatPydErrors renders pydantic's ValidationError text.
func formatPydErrors(model string, errs []pydErr) string {
	lines := []string{fmt.Sprintf("%d validation error%s for %s", len(errs), plural1(len(errs)), model)}
	for _, e := range errs {
		lines = append(lines, e.loc, fmt.Sprintf("  %s [type=%s, input_value=%s, input_type=%s]", e.msg, e.typ, truncRepr(pyValueRepr(e.input)), pyTypeOf(e.input)),
			"    For further information visit https://errors.pydantic.dev/2.14/v/"+e.typ)
	}
	return strings.Join(lines, "\n")
}

func collectArtifactNodes(packRoot string) []OrgNode {
	var nodes []OrgNode
	pluralByKind := map[string]string{}
	for p, s := range OrgPluralToSingular {
		pluralByKind[s] = p
	}
	for _, k := range dct.AllKinds {
		plural, ok := pluralByKind[string(k)]
		if !ok || k.Suffix() == "" {
			continue
		}
		for _, p := range rglob(filepath.Join(packRoot, k.Plural()), k.Suffix()) {
			data := loadArtifactData(p)
			key := "id"
			if k == dct.AgentProfile {
				key = "profile-id"
			}
			idv, _ := data.Get(key)
			id, ok := idv.(string)
			if !ok || strings.TrimSpace(id) == "" {
				continue
			}
			n := OrgNode{ID: id, Kind: plural, Discovered: true}
			tv, ok := data.Get("title")
			if !ok {
				tv, _ = data.Get("name")
			}
			if s, ok := tv.(string); ok {
				n.Title = &s
			}
			if s, ok := func() (string, bool) { v, _ := data.Get("body_path"); s, ok := v.(string); return s, ok }(); ok {
				n.BodyPath = &s
			}
			nodes = append(nodes, n)
		}
	}
	return nodes
}

func collectAugmentationEdges(packRoot string) []OrgEdge {
	var edges []OrgEdge
	for _, ak := range augmentationKinds {
		urnKind, plural := ak[0], ak[1]
		glob := augmentationGlobs[plural]
		dir := filepath.Join(packRoot, plural)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() || glob == "" {
			continue
		}
		var files []string
		if plural == "styleguides" {
			files = rglob(dir, glob)
		} else {
			files = globSuffix(dir, glob)
		}
		for _, f := range files {
			data := loadArtifactData(f)
			idv, _ := data.Get("id")
			id, ok := idv.(string)
			if !ok || id == "" {
				continue
			}
			for _, field := range []string{"enhances", "overrides", "specializes_from"} {
				tv, _ := data.Get(field)
				t, ok := tv.(string)
				if !ok || t == "" {
					continue
				}
				edges = append(edges, OrgEdge{Source: urnKind + ":" + id, Target: urnKind + ":" + t, Relation: field,
					GeneratedReason: fmt.Sprintf("declared via %s.%s field", urnKind, field), Projected: true})
			}
		}
	}
	return edges
}

var governanceScopeFields = [][2]string{{"selected_directives", "directive"}, {"selected_tactics", "tactic"},
	{"selected_paradigms", "paradigm"}, {"selected_styleguides", "styleguide"}, {"selected_toolguides", "toolguide"},
	{"selected_procedures", "procedure"}, {"selected_agent_profiles", "agent_profile"},
	{"selected_mission_step_contracts", "mission_step_contract"}}

// ArtifactToURN is artifact_to_urn.
func ArtifactToURN(kind, id string) string {
	if kind == "directive" {
		return "directive:" + dct.NormalizeDirectiveID(id)
	}
	return kind + ":" + id
}

func collectGovernanceScopeEdges(packRoot string) ([]OrgEdge, error) {
	dir := filepath.Join(packRoot, "mission_types")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, nil
	}
	m, _ := filepath.Glob(filepath.Join(dir, "*", "governance-profile.yaml"))
	sort.Strings(m)
	var edges []OrgEdge
	seen := map[[2]string]bool{}
	for _, p := range m {
		v, err := readYAMLFile(p)
		if err != nil {
			continue
		}
		data, ok := v.(*jsonx.Object)
		if !ok {
			continue
		}
		raw := ""
		for _, k := range []string{"mission_type", "id"} {
			if x, ok := data.Get(k); ok && x != nil && x != "" && x != false {
				raw = fmt.Sprint(x)
				break
			}
		}
		if raw == "" {
			raw = filepath.Base(filepath.Dir(p))
		}
		src := ArtifactToURN("mission_type", raw)
		for _, f := range governanceScopeFields {
			sv, _ := data.Get(f[0])
			if sv == nil {
				continue
			}
			list, ok := sv.([]any)
			if !ok {
				if s, isStr := sv.(string); isStr && s == "" {
					continue
				}
				return nil, &OrgPackSchemaError{fmt.Sprintf("%s: %s must be a list or null", p, f[0]), p}
			}
			for _, item := range list {
				id, ok := item.(string)
				if !ok || id == "" {
					continue
				}
				tgt := ArtifactToURN(f[1], id)
				k := [2]string{src, tgt}
				if seen[k] {
					continue
				}
				seen[k] = true
				edges = append(edges, OrgEdge{Source: src, Target: tgt, Relation: "scope",
					GeneratedReason: "declared via governance-profile.yaml " + f[0] + " selection", Projected: true})
			}
		}
	}
	return edges, nil
}

// LoadOrgDRG is load_org_drg(strict=True): one fragment per configured
// pack (layer index = declaration order). Registry warnings are returned.
func LoadOrgDRG(repoRoot string) ([]*OrgFragment, error) {
	reg := LoadPackRegistry(repoRoot)
	var out []*OrgFragment
	for i, p := range reg.Packs {
		root, err := p.EffectiveRoot(repoRoot)
		if err != nil {
			return nil, err
		}
		f, err := LoadOrgPack(p.Name, root, i+1)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// ---------------------------------------------------------------- merge

// MergedNode is a DRG node with provenance.
type MergedNode struct {
	URN, Kind  string
	Label      *string
	Provenance string
}

// MergedEdge is a DRG edge with provenance.
type MergedEdge struct {
	Source, Target, Relation string
	Reason                   *string
	Provenance               string
}

// MergedGraph is the merge_three_layers result.
type MergedGraph struct {
	Nodes []MergedNode // insertion order (dict order)
	Edges []MergedEdge
}

// NodeURNs returns the node URN set.
func (g *MergedGraph) NodeURNs() map[string]bool {
	out := map[string]bool{}
	for _, n := range g.Nodes {
		out[n.URN] = true
	}
	return out
}

// OrgConflict is OrgDRGConflict.
type OrgConflict struct {
	Kind              string
	ConflictingLayers []string
	TargetID          string
	BuiltInValue      any
	OrgValue          any
	ProjectValue      any
	ResolutionApplied string
}

// LayersRepr renders conflicting_layers like a Python list.
func (c OrgConflict) LayersRepr() string {
	q := make([]string, len(c.ConflictingLayers))
	for i, l := range c.ConflictingLayers {
		q[i] = dct.PyRepr(l)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// OrgConflictError is OrgDRGConflictError.
type OrgConflictError struct{ Conflicts []OrgConflict }

var conflictRemediations = [][2]string{
	{"edge_override", "Remediation (override): remove the override from the org pack, OR escalate the built-in invariant change via a spec-kitty governance proposal."},
	{"node_override", "Remediation (override): remove the override from the org pack, OR escalate the built-in invariant change via a spec-kitty governance proposal."},
	{"layer_rule_violation", "Remediation (layer rule): an org-pack node must not reference src/specify_cli/ — doctrine sits below the runtime layer."},
	{"kind_mismatch", "Remediation (kind): declare the node under a canonical org-pack plural kind (charter.offering.drg.org_pack_loader._ORG_DRG_KIND_ALIASES)."},
	{"unresolved_edge_endpoint", "Remediation (endpoint): an edge endpoint must be either a bare id the fragment declares in its own nodes:, or a fully-qualified DRG URN '<kind>:<id>' whose <kind> is a NodeKind member (e.g. 'agent_profile:researcher-ryan'). Nothing else resolves."},
	{"ambiguous_edge_endpoint", "Remediation (ambiguity): this bare id exists under more than one kind. Qualify it as '<kind>:<id>' to say which one you mean."},
	{"malformed_urn", "Remediation (URN syntax): a URN is '<lower_snake_kind>:<id>' where the id may contain letters, digits, '_', '/', '.' and '-' only."},
}

func (e *OrgConflictError) Error() string {
	lines := []string{fmt.Sprintf("%d org-DRG conflict(s):", len(e.Conflicts))}
	present := map[string]bool{}
	for _, c := range e.Conflicts {
		lines = append(lines, fmt.Sprintf("  - kind=%s, target_id=%s, layers=%s, resolution=%s", c.Kind, c.TargetID, c.LayersRepr(), c.ResolutionApplied))
		present[c.Kind] = true
	}
	for _, r := range conflictRemediations {
		if present[r[0]] {
			dup := false
			for _, l := range lines {
				if l == r[1] {
					dup = true
				}
			}
			if !dup {
				lines = append(lines, r[1])
			}
		}
	}
	return strings.Join(lines, "\n")
}

// HardFailures returns the fatal conflicts.
func (e *OrgConflictError) HardFailures() []OrgConflict {
	var out []OrgConflict
	for _, c := range e.Conflicts {
		if c.ResolutionApplied == "hard_fail" {
			out = append(out, c)
		}
	}
	return out
}

// Advisory returns the precedence-resolved conflicts.
func (e *OrgConflictError) Advisory() []OrgConflict {
	var out []OrgConflict
	for _, c := range e.Conflicts {
		if c.ResolutionApplied != "hard_fail" {
			out = append(out, c)
		}
	}
	return out
}

// HardFailureMessages is hard_failure_messages.
func (e *OrgConflictError) HardFailureMessages() []string {
	var out []string
	for _, c := range e.HardFailures() {
		out = append(out, fmt.Sprintf("org-DRG hard failure: %s target=%s layers=%s", c.Kind, c.TargetID, c.LayersRepr()))
	}
	return out
}

// UnknownRelationError is raised for an unrecognised edge relation.
type UnknownRelationError struct{ Relation, Source string }

func (e *UnknownRelationError) Error() string {
	q := make([]string, len(Relations))
	sorted := append([]string(nil), Relations...)
	sort.Strings(sorted)
	for i, r := range sorted {
		q[i] = dct.PyRepr(r)
	}
	return fmt.Sprintf("Unknown DRG relation %s in fragment %s: not a canonical relation and not a known alias. Valid relations: [%s]. Valid aliases: ['extends']. Remediation: use one of the valid relations/aliases, or extend the Relation enum via a spec-kitty governance proposal.",
		dct.PyRepr(e.Relation), dct.PyRepr(e.Source), strings.Join(q, ", "))
}

// DuplicateURNError is raised for a duplicate asset/template URN.
type DuplicateURNError struct {
	Code, URN string
	Count     int
}

func (e *DuplicateURNError) Error() string {
	return fmt.Sprintf("%s: URN %s appears %d times across the merged built-in/org/project layers — asset and template ids must be globally unique. Remediation: rename the id in one of the colliding packs/layers.", e.Code, dct.PyRepr(e.URN), e.Count)
}

func violatesLayerRule(n OrgNode) bool {
	blobs := []string{n.ID}
	if n.BodyPath != nil && *n.BodyPath != "" {
		blobs = append(blobs, *n.BodyPath)
	}
	if n.Title != nil && *n.Title != "" {
		blobs = append(blobs, *n.Title)
	}
	for _, b := range blobs {
		if strings.Contains(b, "src/specify_cli/") || strings.Contains(b, "specify_cli.") {
			return true
		}
	}
	return false
}

func bridgeNode(n OrgNode, source string) (string, *MergedNode, *OrgConflict) {
	singular, ok := OrgPluralToSingular[n.Kind]
	if !ok {
		return "", nil, &OrgConflict{Kind: "kind_mismatch", ConflictingLayers: []string{source}, TargetID: n.Kind, OrgValue: n.Dump(), ResolutionApplied: "hard_fail"}
	}
	urn := singular + ":" + n.ID
	if !IsValidURN(urn) {
		return "", nil, &OrgConflict{Kind: "malformed_urn", ConflictingLayers: []string{source}, TargetID: urn, OrgValue: n.Dump(), ResolutionApplied: "hard_fail"}
	}
	return urn, &MergedNode{URN: urn, Kind: singular, Label: n.Title, Provenance: source}, nil
}

// OrgLocalEndpointMap is org_local_endpoint_map (trusted=nil form when
// trusted == nil).
func OrgLocalEndpointMap(f *OrgFragment, trusted map[string]bool) map[string]string {
	source := "org:" + f.PackName
	var declared map[string]bool
	if trusted != nil {
		declared = map[string]bool{}
		for _, n := range f.Nodes {
			if n.Discovered {
				continue
			}
			s, ok := OrgPluralToSingular[n.Kind]
			if !ok {
				s = n.Kind
			}
			declared[s+":"+n.ID] = true
		}
	}
	out := map[string]string{}
	for _, n := range f.Nodes {
		if violatesLayerRule(n) {
			continue
		}
		urn, _, c := bridgeNode(n, source)
		if c != nil {
			continue
		}
		if declared == nil || declared[urn] || trusted[urn] {
			out[n.ID] = urn
		}
	}
	return out
}

func isNodeKind(s string) bool {
	for _, k := range NodeKinds {
		if k == s {
			return true
		}
	}
	return false
}

// EndpointError is EndpointResolutionError.
type EndpointError struct{ Kind, Raw string }

func (e *EndpointError) Error() string {
	return fmt.Sprintf("cannot resolve DRG edge endpoint %s: %s", dct.PyRepr(e.Raw), e.Kind)
}

// ResolveEdgeEndpoint is resolve_edge_endpoint.
func ResolveEdgeEndpoint(raw string, local map[string]string, builtInURNs map[string]bool) (string, error) {
	if u, ok := local[raw]; ok {
		return u, nil
	}
	if prefix, _, found := strings.Cut(raw, ":"); found && isNodeKind(prefix) {
		if IsValidURN(raw) {
			return raw, nil
		}
		return "", &EndpointError{"malformed_urn", raw}
	}
	var matches []string
	for u := range builtInURNs {
		if _, id, ok := strings.Cut(u, ":"); ok && id == raw {
			matches = append(matches, u)
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		return "", &EndpointError{"ambiguous_edge_endpoint", raw}
	}
	return "", &EndpointError{"unresolved_edge_endpoint", raw}
}

func resolveRelation(rel, source string) (string, error) {
	for _, r := range Relations {
		if r == rel {
			return r, nil
		}
	}
	if rel == "extends" {
		return "specializes_from", nil
	}
	return "", &UnknownRelationError{rel, source}
}

// GraphFromDoctrine converts a doctrine.Graph into a MergedGraph tagged
// with provenance.
func GraphFromDoctrine(g *dct.Graph, provenance string) *MergedGraph {
	mg := &MergedGraph{}
	if g == nil {
		return mg
	}
	for _, n := range g.Nodes {
		var label *string
		if n.Label != "" {
			l := n.Label
			label = &l
		}
		mg.Nodes = append(mg.Nodes, MergedNode{URN: n.URN, Kind: n.Kind, Label: label, Provenance: provenance})
	}
	for _, e := range g.Edges {
		mg.Edges = append(mg.Edges, MergedEdge{Source: e.Source, Target: e.Target, Relation: e.Relation, Reason: e.Reason, Provenance: provenance})
	}
	return mg
}

// MergeThreeLayers is merge_three_layers(built_in, org_fragments, project).
// Errors: *OrgConflictError, *UnknownRelationError, *DuplicateURNError.
// The second result lists the dangling-endpoint WARNINGs (source marker,
// URN) the merge logs.
func MergeThreeLayers(builtIn *MergedGraph, frags []*OrgFragment, proj *MergedGraph) (*MergedGraph, error) {
	var conflicts []OrgConflict
	order := []string{}
	nodes := map[string]MergedNode{}
	put := func(n MergedNode) {
		if _, ok := nodes[n.URN]; !ok {
			order = append(order, n.URN)
		}
		nodes[n.URN] = n
	}
	var edges []MergedEdge
	invariant := map[string]bool{}
	for _, n := range builtIn.Nodes {
		n.Provenance = dct.LayerBuiltIn
		put(n)
		invariant[n.URN] = true
	}
	for _, e := range builtIn.Edges {
		e.Provenance = dct.LayerBuiltIn
		edges = append(edges, e)
	}
	type contribution struct {
		edge   MergedEdge
		source string
	}
	var contribs []contribution
	kept := map[[3]string]bool{}
	for _, f := range frags {
		source := "org:" + f.PackName
		var surviving []OrgNode
		for _, n := range f.Nodes {
			if violatesLayerRule(n) {
				conflicts = append(conflicts, OrgConflict{Kind: "layer_rule_violation", ConflictingLayers: []string{source}, TargetID: n.ID, OrgValue: n.Dump(), ResolutionApplied: "hard_fail"})
				continue
			}
			surviving = append(surviving, n)
		}
		for _, n := range surviving {
			urn, mn, c := bridgeNode(n, source)
			if c != nil {
				conflicts = append(conflicts, *c)
				continue
			}
			if invariant[urn] {
				bi := nodes[urn]
				if mn.Kind != bi.Kind {
					conflicts = append(conflicts, OrgConflict{Kind: "node_override", ConflictingLayers: []string{dct.LayerBuiltIn, source}, TargetID: urn, BuiltInValue: bi, OrgValue: n.Dump(), ResolutionApplied: "hard_fail"})
					continue
				}
				put(*mn)
				conflicts = append(conflicts, OrgConflict{Kind: "node_override", ConflictingLayers: []string{dct.LayerBuiltIn, source}, TargetID: urn, BuiltInValue: bi, OrgValue: n.Dump(), ResolutionApplied: "org_override"})
				continue
			}
			if _, ok := nodes[urn]; !ok {
				put(*mn)
			}
		}
		local := OrgLocalEndpointMap(f, nil)
		for _, e := range f.Edges {
			rel, err := resolveRelation(e.Relation, source)
			if err != nil {
				return nil, err
			}
			s, err1 := ResolveEdgeEndpoint(e.Source, local, invariant)
			var t string
			var err2 error
			if err1 == nil {
				t, err2 = ResolveEdgeEndpoint(e.Target, local, invariant)
			}
			if err1 != nil || err2 != nil {
				ee := err1
				if ee == nil {
					ee = err2
				}
				x := ee.(*EndpointError)
				conflicts = append(conflicts, OrgConflict{Kind: x.Kind, ConflictingLayers: []string{source}, TargetID: x.Raw, OrgValue: e.Dump(), ResolutionApplied: "hard_fail"})
				continue
			}
			me := MergedEdge{Source: s, Target: t, Relation: rel, Reason: e.Reason, Provenance: source}
			k := [3]string{s, t, rel}
			if kept[k] {
				continue
			}
			kept[k] = true
			contribs = append(contribs, contribution{me, source})
			edges = append(edges, me)
		}
	}
	for _, c := range conflicts {
		if c.ResolutionApplied == "hard_fail" {
			return nil, &OrgConflictError{conflicts}
		}
	}
	if proj != nil {
		for _, n := range proj.Nodes {
			n.Provenance = dct.LayerProject
			put(n)
		}
		for _, e := range proj.Edges {
			e.Provenance = dct.LayerProject
			edges = append(edges, e)
		}
	}
	// asset/template global uniqueness over the raw per-layer union.
	var cands []string
	for _, n := range builtIn.Nodes {
		if n.Kind == "asset" || n.Kind == "template" {
			cands = append(cands, n.URN)
		}
	}
	for _, f := range frags {
		for _, n := range f.Nodes {
			if n.Kind != "assets" && n.Kind != "templates" {
				continue
			}
			if urn, _, c := bridgeNode(n, "org:"+f.PackName); c == nil {
				cands = append(cands, urn)
			}
		}
	}
	if proj != nil {
		for _, n := range proj.Nodes {
			if n.Kind == "asset" || n.Kind == "template" {
				cands = append(cands, n.URN)
			}
		}
	}
	for _, prefix := range []string{"asset:", "template:"} {
		counts := map[string]int{}
		var first []string
		for _, u := range cands {
			if strings.HasPrefix(u, prefix) {
				if counts[u] == 0 {
					first = append(first, u)
				}
				counts[u]++
			}
		}
		for _, u := range first {
			if counts[u] > 1 {
				code := "duplicate_asset_id"
				if prefix == "template:" {
					code = "duplicate_template_id"
				}
				return nil, &DuplicateURNError{code, u, counts[u]}
			}
		}
	}
	mg := &MergedGraph{Edges: edges}
	for _, u := range order {
		mg.Nodes = append(mg.Nodes, nodes[u])
	}
	return mg, nil
}

// ValidateDanglingReferences is validate_dangling_references.
func ValidateDanglingReferences(g *MergedGraph) []string {
	urns := g.NodeURNs()
	var out []string
	for _, e := range g.Edges {
		if !urns[e.Source] {
			out = append(out, fmt.Sprintf("Dangling source: edge (%s --%s--> %s) references non-existent node %s", e.Source, e.Relation, e.Target, dct.PyRepr(e.Source)))
		}
		if !urns[e.Target] {
			out = append(out, fmt.Sprintf("Dangling target: edge (%s --%s--> %s) references non-existent node %s", e.Source, e.Relation, e.Target, dct.PyRepr(e.Target)))
		}
	}
	return out
}

// BuiltInMergedGraph is load_built_in_graph() as a MergedGraph.
func BuiltInMergedGraph() (*MergedGraph, error) {
	g, err := dct.BuiltInGraph()
	if err != nil {
		return nil, err
	}
	return GraphFromDoctrine(g, dct.LayerBuiltIn), nil
}
