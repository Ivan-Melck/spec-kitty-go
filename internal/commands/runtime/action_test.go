package runtime_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffImplementReviewLoop replays TRACE 5.F1, 5.F1b, 5.1, 5.3, 5.4,
// 5.8, 5.15, 5.19 and 9.x refusals: both sides run the Go / oracle
// implement and review claims, the oracle performs the move-task steps.
func TestDiffImplementReviewLoop(t *testing.T) {
	p := newAnalyzedPair(t)
	steps(t, p, true,
		[]string{"agent", "action", "implement", "WP02", "--agent", "claude", "--mission", "$M"},
		[]string{"implement", "WP02", "--mission", "$M", "--json"},
		[]string{"implement", "WP02", "--mission", "$M"},
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude"},
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "nope"},
		[]string{"implement", "WP01", "--mission", "nope"},
		[]string{"implement", "WP01", "--mission", "nope", "--json"},
		[]string{"implement", "WP09", "--mission", "$M", "--json"},
		[]string{"agent", "action", "implement", "WP09", "--agent", "claude", "--mission", "$M"},
		[]string{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"},
	)
	// 5.1 implement WP01 (lane allocation, claim, prompt)
	steps(t, p, false, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	compareTree(t, p, ".worktrees")
	compareTree(t, p, ".kittify", "skills-manifest.json")
	// resume (already in_progress)
	steps(t, p, false, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)

	laneCommit(t, p, "lane-a", "feat(WP01): subtract", map[string]string{
		"src/calc.py":            "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n",
		"tests/test_subtract.py": "from src.calc import subtract\n",
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Ready")
	// 5.3 review claim
	steps(t, p, false, []string{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	compareTree(t, p, ".worktrees")

	// F3 rejection (oracle) then 5.4 fix-mode implement
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("../fb1.md", "**Issue**: missing docstring\n") })
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "planned", "--review-feedback-file", "../fb1.md", "--mission", "$M", "--agent", "claude")
	steps(t, p, false, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	compareTree(t, p, ".worktrees")

	laneCommit(t, p, "lane-a", "fix(WP01): docstring", map[string]string{
		"src/calc.py": "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    \"\"\"Subtract.\"\"\"\n    return a - b\n",
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Fixed")
	// 5.8 review cycle 2
	steps(t, p, false, []string{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--note", "Review passed")

	// 5.15 implement WP02 (dependency lane merge)
	steps(t, p, false, []string{"agent", "action", "implement", "WP02", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	compareTree(t, p, ".worktrees")
	laneCommit(t, p, "lane-b", "feat(WP02): multiply", map[string]string{
		"src/calc_multiply.py":   "from src.calc import add\n\n\ndef multiply(a, b):\n    return a * b\n",
		"tests/test_multiply.py": "from src.calc_multiply import multiply\n",
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T004", "T005", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Ready")
	// 5.19 review WP02 (auto-detected first for_review WP)
	steps(t, p, false, []string{"agent", "action", "review", "--agent", "claude", "--mission", "$M"})
	compareGit(t, p)
	dropAnalysis(t, p)
	compareTree(t, p, "kitty-specs")
	compareTree(t, p, ".worktrees")
	compareTree(t, p, ".kittify", "skills-manifest.json")
}

// writeOpRecord writes a dispatch Op started record correlated with the
// side's mission (same id on both sides).
func writeOpRecord(t *testing.T, p *testkit.Pair, id, wpID, action, profile, model string) {
	writeOpRecordIn(t, p, "", id, wpID, action, profile, model)
}

// writeOpRecordIn is writeOpRecord into the checkout at dir (relative to
// the project; "" = the project itself).
func writeOpRecordIn(t *testing.T, p *testkit.Pair, dir, id, wpID, action, profile, model string) {
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		meta, err := jsonx.DecodeObject([]byte(s.ReadFile(filepath.Join("kitty-specs", slug, "meta.json"))))
		if err != nil {
			t.Fatal(err)
		}
		rec := jsonx.NewObject("event", "started", "invocation_id", id, "profile_id", profile, "action", action,
			"request_text", "implement "+wpID, "actor", "claude", "mode_of_work", "task_execution",
			"governance_context_hash", "0000000000000000", "governance_context_available", false,
			"started_at", "2026-01-01T00:00:00+00:00", "mission_id", meta.GetString("mission_id"), "wp_id", wpID)
		if model != "" {
			rec.Set("model_id", model)
		}
		s.WriteFile(filepath.Join(dir, "kitty-ops", id+".jsonl"), jsonx.Dumps(rec, jsonx.Options{Indent: -1, EnsureASCII: true})+"\n")
	})
}

// TestDiffActionInvocationIDLinkedCheckout: an Op opened from a linked
// checkout keeps its record there (Python main, #6564); the claim seam run
// from the repository root checkout finds it in that checkout.
func TestDiffActionInvocationIDLinkedCheckout(t *testing.T) {
	p := newAnalyzedPair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.Git("worktree", "add", "-q", "-b", "ops-x", filepath.Join(s.Base, "opsco"))
	})
	const okID = "01KAAAAAAAAAAAAAAAAAAAAAAA"
	writeOpRecordIn(t, p, "../opsco", okID, "WP01", "implement", "implementer-ivan", "claude-opus-4-6")
	steps(t, p, false,
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M", "--invocation-id", okID},
	)
	// Both sides must record the binding read from the linked checkout's Op.
	p.Both(func(s *testkit.Sandbox) {
		if r := s.Exec("", "grep", "-rl", "--exclude-dir=opsco", "claude-opus-4-6", s.Base); r.Code != 0 {
			t.Errorf("claim did not record the dispatched model (%s)", s.Dir)
		}
	})
	compareGit(t, p)
	dropAnalysis(t, p)
	compareTree(t, p, "kitty-specs")
}

// TestDiffActionInvocationID covers the dispatch→claim resolved binding:
// --model without --invocation-id, malformed/missing/mismatched Op
// records, and a correlated claim recording profile, model and provider.
func TestDiffActionInvocationID(t *testing.T) {
	p := newAnalyzedPair(t)
	const okID, wpID, badWP = "01KAAAAAAAAAAAAAAAAAAAAAAA", "01KBBBBBBBBBBBBBBBBBBBBBBB", "01KCCCCCCCCCCCCCCCCCCCCCCC"
	writeOpRecord(t, p, okID, "WP01", "implement", "implementer-ivan", "claude-opus-4-6")
	writeOpRecord(t, p, badWP, "WP02", "implement", "implementer-ivan", "")
	writeOpRecord(t, p, wpID, "WP01", "review", "reviewer-renata", "")
	steps(t, p, true,
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M", "--model", "claude-opus-4-6"},
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M", "--invocation-id", "bogus"},
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M", "--invocation-id", "01KDDDDDDDDDDDDDDDDDDDDDDD"},
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M", "--invocation-id", badWP},
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M", "--invocation-id", wpID},
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M", "--invocation-id", okID, "--profile", "reviewer-renata"},
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M", "--invocation-id", okID, "--model", "other"},
	)
	compareGit(t, p)
	steps(t, p, false,
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M", "--invocation-id", okID},
	)
	compareGit(t, p)
	dropAnalysis(t, p)
	compareTree(t, p, "kitty-specs")
}
