package charter

import (
	"os"
	"path/filepath"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
)

// This file ports charter.activation._drg_helpers.load_validated_graph with
// the org layers runtime callers supply: org_roots=resolve_pack_chain(...,
// strict=False) and org_fragments=load_org_drg(..., strict=False).

// DRGProjectValidationError is _drg_helpers.DRGProjectValidationError: a
// merged-graph validation error introduced only by the project overlay.
type DRGProjectValidationError struct{ *dct.DRGValidationError }

// GraphOptions tunes LoadOrgValidatedGraph.
type GraphOptions struct {
	// ProjectDegrade raises *DRGProjectValidationError when the errors are
	// all introduced by the project overlay.
	ProjectDegrade bool
	// ExcludeProject is include_project=False.
	ExcludeProject bool
}

// LoadOrgDRGLenient is load_org_drg(repo_root, strict=False): a pack whose
// directory or drg/fragment.yaml is missing is skipped.
func LoadOrgDRGLenient(repoRoot string) ([]*OrgFragment, error) {
	var out []*OrgFragment
	for i, p := range LoadPackRegistry(repoRoot).Packs {
		root, err := p.EffectiveRoot(repoRoot)
		if err != nil {
			return nil, err
		}
		// strict=False skips a fragment-less pack before load_org_pack runs
		// (so its pack-level check warns nothing).
		if _, err := os.Stat(filepath.Join(root, "drg", "fragment.yaml")); err != nil {
			continue
		}
		f, err := LoadOrgPack(p.Name, root, i+1)
		if err != nil {
			if _, missing := err.(*OrgPackMissingError); missing {
				continue
			}
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// mergedToGraph converts a merge_three_layers result into a DRG graph.
func mergedToGraph(m *MergedGraph, meta *dct.Graph) *dct.Graph {
	out := &dct.Graph{SchemaVersion: meta.SchemaVersion, GeneratedAt: meta.GeneratedAt, GeneratedBy: meta.GeneratedBy}
	for _, n := range m.Nodes {
		dn := dct.Node{URN: n.URN, Kind: n.Kind}
		if n.Label != nil {
			dn.Label = *n.Label
		}
		out.Nodes = append(out.Nodes, dn)
	}
	for _, e := range m.Edges {
		out.Edges = append(out.Edges, dct.Edge{Source: e.Source, Target: e.Target, Relation: e.Relation, Reason: e.Reason, When: e.When})
	}
	return out
}

// collapseDuplicateEdgeTriples is _collapse_duplicate_edge_triples (first
// occurrence of each (source, target, relation) wins).
func collapseDuplicateEdgeTriples(g *dct.Graph) *dct.Graph {
	seen := map[[3]string]bool{}
	var edges []dct.Edge
	for _, e := range g.Edges {
		k := [3]string{e.Source, e.Target, e.Relation}
		if seen[k] {
			continue
		}
		seen[k] = true
		edges = append(edges, e)
	}
	c := *g
	c.Edges = edges
	return &c
}

// foldFinalLayers is _fold_final_layers.
func foldFinalLayers(rootMerged *dct.Graph, frags []*OrgFragment, project *dct.Graph) (*dct.Graph, error) {
	if len(frags) == 0 {
		return dct.MergeLayers(rootMerged, project), nil
	}
	var proj *MergedGraph
	if project != nil {
		proj = GraphFromDoctrine(project, dct.LayerProject)
	}
	m, err := MergeThreeLayers(GraphFromDoctrine(rootMerged, dct.LayerBuiltIn), frags, proj)
	if err != nil {
		return nil, err
	}
	return collapseDuplicateEdgeTriples(mergedToGraph(m, rootMerged)), nil
}

// LoadOrgValidatedGraph is load_validated_graph(repo_root,
// org_roots=resolve_pack_chain(strict=False),
// org_fragments=load_org_drg(strict=False), ...).
func LoadOrgValidatedGraph(repoRoot string, opt GraphOptions) (*dct.Graph, error) {
	roots := ResolvePackChain(repoRoot)
	frags, err := LoadOrgDRGLenient(repoRoot)
	if err != nil {
		return nil, err
	}
	rootMerged, err := dct.LoadGraph("", roots)
	if err != nil {
		return nil, err
	}
	for _, r := range roots {
		if dct.HasGraphFiles(r) {
			continue
		}
		fragExists := false
		if st, err := os.Stat(filepath.Join(r, "drg", "fragment.yaml")); err == nil && !st.IsDir() {
			fragExists = true
		}
		if !fragExists {
			warnLog("Org pack at %s ships no root-level DRG graph (graph.yaml / *.graph.yaml) and no drg/fragment.yaml; it contributes no dependency graph to cascade and was skipped. Author a root-level *.graph.yaml, or supply its drg/fragment.yaml through org_fragments, to contribute requires/suggests edges.", r)
		}
	}
	var project *dct.Graph
	if !opt.ExcludeProject {
		pd := filepath.Join(repoRoot, filepath.FromSlash(dct.ProjectPackRoot))
		if dct.HasGraphFiles(pd) {
			if project, err = loadGraphOrDir(pd); err != nil {
				return nil, err
			}
		}
	}
	merged, err := foldFinalLayers(rootMerged, frags, project)
	if err != nil {
		return nil, err
	}
	if err := dct.AssertValid(merged); err != nil {
		ve := err.(*dct.DRGValidationError)
		if opt.ProjectDegrade && project != nil {
			if base, berr := foldFinalLayers(rootMerged, frags, nil); berr == nil {
				baseErrs := map[string]bool{}
				for _, e := range dct.ValidateGraph(base) {
					baseErrs[e] = true
				}
				projectOnly := true
				for _, e := range ve.Errors {
					if baseErrs[e] {
						projectOnly = false
						break
					}
				}
				if projectOnly {
					return merged, &DRGProjectValidationError{ve}
				}
			}
		}
		return merged, ve
	}
	return merged, nil
}

// IsUnexpectedError reports whether err stands for a Python exception that is
// not a ValueError (DRG load/validation, org-merge refusals): CLI handlers
// render those through their `except Exception` ("Unexpected error") branch.
func IsUnexpectedError(err error) bool {
	switch err.(type) {
	case *dct.DRGValidationError, *DRGProjectValidationError, *OrgConflictError, *OrgPackSchemaError, *OrgPackParseError,
		*UnknownRelationError, *DuplicateURNError, *DRGLoadError, *OrgPackMissingError:
		return true
	}
	return false
}

// PyExceptionClass is type(exc).__name__ for the errors IsUnexpectedError
// recognises.
func PyExceptionClass(err error) string {
	switch err.(type) {
	case *dct.DRGValidationError:
		return "DRGValidationError"
	case *DRGProjectValidationError:
		return "DRGProjectValidationError"
	case *OrgConflictError:
		return "OrgDRGConflictError"
	case *OrgPackSchemaError:
		return "OrgPackSchemaError"
	case *OrgPackParseError:
		return "OrgPackParseError"
	case *UnknownRelationError:
		return "UnknownRelationError"
	case *DuplicateURNError:
		return "DuplicateURNError"
	case *OrgPackMissingError:
		return "OrgPackMissingError"
	}
	return "DRGLoadError"
}
