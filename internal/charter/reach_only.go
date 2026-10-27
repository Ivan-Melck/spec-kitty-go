package charter

import (
	"fmt"
	"os"
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

// SynthesisMembershipUnreadableError is reach_only.SynthesisMembershipUnreadableError
// (#6454), a KittyInternalConsistencyError: neither the synthesis manifest nor
// a created-artifact record says which artifacts synthesis created. Raised
// instead of an empty set, which would re-root every synthesized artifact and
// deliver it even while its directive is inactive (fail-open). Error() is
// str(exc), the code.
type SynthesisMembershipUnreadableError struct{ Body string }

func (e *SynthesisMembershipUnreadableError) Error() string { return e.Code() }

// Code is the coded error's code.
func (e *SynthesisMembershipUnreadableError) Code() string { return "SYNTHESIS_MEMBERSHIP_UNREADABLE" }

// synthesizedSlugs is reach_only._synthesized_slugs: the bare slugs of kind
// synthesis created. The manifest is the authority and an absent manifest
// means nothing was synthesized; a manifest that cannot be read (a dangling
// symlink or a failing stat included) falls back to the created-artifact
// record, which fails closed when it cannot stand in.
func synthesizedSlugs(root, kind string) (map[string]bool, error) {
	p := filepath.Join(root, ManifestPath)
	_, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err == nil {
		var m *SynthManifest
		if m, err = LoadSynthManifest(p); err == nil {
			out := map[string]bool{}
			for _, entry := range m.Artifacts {
				if entry.Kind == kind && entry.Kind != "directive" {
					out[entry.Slug] = true
				}
			}
			return out, nil
		}
	}
	return recordedSlugs(root, kind, err)
}

// recordedSlugs is reach_only._recorded_slugs: the created-artifact record's
// ids of kind standing in for an unreadable manifest (it never under-counts).
func recordedSlugs(root, kind string, manifestErr error) (map[string]bool, error) {
	state := "unreadable"
	if recorded, _, err := createdRecord(root); err == nil {
		if len(recorded[kind]) > 0 {
			warnLog("%s is unreadable (%v); using %s for the synthesized %ss.", ManifestPath, manifestErr, createdArtifactsPath, kind)
			return recorded[kind], nil
		}
		// An absent record, or one listing no id of this kind, says nothing
		// about what was synthesized.
		state = "absent or lists no " + kind
	}
	return nil, &SynthesisMembershipUnreadableError{Body: fmt.Sprintf(unreadableMembership, ManifestPath, createdArtifactsPath, state, kind, kind, ManifestPath, ManifestPath)}
}

const unreadableMembership = "%s cannot be read and %s is %s, so spec-kitty cannot tell which %ss synthesis " +
	"created. Refusing to deliver governance: every synthesized %s would otherwise reach agents even while its " +
	"directive is inactive.\nRemediation: restore %s from version control (for example " +
	"`git checkout -- %s`)."

// ReachOnlyIDs excludes synthesized artifacts from closure roots; their owning
// directive's suggests edge supplies them when that directive is in force.
// An absent or unloadable overlay or an absent manifest yields the empty set;
// an unreadable manifest falls back to the created-artifact record and fails
// closed (*SynthesisMembershipUnreadableError) when that cannot stand in.
func ReachOnlyIDs(root, kind string) (map[string]bool, error) {
	out := map[string]bool{}
	pack := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	if !dct.HasGraphFiles(pack) {
		return out, nil
	}
	graph, err := loadGraphOrDir(pack)
	if err != nil {
		return out, nil
	}
	nodes := map[string]bool{}
	for _, node := range graph.Nodes {
		if k, _ := dct.SplitURN(node.URN); k == kind && node.Kind == kind {
			nodes[node.URN] = true
		}
	}
	candidates := map[string]bool{}
	for _, edge := range graph.Edges {
		if synthesizedSourceEdge(edge) && nodes[edge.Target] {
			candidates[edge.Target] = true
		}
	}
	if len(candidates) == 0 {
		return out, nil // the manifest is read only when an edge could make something reach-only
	}
	registered, err := synthesizedSlugs(root, kind)
	if err != nil {
		return nil, err
	}
	var required []string
	for urn := range candidates {
		_, id := dct.SplitURN(urn)
		if !registered[id] {
			continue
		}
		if required == nil {
			required = OrgRequiredIDs(root, dct.Kind(kind).Plural())
		}
		if !contains(required, id) {
			out[id] = true
		}
	}
	return out, nil
}
func eligibleCreated(root, kind string, slugs []string) ([]string, error) {
	if kind != "styleguide" || len(slugs) == 0 {
		return slugs, nil
	}
	reach, err := ReachOnlyIDs(root, kind)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, slug := range slugs {
		if reach[slug] {
			out = append(out, slug)
		}
	}
	return out, nil
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
		slugs, err := eligibleCreated(root, kind, dedupe(created[kind]))
		if err != nil {
			return nil, err
		}
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
		earlier, err := eligibleCreated(root, kind, dct.SortedKeys(manifest))
		if err != nil {
			return nil, err
		}
		for _, slug := range earlier {
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
