package drivers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// absent marks a side whose file does not exist.
const absent = "\x00absent"

type driverCase struct {
	name               string
	driver             string
	base, ours, theirs string
	env                []string
	args               []string // override argv paths (path-injection cases)
	wantCode           int      // used only by the Go-only run
}

func ev(id, at, extra string) string {
	s := `{"event_id": "` + id + `", "at": "` + at + `"`
	if extra != "" {
		s += ", " + extra
	}
	return s + "}"
}

func lines(xs ...string) string { return strings.Join(xs, "\n") + "\n" }

var driverCases = []driverCase{
	// ---- event log ----
	{name: "clean-union", driver: "merge-driver-event-log",
		base:   lines(ev("01A", "2026-01-01T00:00:00+00:00", `"wp_id": "WP01", "from_lane": "planned", "to_lane": "claimed"`)),
		ours:   lines(ev("01A", "2026-01-01T00:00:00+00:00", `"wp_id": "WP01", "from_lane": "planned", "to_lane": "claimed"`), ev("01C", "2026-01-03T00:00:00+00:00", `"note": "héllo ☃"`)),
		theirs: lines(ev("01A", "2026-01-01T00:00:00+00:00", `"wp_id": "WP01", "from_lane": "planned", "to_lane": "claimed"`), ev("01B", "2026-01-02T00:00:00+00:00", `"z": 1, "a": [1.50, true, null]`))},
	{name: "timestamp-fallback-and-missing-at", driver: "merge-driver-event-log", base: absent,
		ours:   lines(`{"event_id": "X2", "timestamp": "2026-01-01"}`, `{"event_id": "X3"}`, "", "   "),
		theirs: lines(`{"event_id": "X1", "at": null, "timestamp": "2025"}`, `{"event_id": "X0", "at": ""}`)},
	{name: "conflicting-payload", driver: "merge-driver-event-log", base: "",
		ours: lines(ev("E1", "2026", `"x": 1`)), theirs: lines(ev("E1", "2026", `"x": 2`))},
	{name: "equal-numbers-dedupe", driver: "merge-driver-event-log", base: "",
		ours: lines(ev("E1", "2026", `"x": 1`)), theirs: lines(ev("E1", "2026", `"x": 1.0`))},
	{name: "malformed-json", driver: "merge-driver-event-log", base: "",
		ours: lines(ev("E1", "2026", "")), theirs: lines(ev("E2", "2026", ""), `{"event_id": "E3",`)},
	{name: "not-object", driver: "merge-driver-event-log", base: "", ours: "[1, 2]\n", theirs: ""},
	{name: "missing-event-id", driver: "merge-driver-event-log", base: "", ours: `{"at": "x"}` + "\n", theirs: `{"event_id": "  "}` + "\n"},
	{name: "concurrent-fork", driver: "merge-driver-event-log",
		base:   lines(ev("B1", "2026-01-01", `"wp_id": "WP02", "from_lane": "for_review", "to_lane": "in_review"`)),
		ours:   lines(ev("B1", "2026-01-01", `"wp_id": "WP02", "from_lane": "for_review", "to_lane": "in_review"`), ev("O1", "2026-01-02", `"wp_id": "WP02", "from_lane": "in_review", "to_lane": "planned"`)),
		theirs: lines(ev("B1", "2026-01-01", `"wp_id": "WP02", "from_lane": "for_review", "to_lane": "in_review"`), ev("T1", "2026-01-03", `"wp_id": "WP02", "from_lane": "in_review", "to_lane": "approved"`))},
	{name: "forward-fork-is-clean", driver: "merge-driver-event-log", base: "",
		ours:   lines(ev("O1", "2026-01-02", `"wp_id": "WP02", "from_lane": "planned", "to_lane": "in_progress"`)),
		theirs: lines(ev("T1", "2026-01-03", `"wp_id": "WP02", "from_lane": "planned", "to_lane": "for_review"`))},
	{name: "cancel-fork", driver: "merge-driver-event-log", base: "",
		ours:   lines(ev("O1", "2026-01-02", `"wp_id": "WP03", "from_lane": "in_progress", "to_lane": "canceled"`)),
		theirs: lines(ev("T1", "2026-01-03", `"wp_id": "WP03", "from_lane": "in_progress", "to_lane": "for_review"`))},
	{name: "crlf-and-unicode-escapes", driver: "merge-driver-event-log", base: absent,
		ours: "{\"event_id\": \"U1\", \"at\": \"1\", \"s\": \"\\ud83d\\ude00 \\u00e9\"}\r\n", theirs: absent},
	{name: "path-injection", driver: "merge-driver-event-log", base: "", ours: "", theirs: "",
		args: []string{"m/base", "m/ours", "other/theirs"}},

	// ---- meta ----
	{name: "meta-two-way", driver: "merge-driver-meta", base: "",
		ours:   `{"mission_slug": "001-x", "accepted_at": "2026", "accepted_by": "me", "mission_number": 3, "status": "accepted", "merged_at": "m", "acceptance_history": [{"accepted_at": "2", "accepted_by": "b"}]}`,
		theirs: `{"mission_slug": "001-y", "friendly_name": "Ünïcode", "mission_number": null, "status": "active", "acceptance_history": [{"accepted_at": "1", "accepted_by": "a"}, {"accepted_by": "b", "accepted_at": "2"}]}`},
	{name: "meta-two-way-unassigned-number", driver: "merge-driver-meta", base: absent,
		ours: `{"mission_number": 0, "vcs": "git"}`, theirs: `{"mission_number": 7, "vcs": "jj", "x": 1}`},
	{name: "meta-three-way", driver: "merge-driver-meta",
		base:   `{"a": 1, "b": 2, "c": 3, "status": "active", "topology": "flat", "coordination_branch": "x", "discarded_at": null}`,
		ours:   `{"a": 1, "b": 20, "c": 3, "status": "accepted", "topology": "flat", "coordination_branch": "x", "new_o": true}`,
		theirs: `{"a": 10, "b": 2, "status": "discarded", "topology": "coord", "coordination_branch": "y", "flattened": false, "discarded_at": "2026", "new_t": [1]}`},
	{name: "meta-three-way-conflict", driver: "merge-driver-meta",
		base:   `{"friendly_name": "a", "mission_number": 2, "merged_at": "1", "merged_by": "x"}`,
		ours:   `{"friendly_name": "b", "mission_number": null, "merged_at": "2", "merged_by": "x"}`,
		theirs: `{"friendly_name": "c", "mission_number": 5, "merged_at": "3", "merged_by": "y"}`},
	{name: "meta-env-two-way", driver: "merge-driver-meta", env: []string{"SPEC_KITTY_META_MERGE_TWO_WAY=1"},
		base: `{"a": 1, "b": 2}`, ours: `{"a": 1, "status": "x"}`, theirs: `{"b": 2}`},
	{name: "meta-empty-ours-with-base", driver: "merge-driver-meta", base: `{"a": 1}`, ours: "  \n", theirs: `{"a": 2}`},
	{name: "meta-malformed", driver: "merge-driver-meta", base: "", ours: `{"a": 1,}`, theirs: `{}`},
	{name: "meta-not-object", driver: "merge-driver-meta", base: "", ours: `{}`, theirs: `[1]`},
	{name: "meta-base-malformed", driver: "merge-driver-meta", base: `nope`, ours: `{}`, theirs: `{}`},
	{name: "meta-trailing-data", driver: "merge-driver-meta", base: "", ours: `{} {}`, theirs: `{}`},

	// ---- traces ----
	{name: "traces-union", driver: "merge-driver-traces",
		base:   "# Trace\n\n## A\nold a\n\n## B\nb\n",
		ours:   "# Trace\n\n## A\nnew a\n\n## B\nb\n```\n## not a heading\n```\n",
		theirs: "# Trace\n\n## A\nold a\n\n## B\nb\n\n## C\nc\n~~~\n## fenced\n````\n~~~\n<!-- section: s1 -->\nsec\n"},
	{name: "traces-absent-ours", driver: "merge-driver-traces", base: absent, ours: absent, theirs: "x\r\ny\n"},
	{name: "traces-empty", driver: "merge-driver-traces", base: "", ours: "", theirs: ""},
	{name: "traces-dup-headings", driver: "merge-driver-traces",
		base: "## H\n1\n## H\n2\n", ours: "## H\n1\n## H\n2x\n", theirs: "## H\n1\n## H\n2\n## H\n3\n"},
	{name: "traces-non-utf8", driver: "merge-driver-traces", base: "", ours: "ok\n", theirs: "\xff\xfe\n"},

	// ---- issue matrix ----
	{name: "issue-union", driver: "merge-driver-issue-matrix",
		base:   `{"schema_version": 1, "rows": {"#1": {"verdict": "unknown", "notes": "b"}, "#9": {"verdict": "fixed"}}}`,
		ours:   `{"schema_version": 1, "rows": {"GH-1": {"verdict": "fixed", "notes": "o"}, "#9": {"verdict": "fixed"}, "#3": {"verdict": "x"}}}`,
		theirs: `{"schema_version": 1, "rows": {"1": {"verdict": "unknown", "notes": "t", "extra": "é"}, "#2": {"verdict": "wontfix"}}}`},
	{name: "issue-verdict-conflict", driver: "merge-driver-issue-matrix", base: "",
		ours: `{"rows": {"#5": {"verdict": "fixed"}}}`, theirs: `{"rows": {"5": {"verdict": "deferred"}}}`},
	{name: "issue-intra-side-dup", driver: "merge-driver-issue-matrix", base: "",
		ours: `{"rows": {"#5": {"verdict": "fixed"}, "GH-5": {"verdict": "x"}}}`, theirs: `{}`},
	{name: "issue-malformed", driver: "merge-driver-issue-matrix", base: "", ours: `{"rows": `, theirs: `{}`},
	{name: "issue-not-object", driver: "merge-driver-issue-matrix", base: "", ours: `"s"`, theirs: `{}`},
	{name: "issue-non-numeric-keys", driver: "merge-driver-issue-matrix", base: absent,
		ours: `{"rows": {" abc ": {"v": 1}, "x": 5}}`, theirs: `{"rows": {"abc": {"v": 1}}}`},

	// ---- acceptance matrix ----
	{name: "acceptance-union", driver: "merge-driver-acceptance-matrix",
		base: `{"mission_slug": "012-demo", "criteria": [{"criterion_id": "AC1", "description": "d", "proof_type": "test", "pass_fail": "pending"}]}`,
		ours: `{"mission_slug": "012-demo", "mission_type": "software-dev", "overall_verdict": "fail", "criteria": [{"criterion_id": "AC1", "description": "d", "proof_type": "test", "pass_fail": "pass", "evidence": "e"}],
			"negative_invariants": [{"invariant_id": "NI1", "description": "x", "verification_method": "grep", "result": "confirmed_absent", "scope": "src"}]}`,
		theirs: `{"mission_slug": "", "mission_number": "12", "criteria": [{"criterion_id": "AC1", "description": "d", "proof_type": "test", "pass_fail": "pending"}, {"criterion_id": "AC2", "description": "d2", "proof_type": "manual", "custom": 1}]}`},
	{name: "acceptance-verdict-conflict", driver: "merge-driver-acceptance-matrix", base: "",
		ours:   `{"mission_slug": "s", "criteria": [{"criterion_id": "AC1", "description": "d", "proof_type": "t", "pass_fail": "pass"}]}`,
		theirs: `{"mission_slug": "s", "criteria": [{"criterion_id": "AC1", "description": "d", "proof_type": "t", "pass_fail": "fail"}]}`},
	{name: "acceptance-missing-field", driver: "merge-driver-acceptance-matrix", base: "",
		ours: `{"mission_slug": "s", "criteria": [{"criterion_id": "AC1"}]}`, theirs: `{}`},
	{name: "acceptance-conflict-marker", driver: "merge-driver-acceptance-matrix", base: "",
		ours: `{"mission_slug": "s", "criteria": [{"criterion_id": "AC1", "description": "<<<<<<< ours", "proof_type": "t"}]}`, theirs: `{}`},
	{name: "acceptance-all-empty", driver: "merge-driver-acceptance-matrix", base: absent, ours: "", theirs: absent},
	{name: "acceptance-deferred", driver: "merge-driver-acceptance-matrix", base: "",
		ours: `{"mission_slug": "s", "criteria": [{"criterion_id": "AC1", "description": "d", "proof_type": "t", "pass_fail": "pass"}],
			"negative_invariants": [{"invariant_id": "N1", "description": "x", "verification_method": "m", "result": "deferred_to_consolidation", "provenance_origin": "recorded", "deferred_reason": "r"}]}`,
		theirs: `{}`},

	// ---- review cycle ----
	{name: "review-identical", driver: "merge-driver-review-cycle", base: absent, ours: "---\nverdict: approved\n---\n", theirs: "---\nverdict: approved\n---\n"},
	{name: "review-collision", driver: "merge-driver-review-cycle", base: "", ours: "verdict: approved\n", theirs: "verdict: rejected\n"},
	{name: "review-absent-theirs", driver: "merge-driver-review-cycle", base: "", ours: "x", theirs: absent},

	// ---- decision index ----
	{name: "decision-union", driver: "merge-driver-decision-index", base: absent,
		ours: `{"version": 1, "mission_id": "M1", "entries": [` +
			`{"decision_id": "D2", "origin_flow": "plan", "step_id": "s", "input_key": "k", "question": "q?", "status": "resolved", "final_answer": "yes", "created_at": "2026-01-02T00:00:00+00:00", "resolved_at": "2026-01-03T00:00:00.5+00:00", "mission_id": "M1", "mission_slug": "001-x"},` +
			`{"decision_id": "D1", "origin_flow": "specify", "slot_key": "sl", "input_key": "k", "question": "q", "options": ["a", "b"], "status": "open", "created_at": "2026-01-01T00:00:00", "mission_id": "M1", "mission_slug": "001-x"}]}`,
		theirs: `{"version": 1, "mission_id": "", "entries": [` +
			`{"decision_id": "D2", "origin_flow": "plan", "step_id": "s", "input_key": "k", "question": "q?", "status": "open", "created_at": "2026-01-02T00:00:00+00:00", "mission_id": "M1", "mission_slug": "001-x"},` +
			`{"decision_id": "D0", "origin_flow": "charter", "step_id": "s", "input_key": "k", "question": "q", "status": "deferred", "other_answer": "yes", "summary_json": {"a": "b"}, "created_at": 1700000000, "mission_id": "M1", "mission_slug": "001-x"}]}`},
	{name: "decision-reopen", driver: "merge-driver-decision-index", base: "",
		ours:   `{"mission_id": "M", "entries": [{"decision_id": "D", "origin_flow": "plan", "step_id": "s", "input_key": "k", "question": "q", "status": "deferred", "created_at": "2026-01-01T00:00:00Z", "mission_id": "M", "mission_slug": "m"}]}`,
		theirs: `{"mission_id": "M", "entries": [{"decision_id": "D", "origin_flow": "plan", "step_id": "s", "input_key": "k", "question": "q", "status": "resolved", "created_at": "2026-01-01T00:00:00Z", "mission_id": "M", "mission_slug": "m"}]}`},
	{name: "decision-conflict", driver: "merge-driver-decision-index", base: "",
		ours:   `{"mission_id": "M", "entries": [{"decision_id": "D", "status": "canceled"}]}`,
		theirs: `{"mission_id": "M", "entries": [{"decision_id": "D", "status": "resolved"}]}`},
	{name: "decision-bad-status", driver: "merge-driver-decision-index", base: "",
		ours: `{"entries": [{"decision_id": "D", "status": "x"}]}`, theirs: `{"entries": [{"decision_id": "D", "status": "open"}]}`},
	{name: "decision-malformed-entries", driver: "merge-driver-decision-index", base: "", ours: `{"entries": {"a": 1}}`, theirs: `{}`},
	{name: "decision-missing-id", driver: "merge-driver-decision-index", base: "", ours: `{}`, theirs: `{"entries": [{"decision_id": ""}]}`},
	{name: "decision-schema-errors", driver: "merge-driver-decision-index", base: "",
		ours: `{"version": 2, "extra": null, "entries": [{"decision_id": "D", "origin_flow": "nope", "input_key": "", "question": "q", "options": "abc", "status": "open", "created_at": "2026-13-01", "mission_id": 5, "mission_slug": "m", "bogus": 1}, ` +
			`{"decision_id": "E", "origin_flow": "plan", "input_key": "k", "question": "q", "status": "open", "created_at": "2026-01-01T00:00:00Z", "mission_id": "M", "mission_slug": "m"}]}`,
		theirs: `{}`},
	{name: "decision-empty", driver: "merge-driver-decision-index", base: "", ours: "", theirs: absent},
}

func setup(t *testing.T, s *testkit.Sandbox, c driverCase) {
	t.Helper()
	for name, content := range map[string]string{"base": c.base, "ours": c.ours, "theirs": c.theirs} {
		if content == absent {
			continue
		}
		p := filepath.Join(s.Dir, "m", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(s.Dir, "other"), 0o755)
	s.Env = append(s.Env, c.env...)
}

func caseArgs(c driverCase) []string {
	if c.args != nil {
		return append([]string{c.driver}, c.args...)
	}
	return []string{c.driver, "m/base", "m/ours", "m/theirs"}
}

func readOurs(s *testkit.Sandbox) string {
	b, err := os.ReadFile(filepath.Join(s.Dir, "m", "ours"))
	if err != nil {
		return "<missing>"
	}
	return strings.ReplaceAll(string(b), s.Dir, "<PROJ>")
}

// TestDriversParity runs every case through both binaries and compares exit
// code, stdout, stderr (unless upstream crashed with a traceback) and the
// resulting OURS bytes.
func TestDriversParity(t *testing.T) {
	testkit.Oracle(t)
	for _, c := range driverCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, o := testkit.NewSandbox(t), testkit.NewSandbox(t)
			setup(t, g, c)
			setup(t, o, c)
			gr, or := g.Run(caseArgs(c)...), o.RunOracle(caseArgs(c)...)
			if gr.Code != or.Code {
				t.Fatalf("exit go=%d oracle=%d\ngo stderr: %s\noracle stderr: %s", gr.Code, or.Code, gr.Stderr, or.Stderr)
			}
			norm := func(s *testkit.Sandbox, x string) string { return strings.ReplaceAll(x, s.Dir, "<PROJ>") }
			if a, b := norm(g, gr.Stdout), norm(o, or.Stdout); a != b {
				t.Errorf("stdout differs\n%s", testkit.Diff(b, a))
			}
			if !strings.Contains(or.Stderr, "Traceback") {
				if a, b := norm(g, gr.Stderr), norm(o, or.Stderr); a != b {
					t.Errorf("stderr differs\n%s", testkit.Diff(b, a))
				}
			}
			if a, b := readOurs(g), readOurs(o); a != b {
				t.Errorf("OURS differs\n%s", testkit.Diff(b, a))
			}
		})
	}
}

// TestDriversGo checks exit codes without the oracle.
func TestDriversGo(t *testing.T) {
	want := map[string]int{
		"clean-union": 0, "conflicting-payload": 1, "malformed-json": 1, "concurrent-fork": 1, "forward-fork-is-clean": 0,
		"path-injection": 1, "meta-two-way": 0, "meta-empty-ours-with-base": 1, "traces-union": 0, "traces-non-utf8": 1,
		"issue-verdict-conflict": 1, "acceptance-missing-field": 1, "review-collision": 0, "decision-union": 0,
		"decision-conflict": 1, "decision-schema-errors": 1,
	}
	for _, c := range driverCases {
		code, ok := want[c.name]
		if !ok {
			continue
		}
		c := c
		t.Run(c.name, func(t *testing.T) {
			s := testkit.NewSandbox(t)
			setup(t, s, c)
			if r := s.Run(caseArgs(c)...); r.Code != code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.Code, code, r.Stdout, r.Stderr)
			}
		})
	}
}
