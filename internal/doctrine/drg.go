package doctrine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// DRG relations.
const (
	RelRequires          = "requires"
	RelSuggests          = "suggests"
	RelApplies           = "applies"
	RelScope             = "scope"
	RelVocabulary        = "vocabulary"
	RelInstantiates      = "instantiates"
	RelReplaces          = "replaces"
	RelDelegatesTo       = "delegates_to"
	RelSpecializesFrom   = "specializes_from"
	RelEnhances          = "enhances"
	RelOverrides         = "overrides"
	RelRefines           = "refines"
	RelInTensionWith     = "in_tension_with"
	RelReconcilesTension = "reconciles_tension"
	RelRejects           = "rejects"
)

// Node is a DRG node.
type Node struct {
	URN   string
	Kind  string
	Label string // "" when absent
	Tags  []string
}

// ID returns the id part of the URN.
func (n Node) ID() string { _, id := SplitURN(n.URN); return id }

// Edge is a typed directed DRG edge.
type Edge struct {
	Source, Target, Relation string
	When, Reason             *string
}

// Graph is a Doctrine Reference Graph.
type Graph struct {
	SchemaVersion, GeneratedAt, GeneratedBy string
	Nodes                                   []Node
	Edges                                   []Edge
}

// NodeURNs returns the set of node URNs.
func (g *Graph) NodeURNs() map[string]bool {
	out := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		out[n.URN] = true
	}
	return out
}

// Node looks up a node by URN.
func (g *Graph) Node(urn string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].URN == urn {
			return &g.Nodes[i]
		}
	}
	return nil
}

// EdgesFrom returns outgoing edges (relation "" = any).
func (g *Graph) EdgesFrom(urn, relation string) []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.Source == urn && (relation == "" || e.Relation == relation) {
			out = append(out, e)
		}
	}
	return out
}

// EdgesTo returns incoming edges (relation "" = any).
func (g *Graph) EdgesTo(urn, relation string) []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.Target == urn && (relation == "" || e.Relation == relation) {
			out = append(out, e)
		}
	}
	return out
}

// Clone deep-copies the graph.
func (g *Graph) Clone() *Graph {
	c := *g
	c.Nodes = append([]Node(nil), g.Nodes...)
	c.Edges = append([]Edge(nil), g.Edges...)
	return &c
}

// ParseGraph decodes a graph YAML document.
func ParseGraph(src []byte, source string) (*Graph, error) {
	o, err := yamlx.ParseObject(src)
	if err != nil {
		return nil, fmt.Errorf("YAML parse error in %s: %v", source, err)
	}
	g := &Graph{SchemaVersion: pyStr0(o, "schema_version"), GeneratedAt: pyStr0(o, "generated_at"), GeneratedBy: pyStr0(o, "generated_by")}
	for _, raw := range o.GetList("nodes") {
		n, ok := raw.(*jsonx.Object)
		if !ok {
			return nil, fmt.Errorf("Validation error in %s: node is not a mapping", source)
		}
		node := Node{URN: n.GetString("urn"), Kind: n.GetString("kind"), Label: n.GetString("label")}
		for _, t := range n.GetList("tags") {
			node.Tags = append(node.Tags, pyStr(t))
		}
		g.Nodes = append(g.Nodes, node)
	}
	for _, raw := range o.GetList("edges") {
		e, ok := raw.(*jsonx.Object)
		if !ok {
			return nil, fmt.Errorf("Validation error in %s: edge is not a mapping", source)
		}
		edge := Edge{Source: e.GetString("source"), Target: e.GetString("target"), Relation: e.GetString("relation")}
		if v, ok := e.Get("when"); ok && v != nil {
			s := pyStr(v)
			edge.When = &s
		}
		if v, ok := e.Get("reason"); ok && v != nil {
			s := pyStr(v)
			edge.Reason = &s
		}
		g.Edges = append(g.Edges, edge)
	}
	return g, nil
}

func pyStr0(o *jsonx.Object, k string) string {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return ""
	}
	return pyStr(v)
}

// MergeLayers is upstream merge_layers: project nodes add or override the
// label of existing nodes; edges are concatenated.
func MergeLayers(base, overlay *Graph) *Graph {
	if overlay == nil {
		return base
	}
	idx := map[string]int{}
	nodes := append([]Node(nil), base.Nodes...)
	for i, n := range nodes {
		idx[n.URN] = i
	}
	for _, pn := range overlay.Nodes {
		if i, ok := idx[pn.URN]; ok {
			if pn.Label != "" {
				nodes[i].Label = pn.Label
			}
			continue
		}
		idx[pn.URN] = len(nodes)
		nodes = append(nodes, pn)
	}
	edges := append(append([]Edge(nil), base.Edges...), overlay.Edges...)
	return &Graph{SchemaVersion: base.SchemaVersion, GeneratedAt: base.GeneratedAt, GeneratedBy: base.GeneratedBy, Nodes: nodes, Edges: edges}
}

// loadGraphFS loads graph.yaml or the merged *.graph.yaml fragments at the
// root of p (alphabetical, merged left to right).
func loadGraphFS(p *PackFS) (*Graph, error) {
	if p.Exists("graph.yaml") {
		b, err := p.ReadFile("graph.yaml")
		if err != nil {
			return nil, err
		}
		return ParseGraph(b, p.Path("graph.yaml"))
	}
	frags := p.Walk(".", ".graph.yaml", false)
	if len(frags) == 0 {
		return nil, fmt.Errorf("No DRG graph files found in directory: %s", p.Disk)
	}
	var g *Graph
	for _, f := range frags {
		b, err := p.ReadFile(f)
		if err != nil {
			return nil, err
		}
		fg, err := ParseGraph(b, p.Path(f))
		if err != nil {
			return nil, err
		}
		if g == nil {
			g = fg
		} else {
			g = MergeLayers(g, fg)
		}
	}
	return g, nil
}

// HasGraphFiles reports whether dir holds graph.yaml or *.graph.yaml.
func HasGraphFiles(dir string) bool {
	if st, err := os.Stat(filepath.Join(dir, "graph.yaml")); err == nil && !st.IsDir() {
		return true
	}
	m, _ := filepath.Glob(filepath.Join(dir, "*.graph.yaml"))
	return len(m) > 0
}

var (
	builtInGraphOnce sync.Once
	builtInGraph     *Graph
	builtInGraphErr  error
)

// BuiltInGraph returns (a copy of) the shipped built-in DRG.
func BuiltInGraph() (*Graph, error) {
	builtInGraphOnce.Do(func() { builtInGraph, builtInGraphErr = loadGraphFS(BuiltIn()) })
	if builtInGraphErr != nil {
		return nil, builtInGraphErr
	}
	return builtInGraph.Clone(), nil
}

// LoadGraph returns the merged built-in + org roots + project DRG for
// repoRoot (upstream load_validated_graph without org fragments; the
// validation pass is not reproduced).
func LoadGraph(repoRoot string, orgRoots []string) (*Graph, error) {
	g, err := BuiltInGraph()
	if err != nil {
		return nil, err
	}
	for _, r := range orgRoots {
		if HasGraphFiles(r) {
			og, err := loadGraphFS(DiskPack(r))
			if err != nil {
				return nil, err
			}
			g = MergeLayers(g, og)
		}
	}
	if repoRoot != "" {
		pd := filepath.Join(repoRoot, filepath.FromSlash(ProjectPackRoot))
		if HasGraphFiles(pd) {
			pg, err := loadGraphFS(DiskPack(pd))
			if err != nil {
				return nil, err
			}
			g = MergeLayers(g, pg)
		}
	}
	return g, nil
}

// activatableKinds maps singular kinds subject to the activation gate.
func activatableSingular(kind string) (Kind, bool) {
	for _, k := range AllKinds {
		if string(k) == kind && k.Activatable() {
			return k, true
		}
	}
	return "", false
}

// StemIndex maps config stems (file names without the kind suffix) and
// declared ids to canonical ids for one kind.
func StemIndex(off *Offering, k Kind) map[string]string {
	out := map[string]string{}
	for _, a := range off.Repo(k).List() {
		base := filepath.Base(a.Path)
		stem := strings.TrimSuffix(base, k.Suffix())
		out[stem] = a.ID
		if _, ok := out[a.ID]; !ok && k == Directive {
			out[a.ID] = a.ID
		}
	}
	return out
}

// ResolveActivatedURNs resolves a three-state activation set of config
// stems to canonical URNs (nil stays nil; unknown stems are skipped).
func ResolveActivatedURNs(off *Offering, k Kind, ids IDSet) map[string]bool {
	if ids == nil {
		return nil
	}
	out := map[string]bool{}
	if k == AntiPattern {
		for id := range ids {
			out["anti_pattern:"+id] = true
		}
		return out
	}
	idx := StemIndex(off, k)
	for stem := range ids {
		if id, ok := idx[stem]; ok {
			out[string(k)+":"+id] = true
		}
	}
	return out
}

// FilterByActivation keeps only activated nodes (and edges whose endpoints
// both survive), upstream filter_graph_by_activation.
func FilterByActivation(g *Graph, act *Activation, off *Offering) *Graph {
	if act == nil {
		return g
	}
	resolved := map[string]map[string]bool{}
	for _, k := range AllKinds {
		if k.Activatable() {
			resolved[string(k)] = ResolveActivatedURNs(off, k, act.Activated(k))
		}
	}
	mtSet := map[string]bool{}
	for _, m := range act.MissionTypes {
		mtSet[m] = true
	}
	keep := func(n Node) bool {
		kind, id := SplitURN(n.URN)
		if kind == string(MissionStepContract) {
			if head, _, ok := strings.Cut(id, "/"); ok && id != "" {
				return mtSet[head]
			}
		}
		k, ok := activatableSingular(kind)
		if !ok {
			return true
		}
		if !act.ActivatedKinds[k.Plural()] {
			return false
		}
		if r := resolved[kind]; r != nil && id != "" && !r[kind+":"+id] {
			return false
		}
		return true
	}
	out := &Graph{SchemaVersion: g.SchemaVersion, GeneratedAt: g.GeneratedAt, GeneratedBy: g.GeneratedBy}
	alive := map[string]bool{}
	for _, n := range g.Nodes {
		if keep(n) {
			out.Nodes = append(out.Nodes, n)
			alive[n.URN] = true
		}
	}
	for _, e := range g.Edges {
		if alive[e.Source] && alive[e.Target] {
			out.Edges = append(out.Edges, e)
		}
	}
	return out
}

// WalkEdges is a BFS following only the given relations (maxDepth < 0 =
// unbounded). The result includes the start URNs.
func WalkEdges(g *Graph, start []string, relations map[string]bool, maxDepth int) map[string]bool {
	visited := map[string]bool{}
	if len(start) == 0 {
		return visited
	}
	adj := map[string][]string{}
	for _, e := range g.Edges {
		if relations[e.Relation] {
			adj[e.Source] = append(adj[e.Source], e.Target)
		}
	}
	type item struct {
		urn   string
		depth int
	}
	var q []item
	for _, u := range start {
		if !visited[u] {
			visited[u] = true
			q = append(q, item{u, 0})
		}
	}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		if maxDepth >= 0 && cur.depth >= maxDepth {
			continue
		}
		for _, nb := range adj[cur.urn] {
			if !visited[nb] {
				visited[nb] = true
				q = append(q, item{nb, cur.depth + 1})
			}
		}
	}
	return visited
}

// ResolvedContext is the result of ResolveContext.
type ResolvedContext struct {
	ArtifactURNs   map[string]bool
	GlossaryScopes map[string]bool
	// TensionArbiters: arbiter urn -> sorted arbitrated urns (sorted by arbiter).
	TensionArbiters      [][2]any
	UnarbitratedTensions [][2]string
}

// ResolveContext walks scope (1 hop), requires (transitive) and suggests
// (depth hops) from an action node.
func ResolveContext(g *Graph, actionURN string, depth int) ResolvedContext {
	scoped := WalkEdges(g, []string{actionURN}, map[string]bool{RelScope: true}, 1)
	delete(scoped, actionURN)
	scopedList := sortedKeys(scoped)
	required := WalkEdges(g, scopedList, map[string]bool{RelRequires: true}, -1)
	suggested := WalkEdges(g, scopedList, map[string]bool{RelSuggests: true}, depth)
	all := map[string]bool{}
	for _, m := range []map[string]bool{scoped, required, suggested} {
		for k := range m {
			all[k] = true
		}
	}
	vocab := WalkEdges(g, sortedKeys(all), map[string]bool{RelVocabulary: true}, 1)
	gl := map[string]bool{}
	for k := range vocab {
		if !all[k] {
			gl[k] = true
		}
	}
	rc := ResolvedContext{ArtifactURNs: all, GlossaryScopes: gl}
	// tension annotations
	type pair struct{ a, b string }
	pairs := map[pair]bool{}
	for _, e := range g.Edges {
		if e.Relation == RelInTensionWith && all[e.Source] && all[e.Target] {
			pairs[pair{e.Source, e.Target}] = true
		}
	}
	if len(pairs) > 0 {
		recon := map[string]map[string]bool{}
		for _, e := range g.Edges {
			if e.Relation == RelReconcilesTension {
				if recon[e.Target] == nil {
					recon[e.Target] = map[string]bool{}
				}
				recon[e.Target][e.Source] = true
			}
		}
		arb := map[string]map[string]bool{}
		for p := range pairs {
			common := false
			for r := range recon[p.a] {
				if recon[p.b][r] {
					common = true
					if arb[r] == nil {
						arb[r] = map[string]bool{}
					}
					arb[r][p.a], arb[r][p.b] = true, true
				}
			}
			if !common {
				rc.UnarbitratedTensions = append(rc.UnarbitratedTensions, [2]string{p.a, p.b})
			}
		}
		sort.Slice(rc.UnarbitratedTensions, func(i, j int) bool {
			a, b := rc.UnarbitratedTensions[i], rc.UnarbitratedTensions[j]
			if a[0] != b[0] {
				return a[0] < b[0]
			}
			return a[1] < b[1]
		})
		for _, r := range sortedKeys(arbToBool(arb)) {
			rc.TensionArbiters = append(rc.TensionArbiters, [2]any{r, sortedKeys(arb[r])})
		}
	}
	return rc
}

func arbToBool(m map[string]map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SortedKeys returns the sorted keys of a set.
func SortedKeys(m map[string]bool) []string { return sortedKeys(m) }
