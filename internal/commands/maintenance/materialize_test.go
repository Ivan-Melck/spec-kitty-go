package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffMaterialize(t *testing.T) {
	p := newOraclePair(t)
	compareSteps(t, p, "", "",
		step{"materialize"},
		step{"materialize", "--json"},
		step{"materialize", "--mission", "nope"},
		step{"materialize", "--mission", "nope", "--json"},
	)
	g, o := missionfx.OracleCalcMission(t, p)
	compareSteps(t, p, g, o,
		step{"materialize"},
		step{"materialize", "--json"},
		step{"materialize", "--mission", "$M"},
		step{"materialize", "--mission", "calc-ops", "--json"},
	)
	p.CompareTree(".kittify/derived", true)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	// a corrupt event log is reported per mission, others still processed
	writeBoth(p, "kitty-specs/legacy-001/meta.json", `{"slug": "legacy-001"}`+"\n")
	writeBoth(p, "kitty-specs/legacy-001/status.events.jsonl", "not json\n")
	writeBoth(p, "kitty-specs/plain/spec.md", "# x\n")
	compareSteps(t, p, g, o,
		step{"materialize", "--json"},
		step{"materialize"},
	)
	p.CompareTree(".kittify/derived", true)
}

func TestMaterializeWithoutOracle(t *testing.T) {
	s := missionfx.NewLocalMission(t)
	r := s.Run("materialize", "--json")
	if r.Code != 0 {
		t.Fatalf("exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	for _, f := range []string{"status.json", "board-summary.json", "progress.json", "lifecycle.json"} {
		if !s.Exists(join(".kittify/derived", missionfx.LocalSlug, f)) {
			t.Errorf("missing derived %s", f)
		}
	}
	r = s.Run("materialize", "--mission", "nope")
	if r.Code != 1 {
		t.Fatalf("unknown mission: exit %d", r.Code)
	}
	_ = testkit.Files
}
