package charter

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports the DRG extractor (charter.offering.drg.migration:
// extractor, calibrator, id_normalizer, hand_authored_overlay) that
// `charter pack regenerate-graph` runs over a built-in-level pack root
// (DOC-07). generateReferenceGraph is
// hand_authored_overlay.generate_reference_graph_with_overlay.

type regenNode struct {
	URN, Kind string
	Label     *string
	// RawLabel is a non-str label set by _ensure_node's unvalidated
	// model_copy (it fails validation when the node is rebuilt).
	RawLabel any
	Tags     []string
}

type regenEdge struct {
	Source, Target, Relation string
	When, Reason             *string
}

// RegenCrash is an uncaught Python exception raised by the extractor
// (ValueError for fail-closed authoring checks, pydantic ValidationError for
// malformed URNs, ruamel YAML errors); the CLI renders it as a traceback.
// Phase is "graph" (generate_graph) or "manifest" (the manifest
// generator); Frames are the upstream frames below that phase's entry.
type RegenCrash struct {
	Type, Msg string
	Phase     string
	Frames    []rich.Frame
}

func (e *RegenCrash) Error() string { return e.Type + ": " + e.Msg }

func graphCrash(typ, msg string, frames ...rich.Frame) *RegenCrash {
	return &RegenCrash{Type: typ, Msg: msg, Phase: "graph", Frames: frames}
}

type fr = rich.Frame

// yamlCrash is the uncaught ruamel YAMLError of a load.
func yamlCrash(err error, phase string) error {
	if m, ok := err.(*pyyaml.MarkedYAMLError); ok {
		return &RegenCrash{Type: m.Class, Msg: m.Error(), Phase: phase}
	}
	if r, ok := err.(*pyyaml.ReaderError); ok {
		return &RegenCrash{Type: "ReaderError", Msg: r.Error(), Phase: phase}
	}
	return &RegenCrash{Type: "UnicodeDecodeError", Msg: err.Error(), Phase: phase}
}

// nodeSet is an insertion-ordered dict[str, DRGNode].
type nodeSet struct {
	order []string
	m     map[string]*regenNode
}

func newNodeSet() *nodeSet { return &nodeSet{m: map[string]*regenNode{}} }

func (s *nodeSet) has(urn string) bool { _, ok := s.m[urn]; return ok }

// ensure is _ensure_node.
func (s *nodeSet) ensure(urn, kind string, label *string) {
	if n, ok := s.m[urn]; ok {
		if label != nil && *label != "" && n.Label == nil {
			l := *label
			n.Label = &l
		}
		return
	}
	var l *string
	if label != nil {
		v := *label
		l = &v
	}
	s.order = append(s.order, urn)
	s.m[urn] = &regenNode{URN: urn, Kind: kind, Label: l}
}

func (s *nodeSet) list() []*regenNode {
	out := make([]*regenNode, len(s.order))
	for i, u := range s.order {
		out[i] = s.m[u]
	}
	return out
}

// edgeDict is an insertion-ordered dict keyed by (source, target, relation).
type edgeDict struct {
	order []string
	m     map[string]*regenEdge
	merge bool // _merge_edge_metadata on duplicates (else first wins)
}

func newEdgeDict(merge bool) *edgeDict { return &edgeDict{m: map[string]*regenEdge{}, merge: merge} }

func tripleKey(e *regenEdge) string { return e.Source + "\x00" + e.Target + "\x00" + e.Relation }

func (d *edgeDict) add(e regenEdge) {
	k := tripleKey(&e)
	if ex, ok := d.m[k]; ok {
		if d.merge {
			if ex.When == nil && e.When != nil {
				ex.When = e.When
			}
			if ex.Reason == nil && e.Reason != nil {
				ex.Reason = e.Reason
			}
		}
		return
	}
	d.order = append(d.order, k)
	d.m[k] = &e
}

func (d *edgeDict) list() []regenEdge {
	out := make([]regenEdge, len(d.order))
	for i, k := range d.order {
		out[i] = *d.m[k]
	}
	return out
}

var (
	reGraphURN = regexp.MustCompile(`^[a-z_]+:[A-Za-z0-9_/.\-]+$`)
)

// graphNodeKinds is NodeKind (values), _KIND_MAP's key set.
var graphNodeKinds = map[string]bool{}

func init() {
	for _, k := range []string{"directive", "tactic", "paradigm", "styleguide", "toolguide", "procedure", "agent_profile",
		"mission_step_contract", "template", "asset", "action", "glossary_scope", "glossary", "glossary_pack",
		"anti_pattern", "mission_type", "skill"} {
		graphNodeKinds[k] = true
	}
}

// pyStrVal is str(x) for a YAML scalar (the f-string formatting the
// extractor applies to ids).
func pyStrVal(x any) string {
	switch t := x.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return pyjson.Repr(t)
	}
	return fmt.Sprint(x)
}

func pyTruthy(x any) bool {
	switch t := x.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case *jsonx.Object:
		return t.Len() > 0
	}
	return true
}

// getStr is data.get(key, "") rendered as str.
func getStr(o *jsonx.Object, key string) string {
	v, ok := o.Get(key)
	if !ok {
		return ""
	}
	return pyStrVal(v)
}

// getOptStr is data.get(key) for a str | None metadata field.
func getOptStr(o *jsonx.Object, key string) *string {
	v, ok := o.Get(key)
	if !ok || v == nil {
		return nil
	}
	s := pyStrVal(v)
	return &s
}

// getList is data.get(key, []) or [].
func getList(o *jsonx.Object, key string) []any {
	v, _ := o.Get(key)
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

func strp(s string) *string { return &s }

func regenLoadYAML(path string) (*jsonx.Object, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// _yaml = YAML(typ="safe"); _yaml.load(path) reads a binary stream.
	// Python's value space (dates, int64), since the walkers apply Python
	// type semantics to what they read.
	v, err := pyyaml.LoadRuamelInputPy(pyyaml.FileInput(b, path), pyyaml.RuamelSafe)
	if err != nil {
		return nil, yamlCrash(err, "graph")
	}
	o, _ := v.(*jsonx.Object)
	return o, nil
}

// pathLess is pathlib.PurePosixPath ordering (component-wise).
func pathLess(a, b string) bool {
	pa, pb := strings.Split(filepath.ToSlash(a), "/"), strings.Split(filepath.ToSlash(b), "/")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

// globFiles is sorted(dir.glob("*<suffix>")) restricted to regular files.
func globFiles(dir, suffix string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), suffix) && len(e.Name()) >= len(suffix) {
			p := filepath.Join(dir, e.Name())
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return pathLess(out[i], out[j]) })
	return out
}

// rglobFiles is sorted(dir.rglob(name-predicate)) restricted to regular files.
func rglobFiles(dir string, match func(rel string) bool) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if !match(filepath.ToSlash(rel)) {
			return nil
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return pathLess(out[i], out[j]) })
	return out
}

func suffixMatch(suffix string) func(string) bool {
	return func(rel string) bool { return strings.HasSuffix(path1(rel), suffix) }
}

func path1(rel string) string { return rel[strings.LastIndex(rel, "/")+1:] }

// regenArtifactsRoot is _artifacts_root (the package-root shape cannot
// occur outside a Python install).
func regenArtifactsRoot(root string) string { return root }

func regenMissionsRoot(root string) string { return filepath.Join(root, "missions") }

func relationForRefType(t string) string {
	if t == "directive" {
		return "requires"
	}
	return "suggests"
}

func relationForProcedureRefType(t string) string {
	switch t {
	case "directive", "tactic", "procedure":
		return "requires"
	}
	return "suggests"
}

var pathKindPatterns = func() []struct {
	re   *regexp.Regexp
	kind string
} {
	mk := func(dir, ext string) *regexp.Regexp {
		return regexp.MustCompile(`(?:src/charter/offering/` + dir + `/built-in|packs/built-in/` + dir + `)/(?:.+/)?([^/]+)\.` + ext + `\.yaml$`)
	}
	return []struct {
		re   *regexp.Regexp
		kind string
	}{
		{mk("tactics", "tactic"), "tactic"}, {mk("paradigms", "paradigm"), "paradigm"},
		{mk("directives", "directive"), "directive"}, {mk("styleguides", "styleguide"), "styleguide"},
		{mk("toolguides", "toolguide"), "toolguide"}, {mk("procedures", "procedure"), "procedure"},
		{mk("agent_profiles", "agent"), "agent_profile"},
	}
}()

// resolvePathRef is _resolve_path_ref.
func resolvePathRef(s string) (string, string, bool) {
	for _, p := range pathKindPatterns {
		if m := p.re.FindStringSubmatch(s); m != nil {
			return p.kind, m[1], true
		}
	}
	return "", "", false
}

// regenStep is the slice of a validated MissionStep the extractor reads.
type regenStep struct {
	id               string
	sequenceIndex    *int
	inActionSequence bool
	templateFile     string
	hasTemplate      bool
}

var reIdentifier = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// loadRegenStep is _load_step_yaml + MissionStep validation.
func loadRegenStep(p string) *regenStep {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil
	}
	str := func(k string, required bool, pat *regexp.Regexp) (string, bool) {
		x, present := o.Get(k)
		if !present {
			return "", !required
		}
		s, isStr := x.(string)
		if !isStr {
			return "", false
		}
		if pat != nil && !pat.MatchString(s) {
			return "", false
		}
		return s, true
	}
	id, ok1 := str("id", true, reIdentifier)
	_, ok2 := str("display_name", true, nil)
	st, ok3 := str("step_type", true, nil)
	_, ok4 := str("prompt_template", true, nil)
	if !ok1 || !ok2 || !ok3 || !ok4 || (st != "agent" && st != "human_in_loop" && st != "integration") {
		return nil
	}
	if x, present := o.Get("agent_profile"); present && x != nil {
		if s, isStr := x.(string); !isStr || !reIdentifier.MatchString(s) {
			return nil
		}
	}
	if x, present := o.Get("recommended_model_tier"); present && x != nil {
		if _, isStr := x.(string); !isStr {
			return nil
		}
	}
	if x, present := o.Get("depends_on"); present {
		l, isList := x.([]any)
		if !isList {
			return nil
		}
		for _, it := range l {
			if _, isStr := it.(string); !isStr {
				return nil
			}
		}
	}
	s := &regenStep{id: id}
	if x, present := o.Get("sequence_index"); present && x != nil {
		switch n := x.(type) {
		case int:
			s.sequenceIndex = &n
		case int64:
			i := int(n)
			s.sequenceIndex = &i
		default:
			return nil
		}
	}
	if x, present := o.Get("in_action_sequence"); present {
		bv, isBool := x.(bool)
		if !isBool {
			return nil
		}
		s.inActionSequence = bv
	}
	if x, present := o.Get("template"); present && x != nil {
		t, isObj := x.(*jsonx.Object)
		if !isObj {
			return nil
		}
		ak, okA := t.Get("artifact_key")
		tf, okT := t.Get("template_file")
		aks, isA := ak.(string)
		tfs, isT := tf.(string)
		if !okA || !okT || !isA || !isT || !reIdentifier.MatchString(aks) {
			return nil
		}
		for _, kv := range t.Pairs() {
			if kv.Key != "artifact_key" && kv.Key != "template_file" {
				return nil
			}
		}
		s.templateFile, s.hasTemplate = tfs, true
	}
	return s
}

// builtinSteps is MissionStepRepository.resolve_all_for_mission_type(type,
// pack_context=None) over <missions>/mission-steps, sequence-ordered.
func builtinSteps(root, missionType string) []*regenStep {
	d := filepath.Join(regenMissionsRoot(root), "mission-steps", missionType)
	ents, err := os.ReadDir(d)
	if err != nil {
		return nil
	}
	var out []*regenStep
	for _, e := range ents {
		p := filepath.Join(d, e.Name(), "step.yaml")
		if !isDirPath(filepath.Join(d, e.Name())) {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if s := loadRegenStep(p); s != nil {
			out = append(out, s)
		}
	}
	key := func(s *regenStep) int {
		if s.sequenceIndex == nil {
			return 1 << 31
		}
		return *s.sequenceIndex
	}
	sort.SliceStable(out, func(i, j int) bool {
		if key(out[i]) != key(out[j]) {
			return key(out[i]) < key(out[j])
		}
		return out[i].id < out[j].id
	})
	return out
}

// generateReferenceGraph is generate_reference_graph_with_overlay: the
// extracted, calibrated, validated graph plus the hand-authored overlay.
func generateReferenceGraph(root string) (*dct.Graph, error) {
	g, err := generatePureGraph(root)
	if err != nil {
		return nil, err
	}
	nodes := map[string]dct.Node{}
	for _, n := range g.Nodes {
		nodes[n.URN] = n
	}
	for _, n := range handAuthoredNodes {
		nodes[n.URN] = toDctNode(&n)
	}
	edges := map[string]dct.Edge{}
	for _, e := range g.Edges {
		edges[e.Source+"\x00"+e.Target+"\x00"+e.Relation] = e
	}
	for _, e := range handAuthoredEdges {
		edges[e.Source+"\x00"+e.Target+"\x00"+e.Relation] = dct.Edge{Source: e.Source, Target: e.Target, Relation: e.Relation, When: e.When, Reason: e.Reason}
	}
	merged := &dct.Graph{SchemaVersion: g.SchemaVersion, GeneratedAt: g.GeneratedAt, GeneratedBy: g.GeneratedBy}
	for _, n := range nodes {
		merged.Nodes = append(merged.Nodes, n)
	}
	for _, e := range edges {
		merged.Edges = append(merged.Edges, e)
	}
	sortGraph(merged)
	if err := dct.AssertValid(merged); err != nil {
		return nil, err
	}
	return merged, nil
}

func sortGraph(g *dct.Graph) {
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].URN < g.Nodes[j].URN })
	sort.SliceStable(g.Edges, func(i, j int) bool { return edgeLess(g.Edges[i], g.Edges[j]) })
}

func edgeLess(a, b dct.Edge) bool {
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	if a.Target != b.Target {
		return a.Target < b.Target
	}
	return a.Relation < b.Relation
}

// regenLabelUnset marks a node whose label is None (dct.Node keeps "" for
// absent labels; an explicit empty label is kept distinct for output).
const regenLabelUnset = "\x00none"

func toDctNode(n *regenNode) dct.Node {
	label := regenLabelUnset
	if n.Label != nil {
		label = *n.Label
	}
	return dct.Node{URN: n.URN, Kind: n.Kind, Label: label, Tags: n.Tags}
}

func validateRegenNode(n *regenNode) error {
	if !reGraphURN.MatchString(n.URN) {
		return graphCrash("pydantic_core._pydantic_core.ValidationError", fmt.Sprintf("1 validation error for DRGNode\n  Value error, URN %s does not match pattern %s", pyjson.ReprString(n.URN), reGraphURN.String()))
	}
	return nil
}

// generatePureGraph is extractor.generate_graph (minus the write).
func generatePureGraph(root string) (*dct.Graph, error) {
	artNodes, artEdges, err := extractArtifactEdges(root)
	if err != nil {
		return nil, err
	}
	actNodes, actEdges, err := extractActionEdges(root)
	if err != nil {
		return nil, err
	}
	nodes := newNodeSet()
	merge := site{{Name: "generate_graph", Line: 1811}}
	for _, n := range append(artNodes, actNodes...) {
		if err := nodes.ensurePy(merge, n.URN, n.Kind, labelOf(n)); err != nil {
			return nil, err
		}
	}
	if err := discoverBuiltInArtifactNodes(root, nodes); err != nil {
		return nil, err
	}
	mts, err := iterMissionTypeData(root)
	if err != nil {
		return nil, err
	}
	if err := discoverMissionTypeNodes(mts, nodes); err != nil {
		return nil, err
	}
	if err := discoverStepContractNodes(root, nodes); err != nil {
		return nil, err
	}
	// Template instantiation (nodes + action --instantiates--> template).
	var tmplEdges []regenEdge
	seenT := map[string]bool{}
	for _, mt := range mts {
		if _, isStr := mt.rawID.(string); !isStr {
			// MissionStepRepository joins the id onto a Path.
			return nil, graphCrash("TypeError", fmt.Sprintf("unsupported operand type(s) for /: 'PosixPath' and '%s'", pyTpName(mt.rawID)),
				fr{Name: "generate_graph", Line: 1826}, fr{Name: "extract_template_instantiation_edges", Line: 1757},
				fr{Name: "resolve_all_for_mission_type", Line: 412}, fr{Name: "_resolve_all_for_mission_type_cached", Line: 563},
				fr{Name: "_resolve_all_for_mission_type_uncached", Line: 440})
		}
		for _, s := range builtinSteps(root, mt.id) {
			if !s.hasTemplate {
				continue
			}
			tid := mt.id + "/" + s.templateFile
			urn := "template:" + tid
			if !seenT[urn] {
				seenT[urn] = true
				nodes.ensure(urn, "template", strp(tid))
			}
			tmplEdges = append(tmplEdges, regenEdge{ArtifactToURN("action", mt.id+"/"+s.id), urn, "instantiates", nil, nil})
		}
	}
	sort.SliceStable(tmplEdges, func(i, j int) bool {
		if tmplEdges[i].Source != tmplEdges[j].Source {
			return tmplEdges[i].Source < tmplEdges[j].Source
		}
		return tmplEdges[i].Target < tmplEdges[j].Target
	})
	// Mission type -> action requires edges.
	var mtEdges []regenEdge
	for _, mt := range mts {
		var seq []string
		for _, s := range builtinSteps(root, mt.id) {
			if s.inActionSequence {
				seq = append(seq, s.id)
			}
		}
		if len(seq) > 0 {
			if msg := validateActionSequence(seq); msg != "" {
				return nil, graphCrash("ValueError", msg, fr{Name: "generate_graph", Line: 1834}, fr{Name: "extract_mission_type_edges", Line: 1574},
					fr{Name: "_resolve_action_sequence", Line: 1553}, fr{Name: "project_action_sequence", Line: 101}, fr{Name: "validate_action_sequence", Line: 200})
			}
		} else {
			mte := site{{Name: "generate_graph", Line: 1834}, {Name: "extract_mission_type_edges", Line: 1574}}
			items, err := iterGet(mte.at("_resolve_action_sequence", 1554), mt.data, "action_sequence")
			if err != nil {
				return nil, err
			}
			for _, x := range items {
				seq = append(seq, pyStrAny(x))
			}
		}
		src := "mission_type:" + mt.id
		for _, step := range seq {
			e, err := newEdgePy(site{{Name: "generate_graph", Line: 1834}, {Name: "extract_mission_type_edges", Line: 1577}},
				src, "action:"+mt.id+"/"+step, "requires", false, nil, nil)
			if err != nil {
				return nil, err
			}
			mtEdges = append(mtEdges, e)
		}
	}
	govEdges, err := extractGovernanceScopeEdges(root)
	if err != nil {
		return nil, err
	}
	var unresolved []string
	govTargets := map[string]bool{}
	for _, e := range govEdges {
		govTargets[e.Target] = true
		if nodes.has(e.Target) {
			continue
		}
		mtID := e.Source[strings.Index(e.Source, ":")+1:]
		tk, tid := e.Target, ""
		if i := strings.Index(e.Target, ":"); i >= 0 {
			tk, tid = e.Target[:i], e.Target[i+1:]
		}
		field := tk
		for _, f := range governanceScopeFields {
			if f[1] == tk {
				field = f[0]
				break
			}
		}
		unresolved = append(unresolved, fmt.Sprintf("%s:%s=%s", mtID, field, tid))
	}
	if len(unresolved) > 0 {
		return nil, graphCrash("ValueError", "governance-profile.yaml selected_* entries do not resolve to an existing node (triage required): "+strings.Join(unresolved, ", "),
			fr{Name: "generate_graph", Line: 1845}, fr{Name: "assert_governance_scope_edges_resolve", Line: 1710})
	}
	all := append(append(append(append(append([]regenEdge{}, artEdges...), actEdges...), mtEdges...), govEdges...), tmplEdges...)
	allNodes := nodes.list()
	calibrated := calibrateSurfaces(allNodes, all)
	for _, e := range calibrated {
		for _, urn := range []string{e.Source, e.Target} {
			if nodes.has(urn) {
				continue
			}
			if govTargets[urn] {
				return nil, graphCrash("ValueError", fmt.Sprintf("governance scope-edge target %s unresolved after upfront validation (fail-closed defense-in-depth)", pyjson.ReprString(urn)),
					fr{Name: "generate_graph", Line: 1873})
			}
			prefix := strings.SplitN(urn, ":", 2)[0]
			if !graphNodeKinds[prefix] {
				continue
			}
			nodes.ensure(urn, prefix, nil)
		}
	}
	g := &dct.Graph{SchemaVersion: "1.0", GeneratedAt: "STATIC", GeneratedBy: "drg-migration-v1"}
	for _, n := range nodes.list() {
		if err := validateRegenNode(n); err != nil {
			return nil, err
		}
		g.Nodes = append(g.Nodes, toDctNode(n))
	}
	for _, e := range calibrated {
		for _, f := range []struct{ name, v string }{{"source", e.Source}, {"target", e.Target}} {
			if !reGraphURN.MatchString(f.v) {
				return nil, graphCrash("pydantic_core._pydantic_core.ValidationError", fmt.Sprintf("1 validation error for DRGEdge\n  Value error, Edge %s %s does not match URN pattern %s", f.name, pyjson.ReprString(f.v), reGraphURN.String()))
			}
		}
		g.Edges = append(g.Edges, dct.Edge{Source: e.Source, Target: e.Target, Relation: e.Relation, When: e.When, Reason: e.Reason})
	}
	sortGraph(g)
	if err := dct.AssertValid(g); err != nil {
		return nil, err
	}
	return g, nil
}

func validateActionSequence(seq []string) string {
	seen := map[string]bool{}
	for _, s := range seq {
		if seen[s] {
			return "action_sequence must contain unique step IDs"
		}
		seen[s] = true
	}
	return ""
}

// extractGovernanceScopeEdges is extract_governance_profile_scope_edges.
func extractGovernanceScopeEdges(root string) ([]regenEdge, error) {
	md := regenMissionsRoot(root)
	if !isDirPath(md) {
		return nil, nil
	}
	ents, _ := os.ReadDir(md)
	var files []string
	for _, e := range ents {
		p := filepath.Join(md, e.Name(), "governance-profile.yaml")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			files = append(files, p)
		}
	}
	sort.Slice(files, func(i, j int) bool { return pathLess(files[i], files[j]) })
	d := newEdgeDict(false)
	for _, p := range files {
		data, err := regenLoadYAML(p)
		if err != nil {
			return nil, err
		}
		if data == nil {
			continue
		}
		src := "mission_type:" + pyStrAny(dget(data, "mission_type", filepath.Base(filepath.Dir(p))))
		gp := func(line int) site {
			return site{{Name: "generate_graph", Line: 1838}}.at("extract_governance_profile_scope_edges", line)
		}
		for _, f := range governanceScopeFields {
			items, err := iterGet(gp(1650), data, f[0])
			if err != nil {
				return nil, err
			}
			for _, raw := range items {
				tgt, err := pyURN(gp(1651), f[1], raw)
				if err != nil {
					return nil, err
				}
				e, err := newEdgePy(gp(1657), src, tgt, "scope", false, nil, nil)
				if err != nil {
					return nil, err
				}
				d.add(e)
			}
		}
	}
	return d.list(), nil
}

// calibrateSurfaces is calibrator.calibrate_surfaces.
func calibrateSurfaces(nodes []*regenNode, edges []regenEdge) []regenEdge {
	var impl, rev string
	for _, n := range nodes {
		if n.Kind != "action" {
			continue
		}
		if strings.HasSuffix(n.URN, "/implement") {
			impl = n.URN
		} else if strings.HasSuffix(n.URN, "/review") {
			rev = n.URN
		}
	}
	out := append([]regenEdge{}, edges...)
	if impl == "" || rev == "" {
		return out
	}
	targets := func(src string) map[string]bool {
		m := map[string]bool{}
		for _, e := range edges {
			if e.Source == src && e.Relation == "scope" {
				m[e.Target] = true
			}
		}
		return m
	}
	it, rt := targets(impl), targets(rev)
	if len(it) == 0 {
		return out
	}
	threshold := 0.80 * float64(len(it))
	if float64(len(rt)) >= threshold {
		return out
	}
	var missing []string
	for t := range it {
		if !rt[t] {
			missing = append(missing, t)
		}
	}
	sort.Strings(missing)
	for _, t := range missing {
		if float64(len(rt)) >= threshold {
			break
		}
		out = append(out, regenEdge{rev, t, "scope", nil, nil})
		rt[t] = true
	}
	return out
}

// graphFragmentText is _dump_graph_document for one fragment.
func graphFragmentText(g *dct.Graph) string {
	doc := jsonx.NewObject("schema_version", g.SchemaVersion, "generated_at", g.GeneratedAt, "generated_by", g.GeneratedBy)
	nodes := []any{}
	for _, n := range g.Nodes {
		o := jsonx.NewObject("urn", strings.TrimSpace(n.URN), "kind", n.Kind)
		if n.Label != regenLabelUnset {
			o.Set("label", pyStrip(n.Label))
		}
		if len(n.Tags) > 0 {
			tags := []any{}
			for _, t := range n.Tags {
				tags = append(tags, pyStrip(t))
			}
			o.Set("tags", tags)
		}
		nodes = append(nodes, o)
	}
	edges := []any{}
	for _, e := range g.Edges {
		o := jsonx.NewObject("source", pyStrip(e.Source), "target", pyStrip(e.Target), "relation", e.Relation)
		if e.When != nil {
			o.Set("when", pyStrip(*e.When))
		}
		if e.Reason != nil {
			o.Set("reason", pyStrip(*e.Reason))
		}
		edges = append(edges, o)
	}
	doc.Set("nodes", nodes)
	doc.Set("edges", edges)
	return yamlx.Emit(doc)
}

// pyStrip is str.strip() (Unicode whitespace).
func pyStrip(s string) string { return strings.TrimSpace(s) }

// GraphFragments is _partition_by_kind rendered: {"<kind>.graph.yaml": text}.
func GraphFragments(g *dct.Graph) map[string]string {
	kindOf := map[string]string{}
	byKind := map[string]*dct.Graph{}
	for _, n := range g.Nodes {
		kindOf[n.URN] = n.Kind
		f := byKind[n.Kind]
		if f == nil {
			f = &dct.Graph{SchemaVersion: g.SchemaVersion, GeneratedAt: g.GeneratedAt, GeneratedBy: g.GeneratedBy}
			byKind[n.Kind] = f
		}
		f.Nodes = append(f.Nodes, n)
	}
	for _, e := range g.Edges {
		if f := byKind[kindOf[e.Source]]; f != nil {
			f.Edges = append(f.Edges, e)
		}
	}
	out := map[string]string{}
	for k, f := range byKind {
		sortGraph(f)
		out[k+".graph.yaml"] = graphFragmentText(f)
	}
	return out
}
