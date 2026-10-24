package charter

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports the fresh-project parts of `charter synthesize`
// (specify_cli.cli.commands.charter._synthesis / _fresh_project_layer)
// and the request/topic helpers shared with `charter resynthesize`.

// FreshProvenance is _MINIMAL_FRESH_PROJECT_LAYER_PROVENANCE_TEMPLATE.
const FreshProvenance = "# Spec Kitty Doctrine — Fresh Project Seed\n\nThis `.kittify/charter-packs/` tree was materialized by `spec-kitty charter\nsynthesize` running against a **fresh project** (no LLM-authored YAML under\n`.kittify/charter/generated/`). It exists so `ActiveCharterService` discovers a\nproject layer and the runtime can advance; it is intentionally empty.\n\nThe runtime falls back to the packaged built-in doctrine\n(`packs/built-in/`) for all artifact lookups until the LLM harness writes\nproject-local artifacts under `.kittify/charter/generated/` and you re-run\n`spec-kitty charter synthesize`.\n\nReferences\n----------\n- GitHub issue: https://github.com/spec-kitty/spec-kitty/issues/839\n- Spec assumption A2: public CLI synthesize works on a fresh project.\n- Project-root resolution: `src/charter/activation/_project_root_candidates.py`.\n"

// SynthesisArtifactPaths is _SYNTHESIS_ARTIFACT_PATHS.
var SynthesisArtifactPaths = []string{".kittify/charter/synthesis-manifest.yaml", ".kittify/charter/provenance/", dct.ProjectPackRoot + "/"}

// canonicalYAML is synthesize_pipeline.canonical_yaml (sorted keys).
func canonicalYAML(o *jsonx.Object) string {
	return yamlx.EmitWith(sortKeysDeep(o), yamlx.Options{Width: 80})
}

func sortKeysDeep(v any) any {
	switch t := v.(type) {
	case *jsonx.Object:
		keys := make([]string, 0, t.Len())
		for _, kv := range t.Pairs() {
			keys = append(keys, kv.Key)
		}
		sort.Strings(keys)
		o := &jsonx.Object{}
		for _, k := range keys {
			x, _ := t.Get(k)
			o.Set(k, sortKeysDeep(x))
		}
		return o
	case []any:
		l := make([]any, len(t))
		for i, x := range t {
			l[i] = sortKeysDeep(x)
		}
		return l
	}
	return v
}

// FreshSeedManifestText is _fresh_seed_manifest_text().
func FreshSeedManifestText(version string) string {
	m := jsonx.NewObject("schema_version", "2", "mission_id", nil, "created_at", "1970-01-01T00:00:00+00:00", "run_id", "fresh-project-seed",
		"adapter_id", "fresh-seed", "adapter_version", version, "synthesizer_version", version, "artifacts", []any{}, "built_in_only", true,
		"bundle_content_hash", nil)
	sum := sha256.Sum256([]byte(canonicalYAML(m)))
	m.Set("manifest_hash", hex.EncodeToString(sum[:]))
	return canonicalYAML(m)
}

// HasGeneratedArtifacts is _has_generated_artifacts.
func HasGeneratedArtifacts(root string) bool {
	g := Path(root, ".kittify/charter/generated")
	for _, sub := range []string{"directives", "styleguides", "tactics"} {
		if countGlob(filepath.Join(g, sub), ".yaml") > 0 {
			return true
		}
	}
	return false
}

// CatalogIsEstablished is _catalog_is_established.
func CatalogIsEstablished(root string) bool {
	if o := readYAMLObject(Path(root, CharterYAML)); o != nil {
		for _, k := range []string{"activated_kinds", "activated_directives", "activated_tactics", "activated_styleguides", "activated_toolguides",
			"activated_paradigms", "activated_procedures", "activated_agent_profiles", "activated_mission_step_contracts"} {
			if l, ok := mustGetAny(o, k).([]any); ok && len(l) > 0 {
				return true
			}
		}
	}
	mp := Path(root, ManifestPath)
	prior := false
	if exists(mp) {
		o := readYAMLObject(mp)
		if o == nil {
			prior = true
		} else if v, ok := o.Get("built_in_only"); ok && v == false {
			prior = true
		}
	}
	return prior && ReadInterview(InterviewPath(root)) != nil
}

// PlannedFreshPaths / PlannedFreshDeletes are _planned_fresh_project_layer_*.
func PlannedFreshPaths() []string {
	return []string{dct.ProjectPackRoot + "/PROVENANCE.md", ".kittify/charter/synthesis-manifest.yaml"}
}

func PlannedFreshDeletes(root string) []string {
	if exists(filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot), "graph.yaml")) {
		return []string{dct.ProjectPackRoot + "/graph.yaml"}
	}
	return []string{}
}

// MaterializeFreshProjectLayer is _materialize_fresh_project_layer.
func MaterializeFreshProjectLayer(root, version string) ([]string, error) {
	pd := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	cd := Path(root, Dir)
	if err := os.MkdirAll(pd, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cd, 0o755); err != nil {
		return nil, err
	}
	pp := filepath.Join(pd, "PROVENANCE.md")
	if b, err := os.ReadFile(pp); err != nil || string(b) != FreshProvenance {
		if err := os.WriteFile(pp, []byte(FreshProvenance), 0o644); err != nil {
			return nil, err
		}
	}
	mp := filepath.Join(cd, "synthesis-manifest.yaml")
	text := FreshSeedManifestText(version)
	if b, err := os.ReadFile(mp); err != nil || string(b) != text {
		if err := os.WriteFile(mp, []byte(text), 0o644); err != nil {
			return nil, err
		}
	}
	os.Remove(filepath.Join(pd, "graph.yaml"))
	return PlannedFreshPaths(), nil
}

// ResynthesisTopics is _list_resynthesis_topics.
func ResynthesisTopics(root string, req *SynthesisRequest) *jsonx.Object {
	var artifacts []string
	pd := Path(root, ".kittify/charter/provenance")
	if ents, err := os.ReadDir(pd); err == nil {
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			o := readYAMLObject(filepath.Join(pd, e.Name()))
			if o == nil {
				continue
			}
			kind, slug, urn := o.GetString("artifact_kind"), o.GetString("artifact_slug"), o.GetString("artifact_urn")
			if kind == "" || urn == "" {
				continue
			}
			_, id, _ := strings.Cut(urn, ":")
			if kind == "directive" {
				artifacts = append(artifacts, kind+":"+id)
			} else {
				artifacts = append(artifacts, kind+":"+slug)
			}
		}
	}
	urns := append([]string{}, req.DRGNodes...)
	gp := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	if b, err := os.ReadFile(filepath.Join(gp, "graph.yaml")); err == nil {
		if g, err := dct.ParseGraph(b, "graph.yaml"); err == nil {
			for _, n := range g.Nodes {
				urns = append(urns, n.URN)
			}
		}
	}
	uniqSorted := func(l []string) []any {
		m := map[string]bool{}
		for _, x := range l {
			m[x] = true
		}
		return strs(dct.SortedKeys(m))
	}
	var aliases []string
	for _, s := range req.InterviewSections {
		aliases = append(aliases, strings.ReplaceAll(s, "_", "-"))
	}
	sort.Strings(aliases)
	return jsonx.NewObject("project_artifacts", uniqSorted(artifacts), "drg_urns", uniqSorted(urns),
		"interview_sections", strs(req.InterviewSections), "interview_section_aliases", strs(aliases))
}
