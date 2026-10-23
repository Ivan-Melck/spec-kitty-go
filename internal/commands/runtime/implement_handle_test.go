package runtime_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffImplementMissionHandle: the top-level implement resolves --mission
// with resolve_mission_handle (identity ladder only), so a bare human slug
// that only names a composed <slug>-<mid8> directory is "No mission found",
// while the full slug and the mid8 resolve.
func TestDiffImplementMissionHandle(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
	})
	steps(t, p, true,
		[]string{"implement", "WP01", "--mission", "calc-ops"},
		[]string{"implement", "WP01", "--mission", "calc-ops", "--json"},
		[]string{"implement", "WP01", "--mission", "nope"},
		[]string{"implement", "WP01", "--mission", "$M"},
	)
}
