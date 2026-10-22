package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// nativeApproval drives the calc mission's two WPs to approved with the
// native verbs (oracle on both sides), records the acceptance verdicts and
// commits docs/contracts so accept-mission's readiness checks pass.
func nativeApproval(t *testing.T, createArgs ...string) *testkit.Pair {
	t.Helper()
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p, createArgs...)
	oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--input-file", missionfx.AnalysisReport(), "--json")
	work := []struct {
		wp, lane, file, body string
		subtasks             []string
	}{
		{"WP01", "lane-a", "src/calc.py", "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n", []string{"T001", "T002", "T003"}},
		{"WP02", "lane-b", "src/calc_multiply.py", "def multiply(a, b):\n    return a * b\n", []string{"T004", "T005"}},
	}
	for _, w := range work {
		oracleBoth(t, p, "agent", "action", "implement", w.wp, "--agent", "claude", "--mission", "$M")
		p.Both(func(s *testkit.Sandbox) {
			wt := filepath.Join(".worktrees", missionfx.MissionSlug(t, s)+"-"+w.lane)
			s.WriteFile(filepath.Join(wt, w.file), w.body)
			s.Exec(wt, "git", "add", w.file)
			s.Exec(wt, "git", "commit", "-q", "-m", "feat("+w.wp+")")
		})
		oracleBoth(t, p, append(append([]string{"agent", "tasks", "mark-status"}, w.subtasks...), "--status", "done", "--mission", "$M")...)
		oracleBoth(t, p, "agent", "tasks", "move-task", w.wp, "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "ready")
		oracleBoth(t, p, "agent", "action", "review", w.wp, "--agent", "claude", "--mission", "$M")
		oracleBoth(t, p, "agent", "tasks", "move-task", w.wp, "--to", "approved", "--mission", "$M", "--agent", "claude", "--note", "Review passed")
	}
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
	return p
}

// staleOrigin publishes the target and lane-a branches to a bare origin,
// then a teammate moves origin's status log and lane-a ahead of this clone.
func staleOrigin(t *testing.T, p *testkit.Pair) {
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		lane := "kitty/mission-" + slug + "-lane-a"
		bare := filepath.Join(s.Base, "origin.git")
		if r := s.Exec("", "git", "init", "-q", "--bare", bare); r.Code != 0 {
			t.Fatal(r.Stderr)
		}
		s.Git("remote", "add", "origin", bare)
		if r := s.Exec("", "git", "push", "-q", "origin", "feat/calc-ops", lane); r.Code != 0 {
			t.Fatal(r.Stderr)
		}
		clone := filepath.Join(s.Base, "teammate")
		if r := s.Exec("", "git", "clone", "-q", "-b", "feat/calc-ops", bare, clone); r.Code != 0 {
			t.Fatal(r.Stderr)
		}
		log := filepath.Join(clone, "kitty-specs", slug, "status.events.jsonl")
		f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("\n")
		f.Close()
		s.Exec(clone, "git", "commit", "-q", "-am", "teammate status")
		if r := s.Exec(clone, "git", "push", "-q", "origin", "feat/calc-ops"); r.Code != 0 {
			t.Fatal(r.Stderr)
		}
		tree := strings.TrimSpace(s.Git("rev-parse", lane+"^{tree}"))
		parent := strings.TrimSpace(s.Git("rev-parse", lane))
		c := s.Exec("", "git", "commit-tree", tree, "-p", parent, "-m", "teammate lane work")
		if c.Code != 0 {
			t.Fatal(c.Stderr)
		}
		if r := s.Exec("", "git", "push", "-q", "origin", strings.TrimSpace(c.Stdout)+":refs/heads/"+lane); r.Code != 0 {
			t.Fatal(r.Stderr)
		}
	})
}

// orchGatePolicy writes a merge-gate policy whose risk gate fails (parallel
// lanes with overlapping parent directories, threshold 0.1).
func orchGatePolicy(t *testing.T, p *testkit.Pair, mode string) {
	p.Both(func(s *testkit.Sandbox) {
		cfg := s.ReadFile(".kittify/config.yaml")
		if i := strings.Index(cfg, "\npolicy:"); i >= 0 {
			cfg = cfg[:i+1]
		}
		cfg = strings.TrimRight(cfg, "\n") + "\npolicy:\n  merge_gates:\n    mode: " + mode + "\n  risk:\n    threshold: 0.1\n"
		s.WriteFile(".kittify/config.yaml", cfg)
		lj := filepath.Join("kitty-specs", missionfx.MissionSlug(t, s), "lanes.json")
		m, err := jsonx.DecodeObject([]byte(s.ReadFile(lj)))
		if err != nil {
			t.Fatal(err)
		}
		for i, l := range m.GetList("lanes") {
			lo := l.(*jsonx.Object)
			if lo.GetString("lane_id") == "lane-planning" {
				continue
			}
			lo.Set("parallel_group", 0)
			lo.Set("write_scope", []any{[]string{"src/pkg/a.py", "src/pkg/b.py"}[i%2]})
		}
		s.WriteFile(lj, jsonx.Dumps(m, jsonx.Options{Indent: 2, EnsureASCII: true})+"\n")
		s.Git("add", ".kittify/config.yaml", lj)
		s.Git("commit", "-q", "-m", "gate policy "+mode)
	})
}

// lastLine is the final non-empty stderr line (the oracle may print
// interpreter warnings before it).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

// step2M is stepM returning both results.
func step2M(t *testing.T, p *testkit.Pair, args ...string) (testkit.Result, testkit.Result) {
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
	if os.Getenv("KITTY_TEST_TRACE") == "1" {
		t.Logf("$ %s\n[oracle %d] %s%s\n[go %d] %s%s", strings.Join(args, " "), o.Code, o.Stdout, o.Stderr, g.Code, g.Stdout, g.Stderr)
	}
	if g.Code != o.Code {
		t.Errorf("%s: exit code go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", strings.Join(args, " "), g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	if gs, os_ := normEnvelope(p.Go, g.Stdout), normEnvelope(p.Oracle, o.Stdout); gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
	return g, o
}

// TestDiffCloseoutOriginAndGates covers the origin-freshness gate of
// accept-mission (MISSION_NOT_READY) and consolidate-mission
// (PREFLIGHT_FAILED, lane branches included), warn mode's origin_warnings,
// and the merge gates evaluated by consolidate-mission.
func TestDiffCloseoutOriginAndGates(t *testing.T) {
	t.Parallel()
	p := nativeApproval(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	staleOrigin(t, p)
	compareStderr := func(g, o testkit.Result, what string) {
		t.Helper()
		if gl, ol := p.Go.Normalize(lastLine(g.Stderr)), p.Oracle.Normalize(lastLine(o.Stderr)); gl != ol {
			t.Errorf("%s stderr differs\n--- oracle: %s\n--- go:     %s", what, ol, gl)
		}
	}
	step2M(t, p, api, "accept-mission", "--mission", "$M", "--actor", "rev")
	step2M(t, p, api, "consolidate-mission", "--mission", "$M")
	g, o := step2M(t, p, api, "accept-mission", "--mission", "$M", "--actor", "rev", "--origin-check", "warn")
	compareStderr(g, o, "accept warn")
	p.Both(func(s *testkit.Sandbox) {
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "record acceptance")
	})
	orchGatePolicy(t, p, "block")
	g, o = step2M(t, p, api, "consolidate-mission", "--mission", "$M", "--origin-check", "warn")
	compareStderr(g, o, "gate refusal")
	compareGit(t, p, "after gate refusal")
	orchGatePolicy(t, p, "warn")
	step2M(t, p, api, "consolidate-mission", "--mission", "$M", "--origin-check", "warn")
	compareGit(t, p, "after consolidate-mission")
}
