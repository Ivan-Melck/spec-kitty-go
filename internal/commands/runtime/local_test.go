package runtime_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func decodeJSON(t *testing.T, r testkit.Result) *jsonx.Object {
	t.Helper()
	out := strings.TrimSpace(r.Stdout)
	if i := strings.Index(out, "{"); i > 0 {
		out = out[i:]
	}
	o, err := jsonx.DecodeObject([]byte(out))
	if err != nil {
		t.Fatalf("not JSON (exit %d): %q\n%s", r.Code, r.Stdout, r.Stderr)
	}
	return o
}

func fileHash(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) == "tasks.md" {
		b = regexp.MustCompile(`(?m)^(\s*[-*]\s*)\[[ xX]\]`).ReplaceAll(b, []byte("${1}[ ]"))
		b = regexp.MustCompile(`\|(\s*)\[[ xXDP]\](\s*)\|`).ReplaceAll(b, []byte("|${1}[ ]${2}|"))
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// writeAnalysisReport records a current analysis report (the
// record-analysis output shape) for the local calc mission.
func writeAnalysisReport(t *testing.T, s *testkit.Sandbox, slug string) {
	dir := filepath.Join(s.Dir, "kitty-specs", slug)
	body := "---\nschema_version: 1\nartifact_type: spec-kitty.analysis-report\ncommand: /spec-kitty.analyze\n" +
		"mission_slug: " + slug + "\ninput_artifacts:\n" +
		"  spec.md:\n    path: kitty-specs/" + slug + "/spec.md\n    sha256: " + fileHash(t, filepath.Join(dir, "spec.md")) + "\n" +
		"  plan.md:\n    path: kitty-specs/" + slug + "/plan.md\n    sha256: " + fileHash(t, filepath.Join(dir, "plan.md")) + "\n" +
		"  tasks.md:\n    path: kitty-specs/" + slug + "/tasks.md\n    sha256: " + fileHash(t, filepath.Join(dir, "tasks.md")) + "\n" +
		"  charter:\n    path:\n    sha256:\nverdict: ready\n---\n\nReport.\n"
	s.WriteFile(filepath.Join("kitty-specs", slug, "analysis-report.md"), body)
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "record analysis")
}

// TestLocalRuntimeLoop runs next / implement / review end-to-end without the
// oracle on the lanes-topology calc fixture.
func TestLocalRuntimeLoop(t *testing.T) {
	s := missionfx.NewLocalMission(t)
	slug := missionfx.LocalSlug
	r := s.Run("next", "--agent", "claude", "--mission", slug, "--json")
	o := decodeJSON(t, r)
	if r.Code != 0 || o.GetString("mission_state") != "analyze" || o.GetString("error_code") != "ANALYSIS_REPORT_MISSING" {
		t.Fatalf("analyze gate: %d %s", r.Code, r.Stdout)
	}
	r = s.Run("agent", "action", "implement", "WP01", "--agent", "claude", "--mission", slug)
	if r.Code != 1 || !strings.Contains(r.Stdout, "analysis_report_required") {
		t.Fatalf("implement before analysis: %d %s", r.Code, r.Stdout)
	}
	// the recorded fixture pins a planning commit of another repository:
	// re-pin it to this repository's planning commit.
	lj := filepath.Join("kitty-specs", slug, "lanes.json")
	m0, err := jsonx.DecodeObject([]byte(s.ReadFile(lj)))
	if err != nil {
		t.Fatal(err)
	}
	m0.Set("planning_commit_sha", s.Git("rev-parse", "HEAD"))
	s.WriteFile(lj, jsonx.Dumps(m0, jsonx.Pretty)+"\n")
	writeAnalysisReport(t, s, slug)
	r = s.Run("next", "--agent", "claude", "--mission", slug, "--json")
	if o := decodeJSON(t, r); o.GetString("mission_state") != "implement" || o.GetString("wp_id") != "WP01" {
		t.Fatalf("query implement: %s", r.Stdout)
	}
	r = s.Run("agent", "action", "implement", "WP02", "--agent", "claude", "--mission", slug)
	if r.Code != 1 || !strings.Contains(r.Stdout, "dependencies_not_satisfied: WP02 depends on WP01") {
		t.Fatalf("dependency gate: %d %s", r.Code, r.Stdout)
	}
	r = s.Run("implement", "WP02", "--mission", slug, "--json")
	if o := decodeJSON(t, r); r.Code != 1 || !strings.Contains(o.GetString("error"), "dependencies_not_satisfied") {
		t.Fatalf("top-level dependency gate: %d %s", r.Code, r.Stdout)
	}
	r = s.Run("agent", "action", "implement", "WP01", "--agent", "claude", "--mission", slug)
	if r.Code != 0 || !strings.Contains(r.Stdout, "✓ Claimed WP01 (agent: claude") || !strings.Contains(r.Stdout, "Lane worktree ready") {
		t.Fatalf("implement WP01: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	lane := filepath.Join(".worktrees", slug+"-lane-a")
	if !s.Exists(filepath.Join(lane, "src", "calc.py")) {
		t.Fatalf("lane worktree missing")
	}
	if !strings.Contains(s.Git("log", "--format=%s", "-5"), "chore: WP01 claimed for implementation") {
		t.Fatalf("claim commit missing: %s", s.Git("log", "--format=%s", "-5"))
	}
	r = s.Run("agent", "tasks", "status", "--mission", slug, "--json")
	if !strings.Contains(r.Stdout, "in_progress") {
		t.Fatalf("status after claim: %s", r.Stdout)
	}
	// prompt file written under ~/.spec-kitty/spec-kitty-prompts
	m, _ := filepath.Glob(filepath.Join(s.Home, ".spec-kitty", "spec-kitty-prompts", "*", "spec-kitty-implement-"+slug+"-WP01.md"))
	if len(m) != 1 {
		t.Fatalf("implement prompt not written")
	}
	b, _ := os.ReadFile(m[0])
	if !strings.Contains(string(b), "IMPLEMENT: WP01") || !strings.Contains(string(b), "WORK PACKAGE PROMPT BEGINS") {
		t.Fatalf("prompt content: %s", b)
	}
	// resume is a no-op claim
	if r = s.Run("agent", "action", "implement", "WP01", "--agent", "claude", "--mission", slug); r.Code != 0 {
		t.Fatalf("resume: %d %s", r.Code, r.Stdout)
	}
	s.WriteFile(filepath.Join(lane, "src", "calc.py"), "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n")
	s.Exec(lane, "git", "add", "src/calc.py")
	s.Exec(lane, "git", "commit", "-q", "-m", "feat(WP01): subtract")
	if r = s.Run("agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", slug); r.Code != 0 {
		t.Fatalf("mark-status: %s", r.Stdout)
	}
	if r = s.Run("agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", slug, "--agent", "claude", "--note", "Ready"); r.Code != 0 {
		t.Fatalf("for_review: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("next", "--agent", "claude", "--mission", slug, "--json")
	if o := decodeJSON(t, r); o.GetString("mission_state") != "review" {
		t.Fatalf("query review: %s", r.Stdout)
	}
	r = s.Run("agent", "action", "review", "--agent", "claude", "--mission", slug)
	if r.Code != 0 || !strings.Contains(r.Stdout, "✓ Claimed WP01 for review") || !strings.Contains(r.Stdout, "After review, run:") {
		t.Fatalf("review: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if !s.Exists(filepath.Join(lane, ".spec-kitty", "review-lock.json")) {
		t.Fatalf("review lock missing")
	}
	r = s.Run("next", "--agent", "claude", "--mission", slug, "--json")
	if o := decodeJSON(t, r); o.GetString("mission_state") != "blocked" || o.GetString("reason") != "review in progress" {
		t.Fatalf("query in_review: %s", r.Stdout)
	}
	// advancing on first contact with WP01 in review: blocked
	r = s.Run("next", "--agent", "claude", "--mission", slug, "--result", "success", "--json")
	if o := decodeJSON(t, r); r.Code != 1 || o.GetString("kind") != "blocked" {
		t.Fatalf("advance blocked: %d %s", r.Code, r.Stdout)
	}
	if r = s.Run("agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", slug, "--agent", "claude", "--note", "ok"); r.Code != 0 {
		t.Fatalf("approve: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("next", "--agent", "claude", "--mission", slug, "--result", "success", "--json")
	o = decodeJSON(t, r)
	if r.Code != 0 || o.GetString("kind") != "step" || o.GetString("wp_id") != "WP02" || o.GetString("prompt_file") == "" {
		t.Fatalf("advance WP02: %d %s", r.Code, r.Stdout)
	}
	r = s.Run("agent", "action", "implement", "WP02", "--agent", "claude", "--mission", slug)
	if r.Code != 0 || !s.Exists(filepath.Join(".worktrees", slug+"-lane-b", "src", "calc.py")) {
		t.Fatalf("implement WP02: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if got := s.ReadFile(filepath.Join(".worktrees", slug+"-lane-b", "src", "calc.py")); !strings.Contains(got, "subtract") {
		t.Fatalf("lane-b lacks the lane-a dependency merge: %q", got)
	}
	r = s.Run("context", "list", "--json")
	if r.Code != 0 || !strings.Contains(r.Stdout, slug+"-lane-a") && !strings.Contains(r.Stdout, "lane-a") {
		t.Fatalf("context list: %s", r.Stdout)
	}
	r = s.Run("mission", "close", "--mission", slug, "--json")
	if r.Code != 1 || !strings.Contains(r.Stdout, "mission_not_merged") {
		t.Fatalf("close refusal: %d %s", r.Code, r.Stdout)
	}
	r = s.Run("next", "--agent", "claude", "--mission", "no-such-mission", "--json")
	if o := decodeJSON(t, r); r.Code != 1 || o.GetString("error_code") != "MISSION_NOT_FOUND" {
		t.Fatalf("unknown handle: %d %s", r.Code, r.Stdout)
	}
}
