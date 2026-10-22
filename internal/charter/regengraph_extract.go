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
	Tags      []string
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
	v, err := yamlx.ParseWith(b, yamlx.ParseOptions{Safe: true, File: path})
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

type regenExtractor struct {
	nodes *nodeSet
	edges *edgeDict
}

func (x *regenExtractor) addRefEdge(source, refType, refID, relation string, when, reason *string) {
	if !graphNodeKinds[refType] {
		return
	}
	tgt := ArtifactToURN(refType, refID)
	x.nodes.ensure(tgt, refType, nil)
	x.edges.add(regenEdge{source, tgt, relation, when, reason})
}

// refDict yields the mapping entries of a references list (non-mapping
// entries would raise AttributeError upstream; they are skipped).
func refDicts(l []any) []*jsonx.Object {
	var out []*jsonx.Object
	for _, r := range l {
		if o, ok := r.(*jsonx.Object); ok {
			out = append(out, o)
		}
	}
	return out
}

// extractArtifactEdges is extract_artifact_edges.
func extractArtifactEdges(root string) ([]*regenNode, []regenEdge, error) {
	x := &regenExtractor{nodes: newNodeSet(), edges: newEdgeDict(true)}
	packs := regenArtifactsRoot(root)
	load := func(p string) (*jsonx.Object, error) { return regenLoadYAML(p) }

	if d := filepath.Join(packs, "directives"); isDirPath(d) {
		for _, p := range globFiles(d, ".directive.yaml") {
			data, err := load(p)
			if err != nil {
				return nil, nil, err
			}
			if data == nil {
				continue
			}
			src := ArtifactToURN("directive", getStr(data, "id"))
			x.nodes.ensure(src, "directive", strp(getStr(data, "title")))
			for _, t := range getList(data, "tactic_refs") {
				tgt := ArtifactToURN("tactic", pyStrVal(t))
				x.nodes.ensure(tgt, "tactic", nil)
				x.edges.add(regenEdge{src, tgt, "requires", nil, nil})
			}
			for _, ref := range refDicts(getList(data, "references")) {
				rt, rid := getStr(ref, "type"), getStr(ref, "id")
				if rt == "" || rid == "" || !graphNodeKinds[rt] {
					continue
				}
				tgt := ArtifactToURN(rt, rid)
				x.nodes.ensure(tgt, rt, nil)
				x.edges.add(regenEdge{src, tgt, relationForRefType(rt), getOptStr(ref, "when"), getOptStr(ref, "reason")})
			}
		}
	}
	if d := filepath.Join(packs, "tactics"); isDirPath(d) {
		for _, p := range rglobFiles(d, suffixMatch(".tactic.yaml")) {
			data, err := load(p)
			if err != nil {
				return nil, nil, err
			}
			if data == nil {
				continue
			}
			src := ArtifactToURN("tactic", getStr(data, "id"))
			x.nodes.ensure(src, "tactic", strp(getStr(data, "name")))
			refs := refDicts(getList(data, "references"))
			for _, st := range getList(data, "steps") {
				if so, ok := st.(*jsonx.Object); ok {
					refs = append(refs, refDicts(getList(so, "references"))...)
				}
			}
			for _, ref := range refs {
				rt, rid := getStr(ref, "type"), getStr(ref, "id")
				if rt == "" || rid == "" || !graphNodeKinds[rt] {
					continue
				}
				tgt := ArtifactToURN(rt, rid)
				x.nodes.ensure(tgt, rt, nil)
				x.edges.add(regenEdge{src, tgt, "suggests", getOptStr(ref, "when"), getOptStr(ref, "reason")})
			}
		}
	}
	if d := filepath.Join(packs, "paradigms"); isDirPath(d) {
		for _, p := range globFiles(d, ".paradigm.yaml") {
			data, err := load(p)
			if err != nil {
				return nil, nil, err
			}
			if data == nil {
				continue
			}
			src := ArtifactToURN("paradigm", getStr(data, "id"))
			x.nodes.ensure(src, "paradigm", strp(getStr(data, "name")))
			for _, t := range getList(data, "tactic_refs") {
				tgt := ArtifactToURN("tactic", pyStrVal(t))
				x.nodes.ensure(tgt, "tactic", nil)
				x.edges.add(regenEdge{src, tgt, "requires", nil, nil})
			}
			for _, t := range getList(data, "directive_refs") {
				tgt := ArtifactToURN("directive", pyStrVal(t))
				x.nodes.ensure(tgt, "directive", nil)
				x.edges.add(regenEdge{src, tgt, "requires", nil, nil})
			}
			for _, ref := range refDicts(getList(data, "references")) {
				rt, rid := getStr(ref, "type"), getStr(ref, "id")
				if rt == "" || rid == "" {
					continue
				}
				x.addRefEdge(src, rt, rid, relationForProcedureRefType(rt), getOptStr(ref, "when"), getOptStr(ref, "reason"))
			}
		}
	}
	if d := filepath.Join(packs, "procedures"); isDirPath(d) {
		for _, p := range globFiles(d, ".procedure.yaml") {
			data, err := load(p)
			if err != nil {
				return nil, nil, err
			}
			if data == nil {
				continue
			}
			src := ArtifactToURN("procedure", getStr(data, "id"))
			x.nodes.ensure(src, "procedure", strp(getStr(data, "name")))
			for _, ref := range refDicts(getList(data, "references")) {
				rt, rid := getStr(ref, "type"), getStr(ref, "id")
				if rt == "" || rid == "" {
					continue
				}
				x.addRefEdge(src, rt, rid, relationForProcedureRefType(rt), getOptStr(ref, "when"), getOptStr(ref, "reason"))
			}
		}
	}
	// Agent profiles.
	if d := filepath.Join(packs, "agent_profiles"); isDirPath(d) {
		for _, p := range globFiles(d, ".agent.yaml") {
			data, err := load(p)
			if err != nil {
				return nil, nil, err
			}
			if data == nil {
				continue
			}
			pid := getStr(data, "profile-id")
			if pid == "" {
				continue
			}
			src := ArtifactToURN("agent_profile", pid)
			var label *string
			if l := getStr(data, "name"); l != "" {
				label = &l
			}
			x.nodes.ensure(src, "agent_profile", label)
			for _, ref := range refDicts(getList(data, "directive-references")) {
				if code := getStr(ref, "code"); code != "" {
					x.addRefEdge(src, "directive", code, "requires", nil, nil)
				}
			}
			for _, ref := range refDicts(getList(data, "tactic-references")) {
				if id := getStr(ref, "id"); id != "" {
					x.addRefEdge(src, "tactic", id, "requires", nil, getOptStr(ref, "rationale"))
				}
			}
			for _, rt := range []string{"toolguide", "styleguide"} {
				for _, ref := range refDicts(getList(data, rt+"-references")) {
					if id := getStr(ref, "id"); id != "" {
						x.addRefEdge(src, rt, id, "suggests", nil, getOptStr(ref, "rationale"))
					}
				}
			}
		}
	}
	// Styleguides and toolguides: path-string references.
	for _, kd := range []struct{ dir, kind string }{{"styleguides", "styleguide"}, {"toolguides", "toolguide"}} {
		d := filepath.Join(packs, kd.dir)
		if !isDirPath(d) {
			continue
		}
		for _, p := range rglobFiles(d, suffixMatch("."+kd.kind+".yaml")) {
			data, err := load(p)
			if err != nil {
				return nil, nil, err
			}
			if data == nil {
				continue
			}
			id := getStr(data, "id")
			if id == "" {
				continue
			}
			src := ArtifactToURN(kd.kind, id)
			var label *string
			if t := getStr(data, "title"); t != "" {
				label = &t
			}
			x.nodes.ensure(src, kd.kind, label)
			for _, r := range getList(data, "references") {
				s, ok := r.(string)
				if !ok {
					continue
				}
				rk, rid, ok := resolvePathRef(s)
				if !ok || !graphNodeKinds[rk] {
					continue
				}
				tgt := ArtifactToURN(rk, rid)
				x.nodes.ensure(tgt, rk, nil)
				x.edges.add(regenEdge{src, tgt, "suggests", nil, nil})
			}
		}
	}
	// Glossary packs and skills (source nodes only).
	for _, p := range globFilesIfDir(filepath.Join(packs, "glossary_packs"), ".glossary-pack.yaml") {
		data, err := load(p)
		if err != nil {
			return nil, nil, err
		}
		if data == nil {
			continue
		}
		if id := getStr(data, "id"); id != "" {
			x.nodes.ensure(ArtifactToURN("glossary_pack", id), "glossary_pack", nil)
		}
	}
	for _, p := range globFilesIfDir(filepath.Join(packs, "skills"), ".skill.yaml") {
		data, err := load(p)
		if err != nil {
			return nil, nil, err
		}
		if data == nil {
			continue
		}
		if id := getStr(data, "id"); id != "" {
			x.nodes.ensure(ArtifactToURN("skill", id), "skill", nil)
		}
	}
	if err := x.emitOperatingProcedureEdges(filepath.Join(packs, "agent_profiles")); err != nil {
		return nil, nil, err
	}
	for _, c := range curatedArtifactEdges {
		x.nodes.ensure(c[0], strings.SplitN(c[0], ":", 2)[0], nil)
		x.nodes.ensure(c[1], strings.SplitN(c[1], ":", 2)[0], nil)
		x.edges.add(regenEdge{c[0], c[1], c[2], nil, nil})
	}
	return x.nodes.list(), x.edges.list(), nil
}

func globFilesIfDir(dir, suffix string) []string {
	if !isDirPath(dir) {
		return nil
	}
	return globFiles(dir, suffix)
}

// emitOperatingProcedureEdges is _emit_operating_procedure_edges.
func (x *regenExtractor) emitOperatingProcedureEdges(profilesDir string) error {
	if !isDirPath(profilesDir) {
		return nil
	}
	byKind := map[string]map[string]bool{}
	for _, n := range x.nodes.list() {
		if byKind[n.Kind] == nil {
			byKind[n.Kind] = map[string]bool{}
		}
		byKind[n.Kind][n.URN] = true
	}
	procs := byKind["procedure"]
	type entryList struct {
		profile string
		entries []string
	}
	var entries []entryList
	idx := map[string]int{}
	for _, p := range globFiles(profilesDir, ".agent.yaml") {
		data, err := regenLoadYAML(p)
		if err != nil {
			return err
		}
		if data == nil {
			continue
		}
		pv, _ := data.Get("profile-id")
		if !pyTruthy(pv) {
			continue
		}
		pid := pyStrVal(pv)
		var raw []any
		if c, ok := data.Get("collaboration"); ok && pyTruthy(c) {
			if co, ok := c.(*jsonx.Object); ok {
				if r, ok := co.Get("operating-procedures"); ok && pyTruthy(r) {
					raw, _ = r.([]any)
				}
			}
		}
		var es []string
		for _, e := range raw {
			if pyTruthy(e) {
				es = append(es, pyStrVal(e))
			}
		}
		if i, ok := idx[pid]; ok {
			entries[i].entries = es
		} else {
			idx[pid] = len(entries)
			entries = append(entries, entryList{pid, es})
		}
	}
	type unresolved struct{ profile, entry, reason string }
	var bad []unresolved
	var kinds []string
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, el := range entries {
		src := ArtifactToURN("agent_profile", el.profile)
		for _, e := range el.entries {
			tgt := ArtifactToURN("procedure", e)
			if procs[tgt] {
				x.edges.add(regenEdge{src, tgt, "requires", nil, nil})
				continue
			}
			reason := "no_node"
			for _, k := range kinds {
				if k != "procedure" && byKind[k][ArtifactToURN(k, e)] {
					reason = "wrong_kind"
					break
				}
			}
			bad = append(bad, unresolved{el.profile, e, reason})
		}
	}
	if len(bad) > 0 {
		sort.SliceStable(bad, func(i, j int) bool {
			if bad[i].profile != bad[j].profile {
				return bad[i].profile < bad[j].profile
			}
			return bad[i].entry < bad[j].entry
		})
		var parts []string
		for _, u := range bad {
			parts = append(parts, fmt.Sprintf("%s:%s (%s)", u.profile, u.entry, u.reason))
		}
		return graphCrash("ValueError", "built-in operating-procedures entries do not resolve to a procedure node (triage required): "+strings.Join(parts, ", "),
			fr{Name: "generate_graph", Line: 1803}, fr{Name: "extract_artifact_edges", Line: 1295}, fr{Name: "_emit_operating_procedure_edges", Line: 996})
	}
	return nil
}

var actionScopeFields = [][2]string{{"directives", "directive"}, {"tactics", "tactic"}, {"paradigms", "paradigm"},
	{"styleguides", "styleguide"}, {"toolguides", "toolguide"}, {"procedures", "procedure"}, {"agent_profiles", "agent_profile"}}

// extractActionEdges is extract_action_edges.
func extractActionEdges(root string) ([]*regenNode, []regenEdge, error) {
	nodes := newNodeSet()
	edges := newEdgeDict(false)
	md := regenMissionsRoot(root)
	if !isDirPath(md) {
		return nil, nil, nil
	}
	files := rglobFiles(md, func(rel string) bool {
		parts := strings.Split(rel, "/")
		n := len(parts)
		return n >= 3 && parts[n-1] == "index.yaml" && parts[n-3] == "actions"
	})
	for _, p := range files {
		data, err := regenLoadYAML(p)
		if err != nil {
			return nil, nil, err
		}
		if data == nil {
			continue
		}
		action := filepath.Base(filepath.Dir(p))
		if v, ok := data.Get("action"); ok {
			action = pyStrVal(v)
		}
		mission := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(p))))
		au := "action:" + mission + "/" + action
		nodes.ensure(au, "action", strp(action))
		for _, f := range actionScopeFields {
			for _, raw := range getList(data, f[0]) {
				tgt := ArtifactToURN(f[1], pyStrVal(raw))
				nodes.ensure(tgt, f[1], nil)
				edges.add(regenEdge{au, tgt, "scope", nil, nil})
			}
		}
	}
	return nodes.list(), edges.list(), nil
}

// discoverBuiltInArtifactNodes is _discover_built_in_artifact_nodes.
func discoverBuiltInArtifactNodes(root string, nodes *nodeSet) error {
	for _, sd := range [][2]string{{"styleguides", "styleguide"}, {"toolguides", "toolguide"}, {"procedures", "procedure"},
		{"agent_profiles", "agent_profile"}, {"assets", "asset"}} {
		d := filepath.Join(regenArtifactsRoot(root), sd[0])
		if !isDirPath(d) {
			continue
		}
		kind := sd[1]
		suffix, idKey := "."+kind+".yaml", "id"
		if kind == "agent_profile" {
			suffix, idKey = ".agent.yaml", "profile-id"
		}
		for _, p := range rglobFiles(d, suffixMatch(suffix)) {
			data, err := regenLoadYAML(p)
			if err != nil {
				return err
			}
			if data == nil {
				continue
			}
			id := getStr(data, idKey)
			var label string
			if v, ok := data.Get("name"); ok {
				label = pyStrVal(v)
			} else {
				label = getStr(data, "title")
			}
			if id == "" {
				continue
			}
			var l *string
			if label != "" {
				l = &label
			}
			nodes.ensure(ArtifactToURN(kind, id), kind, l)
		}
	}
	return nil
}

type missionTypeData struct {
	id   string
	data *jsonx.Object
	path string
}

func iterMissionTypeData(root string) ([]missionTypeData, error) {
	d := filepath.Join(regenMissionsRoot(root), "mission_types")
	if !isDirPath(d) {
		return nil, nil
	}
	var out []missionTypeData
	for _, p := range globFiles(d, ".yaml") {
		data, err := regenLoadYAML(p)
		if err != nil {
			return nil, err
		}
		if data == nil {
			continue
		}
		if id := getStr(data, "id"); id != "" {
			out = append(out, missionTypeData{id, data, p})
		}
	}
	return out, nil
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
	for _, n := range append(artNodes, actNodes...) {
		nodes.ensure(n.URN, n.Kind, n.Label)
	}
	if err := discoverBuiltInArtifactNodes(root, nodes); err != nil {
		return nil, err
	}
	mts, err := iterMissionTypeData(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	for _, mt := range mts {
		if prev, ok := seen[mt.id]; ok {
			return nil, graphCrash("ValueError", fmt.Sprintf("Duplicate mission_type id %s declared by both %s and %s in %s",
				pyjson.ReprString(mt.id), filepath.Base(prev), filepath.Base(mt.path), filepath.Dir(mt.path)),
				fr{Name: "generate_graph", Line: 1817}, fr{Name: "_discover_mission_type_nodes", Line: 1473})
		}
		seen[mt.id] = mt.path
		var l *string
		if dn := getStr(mt.data, "display_name"); dn != "" {
			l = &dn
		}
		nodes.ensure(ArtifactToURN("mission_type", mt.id), "mission_type", l)
	}
	// Step contracts.
	if cd := filepath.Join(regenMissionsRoot(root), "built_in_step_contracts"); isDirPath(cd) {
		seenC := map[string]string{}
		for _, p := range globFiles(cd, ".step-contract.yaml") {
			data, err := regenLoadYAML(p)
			if err != nil {
				return nil, err
			}
			if data == nil {
				continue
			}
			mission, action := getStr(data, "mission"), getStr(data, "action")
			if mission == "" || action == "" {
				continue
			}
			urn := ArtifactToURN("mission_step_contract", mission+"/"+action)
			if prev, ok := seenC[urn]; ok {
				return nil, graphCrash("ValueError", fmt.Sprintf("Duplicate mission_step_contract %s declared by both %s and %s in %s",
					pyjson.ReprString(urn), filepath.Base(prev), filepath.Base(p), filepath.Dir(p)),
					fr{Name: "generate_graph", Line: 1821}, fr{Name: "_discover_mission_step_contract_nodes", Line: 1523})
			}
			seenC[urn] = p
			nodes.ensure(urn, "mission_step_contract", strp(action))
		}
	}
	// Template instantiation (nodes + action --instantiates--> template).
	var tmplEdges []regenEdge
	seenT := map[string]bool{}
	for _, mt := range mts {
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
			for _, x := range getList(mt.data, "action_sequence") {
				seq = append(seq, pyStrVal(x))
			}
		}
		src := ArtifactToURN("mission_type", mt.id)
		for _, step := range seq {
			mtEdges = append(mtEdges, regenEdge{src, ArtifactToURN("action", mt.id+"/"+step), "requires", nil, nil})
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
		mt := filepath.Base(filepath.Dir(p))
		if v, ok := data.Get("mission_type"); ok {
			mt = pyStrVal(v)
		}
		src := ArtifactToURN("mission_type", mt)
		for _, f := range governanceScopeFields {
			for _, raw := range getList(data, f[0]) {
				d.add(regenEdge{src, ArtifactToURN(f[1], pyStrVal(raw)), "scope", nil, nil})
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
