package maintenance_test

import (
	"os"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestExploreOracle prints oracle output for ad-hoc inspection
// (KITTY_EXPLORE=1).
func TestExploreOracle(t *testing.T) {
	if os.Getenv("KITTY_EXPLORE") == "" {
		t.Skip("exploration only")
	}
	p := newOraclePair(t)
	_, o := missionfx.OracleCalcMission(t, p)
	for _, a := range [][]string{
		{"doctor", "decisions", "--mission", o, "--json"},
		{"doctor", "decisions", "--mission", o},
		{"doctor", "mission-state", "--json"},
		{"doctor", "mission-state"},
		{"doctor", "coordination", "--json"},
		{"doctor", "coordination"},
	} {
		r := p.Oracle.RunOracle(a...)
		t.Logf("%v exit=%d\n%s\n--stderr--\n%s", a, r.Code, r.Stdout, r.Stderr)
	}
}
