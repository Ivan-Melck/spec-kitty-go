package cutover

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func claim(id, wp string, policy string) string {
	return fmt.Sprintf(`{"actor": "t", "at": "2026-01-01T00:00:0%s+00:00", "event_id": "01KZ00000000000000000000%s", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_id": "M", "mission_slug": "m", "policy_metadata": %s, "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "%s"}`,
		id[len(id)-1:], id, policy, wp) + "\n"
}

const shell = `{"shell_pid": 123, "agent": "claude"}`

func mission(s *testkit.Sandbox, slug, meta, events string, extra map[string]string) {
	if meta != "" {
		s.WriteFile("kitty-specs/"+slug+"/meta.json", meta)
	}
	if events != "" {
		s.WriteFile("kitty-specs/"+slug+"/status.events.jsonl", events)
	}
	for k, v := range extra {
		s.WriteFile("kitty-specs/"+slug+"/"+k, v)
	}
}

func corpus(s *testkit.Sandbox) {
	s.WriteFile(".kittify/config.yaml", "agents: {}\n")
	mission(s, "a-noid", `{"mission_slug": "a"}`, claim("01", "WP01", shell), nil)
	mission(s, "b-noruntime", `{"mission_id": "M"}`, claim("02", "WP01", `{"lane_head": "abc"}`), nil)
	mission(s, "c-exempt", `{"mission_id": "M"}`, claim("03", "WP01", shell), map[string]string{"tasks/WP01-x.md": "---\nwork_package_id: WP01\nagent: claude\n---\n"})
	mission(s, "d-accepted", `{"mission_id": "M", "accepted_at": "2026-01-01"}`, claim("04", "WP01", shell), nil)
	mission(s, "e-phase-bad", `{"mission_id": "M", "status_phase": "x"}`, claim("05", "WP01", shell), nil)
	mission(s, "f-dupkeys", `{"mission_id": "M", "a": 1, "a": 2}`, claim("06", "WP01", shell), nil)
	mission(s, "g-legacy", `{"mission_id": "M", "status_phase": 0}`, claim("07", "WP01", shell), map[string]string{"tasks/WP01-x.md": "---\nwork_package_id: WP01\nshell_pid: '42'\n---\n"})
	mission(s, "h-malformed-terminal", `{"mission_id": "M", "merged_at": 5}`, claim("08", "WP01", shell), nil)
	mission(s, "i-numbered", `{"mission_id": "M", "mission_number": 0}`, claim("09", "WP01", shell), nil)
	mission(s, "j-nometa", "", claim("01", "WP01", shell), nil)
	mission(s, "k-annotation", "\ufeff"+`{"mission_id": "M"}`, `{"kind": "inner_state_changed", "event_id": "01KZ0000000000000000000099", "wp_id": "WP01", "at": "2026-01-01T00:00:00+00:00", "mission_slug": "k", "actor": "t", "delta": {"agent": "x"}}`+"\n", map[string]string{"tasks": "not a dir"})
	mission(s, "l-stamped", `{"mission_id": "M", "status_phase": 2}`, claim("01", "WP01", shell), map[string]string{"tasks/WP01-x.md": "---\nwork_package_id: WP01\n---\n"})
	s.WriteFile("changed.txt",strings.Join([]string{
		"README.md", "kitty-specs/a-noid/meta.json", "kitty-specs/b-noruntime/spec.md", "kitty-specs/c-exempt/x",
		"kitty-specs/d-accepted/x", "  kitty-specs/e-phase-bad/x  ", "kitty-specs/f-dupkeys/x", "kitty-specs/g-legacy/x",
		"kitty-specs/h-malformed-terminal/x", "kitty-specs/i-numbered/x", "kitty-specs/j-nometa/x", "kitty-specs/k-annotation/x", "kitty-specs/l-stamped/x",
		"kitty-specs/..evil/x", "kitty-specs/gone/x", "", "kitty-specs",
	}, "\n")+"\n")
	s.WriteFile("ok.txt", "kitty-specs/b-noruntime/x\nkitty-specs/c-exempt/y\n")
	s.WriteFile("none.txt", "src/a.py\n")
}

func TestCutoverGuardParity(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(corpus)
	for _, args := range [][]string{
		{"cutover-guard"},
		{"cutover-guard", "--base-ref", "main", "--paths-from", "x"},
		{"cutover-guard", "--paths-from", "missing.txt"},
		{"cutover-guard", "--paths-from", "changed.txt"},
		{"cutover-guard", "--paths-from", "changed.txt", "--json"},
		{"cutover-guard", "--paths-from", "ok.txt"},
		{"cutover-guard", "--paths-from", "ok.txt", "--json"},
		{"cutover-guard", "--paths-from", "none.txt"},
		{"cutover-guard", "--base-ref", "nope"},
		{"cutover-guard", "--base-ref", "nope", "--json"},
	} {
		p.Compare(true, args...)
	}
}

func TestCutoverGuardBaseRefParity(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/config.yaml", "agents: {}\n")
		s.WriteFile("kitty-specs/evidence/notes.md", "n\n")
		s.WriteFile("kitty-specs/old-mission/nested/meta.json", "{}\n")
		s.WriteFile("kitty-specs/live/meta.json", `{"mission_id": "M"}`)
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "base")
		s.Git("checkout", "-q", "-b", "feature")
		s.Git("rm", "-q", "-r", "kitty-specs/evidence", "kitty-specs/old-mission")
		s.WriteFile("kitty-specs/live/status.events.jsonl", claim("01", "WP01", shell))
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "change")
	})
	p.Compare(true, "cutover-guard", "--base-ref", "main")
	p.Compare(true, "cutover-guard", "--base-ref", "main", "--json")
	p.Compare(true, "cutover-guard", "--base-ref", "HEAD")
}

func TestCutoverGuardNoProject(t *testing.T) {
	s := testkit.NewSandbox(t)
	r := s.Run("cutover-guard", "--paths-from", "x")
	if r.Code != 1 || !strings.Contains(r.Stderr, "Could not locate project root") {
		t.Fatalf("got %d %q", r.Code, r.Stderr)
	}
}

func TestEvaluateUnit(t *testing.T) {
	if got := TouchedMissionSlugs([]string{"kitty-specs/b/x", "kitty-specs//a/./y", "x/kitty-specs/c", "kitty-specs"}); strings.Join(got, ",") != "a,b" {
		t.Fatalf("slugs %v", got)
	}
	if err := AssertSafePathSegment("001-ok.v2"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", " a", "..", ".x", "a/b", "é"} {
		if AssertSafePathSegment(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
