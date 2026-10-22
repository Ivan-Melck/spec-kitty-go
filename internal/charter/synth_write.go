package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports the persistence half of the charter synthesizer:
// project_drg (overlay emit/persist/post-condition), validation_gate,
// staging, write_pipeline (stage/validate/promote), manifest, reconcile,
// the neutrality gate and the orchestrator / resynthesize entry points.

const synthManifestRel = ".kittify/charter/synthesis-manifest.yaml"

var synthKindSubdirs = []string{"directive", "tactic", "styleguide"}

// ------------------------------------------------------------------ graphs

// snapshotGraph is orchestrator._built_in_drg_from_snapshot.
func snapshotGraph(req *SynthesisRequest) *dct.Graph {
	g := &dct.Graph{SchemaVersion: "1.0", GeneratedAt: "1970-01-01T00:00:00+00:00", GeneratedBy: "request.drg_snapshot"}
	for _, u := range req.DRGNodes {
		g.Nodes = append(g.Nodes, dct.Node{URN: u, Kind: "directive"})
	}
	return g
}

// orgChainGraph is _drg_helpers.org_chain_graph (nil without org packs).
func orgChainGraph(root string) *dct.Graph {
	roots := ResolvePackChain(root)
	frags, _ := LoadOrgDRG(root)
	if len(roots) == 0 && len(frags) == 0 {
		return nil
	}
	g, err := dct.LoadGraph("", roots)
	if err != nil {
		return nil
	}
	if len(frags) == 0 {
		return g
	}
	m, err := MergeThreeLayers(GraphFromDoctrine(g, "built_in"), frags, nil)
	if err != nil {
		return g
	}
	out := &dct.Graph{SchemaVersion: g.SchemaVersion, GeneratedAt: g.GeneratedAt, GeneratedBy: g.GeneratedBy}
	for _, n := range m.Nodes {
		dn := dct.Node{URN: n.URN, Kind: n.Kind}
		if n.Label != nil {
			dn.Label = *n.Label
		}
		out.Nodes = append(out.Nodes, dn)
	}
	for _, e := range m.Edges {
		out.Edges = append(out.Edges, dct.Edge{Source: e.Source, Target: e.Target, Relation: e.Relation, Reason: e.Reason})
	}
	return out
}

// graphDocument is migration.extractor.graph_document_to_dict.
func graphDocument(g *dct.Graph) *jsonx.Object {
	nodes, edges := []any{}, []any{}
	for _, n := range g.Nodes {
		o := jsonx.NewObject("urn", strings.TrimSpace(n.URN), "kind", n.Kind)
		if n.Label != "" {
			o.Set("label", strings.TrimSpace(n.Label))
		}
		if len(n.Tags) > 0 {
			var t []any
			for _, x := range n.Tags {
				t = append(t, strings.TrimSpace(x))
			}
			o.Set("tags", t)
		}
		nodes = append(nodes, o)
	}
	for _, e := range g.Edges {
		o := jsonx.NewObject("source", strings.TrimSpace(e.Source), "target", strings.TrimSpace(e.Target), "relation", e.Relation)
		if e.When != nil {
			o.Set("when", strings.TrimSpace(*e.When))
		}
		if e.Reason != nil {
			o.Set("reason", strings.TrimSpace(*e.Reason))
		}
		edges = append(edges, o)
	}
	return jsonx.NewObject("schema_version", g.SchemaVersion, "generated_at", g.GeneratedAt, "generated_by", g.GeneratedBy, "nodes", nodes, "edges", edges)
}

// serializeGraph is project_drg._serialize_graph (field order, ruamel rt).
func serializeGraph(g *dct.Graph) string {
	return yamlx.EmitWith(graphDocument(g), yamlx.Options{Width: 80})
}

// loadGraphFile is loader.load_graph (pydantic-validated).
func synLoadGraphFile(p string) (*dct.Graph, error) {
	if _, err := loadGraphDocument(p); err != nil {
		if gl, ok := err.(*graphLoadError); ok {
			return nil, &DRGLoadError{gl.Error()}
		}
		return nil, &DRGLoadError{err.Error()}
	}
	b, _ := os.ReadFile(p)
	g, err := dct.ParseGraph(b, p)
	if err != nil {
		return nil, &DRGLoadError{err.Error()}
	}
	return g, nil
}

// loadGraphOrDir is loader.load_graph_or_dir.
func loadGraphOrDir(p string) (*dct.Graph, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, &DRGLoadError{"Path not found: " + p}
	}
	if !st.IsDir() {
		return synLoadGraphFile(p)
	}
	if fi, err := os.Stat(filepath.Join(p, "graph.yaml")); err == nil && !fi.IsDir() {
		return synLoadGraphFile(filepath.Join(p, "graph.yaml"))
	}
	frags, _ := filepath.Glob(filepath.Join(p, "*.graph.yaml"))
	sort.Strings(frags)
	if len(frags) == 0 {
		return nil, &DRGLoadError{"No DRG graph files found in directory: " + p}
	}
	g, err := synLoadGraphFile(frags[0])
	if err != nil {
		return nil, err
	}
	for _, f := range frags[1:] {
		fg, err := synLoadGraphFile(f)
		if err != nil {
			return nil, err
		}
		g = dct.MergeLayers(g, fg)
	}
	return g, nil
}

func edgeLabel(e dct.Edge) string { return e.Source + "--" + e.Relation + "-->" + e.Target }

func danglingEndpoints(g *dct.Graph) []dct.Edge {
	urns := g.NodeURNs()
	var out []dct.Edge
	for _, e := range g.Edges {
		if !urns[e.Source] || !urns[e.Target] {
			out = append(out, e)
		}
	}
	return out
}

func duplicateEdgeTriples(g *dct.Graph) []dct.Edge {
	seen := map[string]bool{}
	var out []dct.Edge
	for _, e := range g.Edges {
		k := e.Source + "\x00" + e.Target + "\x00" + e.Relation
		if seen[k] {
			out = append(out, e)
		}
		seen[k] = true
	}
	return out
}

func cycleErrors(g *dct.Graph, relation, prefix string) []string {
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
	var dfs func(n string, path []string) []string
	dfs = func(n string, path []string) []string {
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
				path = dfs(nb, path)
			}
		}
		path = path[:len(path)-1]
		color[n] = 2
		return path
	}
	for _, n := range dct.SortedKeys(all) {
		if color[n] == 0 {
			dfs(n, nil)
		}
	}
	return errs
}

// validateGraph is drg.validator.validate_graph.
func validateGraph(g *dct.Graph) []string {
	var errs []string
	urns := g.NodeURNs()
	for _, e := range danglingEndpoints(g) {
		if !urns[e.Source] {
			errs = append(errs, fmt.Sprintf("Dangling source: edge (%s --%s--> %s) references non-existent node %s", e.Source, e.Relation, e.Target, dct.PyRepr(e.Source)))
		}
		if !urns[e.Target] {
			errs = append(errs, fmt.Sprintf("Dangling target: edge (%s --%s--> %s) references non-existent node %s", e.Source, e.Relation, e.Target, dct.PyRepr(e.Target)))
		}
	}
	for _, e := range duplicateEdgeTriples(g) {
		errs = append(errs, fmt.Sprintf("Duplicate edge: (%s --%s--> %s)", e.Source, e.Relation, e.Target))
	}
	errs = append(errs, cycleErrors(g, dct.RelRequires, "Cycle in requires: ")...)
	kind := map[string]string{}
	for _, n := range g.Nodes {
		kind[n.URN] = n.Kind
	}
	for _, e := range g.Edges {
		if e.Relation != dct.RelSpecializesFrom && e.Relation != dct.RelDelegatesTo {
			continue
		}
		for _, ep := range [][2]string{{"source", e.Source}, {"target", e.Target}} {
			k, ok := kind[ep[1]]
			if ok && k != "agent_profile" {
				errs = append(errs, fmt.Sprintf("Profile-edge %s must be an agent_profile: edge (%s --%s--> %s) has %s %s of kind %s", ep[0], e.Source, e.Relation, e.Target, ep[0], dct.PyRepr(ep[1]), dct.PyRepr(k)))
			}
		}
	}
	errs = append(errs, cycleErrors(g, dct.RelSpecializesFrom, "Cycle in specializes_from lineage: ")...)
	for _, e := range g.Edges {
		if e.Relation != dct.RelRejects {
			continue
		}
		if k, ok := kind[e.Target]; ok && k != "anti_pattern" {
			errs = append(errs, fmt.Sprintf("Rejects-edge target must be an anti_pattern node: edge (%s --%s--> %s) has target %s of kind %s", e.Source, e.Relation, e.Target, dct.PyRepr(e.Target), dct.PyRepr(k)))
		}
	}
	for _, n := range g.Nodes {
		if n.Kind == "anti_pattern" && len(g.EdgesTo(n.URN, dct.RelRejects)) == 0 {
			errs = append(errs, fmt.Sprintf("Orphaned anti_pattern node: %s is marked anti_pattern but has no inbound rejects edge", dct.PyRepr(n.URN)))
		}
	}
	return errs
}

// walkProjectProfileNodes is project_scan.walk_project_agent_profile_nodes.
func walkProjectProfileNodes(root string) ([]dct.Node, error) {
	dir := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot), "agent_profiles")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, nil
	}
	var paths []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".agent.yaml") {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	seen := map[string]string{}
	var nodes []dct.Node
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("Project agent_profile file %s could not be parsed: %v", p, err)
		}
		v, err := yamlx.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("Project agent_profile file %s could not be parsed: %v", p, err)
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			return nil, fmt.Errorf("Project agent_profile file %s does not contain a YAML mapping", p)
		}
		id := ""
		for _, k := range []string{"profile-id", "profile_id"} {
			if s, ok := mustGetAny(o, k).(string); ok && s != "" {
				id = s
				break
			}
		}
		if id == "" {
			return nil, fmt.Errorf("Project agent_profile file %s is missing a non-empty 'profile-id' field", p)
		}
		if !IsValidURN("agent_profile:" + id) {
			return nil, fmt.Errorf("Project agent_profile file %s has a URN-unsafe 'profile-id' %s: it must form a valid 'agent_profile:<id>' URN (lowercase/underscore-prefixed, no spaces or non-ASCII).", p, dct.PyRepr(id))
		}
		if prev, dup := seen[id]; dup {
			return nil, fmt.Errorf("Duplicate project agent_profile 'profile-id' %s: declared by both %s and %s. Each authored profile must carry a distinct profile-id so no overlay node is silently dropped.", dct.PyRepr(id), prev, p)
		}
		seen[id] = p
		n := dct.Node{URN: "agent_profile:" + id, Kind: "agent_profile"}
		if s, ok := mustGetAny(o, "name").(string); ok {
			n.Label = s
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// emitProjectLayer is project_drg.emit_project_layer (answer-driven nodes,
// hand-authored project agent profiles).
func emitProjectLayer(targets []SynthTarget, version string, base *dct.Graph, root string) (*dct.Graph, []string, error) {
	baseURNs := base.NodeURNs()
	baseTriples := map[string]bool{}
	for _, e := range base.Edges {
		baseTriples[e.Source+"\x00"+e.Target+"\x00"+e.Relation] = true
	}
	g := &dct.Graph{SchemaVersion: "1.0", GeneratedAt: nowUTCSeconds(), GeneratedBy: "spec-kitty charter synthesize " + version}
	seen := map[string]bool{}
	for _, t := range targets {
		if t.Kind != "directive" && t.Kind != "tactic" && t.Kind != "styleguide" && t.Kind != "agent_profile" {
			continue
		}
		urn := t.URN()
		if baseURNs[urn] {
			return nil, nil, projectDRGError([]string{fmt.Sprintf("Additive-only violation (FR-020 / EC-6): URN '%s' already exists in the built-in DRG layer.  Synthesized artifacts must carry new URNs disjoint from built-in nodes.", urn)},
				fmt.Sprintf("built_in_nodes=%d, colliding_urn=%s", len(base.Nodes), dct.PyRepr(urn)))
		}
		if seen[urn] {
			return nil, nil, projectDRGError([]string{fmt.Sprintf("Duplicate project-layer URN '%s': each target must produce a distinct URN within one synthesis run.", urn)},
				"colliding_urn="+dct.PyRepr(urn))
		}
		seen[urn] = true
		g.Nodes = append(g.Nodes, dct.Node{URN: urn, Kind: t.Kind, Label: t.Title})
		for _, src := range t.SourceURNs {
			rel := dct.RelApplies
			if t.Kind == "directive" {
				rel = dct.RelRequires
			}
			if baseTriples[urn+"\x00"+src+"\x00"+rel] {
				return nil, nil, projectDRGError([]string{fmt.Sprintf("Duplicate edge (FR-020 / EC-6): triple (%s --%s--> %s) already exists in the built-in DRG layer.", dct.PyRepr(urn), rel, dct.PyRepr(src))},
					fmt.Sprintf("colliding_edge=(%s --%s--> %s)", urn, rel, src))
			}
			reason := "Derived from synthesis target " + dct.PyRepr(t.Slug)
			g.Edges = append(g.Edges, dct.Edge{Source: urn, Target: src, Relation: rel, Reason: &reason})
		}
	}
	if root != "" {
		profiles, err := walkProjectProfileNodes(root)
		if err != nil {
			return nil, nil, err
		}
		for _, n := range profiles {
			if baseURNs[n.URN] {
				return nil, nil, projectDRGError([]string{fmt.Sprintf("Additive-only violation (INV-1): project agent_profile URN '%s' already exists in the built-in DRG layer.  A hand-authored project profile must carry a new URN disjoint from built-in nodes.", n.URN)},
					fmt.Sprintf("built_in_nodes=%d, colliding_urn=%s", len(base.Nodes), dct.PyRepr(n.URN)))
			}
			if seen[n.URN] {
				continue
			}
			seen[n.URN] = true
			g.Nodes = append(g.Nodes, n)
		}
	}
	return g, nil, nil
}

// ------------------------------------------------------------------ staging

type stagingDir struct{ root, runID string }

func createStaging(repoRoot, runID string) (*stagingDir, error) {
	s := &stagingDir{root: filepath.Join(repoRoot, ".kittify", "charter", ".staging", runID), runID: runID}
	for _, k := range synthKindSubdirs {
		if err := os.MkdirAll(filepath.Join(s.root, "charter-packs", k), 0o755); err != nil {
			return nil, err
		}
	}
	return s, os.MkdirAll(filepath.Join(s.root, "charter", "provenance"), 0o755)
}

func (s *stagingDir) contentPath(kind, filename string) string {
	return filepath.Join(s.root, "charter-packs", kind, filename)
}

func (s *stagingDir) provenancePath(kind, slug string) string {
	return filepath.Join(s.root, "charter", "provenance", kind+"-"+slug+".yaml")
}

// commitToFailed is StagingDir.commit_to_failed.
func (s *stagingDir) commitToFailed(cause string) {
	failed := filepath.Join(filepath.Dir(s.root), s.runID+".failed")
	dir := failed
	if err := os.Rename(s.root, failed); err != nil {
		dir = ""
		if exists(s.root) {
			dir = s.root
		}
	}
	if dir == "" || !exists(dir) {
		return
	}
	doc := jsonx.NewObject("reason", cause, "traceback", "Traceback unavailable (Go build).\n"+cause+"\n", "timestamp", nowUTCISO())
	os.WriteFile(filepath.Join(dir, "cause.yaml"), []byte(yamlx.EmitWith(doc, yamlx.Options{Width: 80})), 0o644)
}

func (s *stagingDir) wipe() { os.RemoveAll(s.root) }

// withStaging is the StagingDir context manager: an error routes the
// directory to <run_id>.failed/.
func withStaging(repoRoot, runID string, body func(s *stagingDir) error) error {
	s, err := createStaging(repoRoot, runID)
	if err != nil {
		return err
	}
	if err := body(s); err != nil {
		s.commitToFailed(synthErrClass(err) + ": " + err.Error())
		return err
	}
	return nil
}

func synthErrClass(err error) string {
	switch t := err.(type) {
	case *SynthError:
		return t.Type
	case *DRGLoadError:
		return "DRGLoadError"
	}
	return "ValueError"
}

// ------------------------------------------------------------------ write

func stripVolatile(text string, keys []string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var kept []string
	for _, l := range lines {
		vol := false
		if l == "" || !strings.ContainsAny(l[:1], " \t-") {
			for _, k := range keys {
				if strings.HasPrefix(l, k+":") {
					vol = true
				}
			}
		}
		if !vol {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

func substantivelyEqual(candidate string, existingPath string, keys []string) bool {
	b, err := os.ReadFile(existingPath)
	if err != nil {
		return false
	}
	return stripVolatile(string(b), keys) == stripVolatile(candidate, keys)
}

var (
	volatileProv     = []string{"produced_at", "synthesizer_version", "synthesis_run_id", "generated_at"}
	volatileManifest = []string{"created_at", "run_id", "adapter_version", "synthesizer_version", "manifest_hash"}
	volatileGraph    = []string{"generated_at", "generated_by"}
)

// StagedArtifact is write_pipeline.StagedArtifact.
type StagedArtifact struct{ Path, Kind, Slug, ArtifactID string }

// Object renders the WrittenArtifact dict.
func (a StagedArtifact) Object() *jsonx.Object {
	var id any
	if a.ArtifactID != "" {
		id = a.ArtifactID
	}
	return jsonx.NewObject("path", a.Path, "kind", a.Kind, "slug", a.Slug, "artifact_id", id)
}

func liveContentRel(kind, slug, id string) string {
	return dct.ProjectPackRoot + "/" + kind + "/" + ArtifactFilename(kind, slug, id)
}

// ComputeWrittenArtifacts is write_pipeline.compute_written_artifacts.
func ComputeWrittenArtifacts(results []SynthResult) ([]StagedArtifact, error) {
	var out []StagedArtifact
	for _, r := range results {
		id, err := r.Prov.ArtifactID()
		if err != nil {
			return nil, err
		}
		out = append(out, StagedArtifact{liveContentRel(r.Prov.Kind, r.Prov.Slug, id), r.Prov.Kind, r.Prov.Slug, id})
	}
	return out, nil
}

func stageArtifacts(s *stagingDir, results []SynthResult) ([]string, error) {
	var sel []string
	for _, r := range results {
		id, err := r.Prov.ArtifactID()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(s.contentPath(r.Prov.Kind, ArtifactFilename(r.Prov.Kind, r.Prov.Slug, id)), []byte(canonicalYAML(r.Body)), 0o644); err != nil {
			return nil, err
		}
		if err := os.WriteFile(s.provenancePath(r.Prov.Kind, r.Prov.Slug), []byte(canonicalYAML(r.Prov.Object())), 0o644); err != nil {
			return nil, err
		}
		sel = append(sel, r.Prov.Kind+":"+r.Prov.Slug)
	}
	return sel, nil
}

// bannedTerms is neutrality/banned_terms.yaml.
type bannedTerm struct {
	id, kind, pattern string
	re                *regexp.Regexp
}

func loadBannedTerms() []bannedTerm {
	b, err := assets.ReadFile("charter/activation/neutrality/banned_terms.yaml")
	if err != nil {
		return nil
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return nil
	}
	var out []bannedTerm
	for _, x := range o.GetList("terms") {
		e, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		t := bannedTerm{id: e.GetString("id"), kind: e.GetString("kind"), pattern: e.GetString("pattern")}
		cs := true
		if v, ok := e.Get("case_sensitive"); ok {
			cs, _ = v.(bool)
		}
		flags := "(?m)"
		if !cs {
			flags = "(?mi)"
		}
		if t.kind == "regex" {
			t.re = regexp.MustCompile(flags + t.pattern)
		} else if !cs {
			t.re = regexp.MustCompile(flags + regexp.QuoteMeta(t.pattern))
		}
		out = append(out, t)
	}
	return out
}

// neutralityHits is run_neutrality_lint over one staged file.
func neutralityHits(p string) []string {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	terms := loadBannedTerms()
	var hits []string
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		for _, t := range terms {
			if t.re != nil {
				for _, m := range t.re.FindAllString(line, -1) {
					hits = append(hits, m)
				}
				continue
			}
			for idx := 0; ; {
				pos := strings.Index(line[idx:], t.pattern)
				if pos < 0 || t.pattern == "" {
					break
				}
				hits = append(hits, t.pattern)
				idx += pos + len(t.pattern)
			}
		}
	}
	return hits
}

func runNeutralityGate(s *stagingDir, results []SynthResult, ev *SynthEvidence) error {
	for _, r := range results {
		if ev != nil && ev.Code != nil && ev.Code.PrimaryLanguage != "unknown" && strings.Contains(r.Prov.Slug, ev.Code.PrimaryLanguage) {
			continue
		}
		id, err := r.Prov.ArtifactID()
		if err != nil {
			return err
		}
		p := s.contentPath(r.Prov.Kind, ArtifactFilename(r.Prov.Kind, r.Prov.Slug, id))
		if !exists(p) {
			continue
		}
		if hits := neutralityHits(p); len(hits) > 0 {
			if len(hits) > 5 {
				hits = hits[:5]
			}
			q := make([]string, len(hits))
			for i, h := range hits {
				q[i] = "'" + h + "'"
			}
			return &SynthError{Type: "NeutralityGateViolation", StagingDir: s.root,
				Msg: fmt.Sprintf("Neutrality gate blocked promotion of %s.\nLanguage-specific terms detected in a generic-scoped artifact: %s.\nInspect the staged artifact at: %s", r.Prov.URN, strings.Join(q, ", "), s.root)}
		}
	}
	return nil
}

// ------------------------------------------------------------------ manifest

// ManifestEntry is ManifestArtifactEntry.
type ManifestEntry struct{ Kind, Slug, Path, ProvenancePath, ContentHash string }

// SynthManifest is synthesizer.manifest.SynthesisManifest.
type SynthManifest struct {
	SchemaVersion                               string
	MissionID                                   any
	CreatedAt, RunID, AdapterID, AdapterVersion string
	SynthesizerVersion, ManifestHash            string
	Artifacts                                   []ManifestEntry
	BuiltInOnly                                 bool
	BundleContentHash                           *string
	raw                                         *jsonx.Object
}

// Object is model_dump(mode="python").
func (m *SynthManifest) Object(withHash bool) *jsonx.Object {
	arts := []any{}
	for _, a := range m.Artifacts {
		arts = append(arts, jsonx.NewObject("kind", a.Kind, "slug", a.Slug, "path", a.Path, "provenance_path", a.ProvenancePath, "content_hash", a.ContentHash))
	}
	o := jsonx.NewObject("schema_version", m.SchemaVersion, "mission_id", m.MissionID, "created_at", m.CreatedAt, "run_id", m.RunID,
		"adapter_id", m.AdapterID, "adapter_version", m.AdapterVersion, "synthesizer_version", m.SynthesizerVersion)
	if withHash {
		o.Set("manifest_hash", m.ManifestHash)
	}
	o.Set("artifacts", arts)
	o.Set("built_in_only", m.BuiltInOnly)
	o.Set("bundle_content_hash", strPtrAny(m.BundleContentHash))
	return o
}

// Finalize is finalize_manifest.
func (m *SynthManifest) Finalize() *SynthManifest {
	c := *m
	c.Artifacts = append([]ManifestEntry(nil), m.Artifacts...)
	c.ManifestHash = sha256Hex(canonicalYAML(c.Object(false)))
	return &c
}

// LoadSynthManifest is manifest.load_yaml.
func LoadSynthManifest(p string) (*SynthManifest, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return parseSynthManifest(b)
}

func parseSynthManifest(b []byte) (*SynthManifest, error) {
	o, err := yamlx.ParseObject(b)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, fmt.Errorf("1 validation error for SynthesisManifest\n  Input should be a valid dictionary or instance of SynthesisManifest [type=model_type, input_value=None, input_type=NoneType]")
	}
	var missing []string
	for _, k := range []string{"created_at", "run_id", "adapter_id", "adapter_version", "synthesizer_version", "manifest_hash"} {
		if v, ok := o.Get(k); !ok || v == nil {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		lines := []string{fmt.Sprintf("%d validation error%s for SynthesisManifest", len(missing), plural1(len(missing)))}
		for _, k := range missing {
			lines = append(lines, k, fmt.Sprintf("  Field required [type=missing, input_value=%s, input_type=dict]", truncRepr(pyValueRepr(o))),
				"    For further information visit https://errors.pydantic.dev/2.14/v/missing")
		}
		return nil, fmt.Errorf("%s", strings.Join(lines, "\n"))
	}
	m := &SynthManifest{SchemaVersion: "3", raw: o, CreatedAt: pyStrOf(mustGetAny(o, "created_at")), RunID: pyStrOf(mustGetAny(o, "run_id")),
		AdapterID: pyStrOf(mustGetAny(o, "adapter_id")), AdapterVersion: pyStrOf(mustGetAny(o, "adapter_version")),
		SynthesizerVersion: pyStrOf(mustGetAny(o, "synthesizer_version")), ManifestHash: pyStrOf(mustGetAny(o, "manifest_hash"))}
	if v, ok := o.Get("schema_version"); ok && v != nil {
		m.SchemaVersion = pyStrOf(v)
	}
	m.MissionID, _ = o.Get("mission_id")
	if v, ok := o.Get("built_in_only"); ok {
		m.BuiltInOnly, _ = v.(bool)
	}
	if v, ok := o.Get("bundle_content_hash"); ok && v != nil {
		s := pyStrOf(v)
		m.BundleContentHash = &s
	}
	for _, x := range o.GetList("artifacts") {
		e, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		m.Artifacts = append(m.Artifacts, ManifestEntry{e.GetString("kind"), e.GetString("slug"), e.GetString("path"), e.GetString("provenance_path"), e.GetString("content_hash")})
	}
	return m, nil
}

func sortManifestEntries(es []ManifestEntry) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].Kind != es[j].Kind {
			return es[i].Kind < es[j].Kind
		}
		return es[i].Slug < es[j].Slug
	})
}

func bundleHashPtr(root string) *string {
	if h := BundleContentHash(root); h != "" {
		return &h
	}
	return nil
}

// rewriteManifest is reconcile.rewrite_manifest.
func rewriteManifest(existing *SynthManifest, results []SynthResult, runID, root, version string) *SynthManifest {
	newBy := map[string]ManifestEntry{}
	var newKeys []string
	ids, vers := map[string]bool{}, map[string]bool{}
	var lastID, lastVer string
	for _, r := range results {
		id := ""
		if r.Prov.Kind == "directive" {
			_, id, _ = strings.Cut(r.Prov.URN, ":")
		}
		k := r.Prov.Kind + "\x00" + r.Prov.Slug
		if _, ok := newBy[k]; !ok {
			newKeys = append(newKeys, k)
		}
		newBy[k] = ManifestEntry{r.Prov.Kind, r.Prov.Slug, liveContentRel(r.Prov.Kind, r.Prov.Slug, id),
			".kittify/charter/provenance/" + r.Prov.Kind + "-" + r.Prov.Slug + ".yaml", sha256Hex(canonicalYAML(r.Body))}
		ids[r.Prov.AdapterID], vers[r.Prov.AdapterVersion] = true, true
		lastID, lastVer = r.Prov.AdapterID, r.Prov.AdapterVersion
	}
	var merged []ManifestEntry
	have := map[string]bool{}
	for _, e := range existing.Artifacts {
		k := e.Kind + "\x00" + e.Slug
		have[k] = true
		if n, ok := newBy[k]; ok {
			merged = append(merged, n)
		} else {
			merged = append(merged, e)
		}
	}
	for _, k := range newKeys {
		if !have[k] {
			merged = append(merged, newBy[k])
		}
	}
	aid, aver := existing.AdapterID, existing.AdapterVersion
	if len(ids) == 1 {
		aid = lastID
		if len(vers) == 1 {
			aver = lastVer
		}
	}
	sortManifestEntries(merged)
	m := &SynthManifest{SchemaVersion: "3", MissionID: existing.MissionID, CreatedAt: nowUTCISO(), RunID: runID, AdapterID: aid, AdapterVersion: aver,
		SynthesizerVersion: version, Artifacts: merged, BundleContentHash: bundleHashPtr(root)}
	return m.Finalize()
}

// ------------------------------------------------------------------ reconcile

// NodeOrEdgeRef is reconcile.NodeOrEdgeRef.
type NodeOrEdgeRef struct {
	RefKind, URN    string
	BackingArtifact string // "" = orphaned
}

// Object renders _ref_to_dict.
func (r NodeOrEdgeRef) Object() *jsonx.Object {
	var b any
	if r.BackingArtifact != "" {
		b = r.BackingArtifact
	}
	return jsonx.NewObject("ref_kind", r.RefKind, "urn", r.URN, "backing_artifact", b)
}

// ReconConflict is reconcile.ReconciliationConflict.
type ReconConflict struct {
	Kind, TargetID, BackingArtifact, Remediation, Provenance string
}

// Object renders _conflict_to_dict.
func (c ReconConflict) Object() *jsonx.Object {
	var b any
	if c.BackingArtifact != "" {
		b = c.BackingArtifact
	}
	return jsonx.NewObject("kind", c.Kind, "target_id", c.TargetID, "backing_artifact", b, "remediation", c.Remediation, "provenance", c.Provenance)
}

var reconcileRemediations = map[string]string{
	"duplicate_triple":            "Remediation (duplicate triple): a preserved edge repeats a (source, target, relation) triple already present in the merged graph. Remove the duplicate from its backing doctrine artifact, or drop the stale copy with `spec-kitty charter synthesize --prune`.",
	"preserved_dangling_endpoint": "Remediation (dangling endpoint): a preserved edge references an endpoint the current run no longer emits. Restore the backing artifact that defines the endpoint, or remove the edge with `spec-kitty charter synthesize --prune`.",
}

type manifestRef struct{ kind, slug string }

// ReconDelta is reconcile.ReconciliationDelta.
type ReconDelta struct {
	Retained, Added, Removable []NodeOrEdgeRef
	manifestRemovable          []manifestRef
	Conflicts                  []ReconConflict
}

type reconOutcome struct {
	overlay  *dct.Graph
	manifest *SynthManifest
	delta    *ReconDelta
}

// mergeProjectOverlay is reconcile.merge_project_overlay.
func mergeProjectOverlay(existing, updated *dct.Graph) *dct.Graph {
	updURNs := updated.NodeURNs()
	byURN := map[string]dct.Node{}
	for _, n := range updated.Nodes {
		byURN[n.URN] = n
	}
	used := map[string]bool{}
	var nodes []dct.Node
	for _, n := range existing.Nodes {
		if r, ok := byURN[n.URN]; ok && !used[n.URN] {
			nodes = append(nodes, r)
			used[n.URN] = true
		} else {
			nodes = append(nodes, n)
		}
	}
	for _, n := range updated.Nodes {
		if !used[n.URN] {
			nodes = append(nodes, n)
			used[n.URN] = true
		}
	}
	bySrc := map[string][]dct.Edge{}
	var srcOrder []string
	for _, e := range updated.Edges {
		if _, ok := bySrc[e.Source]; !ok {
			srcOrder = append(srcOrder, e.Source)
		}
		bySrc[e.Source] = append(bySrc[e.Source], e)
	}
	inserted := map[string]bool{}
	var edges []dct.Edge
	for _, e := range existing.Edges {
		if !updURNs[e.Source] {
			edges = append(edges, e)
			continue
		}
		if inserted[e.Source] {
			continue
		}
		edges = append(edges, bySrc[e.Source]...)
		inserted[e.Source] = true
	}
	for _, s := range srcOrder {
		if !inserted[s] {
			edges = append(edges, bySrc[s]...)
		}
	}
	return &dct.Graph{SchemaVersion: updated.SchemaVersion, GeneratedAt: updated.GeneratedAt, GeneratedBy: updated.GeneratedBy, Nodes: nodes, Edges: edges}
}

func provenanceURN(root, rel string) string {
	o := readYAMLObject(filepath.Join(root, filepath.FromSlash(rel)))
	if o == nil {
		return ""
	}
	return o.GetString("artifact_urn")
}

func reconcileSynthesis(root string, fresh *dct.Graph, results []SynthResult, runID, version string, base *dct.Graph) (*reconOutcome, error) {
	pack := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	var existing *dct.Graph
	if exists(pack) && dct.HasGraphFiles(pack) {
		g, err := loadGraphOrDir(pack)
		if err != nil {
			return nil, err
		}
		existing = g
	}
	merged := fresh
	if existing != nil {
		merged = mergeProjectOverlay(existing, fresh)
	}
	mp := Path(root, synthManifestRel)
	var em *SynthManifest
	if exists(mp) {
		m, err := LoadSynthManifest(mp)
		if err != nil {
			return nil, err
		}
		em = m
	} else {
		em = &SynthManifest{SchemaVersion: "3", CreatedAt: nowUTCISO(), RunID: runID, SynthesizerVersion: version}
	}
	mm := rewriteManifest(em, results, runID, root, version)
	urnToPath := map[string]string{}
	for _, e := range mm.Artifacts {
		if u := provenanceURN(root, e.ProvenancePath); u != "" && exists(filepath.Join(root, filepath.FromSlash(e.Path))) {
			urnToPath[u] = e.Path
		}
	}
	nref := func(n dct.Node) NodeOrEdgeRef { return NodeOrEdgeRef{"node", n.URN, urnToPath[n.URN]} }
	eref := func(e dct.Edge) NodeOrEdgeRef { return NodeOrEdgeRef{"edge", edgeLabel(e), urnToPath[e.Source]} }
	d := &ReconDelta{}
	freshURNs := fresh.NodeURNs()
	if existing == nil {
		for _, n := range fresh.Nodes {
			d.Added = append(d.Added, nref(n))
		}
		for _, e := range fresh.Edges {
			d.Added = append(d.Added, eref(e))
		}
	} else {
		exURNs := existing.NodeURNs()
		for _, n := range existing.Nodes {
			if !freshURNs[n.URN] {
				d.Retained = append(d.Retained, nref(n))
			}
		}
		for _, e := range existing.Edges {
			if !freshURNs[e.Source] {
				d.Retained = append(d.Retained, eref(e))
			}
		}
		for _, n := range fresh.Nodes {
			if !exURNs[n.URN] {
				d.Added = append(d.Added, nref(n))
			}
		}
		for _, e := range fresh.Edges {
			if !exURNs[e.Source] {
				d.Added = append(d.Added, eref(e))
			}
		}
		d.Removable = d.Retained
	}
	newKeys := map[string]bool{}
	for _, r := range results {
		newKeys[r.Prov.Kind+"\x00"+r.Prov.Slug] = true
	}
	var pres []string
	for _, e := range em.Artifacts {
		if k := e.Kind + "\x00" + e.Slug; !newKeys[k] {
			pres = append(pres, k)
		}
	}
	sort.Strings(pres)
	inMerged := map[string]bool{}
	for _, e := range mm.Artifacts {
		inMerged[e.Kind+"\x00"+e.Slug] = true
	}
	seenPres := map[string]bool{}
	for _, k := range pres {
		if inMerged[k] && !seenPres[k] {
			seenPres[k] = true
			kind, slug, _ := strings.Cut(k, "\x00")
			d.manifestRemovable = append(d.manifestRemovable, manifestRef{kind, slug})
		}
	}
	full := dct.MergeLayers(base, merged)
	conflict := func(kind string, e dct.Edge) ReconConflict {
		prov := "preserved"
		if freshURNs[e.Source] {
			prov = "new_emit"
		}
		return ReconConflict{kind, edgeLabel(e), urnToPath[e.Source], reconcileRemediations[kind], prov}
	}
	for _, e := range duplicateEdgeTriples(full) {
		d.Conflicts = append(d.Conflicts, conflict("duplicate_triple", e))
	}
	for _, e := range danglingEndpoints(full) {
		d.Conflicts = append(d.Conflicts, conflict("preserved_dangling_endpoint", e))
	}
	return &reconOutcome{merged, mm, d}, nil
}

// applyPrune is reconcile.apply_prune.
func applyPrune(o *reconOutcome) *reconOutcome {
	rm := map[string]bool{}
	for _, r := range o.delta.Removable {
		if r.RefKind == "node" {
			rm[r.URN] = true
		}
	}
	g := &dct.Graph{SchemaVersion: o.overlay.SchemaVersion, GeneratedAt: o.overlay.GeneratedAt, GeneratedBy: o.overlay.GeneratedBy}
	for _, n := range o.overlay.Nodes {
		if !rm[n.URN] {
			g.Nodes = append(g.Nodes, n)
		}
	}
	for _, e := range o.overlay.Edges {
		if !rm[e.Source] {
			g.Edges = append(g.Edges, e)
		}
	}
	rmk := map[string]bool{}
	for _, r := range o.delta.manifestRemovable {
		rmk[r.kind+"\x00"+r.slug] = true
	}
	m := *o.manifest
	m.Artifacts = nil
	for _, a := range o.manifest.Artifacts {
		if !rmk[a.Kind+"\x00"+a.Slug] {
			m.Artifacts = append(m.Artifacts, a)
		}
	}
	return &reconOutcome{g, m.Finalize(), o.delta}
}

// ------------------------------------------------------------------ gate

func graphExcludingPreserved(g *dct.Graph, conflicts []ReconConflict) *dct.Graph {
	dang, dup := map[string]bool{}, map[string]bool{}
	for _, c := range conflicts {
		if c.Provenance != "preserved" {
			continue
		}
		if c.Kind == "preserved_dangling_endpoint" {
			dang[c.TargetID] = true
		} else if c.Kind == "duplicate_triple" {
			dup[c.TargetID] = true
		}
	}
	if len(dang) == 0 && len(dup) == 0 {
		return g
	}
	c := g.Clone()
	c.Edges = nil
	seen := map[string]bool{}
	for _, e := range g.Edges {
		k := edgeLabel(e)
		if dang[k] {
			continue
		}
		if dup[k] {
			if seen[k] {
				continue
			}
			seen[k] = true
		}
		c.Edges = append(c.Edges, e)
	}
	return c
}

// validationGate is validation_gate.validate over the staged overlay.
func validationGate(stagingRoot string, builtIn, org *dct.Graph, conflicts []ReconConflict) error {
	pack := filepath.Join(stagingRoot, "charter-packs")
	overlay, err := loadGraphOrDir(pack)
	if err != nil {
		return projectDRGError([]string{fmt.Sprintf("Could not load staged project overlay from %s: %s", pack, err.Error())},
			fmt.Sprintf("staging_dir=%s, built_in_nodes=%d", stagingRoot, len(builtIn.Nodes)))
	}
	base := builtIn
	if org != nil {
		base = org
	}
	merged := dct.MergeLayers(base, overlay)
	errs := validateGraph(graphExcludingPreserved(merged, conflicts))
	if len(errs) == 0 {
		return nil
	}
	urns := dct.SortedKeys(overlay.NodeURNs())
	return projectDRGError(errs, fmt.Sprintf("built_in_nodes=%d, project_nodes=%d, merged_nodes=%d, merged_edges=%d, project_urns=[%s]",
		len(builtIn.Nodes), len(overlay.Nodes), len(merged.Nodes), len(merged.Edges), strings.Join(urns, ", ")))
}

func persistGraph(g *dct.Graph, stagingRoot string) error {
	dir := filepath.Join(stagingRoot, "charter-packs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "graph.yaml"), []byte(serializeGraph(g)), 0o644)
}

// ------------------------------------------------------------------ promote

func promote(root string, s *stagingDir, req *SynthesisRequest, results []SynthResult, validate func(*stagingDir) error, manifest *SynthManifest) (*SynthManifest, error) {
	if _, err := stageArtifacts(s, results); err != nil {
		return nil, err
	}
	if err := validate(s); err != nil {
		s.commitToFailed("Validation failed: " + err.Error())
		return nil, err
	}
	if err := runNeutralityGate(s, results, req.Evidence); err != nil {
		return nil, err
	}
	for _, k := range []string{"directive", "styleguide", "tactic"} {
		os.MkdirAll(filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot), k), 0o755)
	}
	os.MkdirAll(Path(root, ".kittify/charter/provenance"), 0o755)
	promoteErr := func(cause string) error {
		return &SynthError{Type: "StagingPromoteError", Msg: fmt.Sprintf("Synthesis promote failed for run %s (staging: %s): %s. Staging directory preserved as %s.failed/ for diagnosis.", s.runID, s.root, cause, s.root)}
	}
	for _, r := range results {
		id, _ := r.Prov.ArtifactID()
		fn := ArtifactFilename(r.Prov.Kind, r.Prov.Slug, id)
		live := Path(root, liveContentRel(r.Prov.Kind, r.Prov.Slug, id))
		if !substantivelyEqual(canonicalYAML(r.Body), live, nil) {
			if err := os.Rename(s.contentPath(r.Prov.Kind, fn), live); err != nil {
				return nil, promoteErr(err.Error())
			}
		}
		sp := s.provenancePath(r.Prov.Kind, r.Prov.Slug)
		lp := Path(root, ".kittify/charter/provenance/"+r.Prov.Kind+"-"+r.Prov.Slug+".yaml")
		b, _ := os.ReadFile(sp)
		if !substantivelyEqual(string(b), lp, volatileProv) {
			if err := os.Rename(sp, lp); err != nil {
				return nil, promoteErr(err.Error())
			}
		}
	}
	sg := filepath.Join(s.root, "charter-packs", "graph.yaml")
	if b, err := os.ReadFile(sg); err == nil {
		lg := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot), "graph.yaml")
		if !substantivelyEqual(string(b), lg, volatileGraph) {
			if err := os.Rename(sg, lg); err != nil {
				return nil, promoteErr(err.Error())
			}
		}
	}
	mp := Path(root, synthManifestRel)
	os.MkdirAll(filepath.Dir(mp), 0o755)
	text := canonicalYAML(manifest.Object(true))
	written := manifest
	if substantivelyEqual(text, mp, volatileManifest) {
		if m, err := LoadSynthManifest(mp); err == nil {
			written = m
		}
	} else if err := os.WriteFile(mp, []byte(text), 0o644); err != nil {
		return nil, promoteErr("manifest write failed: " + err.Error())
	}
	s.wipe()
	return written, nil
}

// applyPostCondition is project_drg.apply_post_condition.
func applyPostCondition(root string, hasGraph bool) error {
	mp := Path(root, synthManifestRel)
	gp := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot), "graph.yaml")
	if !exists(mp) {
		return nil
	}
	m, err := LoadSynthManifest(mp)
	if err != nil {
		return err
	}
	want := !hasGraph
	if m.BuiltInOnly == want && !(want && exists(gp)) {
		return nil
	}
	m.BuiltInOnly = want
	m = m.Finalize()
	text := yamlx.EmitWith(m.Object(true), yamlx.Options{Width: 80})
	if want {
		os.Remove(gp)
	}
	return os.WriteFile(mp, []byte(text), 0o644)
}

// ------------------------------------------------------------------ entry points

// SynthesizeMode is reconcile.SynthesizeMode.
type SynthesizeMode int

// Synthesize modes.
const (
	ModePreserve SynthesizeMode = iota
	ModePrune
)

// SynthOutcome is orchestrator.SynthesisResult.
type SynthOutcome struct {
	TargetKind, TargetSlug, InputsHash, AdapterID, AdapterVersion string
	Delta                                                         *ReconDelta
	ReferenceWarnings                                             []string
}

func primaryOutcome(req *SynthesisRequest, results []SynthResult, d *ReconDelta, warns []string) *SynthOutcome {
	pick := results[0].Prov
	for _, r := range results {
		if r.Prov.Kind == req.Placeholder.Kind && r.Prov.Slug == req.Placeholder.Slug {
			pick = r.Prov
			break
		}
	}
	return &SynthOutcome{pick.Kind, pick.Slug, pick.InputsHash, pick.AdapterID, pick.AdapterVersion, d, warns}
}

func synthTargets(req *SynthesisRequest) ([]SynthTarget, error) {
	return buildTargets(req, NormalizeInterviewSnapshot(req.InterviewSnapshot))
}

// Synthesize is orchestrator.synthesize (preserve / prune).
func Synthesize(root string, req *SynthesisRequest, a *SynthAdapter, version string, mode SynthesizeMode) (*SynthOutcome, error) {
	results, err := RunAll(req, a, version)
	if err != nil {
		return nil, err
	}
	builtIn := snapshotGraph(req)
	targets, err := synthTargets(req)
	if err != nil {
		return nil, err
	}
	org := orgChainGraph(root)
	fresh, warns, err := emitProjectLayer(targets, version, builtIn, root)
	if err != nil {
		return nil, err
	}
	base := builtIn
	if org != nil {
		base = org
	}
	out, err := reconcileSynthesis(root, fresh, results, req.RunID, version, base)
	if err != nil {
		return nil, err
	}
	if mode == ModePrune {
		out = applyPrune(out)
	}
	err = withStaging(root, req.RunID, func(s *stagingDir) error {
		_, err := promote(root, s, req, results, func(s *stagingDir) error {
			if len(out.overlay.Nodes) > 0 {
				if err := persistGraph(out.overlay, s.root); err != nil {
					return err
				}
			}
			return validationGate(s.root, builtIn, org, out.delta.Conflicts)
		}, out.manifest)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := applyPostCondition(root, len(out.overlay.Nodes) > 0); err != nil {
		return nil, err
	}
	return primaryOutcome(req, results, out.delta, warns), nil
}

// SynthesisDryRun is _run_synthesis_dry_run_with_artifacts +
// _reconciliation_preview.
func SynthesisDryRun(root string, req *SynthesisRequest, a *SynthAdapter, version string) ([]string, []StagedArtifact, *ReconDelta, error) {
	results, err := RunAll(req, a, version)
	if err != nil {
		return nil, nil, nil, err
	}
	builtIn := snapshotGraph(req)
	targets, err := synthTargets(req)
	if err != nil {
		return nil, nil, nil, err
	}
	org := orgChainGraph(root)
	var staged []string
	err = withStaging(root, req.RunID, func(s *stagingDir) error {
		sel, err := stageArtifacts(s, results)
		if err != nil {
			return err
		}
		g, _, err := emitProjectLayer(targets, version, builtIn, root)
		if err != nil {
			return err
		}
		if err := persistGraph(g, s.root); err != nil {
			return err
		}
		if err := validationGate(s.root, builtIn, org, nil); err != nil {
			return err
		}
		if err := runNeutralityGate(s, results, req.Evidence); err != nil {
			return err
		}
		staged = sel
		s.wipe()
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	written, err := ComputeWrittenArtifacts(results)
	if err != nil {
		return nil, nil, nil, err
	}
	d, err := ReconciliationPreview(root, req, version)
	if err != nil {
		return nil, nil, nil, err
	}
	return staged, written, d, nil
}

// ReconciliationPreview is _reconciliation_preview.
func ReconciliationPreview(root string, req *SynthesisRequest, version string) (*ReconDelta, error) {
	builtIn := snapshotGraph(req)
	targets, err := synthTargets(req)
	if err != nil {
		return nil, err
	}
	org := orgChainGraph(root)
	fresh, _, err := emitProjectLayer(targets, version, builtIn, root)
	if err != nil {
		return nil, err
	}
	base := builtIn
	if org != nil {
		base = org
	}
	out, err := reconcileSynthesis(root, fresh, nil, req.RunID, version, base)
	if err != nil {
		return nil, err
	}
	return out.delta, nil
}

// OrphanedRemovals is _orphaned_removals.
func OrphanedRemovals(d *ReconDelta) []NodeOrEdgeRef {
	if d == nil {
		return nil
	}
	var out []NodeOrEdgeRef
	for _, r := range d.Removable {
		if r.BackingArtifact == "" {
			out = append(out, r)
		}
	}
	return out
}

// RequireBundleComplete is _raise_if_bundle_incomplete.
func RequireBundleComplete(root string) error {
	if !exists(Path(root, CharterYAML)) {
		return &RequestError{"Charter bundle incomplete: 'charter.yaml' is missing. Run `spec-kitty charter generate` (or the charter.yaml migration) first to compile it, then retry `charter synthesize`."}
	}
	return nil
}

// WrittenArtifactsFromManifest is _load_written_artifacts_from_manifest.
func WrittenArtifactsFromManifest(root string) []any {
	out := []any{}
	m, err := LoadSynthManifest(Path(root, synthManifestRel))
	if err != nil {
		return out
	}
	for _, e := range m.Artifacts {
		var id any
		if e.Kind == "directive" {
			if u := provenanceURN(root, e.ProvenancePath); u != "" {
				if _, x, ok := strings.Cut(u, ":"); ok && x != "" {
					id = x
				}
			}
		}
		out = append(out, jsonx.NewObject("path", e.Path, "kind", e.Kind, "slug", e.Slug, "artifact_id", id))
	}
	return out
}

// ------------------------------------------------------------------ resynthesize

// ResynthOutcome is resynthesize_pipeline.ResynthesisResult.
type ResynthOutcome struct {
	MatchedForm, MatchedValue string
	Targets                   []SynthTarget
	RunID                     string
	ManifestArtifacts         int
	Noop                      bool
	Diagnostic                string
	ReferenceWarnings         []string
}

// projectArtifactsFromProvenance is _load_project_artifacts_from_provenance.
func projectArtifactsFromProvenance(root string) []SynthTarget {
	dir := Path(root, ".kittify/charter/provenance")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	labels := map[string]string{}
	pack := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	if exists(pack) {
		if g, err := loadGraphOrDir(pack); err == nil {
			for _, n := range g.Nodes {
				if n.Label != "" {
					labels[n.URN] = n.Label
				}
			}
		}
	}
	var names []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".yaml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []SynthTarget
	for _, n := range names {
		o := readYAMLObject(filepath.Join(dir, n))
		if o == nil {
			continue
		}
		kind, slug, urn := o.GetString("artifact_kind"), o.GetString("artifact_slug"), o.GetString("artifact_urn")
		_, id, ok := strings.Cut(urn, ":")
		if !ok || kind == "" || slug == "" {
			continue
		}
		var ss *string
		if v, ok := o.Get("source_section"); ok && v != nil {
			s := pyStrOf(v)
			ss = &s
		}
		var su []string
		for _, x := range o.GetList("source_urns") {
			su = append(su, pyStrOf(x))
		}
		title := labels[kind+":"+id]
		if title == "" {
			title = pyTitle(strings.ReplaceAll(slug, "-", " "))
		}
		t, err := newSynthTarget(SynthTarget{Kind: kind, Slug: slug, Title: title, ArtifactID: id, SourceSection: ss, SourceURNs: su})
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	return out
}

// mergedDRGURNs is the node URNs of _load_merged_drg.
func mergedDRGURNs(root string, req *SynthesisRequest) []string {
	urns := append([]string{}, req.DRGNodes...)
	pack := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	if exists(pack) {
		if g, err := loadGraphOrDir(pack); err == nil {
			for _, n := range g.Nodes {
				urns = append(urns, n.URN)
			}
		}
	}
	return urns
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if a == b {
		return 0
	}
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	prev := make([]int, len(ra)+1)
	for i := range prev {
		prev[i] = i
	}
	for _, cb := range rb {
		curr := []int{prev[0] + 1}
		for j, ca := range ra {
			cost := 1
			if ca == cb {
				cost = 0
			}
			curr = append(curr, min(curr[j]+1, prev[j+1]+1, prev[j]+cost))
		}
		prev = curr
	}
	return prev[len(prev)-1]
}

var drgNodeKinds = map[string]bool{"directive": true, "tactic": true, "paradigm": true, "styleguide": true, "toolguide": true, "procedure": true,
	"agent_profile": true, "action": true, "glossary_scope": true, "template": true, "mission_step_contract": true, "asset": true,
	"glossary_pack": true, "anti_pattern": true, "mission_type": true, "skill": true}

// resolveTopic is topic_resolver.resolve.
func resolveTopic(raw string, arts []SynthTarget, drgURNs, sections []string) (string, string, []SynthTarget, error) {
	if strings.TrimSpace(raw) == "" {
		return "", "", nil, fmt.Errorf("Topic selector must be a non-empty string. Free-text selectors are rejected (C-004). Use one of: <kind>:<slug>, <drg-urn>, or <interview-section-label>.")
	}
	raw = strings.TrimSpace(raw)
	var tried []string
	if i := strings.Index(raw, ":"); i >= 0 {
		lhs, rhs := strings.TrimSpace(raw[:i]), strings.TrimSpace(raw[i+1:])
		if lhs == "directive" || lhs == "tactic" || lhs == "styleguide" {
			tried = append(tried, "kind_slug")
			for _, a := range arts {
				if a.Kind == lhs && (a.ArtifactID == rhs || a.Slug == rhs) {
					return "kind_slug", raw, []SynthTarget{a}, nil
				}
			}
		}
		tried = append(tried, "drg_urn")
		if drgNodeKinds[lhs] {
			for _, u := range drgURNs {
				if u == raw {
					matched := []SynthTarget{}
					for _, a := range arts {
						for _, su := range a.SourceURNs {
							if su == raw {
								matched = append(matched, a)
								break
							}
						}
					}
					return "drg_urn", raw, matched, nil
				}
			}
		}
	} else {
		tried = append(tried, "interview_section")
		norm := CanonicalizeInterviewSectionLabel(raw)
		for _, s := range sections {
			if CanonicalizeInterviewSectionLabel(s) == norm {
				matched := []SynthTarget{}
				for _, a := range arts {
					if a.SourceSection != nil && *a.SourceSection == norm {
						matched = append(matched, a)
					}
				}
				return "interview_section", norm, matched, nil
			}
		}
	}
	type cand struct {
		kind, value string
		dist        int
	}
	var cs []cand
	for _, a := range arts {
		v := a.Kind + ":" + a.ArtifactID
		cs = append(cs, cand{"kind_slug", v, levenshtein(raw, v)})
	}
	for _, u := range drgURNs {
		if u != "" {
			cs = append(cs, cand{"drg_urn", u, levenshtein(raw, u)})
		}
	}
	for _, s := range sections {
		cs = append(cs, cand{"interview_section", s, levenshtein(raw, s)})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].dist != cs[j].dist {
			return cs[i].dist < cs[j].dist
		}
		return cs[i].value < cs[j].value
	})
	if len(cs) > 5 {
		cs = cs[:5]
	}
	var strsC []string
	for _, c := range cs {
		strsC = append(strsC, fmt.Sprintf("%s:%s (distance=%d)", c.kind, c.value, c.dist))
	}
	forms := strings.Join(tried, ", ")
	if forms == "" {
		forms = "all"
	}
	msg := fmt.Sprintf("Topic selector '%s' could not be resolved (tried: %s). ", raw, forms)
	if len(strsC) > 0 {
		msg += "Did you mean one of: " + strings.Join(strsC, ", ") + "?"
	} else {
		msg += "No candidates found in the project-local artifact set, merged DRG, or interview section labels."
	}
	return "", "", nil, &SynthError{Type: "TopicSelectorUnresolvedError", Msg: msg, Raw: raw, Candidates: strsC}
}

// newULID is resynthesize_pipeline._new_ulid.
func newULID() string {
	const alpha = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	ts := uint64(time.Now().UnixMilli())
	var b strings.Builder
	tsPart := make([]byte, 10)
	for i := 9; i >= 0; i-- {
		tsPart[i] = alpha[ts&0x1f]
		ts >>= 5
	}
	b.Write(tsPart)
	rnd := []byte(newRunID())
	for i := 0; i < 16; i++ {
		b.WriteByte(alpha[int(rnd[i])%32])
	}
	return b.String()
}

// Resynthesize is resynthesize_pipeline.run.
func Resynthesize(root string, req *SynthesisRequest, a *SynthAdapter, version, topic string) (*ResynthOutcome, error) {
	mp := Path(root, synthManifestRel)
	if !exists(mp) {
		return nil, &missingManifestError{fmt.Sprintf("No prior synthesis manifest found at '%s'. Run 'spec-kitty charter synthesize' first to create a baseline, then use 'spec-kitty charter resynthesize --topic' for bounded updates.", mp)}
	}
	em, err := LoadSynthManifest(mp)
	if err != nil {
		return nil, err
	}
	arts := projectArtifactsFromProvenance(root)
	var sections []string
	for _, kv := range req.InterviewSnapshot.Pairs() {
		sections = append(sections, kv.Key)
	}
	form, value, targets, err := resolveTopic(topic, arts, mergedDRGURNs(root, req), sections)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return &ResynthOutcome{MatchedForm: form, MatchedValue: value, RunID: em.RunID, ManifestArtifacts: len(em.Artifacts), Noop: true,
			Diagnostic: fmt.Sprintf("Topic '%s' resolved to a DRG URN but no project-local artifacts reference it. No writes performed (EC-4).", topic)}, nil
	}
	bounded := *req
	bounded.RunID = newULID()
	slugs := map[string]bool{}
	for _, t := range targets {
		slugs[t.Slug] = true
	}
	all, err := RunAll(&bounded, a, version)
	if err != nil {
		return nil, err
	}
	var results []SynthResult
	for _, r := range all {
		if slugs[r.Prov.Slug] {
			results = append(results, r)
		}
	}
	nm := rewriteManifest(em, results, bounded.RunID, root, version)
	builtIn := snapshotGraph(req)
	org := orgChainGraph(root)
	var written *SynthManifest
	err = withStaging(root, bounded.RunID, func(s *stagingDir) error {
		m, err := promote(root, s, &bounded, results, func(s *stagingDir) error {
			upd, _, err := emitProjectLayer(targets, version, builtIn, root)
			if err != nil {
				return err
			}
			g := upd
			pack := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
			if exists(pack) {
				ex, err := loadGraphOrDir(pack)
				if err != nil {
					return err
				}
				g = mergeProjectOverlay(ex, upd)
			}
			if err := persistGraph(g, s.root); err != nil {
				return err
			}
			return validationGate(s.root, builtIn, org, nil)
		}, nm)
		written = m
		return err
	})
	if err != nil {
		return nil, err
	}
	return &ResynthOutcome{MatchedForm: form, MatchedValue: value, Targets: targets, RunID: written.RunID, ManifestArtifacts: len(written.Artifacts)}, nil
}

type missingManifestError struct{ msg string }

func (e *missingManifestError) Error() string { return e.msg }

// IsMissingManifest reports a resynthesize FileNotFoundError.
func IsMissingManifest(err error) bool { _, ok := err.(*missingManifestError); return ok }
