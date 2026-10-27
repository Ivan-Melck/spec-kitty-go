package doctrine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// trackOpsOnMain makes the primary branch track an Op record, so Op-record
// commits are attempted (and admission is judged) rather than skipped.
func trackOpsOnMain(s *testkit.Sandbox) {
	s.WriteFile("kitty-ops/01ARZ3NDEKTSV4RRFFQ69G5FAV.jsonl", "{\"event\": \"started\"}\n")
	s.Git("add", "kitty-ops")
	s.Git("commit", "-qm", "track Op audit records")
}

// ---- Go-only ---------------------------------------------------------------

// An Op opened from a linked worktree keeps its record (and the routing
// scan's glossary events) there; the repository root checkout is untouched,
// and list / complete find it from either checkout (#6564).
func TestOpRecordStaysInLinkedWorktree(t *testing.T) {
	s := newProject(t)
	trackOpsOnMain(s)
	missionfx.AddOwnedCheckouts(s)
	r := s.RunIn(missionfx.OwnedRel, "dispatch", "implement the login feature", "--json")
	if r.Code != 0 {
		t.Fatalf("dispatch in P: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		t.Fatal(err)
	}
	id := payload["invocation_id"].(string)
	owned := missionfx.OwnedSandbox(s)
	if !owned.Exists("kitty-ops/"+id+".jsonl") || s.Exists("kitty-ops/"+id+".jsonl") {
		t.Fatalf("record must be in P only")
	}
	if st, err := os.Stat(filepath.Join(owned.Dir, "kitty-ops", id+".jsonl")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("record mode: %v %v", st, err)
	}
	if l := s.MustRun("invocations", "list", "--owned-checkout", missionfx.OwnedRel, "--json"); !strings.Contains(l.Stdout, id) {
		t.Fatalf("owned list: %s", l.Stdout)
	}
	if l := s.MustRun("invocations", "list", "--json"); strings.Contains(l.Stdout, id) {
		t.Fatalf("R list must not show P's record: %s", l.Stdout)
	}
	// Closed from R (protected main): found in P and admitted there.
	s.MustRun("profile-invocation", "complete", "-i", id, "--outcome", "done")
	if got := missionfx.GitIn(t, s, owned.Dir, "log", "-1", "--format=%s"); got != "op(generic-agent): implement ["+id[:8]+"]" {
		t.Fatalf("P commit subject %q", got)
	}
	if st := s.Git("status", "--porcelain"); st != "" {
		t.Fatalf("R must stay clean: %q", st)
	}
	// From R on protected main, a new Op is refused before anything is written.
	ref := s.Run("dispatch", "implement the login feature", "--json")
	if ref.Code != 1 || !strings.Contains(ref.Stderr, `"error_code": "OP_WRITE_PROTECTED_BRANCH"`) {
		t.Fatalf("refusal: %d %s %s", ref.Code, ref.Stdout, ref.Stderr)
	}
	if fs := treeFiles(s, "kitty-ops"); len(fs) != 1 {
		t.Fatalf("R kitty-ops after refusal: %v", fs)
	}
}

// ---- differential ----------------------------------------------------------

// Python main keeps the Op record of work done in a linked checkout inside
// that checkout: Go must find it there (list, complete), close it where it
// lives, and never write a duplicate into the repository root checkout.
func TestParityOpCustodyLinkedCheckout(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		trackOpsOnMain(s)
		missionfx.AddOwnedCheckouts(s)
	})
	o := p.Oracle.Exec(missionfx.OwnedRel, testkit.Oracle(t), "dispatch", "implement the login feature", "--json")
	var payload map[string]any
	if err := json.Unmarshal([]byte(o.Stdout), &payload); err != nil {
		t.Fatalf("oracle dispatch in P: %v %s %s", err, o.Stdout, o.Stderr)
	}
	id := payload["invocation_id"].(string)
	og, oo := missionfx.OwnedSandbox(p.Go), missionfx.OwnedSandbox(p.Oracle)
	for _, f := range []string{"kitty-ops/" + id + ".jsonl", "kitty-ops/ops-index.jsonl"} {
		copyFile(t, oo, og, f)
	}
	os.Chmod(filepath.Join(og.Dir, "kitty-ops", id+".jsonl"), 0o600)
	g, or := p.RunIn(missionfx.OwnedRel, "invocations", "list", "--json")
	if g.Code != or.Code || p.Go.Normalize(g.Stdout) != p.Oracle.Normalize(or.Stdout) {
		t.Errorf("list in P differs:\n%s", testkit.Diff(p.Oracle.Normalize(or.Stdout), p.Go.Normalize(g.Stdout)))
	}
	for _, args := range [][]string{
		{"invocations", "list", "--json"},
		{"invocations", "list", "--owned-checkout", missionfx.OwnedRel, "--json"},
		{"invocations", "list", "--owned-checkout", ".", "--json"},
		{"invocations", "list", "--owned-checkout", "/nonexistent-owned"},
		{"dispatch", "implement the login feature", "--json"},
		{"dispatch", "implement the login feature"},
		{"dispatch", "implement the login feature", "--owned-checkout", ".", "--json"},
		{"profile-invocation", "complete", "-i", id, "--outcome", "done", "--owned-checkout", "/nonexistent-owned", "--json"},
		{"profile-invocation", "complete", "-i", id, "--outcome", "done", "--artifact", "README.md", "--json"},
		{"profile-invocation", "complete", "-i", id, "--outcome", "done", "--json"},
	} {
		compareMasked(t, p, true, args...)
	}
	if g, o := p.Go.Normalize(og.ReadFile("kitty-ops/"+id+".jsonl")), p.Oracle.Normalize(oo.ReadFile("kitty-ops/"+id+".jsonl")); g != o {
		t.Errorf("record differs:\n%s", testkit.Diff(o, g))
	}
	gl := missionfx.GitIn(t, p.Go, og.Dir, "log", "-1", "--format=%s %n%b", "--name-only")
	ol := missionfx.GitIn(t, p.Oracle, oo.Dir, "log", "-1", "--format=%s %n%b", "--name-only")
	if gl != ol {
		t.Errorf("P commit differs:\n%s", testkit.Diff(ol, gl))
	}
	if gs, os_ := p.Go.Git("status", "--porcelain"), p.Oracle.Git("status", "--porcelain"); gs != os_ {
		t.Errorf("R status differs: go=%q oracle=%q", gs, os_)
	}
	// Each side opens an Op from P (owned flag and plain cwd): records land
	// in P on both sides, R untouched.
	compareMasked(t, p, true, "dispatch", "implement the login feature", "--owned-checkout", missionfx.OwnedRel, "--json")
	g, or = p.RunIn(missionfx.OwnedRel, "dispatch", "review the code", "--profile", "reviewer-renata", "--json")
	if g.Code != or.Code || maskDur(p.Go.Normalize(g.Stdout)) != maskDur(p.Oracle.Normalize(or.Stdout)) {
		t.Errorf("dispatch in P differs:\n%s", testkit.Diff(maskDur(p.Oracle.Normalize(or.Stdout)), maskDur(p.Go.Normalize(g.Stdout))))
	}
	gf, of := treeFiles(og, "kitty-ops"), treeFiles(oo, "kitty-ops")
	if testkit.NormalizeValues(strings.Join(gf, "\n")) != testkit.NormalizeValues(strings.Join(of, "\n")) {
		t.Errorf("P kitty-ops differs:\n%s", testkit.Diff(strings.Join(of, "\n"), strings.Join(gf, "\n")))
	}
	gf, of = treeFiles(p.Go, "kitty-ops"), treeFiles(p.Oracle, "kitty-ops")
	if strings.Join(gf, "\n") != strings.Join(of, "\n") {
		t.Errorf("R kitty-ops differs:\n%s", testkit.Diff(strings.Join(of, "\n"), strings.Join(gf, "\n")))
	}
	if gs, os_ := testkit.NormalizeValues(missionfx.GitIn(t, p.Go, og.Dir, "log", "--format=%s")), testkit.NormalizeValues(missionfx.GitIn(t, p.Oracle, oo.Dir, "log", "--format=%s")); gs != os_ {
		t.Errorf("P history differs:\n%s", testkit.Diff(os_, gs))
	}
}

// A torn trail or protection config that cannot be read refuses the close
// with the upstream message and code.
func TestParityOpWriteRefusals(t *testing.T) {
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		trackOpsOnMain(s)
		s.Git("checkout", "-q", "-b", "topic")
	})
	o := p.Oracle.RunOracle("dispatch", "implement the login feature", "--json")
	var payload map[string]any
	if err := json.Unmarshal([]byte(o.Stdout), &payload); err != nil {
		t.Fatalf("oracle dispatch: %v %s", err, o.Stdout)
	}
	id := payload["invocation_id"].(string)
	p.Both(func(s *testkit.Sandbox) { s.Git("checkout", "-q", "main") })
	copyFile(t, p.Oracle, p.Go, "kitty-ops/"+id+".jsonl")
	compareMasked(t, p, true, "profile-invocation", "complete", "-i", id, "--outcome", "done", "--json")
	compareMasked(t, p, true, "profile-invocation", "complete", "-i", id, "--outcome", "done")
	p.Both(func(s *testkit.Sandbox) {
		s.Git("checkout", "-q", "topic")
		s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+"protection:\n  protected_branches: main\n")
	})
	compareMasked(t, p, true, "dispatch", "implement the login feature", "--json")
	compareMasked(t, p, true, "profile-invocation", "complete", "-i", id, "--outcome", "done", "--json")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/config.yaml", strings.Replace(s.ReadFile(".kittify/config.yaml"), "protected_branches: main\n", "protected_branches: []\n", 1))
		s.WriteFile("kitty-ops/"+id+".jsonl", s.ReadFile("kitty-ops/"+id+".jsonl")+`{"event": "x"`)
	})
	compareMasked(t, p, true, "profile-invocation", "complete", "-i", id, "--outcome", "done", "--json")
	if g, o := p.Go.Normalize(p.Go.ReadFile("kitty-ops/"+id+".jsonl")), p.Oracle.Normalize(p.Oracle.ReadFile("kitty-ops/"+id+".jsonl")); g != o {
		t.Errorf("torn record changed:\n%s", testkit.Diff(o, g))
	}
}
