package agentcore_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffMoveTaskInvocationID covers CORE-06: move-task --invocation-id
// reads the dispatch Op record (kitty-ops/<id>.jsonl), validates its
// correlation (mission, WP, action: review for in_review/approved/done),
// rejects disagreeing --profile/--model, and stamps the resolved binding
// (profile, model, provider) on the claim / review annotations.
func TestDiffMoveTaskInvocationID(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	const okID, revID, badWP, revWrongWP = "01KAAAAAAAAAAAAAAAAAAAAAAA", "01KBBBBBBBBBBBBBBBBBBBBBBB", "01KCCCCCCCCCCCCCCCCCCCCCCC", "01KEEEEEEEEEEEEEEEEEEEEEEE"
	p.Both(func(s *testkit.Sandbox) {
		missionfx.WriteOpRecord(t, s, okID, "WP01", "implement", "implementer-ivan", "claude-opus-4-6")
		missionfx.WriteOpRecord(t, s, badWP, "WP02", "implement", "implementer-ivan", "")
		missionfx.WriteOpRecord(t, s, revID, "WP01", "review", "reviewer-renata", "")
		missionfx.WriteOpRecord(t, s, revWrongWP, "WP02", "review", "reviewer-renata", "gpt-5")
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "op records")
	})
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--invocation-id", "bogus", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--invocation-id", "01KDDDDDDDDDDDDDDDDDDDDDDD", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--invocation-id", badWP, "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--invocation-id", revID},
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--invocation-id", okID, "--profile", "reviewer-renata", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--invocation-id", okID, "--model", "other", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--invocation-id", okID, "--model", "claude-opus-4-6", "--shell-pid", "4242", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", "$M", "--agent", "rev", "--invocation-id", okID, "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", "$M", "--agent", "rev", "--invocation-id", revWrongWP, "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", "$M", "--agent", "rev", "--invocation-id", revID, "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "rev", "--invocation-id", revID, "--note", "ok"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
	})
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}

// TestDiffMoveTaskArbiterIssueMatrix covers DOCS-07's ordering: the
// OLD-timing arbiter persist fires before the issue-matrix guard, so an
// issue-matrix refusal still leaves the arbiter override on disk.
func TestDiffMoveTaskArbiterIssueMatrix(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionSlug(t, s)
		spec := "kitty-specs/" + slug + "/spec.md"
		s.WriteFile(spec, s.ReadFile(spec)+"\n## Issues\n\n- Implements #4321 (subtract).\n")
		s.Git("commit", "-qam", "issues")
		s.WriteFile("fb.md", "**Issue**: bad\n")
	})
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", "$M", "--agent", "rev", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "planned", "--review-feedback-file", "fb.md", "--mission", "$M", "--agent", "rev", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "arb", "--force", "--note", "[pre_existing_failure] flaky", "--json"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
	})
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}
