package planning_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// Go-only tests (no oracle): they keep `go test ./...` meaningful when
// SPEC_KITTY_ORACLE is unset.

func lastJSON(t *testing.T, r testkit.Result) *jsonx.Object {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "{") {
			if o, err := jsonx.DecodeObject([]byte(l)); err == nil {
				return o
			}
		}
	}
	if o, err := jsonx.DecodeObject([]byte(strings.TrimSpace(r.Stdout))); err == nil {
		return o
	}
	t.Fatalf("no JSON in output (exit %d):\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	return nil
}

func TestLocalSafeCommit(t *testing.T) {
	s := missionfx.NewLocalMission(t)
	s.WriteFile("notes.txt", "hello\n")
	r := s.Run("safe-commit", "notes.txt", "-m", "x", "--to-branch", "main", "--json")
	if r.Code == 0 {
		t.Fatalf("expected refusal for HEAD mismatch: %s", r.Stdout)
	}
	r = s.Run("safe-commit", "notes.txt", "-m", "add notes", "--to-branch", "feat/calc-ops", "--json")
	if r.Code != 0 {
		t.Fatalf("safe-commit: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if got := strings.TrimSpace(s.Git("log", "-1", "--format=%s")); got != "add notes" {
		t.Fatalf("last commit = %q", got)
	}
	if st := strings.TrimSpace(s.Git("status", "--short")); st != "" {
		t.Fatalf("tree not clean after safe-commit: %q", st)
	}
	s.WriteFile(filepath.Join("kitty-specs", missionfx.LocalSlug, "research.md"), "# Research\n\nFindings.\n")
	r = s.Run("spec-commit", "-m", "research", filepath.Join("kitty-specs", missionfx.LocalSlug, "research.md"), "--json")
	if r.Code != 0 {
		t.Fatalf("spec-commit: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if !strings.Contains(s.Git("log", "-1", "--name-only", "--format="), "research.md") {
		t.Fatal("spec-commit did not commit research.md")
	}
}

func TestLocalCheckPrerequisites(t *testing.T) {
	s := missionfx.NewLocalMission(t)
	r := s.Run("agent", "mission", "check-prerequisites", "--mission", missionfx.LocalSlug, "--json", "--include-tasks")
	o := lastJSON(t, r)
	if r.Code != 0 || !o.GetBool("valid") || !strings.HasSuffix(o.GetString("FEATURE_DIR"), missionfx.LocalSlug) {
		t.Fatalf("check-prerequisites: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("agent", "check-prerequisites", "--mission", "no-such-mission", "--json")
	if r.Code == 0 {
		t.Fatalf("expected failure for unknown mission: %s", r.Stdout)
	}
}

func TestLocalDecisions(t *testing.T) {
	s := missionfx.NewLocalMission(t)
	r := s.Run("agent", "decision", "open", "--mission", missionfx.LocalSlug, "--flow", "plan", "--step-id", "plan.q1",
		"--input-key", "arch", "--question", "Which architecture?", "--actor", "claude")
	o := lastJSON(t, r)
	id := o.GetString("decision_id")
	if r.Code != 0 || id == "" {
		t.Fatalf("open: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	idx := filepath.Join(s.Dir, "kitty-specs", missionfx.LocalSlug, "decisions", "index.json")
	if _, err := os.Stat(idx); err != nil {
		t.Fatalf("index.json not written: %v", err)
	}
	if r = s.Run("agent", "decision", "resolve", id, "--mission", missionfx.LocalSlug, "--final-answer", "hexagonal"); r.Code != 0 {
		t.Fatalf("resolve: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if r = s.Run("agent", "decision", "resolve", id, "--mission", missionfx.LocalSlug, "--final-answer", "layered"); r.Code == 0 {
		t.Fatalf("conflicting resolve should fail: %s", r.Stdout)
	}
	r = s.Run("agent", "decision", "list", "--mission", missionfx.LocalSlug, "--status", "resolved")
	if r.Code != 0 || !strings.Contains(r.Stdout, id) {
		t.Fatalf("list: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if r = s.Run("agent", "decision", "verify", "--mission", missionfx.LocalSlug); r.Code != 0 {
		t.Fatalf("verify: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
}

func TestLocalLifecycleAndResearch(t *testing.T) {
	s := missionfx.NewLocalMission(t)
	r := s.Run("specify", "  ", "--json")
	if o := lastJSON(t, r); r.Code != 1 || o.GetString("kind") != "NonAsciiNameError" {
		t.Fatalf("specify blank: %d %s", r.Code, r.Stdout)
	}
	if r = s.Run("plan", "--json"); r.Code != 2 || lastJSON(t, r).GetString("error") != "--mission <slug> is required" {
		t.Fatalf("plan w/o mission: %d %s", r.Code, r.Stdout)
	}
	r = s.Run("specify", "deep dive", "--mission-type", "research", "--topology", "lanes", "--json")
	o := lastJSON(t, r)
	slug := o.GetString("mission_slug")
	if r.Code != 0 || !o.GetBool("scaffold_only") || slug == "" {
		t.Fatalf("specify: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("research", "--mission", slug)
	if r.Code != 0 || !strings.Contains(r.Stdout, "Research Phase Setup") {
		t.Fatalf("research: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	research := filepath.Join(s.Dir, "kitty-specs", slug, "research.md")
	if _, err := os.Stat(research); err != nil {
		t.Fatalf("research.md not scaffolded: %v", err)
	}
	if err := os.WriteFile(research, []byte("# mine\n\nkeep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r = s.Run("research", "--mission", slug); r.Code != 0 {
		t.Fatalf("research rerun: %d %s", r.Code, r.Stdout)
	}
	if b, _ := os.ReadFile(research); !strings.Contains(string(b), "keep me") {
		t.Fatal("research overwrote an existing file without --force")
	}
	if r = s.Run("research", "--mission", slug, "--force"); r.Code != 0 {
		t.Fatalf("research --force: %d %s", r.Code, r.Stdout)
	}
	if b, _ := os.ReadFile(research); strings.Contains(string(b), "keep me") {
		t.Fatal("research --force did not overwrite")
	}
}

func TestLocalRecordAnalysisDirty(t *testing.T) {
	s := missionfx.NewLocalMission(t)
	s.WriteFile("stray.txt", "dirty\n")
	r := s.Run("agent", "mission", "record-analysis", "--mission", missionfx.LocalSlug, "--input-file", missionfx.Path("calc/analysis-report.md"), "--json")
	if r.Code == 0 || !strings.Contains(r.Stdout, "DIRTY_WORKTREE") {
		t.Fatalf("expected DIRTY_WORKTREE refusal: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
}
