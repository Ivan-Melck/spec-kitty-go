package maintenance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func coordWorktree(t *testing.T, s *testkit.Sandbox) string {
	slug := missionfx.MissionSlug(t, s)
	m, _ := filepath.Glob(filepath.Join(s.Dir, ".worktrees", "*-coord"))
	if len(m) == 0 {
		t.Fatalf("no coord worktree for %s", slug)
	}
	return m[0]
}

func TestDiffDoctorCoordination(t *testing.T) {
	p := newOraclePair(t)
	compareSteps(t, p, "", "",
		step{"doctor", "coordination"},
		step{"doctor", "coordination", "--json"},
	)
	g, o := missionfx.OracleCalcMission(t, p)
	compareSteps(t, p, g, o,
		step{"doctor", "coordination", "--check-staleness"},
		step{"doctor", "coordination", "--mission", "$M", "--json"},
		step{"doctor", "coordination", "--mission", "nope"},
		step{"doctor", "coordination", "--mission", "nope", "--json"},
	)
	// dirty coord worktree + a never-created coordination branch
	p.Both(legacyCorpus)
	p.Both(func(s *testkit.Sandbox) {
		os.WriteFile(filepath.Join(coordWorktree(t, s), "scratch.txt"), []byte("x"), 0o644)
	})
	compareSteps(t, p, g, o,
		step{"doctor", "coordination"},
		step{"doctor", "coordination", "--json", "--check-staleness"},
	)
	// missing coord worktree (branch present) is materialized by --fix; the
	// never-created branch is flattened.
	p.Both(func(s *testkit.Sandbox) {
		wt := coordWorktree(t, s)
		s.Git("worktree", "remove", "--force", wt)
	})
	compareSteps(t, p, g, o,
		step{"doctor", "coordination", "--json"},
		step{"doctor", "coordination", "--fix"},
		step{"doctor", "coordination", "--json"},
	)
	p.CompareTree("kitty-specs", true)
}
