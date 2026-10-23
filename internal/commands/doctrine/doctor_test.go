package doctrine_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

const (
	opStaleA = `{"event": "started", "invocation_id": "01KAAAAAAAAAAAAAAAAAAAAAAA", "profile_id": "implementer", "action": "implement", "request_text": "x", "actor": "claude", "mode_of_work": "task_execution", "governance_context_hash": "abc", "governance_context_available": true, "started_at": "2024-01-01T00:00:00+00:00"}` + "\n"
	opFreshB = `{"event": "started", "invocation_id": "01KBBBBBBBBBBBBBBBBBBBBBBB", "profile_id": "reviewer", "action": "review", "request_text": "x", "actor": "claude", "mode_of_work": "task_execution", "governance_context_hash": "abc", "governance_context_available": true, "started_at": "2099-01-01T00:00:00+00:00"}` + "\n"
	opBadC   = `{"event": "started", "invocation_id": "01KCCCCCCCCCCCCCCCCCCCCCCC", "profile_id": "reviewer", "action": "review", "request_text": "x", "actor": "claude", "mode_of_work": "task_execution", "governance_context_hash": "abc", "governance_context_available": true, "started_at": "garbage"}` + "\n"
	opDoneD  = `{"event": "started", "invocation_id": "01KDDDDDDDDDDDDDDDDDDDDDDD", "profile_id": "reviewer", "action": "review", "request_text": "x", "actor": "claude", "mode_of_work": "task_execution", "governance_context_hash": "abc", "governance_context_available": true, "started_at": "2024-01-01T00:00:00+00:00"}` + "\n" +
		`{"event": "completed", "invocation_id": "01KDDDDDDDDDDDDDDDDDDDDDDD", "completed_at": "2024-01-01T01:00:00+00:00", "outcome": "done", "closed_by": "agent"}` + "\n"
	lifecycleLog = `{"agent": "claude", "at": "2024-01-01T00:00:00+00:00", "canonical_action_id": "specify::write", "mission_id": "M1", "phase": "started", "reason": null, "wp_id": null}
{"agent": "claude", "at": "2024-01-01T00:00:00", "canonical_action_id": "implement::code", "mission_id": "M1", "phase": "started", "reason": null, "wp_id": "WP01"}
{"agent": "claude", "at": "2024-01-01T01:00:00Z", "canonical_action_id": "specify::write", "mission_id": "M1", "phase": "completed", "reason": null, "wp_id": null}
{"agent": "codex", "at": "2024-01-02T00:00:00+02:00", "canonical_action_id": "specify::write", "mission_id": "M2", "phase": "started", "wp_id": null}
garbage
`
)

var reAge = regexp.MustCompile(`("age_hours": )-?[0-9.e+-]+|-?[0-9]+\.[0-9](    (?:closed_abandoned|none|already_closed))`)

func maskAge(s string) string { return reAge.ReplaceAllString(s, "$1<AGE>$2") }

func writeOps(s *testkit.Sandbox) {
	s.WriteFile("kitty-ops/01KAAAAAAAAAAAAAAAAAAAAAAA.jsonl", opStaleA)
	s.WriteFile("kitty-ops/01KBBBBBBBBBBBBBBBBBBBBBBB.jsonl", opFreshB)
	s.WriteFile("kitty-ops/01KCCCCCCCCCCCCCCCCCCCCCCC.jsonl", opBadC)
	s.WriteFile("kitty-ops/01KDDDDDDDDDDDDDDDDDDDDDDD.jsonl", opDoneD)
	s.WriteFile("kitty-ops/lifecycle.jsonl", lifecycleLog)
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "ops")
}

func compareAge(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d\n%s\n%s", strings.Join(args, " "), g.Code, o.Code, g.Stdout, o.Stdout)
	}
	if gs, os_ := maskAge(p.Go.Normalize(g.Stdout)), maskAge(p.Oracle.Normalize(o.Stdout)); gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
	if ge, oe := p.Go.Normalize(g.Stderr), p.Oracle.Normalize(o.Stderr); ge != oe {
		t.Errorf("%s: stderr differs\n%s", strings.Join(args, " "), testkit.Diff(oe, ge))
	}
}

func TestParityDoctorOpsAndPairing(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	for _, args := range [][]string{
		{"doctor", "ops"}, {"doctor", "ops", "--json"},
		{"doctor", "invocation-pairing"}, {"doctor", "invocation-pairing", "--json"},
		{"doctor", "ops", "--threshold", "3"},
	} {
		compareMasked(t, p, true, args...)
	}
	p.Both(writeOps)
	for _, args := range [][]string{
		{"doctor", "ops"}, {"doctor", "ops", "--json"},
		{"doctor", "invocation-pairing"}, {"doctor", "invocation-pairing", "--json"},
	} {
		compareMasked(t, p, true, args...)
	}
	// Sweep on protected main: closures recorded, commit refused.
	compareAge(t, p, "doctor", "ops", "--close-stale", "--json")
	compareAge(t, p, "doctor", "ops", "--close-stale", "--threshold", "0")
	g, o := p.Go.ReadFile("kitty-ops/op-closures.jsonl"), p.Oracle.ReadFile("kitty-ops/op-closures.jsonl")
	if p.Go.Normalize(g) != p.Oracle.Normalize(o) {
		t.Errorf("spine differs\n%s", testkit.Diff(p.Oracle.Normalize(o), p.Go.Normalize(g)))
	}
	compareMasked(t, p, true, "doctor", "ops", "--json")
}

func TestParityDoctorOpsSweepTopic(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.Git("checkout", "-q", "-b", "topic")
		writeOps(s)
	})
	compareAge(t, p, "doctor", "ops", "--close-stale")
	if g, o := p.Go.Git("log", "--format=%s", "-3"), p.Oracle.Git("log", "--format=%s", "-3"); g != o {
		t.Errorf("git log differs\n%s", testkit.Diff(o, g))
	}
	if g, o := p.Go.Git("status", "--porcelain"), p.Oracle.Git("status", "--porcelain"); g != o {
		t.Errorf("git status differs\n%s", testkit.Diff(o, g))
	}
}

func TestParityDoctorMissionType(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	metas := map[string]string{
		"001-resolved": `{"mission_type": "software-dev"}`,
		"002-unknown":  `{"mission_type": "bogus"}`,
		"003-typeless": `{"mission_type": ""}`,
		"004-null":     `{"mission_type": null, "mission": "research"}`,
		"005-legacy":   `{"mission": " research "}`,
		"006-none":     `{}`,
		"007-bad":      `{bad json`,
		"008-list":     `[1,2]`,
		"010-num":      `{"mission_type": 5}`,
	}
	p.Both(func(s *testkit.Sandbox) {
		for slug, m := range metas {
			s.WriteFile("kitty-specs/"+slug+"/meta.json", m)
		}
		s.WriteFile("kitty-specs/009-nometa/spec.md", "x\n")
		s.WriteFile("kitty-specs/README.md", "hi\n")
	})
	for _, args := range [][]string{
		{"doctor", "mission-type"}, {"doctor", "mission-type", "--json"},
		{"doctor", "mission-type", "--mission", "002-unknown", "--fail-on", "unknown,typeless"},
		{"doctor", "mission-type", "--mission", "nope"},
		{"doctor", "mission-type", "--fail-on", "error"},
		{"doctor", "mission-type", "--fail-on", "bogus,resolved"},
	} {
		compareMasked(t, p, true, args...)
	}
}

// ---- Go-only ---------------------------------------------------------------

func TestDoctorOpsGoOnly(t *testing.T) {
	s := newProject(t)
	s.WriteFile("kitty-ops/01KAAAAAAAAAAAAAAAAAAAAAAA.jsonl", "{}\n")
	s.Git("add", "kitty-ops")
	s.Git("commit", "-qm", "track Op records on primary")
	s.Git("checkout", "-q", "-b", "topic")
	writeOps(s)
	r := s.Run("doctor", "ops", "--json")
	if r.Code != 1 || strings.Count(r.Stdout, `"path"`) != 3 || strings.Contains(r.Stdout, "01KDDD") {
		t.Fatalf("doctor ops: %d %s", r.Code, r.Stdout)
	}
	r = s.Run("doctor", "invocation-pairing", "--json")
	if r.Code != 1 || !strings.Contains(r.Stdout, `"orphan_count": 2`) || !strings.Contains(r.Stdout, `"total_groups": 3`) {
		t.Fatalf("pairing: %d %s", r.Code, r.Stdout)
	}
	r = s.Run("doctor", "ops", "--close-stale", "--threshold", "0", "--json")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"swept": 3`) || !strings.Contains(r.Stdout, `"commit": "committed"`) {
		t.Fatalf("sweep: %d %s", r.Code, r.Stdout)
	}
	if !strings.Contains(s.ReadFile("kitty-ops/op-closures.jsonl"), `"closed_by": "doctor_sweep"`) {
		t.Fatal("spine not written")
	}
	if log := s.Git("log", "--format=%s", "-1"); !strings.Contains(log, "(doctor sweep)") {
		t.Fatalf("commit: %s", log)
	}
	r = s.Run("doctor", "ops")
	if r.Code != 0 || !strings.Contains(r.Stdout, "no orphan op records") {
		t.Fatalf("after sweep: %d %s", r.Code, r.Stdout)
	}
}

func TestDoctorMissionTypeGoOnly(t *testing.T) {
	s := newProject(t)
	s.WriteFile("kitty-specs/001-a/meta.json", `{"mission": "research"}`)
	s.WriteFile("kitty-specs/002-b/meta.json", `{bad`)
	r := s.Run("doctor", "mission-type", "--json", "--fail-on", "error")
	if r.Code != 1 || !strings.Contains(r.Stdout, `"legacy-key-only": 1`) || !strings.Contains(r.Stdout, `"error": 1`) {
		t.Fatalf("mission-type: %d %s", r.Code, r.Stdout)
	}
}
