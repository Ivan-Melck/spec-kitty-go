package charter

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

const createdArtifactsPath = ".kittify/charter/synthesized-artifacts.yaml"
const createdArtifactsHeader = "# Artifact ids spec-kitty synthesis has created, per kind. Synthesis adds an\n# artifact to an explicit activated_<kind>s list only the first time it creates\n# it, so an artifact you remove stays removed. Written by spec-kitty; do not edit.\n"

func manifestSlugs(root, kind string) map[string]bool {
	out := map[string]bool{}
	m, err := LoadSynthManifest(filepath.Join(root, ManifestPath))
	if err != nil {
		return out
	}
	for _, entry := range m.Artifacts {
		if entry.Kind == kind {
			out[entry.Slug] = true
		}
	}
	return out
}

// ReachOnlyIDs excludes synthesized artifacts from closure roots; their owning
// directive's suggests edge supplies them when that directive is in force.
func ReachOnlyIDs(root, kind string) map[string]bool {
	out := map[string]bool{}
	pack := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	if !dct.HasGraphFiles(pack) {
		return out
	}
	graph, err := loadGraphOrDir(pack)
	if err != nil {
		return out
	}
	registered := manifestSlugs(root, kind)
	nodes := map[string]bool{}
	for _, node := range graph.Nodes {
		k, id := dct.SplitURN(node.URN)
		if k == kind && node.Kind == kind && registered[id] {
			nodes[node.URN] = true
		}
	}
	required := OrgRequiredIDs(root, dct.Kind(kind).Plural())
	for _, edge := range graph.Edges {
		if synthesizedSourceEdge(edge) && nodes[edge.Target] {
			_, id := dct.SplitURN(edge.Target)
			if !contains(required, id) {
				out[id] = true
			}
		}
	}
	return out
}
func eligibleCreated(root, kind string, slugs []string) []string {
	if kind != "styleguide" {
		return slugs
	}
	reach := ReachOnlyIDs(root, kind)
	var out []string
	for _, slug := range slugs {
		if reach[slug] {
			out = append(out, slug)
		}
	}
	return out
}
func createdRecord(root string) (map[string]map[string]bool, *jsonx.Object, error) {
	recorded := map[string]map[string]bool{"tactic": {}, "styleguide": {}}
	foreign := &jsonx.Object{}
	for _, rel := range []string{createdArtifactsPath, ".kittify/charter/synthesized-tactics.yaml"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if !exists(p) {
			continue
		}
		data, err := readRenameMapping(p)
		if err != nil {
			return nil, nil, fmt.Errorf("%s", filepath.ToSlash(p))
		}
		for _, kind := range reachOnlyKinds {
			key := "created_" + dct.Kind(kind).Plural()
			raw, present := data.Get(key)
			if !present {
				continue
			}
			ids, ok := raw.([]any)
			if !ok {
				return nil, nil, fmt.Errorf("%s", filepath.ToSlash(p))
			}
			for _, value := range ids {
				id, ok := value.(string)
				if !ok {
					return nil, nil, fmt.Errorf("%s", filepath.ToSlash(p))
				}
				if rel == createdArtifactsPath || kind == "tactic" {
					recorded[kind][id] = true
				}
			}
		}
		if rel == createdArtifactsPath {
			for _, pair := range data.Pairs() {
				if pair.Key != "created_tactics" && pair.Key != "created_styleguides" {
					foreign.Set(pair.Key, pair.Value)
				}
			}
		}
	}
	return recorded, foreign, nil
}
func AppendSynthesizedActivations(root string, created map[string][]string) ([]string, error) {
	participating := map[string][]string{}
	for _, kind := range reachOnlyKinds {
		slugs := eligibleCreated(root, kind, dedupe(created[kind]))
		if len(slugs) > 0 {
			participating[kind] = slugs
		}
	}
	if len(participating) == 0 {
		return nil, nil
	}
	recorded, foreign, err := createdRecord(root)
	if err != nil {
		return []string{err.Error() + " is unreadable; no synthesized artifact was added to an activation list. Activate one with `spec-kitty charter activate <kind> <id>`."}, nil
	}
	first := map[string][]string{}
	updated := map[string]map[string]bool{}
	recordChanged := false
	for _, kind := range reachOnlyKinds {
		updated[kind] = map[string]bool{}
		for id := range recorded[kind] {
			updated[kind][id] = true
		}
		slugs, active := participating[kind]
		if !active {
			continue
		}
		manifest := manifestSlugs(root, kind)
		for _, slug := range slugs {
			if !recorded[kind][slug] {
				first[kind] = append(first[kind], slug)
			}
			updated[kind][slug] = true
		}
		for _, slug := range eligibleCreated(root, kind, dct.SortedKeys(manifest)) {
			updated[kind][slug] = true
		}
		recordChanged = recordChanged || len(updated[kind]) != len(recorded[kind])
	}
	var warnings []string
	wrote := false
	if len(first) > 0 {
		target, err := LoadWriteTarget(root)
		if err != nil {
			return nil, err
		}
		activation, err := dct.LoadActivation(root)
		if err != nil {
			return nil, err
		}
		for _, kind := range reachOnlyKinds {
			key := "activated_" + dct.Kind(kind).Plural()
			raw, present := target.Data.Get(key)
			if !present || raw == nil {
				continue
			}
			current, _, err := currentList(target.Data, key)
			if err != nil {
				return nil, err
			}
			additions := []string{}
			for _, slug := range first[kind] {
				if !contains(current, slug) {
					additions = append(additions, slug)
				}
			}
			if len(additions) == 0 {
				continue
			}
			for _, slug := range additions {
				if activation.Denied[dct.Kind(kind)][slug] {
					warnings = append(warnings, "kept deactivated: "+kind+" "+slug)
				} else {
					current = append(current, slug)
				}
			}
			target.Data.Set(key, strsAny(current))
			if err := target.Save(); err != nil {
				return nil, err
			}
			wrote = true
		}
	}
	if recordChanged {
		body := &jsonx.Object{}
		for _, kind := range reachOnlyKinds {
			ids := dct.SortedKeys(updated[kind])
			sort.Strings(ids)
			body.Set("created_"+dct.Kind(kind).Plural(), strsAny(ids))
		}
		for _, pair := range foreign.Pairs() {
			body.Set(pair.Key, pair.Value)
		}
		if err := atomicfile.Write(filepath.Join(root, createdArtifactsPath), []byte(createdArtifactsHeader+yamlx.Emit(body))); err != nil {
			return nil, err
		}
	}
	if wrote {
		if err := RestampBundleHash(root); err != nil {
			return nil, err
		}
	}
	return warnings, nil
}
func CreatedSlugsByKind(delta *ReconDelta) map[string][]string {
	out := map[string][]string{}
	if delta == nil {
		return out
	}
	for _, entry := range delta.manifestAdded {
		if contains(reachOnlyKinds, entry.kind) {
			out[entry.kind] = append(out[entry.kind], entry.slug)
		}
	}
	return out
}
