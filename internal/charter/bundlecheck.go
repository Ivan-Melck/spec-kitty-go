package charter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports charter.bundle.validate_synthesis_state and the
// provenance checks of `charter bundle validate`.

// directWriteKinds is DIRECT_WRITE_KINDS with their file suffixes.
var directWriteKinds = [][2]string{{"directive", ".directive.yaml"}, {"tactic", ".tactic.yaml"}, {"styleguide", ".styleguide.yaml"},
	{"procedure", ".procedure.yaml"}, {"agent_profile", ".agent.yaml"}}

func kindSuffix(kind string) string {
	for _, k := range directWriteKinds {
		if k[0] == kind {
			return k[1]
		}
	}
	return ""
}

func kindAndSlug(p string) (string, string) {
	name := filepath.Base(p)
	for _, k := range directWriteKinds {
		if strings.HasSuffix(name, k[1]) {
			base := strings.TrimSuffix(name, k[1])
			if k[0] == "directive" {
				if a, b, ok := strings.Cut(base, "-"); ok && a != "" && strings.IndexFunc(a, func(r rune) bool { return !unicode.IsDigit(r) }) < 0 {
					return k[0], b
				}
			}
			return k[0], base
		}
	}
	return "", ""
}

func rglobSuffix(dir, suffix string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), suffix) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// BundleValidationResult is charter.bundle.BundleValidationResult.
type BundleValidationResult struct {
	Present          bool
	Errors, Warnings []string
}

// Passed reports whether no errors were recorded.
func (r *BundleValidationResult) Passed() bool { return len(r.Errors) == 0 }

// manifestPayload is SynthesisManifest.model_dump(mode="python") (raw
// field subset when legacy=true).
func manifestHashOK(raw *jsonx.Object, stored string) (bool, string) {
	full := &jsonx.Object{}
	defaults := []struct {
		k string
		v any
	}{{"schema_version", "3"}, {"mission_id", nil}, {"created_at", nil}, {"run_id", nil}, {"adapter_id", nil}, {"adapter_version", nil},
		{"synthesizer_version", nil}, {"artifacts", []any{}}, {"built_in_only", false}, {"bundle_content_hash", nil}}
	for _, d := range defaults {
		if v, ok := raw.Get(d.k); ok {
			full.Set(d.k, v)
		} else {
			full.Set(d.k, d.v)
		}
	}
	h := func(o *jsonx.Object) string {
		sum := sha256.Sum256([]byte(canonicalYAML(o)))
		return hex.EncodeToString(sum[:])
	}
	computed := h(full)
	if computed == stored {
		return true, computed
	}
	subset := &jsonx.Object{}
	for _, kv := range full.Pairs() {
		if raw.Has(kv.Key) {
			subset.Set(kv.Key, kv.Value)
		}
	}
	if h(subset) == stored {
		return true, computed
	}
	return false, computed
}

// ValidateSynthesisState is charter.bundle.validate_synthesis_state.
func ValidateSynthesisState(root string) *BundleValidationResult {
	r := &BundleValidationResult{}
	staging := Path(root, ".kittify/charter/.staging")
	if ents, err := os.ReadDir(staging); err == nil {
		for _, e := range ents {
			if e.IsDir() && strings.HasSuffix(e.Name(), ".failed") {
				r.Warnings = append(r.Warnings, fmt.Sprintf("Stale failed staging directory found: %s (inspect cause.yaml, then remove to suppress this warning)", filepath.Join(".kittify/charter/.staging", e.Name())))
			}
		}
	}
	pack := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	prov := Path(root, ".kittify/charter/provenance")
	mp := Path(root, ManifestPath)
	type cand struct{ base, leaf string }
	cands := []cand{{prov, "provenance"}}
	for _, k := range []string{"directive", "styleguide", "tactic"} {
		cands = append(cands, cand{filepath.Join(pack, k), k})
	}
	for _, c := range cands {
		dd := filepath.Join(c.base, c.leaf)
		var files []string
		filepath.WalkDir(dd, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				files = append(files, p)
			}
			return nil
		})
		sort.Strings(files)
		for _, f := range files {
			rel, _ := filepath.Rel(root, f)
			r.Errors = append(r.Errors, fmt.Sprintf("Doubled-leaf synthesis artifact detected: '%s' — a path-join wrote into '%s/%s/' instead of the canonical '%s/' (#3819).", rel, c.leaf, c.leaf, c.leaf))
		}
	}
	var artifacts []string
	for _, k := range directWriteKinds {
		artifacts = append(artifacts, rglobSuffix(pack, k[1])...)
	}
	var provFiles []string
	if ents, err := os.ReadDir(prov); err == nil {
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".yaml") {
				provFiles = append(provFiles, filepath.Join(prov, e.Name()))
			}
		}
	}
	sort.Strings(provFiles)
	if len(artifacts) == 0 && len(provFiles) == 0 && !exists(mp) {
		return r
	}
	m, merr := (*synthesisManifest)(nil), error(nil)
	if exists(mp) {
		m, merr = loadManifest(mp)
	}
	r.Present = true
	if m != nil && m.builtInOnly && len(m.artifacts) == 0 {
		return r
	}
	byPath, byProv := map[string]manifestEntry{}, map[string]manifestEntry{}
	if m != nil {
		for _, e := range m.artifacts {
			byPath[e.path], byProv[e.provenancePath] = e, e
		}
	}
	sort.Strings(artifacts)
	for _, a := range artifacts {
		rel, _ := filepath.Rel(root, a)
		if e, ok := byPath[filepath.ToSlash(rel)]; ok {
			if !exists(filepath.Join(root, filepath.FromSlash(e.provenancePath))) {
				r.Errors = append(r.Errors, fmt.Sprintf("Artifact '%s' has no provenance sidecar (expected: %s)", rel, e.provenancePath))
			}
			continue
		}
		k, s := kindAndSlug(a)
		if k == "" {
			continue
		}
		exp := filepath.Join(prov, k+"-"+s+".yaml")
		if !exists(exp) {
			er, _ := filepath.Rel(root, exp)
			r.Errors = append(r.Errors, fmt.Sprintf("Artifact '%s' has no provenance sidecar (expected: %s)", rel, er))
		}
	}
	for _, pf := range provFiles {
		rel, _ := filepath.Rel(root, pf)
		if e, ok := byProv[filepath.ToSlash(rel)]; ok {
			if !exists(filepath.Join(root, filepath.FromSlash(e.path))) {
				r.Errors = append(r.Errors, fmt.Sprintf("Provenance sidecar '%s' references non-existent artifact (kind=%s, slug=%s)", rel, e.kind, e.slug))
			}
			continue
		}
		stem := strings.TrimSuffix(filepath.Base(pf), ".yaml")
		k, s, ok := strings.Cut(stem, "-")
		if !ok {
			r.Errors = append(r.Errors, "Provenance file has unexpected name format: "+rel)
			continue
		}
		if kindSuffix(k) == "" {
			r.Errors = append(r.Errors, fmt.Sprintf("Provenance file has unknown kind '%s': %s", k, rel))
			continue
		}
		found := false
		for _, c := range rglobSuffix(pack, kindSuffix(k)) {
			if ck, cs := kindAndSlug(c); ck == k && cs == s {
				found = true
				break
			}
		}
		if !found {
			r.Errors = append(r.Errors, fmt.Sprintf("Provenance sidecar '%s' references non-existent artifact (kind=%s, slug=%s)", rel, k, s))
		}
	}
	if exists(mp) {
		if merr != nil {
			r.Errors = append(r.Errors, "Could not load synthesis manifest: "+merr.Error())
			return r
		}
		if err := verifyManifest(m, root); err != nil {
			r.Errors = append(r.Errors, "Synthesis manifest integrity check failed: "+err.Error())
		}
		raw := readYAMLObject(mp)
		stored := raw.GetString("manifest_hash")
		if ok, computed := manifestHashOK(raw, stored); !ok {
			r.Errors = append(r.Errors, fmt.Sprintf("Synthesis manifest self-hash mismatch: manifest_hash mismatch (stored %s..., computed %s...)", first12(stored), first12(computed)))
		}
	}
	return r
}

func first12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ProvenanceValidationErrors is _collect_provenance_validation_errors.
func ProvenanceValidationErrors(root string) []string {
	var errs []string
	prov := Path(root, ".kittify/charter/provenance")
	var files []string
	if ents, err := os.ReadDir(prov); err == nil {
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".yaml") {
				files = append(files, e.Name())
			}
		}
	}
	sort.Strings(files)
	for _, n := range files {
		b, _ := os.ReadFile(filepath.Join(prov, n))
		v, err := yamlx.Parse(b)
		if err != nil {
			errs = append(errs, n+": could not parse YAML: "+err.Error())
			continue
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			errs = append(errs, n+": provenance sidecar must be a YAML mapping")
			continue
		}
		if err := ValidateProvenanceEntry(o, false); err != nil {
			errs = append(errs, n+": "+err.Error())
		}
	}
	mp := Path(root, ManifestPath)
	b, err := os.ReadFile(mp)
	if err != nil {
		return errs
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return append(errs, "synthesis-manifest.yaml: could not parse YAML: "+err.Error())
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return append(errs, "synthesis-manifest.yaml: synthesis manifest must be a YAML mapping")
	}
	av, has := o.Get("artifacts")
	if !has {
		return errs
	}
	arts, isList := av.([]any)
	if !isList {
		return append(errs, "synthesis-manifest.yaml: artifacts must be a list")
	}
	for _, a := range arts {
		ao, isObj := a.(*jsonx.Object)
		if !isObj {
			errs = append(errs, "synthesis-manifest.yaml: artifact entries must be YAML mappings")
			continue
		}
		pr := ao.GetString("provenance_path")
		if pr == "" {
			continue
		}
		if !exists(filepath.Join(root, filepath.FromSlash(pr))) {
			slug := ao.GetString("slug")
			if slug == "" {
				slug = "?"
			}
			errs = append(errs, fmt.Sprintf("Missing provenance sidecar for artifact '%s': %s", slug, pr))
		}
	}
	return errs
}

// BundleCompatibilityMessage returns the incompatibility message ("" when compatible).
func BundleCompatibilityMessage(root string) string {
	if err := AssertBundleCompatible(root); err != nil {
		return err.Error()
	}
	return ""
}
