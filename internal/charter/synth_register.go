package charter

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports charter.activation.project_registration (direct-written
// project doctrine registration) and the synthesize fresh-project
// `_synthesize_project_layer` lane that preserves it.

// ProjectArtifact is project_scan.ProjectArtifact.
type ProjectArtifact struct {
	Path string // absolute
	Node dct.Node
	Body *jsonx.Object
}

var regDirectWriteKinds = []dct.Kind{dct.Directive, dct.Tactic, dct.Styleguide, dct.Procedure, dct.AgentProfile}

// artifactURN is id_normalizer.artifact_to_urn.
func artifactURN(kind, id string) string {
	if kind == "directive" {
		return "directive:" + dct.NormalizeDirectiveID(id)
	}
	return kind + ":" + id
}

// ScanProjectArtifacts is project_scan.scan_project_artifacts (paths nil =
// every artifact).
func ScanProjectArtifacts(root string, paths map[string]bool) ([]ProjectArtifact, error) {
	pack := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	var out []ProjectArtifact
	seen := map[string]string{}
	for _, k := range regDirectWriteKinds {
		dir := filepath.Join(pack, dct.ProjectKindDir(k))
		var files []string
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), k.Suffix()) {
				files = append(files, p)
			}
			return nil
		})
		sort.Strings(files)
		for _, p := range files {
			if paths != nil && !paths[p] {
				continue
			}
			fail := func(msg string) error { return fmt.Errorf("Invalid project %s artifact %s: %s", k, p, msg) }
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, fail(err.Error())
			}
			v, err := yamlx.Parse(b)
			if err != nil {
				return nil, fail(err.Error())
			}
			body, ok := v.(*jsonx.Object)
			if !ok {
				return nil, fail(fmt.Sprintf("1 validation error for %s\n  Input should be a valid dictionary or instance of %s [type=model_type, input_value=%s, input_type=%s]",
					schemaModels[k.Plural()].name, schemaModels[k.Plural()].name, truncRepr(pyValueRepr(v)), pyTypeOf(v)))
			}
			if msg := ValidateArtifactSchema(k.Plural(), body); msg != "" {
				return nil, fail(msg)
			}
			idKey := "id"
			if k == dct.AgentProfile {
				idKey = "profile-id"
				if _, ok := body.Get(idKey); !ok {
					idKey = "profile_id"
				}
			}
			id := pyStrOf(mustGetAny(body, idKey))
			urn := artifactURN(string(k), id)
			node := dct.Node{URN: urn, Kind: string(k)}
			if s, ok := mustGetAny(body, "name").(string); ok && s != "" {
				node.Label = s
			} else if s, ok := mustGetAny(body, "title").(string); ok && s != "" {
				node.Label = s
			}
			if prev, dup := seen[urn]; dup {
				return nil, fmt.Errorf("Duplicate project artifact %s: %s and %s", urn, prev, p)
			}
			seen[urn] = p
			out = append(out, ProjectArtifact{p, node, body})
		}
	}
	return out, nil
}

// projectReferenceEdges is project_scan.project_reference_edges.
func projectReferenceEdges(arts []ProjectArtifact, universe []dct.Node) ([]dct.Edge, []string) {
	known := map[string]bool{}
	for _, n := range universe {
		known[n.URN] = true
	}
	type key struct{ s, t, r string }
	var order []key
	edges := map[key]dct.Edge{}
	warnSet := map[string]bool{}
	for _, a := range arts {
		if a.Node.Kind != "agent_profile" {
			continue
		}
		var cands []dct.Edge
		add := func(refKey, kind, field, rel string, withReason bool) {
			for _, x := range a.Body.GetList(refKey) {
				r, ok := x.(*jsonx.Object)
				if !ok {
					continue
				}
				id := pyStrOf(mustGetAny(r, field))
				if id == "" || mustGetAny(r, field) == nil {
					continue
				}
				e := dct.Edge{Source: a.Node.URN, Target: artifactURN(kind, id), Relation: rel}
				if withReason {
					if v, ok := r.Get("rationale"); ok && v != nil {
						s := pyStrOf(v)
						e.Reason = &s
					}
				}
				cands = append(cands, e)
			}
		}
		add("directive-references", "directive", "code", dct.RelRequires, false)
		add("tactic-references", "tactic", "id", dct.RelRequires, true)
		add("toolguide-references", "toolguide", "id", dct.RelSuggests, true)
		add("styleguide-references", "styleguide", "id", dct.RelSuggests, true)
		if c := a.Body.GetObject("collaboration"); c != nil {
			for _, p := range c.GetList("operating-procedures") {
				cands = append(cands, dct.Edge{Source: a.Node.URN, Target: artifactURN("procedure", pyStrOf(p)), Relation: dct.RelRequires})
			}
		}
		for _, e := range cands {
			if !known[e.Target] {
				warnSet[fmt.Sprintf("%s references unresolved %s; author or install that artifact, then repeat charter activate --cascade all.", a.Node.URN, e.Target)] = true
				continue
			}
			k := key{e.Source, e.Target, e.Relation}
			if _, ok := edges[k]; !ok {
				order = append(order, k)
			}
			edges[k] = e
		}
	}
	var out []dct.Edge
	for _, k := range order {
		out = append(out, edges[k])
	}
	return out, dct.SortedKeys(warnSet)
}

// slugFor is artifact_kinds.slug_for.
func slugFor(kind, id string) string {
	if kind == "directive" {
		id = strings.ReplaceAll(strings.ToLower(id), "_", "-")
	}
	return strings.ReplaceAll(url.PathEscape(id), "/", "%2F")
}

func sidecarRecordedURN(root string, e ManifestEntry) string {
	o := readYAMLObject(filepath.Join(root, filepath.FromSlash(e.ProvenancePath)))
	if o == nil || o.GetString("adapter_id") != "project-direct-write" {
		return ""
	}
	return o.GetString("artifact_urn")
}

type staleReg struct {
	urn   string
	entry ManifestEntry
}

// verifyManifestHash is manifest.verify_manifest_hash.
func verifyManifestHash(m *SynthManifest) error {
	computed := m.Finalize().ManifestHash
	if computed == m.ManifestHash {
		return nil
	}
	if m.raw != nil {
		full := m.Object(false)
		sub := &jsonx.Object{}
		for _, kv := range full.Pairs() {
			if _, ok := m.raw.Get(kv.Key); ok {
				sub.Set(kv.Key, kv.Value)
			}
		}
		if sha256Hex(canonicalYAML(sub)) == m.ManifestHash {
			return nil
		}
	}
	return fmt.Errorf("manifest_hash mismatch (stored %s..., computed %s...)", trunc12(m.ManifestHash), trunc12(computed))
}

func trunc12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

type regWrite struct{ path, text string }

// RegistrationPlan is project_registration.ProjectRegistrationPlan.
type RegistrationPlan struct {
	Artifacts []ProjectArtifact
	Warnings  []string
	Writes    []regWrite
	Deletes   []string
}

// PlanProjectRegistration is plan_project_registration.
func PlanProjectRegistration(root, version string) (*RegistrationPlan, error) {
	arts, err := ScanProjectArtifacts(root, nil)
	if err != nil {
		return nil, err
	}
	// The validated base graph is loaded before the no-op early return
	// (load_validated_graph raises DRGValidationError on an invalid merge).
	if _, err := dct.LoadValidatedGraph(root, ResolvePackChain(root)); err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, a := range arts {
		live[a.Node.URN] = true
	}
	mp := Path(root, synthManifestRel)
	var stale []staleReg
	var existing *SynthManifest
	if exists(mp) {
		m, err := LoadSynthManifest(mp)
		if err != nil {
			return nil, err
		}
		existing = m
		for _, e := range m.Artifacts {
			if u := sidecarRecordedURN(root, e); u != "" && !live[u] {
				stale = append(stale, staleReg{u, e})
			}
		}
	}
	if len(arts) == 0 && len(stale) == 0 {
		return &RegistrationPlan{}, nil
	}
	base, err := dct.LoadGraph(root, ResolvePackChain(root))
	if err != nil {
		return nil, err
	}
	staleURNs := map[string]bool{}
	for _, s := range stale {
		staleURNs[s.urn] = true
	}
	// _project_graph
	pack := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	var ex *dct.Graph
	if dct.HasGraphFiles(pack) {
		if ex, err = loadGraphOrDir(pack); err != nil {
			return nil, err
		}
	}
	var nodeOrder []string
	nodes := map[string]dct.Node{}
	put := func(n dct.Node) {
		if _, ok := nodes[n.URN]; !ok {
			nodeOrder = append(nodeOrder, n.URN)
		}
		nodes[n.URN] = n
	}
	if ex != nil {
		for _, n := range ex.Nodes {
			put(n)
		}
	}
	for u := range staleURNs {
		if _, ok := nodes[u]; ok {
			delete(nodes, u)
			var keep []string
			for _, x := range nodeOrder {
				if x != u {
					keep = append(keep, x)
				}
			}
			nodeOrder = keep
		}
	}
	for _, a := range arts {
		put(a.Node)
	}
	var projNodes []dct.Node
	for _, u := range nodeOrder {
		projNodes = append(projNodes, nodes[u])
	}
	var universe []dct.Node
	for _, n := range base.Nodes {
		if !staleURNs[n.URN] {
			universe = append(universe, n)
		}
	}
	projected, warnings := projectReferenceEdges(arts, append(universe, projNodes...))
	profileURNs := map[string]bool{}
	for _, a := range arts {
		if a.Node.Kind == "agent_profile" {
			profileURNs[a.Node.URN] = true
		}
	}
	refRel := map[string]bool{dct.RelRequires: true, dct.RelSuggests: true}
	type tk struct{ s, t, r string }
	var tOrder []tk
	tmap := map[tk]dct.Edge{}
	putE := func(e dct.Edge) {
		k := tk{e.Source, e.Target, e.Relation}
		if _, ok := tmap[k]; !ok {
			tOrder = append(tOrder, k)
		}
		tmap[k] = e
	}
	if ex != nil {
		for _, e := range ex.Edges {
			if profileURNs[e.Source] && refRel[e.Relation] {
				continue
			}
			if len(stale) > 0 && (staleURNs[e.Source] || staleURNs[e.Target]) {
				continue
			}
			putE(e)
		}
	}
	for _, e := range projected {
		putE(e)
	}
	genAt := nowUTCSeconds()
	if ex != nil {
		genAt = ex.GeneratedAt
	}
	project := &dct.Graph{SchemaVersion: "1.0", GeneratedAt: genAt, GeneratedBy: "spec-kitty project registration", Nodes: projNodes}
	for _, k := range tOrder {
		project.Edges = append(project.Edges, tmap[k])
	}
	var staleWarn []string
	for _, s := range stale {
		staleWarn = append(staleWarn, fmt.Sprintf("Pruned project registration for %s: the current project scan no longer produces it (registered source %s); removed its graph node and edges, manifest entry, and provenance sidecar.", s.urn, s.entry.Path))
	}
	projTriples := map[tk]bool{}
	for _, e := range project.Edges {
		projTriples[tk{e.Source, e.Target, e.Relation}] = true
	}
	filtered := &dct.Graph{SchemaVersion: base.SchemaVersion, GeneratedAt: base.GeneratedAt, GeneratedBy: base.GeneratedBy, Nodes: universe}
	for _, e := range base.Edges {
		if projTriples[tk{e.Source, e.Target, e.Relation}] || (profileURNs[e.Source] && refRel[e.Relation]) || staleURNs[e.Source] || staleURNs[e.Target] {
			continue
		}
		filtered.Edges = append(filtered.Edges, e)
	}
	if errs := validateGraph(dct.MergeLayers(filtered, project)); len(errs) > 0 {
		return nil, fmt.Errorf("%d validation error(s): %s", len(errs), strings.Join(errs, "; "))
	}
	writes := []regWrite{{filepath.Join(pack, "graph.yaml"), canonicalYAML(graphDocument(project))}}
	// _registration_records
	if existing != nil {
		if err := verifyManifestHash(existing); err != nil {
			return nil, err
		}
	}
	type ek struct{ kind, slug string }
	var eOrder []ek
	entries := map[ek]ManifestEntry{}
	putEntry := func(e ManifestEntry) {
		k := ek{e.Kind, e.Slug}
		if _, ok := entries[k]; !ok {
			eOrder = append(eOrder, k)
		}
		entries[k] = e
	}
	delEntry := func(k ek) {
		if _, ok := entries[k]; !ok {
			return
		}
		delete(entries, k)
		var keep []ek
		for _, x := range eOrder {
			if x != k {
				keep = append(keep, x)
			}
		}
		eOrder = keep
	}
	if existing != nil {
		for _, e := range existing.Artifacts {
			putEntry(e)
		}
	}
	for _, s := range stale {
		delEntry(ek{s.entry.Kind, s.entry.Slug})
	}
	urnEntries := map[string]ManifestEntry{}
	for _, k := range eOrder {
		if u := sidecarRecordedURN(root, entries[k]); u != "" {
			if _, ok := urnEntries[u]; !ok {
				urnEntries[u] = entries[k]
			}
		}
	}
	var staleSidecars []string
	ts := nowUTCSeconds()
	runID := newULID()
	notes := "Registered authored project content; no generation or source rewrite performed."
	for _, a := range arts {
		kind, id, _ := strings.Cut(a.Node.URN, ":")
		slug := slugFor(kind, id)
		b, _ := os.ReadFile(a.Path)
		hash := sha256Hex(string(b))
		rel, _ := filepath.Rel(root, a.Path)
		rel = filepath.ToSlash(rel)
		prev, hasPrev := urnEntries[a.Node.URN]
		if !hasPrev {
			prev, hasPrev = entries[ek{kind, slug}]
		}
		if hasPrev && prev.Slug != slug {
			delEntry(ek{kind, prev.Slug})
			if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(prev.ProvenancePath))); err == nil && !fi.IsDir() {
				staleSidecars = append(staleSidecars, filepath.Join(root, filepath.FromSlash(prev.ProvenancePath)))
			}
		}
		provRel := ".kittify/charter/provenance/" + kind + "-" + slug + ".yaml"
		if hasPrev && prev.Slug == slug && prev.ContentHash == hash && prev.Path == rel && prev.ProvenancePath == provRel && exists(Path(root, provRel)) {
			continue
		}
		ss := rel
		rec := &ProvEntry{URN: a.Node.URN, Kind: kind, Slug: slug, ContentHash: hash, InputsHash: hash, AdapterID: "project-direct-write",
			AdapterVersion: "1", SynthesizerVersion: version, SourceSection: &ss, SourceURNs: []string{}, SourceInputIDs: []string{rel},
			GeneratedAt: ts, ProducedAt: ts, CorpusSnapshotID: "(none)", RunID: runID, Notes: &notes}
		writes = append(writes, regWrite{Path(root, provRel), canonicalYAML(rec.Object())})
		putEntry(ManifestEntry{kind, slug, rel, provRel, hash})
	}
	var m *SynthManifest
	var arts2 []ManifestEntry
	for _, k := range eOrder {
		arts2 = append(arts2, entries[k])
	}
	if existing != nil {
		c := *existing
		c.Artifacts = arts2
		c.BuiltInOnly = false
		m = &c
	} else {
		m = &SynthManifest{SchemaVersion: "3", CreatedAt: ts, RunID: runID, AdapterID: "project-direct-write", AdapterVersion: "1",
			SynthesizerVersion: version, Artifacts: arts2}
	}
	m = m.Finalize()
	writes = append(writes, regWrite{mp, canonicalYAML(m.Object(true))})
	written := map[string]bool{}
	for _, w := range writes {
		written[w.path] = true
	}
	delSet := map[string]bool{}
	for _, s := range stale {
		p := Path(root, s.entry.ProvenancePath)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			delSet[p] = true
		}
	}
	for _, p := range staleSidecars {
		delSet[p] = true
	}
	plan := &RegistrationPlan{Artifacts: arts, Warnings: append(staleWarn, warnings...)}
	for _, p := range dct.SortedKeys(delSet) {
		if !written[p] {
			plan.Deletes = append(plan.Deletes, p)
		}
	}
	for _, w := range writes {
		if b, err := os.ReadFile(w.path); err != nil || string(b) != w.text {
			plan.Writes = append(plan.Writes, w)
		}
	}
	return plan, nil
}

// CommitProjectRegistration is commit_project_registration.
func CommitProjectRegistration(plan *RegistrationPlan) error {
	for _, w := range plan.Writes {
		if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(w.path, []byte(w.text), 0o644); err != nil {
			return err
		}
	}
	for _, p := range plan.Deletes {
		os.Remove(p)
	}
	return nil
}

const projectDoctrineProvenance = "# Project Doctrine Provenance\n\nThis project contains authored doctrine registered by `spec-kitty charter synthesize`.\nSource YAML remains unchanged. Per-artifact source paths and content hashes are recorded\nin `.kittify/charter/provenance/` and `.kittify/charter/synthesis-manifest.yaml`.\n"

// SynthesizeProjectLayer is _fresh_project_layer._synthesize_project_layer
// (nil when the project holds no direct-written artifacts).
func SynthesizeProjectLayer(root, version string, dryRun bool) (*jsonx.Object, error) {
	arts, err := ScanProjectArtifacts(root, nil)
	if err != nil || len(arts) == 0 {
		return nil, err
	}
	plan, err := PlanProjectRegistration(root, version)
	if err != nil {
		return nil, err
	}
	mp := Path(root, synthManifestRel)
	var m *SynthManifest
	var rest []regWrite
	for _, w := range plan.Writes {
		if w.path == mp {
			m, err = parseSynthManifest([]byte(w.text))
			if err != nil {
				return nil, err
			}
			continue
		}
		rest = append(rest, w)
	}
	if m == nil {
		if m, err = LoadSynthManifest(mp); err != nil {
			return nil, err
		}
	}
	m.SchemaVersion = "3"
	m.BuiltInOnly = false
	m.BundleContentHash = bundleHashPtr(root)
	m = m.Finalize()
	rest = append(rest, regWrite{filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot), "PROVENANCE.md"), projectDoctrineProvenance})
	rest = append(rest, regWrite{mp, canonicalYAML(m.Object(true))})
	var writes []regWrite
	for _, w := range rest {
		if b, err := os.ReadFile(w.path); err != nil || string(b) != w.text {
			writes = append(writes, w)
		}
	}
	if !dryRun {
		if err := CommitProjectRegistration(&RegistrationPlan{Writes: writes, Deletes: plan.Deletes}); err != nil {
			return nil, err
		}
	}
	wa := []any{}
	for _, a := range plan.Artifacts {
		rel, _ := filepath.Rel(root, a.Path)
		kind, id, _ := strings.Cut(a.Node.URN, ":")
		wa = append(wa, jsonx.NewObject("path", filepath.ToSlash(rel), "kind", kind, "slug", id, "artifact_id", id))
	}
	files := []any{}
	for _, w := range writes {
		rel, _ := filepath.Rel(root, w.path)
		files = append(files, filepath.ToSlash(rel))
	}
	result, mode, key := "success", "project_direct_write", "files_written"
	if dryRun {
		result, mode, key = "dry_run", "project_direct_write_dry_run", "files_planned"
	}
	return jsonx.NewObject("result", result, "success", true, "mode", mode, "adapter", jsonx.NewObject("id", "project-direct-write", "version", version),
		"written_artifacts", wa, "warnings", strs(nz(plan.Warnings)), key, files, "planned_deletes", []any{}), nil
}
