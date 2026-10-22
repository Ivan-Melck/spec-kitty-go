package doctrine

import (
	"fmt"
	"strings"
)

// This file ports charter.offering.drg.validator (validate_graph /
// assert_valid / DRGValidationError) and the validation pass of
// charter.activation._drg_helpers.load_validated_graph.

// DRGValidationError is drg.validator.DRGValidationError.
type DRGValidationError struct{ Errors []string }

func (e *DRGValidationError) Error() string {
	return fmt.Sprintf("%d validation error(s): %s", len(e.Errors), strings.Join(e.Errors, "; "))
}

// DanglingEndpoints is dangling_endpoints: each edge whose source and/or
// target is not a node URN.
func DanglingEndpoints(g *Graph) []Edge {
	urns := g.NodeURNs()
	var out []Edge
	for _, e := range g.Edges {
		if !urns[e.Source] || !urns[e.Target] {
			out = append(out, e)
		}
	}
	return out
}

// DuplicateEdgeTriples is duplicate_edge_triples: the second and later
// occurrence of each repeated (source, target, relation) triple.
func DuplicateEdgeTriples(g *Graph) []Edge {
	seen := map[string]bool{}
	var out []Edge
	for _, e := range g.Edges {
		k := e.Source + "\x00" + e.Target + "\x00" + e.Relation
		if seen[k] {
			out = append(out, e)
		}
		seen[k] = true
	}
	return out
}

// ValidateDangling is validate_dangling_references.
func ValidateDangling(g *Graph) []string {
	var errs []string
	urns := g.NodeURNs()
	for _, e := range DanglingEndpoints(g) {
		if !urns[e.Source] {
			errs = append(errs, fmt.Sprintf("Dangling source: edge (%s --%s--> %s) references non-existent node %s", e.Source, e.Relation, e.Target, PyRepr(e.Source)))
		}
		if !urns[e.Target] {
			errs = append(errs, fmt.Sprintf("Dangling target: edge (%s --%s--> %s) references non-existent node %s", e.Source, e.Relation, e.Target, PyRepr(e.Target)))
		}
	}
	return errs
}

// CycleErrors reports every cycle of the relation subgraph (DFS from the
// sorted node set, upstream _validate_requires_cycles order).
func CycleErrors(g *Graph, relation, prefix string) []string {
	adj := map[string][]string{}
	all := map[string]bool{}
	for _, e := range g.Edges {
		if e.Relation == relation {
			adj[e.Source] = append(adj[e.Source], e.Target)
			all[e.Source], all[e.Target] = true, true
		}
	}
	color := map[string]int{}
	var errs []string
	var path []string
	var dfs func(n string)
	dfs = func(n string) {
		color[n] = 1
		path = append(path, n)
		for _, nb := range adj[n] {
			switch color[nb] {
			case 1:
				start := 0
				for i, p := range path {
					if p == nb {
						start = i
						break
					}
				}
				cyc := append(append([]string{}, path[start:]...), nb)
				errs = append(errs, prefix+strings.Join(cyc, " -> "))
			case 0:
				dfs(nb)
			}
		}
		path = path[:len(path)-1]
		color[n] = 2
	}
	for _, n := range sortedKeys(all) {
		if color[n] == 0 {
			dfs(n)
		}
	}
	return errs
}

// ValidateGraph is validate_graph (empty = valid).
func ValidateGraph(g *Graph) []string {
	errs := ValidateDangling(g)
	for _, e := range DuplicateEdgeTriples(g) {
		errs = append(errs, fmt.Sprintf("Duplicate edge: (%s --%s--> %s)", e.Source, e.Relation, e.Target))
	}
	errs = append(errs, CycleErrors(g, RelRequires, "Cycle in requires: ")...)
	kind := map[string]string{}
	for _, n := range g.Nodes {
		kind[n.URN] = n.Kind
	}
	for _, e := range g.Edges {
		if e.Relation != RelSpecializesFrom && e.Relation != RelDelegatesTo {
			continue
		}
		for _, ep := range [][2]string{{"source", e.Source}, {"target", e.Target}} {
			k, ok := kind[ep[1]]
			if ok && k != "agent_profile" {
				errs = append(errs, fmt.Sprintf("Profile-edge %s must be an agent_profile: edge (%s --%s--> %s) has %s %s of kind %s", ep[0], e.Source, e.Relation, e.Target, ep[0], PyRepr(ep[1]), PyRepr(k)))
			}
		}
	}
	errs = append(errs, CycleErrors(g, RelSpecializesFrom, "Cycle in specializes_from lineage: ")...)
	for _, e := range g.Edges {
		if e.Relation != RelRejects {
			continue
		}
		if k, ok := kind[e.Target]; ok && k != "anti_pattern" {
			errs = append(errs, fmt.Sprintf("Rejects-edge target must be an anti_pattern node: edge (%s --%s--> %s) has target %s of kind %s", e.Source, e.Relation, e.Target, PyRepr(e.Target), PyRepr(k)))
		}
	}
	for _, n := range g.Nodes {
		if n.Kind == "anti_pattern" && len(g.EdgesTo(n.URN, RelRejects)) == 0 {
			errs = append(errs, fmt.Sprintf("Orphaned anti_pattern node: %s is marked anti_pattern but has no inbound rejects edge", PyRepr(n.URN)))
		}
	}
	return errs
}

// AssertValid is assert_valid.
func AssertValid(g *Graph) error {
	if errs := ValidateGraph(g); len(errs) > 0 {
		return &DRGValidationError{Errors: errs}
	}
	return nil
}

// LoadValidatedGraph is load_validated_graph(repo_root, org_roots=...)
// without org fragments: LoadGraph plus the assert_valid pass. The graph is
// returned even when validation fails (callers that degrade use it).
func LoadValidatedGraph(repoRoot string, orgRoots []string) (*Graph, error) {
	g, err := LoadGraph(repoRoot, orgRoots)
	if err != nil {
		return nil, err
	}
	if err := AssertValid(g); err != nil {
		return g, err
	}
	return g, nil
}

// DRGErrorClass is type(exc).__name__ for a DRG load/validation failure.
func DRGErrorClass(err error) string {
	if _, ok := err.(*DRGValidationError); ok {
		return "DRGValidationError"
	}
	return "DRGLoadError"
}
