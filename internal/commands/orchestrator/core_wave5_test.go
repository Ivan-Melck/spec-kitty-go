package orchestrator

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

var reCalcMid8 = regexp.MustCompile(`calc-ops-[0-9A-Z]{8}`)

// TestDiffAmbiguousMissionHandle covers CORE-19: an ambiguous bare --mission
// handle propagates MissionSelectorAmbiguous out of every verb (traceback on
// stderr, empty stdout, exit 1).
func TestDiffAmbiguousMissionHandle(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.Git("switch", "-q", "-c", "feat/x")
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--topology", "lanes", "--json")
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--topology", "lanes", "--json", "--allow-duplicate")
	})
	verbs := [][]string{
		{"mission-state", "--mission", "calc-ops"},
		{"list-ready", "--mission", "calc-ops"},
		{"check-prerequisites", "--mission", "calc-ops"},
		{"start-implementation", "--mission", "calc-ops", "--wp", "WP01", "--actor", "a", "--policy", testPolicy},
		{"start-review", "--mission", "calc-ops", "--wp", "WP01", "--actor", "a", "--policy", testPolicy},
		{"transition", "--mission", "calc-ops", "--wp", "WP01", "--to", "claimed", "--actor", "a", "--policy", testPolicy},
		{"append-history", "--mission", "calc-ops", "--wp", "WP01", "--actor", "a", "--note", "n"},
		{"resolve-workspace", "--mission", "calc-ops", "--wp", "WP01"},
		{"accept-mission", "--mission", "calc-ops", "--actor", "a"},
		{"consolidate-mission", "--mission", "calc-ops"},
		{"record-analysis", "--mission", "calc-ops", "--policy", testPolicy},
		{"design-status", "--mission", "calc-ops"},
		{"artifact-read", "--mission", "calc-ops", "--kind", "spec"},
		{"next", "--mission", "calc-ops"},
		{"open-decision", "--mission", "calc-ops", "--origin", "specify", "--input-key", "k", "--question", "q", "--actor", "a", "--policy", testPolicy},
	}
	for _, v := range verbs {
		args := append([]string{"orchestrator-api"}, v...)
		g, o := step(t, p, args...)
		gl := reCalcMid8.ReplaceAllString(lastLine(g.Stderr), "calc-ops-<MID8>")
		ol := reCalcMid8.ReplaceAllString(lastLine(o.Stderr), "calc-ops-<MID8>")
		if gl != ol || !strings.HasPrefix(ol, "MissionSelectorAmbiguous: ") {
			t.Errorf("%s: final stderr line differs\n go: %s\n oracle: %s", v[0], gl, ol)
		}
	}
}
