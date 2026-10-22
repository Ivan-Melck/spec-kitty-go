package runtime_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// recordSnapshotPy records the mission's dossier snapshot with the upstream
// indexer (the sync dossier pipeline's writer).
const recordSnapshotPy = `import sys
from pathlib import Path
from specify_cli.dossier.indexer import Indexer
from specify_cli.dossier.manifest import ManifestRegistry
from specify_cli.dossier.snapshot import compute_snapshot, save_snapshot
from specify_cli.missions._read_path_resolver import candidate_feature_dir_for_mission
root = Path.cwd()
fd = candidate_feature_dir_for_mission(root, sys.argv[1])
save_snapshot(compute_snapshot(Indexer(ManifestRegistry(), repo_root=root).index_feature(fd, "software-dev")), fd)
`

// oraclePython is the interpreter named by the oracle script's shebang.
func oraclePython(t *testing.T) string {
	t.Helper()
	f, err := os.Open(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	if !strings.HasPrefix(line, "#!") {
		t.Skip("oracle is not a Python script")
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "#!"))
}

// TestDiffReconcileDossier records a snapshot with the upstream indexer, then
// reconciles: parity, churn immunity of runtime WP frontmatter fields, and
// each divergence kind (missing_in_projection, content_mismatch,
// missing_in_source, a schema-invalid WP failing closed).
func TestDiffReconcileDossier(t *testing.T) {
	py := oraclePython(t)
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	p.Both(func(s *testkit.Sandbox) {
		if r := s.Exec("", py, "-c", recordSnapshotPy, missionfx.MissionSlug(t, s)); r.Code != 0 {
			t.Fatalf("record snapshot: %s%s", r.Stdout, r.Stderr)
		}
	})
	rec := [][]string{{"reconcile", "--mission", "$M", "--json"}, {"reconcile", "--mission", "$M"}}
	steps(t, p, false, rec...)
	edit := func(rel string, fn func(string) string) {
		p.Both(func(s *testkit.Sandbox) {
			path := filepath.Join("kitty-specs", missionfx.MissionSlug(t, s), rel)
			s.WriteFile(path, fn(s.ReadFile(path)))
		})
	}
	// runtime-only frontmatter churn keeps parity
	edit("tasks/WP01-subtract-helper.md", func(b string) string {
		return strings.Replace(b, "assignee: \"\"", "assignee: \"bob\"", 1)
	})
	steps(t, p, false, rec...)
	edit("notes.md", func(string) string { return "# notes\n" })
	edit(".hidden.md", func(string) string { return "ignored\n" })
	edit("spec.md", func(b string) string { return b + "\nAddendum.\n" })
	edit("tasks/WP02-multiply.md", func(b string) string { return strings.Replace(b, "---\n", "---\nbogus: 1\n", 1) })
	p.Both(func(s *testkit.Sandbox) { os.Remove(filepath.Join(s.Dir, "kitty-specs", missionfx.MissionSlug(t, s), "plan.md")) })
	steps(t, p, false, rec...)
}
