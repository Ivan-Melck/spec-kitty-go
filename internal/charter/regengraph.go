package charter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// `charter pack regenerate-graph` support: the DRG extractor port lives in
// regengraph_extract.go, the built-in pack manifest generator
// (charter.offering.packs.builtin_manifest) here.

// RequireBuiltInLevel is _require_built_in_level: ("", true) when the root
// carries a pack.yaml declaring pack_level: built-in.
func RequireBuiltInLevel(root string) (string, bool) {
	p := filepath.Join(root, "pack.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		return fmt.Sprintf("[red]--pack-root %s needs a valid pack.yaml declaring pack_level: built-in:[/red] [Errno 2] No such file or directory: %s", root, dct.PyRepr(p)), false
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return fmt.Sprintf("[red]--pack-root %s needs a valid pack.yaml declaring pack_level: built-in:[/red] %v", root, err), false
	}
	if msg := ValidatePackDescriptor(v); msg != "" {
		return fmt.Sprintf("[red]--pack-root %s needs a valid pack.yaml declaring pack_level: built-in:[/red] %s", root, msg), false
	}
	level := v.(*jsonx.Object).GetString("pack_level")
	if level != "built-in" {
		return fmt.Sprintf("[red]--pack-root only supports built-in-level packs;[/red] %s declares pack_level: %s", p, level), false
	}
	return "", true
}

func readGraphSource(dir string) map[string][]byte {
	out := map[string][]byte{}
	single := filepath.Join(dir, "graph.yaml")
	if st, err := os.Stat(single); err == nil && st.Mode().IsRegular() {
		b, _ := os.ReadFile(single)
		out["graph.yaml"] = b
		return out
	}
	m, _ := filepath.Glob(filepath.Join(dir, "*.graph.yaml"))
	sort.Strings(m)
	for _, f := range m {
		b, _ := os.ReadFile(f)
		out[filepath.Base(f)] = b
	}
	return out
}

func lfNormalise(b []byte) []byte {
	return []byte(strings.ReplaceAll(string(b), "\r\n", "\n"))
}

// RegenStatus is the outcome of a regenerate-graph run.
type RegenStatus struct {
	Status string // written | fresh | stale | invalid
	Detail string // "; ".join(DRGValidationError.errors) for invalid
}

// CheckBuiltInGraph is the --check path: regenerate in memory and compare
// the per-kind fragments (text, universal newlines) and the pack manifest
// (bytes) against the committed source under root.
func CheckBuiltInGraph(root string) (RegenStatus, error) {
	g, err := generateReferenceGraph(root)
	if err != nil {
		if v, ok := err.(*dct.DRGValidationError); ok {
			return RegenStatus{"invalid", strings.Join(v.Errors, "; ")}, nil
		}
		return RegenStatus{}, err
	}
	want := GraphFragments(g)
	got := readGraphSource(root)
	fresh := len(want) == len(got)
	if fresh {
		for k, v := range want {
			if string(lfNormaliseAll(got[k])) != v {
				fresh = false
				break
			}
		}
	}
	if fresh {
		have, err := os.ReadFile(filepath.Join(root, BuiltinManifestFile))
		if err != nil {
			fresh = false
		} else {
			m, err := BuildBuiltinManifest(root)
			if err != nil {
				return RegenStatus{}, err
			}
			fresh = bytes.Equal(have, []byte(m))
		}
	}
	if fresh {
		return RegenStatus{Status: "fresh"}, nil
	}
	return RegenStatus{Status: "stale"}, nil
}

// WriteBuiltInGraph regenerates the per-kind fragments under root (retiring
// stale fragments and a graph.yaml monolith) and then the pack manifest.
func WriteBuiltInGraph(root string) (RegenStatus, error) {
	g, err := generateReferenceGraph(root)
	if err != nil {
		if v, ok := err.(*dct.DRGValidationError); ok {
			return RegenStatus{"invalid", strings.Join(v.Errors, "; ")}, nil
		}
		return RegenStatus{}, err
	}
	frags := GraphFragments(g)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return RegenStatus{}, err
	}
	for name, text := range frags {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			return RegenStatus{}, err
		}
	}
	m, _ := filepath.Glob(filepath.Join(root, "*.graph.yaml"))
	for _, f := range m {
		if _, ok := frags[filepath.Base(f)]; !ok {
			_ = os.Remove(f)
		}
	}
	if st, err := os.Stat(filepath.Join(root, "graph.yaml")); err == nil && st.Mode().IsRegular() {
		_ = os.Remove(filepath.Join(root, "graph.yaml"))
	}
	text, err := BuildBuiltinManifest(root)
	if err != nil {
		return RegenStatus{}, err
	}
	if err := os.WriteFile(filepath.Join(root, BuiltinManifestFile), []byte(text), 0o644); err != nil {
		return RegenStatus{}, err
	}
	return RegenStatus{Status: "written"}, nil
}

// BuiltinManifestFile is builtin_manifest.MANIFEST_FILENAME.
const BuiltinManifestFile = "pack-manifest.yaml"

// BuildBuiltinManifest is dump_pack_manifest_bytes(build_builtin_manifest(root)).
func BuildBuiltinManifest(root string) (string, error) {
	type constituent struct{ kind, id, path, hash string }
	var cs []constituent
	for _, k := range dct.AllKinds {
		if !k.HasBuiltInContentDir() {
			continue
		}
		d := filepath.Join(root, k.Plural())
		if !isDirPath(d) {
			continue
		}
		idKey := "id"
		if k == dct.AgentProfile {
			idKey = "profile-id"
		}
		for _, p := range rglobFiles(d, suffixMatch(k.Suffix())) {
			raw, err := os.ReadFile(p)
			if err != nil {
				return "", err
			}
			v, err := yamlx.Parse(raw)
			if err != nil {
				return "", &RegenCrash{"ruamel.yaml.scanner.ScannerError", err.Error()}
			}
			id := ""
			if o, ok := v.(*jsonx.Object); ok {
				if x, ok := o.Get(idKey); ok {
					id = strings.TrimSpace(pyStrVal(x))
				}
			}
			if id == "" {
				return "", &RegenCrash{"ValueError", fmt.Sprintf("artifact file %s is missing its %s id field; cannot enumerate it as a pack constituent (fail-closed, no silent drop)", p, dct.PyRepr(idKey))}
			}
			rel, _ := filepath.Rel(root, p)
			sum := sha256.Sum256(lfNormaliseAll(raw))
			cs = append(cs, constituent{string(k), id, filepath.ToSlash(rel), hex.EncodeToString(sum[:])})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].kind != cs[j].kind {
			return cs[i].kind < cs[j].kind
		}
		return cs[i].id < cs[j].id
	})
	cl := []any{}
	for _, c := range cs {
		cl = append(cl, jsonx.NewObject("kind", c.kind, "id", c.id, "path", c.path, "content_hash", c.hash, "provenance_path", nil))
	}
	var presets []any
	for _, f := range PresetFiles(root) {
		b, _ := os.ReadFile(f)
		sum := sha256.Sum256(lfNormaliseAll(b))
		rel, _ := filepath.Rel(root, f)
		presets = append(presets, jsonx.NewObject("name", stem(f), "path", filepath.ToSlash(rel), "content_hash", hex.EncodeToString(sum[:])))
	}
	m := jsonx.NewObject("schema_version", "1", "generated_by", "spec-kitty charter pack regenerate-graph", "generated_at", nil,
		"source_url", nil, "source_type", "built-in", "fetched_at", nil, "manifest_hash", nil, "constituents", cl, "charter", nil)
	if presets != nil {
		m.Set("presets", presets)
	}
	hashed := jsonx.NewObject()
	for _, kv := range m.Pairs() {
		switch kv.Key {
		case "manifest_hash", "generated_at", "generated_by":
			continue
		}
		hashed.Set(kv.Key, kv.Value)
	}
	sum := sha256.Sum256([]byte(canonicalYAML(hashed)))
	m.Set("manifest_hash", hex.EncodeToString(sum[:]))
	return canonicalYAML(m), nil
}

func lfNormaliseAll(b []byte) []byte {
	return []byte(strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n"))
}
