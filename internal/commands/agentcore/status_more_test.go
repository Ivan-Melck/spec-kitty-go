package agentcore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestDiffStatusLeaves(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "status", "lifecycle", "--mission", "$M"},
		{"agent", "status", "lifecycle", "--mission", "$M", "--json"},
		{"agent", "status", "lifecycle", "--json"},
		{"agent", "status", "validate", "--mission", "$M"},
		{"agent", "status", "validate", "--mission", "$M", "--json"},
		{"agent", "status", "migrate", "--mission", "$M"},
		{"agent", "status", "migrate", "--json"},
		{"agent", "status", "reconcile", "--mission", "$M", "--json"},
		{"agent", "status", "reconcile", "--apply"},
		{"agent", "tasks", "move-task", "WP01", "--to", "doing", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "status", "lifecycle", "--mission", "$M"},
		{"agent", "status", "validate", "--mission", "$M", "--json"},
	})
	// drift: rewrite status.json lane by hand
	p.Both(func(s *testkit.Sandbox) {
		slug := missionSlug(t, s)
		f := filepath.Join(s.Dir, ".worktrees", slug+"-coord", "kitty-specs", slug, "status.json")
		b, _ := os.ReadFile(f)
		os.WriteFile(f, []byte(replaceOnce(string(b), `"lane": "in_progress"`, `"lane": "planned"`)), 0o644)
	})
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "status", "validate", "--mission", "$M"},
		{"agent", "status", "validate", "--mission", "$M", "--json"},
	})
	p.Both(func(s *testkit.Sandbox) {
		slug := missionSlug(t, s)
		f := filepath.Join(s.Dir, ".worktrees", slug+"-coord", "kitty-specs", slug, "status.events.jsonl")
		b, _ := os.ReadFile(f)
		os.WriteFile(f, append(b, []byte(`{"event_id": "bad", "wp_id": "WP01", "from_lane": "doing", "to_lane": "genesis", "at": "nope", "actor": "x", "force": "yes", "execution_mode": "cloud", "mission_slug": "m"}`+"\n")...), 0o644)
	})
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "status", "validate", "--mission", "$M", "--json"},
	})
}

func replaceOnce(s, old, nw string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + nw + s[i+len(old):]
		}
	}
	return s
}
