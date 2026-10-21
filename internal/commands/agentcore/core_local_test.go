package agentcore_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func mustJSON(t *testing.T, r testkit.Result) *jsonx.Object {
	t.Helper()
	line := strings.TrimSpace(r.Stdout)
	if i := strings.LastIndex(line, "\n{"); i >= 0 {
		line = line[i+1:]
	}
	o, err := jsonx.DecodeObject([]byte(line))
	if err != nil {
		t.Fatalf("not JSON (exit %d): %q\n%s", r.Code, r.Stdout, r.Stderr)
	}
	return o
}

// TestLocalCoreLifecycle exercises the core commands end-to-end without the
// oracle: handle forms, move-task guards and hops, review cycles, status
// emit/validate/materialize and the status commits on the primary checkout.
func TestLocalCoreLifecycle(t *testing.T) {
	s := newLocalMission(t)
	for _, h := range []string{localSlug, "calc-ops", "01M54Y16", "01M54Y16550EHN17T75926T0NP"} {
		r := s.Run("agent", "tasks", "status", "--mission", h, "--json")
		if r.Code != 0 || mustJSON(t, r).GetString("mission_slug") != localSlug {
			t.Fatalf("handle %s: exit %d %s %s", h, r.Code, r.Stdout, r.Stderr)
		}
	}
	r := s.Run("agent", "tasks", "move-task", "WP99", "--to", "planned", "--mission", localSlug, "--json")
	if r.Code != 1 || !strings.Contains(mustJSON(t, r).GetString("error"), "Work package 'WP99' not found") {
		t.Fatalf("WP99: %d %s", r.Code, r.Stdout)
	}
	r = s.Run("agent", "tasks", "move-task", "WP01", "--to", "bogus-lane", "--mission", localSlug, "--json")
	if r.Code != 1 || !strings.HasPrefix(mustJSON(t, r).GetString("error"), "Invalid lane 'bogus-lane'.") {
		t.Fatalf("bogus: %d %s", r.Code, r.Stdout)
	}
	r = s.Run("agent", "tasks", "move-task", "WP01", "--to", "doing", "--mission", localSlug, "--agent", "claude", "--json")
	if o := mustJSON(t, r); r.Code != 0 || o.GetString("new_lane") != "in_progress" || o.GetString("old_lane") != "planned" {
		t.Fatalf("doing: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if log := s.Git("log", "--format=%s", "-3"); !strings.Contains(log, "chore(spec-kitty): status transition WP01") || !strings.Contains(log, "inner-state annotation WP01") {
		t.Fatalf("expected status commits, got:\n%s", log)
	}
	r = s.Run("agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", localSlug, "--json")
	if r.Code != 1 || !strings.Contains(mustJSON(t, r).GetString("error"), "unchecked subtasks") {
		t.Fatalf("unchecked: %d %s", r.Code, r.Stdout)
	}
	if r = s.Run("agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", localSlug); r.Code != 0 {
		t.Fatalf("mark-status: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", localSlug, "--json")
	if o := mustJSON(t, r); r.Code != 0 || o.GetObject("pre_review_gate").GetString("outcome") != "no_coverage" {
		t.Fatalf("for_review: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if r = s.Run("agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", localSlug, "--agent", "rev"); r.Code != 0 {
		t.Fatalf("in_review: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("agent", "tasks", "move-task", "WP01", "--to", "planned", "--mission", localSlug, "--agent", "rev", "--json")
	if r.Code != 1 || !strings.Contains(mustJSON(t, r).GetString("error"), "requires review feedback") {
		t.Fatalf("planned w/o feedback: %d %s", r.Code, r.Stdout)
	}
	s.WriteFile("fb.md", "**Issue**: bad\n")
	r = s.Run("agent", "tasks", "move-task", "WP01", "--to", "planned", "--review-feedback-file", "fb.md", "--mission", localSlug, "--agent", "rev", "--json")
	o := mustJSON(t, r)
	if r.Code != 0 || o.GetString("review_feedback") != "review-cycle://"+localSlug+"/WP01-subtract-helper/review-cycle-1.md" || !o.GetBool("verdict_durably_persisted") {
		t.Fatalf("rejection: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	cycle := filepath.Join(s.Dir, "kitty-specs", localSlug, "tasks", "WP01-subtract-helper", "review-cycle-1.md")
	if b, err := os.ReadFile(cycle); err != nil || !strings.Contains(string(b), "reviewer_agent: rev") {
		t.Fatalf("review cycle artifact: %v %s", err, b)
	}
	r = s.Run("agent", "status", "emit", "WP02", "--to", "blocked", "--actor", "x", "--mission", localSlug, "--json")
	if o := mustJSON(t, r); r.Code != 0 || o.GetString("to_lane") != "blocked" {
		t.Fatalf("emit: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if r = s.Run("agent", "status", "validate", "--mission", localSlug, "--json"); r.Code != 0 || !mustJSON(t, r).GetBool("passed") {
		t.Fatalf("validate: %d %s", r.Code, r.Stdout)
	}
	if r = s.Run("agent", "status", "materialize", "--mission", localSlug, "--json"); r.Code != 0 {
		t.Fatalf("materialize: %d %s", r.Code, r.Stdout)
	}
	derived := filepath.Join(s.Dir, ".kittify", "derived", localSlug)
	for _, f := range []string{"status.json", "board-summary.json", "progress.json", "lifecycle.json"} {
		if _, err := os.Stat(filepath.Join(derived, f)); err != nil {
			t.Errorf("derived view %s missing: %v", f, err)
		}
	}
	r = s.Run("agent", "tasks", "list-dependents", "WP01", "--mission", localSlug, "--json")
	if o := mustJSON(t, r); r.Code != 0 || len(o.GetList("dependents")) != 1 {
		t.Fatalf("list-dependents: %d %s", r.Code, r.Stdout)
	}
	if st := s.Git("status", "--short", "--untracked-files=no"); strings.TrimSpace(st) != "" {
		t.Fatalf("tracked tree dirty after status commits:\n%s", st)
	}
}
