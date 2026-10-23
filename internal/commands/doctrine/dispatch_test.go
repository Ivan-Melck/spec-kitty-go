package doctrine_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// ---- Go-only ---------------------------------------------------------------

func TestDispatchOpenCompleteList(t *testing.T) {
	s := newProject(t)
	r := s.MustRun("dispatch", "implement the login feature", "--json")
	var payload map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		t.Fatalf("dispatch json: %v\n%s", err, r.Stdout)
	}
	id, _ := payload["invocation_id"].(string)
	if payload["profile_id"] != "generic-agent" || payload["action"] != "implement" ||
		payload["router_confidence"] != "generic_fallback" || payload["status"] != "open" || len(id) != 26 {
		t.Fatalf("unexpected payload: %v", payload)
	}
	rec := s.ReadFile("kitty-ops/" + id + ".jsonl")
	if !strings.HasPrefix(rec, `{"event": "started", "invocation_id": "`+id+`", "profile_id": "generic-agent", "action": "implement"`) {
		t.Fatalf("started record: %s", rec)
	}
	if !strings.Contains(s.ReadFile("kitty-ops/ops-index.jsonl"), id) {
		t.Fatal("index entry missing")
	}
	// Open Ops are listed as open.
	l := s.MustRun("invocations", "list", "--json")
	if !strings.Contains(l.Stdout, `"status": "open"`) {
		t.Fatalf("list: %s", l.Stdout)
	}
	// Close on protected main: event written, commit refused.
	c := s.MustRun("profile-invocation", "complete", "--invocation-id", id, "--outcome", "done", "--artifact", "README.md", "--commit", "abc123")
	if !strings.Contains(c.Stdout, "Invocation "+id+" closed.") || !strings.Contains(c.Stdout, "auto-commit was refused") {
		t.Fatalf("complete: %s", c.Stdout)
	}
	rec = s.ReadFile("kitty-ops/" + id + ".jsonl")
	for _, want := range []string{`"event": "completed"`, `"closed_by": "agent"`, `"event": "artifact_link"`, `"ref": "README.md"`, `"event": "commit_link"`, `"sha": "abc123"`} {
		if !strings.Contains(rec, want) {
			t.Errorf("record lacks %s:\n%s", want, rec)
		}
	}
	again := s.Run("profile-invocation", "complete", "-i", id, "--outcome", "done", "--json")
	if again.Code != 1 || !strings.Contains(again.Stderr, `"already_closed"`) {
		t.Fatalf("second close: %d %s %s", again.Code, again.Stdout, again.Stderr)
	}
	l = s.MustRun("invocations", "list", "--json")
	if !strings.Contains(l.Stdout, `"status": "closed"`) || !strings.Contains(l.Stdout, `"outcome": "done"`) {
		t.Fatalf("list after close: %s", l.Stdout)
	}
}

func TestDispatchCommitOnTopicBranch(t *testing.T) {
	s := newProject(t)
	s.Git("checkout", "-q", "-b", "feat/x")
	r := s.MustRun("dispatch", "review the code", "--profile", "reviewer-renata", "--json")
	var payload map[string]any
	json.Unmarshal([]byte(r.Stdout), &payload)
	id := payload["invocation_id"].(string)
	if payload["action"] != "audit" || payload["governance_context_available"] != true {
		t.Fatalf("payload: %v", payload)
	}
	if !strings.Contains(payload["governance_context_text"].(string), "Profile-Cited Directives (reviewer-renata):") {
		t.Fatal("profile sections missing from governance text")
	}
	s.MustRun("profile-invocation", "complete", "-i", id, "--outcome", "failed")
	if got := s.Git("log", "-1", "--format=%s"); got != "op(reviewer-renata): audit ["+id[:8]+"]" {
		t.Fatalf("commit subject %q", got)
	}
}

func TestDispatchRoutingErrors(t *testing.T) {
	s := newProject(t)
	r := s.Run("dispatch", "x", "--profile", "nope", "--json")
	if r.Code != 1 || !strings.Contains(r.Stderr, `"error_code": "PROFILE_NOT_FOUND"`) {
		t.Fatalf("unknown profile: %d %s", r.Code, r.Stderr)
	}
	r = s.Run("profile-invocation", "complete", "-i", "nope", "--outcome", "done")
	if r.Code != 1 || !strings.Contains(r.Stderr, "complete_failed") {
		t.Fatalf("bad id: %d %s", r.Code, r.Stderr)
	}
	r = s.Run("profile-invocation", "complete", "-i", "01M5ENPE8T0VR4D1YZ2CDXH7XQ", "--outcome", "bogus")
	if r.Code != 2 {
		t.Fatalf("bad outcome: %d", r.Code)
	}
	if r := s.Run("ops", "undo"); r.Code != 1 || !strings.Contains(r.Stdout, "Undo not supported for git") {
		t.Fatalf("ops undo: %d %s", r.Code, r.Stdout)
	}
}

// ---- differential ----------------------------------------------------------

func TestParityDispatchFallback(t *testing.T) {
	p := newOraclePair(t)
	for _, args := range [][]string{
		{"dispatch", "implement the login feature", "--json"},
		{"dispatch", "implement the login feature"},
		{"dispatch", "implement the login feature", "--dry-run", "--json"},
		{"dispatch", "implement the login feature", "--dry-run"},
		{"dispatch", "review the code", "--profile", "reviewer-renata", "--json"},
		{"dispatch", "look at this", "--profile", "architect-alphonso", "--dry-run", "--json"},
		{"dispatch", "look at this", "--profile", "python-pedro", "--dry-run", "--json"},
		{"dispatch", "xyzzy", "--profile", "nope", "--json"},
		{"invocations", "list", "--json"},
		{"invocations", "list"},
	} {
		compareMasked(t, p, true, args...)
	}
	compareMaskedWith(t, p, true, maskReflogMinute, "ops", "log")
	gf, of := treeFiles(p.Go, "kitty-ops"), treeFiles(p.Oracle, "kitty-ops")
	if testkit.NormalizeValues(strings.Join(gf, "\n")) != testkit.NormalizeValues(strings.Join(of, "\n")) {
		t.Errorf("kitty-ops tree differs:\n%s", testkit.Diff(strings.Join(of, "\n"), strings.Join(gf, "\n")))
	}
	p.CompareTree(".kittify/events", false)
}

func TestParityDispatchRouting(t *testing.T) {
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+
			"activated_agent_profiles:\n- implementer-ivan\n- reviewer-renata\n- architect-alphonso\n- planner-priti\n- python-pedro\n- researcher-robbie\n- designer-dagmar\n- curator-carla\n")
	})
	for _, args := range [][]string{
		{"dispatch", "implement the login feature", "--json"},
		{"dispatch", "review the code"},
		{"dispatch", "please do things", "--json"},
		{"dispatch", "investigate the python flakiness", "--json"},
		{"dispatch", "design and build a widget", "--dry-run", "--json"},
		{"dispatch", "design and build a widget", "--dry-run"},
		{"dispatch", "audit", "--dry-run", "--json"},
		{"dispatch", "xyz", "--profile", "frontend-freddy", "--json"},
	} {
		compareMasked(t, p, true, args...)
	}
}

func TestParityProfileInvocationComplete(t *testing.T) {
	p := newOraclePair(t)
	o := p.Oracle.RunOracle("dispatch", "implement the login feature", "--json")
	var payload map[string]any
	if err := json.Unmarshal([]byte(o.Stdout), &payload); err != nil {
		t.Fatalf("oracle dispatch: %v %s", err, o.Stdout)
	}
	id := payload["invocation_id"].(string)
	copyFile(t, p.Oracle, p.Go, "kitty-ops/"+id+".jsonl")
	copyFile(t, p.Oracle, p.Go, "kitty-ops/ops-index.jsonl")
	compareMasked(t, p, true, "invocations", "list", "--json")
	compareMasked(t, p, true, "profile-invocation", "complete", "-i", id, "--outcome", "done", "--artifact", "README.md", "--commit", "abc123", "--json")
	compareMasked(t, p, true, "profile-invocation", "complete", "-i", id, "--outcome", "done")
	compareMasked(t, p, true, "profile-invocation", "complete", "-i", id, "--outcome", "done", "--json")
	compareMasked(t, p, true, "profile-invocation", "complete", "-i", "01M5ENPE8T0VR4D1YZ2CDXXXXX", "--outcome", "done", "--json")
	compareMasked(t, p, true, "invocations", "list", "--json")
	compareMasked(t, p, true, "invocations", "list")
	if g, o := p.Go.Normalize(p.Go.ReadFile("kitty-ops/"+id+".jsonl")), p.Oracle.Normalize(p.Oracle.ReadFile("kitty-ops/"+id+".jsonl")); g != o {
		t.Errorf("record differs:\n%s", testkit.Diff(o, g))
	}
}
