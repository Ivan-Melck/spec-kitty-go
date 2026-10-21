package agentcore_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffMoveTaskCoord walks WP01/WP02 of the calc mission (coordination
// topology) through the move-task lifecycle and guard errors.
func TestDiffMoveTaskCoord(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP99", "--to", "planned", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP99", "--to", "planned", "--mission", "$M", "--agent", "claude"},
		{"agent", "tasks", "move-task", "WP01", "--to", "bogus-lane", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "bogus-lane", "--mission", "$M"},
		{"agent", "tasks", "move-task", "WP01", "--to", "planned", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--shell-pid", "4242", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--mission", "$M"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude"},
		{"agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--note", "ready now"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", "$M", "--agent", "rev"},
		{"agent", "tasks", "move-task", "WP01", "--to", "planned", "--mission", "$M", "--agent", "rev", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--mission", "$M", "--agent", "rev", "--json"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
	})
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("fb.md", "**Issue**: bad\n") })
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "planned", "--review-feedback-file", "fb.md", "--mission", "$M", "--agent", "rev", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "doing", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", "$M", "--agent", "rev", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "rev", "--note", "looks good"},
		{"agent", "tasks", "move-task", "WP01", "--to", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "done", "--mission", "$M", "--done-override-reason", "hotfix"},
		{"agent", "tasks", "move-task", "WP01", "--to", "planned", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP02", "--to", "blocked", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP02", "--to", "planned", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP02", "--to", "in_progress", "--mission", "$M", "--agent", "bob"},
		{"agent", "tasks", "move-task", "WP02", "--to", "for_review", "--mission", "$M", "--agent", "claude"},
		{"agent", "tasks", "move-task", "WP02", "--to", "canceled", "--mission", "$M", "--agent", "bob", "--json"},
		{"agent", "tasks", "move-task", "WP02", "--to", "planned", "--mission", "$M", "--json", "--force"},
		{"agent", "tasks", "move-task", "WP02", "--to", "in_progress", "--mission", "$M", "--json", "--force"},
		{"agent", "tasks", "move-task", "WP02", "--to", "in_progress", "--mission", "$M", "--json", "--force", "--note", "revive"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
	})
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	p.CompareTree(".kittify/derived", true)
	compareGit(t, p)
}

// TestDiffMoveTaskLanes covers the lanes (no coordination branch) topology
// where status commits land on the primary checkout, and the protected
// branch refusal.
func TestDiffMoveTaskLanes(t *testing.T) {
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) { s.Git("switch", "-q", "-c", "feat/x") })
	g, o := oracleCalcMission(t, p, "--topology", "lanes")
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "doing", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "doing", "--mission", "$M", "--agent", "claude", "--json", "--no-auto-commit"},
		{"agent", "tasks", "move-task", "WP01", "--to", "blocked", "--mission", "$M", "--agent", "claude", "--json", "--no-auto-commit"},
		{"agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--json"},
	})
	p.Both(func(s *testkit.Sandbox) { s.Git("switch", "-q", "main") })
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP02", "--to", "doing", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP02", "--to", "doing", "--mission", "$M"},
	})
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".kittify/derived", true)
	compareGit(t, p)
}
