package charter

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// `charter pack regenerate-graph` support. The DRG extractor/calibrator is
// not ported: the regenerated fragment set is the shipped (embedded)
// built-in graph source plus its pack-manifest.yaml, which is exactly what
// upstream's regeneration yields for the unmodified shipped pack.

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

// shippedGraphSource returns {name: bytes} of the embedded graph source and
// the embedded pack-manifest.yaml.
func shippedGraphSource() (map[string][]byte, []byte) {
	bi := dct.BuiltIn()
	out := map[string][]byte{}
	for _, rel := range bi.Walk(".", ".graph.yaml", false) {
		if b, err := bi.ReadFile(rel); err == nil {
			out[filepath.Base(rel)] = b
		}
	}
	m, _ := bi.ReadFile("pack-manifest.yaml")
	return out, m
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

// BuiltInGraphIsFresh is the --check comparison.
func BuiltInGraphIsFresh(root string) bool {
	want, manifest := shippedGraphSource()
	got := readGraphSource(root)
	if len(want) != len(got) {
		return false
	}
	for k, v := range want {
		if !bytes.Equal(lfNormalise(v), lfNormalise(got[k])) {
			return false
		}
	}
	have, err := os.ReadFile(filepath.Join(root, "pack-manifest.yaml"))
	return err == nil && bytes.Equal(lfNormalise(have), lfNormalise(manifest))
}

// WriteBuiltInGraph writes the regenerated fragments (retiring a legacy
// graph.yaml monolith) and the built-in pack manifest under root.
func WriteBuiltInGraph(root string) error {
	want, manifest := shippedGraphSource()
	_ = os.Remove(filepath.Join(root, "graph.yaml"))
	m, _ := filepath.Glob(filepath.Join(root, "*.graph.yaml"))
	for _, f := range m {
		if _, ok := want[filepath.Base(f)]; !ok {
			_ = os.Remove(f)
		}
	}
	for name, b := range want {
		if err := os.WriteFile(filepath.Join(root, name), b, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(root, "pack-manifest.yaml"), manifest, 0o644)
}
