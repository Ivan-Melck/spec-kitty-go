package setup_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// treeEntry lists every file and directory under root with its permission
// bits ("0644 f path"), skipping .git.
func treeEntries(root string, ignore []string) []string {
	var out []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		for _, ig := range ignore {
			if strings.HasPrefix(rel, ig) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		kind := "f"
		if info.IsDir() {
			kind = "d"
		}
		out = append(out, fmt.Sprintf("%04o %s %s", info.Mode().Perm(), kind, rel))
		return nil
	})
	sort.Strings(out)
	return out
}

// compareDirs diffs modes/file sets and normalized contents of two dirs.
func compareDirs(t *testing.T, p *testkit.Pair, goDir, orDir string, ignore ...string) {
	t.Helper()
	ge, oe := treeEntries(goDir, ignore), treeEntries(orDir, ignore)
	if strings.Join(ge, "\n") != strings.Join(oe, "\n") {
		t.Errorf("tree %s differs (oracle vs go):\n%s", filepath.Base(goDir), testkit.Diff(strings.Join(oe, "\n"), strings.Join(ge, "\n")))
		return
	}
	for _, e := range ge {
		f := strings.SplitN(e, " ", 3)
		if f[1] != "f" {
			continue
		}
		gb, _ := os.ReadFile(filepath.Join(goDir, f[2]))
		ob, _ := os.ReadFile(filepath.Join(orDir, f[2]))
		gn, on := p.Go.Normalize(string(gb)), p.Oracle.Normalize(string(ob))
		if gn != on {
			t.Errorf("file %s differs:\n%s", f[2], testkit.Diff(on, gn))
		}
	}
}
