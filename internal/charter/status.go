package charter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
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

// This file ports the `charter status` collectors
// (specify_cli.cli.commands.charter._status_collectors), the evidence
// orchestrator (charter.activation.evidence) and the freshness computer
// (specify_cli.charter_runtime.freshness.computer).

// ManifestPath is synthesizer.manifest.MANIFEST_PATH.
const ManifestPath = ".kittify/charter/synthesis-manifest.yaml"

// BundleCompatError is _assert_bundle_compatible's TaskCliError.
type BundleCompatError struct{ Msg string }

func (e *BundleCompatError) Error() string { return e.Msg }

// AssertBundleCompatible is _assert_bundle_compatible(.kittify/charter).
func AssertBundleCompatible(root string) error {
	var ver *int
	if b := LoadBundle(root); b != nil {
		if m := b.GetObject("metadata"); m != nil {
			if v, ok := m.Get("bundle_schema_version"); ok {
				if i, isInt := v.(int); isInt {
					ver = &i
				}
			}
		}
	}
	switch {
	case ver == nil:
		return &BundleCompatError{"Bundle schema version not found; treating as v1. Run `spec-kitty upgrade`."}
	case *ver == 2:
		return nil
	case *ver >= 1 && *ver < 2:
		return &BundleCompatError{fmt.Sprintf("Bundle schema version %d requires migration. Run `spec-kitty upgrade`.", *ver)}
	case *ver < 1:
		return &BundleCompatError{fmt.Sprintf("Bundle schema version %d predates the earliest supported version (1). Contact support.", *ver)}
	}
	return &BundleCompatError{fmt.Sprintf("Bundle schema version %d is newer than this CLI supports (2). Upgrade your CLI.", *ver)}
}

func displayPath(p, root string) string {
	if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

// hashContent is charter.hasher.hash_content.
func hashContent(content string) string {
	c := strings.TrimPrefix(content, "\ufeff")
	c = strings.ReplaceAll(strings.ReplaceAll(c, "\r\n", "\n"), "\r", "\n")
	sum := sha256.Sum256([]byte(strings.TrimSpace(c)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// CollectCharterSync is _collect_charter_sync_status.
func CollectCharterSync(root string) *jsonx.Object {
	outDir := Path(root, Dir)
	var charterPath string
	if exists(Path(root, CharterYAML)) {
		charterPath = filepath.Join(outDir, "charter.md")
		if !exists(charterPath) {
			charterPath = filepath.Join(outDir, "charter.yaml")
		}
	} else {
		charterPath = Path(root, CharterMD)
		if !exists(charterPath) {
			return jsonx.NewObject("available", false, "error", "Charter not found at "+charterPath+"\n  Run 'spec-kitty charter interview' to create one,\n  or 'spec-kitty upgrade' if migrating from an older version.")
		}
	}
	metaPath := filepath.Join(outDir, "metadata.yaml")
	stale, current, stored := !exists(filepath.Join(outDir, "charter.yaml")), "", ""
	var lastSync any
	if exists(metaPath) {
		b, _ := os.ReadFile(charterPath)
		current = hashContent(string(b))
		meta := readYAMLObject(metaPath)
		if meta != nil {
			if v, ok := meta.Get("charter_hash"); ok && v != nil {
				stored = pyStrOf(v)
			}
			for _, k := range []string{"timestamp_utc", "extracted_at"} {
				if v, ok := meta.Get(k); ok && v != nil && v != "" && v != false {
					lastSync = pyStrOf(v)
					break
				}
			}
		}
		stale = current != stored
	}
	files := []any{}
	for _, n := range []string{"charter.yaml", "charter.md"} {
		st, err := os.Stat(filepath.Join(outDir, n))
		if err == nil {
			files = append(files, jsonx.NewObject("name", n, "exists", true, "size_kb", float64(st.Size())/1024))
		} else {
			files = append(files, jsonx.NewObject("name", n, "exists", false, "size_kb", 0.0))
		}
	}
	lib := 0
	if ents, err := os.ReadDir(filepath.Join(outDir, "library")); err == nil {
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".md") {
				lib++
			}
		}
	}
	status := "synced"
	if stale {
		status = "stale"
	}
	return jsonx.NewObject("available", true, "charter_path", displayPath(charterPath, root), "status", status,
		"current_hash", current, "stored_hash", stored, "last_sync", lastSync, "library_docs", lib, "files", files)
}

// CollectGovernanceReferences is _collect_governance_reference_status.
func CollectGovernanceReferences(root string) *jsonx.Object {
	sel := LoadSelection(root)
	refs, warns := []any{}, []any{}
	for _, s := range CollectGovernanceReferenceStatus(root, sel.GovernanceReferences) {
		refs = append(refs, s.Object())
		if s.HasWarning {
			warns = append(warns, s.Warning)
		}
	}
	return jsonx.NewObject("available", true, "references", refs, "warnings", warns)
}

func countGlob(dir, suffix string) int {
	n := 0
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), suffix) {
				n++
			}
		}
	}
	return n
}

func countRGlob(dir, suffix string) int {
	n := 0
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), suffix) {
			n++
		}
		return nil
	})
	return n
}

type manifestEntry struct{ kind, slug, path, provenancePath, contentHash string }

type synthesisManifest struct {
	runID, createdAt, adapterID, adapterVersion string
	builtInOnly                                 bool
	bundleContentHash                           string
	artifacts                                   []manifestEntry
}

// loadManifest is synthesizer.manifest.load_yaml (required-field checks only).
func loadManifest(p string) (*synthesisManifest, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	raw, err := yamlx.Parse(b)
	if err != nil {
		return nil, err
	}
	if ve := validatePyd(synthesisManifestModel, raw, true); ve != nil {
		return nil, ve
	}
	o := raw.(*jsonx.Object)
	m := &synthesisManifest{}
	m.runID, m.createdAt = pyStrOf(mustGetAny(o, "run_id")), pyStrOf(mustGetAny(o, "created_at"))
	m.adapterID, m.adapterVersion = pyStrOf(mustGetAny(o, "adapter_id")), pyStrOf(mustGetAny(o, "adapter_version"))
	if v, ok := o.Get("built_in_only"); ok {
		m.builtInOnly, _ = v.(bool)
	}
	if v, ok := o.Get("bundle_content_hash"); ok && v != nil {
		m.bundleContentHash = pyStrOf(v)
	}
	for _, x := range o.GetList("artifacts") {
		e, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		m.artifacts = append(m.artifacts, manifestEntry{e.GetString("kind"), e.GetString("slug"), e.GetString("path"), e.GetString("provenance_path"), e.GetString("content_hash")})
	}
	return m, nil
}

func validateManifestPath(raw, field, prefix string) error {
	p := filepath.ToSlash(raw)
	bad := filepath.IsAbs(p)
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			bad = true
		}
	}
	if bad {
		return fmt.Errorf("%s must be repo-relative and stay under %s: %s", field, prefix, raw)
	}
	if p != prefix && !strings.HasPrefix(p, prefix+"/") {
		return fmt.Errorf("%s must be under %s: %s", field, prefix, raw)
	}
	return nil
}

// verifyManifest is synthesizer.manifest.verify.
func verifyManifest(m *synthesisManifest, root string) error {
	for _, e := range m.artifacts {
		if err := validateManifestPath(e.path, "manifest artifact path", dct.ProjectPackRoot); err != nil {
			return err
		}
		if err := validateManifestPath(e.provenancePath, "manifest provenance path", ".kittify/charter/provenance"); err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.path)))
		sum := sha256.Sum256(b)
		if err != nil || hex.EncodeToString(sum[:]) != e.contentHash {
			return fmt.Errorf("Manifest integrity error in %s: artifact %s is missing or has been modified.", ManifestPath, e.path)
		}
	}
	return nil
}

func liveArtifactCount(root string) int {
	pr := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	n := 0
	for _, k := range []dct.Kind{dct.Directive, dct.Tactic, dct.Styleguide, dct.Procedure, dct.AgentProfile} {
		n += countRGlob(filepath.Join(pr, dct.ProjectKindDir(k)), k.Suffix())
	}
	return n
}

func collectManifestStatus(root string) (*jsonx.Object, *synthesisManifest) {
	mp := Path(root, ManifestPath)
	live := liveArtifactCount(root)
	liveProv := countGlob(Path(root, ".kittify/charter/provenance"), ".yaml")
	base := func(exists bool, state string, count int, m *synthesisManifest, missing []any, errV any) *jsonx.Object {
		var runID, created, aid, aver any
		if m != nil {
			runID, created, aid, aver = m.runID, m.createdAt, m.adapterID, m.adapterVersion
		}
		return jsonx.NewObject("path", displayPath(mp, root), "exists", exists, "state", state, "artifact_count", count,
			"live_artifact_count", live, "live_provenance_count", liveProv, "run_id", runID, "created_at", created,
			"adapter_id", aid, "adapter_version", aver, "missing_provenance_paths", missing, "error", errV)
	}
	if !exists(mp) {
		state := "missing"
		if live > 0 || liveProv > 0 {
			state = "partial"
		}
		return base(false, state, 0, nil, []any{}, nil), nil
	}
	m, err := loadManifest(mp)
	if err != nil {
		return base(true, "invalid", 0, nil, []any{}, "manifest could not be parsed: "+err.Error()), nil
	}
	state, errV := "valid", any(nil)
	if verr := verifyManifest(m, root); verr != nil {
		state, errV = "invalid", verr.Error()
	}
	missing := []any{}
	for _, e := range m.artifacts {
		if !exists(filepath.Join(root, filepath.FromSlash(e.provenancePath))) {
			missing = append(missing, e.provenancePath)
		}
	}
	return base(true, state, len(m.artifacts), m, missing, errV), m
}

func collectProvenanceStatus(root string, m *synthesisManifest, includeEntries bool) *jsonx.Object {
	pr := Path(root, ".kittify/charter/provenance")
	var paths []string
	if ents, err := os.ReadDir(pr); err == nil {
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".yaml") {
				paths = append(paths, filepath.Join(pr, e.Name()))
			}
		}
	}
	sort.Strings(paths)
	visible := map[string]bool{}
	for _, p := range paths {
		visible[displayPath(p, root)] = true
	}
	manifestPaths := map[string]bool{}
	if m != nil {
		for _, e := range m.artifacts {
			manifestPaths[e.provenancePath] = true
		}
	}
	warnings, entries := []any{}, []any{}
	corpus, adapters := map[string]bool{}, map[string]bool{}
	for _, p := range paths {
		rel := displayPath(p, root)
		b, rerr := os.ReadFile(p)
		raw, perr := yamlx.Parse(b)
		if rerr != nil || perr != nil {
			msg := "invalid provenance sidecar"
			if perr != nil {
				msg = perr.Error()
			}
			warnings = append(warnings, rel+": "+msg)
			continue
		}
		if err := ValidateProvenanceEntry(raw, true); err != nil {
			warnings = append(warnings, rel+": "+err.Error())
			continue
		}
		o := raw.(*jsonx.Object)
		if s := o.GetString("corpus_snapshot_id"); s != "" {
			corpus[s] = true
		}
		adapters[pyStrOf(mustGetAny(o, "adapter_id"))+"@"+pyStrOf(mustGetAny(o, "adapter_version"))] = true
		if includeEntries {
			get := func(k string) any { v, _ := o.Get(k); return v }
			entries = append(entries, jsonx.NewObject("path", rel, "kind", get("artifact_kind"), "slug", get("artifact_slug"),
				"artifact_urn", get("artifact_urn"), "adapter_id", get("adapter_id"), "adapter_version", get("adapter_version"),
				"synthesizer_version", get("synthesizer_version"), "produced_at", get("produced_at"),
				"corpus_snapshot_id", get("corpus_snapshot_id"), "evidence_bundle_hash", get("evidence_bundle_hash"), "generated_at", get("generated_at")))
		}
	}
	var missing []string
	for p := range manifestPaths {
		if !visible[p] {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	return jsonx.NewObject("path", displayPath(pr, root), "count", len(paths), "parsed_count", len(paths)-len(warnings),
		"manifest_artifact_count", len(manifestPaths), "missing_for_manifest_count", len(missing), "missing_for_manifest", strs(missing),
		"corpus_snapshot_ids", strs(dct.SortedKeys(corpus)), "adapters", strs(dct.SortedKeys(adapters)), "warnings", warnings, "entries", entries)
}

// ------------------------------------------------------------- evidence

var (
	evidenceExcludedDirs = map[string]bool{"node_modules": true, ".venv": true, "venv": true, "env": true, "__pycache__": true,
		".git": true, "dist": true, "build": true, ".worktrees": true, ".mypy_cache": true, ".tox": true}
	languageIndicators = [][2]string{{"pyproject.toml", "python"}, {"setup.py", "python"}, {"setup.cfg", "python"},
		{"requirements.txt", "python"}, {"go.mod", "go"}, {"Cargo.toml", "rust"}, {"pom.xml", "java"}, {"build.gradle", "java"},
		{"Gemfile", "ruby"}, {"composer.json", "php"}, {"package.json", "javascript"}}
	frameworkIndicators = [][2]string{{"manage.py", "django"}, {"next.config.ts", "nextjs"}, {"next.config.js", "nextjs"},
		{"angular.json", "angular"}, {"svelte.config.js", "svelte"}, {"svelte.config.ts", "svelte"}, {"nuxt.config.ts", "nuxt"}, {"nuxt.config.js", "nuxt"}}
	testFrameworkIndicators = [][2]string{{"pytest.ini", "pytest"}, {"conftest.py", "pytest"}, {"jest.config.js", "jest"},
		{"jest.config.ts", "jest"}, {"vitest.config.ts", "vitest"}, {"vitest.config.js", "vitest"}}
)

// CodeSignals is synthesizer.evidence.CodeSignals (the summarised parts).
type CodeSignals struct {
	StackID, PrimaryLanguage  string
	Frameworks, TestFramework []string
	Representative            []string
}

func isTestFile(rel, name string) bool {
	n := strings.ToLower(name)
	if strings.HasPrefix(n, "test_") && strings.HasSuffix(n, ".py") || strings.HasSuffix(n, "_test.py") {
		return true
	}
	for _, s := range []string{".test.ts", ".spec.ts", ".test.js", ".spec.js"} {
		if strings.HasSuffix(n, s) {
			return true
		}
	}
	for _, p := range strings.Split(rel, "/") {
		if p == "tests" || p == "__tests__" {
			return true
		}
	}
	return false
}

// readDirUnsorted lists a directory in on-disk order (os.scandir order).
func readDirUnsorted(dir string) ([]os.DirEntry, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// CollectCodeSignals is CodeReadingCollector(root).collect().
func CollectCodeSignals(root string) CodeSignals {
	indicators := map[string]bool{}
	var src, tests []string
	ts, js := 0, 0
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		if depth > 3 {
			return
		}
		ents, err := readDirUnsorted(dir)
		if err != nil {
			return
		}
		var dirs []string
		for _, e := range ents {
			full := filepath.Join(dir, e.Name())
			isDir := e.IsDir()
			if e.Type()&os.ModeSymlink != 0 {
				if st, err := os.Stat(full); err == nil && st.IsDir() {
					isDir = true
				}
			}
			if isDir {
				if !evidenceExcludedDirs[e.Name()] && e.Type()&os.ModeSymlink == 0 {
					dirs = append(dirs, e.Name())
				}
				continue
			}
			name := e.Name()
			rp := name
			if rel != "" {
				rp = rel + "/" + name
			}
			indicators[name] = true
			switch {
			case strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx"):
				ts++
			case strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".jsx") || strings.HasSuffix(name, ".mjs"):
				js++
			}
			if isTestFile(rp, name) {
				tests = append(tests, rp)
			} else {
				src = append(src, rp)
			}
		}
		for _, d := range dirs {
			r := d
			if rel != "" {
				r = rel + "/" + d
			}
			walk(filepath.Join(dir, d), r, depth+1)
		}
	}
	walk(root, "", 0)
	lang := "unknown"
	if indicators["package.json"] {
		lang = "javascript"
		if indicators["tsconfig.json"] || (ts+js > 0 && float64(ts)/float64(ts+js) > 0.5) {
			lang = "typescript"
		}
	} else {
		for _, li := range languageIndicators {
			if li[0] != "package.json" && indicators[li[0]] {
				lang = li[1]
				break
			}
		}
	}
	var fws, tfs []string
	for _, f := range frameworkIndicators {
		if indicators[f[0]] && !contains(fws, f[1]) {
			fws = append(fws, f[1])
		}
	}
	for _, f := range testFrameworkIndicators {
		if indicators[f[0]] && !contains(tfs, f[1]) {
			tfs = append(tfs, f[1])
		}
	}
	if len(tfs) == 0 && lang == "python" {
		for _, t := range tests {
			if contains(strings.Split(t, "/"), "tests") {
				tfs = append(tfs, "pytest")
				break
			}
		}
	}
	stack := "unknown"
	if lang != "unknown" {
		parts := []string{lang}
		if len(fws) > 0 {
			parts = append(parts, fws[0])
		}
		if len(tfs) > 0 {
			parts = append(parts, tfs[0])
		}
		stack = strings.Join(parts, "+")
	}
	rep := append(append([]string{}, src[:min(5, len(src))]...), tests[:min(5, len(tests))]...)
	return CodeSignals{StackID: stack, PrimaryLanguage: lang, Frameworks: fws, TestFramework: tfs, Representative: rep}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ConfigShapeError is evidence.orchestrator.ConfigShapeError.
type ConfigShapeError struct{ Msg string }

func (e *ConfigShapeError) Error() string { return e.Msg }

// LoadURLListFromConfig is load_url_list_from_config.
func LoadURLListFromConfig(root string) ([]string, error) {
	p := dct.ConfigPath(root)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, nil
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, nil
	}
	if v == nil {
		return nil, nil
	}
	cfg, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &ConfigShapeError{fmt.Sprintf("%s must be a YAML mapping at the top level; found %s instead. Repair the file (it should look like `charter: ...`) or restore it from version control.", p, pyTypeName(v))}
	}
	cc, _ := cfg.Get("charter")
	co, _ := cc.(*jsonx.Object)
	if co == nil {
		return nil, nil
	}
	si := co.GetObject("synthesis_inputs")
	if si == nil {
		return nil, nil
	}
	var out []string
	for _, u := range si.GetList("url_list") {
		if s, ok := u.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// LoadCorpus is CorpusLoader().load(profile_key): (snapshot id, entry count, found).
func LoadCorpus(profileKey string) (string, int, bool) {
	primary, _, _ := strings.Cut(profileKey, "+")
	for _, c := range []string{primary, "generic"} {
		b, err := assets.ReadFile("charter/activation/corpus/" + c + ".corpus.yaml")
		if err != nil {
			continue
		}
		o, err := yamlx.ParseObject(b)
		if err != nil {
			return "", 0, false
		}
		return pyStrOf(mustGetAny(o, "snapshot_id")), len(o.GetList("entries")), true
	}
	return "", 0, false
}

// SummarizeEvidence is _summarize_evidence.
func SummarizeEvidence(root string) (*jsonx.Object, error) {
	urls, err := LoadURLListFromConfig(root)
	if err != nil {
		return nil, err
	}
	cs := CollectCodeSignals(root)
	code := jsonx.NewObject("stack_id", cs.StackID, "primary_language", cs.PrimaryLanguage, "frameworks", strs(cs.Frameworks),
		"test_frameworks", strs(cs.TestFramework), "representative_files_count", len(cs.Representative),
		"representative_files_preview", strs(cs.Representative[:min(5, len(cs.Representative))]))
	warnings := []any{}
	var snap any
	count := 0
	if id, n, ok := LoadCorpus(cs.StackID); ok {
		snap, count = id, n
	} else {
		warnings = append(warnings, "No corpus found for profile '"+cs.StackID+"' or 'generic'; synthesis proceeds without corpus evidence.")
	}
	return jsonx.NewObject("warnings", warnings, "code", code, "configured_urls", strs(urls), "configured_url_count", len(urls),
		"corpus_snapshot_id", snap, "corpus_entry_count", count), nil
}

// CollectSynthesis is _collect_synthesis_status.
func CollectSynthesis(root string, includeProvenance bool) (*jsonx.Object, error) {
	in := Path(root, ".kittify/charter/generated")
	counts := jsonx.NewObject("directive", countGlob(filepath.Join(in, "directives"), ".yaml"),
		"tactic", countGlob(filepath.Join(in, "tactics"), ".yaml"), "styleguide", countGlob(filepath.Join(in, "styleguides"), ".yaml"))
	total := 0
	for _, kv := range counts.Pairs() {
		total += kv.Value.(int)
	}
	gen := jsonx.NewObject("path", displayPath(in, root), "exists", exists(in), "counts", counts, "total", total)
	ms, m := collectManifestStatus(root)
	ps := collectProvenanceStatus(root, m, includeProvenance)
	ev, err := SummarizeEvidence(root)
	if err != nil {
		return nil, err
	}
	missingCount, _ := ps.Get("missing_for_manifest_count")
	warns := ps.GetList("warnings")
	state := ms.GetString("state")
	gs := "not_started"
	switch {
	case state == "valid" && missingCount == 0 && len(warns) == 0:
		gs = "promoted"
	case state == "invalid" || state == "partial" || missingCount.(int) > 0 || len(warns) > 0:
		gs = "needs_attention"
	case total > 0:
		gs = "ready_for_validation"
	}
	return jsonx.NewObject("generation_state", gs, "generated_inputs", gen, "manifest", ms, "provenance", ps, "evidence", ev), nil
}

// CollectOrgLayer is _collect_org_layer_status: the loaded org fragments
// (artifact-derived nodes and augmentation edges included), the merge's
// collision records and hard failures, and the dangling endpoints of the
// complete (shipped built-in + every pack) merged graph.
func CollectOrgLayer(root string) *jsonx.Object {
	errs := []any{}
	res := jsonx.NewObject("has_built_in", true, "packs", []any{}, "collision_warnings", []any{}, "errors", errs)
	frags, err := LoadOrgDRG(root)
	if err != nil {
		if _, ok := err.(*OrgPackMissingError); ok {
			errs = append(errs, err.Error())
		} else {
			errs = append(errs, "org-DRG load error: "+err.Error())
		}
		res.Set("errors", errs)
		return res
	}
	packs := []any{}
	for _, f := range frags {
		packs = append(packs, jsonx.NewObject("name", f.PackName, "source_kind", f.SourceKind, "source_ref", f.SourceRef,
			"layer_index", f.LayerIndex, "fetched", true, "node_count", len(f.Nodes), "edge_count", len(f.Edges)))
	}
	res.Set("packs", packs)
	if len(frags) == 0 {
		return res
	}
	var merged *MergedGraph
	bi, err := BuiltInMergedGraph()
	if err == nil {
		merged, err = MergeThreeLayers(bi, frags, nil)
	}
	if err != nil {
		if ce, ok := err.(*OrgConflictError); ok {
			cw := []any{}
			for _, c := range ce.Conflicts {
				layers := []any{}
				for _, l := range c.ConflictingLayers {
					layers = append(layers, l)
				}
				cw = append(cw, jsonx.NewObject("kind", c.Kind, "target_id", c.TargetID, "conflicting_layers", layers, "resolution", c.ResolutionApplied))
			}
			res.Set("collision_warnings", cw)
			for _, m := range ce.HardFailureMessages() {
				errs = append(errs, m)
			}
		} else {
			errs = append(errs, "org-layer merge check failed: "+err.Error())
		}
		merged = nil
	}
	if merged != nil {
		for _, m := range ValidateDanglingReferences(merged) {
			errs = append(errs, m)
		}
	}
	res.Set("errors", errs)
	return res
}

// ------------------------------------------------------------ freshness

func mtimeISO(p string) any {
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	return isoFromTime(st.ModTime())
}

// isoFromTime is datetime.fromtimestamp(st_mtime, tz=UTC).isoformat().
func isoFromTime(t time.Time) string {
	f := float64(t.UnixNano()) / 1e9
	sec := math.Floor(f)
	us := math.RoundToEven((f - sec) * 1e6)
	if us >= 1e6 {
		sec++
		us = 0
	}
	u := time.Unix(int64(sec), int64(us)*1000).UTC()
	if us == 0 {
		return u.Format("2006-01-02T15:04:05") + "+00:00"
	}
	return u.Format("2006-01-02T15:04:05.000000") + "+00:00"
}

var legacyBundleFiles = []string{"governance.yaml", "directives.yaml", "metadata.yaml", "references.yaml"}

func legacyPresent(root string) []string {
	var out []string
	for _, n := range legacyBundleFiles {
		if exists(filepath.Join(Path(root, Dir), n)) {
			out = append(out, n)
		}
	}
	return out
}

func missingCharterSourceDetail(root string) string {
	present := legacyPresent(root)
	if exists(Path(root, CharterMD)) {
		present = append([]string{"charter.md"}, present...)
	}
	if len(present) == 1 {
		return "no charter.yaml, but a legacy charter bundle file (" + present[0] + ") is present; this project has a charter, just not in the required form"
	}
	if len(present) > 1 {
		return "no charter.yaml, but legacy charter bundle files (" + strings.Join(present, "/") + ") are present; this project has a charter, just not in the required form"
	}
	return "no charter.yaml and no legacy charter bundle files; this project has no charter at all"
}

func subState(state string, last, rem, detail any) *jsonx.Object {
	return jsonx.NewObject("state", state, "last_change", last, "remediation", rem, "detail", detail)
}

var schemaVersionRe = regexp.MustCompile(`^(\d+)\.\d+\.\d+$`)

// ComputeFreshness is compute_freshness(root).to_dict().
func ComputeFreshness(root string) *jsonx.Object {
	yp := Path(root, CharterYAML)
	var src *jsonx.Object
	if !exists(yp) {
		rem := "spec-kitty charter generate --no-from-interview"
		if len(legacyPresent(root)) > 0 {
			rem = "spec-kitty upgrade --yes"
		}
		src = subState("missing", nil, rem, missingCharterSourceDetail(root))
	} else {
		last := mtimeISO(yp)
		b, _ := os.ReadFile(yp)
		v, err := yamlx.Parse(b)
		o, isObj := v.(*jsonx.Object)
		if v == nil && err == nil {
			o, isObj = &jsonx.Object{}, true
		}
		switch {
		case err != nil || !isObj:
			src = subState("invalid", last, nil, "charter.yaml exists but cannot be parsed; `charter generate` needs it to already parse — restore from VCS or hand-repair, then re-run")
		default:
			sv, _ := o.Get("schema_version")
			s, isStr := sv.(string)
			m := schemaVersionRe.FindStringSubmatch(s)
			if o.Len() == 0 || !isStr || m == nil || m[1] != "2" {
				src = subState("invalid", last, nil, "charter.yaml exists and parses as YAML but does not match the charter bundle contract (empty mapping, or a `schema_version` this install does not support); `charter generate` needs it to already be a valid bundle to merge into — restore from VCS or hand-repair, then re-run")
			} else {
				src = subState("fresh", last, nil, nil)
			}
		}
	}
	var bundle *jsonx.Object
	if !exists(yp) {
		rem := "spec-kitty charter generate --no-from-interview"
		if len(legacyPresent(root)) > 0 {
			rem = "spec-kitty upgrade --yes"
		}
		d, _ := src.Get("detail")
		bundle = subState("missing", nil, rem, d)
	} else if src.GetString("state") != "fresh" {
		bundle = subState("stale", mtimeISO(yp), nil, "synced bundle mirrors charter_source, which is unparseable; the parse error must be fixed there first — restore charter.yaml from VCS or hand-repair, then re-run")
	} else {
		bundle = subState("fresh", mtimeISO(yp), nil, nil)
	}
	return jsonx.NewObject("charter_source", src, "synced_bundle", bundle, "synthesized_drg", computeSynthesizedDRG(root, bundle))
}

func computeSynthesizedDRG(root string, bundle *jsonx.Object) *jsonx.Object {
	mp := Path(root, ManifestPath)
	pr := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	gp := filepath.Join(pr, "graph.yaml")
	var m *synthesisManifest
	if exists(mp) {
		m, _ = loadManifest(mp)
	}
	if m != nil && m.builtInOnly {
		if exists(gp) {
			return subState("built_in_only", mtimeISO(gp), nil, "stale graph residue: graph.yaml present but the synthesis manifest declares built_in_only; the manifest is authoritative, the residual graph.yaml is ignored")
		}
		return subState("built_in_only", mtimeISO(mp), nil, nil)
	}
	if !exists(gp) {
		seed := filepath.Join(pr, "PROVENANCE.md")
		if b, err := os.ReadFile(seed); err == nil {
			l := strings.ToLower(string(b))
			if strings.Contains(l, "fresh project seed") && strings.Contains(l, "llm-authored yaml") && strings.Contains(l, "built-in doctrine") {
				return subState("built_in_only", mtimeISO(seed), nil, "legacy fresh-project seed marker; re-run `spec-kitty charter synthesize` to write synthesis-manifest.yaml")
			}
		}
		return subState("missing", nil, "spec-kitty charter synthesize", nil)
	}
	gm := mtimeISO(gp)
	if bundle.GetString("state") != "fresh" {
		return subState("stale", gm, "spec-kitty charter synthesize", nil)
	}
	cur := BundleContentHash(root)
	if m == nil || m.bundleContentHash == "" || cur == "" || m.bundleContentHash != cur {
		return subState("stale", gm, "spec-kitty charter synthesize", nil)
	}
	return subState("fresh", gm, nil, nil)
}

// BundleContentHash is charter.bundle.compute_bundle_content_hash ("" for None).
func BundleContentHash(root string) string {
	b, err := os.ReadFile(Path(root, CharterYAML))
	if err != nil {
		return ""
	}
	return hashContent(hashContent(string(b)))
}
