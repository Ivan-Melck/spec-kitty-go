package orchestrator

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// oracleBoth runs an oracle command ($M = the side's mission slug) on both sides.
func oracleBoth(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		a := make([]string, len(args))
		for i, x := range args {
			a[i] = strings.ReplaceAll(x, "$M", slug)
		}
		missionfx.RunOracle(t, s, a...)
	})
}

func laneCommit(t *testing.T, s *testkit.Sandbox, lane string, files map[string]string, msg string) {
	t.Helper()
	wt := filepath.Join(".worktrees", "calc-ops-"+lane)
	var names []string
	for rel, body := range files {
		s.WriteFile(filepath.Join(wt, rel), body)
		names = append(names, rel)
	}
	if r := s.Exec(wt, "git", append([]string{"add"}, names...)...); r.Code != 0 {
		t.Fatalf("git add: %s", r.Stderr)
	}
	if r := s.Exec(wt, "git", "commit", "-q", "-m", msg); r.Code != 0 {
		t.Fatalf("git commit: %s", r.Stderr)
	}
}

// orchestratedApproval drives both WPs to approved through orchestrator-api
// (oracle on both sides).
func orchestratedApproval(t *testing.T, createArgs ...string) *testkit.Pair {
	t.Helper()
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p, createArgs...)
	work := []struct {
		wp, lane string
		subtasks []string
		files    map[string]string
	}{
		{"WP01", "lane-a", []string{"T001", "T002", "T003"}, map[string]string{
			"src/calc.py":            "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n",
			"tests/test_subtract.py": "from src.calc import subtract\n"}},
		{"WP02", "lane-b", []string{"T004", "T005"}, map[string]string{
			"src/calc_multiply.py":   "from src.calc import add\n\n\ndef multiply(a, b):\n    return a * b\n",
			"tests/test_multiply.py": "from src.calc_multiply import multiply\n"}},
	}
	for _, w := range work {
		oracleBoth(t, p, api, "start-implementation", "--mission", "calc-ops", "--wp", w.wp, "--actor", "claude", "--policy", testPolicy)
		p.Both(func(s *testkit.Sandbox) { laneCommit(t, s, w.lane, w.files, "feat("+w.wp+")") })
		oracleBoth(t, p, append(append([]string{"agent", "tasks", "mark-status"}, w.subtasks...), "--status", "done", "--mission", "$M")...)
		oracleBoth(t, p, api, "transition", "--mission", "calc-ops", "--wp", w.wp, "--to", "for_review", "--actor", "claude", "--policy", testPolicy)
		oracleBoth(t, p, api, "start-review", "--mission", "calc-ops", "--wp", w.wp, "--actor", "rev", "--policy", testPolicy)
		oracleBoth(t, p, api, "transition", "--mission", "calc-ops", "--wp", w.wp, "--to", "approved", "--actor", "rev", "--policy", testPolicy,
			"--review-result-json", `{"reviewer": "rev", "verdict": "approved", "reference": "r1"}`)
	}
	return p
}

// TestDiffAcceptAndConsolidateMission covers accept-mission (not-ready and
// accepted) and consolidate-mission (preflight refusals and the lane merge).
func TestDiffAcceptAndConsolidateMission(t *testing.T) {
	t.Parallel()
	p := orchestratedApproval(t)
	m := "calc-ops"
	step(t, p, api, "accept-mission", "--mission", m)
	step(t, p, api, "accept-mission", "--mission", m, "--actor", "rev")
	step(t, p, api, "accept-mission", "--mission", m, "--actor", "rev", "--origin-check", "nope")
	step(t, p, api, "consolidate-mission", "--mission", m, "--strategy", "octopus")
	step(t, p, api, "consolidate-mission", "--mission", m, "--target", "no-such-branch")
	goSlug, oSlug := missionfx.MissionSlug(t, p.Go), missionfx.MissionSlug(t, p.Oracle)
	step(t, p, api, "consolidate-mission", "--mission", m)
	compareGit(t, p, "after consolidate-mission")
	compareFiles(t, p, goSlug, oSlug,
		".worktrees/{slug}-coord/kitty-specs/{slug}/status.events.jsonl",
		".worktrees/{slug}-coord/kitty-specs/{slug}/status.json",
		"src/calc.py", "src/calc_multiply.py")
	step(t, p, api, "mission-state", "--mission", m)
	step(t, p, api, "consolidate-mission", "--mission", m)
}

// stepM is step with $M replaced by each side's mission slug.
func stepM(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	sub := func(s *testkit.Sandbox) []string {
		slug := missionfx.MissionSlug(t, s)
		out := make([]string, len(args))
		for i, a := range args {
			out[i] = strings.ReplaceAll(a, "$M", slug)
		}
		return out
	}
	g := p.Go.Run(sub(p.Go)...)
	o := p.Oracle.RunOracle(sub(p.Oracle)...)
	if g.Code != o.Code {
		t.Errorf("%s: exit code go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", strings.Join(args, " "), g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	if gs, os_ := normEnvelope(p.Go, g.Stdout), normEnvelope(p.Oracle, o.Stdout); gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
}

// TestDiffAcceptMissionAccepted accepts a natively driven mission (claims
// carry the agent) and consolidates it through orchestrator-api.
func TestDiffAcceptMissionAccepted(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p)
	oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--input-file", missionfx.AnalysisReport(), "--json")
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	p.Both(func(s *testkit.Sandbox) {
		wt := filepath.Join(".worktrees", missionfx.MissionSlug(t, s)+"-lane-a")
		s.WriteFile(filepath.Join(wt, "src/calc.py"), "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n")
		s.Exec(wt, "git", "add", "src/calc.py")
		s.Exec(wt, "git", "commit", "-q", "-m", "feat(WP01)")
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "ready")
	oracleBoth(t, p, "agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--note", "Review passed")
	oracleBoth(t, p, "agent", "action", "implement", "WP02", "--agent", "claude", "--mission", "$M")
	p.Both(func(s *testkit.Sandbox) {
		wt := filepath.Join(".worktrees", missionfx.MissionSlug(t, s)+"-lane-b")
		s.WriteFile(filepath.Join(wt, "src/calc_multiply.py"), "def multiply(a, b):\n    return a * b\n")
		s.Exec(wt, "git", "add", "src/calc_multiply.py")
		s.Exec(wt, "git", "commit", "-q", "-m", "feat(WP02)")
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T004", "T005", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "ready")
	oracleBoth(t, p, "agent", "action", "review", "WP02", "--agent", "claude", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "approved", "--mission", "$M", "--agent", "claude", "--note", "Review passed")
	stepM(t, p, api, "accept-mission", "--mission", "$M", "--actor", "rev")
	for _, fr := range []string{"FR-001", "FR-002"} {
		oracleBoth(t, p, "agent", "acceptance-verdict", "--mission", "$M", "--criterion", fr, "--result", "pass",
			"--verification-method", "automated_test", "--actor", "claude", "--json")
	}
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		s.WriteFile("docs/README.md", "# docs\n")
		s.WriteFile("tests/README.md", "# tests\n")
		s.WriteFile(filepath.Join("kitty-specs", slug, "contracts", "api.md"), "# api\n")
		s.Git("add", "docs", "tests", filepath.Join("kitty-specs", slug, "contracts"))
		s.Git("commit", "-q", "-m", "docs and contracts")
	})
	stepM(t, p, api, "accept-mission", "--mission", "$M", "--actor", "rev")
	goSlug, oSlug := missionfx.MissionSlug(t, p.Go), missionfx.MissionSlug(t, p.Oracle)
	compareFiles(t, p, goSlug, oSlug, "kitty-specs/{slug}/meta.json")
	compareGit(t, p, "after accept-mission")
	p.Both(func(s *testkit.Sandbox) {
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "record acceptance")
	})
	stepM(t, p, api, "consolidate-mission", "--mission", "$M", "--strategy", "squash")
	compareGit(t, p, "after squash consolidate-mission")
}

// TestDiffSingleBranchLoop drives a single_branch mission (repo-root
// planning lane) through the WP verbs.
func TestDiffSingleBranchLoop(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "single_branch")
	m := "calc-ops"
	step(t, p, api, "mission-state", "--mission", m)
	step(t, p, api, "resolve-workspace", "--mission", m, "--wp", "WP01")
	step(t, p, api, "start-implementation", "--mission", m, "--wp", "WP01", "--actor", "claude", "--policy", testPolicy)
	compareGit(t, p, "after single_branch claim")
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "for_review", "--actor", "claude", "--policy", testPolicy)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("src/calc.py", "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n")
		s.Git("add", "src/calc.py")
		s.Git("commit", "-q", "-m", "feat(WP01): subtract")
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M")
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "for_review", "--actor", "claude", "--policy", testPolicy)
	step(t, p, api, "start-review", "--mission", m, "--wp", "WP01", "--actor", "rev", "--policy", testPolicy)
	step(t, p, api, "append-history", "--mission", m, "--wp", "WP01", "--actor", "rev", "--note", "looks good")
	step(t, p, api, "mission-state", "--mission", m)
	compareGit(t, p, "after single_branch review")
}

// TestDiffLanesTopologyLoop runs the orchestrated loop on a lanes (no
// coordination branch) mission: a raw `--to claimed` transition, a forced
// review exit, a rework rewind and a refused consolidation.
func TestDiffLanesTopologyLoop(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	m := "calc-ops"
	step(t, p, api, "mission-state", "--mission", m)
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "claimed", "--actor", "claude", "--policy", testPolicy)
	compareGit(t, p, "after raw claim")
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "in_progress", "--actor", "claude", "--policy", testPolicy)
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "for_review", "--actor", "claude", "--policy", testPolicy, "--force", "--note", "nothing to commit")
	step(t, p, api, "start-review", "--mission", m, "--wp", "WP01", "--actor", "rev", "--policy", testPolicy, "--review-ref", "PR#7")
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "planned", "--actor", "rev", "--force")
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "planned", "--actor", "rev", "--force", "--note", "rework")
	step(t, p, api, "mission-state", "--mission", m)
	compareGit(t, p, "after rework")
	compareFiles(t, p, missionfx.MissionSlug(t, p.Go), missionfx.MissionSlug(t, p.Oracle), "kitty-specs/{slug}/status.events.jsonl")
	step(t, p, api, "consolidate-mission", "--mission", m, "--strategy", "rebase")
	compareGit(t, p, "after refused consolidate")
}
