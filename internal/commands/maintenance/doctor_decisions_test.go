package maintenance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffDoctorDecisions(t *testing.T) {
	p := newOraclePair(t)
	g, o := missionfx.OracleCalcMission(t, p)
	compareSteps(t, p, g, o,
		step{"doctor", "decisions", "--mission", "$M"},
		step{"doctor", "decisions", "--mission", "$M", "--json"},
		step{"doctor", "decisions", "--mission", "nope"},
		step{"doctor", "decisions", "--mission", "nope", "--json"},
	)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		missionfx.RunOracle(t, s, "agent", "decision", "open", "--mission", slug, "--flow", "plan", "--input-key", "db", "--question", "Which DB?", "--step-id", "s1", "--options", `["pg","sqlite"]`)
		r := missionfx.RunOracle(t, s, "agent", "decision", "open", "--mission", slug, "--flow", "plan", "--input-key", "cache", "--question", "Cache?", "--step-id", "s2")
		id := between(r.Stdout, `"decision_id": "`, `"`)
		missionfx.RunOracle(t, s, "agent", "decision", "resolve", id, "--mission", slug, "--final-answer", "redis")
	})
	compareSteps(t, p, g, o,
		step{"doctor", "decisions", "--mission", "$M"},
		step{"doctor", "decisions", "--mission", "$M", "--json"},
	)
	// break the index: drop it entirely, then repair
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		os.Remove(filepath.Join(s.Dir, "kitty-specs", slug, "decisions", "index.json"))
	})
	compareSteps(t, p, g, o,
		step{"doctor", "decisions", "--mission", "$M", "--json"},
		step{"doctor", "decisions", "--mission", "$M", "--repair"},
		step{"doctor", "decisions", "--mission", "$M", "--json"},
	)
	p.CompareTree("kitty-specs", true)
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return ""
	}
	return s[:j]
}
