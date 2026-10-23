package consolidate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

var wpFileIDRe = regexp.MustCompile(`^(WP\d{2})`)

// dependencyGraph is build_dependency_graph over the PRIMARY tasks/ dir.
// Each WP is read through read_wp_frontmatter, which re-reads the event log
// of the WP's own mission dir (the PRIMARY copy) for its runtime fields: an
// unreadable or corrupt log there fails the graph with the store's text.
func dependencyGraph(primaryDir string) (map[string][]string, error) {
	graph := map[string][]string{}
	tasks := filepath.Join(primaryDir, "tasks")
	if _, err := os.Stat(tasks); err != nil {
		return graph, nil
	}
	files, _ := filepath.Glob(filepath.Join(tasks, "WP*.md"))
	sort.Strings(files)
	for _, f := range files {
		m := wpFileIDRe.FindStringSubmatch(filepath.Base(f))
		if m == nil {
			continue
		}
		w, err := wp.Read(f)
		if err != nil {
			return nil, err
		}
		if _, err := status.ReadStream(primaryDir, ""); err != nil {
			return nil, err
		}
		id := m[1]
		if fid := w.String("work_package_id"); fid != "" && fid != id {
			return nil, fmt.Errorf("WP ID mismatch: filename %s vs frontmatter %s in %s", id, fid, f)
		}
		graph[id] = w.Dependencies()
	}
	return graph, nil
}
