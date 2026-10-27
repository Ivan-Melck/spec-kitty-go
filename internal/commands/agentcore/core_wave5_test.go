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

// TestDiffOrphanedEventWarnings covers CORE-02: a legacy lane row without
// mission_id whose mission_slug has no sibling meta.json logs the
// "(orphaned event)" warning once per read_events pass (human mode only).
func TestDiffOrphanedEventWarnings(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionSlug(t, s)
		f := ".worktrees/" + slug + "-coord/kitty-specs/" + slug + "/status.events.jsonl"
		s.WriteFile(f, s.ReadFile(f)+`{"actor": "x", "at": "2026-01-01T00:00:00+00:00", "event_id": "01JAAAAAAAAAAAAAAAAAAAAAAA", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_slug": "ghost-mission", "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "WP09"}`+"\n"+
			`{"actor": "x", "at": "2026-01-01T00:00:01+00:00", "event_id": "01JAAAAAAAAAAAAAAAAAAAAAAB", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_slug": "../evil", "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "WP08"}`+"\n")
	})
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "status", "validate", "--mission", "$M"},
		{"agent", "status", "validate", "--mission", "$M", "--json"},
		{"agent", "status", "lifecycle", "--mission", "$M"},
		{"agent", "status", "doctor", "--mission", "$M"},
		{"agent", "tasks", "list-tasks", "--mission", "$M"},
		{"agent", "tasks", "status", "--mission", "$M"},
		{"agent", "status", "materialize", "--mission", "$M"},
	})
}

// TestDiffStatusDoctorChecks covers CORE-03 (issue-matrix finding), CORE-04
// (sparse-checkout over lane/coord worktrees) and CORE-05 (the findings
// table wraps at the console width).
func TestDiffStatusDoctorChecks(t *testing.T) {
	for _, topo := range []string{"lanes", "coord"} {
		topo := topo
		t.Run(topo, func(t *testing.T) {
			t.Parallel()
			p := newOraclePair(t)
			var g, o string
			if topo == "lanes" {
				p.Both(func(s *testkit.Sandbox) { s.Git("switch", "-q", "-c", "feat/x") })
				g, o = oracleCalcMission(t, p, "--topology", "lanes")
			} else {
				g, o = oracleCalcMission(t, p)
			}
			p.Both(func(s *testkit.Sandbox) {
				slug := missionSlug(t, s)
				for _, dir := range []string{"kitty-specs/" + slug, ".worktrees/" + slug + "-coord/kitty-specs/" + slug} {
					if s.Exists(dir + "/spec.md") {
						s.WriteFile(dir+"/spec.md", s.ReadFile(dir+"/spec.md")+"\n## Issues\n\n- Implements #4321 (subtract).\n- Fixes #77 too.\n")
					}
				}
				s.Git("commit", "-qam", "issues")
			})
			comparePairSteps(t, p, g, o, [][]string{
				{"agent", "status", "doctor", "--mission", "$M"},
				{"agent", "status", "doctor", "--mission", "$M", "--json"},
			})
			oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--input-file", tdPath("calc/analysis-report.md"), "--json")
			oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
			p.Both(func(s *testkit.Sandbox) { s.Git("config", "core.sparseCheckout", "true") })
			comparePairSteps(t, p, g, o, [][]string{
				{"agent", "status", "doctor", "--mission", "$M"},
				{"agent", "status", "doctor", "--mission", "$M", "--json"},
			})
			p.Both(func(s *testkit.Sandbox) { s.Env = append(s.Env, "COLUMNS=120") })
			comparePairSteps(t, p, g, o, [][]string{
				{"agent", "status", "doctor", "--mission", "$M"},
			})
		})
	}
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

// TestDiffStatusDoctorIssueMatrixNotAuthored covers #6653: with the
// coordination worktree gone, the doctor reads the issue-matrix from the
// coordination branch, which holds none: a typed ISSUE_MATRIX_NOT_AUTHORED
// read error, surfaced as a warning finding.
func TestDiffStatusDoctorIssueMatrixNotAuthored(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionSlug(t, s)
		spec := "kitty-specs/" + slug + "/spec.md"
		s.WriteFile(spec, s.ReadFile(spec)+"\n## Issues\n\n- Implements #4321 (subtract).\n")
		s.Git("commit", "-qam", "issues")
		s.Git("worktree", "remove", "--force", ".worktrees/"+slug+"-coord")
	})
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "status", "doctor", "--mission", "$M", "--json"},
		{"agent", "status", "doctor", "--mission", "$M"},
	})
}
