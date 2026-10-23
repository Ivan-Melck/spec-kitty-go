package planning_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffBulkEditFinalizeGate(t *testing.T) {
	testkit.Oracle(t)
	p := &testkit.Pair{T: t, Go: missionfx.NewLocalMission(t), Oracle: missionfx.NewLocalMission(t)}
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/migration-ledger.jsonl", "")
		file := filepath.Join("kitty-specs", missionfx.LocalSlug, "meta.json")
		meta, err := jsonx.DecodeObject([]byte(s.ReadFile(file)))
		if err != nil {
			t.Fatal(err)
		}
		meta.Set("change_mode", "bulk_edit")
		s.WriteFile(file, jsonx.Dumps(meta, jsonx.Pretty))
		s.Git("add", "--", ".kittify/migration-ledger.jsonl", file)
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
	p.CompareTree("kitty-specs", true)
}
