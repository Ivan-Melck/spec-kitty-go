package charter

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports charter.offering.packs.pack_assembler (assemble_pack,
// render_assembly_result) and the generated pack-manifest writer
// (pack_manifest.write_pack_manifest).

// ConflictItem is one assembly conflict.
type ConflictItem struct {
	ArtifactType, ArtifactID string
	Packs                    []string
}

// Object is ConflictItem.to_dict.
func (c ConflictItem) Object() *jsonx.Object {
	return jsonx.NewObject("artifact_type", c.ArtifactType, "artifact_id", c.ArtifactID, "conflicting_packs", strsAny(c.Packs))
}

// AssemblyResult is the assemble outcome.
type AssemblyResult struct {
	OK               bool
	ArtifactsWritten int
	Conflicts        []ConflictItem
	Errors           []string
}

// Object is AssemblyResult.to_dict.
func (r *AssemblyResult) Object() *jsonx.Object {
	cs := []any{}
	for _, c := range r.Conflicts {
		cs = append(cs, c.Object())
	}
	return jsonx.NewObject("ok", r.OK, "artifacts_written", r.ArtifactsWritten, "conflicts", cs, "errors", strsAny(r.Errors))
}

// assembleKinds are the kinds with a built-in content dir (ArtifactKind order).
var assembleKinds = func() []dct.Kind {
	var out []dct.Kind
	for _, k := range dct.AllKinds {
		if k.HasBuiltInContentDir() {
			out = append(out, k)
		}
	}
	return out
}()

func assembleScan(pack string, k dct.Kind) []string {
	d := filepath.Join(pack, k.Plural())
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		return nil
	}
	if k == dct.Styleguide {
		return rglob(d, k.Suffix())
	}
	return globSuffix(d, k.Suffix())
}

func readArtifactID(p string) (string, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return "", false
	}
	if o, ok := v.(*jsonx.Object); ok {
		if s, ok := o.Get("id"); ok {
			if id, ok := s.(string); ok {
				return id, true
			}
		}
	}
	return "", false
}

type owner struct{ pack, file string }

// AssemblePack is assemble_pack_with_org_charter.
func AssemblePack(inputs []string, out string, force bool, conflictsOut *string) *AssemblyResult {
	finish := func(r *AssemblyResult) *AssemblyResult {
		if conflictsOut != nil {
			cs := []any{}
			for _, c := range r.Conflicts {
				cs = append(cs, c.Object())
			}
			_ = os.MkdirAll(filepath.Dir(*conflictsOut), 0o755)
			_ = os.WriteFile(*conflictsOut, []byte(jsonx.Dumps(cs, jsonx.SortedPretty)), 0o644)
		}
		return r
	}
	if len(inputs) == 0 {
		return finish(&AssemblyResult{Errors: []string{"no input packs provided"}})
	}
	var missing []string
	for _, p := range inputs {
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			missing = append(missing, "input pack not found: "+p)
		}
	}
	if len(missing) > 0 {
		return finish(&AssemblyResult{Errors: missing})
	}
	var orgCharters []string
	for _, p := range inputs {
		c := filepath.Join(p, "org-charter.yaml")
		if _, err := os.Stat(c); err == nil {
			orgCharters = append(orgCharters, c)
		}
	}
	// artifact conflicts
	type occ struct{ pack, file string }
	seen := map[string]map[string][]occ{}
	var pluralOrder []string
	lastOwner := map[string]map[string]owner{}
	var lastOrder = map[string][]string{}
	for _, p := range inputs {
		for _, k := range assembleKinds {
			for _, f := range assembleScan(p, k) {
				id, ok := readArtifactID(f)
				if !ok {
					continue
				}
				pl := k.Plural()
				if seen[pl] == nil {
					seen[pl] = map[string][]occ{}
					pluralOrder = append(pluralOrder, pl)
					lastOwner[pl] = map[string]owner{}
				}
				seen[pl][id] = append(seen[pl][id], occ{filepath.Base(p), f})
				if _, ok := lastOwner[pl][id]; !ok {
					lastOrder[pl] = append(lastOrder[pl], id)
				}
				lastOwner[pl][id] = owner{p, f}
			}
		}
	}
	var conflicts []ConflictItem
	for _, pl := range pluralOrder {
		ids := make([]string, 0, len(seen[pl]))
		for id := range seen[pl] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if o := seen[pl][id]; len(o) > 1 {
				var names []string
				for _, x := range o {
					names = append(names, x.pack)
				}
				conflicts = append(conflicts, ConflictItem{pl, id, names})
			}
		}
	}
	// DRG conflicts
	type fragSet struct {
		pack  string
		files []string
	}
	var fragsByPack []fragSet
	edgeOwners := map[[3]string][]string{}
	var edgeOrder [][3]string
	for _, p := range inputs {
		d := filepath.Join(p, "drg")
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		fs := globSuffix(d, ".graph.yaml")
		fragsByPack = append(fragsByPack, fragSet{p, fs})
		for _, f := range fs {
			g, err := loadGraphDocument(f)
			if err != nil {
				continue
			}
			for _, e := range g.Edges {
				k := [3]string{e.Source, e.Target, e.Relation}
				if _, ok := edgeOwners[k]; !ok {
					edgeOrder = append(edgeOrder, k)
				}
				edgeOwners[k] = append(edgeOwners[k], filepath.Base(p))
			}
		}
	}
	for _, k := range edgeOrder {
		var uniq []string
		m := map[string]bool{}
		for _, o := range edgeOwners[k] {
			if !m[o] {
				m[o] = true
				uniq = append(uniq, o)
			}
		}
		if len(uniq) > 1 {
			conflicts = append(conflicts, ConflictItem{"drg", fmt.Sprintf("%s -[%s]-> %s", k[0], k[2], k[1]), uniq})
		}
	}
	// sanction conflicts
	merged := map[string]string{}
	owners := map[string][][2]string{}
	anyFile := false
	var serrs []string
	for _, p := range inputs {
		if !packSanctionPresent(p) {
			continue
		}
		anyFile = true
		pol, err := loadPackSanction(filepath.Base(p), p)
		if err != nil {
			serrs = append(serrs, err.Error())
			continue
		}
		for _, e := range pol.Entries {
			merged[e.URN] = e.Reason
			owners[e.URN] = append(owners[e.URN], [2]string{filepath.Base(p), e.Reason})
		}
	}
	if len(serrs) > 0 {
		return finish(&AssemblyResult{Errors: serrs})
	}
	urns := make([]string, 0, len(owners))
	for u := range owners {
		urns = append(urns, u)
	}
	sort.Strings(urns)
	for _, u := range urns {
		reasons := map[string]bool{}
		var names []string
		for _, d := range owners[u] {
			reasons[d[1]] = true
			names = append(names, d[0])
		}
		if len(reasons) > 1 {
			conflicts = append(conflicts, ConflictItem{"replaceable_builtins", u, names})
		}
	}
	if len(conflicts) > 0 && !force {
		return finish(&AssemblyResult{Conflicts: conflicts})
	}
	if st, err := os.Stat(out); err == nil && st.IsDir() {
		entries, _ := os.ReadDir(out)
		if len(entries) > 0 {
			if !force {
				return finish(&AssemblyResult{Errors: []string{fmt.Sprintf("output directory %s exists and is non-empty; use --force to overwrite", out)}})
			}
			if !hasRecognisablePackManifest(out) {
				return finish(&AssemblyResult{Errors: []string{fmt.Sprintf("refusing to delete non-pack output directory %s; with --force, the directory must contain a recognisable pack-manifest.yaml", out)}})
			}
			os.RemoveAll(out)
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return finish(&AssemblyResult{Errors: []string{err.Error()}})
	}
	written := 0
	copyTo := func(pack, pl, f string) {
		dest := filepath.Join(out, pl, filepath.Base(f))
		if pl == "styleguides" {
			if rel, err := filepath.Rel(filepath.Join(pack, pl), f); err == nil && !strings.HasPrefix(rel, "..") {
				dest = filepath.Join(out, pl, rel)
			}
		}
		_ = os.MkdirAll(filepath.Dir(dest), 0o755)
		_ = copyFile(f, dest)
		written++
	}
	if force {
		for _, pl := range pluralOrder {
			for _, id := range lastOrder[pl] {
				o := lastOwner[pl][id]
				copyTo(o.pack, pl, o.file)
			}
		}
	} else {
		for _, p := range inputs {
			for _, k := range assembleKinds {
				for _, f := range assembleScan(p, k) {
					copyTo(p, k.Plural(), f)
				}
			}
		}
	}
	if len(fragsByPack) > 0 {
		drgOut := filepath.Join(out, "drg")
		_ = os.MkdirAll(drgOut, 0o755)
		seq := 0
		seenEdges := map[[3]string]bool{}
		for _, fs := range fragsByPack {
			for _, f := range fs.files {
				seq++
				dest := filepath.Join(drgOut, fmt.Sprintf("%03d-%s-%s", seq, filepath.Base(fs.pack), filepath.Base(f)))
				if force {
					g, err := loadGraphDocument(f)
					if err != nil {
						_ = copyFile(f, dest)
						written++
						continue
					}
					var kept []MergedEdge
					for _, e := range g.Edges {
						k := [3]string{e.Source, e.Target, e.Relation}
						if seenEdges[k] {
							continue
						}
						seenEdges[k] = true
						kept = append(kept, e)
					}
					raw := readGraphHeader(f)
					_ = os.WriteFile(dest, []byte(pyyaml.Dump(graphDocumentDict(raw, g, kept), pyyaml.Options{})), 0o644)
				} else {
					_ = copyFile(f, dest)
				}
				written++
			}
		}
	}
	if len(orgCharters) > 0 {
		mergeOrgCharterFiles(orgCharters, out)
	}
	if anyFile {
		var es []ReplaceableBuiltin
		keys := make([]string, 0, len(merged))
		for u := range merged {
			keys = append(keys, u)
		}
		sort.Strings(keys)
		for _, u := range keys {
			es = append(es, ReplaceableBuiltin{u, merged[u]})
		}
		var items []any
		for _, e := range es {
			items = append(items, jsonx.NewObject("urn", e.URN, "reason", e.Reason))
		}
		if items == nil {
			items = []any{}
		}
		_ = os.WriteFile(filepath.Join(out, PackPolicyFilename), []byte(pyyaml.Dump(jsonx.NewObject("replaceable_builtins", items), pyyaml.Options{})), 0o644)
	}
	val := ValidatePack(out, false)
	if !val.OK() {
		os.RemoveAll(out)
		var msgs []string
		for i, e := range val.Errors {
			if i >= 3 {
				break
			}
			msgs = append(msgs, e.Message)
		}
		suffix := ""
		if len(val.Errors) > 3 {
			suffix = "; ..."
		}
		r := &AssemblyResult{Errors: []string{"assembled pack failed validation: " + strings.Join(msgs, "; ") + suffix}}
		if force {
			r.Conflicts = conflicts
		}
		return finish(r)
	}
	r := &AssemblyResult{OK: true, ArtifactsWritten: written}
	if force {
		r.Conflicts = conflicts
	}
	WritePackManifest(out, nil, nil, strings.Join(inputs, ","), "assemble")
	return finish(r)
}

func readGraphHeader(p string) *jsonx.Object {
	b, _ := os.ReadFile(p)
	v, _ := yamlx.Parse(b)
	if o, ok := v.(*jsonx.Object); ok {
		return o
	}
	return jsonx.NewObject()
}

// graphDocumentDict is extractor.graph_document_to_dict for a pruned
// fragment (node/edge fields as authored, provenance withheld, empty
// optional fields omitted).
func graphDocumentDict(raw *jsonx.Object, g *MergedGraph, kept []MergedEdge) *jsonx.Object {
	nodes := []any{}
	for _, n := range raw.GetList("nodes") {
		if o, ok := n.(*jsonx.Object); ok {
			c := jsonx.NewObject()
			for _, k := range []string{"urn", "kind", "label", "tags"} {
				if v, ok := o.Get(k); ok && v != nil {
					if l, isList := v.([]any); isList && len(l) == 0 {
						continue
					}
					c.Set(k, v)
				}
			}
			nodes = append(nodes, c)
		}
	}
	edges := []any{}
	for _, e := range kept {
		c := jsonx.NewObject("source", e.Source, "target", e.Target, "relation", e.Relation)
		if e.Reason != nil {
			c.Set("reason", *e.Reason)
		}
		edges = append(edges, c)
	}
	return jsonx.NewObject("schema_version", raw.GetString("schema_version"), "generated_at", raw.GetString("generated_at"),
		"generated_by", raw.GetString("generated_by"), "nodes", nodes, "edges", edges)
}

func mergeOrgCharterFiles(paths []string, out string) {
	interview := jsonx.NewObject()
	var required []any
	reqSeen := map[string]bool{}
	var gps []*jsonx.Object
	var schemaVersion any
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		v, err := yamlx.Parse(b)
		if err != nil {
			continue
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			continue
		}
		if sv, ok := o.Get("schema_version"); ok {
			schemaVersion = sv
		}
		if id := o.GetObject("interview_defaults"); id != nil {
			for _, kv := range id.Pairs() {
				interview.Set(kv.Key, kv.Value)
			}
		}
		for _, x := range o.GetList("required_directives") {
			s := pyStrOf(x)
			if !reqSeen[s] {
				reqSeen[s] = true
				required = append(required, x)
			}
		}
		for _, x := range o.GetList("governance_policies") {
			if g, ok := x.(*jsonx.Object); ok {
				gps = append(gps, g)
			}
		}
	}
	type key struct{ f, v string }
	seen := map[key]bool{}
	var dedup []any
	for _, g := range gps {
		fv, _ := g.Get("field")
		vv, _ := g.Get("value")
		k := key{pyStrOf(fv), pyStrOf(vv)}
		if !seen[k] {
			seen[k] = true
			dedup = append(dedup, g)
		}
	}
	if schemaVersion == nil || schemaVersion == "" {
		schemaVersion = "1.0"
	}
	if required == nil {
		required = []any{}
	}
	if dedup == nil {
		dedup = []any{}
	}
	sv := 0
	switch t := schemaVersion.(type) {
	case int:
		sv = t
	case string:
		if _, err := fmt.Sscanf(t, "%d", &sv); err != nil {
			return // OrgCharterPolicy validation fails: write nothing
		}
		if fmt.Sprint(sv) != strings.TrimSpace(t) {
			return
		}
	default:
		return
	}
	var gpOut []any
	for _, x := range dedup {
		g := x.(*jsonx.Object)
		fv, _ := g.Get("field")
		vv, _ := g.Get("value")
		enf := "advisory"
		if e, ok := g.Get("enforcement"); ok {
			enf = pyStrOf(e)
		}
		gpOut = append(gpOut, jsonx.NewObject("field", fv, "value", vv, "enforcement", enf))
	}
	if gpOut == nil {
		gpOut = []any{}
	}
	empty := func() []any { return []any{} }
	doc := jsonx.NewObject("schema_version", sv, "extends", nil, "org_name", nil, "interview_defaults", interview,
		"required_directives", required, "required_tactics", empty(), "required_paradigms", empty(), "required_styleguides", empty(),
		"required_toolguides", empty(), "required_procedures", empty(), "required_agent_profiles", empty(),
		"required_mission_step_contracts", empty(), "required_glossary_packs", empty(), "required_assets", empty(),
		"required_skills", empty(), "skill_namespace", nil, "governance_policies", gpOut, "activations", empty())
	_ = os.WriteFile(filepath.Join(out, "org-charter.yaml"), []byte(pyyaml.Dump(doc, pyyaml.Options{})), 0o644)
}

func hasRecognisablePackManifest(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "pack-manifest.yaml"))
	if err != nil {
		return false
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return false
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return false
	}
	for _, k := range []string{"artifact_counts", "fetched_at", "source_type", "source_url"} {
		if !o.Has(k) {
			return false
		}
	}
	if o.GetObject("artifact_counts") == nil {
		return false
	}
	if !o.Has("pack_version") && readAuthoredPackVersion(dir) == "" {
		return false
	}
	switch o.GetString("source_type") {
	case "assemble", "git", "https", "artifactory", "api":
		return true
	}
	return false
}

// StripSourceCredentials is strip_source_credentials.
func StripSourceCredentials(u string) string {
	if u == "" {
		return ""
	}
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	if p.Scheme != "http" && p.Scheme != "https" {
		return u
	}
	host := p.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := p.Port(); port != "" {
		host += ":" + port
	}
	return p.Scheme + "://" + host + p.EscapedPath()
}

// snapshotSHA256 is snapshot_sha256.
func snapshotSHA256(root string) string {
	h := sha256.New()
	var files []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if rel == "pack-manifest.yaml" || d.IsDir() {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	sort.Strings(files)
	for _, rel := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		rb := []byte(rel)
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(rb)))
		h.Write(n[:])
		h.Write(rb)
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		binary.BigEndian.PutUint64(n[:], uint64(st.Size()))
		h.Write(n[:])
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		_, _ = io.Copy(h, f)
		f.Close()
	}
	return hex.EncodeToString(h.Sum(nil))
}

var recognisedArtifactDirs = func() map[string]bool {
	m := map[string]bool{"drg": true}
	for _, k := range dct.AllKinds {
		if k.HasLayeredRepository() {
			m[k.Plural()] = true
		}
	}
	return m
}()

// countSnapshotArtifacts is count_snapshot_artifacts.
func countSnapshotArtifacts(dir string) *jsonx.Object {
	o := jsonx.NewObject()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return o
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if st, err := os.Stat(p); err != nil || !st.IsDir() || !recognisedArtifactDirs[e.Name()] {
			continue
		}
		bucket := e.Name()
		if bucket == "drg" {
			bucket = "drg_fragments"
		}
		o.Set(bucket, len(rglob(p, ".yaml")))
	}
	if n := len(globSuffix(dir, ".graph.yaml")); n > 0 {
		c, _ := o.GetInt("drg_fragments")
		o.Set("drg_fragments", c+n)
	}
	return o
}

// WritePackManifest is write_pack_manifest.
func WritePackManifest(dir string, packVersion, etag *string, sourceURL, sourceType string) {
	safe := StripSourceCredentials(sourceURL)
	fp := sha256.Sum256([]byte(safe))
	usesQuery := false
	if p, err := url.Parse(sourceURL); err == nil && p.RawQuery != "" {
		usesQuery = true
	}
	m := jsonx.NewObject("schema_version", "1", "generated_by", nil, "generated_at", nil, "source_url", safe,
		"source_type", sourceType, "fetched_at", clock.Now().UTC().Format("2006-01-02T15:04:05Z"))
	if packVersion != nil {
		m.Set("pack_version", *packVersion)
	} else {
		switch sourceType {
		case "api", "artifactory", "assemble", "git", "https":
			m.Set("pack_version", nil)
		}
	}
	if etag != nil {
		m.Set("etag", *etag)
	}
	m.Set("source_fingerprint", hex.EncodeToString(fp[:]))
	m.Set("source_uses_query", usesQuery)
	m.Set("snapshot_sha256", snapshotSHA256(dir))
	m.Set("artifact_counts", countSnapshotArtifacts(dir))
	presets := []any{}
	for _, f := range PresetFiles(dir) {
		b, _ := os.ReadFile(f)
		lf := strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n")
		sum := sha256.Sum256([]byte(lf))
		rel, _ := filepath.Rel(dir, f)
		presets = append(presets, jsonx.NewObject("name", stem(f), "path", filepath.ToSlash(rel), "content_hash", hex.EncodeToString(sum[:])))
	}
	if len(presets) > 0 {
		m.Set("presets", presets)
	}
	hashed := jsonx.NewObject()
	for _, kv := range m.Pairs() {
		switch kv.Key {
		case "generated_at", "generated_by":
			continue
		}
		hashed.Set(kv.Key, kv.Value)
	}
	sum := sha256.Sum256([]byte(canonicalYAML(hashed)))
	// field order (model order): schema_version, generated_by, generated_at,
	// source_url, source_type, fetched_at, pack_version, etag,
	// source_fingerprint, source_uses_query, snapshot_sha256,
	// artifact_counts, manifest_hash, constituents?, charter, presets?
	final := jsonx.NewObject()
	for _, kv := range m.Pairs() {
		if kv.Key == "presets" {
			continue
		}
		final.Set(kv.Key, kv.Value)
	}
	final.Set("manifest_hash", hex.EncodeToString(sum[:]))
	final.Set("charter", nil)
	if len(presets) > 0 {
		final.Set("presets", presets)
	}
	_ = os.WriteFile(filepath.Join(dir, "pack-manifest.yaml"), []byte(canonicalYAML(final)), 0o644)
}

// RenderAssemblyResult is render_assembly_result.
func RenderAssemblyResult(w io.Writer, r *AssemblyResult, out string, inputs []string, jsonMode bool) {
	if jsonMode {
		fmt.Fprintln(w, jsonx.Dumps(r.Object(), jsonx.Line))
		return
	}
	if r.OK {
		s := "s"
		if len(inputs) == 1 {
			s = ""
		}
		fmt.Fprintf(w, "Assembled %d pack%s → %s/ (%d artifacts)\n", len(inputs), s, out, r.ArtifactsWritten)
		for _, c := range r.Conflicts {
			fmt.Fprintf(w, "⚠ advisory: last-pack-wins for %s/%s from %s\n", c.ArtifactType, c.ArtifactID, c.Packs[len(c.Packs)-1])
		}
		return
	}
	for _, c := range r.Conflicts {
		q := make([]string, len(c.Packs))
		for i, n := range c.Packs {
			q[i] = dct.PyRepr(n)
		}
		fmt.Fprintf(w, "✗ Conflict: artifact id %s (%s) declared in %s\n", dct.PyRepr(c.ArtifactID), c.ArtifactType, strings.Join(q, " and "))
	}
	for _, e := range r.Errors {
		fmt.Fprintf(w, "✗ %s\n", e)
	}
	if len(r.Conflicts) > 0 {
		fmt.Fprintln(w, "Resolve conflicts and re-run, or use --force to let last pack win.")
	}
}
