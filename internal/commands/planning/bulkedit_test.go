package planning_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffBulkEditFinalizeGate(t *testing.T) {
	testkit.Oracle(t)
	p := &testkit.Pair{T: t, Go: missionfx.NewLocalMission(t), Oracle: missionfx.NewLocalMission(t)}
	p.Both(func(s *testkit.Sandbox) {
		file := filepath.Join("kitty-specs", missionfx.LocalSlug, "meta.json")
		meta, err := jsonx.DecodeObject([]byte(s.ReadFile(file)))
		if err != nil {
			t.Fatal(err)
		}
		meta.Set("change_mode", "bulk_edit")
		s.WriteFile(file, jsonx.Dumps(meta, jsonx.Pretty))
		s.Git("add", "--", file)
		s.Git("commit", "-qm", "mark bulk edit")
	})
	for _, group := range []string{"mission", "tasks"} {
		for _, jsonMode := range []bool{true, false} {
			args := []string{"agent", group, "finalize-tasks", "--mission", missionfx.LocalSlug, "--validate-only"}
			if jsonMode {
				args = append(args, "--json")
			}
			g, _ := p.Compare(true, args...)
			if g.Code != 1 {
				t.Fatalf("missing occurrence map must refuse: %d %s %s", g.Code, g.Stdout, g.Stderr)
			}
		}
	}
	for _, jsonMode := range []bool{true, false} {
		args := []string{"implement", "WP01", "--mission", missionfx.LocalSlug}
		if jsonMode {
			args = append(args, "--json")
		}
		g, _ := p.Compare(true, args...)
		if g.Code != 1 {
			t.Fatalf("missing occurrence map must refuse implementation: %d %s %s", g.Code, g.Stdout, g.Stderr)
		}
	}
	p.Both(func(s *testkit.Sandbox) {
		r := s.Run("agent", "tasks", "move-task", "WP01", "--to", "for_review", "--force", "--agent", "author", "--mission", missionfx.LocalSlug, "--json")
		if r.Code != 0 {
			t.Fatalf("seed review status: %d %s %s", r.Code, r.Stdout, r.Stderr)
		}
	})
	g, _ := p.Compare(true, "agent", "action", "review", "WP01", "--mission", missionfx.LocalSlug, "--agent", "reviewer")
	if g.Code != 1 {
		t.Fatalf("missing occurrence map must refuse review: %d %s %s", g.Code, g.Stdout, g.Stderr)
	}
	p.Both(func(s *testkit.Sandbox) {
		mapPath := filepath.Join("kitty-specs", missionfx.LocalSlug, "occurrence_map.yaml")
		s.WriteFile(mapPath, "target: {term: old-token, operation: rename}\ncategories:\n  code_symbols: {action: do_not_change}\n  import_paths: {action: rename}\n  filesystem_paths: {action: rename}\n  serialized_keys: {action: rename}\n  cli_commands: {action: rename}\n  user_facing_strings: {action: rename}\n  tests_fixtures: {action: rename}\n  logs_telemetry: {action: rename}\n")
		s.Git("add", "--", mapPath)
		s.Git("commit", "-qm", "classify occurrences")
		s.Git("branch", "kitty/mission-"+missionfx.LocalSlug, "HEAD")
		s.Git("checkout", "-qb", lanes.LaneBranchName(missionfx.LocalSlug, "lane-a", "feat/calc-ops"))
		s.WriteFile("src/calc.py", "# protected source changed\n")
		s.Git("add", "--", "src/calc.py")
		s.Git("commit", "-qm", "modify protected occurrence")
		s.Git("checkout", "-q", "feat/calc-ops")
	})
	g, _ = p.Compare(true, "agent", "action", "review", "WP01", "--mission", missionfx.LocalSlug, "--agent", "reviewer")
	if g.Code != 1 {
		t.Fatalf("protected diff must refuse review: %d %s %s", g.Code, g.Stdout, g.Stderr)
	}
	p.CompareTree("kitty-specs", true)
}
