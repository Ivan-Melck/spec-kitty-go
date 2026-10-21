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
	missionfx.OracleCalcMission(t, p)
	p.Both(legacyCorpus)
	for _, a := range [][]string{
		{"doctor", "mission-state", "--audit"},
		{"doctor", "mission-state", "--fix"},
		{"git", "status"},

		{"doctor", "mission-state", "--fix", "--json"},
		{"doctor", "mission-state", "--teamspace-dry-run"},
		{"doctor", "coordination", "--json"},
		{"doctor", "coordination"},
	} {
		r := p.Oracle.RunOracle(a...)
		if a[0] == "git" {
			r.Stdout = p.Oracle.Git("status", "--short")
		}
		t.Logf("%v exit=%d\n%s\n--stderr--\n%s", a, r.Code, r.Stdout, r.Stderr)
	}
}
