package charter

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pydantic"
)

// The extractor's YAML walkers (extract_artifact_edges, extract_action_edges,
// the node-discovery passes and the governance-profile pass), applying
// Python semantics to the raw values: see regengraph_py.go. Line numbers
// are upstream charter/offering/drg/migration/extractor.py.

type regenExtractor struct {
	nodes *nodeSet
	edges *edgeDict
}

// orNone is `v or None`.
func orNone(v any) any {
	if pyTruthyAny(v) {
		return v
	}
	return nil
}

// labelOf is node.label as a raw value.
func labelOf(n *regenNode) any {
	if n.RawLabel != nil {
		return n.RawLabel
	}
	if n.Label != nil {
		return *n.Label
	}
	return nil
}

// addRefEdge is _add_ref_edge(...) called at s (relation already computed).
func (x *regenExtractor) addRefEdge(s site, source string, refType, refID any, relation string, when, reason any) error {
	in := func(line int) site { return s.at("_add_ref_edge", line) }
	kind, err := kindForType(in(768), refType)
	if err != nil || kind == "" {
		return err
	}
	tgt, err := pyURN(in(771), kind, refID)
	if err != nil {
		return err
	}
	if err := x.nodes.ensurePy(in(772), tgt, kind, nil); err != nil {
		return err
	}
	e, err := newEdgePy(in(774), source, tgt, relation, true, when, reason)
	if err != nil {
		return err
	}
	x.edges.add(e)
	return nil
}

// procedureRelation is _relation_for_procedure_ref_type(ref_type) at s.
func procedureRelation(s site, refType any) (string, error) {
	if pyUnhashable(refType) {
		return "", s.crash("TypeError", fmt.Sprintf("unhashable type: '%s'", pyTpName(refType)), fr{Name: "_relation_for_procedure_ref_type", Line: 715})
	}
	t, _ := refType.(string)
	return relationForProcedureRefType(t), nil
}

// typedRefs walks a `{type, id, when?, reason?}` references list: iter at
// iterLine, ref.get at getLine; each truthy (type, id) pair goes to fn.
func typedRefs(ea func(int) site, data *jsonx.Object, iterLine, getLine int, fn func(ref *jsonx.Object, rt, rid any) error) error {
	refs, err := iterGet(ea(iterLine), data, "references")
	if err != nil {
		return err
	}
	for _, r := range refs {
		rd, err := asDict(ea(getLine), r)
		if err != nil {
			return err
		}
		rt, rid := dget(rd, "type", ""), dget(rd, "id", "")
		if !pyTruthyAny(rt) || !pyTruthyAny(rid) {
			continue
		}
		if err := fn(rd, rt, rid); err != nil {
			return err
		}
	}
	return nil
}

// directRef handles a directive/tactic reference resolved through
// _kind_for_type + artifact_to_urn + _ensure_node + DRGEdge(**kwargs) at
// the given lines.
func (x *regenExtractor) directRef(ea func(int) site, src string, ref *jsonx.Object, rt, rid any, kindLine, urnLine, ensureLine, edgeLine int, relation func(string) string) error {
	kind, err := kindForType(ea(kindLine), rt)
	if err != nil || kind == "" {
		return err
	}
	tgt, err := pyURN(ea(urnLine), kind, rid)
	if err != nil {
		return err
	}
	if err := x.nodes.ensurePy(ea(ensureLine), tgt, kind, nil); err != nil {
		return err
	}
	e, err := newEdgePy(ea(edgeLine), src, tgt, relation(kind), true, dget(ref, "when", nil), dget(ref, "reason", nil))
	if err != nil {
		return err
	}
	x.edges.add(e)
	return nil
}

// bareRefs walks a list of bare ids (tactic_refs, directive_refs): iter,
// artifact_to_urn, _ensure_node and DRGEdge at the given lines.
func (x *regenExtractor) bareRefs(ea func(int) site, data *jsonx.Object, key, kind, src, relation string, iterLine, urnLine, ensureLine, edgeLine int) error {
	items, err := iterGet(ea(iterLine), data, key)
	if err != nil {
		return err
	}
	for _, it := range items {
		tgt, err := pyURN(ea(urnLine), kind, it)
		if err != nil {
			return err
		}
		if err := x.nodes.ensurePy(ea(ensureLine), tgt, kind, nil); err != nil {
			return err
		}
		e, err := newEdgePy(ea(edgeLine), src, tgt, relation, false, nil, nil)
		if err != nil {
			return err
		}
		x.edges.add(e)
	}
	return nil
}

// sourceNode mints a walker's own node: artifact_to_urn at urnLine,
// _ensure_node at ensureLine.
func (x *regenExtractor) sourceNode(ea func(int) site, kind string, id, label any, urnLine, ensureLine int) (string, error) {
	src, err := pyURN(ea(urnLine), kind, id)
	if err != nil {
		return "", err
	}
	return src, x.nodes.ensurePy(ea(ensureLine), src, kind, label)
}

// extractArtifactEdges is extract_artifact_edges.
func extractArtifactEdges(root string) ([]*regenNode, []regenEdge, error) {
	x := &regenExtractor{nodes: newNodeSet(), edges: newEdgeDict(true)}
	packs := regenArtifactsRoot(root)
	top := site{{Name: "generate_graph", Line: 1803}}
	ea := func(line int) site { return top.at("extract_artifact_edges", line) }
	sameRel := func(rel string) func(string) string { return func(string) string { return rel } }

	if d := filepath.Join(packs, "directives"); isDirPath(d) {
		for _, p := range globFiles(d, ".directive.yaml") {
			data, err := regenLoadYAML(p)
			if err != nil {
				return nil, nil, err
			}
			if data == nil {
				continue
			}
			src, err := x.sourceNode(ea, "directive", dget(data, "id", ""), dget(data, "title", ""), 1037, 1038)
			if err != nil {
				return nil, nil, err
			}
			if err := x.bareRefs(ea, data, "tactic_refs", "tactic", src, "requires", 1041, 1042, 1043, 1045); err != nil {
				return nil, nil, err
			}
			if err := typedRefs(ea, data, 1053, 1054, func(ref *jsonx.Object, rt, rid any) error {
				return x.directRef(ea, src, ref, rt, rid, 1058, 1061, 1062, 1064, relationForRefType)
			}); err != nil {
				return nil, nil, err
			}
		}
	}
	if d := filepath.Join(packs, "tactics"); isDirPath(d) {
		for _, p := range rglobFiles(d, suffixMatch(".tactic.yaml")) {
			data, err := regenLoadYAML(p)
			if err != nil {
				return nil, nil, err
			}
			if data == nil {
				continue
			}
			src, err := x.sourceNode(ea, "tactic", dget(data, "id", ""), dget(data, "name", ""), 1088, 1089)
			if err != nil {
				return nil, nil, err
			}
			if err := typedRefs(ea, data, 1092, 1093, func(ref *jsonx.Object, rt, rid any) error {
				return x.directRef(ea, src, ref, rt, rid, 1097, 1100, 1101, 1103, sameRel("suggests"))
			}); err != nil {
				return nil, nil, err
			}
			steps, err := iterGet(ea(1112), data, "steps")
			if err != nil {
				return nil, nil, err
			}
			for _, st := range steps {
				sd, err := asDict(ea(1113), st)
				if err != nil {
					return nil, nil, err
				}
				if err := typedRefs(ea, sd, 1113, 1114, func(ref *jsonx.Object, rt, rid any) error {
					return x.directRef(ea, src, ref, rt, rid, 1118, 1121, 1122, 1124, sameRel("suggests"))
				}); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	// Paradigms and procedures: references go through _add_ref_edge with the
	// procedure relation (evaluated first, at relLine).
	procRefs := func(src string, data *jsonx.Object, iterLine, getLine, relLine, callLine int) error {
		return typedRefs(ea, data, iterLine, getLine, func(ref *jsonx.Object, rt, rid any) error {
			rel, err := procedureRelation(ea(relLine), rt)
			if err != nil {
				return err
			}
			return x.addRefEdge(ea(callLine), src, rt, rid, rel, dget(ref, "when", nil), dget(ref, "reason", nil))
		})
	}
	if d := filepath.Join(packs, "paradigms"); isDirPath(d) {
		for _, p := range globFiles(d, ".paradigm.yaml") {
			data, err := regenLoadYAML(p)
			if err != nil {
				return nil, nil, err
			}
			if data == nil {
				continue
			}
			src, err := x.sourceNode(ea, "paradigm", dget(data, "id", ""), dget(data, "name", ""), 1141, 1142)
			if err != nil {
				return nil, nil, err
			}
			if err := x.bareRefs(ea, data, "tactic_refs", "tactic", src, "requires", 1145, 1146, 1147, 1149); err != nil {
				return nil, nil, err
			}
			if err := x.bareRefs(ea, data, "directive_refs", "directive", src, "requires", 1157, 1158, 1159, 1161); err != nil {
				return nil, nil, err
			}
			if err := procRefs(src, data, 1168, 1169, 1179, 1173); err != nil {
				return nil, nil, err
			}
		}
	}
	if d := filepath.Join(packs, "procedures"); isDirPath(d) {
		for _, p := range globFiles(d, ".procedure.yaml") {
			data, err := regenLoadYAML(p)
			if err != nil {
				return nil, nil, err
			}
			if data == nil {
				continue
			}
			src, err := x.sourceNode(ea, "procedure", dget(data, "id", ""), dget(data, "name", ""), 1192, 1193)
			if err != nil {
				return nil, nil, err
			}
			if err := procRefs(src, data, 1195, 1196, 1206, 1200); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := x.emitAgentProfileEdges(ea(1211), filepath.Join(packs, "agent_profiles")); err != nil {
		return nil, nil, err
	}
	// Styleguides and toolguides: path-string references.
	for _, kd := range []struct {
		dir, kind                                 string
		urnL, ensureL, iterL, refEnsureL, refEdgeL int
	}{{"styleguides", "styleguide", 1227, 1228, 1230, 1241, 1243}, {"toolguides", "toolguide", 1264, 1265, 1267, 1278, 1280}} {
		d := filepath.Join(packs, kd.dir)
		if !isDirPath(d) {
			continue
		}
		for _, p := range rglobFiles(d, suffixMatch("."+kd.kind+".yaml")) {
			data, err := regenLoadYAML(p)
			if err != nil {
				return nil, nil, err
			}
			if data == nil {
				continue
			}
			id, title := dget(data, "id", ""), dget(data, "title", "")
			if !pyTruthyAny(id) {
				continue
			}
			src, err := x.sourceNode(ea, kd.kind, id, orNone(title), kd.urnL, kd.ensureL)
			if err != nil {
				return nil, nil, err
			}
			refs, err := iterGet(ea(kd.iterL), data, "references")
			if err != nil {
				return nil, nil, err
			}
			for _, r := range refs {
				s, ok := r.(string)
				if !ok {
					continue
				}
				rk, rid, ok := resolvePathRef(s)
				if !ok || !graphNodeKinds[rk] {
					continue
				}
				tgt := ArtifactToURN(rk, rid)
				if err := x.nodes.ensurePy(ea(kd.refEnsureL), tgt, rk, nil); err != nil {
					return nil, nil, err
				}
				e, err := newEdgePy(ea(kd.refEdgeL), src, tgt, "suggests", false, nil, nil)
				if err != nil {
					return nil, nil, err
				}
				x.edges.add(e)
			}
		}
	}
	// Glossary packs and skills (source nodes only).
	gp := ea(1288)
	for _, p := range globFilesIfDir(filepath.Join(packs, "glossary_packs"), ".glossary-pack.yaml") {
		data, err := regenLoadYAML(p)
		if err != nil {
			return nil, nil, err
		}
		if data == nil {
			continue
		}
		if id := dget(data, "id", ""); pyTruthyAny(id) {
			in := func(line int) site { return gp.at("_emit_glossary_pack_nodes", line) }
			if _, err := x.sourceNode(in, "glossary_pack", id, nil, 923, 924); err != nil {
				return nil, nil, err
			}
		}
	}
	sk := ea(1289)
	for _, p := range globFilesIfDir(filepath.Join(packs, "skills"), ".skill.yaml") {
		data, err := regenLoadYAML(p)
		if err != nil {
			return nil, nil, err
		}
		var id any = ""
		if data != nil {
			id = dget(data, "id", "")
		}
		if pyTruthyAny(id) {
			in := func(line int) site { return sk.at("_emit_skill_nodes", line) }
			if _, err := x.sourceNode(in, "skill", id, nil, 943, 943); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := x.emitOperatingProcedureEdges(ea(1295), filepath.Join(packs, "agent_profiles")); err != nil {
		return nil, nil, err
	}
	for _, c := range curatedArtifactEdges {
		x.nodes.ensure(c[0], strings.SplitN(c[0], ":", 2)[0], nil)
		x.nodes.ensure(c[1], strings.SplitN(c[1], ":", 2)[0], nil)
		x.edges.add(regenEdge{c[0], c[1], c[2], nil, nil})
	}
	return x.nodes.list(), x.edges.list(), nil
}

// emitAgentProfileEdges is _emit_agent_profile_edges (called at s) plus
// _project_profile_reference_edges.
func (x *regenExtractor) emitAgentProfileEdges(s site, profilesDir string) error {
	if !isDirPath(profilesDir) {
		return nil
	}
	ap := func(line int) site { return s.at("_emit_agent_profile_edges", line) }
	for _, p := range globFiles(profilesDir, ".agent.yaml") {
		data, err := regenLoadYAML(p)
		if err != nil {
			return err
		}
		if data == nil {
			continue
		}
		pid := dget(data, "profile-id", "")
		if !pyTruthyAny(pid) {
			continue
		}
		src, err := x.sourceNode(ap, "agent_profile", pid, orNone(dget(data, "name", "")), 885, 887)
		if err != nil {
			return err
		}
		pp := func(line int) site { return ap(888).at("_project_profile_reference_edges", line) }
		walk := func(key, idKey string, iterL, getL, callL int, fn func(ref *jsonx.Object, id any, at site) error) error {
			refs, err := iterGet(pp(iterL), data, key)
			if err != nil {
				return err
			}
			for _, r := range refs {
				rd, err := asDict(pp(getL), r)
				if err != nil {
					return err
				}
				id := dget(rd, idKey, "")
				if !pyTruthyAny(id) {
					continue
				}
				if err := fn(rd, id, pp(callL)); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk("directive-references", "code", 816, 817, 820, func(_ *jsonx.Object, id any, at site) error {
			return x.addRefEdge(at, src, "directive", pyStrAny(id), "requires", nil, nil)
		}); err != nil {
			return err
		}
		if err := walk("tactic-references", "id", 828, 829, 832, func(rd *jsonx.Object, id any, at site) error {
			return x.addRefEdge(at, src, "tactic", id, "requires", nil, dget(rd, "rationale", nil))
		}); err != nil {
			return err
		}
		for _, rt := range []string{"toolguide", "styleguide"} {
			if err := walk(rt+"-references", "id", 842, 843, 846, func(rd *jsonx.Object, id any, at site) error {
				return x.addRefEdge(at, src, rt, id, "suggests", nil, dget(rd, "rationale", nil))
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func globFilesIfDir(dir, suffix string) []string {
	if !isDirPath(dir) {
		return nil
	}
	return globFiles(dir, suffix)
}

// pyKey is a dict key identity for a hashable YAML scalar.
func pyKey(v any) string { return pyTpName(v) + "\x00" + pyStrAny(v) }

// emitOperatingProcedureEdges is _emit_operating_procedure_edges (called at
// s).
func (x *regenExtractor) emitOperatingProcedureEdges(s site, profilesDir string) error {
	if !isDirPath(profilesDir) {
		return nil
	}
	op := func(line int) site { return s.at("_emit_operating_procedure_edges", line) }
	byKind := map[string]map[string]bool{}
	for _, n := range x.nodes.list() {
		if byKind[n.Kind] == nil {
			byKind[n.Kind] = map[string]bool{}
		}
		byKind[n.Kind][n.URN] = true
	}
	procs := byKind["procedure"]
	// collect_operating_procedure_entries
	type entryList struct {
		profile any
		entries []any
	}
	var entries []entryList
	idx := map[string]int{}
	co := func(line int) site { return op(982).at("collect_operating_procedure_entries", line) }
	for _, p := range globFiles(profilesDir, ".agent.yaml") {
		data, err := regenLoadYAML(p)
		if err != nil {
			return err
		}
		if data == nil {
			continue
		}
		pid := dget(data, "profile-id", nil)
		if !pyTruthyAny(pid) {
			continue
		}
		collab := dget(data, "collaboration", nil)
		if !pyTruthyAny(collab) {
			collab = jsonx.NewObject()
		}
		cd, err := asDict(co(99), collab)
		if err != nil {
			return err
		}
		raw := dget(cd, "operating-procedures", nil)
		items, msg := pyIterOr(raw)
		if msg != "" {
			return co(100).crash("TypeError", msg)
		}
		var es []any
		for _, e := range items {
			if pyTruthyAny(e) {
				es = append(es, e)
			}
		}
		if pyUnhashable(pid) {
			return co(100).crash("TypeError", fmt.Sprintf("unhashable type: '%s'", pyTpName(pid)))
		}
		if i, ok := idx[pyKey(pid)]; ok {
			entries[i].entries = es
		} else {
			idx[pyKey(pid)] = len(entries)
			entries = append(entries, entryList{pid, es})
		}
	}
	for _, el := range entries {
		src := "agent_profile:" + pyStrAny(el.profile)
		for _, e := range el.entries {
			tgt := "procedure:" + pyStrAny(e)
			if procs[tgt] {
				edge, err := newEdgePy(op(989), src, tgt, "requires", false, nil, nil)
				if err != nil {
					return err
				}
				x.edges.add(edge)
			}
		}
	}
	// resolve_operating_procedure_entries
	type unresolved struct {
		profile, entry any
		reason         string
	}
	var bad []unresolved
	var kinds []string
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	cl := func(line int) site {
		return op(991).at("resolve_operating_procedure_entries", 158).at("_classify", line)
	}
	for _, el := range entries {
		for _, e := range el.entries {
			if procs["procedure:"+pyStrAny(e)] {
				continue
			}
			reason := "no_node"
			for _, k := range kinds {
				if k == "procedure" {
					continue
				}
				u, err := pyURN(cl(138), k, e)
				if err != nil {
					return err
				}
				if byKind[k][u] {
					reason = "wrong_kind"
					break
				}
			}
			bad = append(bad, unresolved{el.profile, e, reason})
		}
	}
	if len(bad) == 0 {
		return nil
	}
	keys := make([][2]any, len(bad))
	for i, u := range bad {
		keys[i] = [2]any{u.profile, u.entry}
	}
	perm, msg := pySortPairs(keys)
	if msg != "" {
		return op(991).at("resolve_operating_procedure_entries", 161).crash("TypeError", msg)
	}
	var parts []string
	for _, i := range perm {
		u := bad[i]
		parts = append(parts, fmt.Sprintf("%s:%s (%s)", pyStrAny(u.profile), pyStrAny(u.entry), u.reason))
	}
	return op(996).crash("ValueError", "built-in operating-procedures entries do not resolve to a procedure node (triage required): "+strings.Join(parts, ", "))
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
	top := site{{Name: "generate_graph", Line: 1806}}
	ae := func(line int) site { return top.at("extract_action_edges", line) }
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
		action := dget(data, "action", filepath.Base(filepath.Dir(p)))
		mission := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(p))))
		au := "action:" + mission + "/" + pyStrAny(action)
		if err := nodes.ensurePy(ae(1339), au, "action", action); err != nil {
			return nil, nil, err
		}
		for _, f := range actionScopeFields {
			items, err := iterGet(ae(1344), data, f[0])
			if err != nil {
				return nil, nil, err
			}
			for _, raw := range items {
				tgt, err := pyURN(ae(1345), f[1], raw)
				if err != nil {
					return nil, nil, err
				}
				if err := nodes.ensurePy(ae(1351), tgt, f[1], nil); err != nil {
					return nil, nil, err
				}
				e, err := newEdgePy(ae(1353), au, tgt, "scope", false, nil, nil)
				if err != nil {
					return nil, nil, err
				}
				edges.add(e)
			}
		}
	}
	return nodes.list(), edges.list(), nil
}

// discoverBuiltInArtifactNodes is _discover_built_in_artifact_nodes.
func discoverBuiltInArtifactNodes(root string, nodes *nodeSet) error {
	top := site{{Name: "generate_graph", Line: 1814}, {Name: "_discover_built_in_artifact_nodes", Line: 1394}}
	in := func(line int) site { return top.at("_discover_built_in_nodes_in_dir", line) }
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
			id := dget(data, idKey, "")
			label := dget(data, "name", dget(data, "title", ""))
			if !pyTruthyAny(id) {
				continue
			}
			if err := nodes.ensurePy(in(1419), kind+":"+pyStrAny(id), kind, orNone(label)); err != nil {
				return err
			}
		}
	}
	return nil
}

type missionTypeData struct {
	id    string // str(id)
	rawID any
	data  *jsonx.Object
	path  string
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
		if id := dget(data, "id", ""); pyTruthyAny(id) {
			out = append(out, missionTypeData{pyStrAny(id), id, data, p})
		}
	}
	return out, nil
}

// discoverMissionTypeNodes is _discover_mission_type_nodes.
func discoverMissionTypeNodes(mts []missionTypeData, nodes *nodeSet) error {
	in := func(line int) site { return site{{Name: "generate_graph", Line: 1817}}.at("_discover_mission_type_nodes", line) }
	seen := map[string]string{}
	for _, mt := range mts {
		if pyUnhashable(mt.rawID) {
			return in(1467).crash("TypeError", fmt.Sprintf("unhashable type: '%s'", pyTpName(mt.rawID)))
		}
		k := pyKey(mt.rawID)
		if prev, ok := seen[k]; ok {
			return in(1473).crash("ValueError", fmt.Sprintf("Duplicate mission_type id %s declared by both %s and %s in %s",
				pydantic.Repr(mt.rawID), filepath.Base(prev), filepath.Base(mt.path), filepath.Dir(mt.path)))
		}
		seen[k] = mt.path
		if err := nodes.ensurePy(in(1477), "mission_type:"+mt.id, "mission_type", orNone(dget(mt.data, "display_name", ""))); err != nil {
			return err
		}
	}
	return nil
}

// discoverStepContractNodes is _discover_mission_step_contract_nodes.
func discoverStepContractNodes(root string, nodes *nodeSet) error {
	cd := filepath.Join(regenMissionsRoot(root), "built_in_step_contracts")
	if !isDirPath(cd) {
		return nil
	}
	in := func(line int) site {
		return site{{Name: "generate_graph", Line: 1821}}.at("_discover_mission_step_contract_nodes", line)
	}
	seen := map[string]string{}
	for _, p := range globFiles(cd, ".step-contract.yaml") {
		data, err := regenLoadYAML(p)
		if err != nil {
			return err
		}
		if data == nil {
			continue
		}
		mission, action := dget(data, "mission", ""), dget(data, "action", "")
		if !pyTruthyAny(mission) || !pyTruthyAny(action) {
			continue
		}
		urn := "mission_step_contract:" + pyStrAny(mission) + "/" + pyStrAny(action)
		if prev, ok := seen[urn]; ok {
			return in(1523).crash("ValueError", fmt.Sprintf("Duplicate mission_step_contract %s declared by both %s and %s in %s",
				pydantic.Repr(urn), filepath.Base(prev), filepath.Base(p), filepath.Dir(p)))
		}
		seen[urn] = p
		if err := nodes.ensurePy(in(1525), urn, "mission_step_contract", action); err != nil {
			return err
		}
	}
	return nil
}

// pySortPairs is list.sort(key=lambda u: (a, b)) over 2-tuples of YAML
// scalars: the sorted permutation, or the TypeError text when two keys
// are not orderable (CPython's binary insertion sort compare order).
func pySortPairs(keys [][2]any) ([]int, string) {
	perm := make([]int, len(keys))
	for i := range perm {
		perm[i] = i
	}
	errMsg := ""
	lt := func(a, b [2]any) bool {
		if errMsg != "" {
			return false
		}
		for i := 0; i < 2; i++ {
			if pyEq(a[i], b[i]) {
				continue
			}
			r, msg := pyLess(a[i], b[i])
			if msg != "" {
				errMsg = msg
			}
			return r
		}
		return false
	}
	n := len(perm)
	if n < 2 {
		return perm, ""
	}
	// count_run: ascending (non-descending) prefix, or a strictly
	// descending one that is reversed in place.
	run := 1
	if lt(keys[perm[1]], keys[perm[0]]) {
		run = 2
		for run < n && lt(keys[perm[run]], keys[perm[run-1]]) {
			run++
		}
		for i, j := 0, run-1; i < j; i, j = i+1, j-1 {
			perm[i], perm[j] = perm[j], perm[i]
		}
	} else {
		run = 2
		for run < n && !lt(keys[perm[run]], keys[perm[run-1]]) {
			run++
		}
	}
	if errMsg != "" {
		return nil, errMsg
	}
	// binarysort the rest.
	for i := run; i < n; i++ {
		pivot := perm[i]
		l, r := 0, i
		for l < r {
			m := l + (r-l)/2
			if lt(keys[pivot], keys[perm[m]]) {
				r = m
			} else {
				l = m + 1
			}
		}
		if errMsg != "" {
			return nil, errMsg
		}
		copy(perm[l+1:i+1], perm[l:i])
		perm[l] = pivot
	}
	return perm, ""
}

func pyNum(v any) (float64, bool) {
	switch t := v.(type) {
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case float64:
		return t, true
	}
	return 0, false
}

func pyEq(a, b any) bool {
	if x, ok := pyNum(a); ok {
		if y, ok := pyNum(b); ok {
			return x == y
		}
		return false
	}
	return pyTpName(a) == pyTpName(b) && pyStrAny(a) == pyStrAny(b)
}

func pyLess(a, b any) (bool, string) {
	if x, ok := pyNum(a); ok {
		if y, ok := pyNum(b); ok {
			return x < y, ""
		}
	}
	sa, okA := a.(string)
	sb, okB := b.(string)
	if okA && okB {
		return sa < sb, ""
	}
	return false, fmt.Sprintf("'<' not supported between instances of '%s' and '%s'", pyTpName(a), pyTpName(b))
}
