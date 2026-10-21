package orchestrator

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// stepF runs per-side argv (built by fn for each sandbox, with the side's
// runner) and compares exit code + normalized stdout.
func stepF(t *testing.T, p *testkit.Pair, fn func(s *testkit.Sandbox, run func(...string) testkit.Result) []string) {
	t.Helper()
	ga := fn(p.Go, p.Go.Run)
	oa := fn(p.Oracle, p.Oracle.RunOracle)
	g, o := p.Go.Run(ga...), p.Oracle.RunOracle(oa...)
	if os.Getenv("KITTY_TEST_TRACE") == "1" {
		t.Logf("$ %s\n[oracle %d] %s%s\n[go %d] %s%s", strings.Join(oa, " "), o.Code, o.Stdout, o.Stderr, g.Code, g.Stdout, g.Stderr)
	}
	if g.Code != o.Code {
		t.Errorf("%s: exit code go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", strings.Join(oa, " "), g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	if gs, os_ := normEnvelope(p.Go, g.Stdout), normEnvelope(p.Oracle, o.Stdout); gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(oa, " "), testkit.Diff(os_, gs))
	}
}

// dataField reads data.<key> (string) from an envelope.
func dataField(t *testing.T, r testkit.Result, key string) string {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil {
		t.Fatalf("envelope: %v\n%s%s", err, r.Stdout, r.Stderr)
	}
	s, _ := env.Data[key].(string)
	return s
}

func submitRequest(t *testing.T, context string, parents map[string]string, artifacts ...map[string]any) string {
	t.Helper()
	req := map[string]any{"artifacts": artifacts, "context_sha256": context}
	if parents != nil {
		req["parents"] = parents
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const outlineYAML = `work_packages:
  - id: WP01
    title: Subtraction and shared helper
    dependencies: []
    owned_files: [src/calc.py]
    requirement_refs: [FR-001]
    subtasks: [T001]
  - id: WP02
    title: Multiply
    dependencies: [WP01]
    owned_files: [src/calc_multiply.py]
    requirement_refs: [FR-002]
    subtasks: [T002]
`

func wpDraft(id, title, deps, owned, ref, sub string) string {
	return "---\nwork_package_id: " + id + "\ntitle: " + title + "\ndependencies: " + deps + "\nrequirement_refs: [" + ref +
		"]\nowned_files: [" + owned + "]\nsubtasks: [" + sub + "]\nexecution_mode: code_change\n---\n# " + id + "\n\nImplement " + title + ".\n"
}

// TestDiffDesignAuthoring drives the governed planning surface: specify,
// interview-record, artifact-read/submit, design-validate, plan,
// design-status and the decision verbs.
func TestDiffDesignAuthoring(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	m := "calc-ops"
	step(t, p, api, "design-context", "--mission-type", "software-dev")
	step(t, p, api, "specify", "--mission", m, "--mission-type", "software-dev")
	step(t, p, api, "specify", "--mission", m, "--mission-type", "software-dev", "--topology", "bogus", "--policy", testPolicy)
	step(t, p, api, "specify", "--mission", m, "--mission-type", "bogus", "--policy", testPolicy)
	step(t, p, api, "specify", "--mission", m, "--mission-type", "software-dev", "--policy", testPolicy)
	compareGit(t, p, "after specify")
	step(t, p, api, "design-status", "--mission", m)
	step(t, p, api, "check-prerequisites", "--mission", m)
	step(t, p, api, "artifact-read", "--mission", m, "--kind", "specification")
	step(t, p, api, "design-validate", "--mission", m, "--stage", "specify")
	for _, a := range []string{`{}`, `[1]`, `{"bogus": "x"}`, `{"problem_statement": ""}`, `{`} {
		step(t, p, api, "interview-record", "--mission", m, "--stage", "specify", "--answers", a, "--actor", "me", "--policy", testPolicy)
	}
	step(t, p, api, "interview-record", "--mission", m, "--stage", "tasks", "--answers", `{"x": "y"}`, "--actor", "me", "--policy", testPolicy)
	step(t, p, api, "interview-record", "--mission", m, "--stage", "specify", "--answers",
		`{"problem_statement": "Calc lacks ops", "success_criteria": "ops work", "scope_boundaries": "no division"}`, "--actor", "me", "--policy", testPolicy)
	compareGit(t, p, "after interview")
	step(t, p, api, "design-context", "--mission", m, "--stage", "specify")
	submitSpec := func(expected func(s *testkit.Sandbox, run func(...string) testkit.Result) string) func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		return func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
			slug := missionfx.MissionSlug(t, s)
			ctxSHA := dataField(t, run(api, "design-context", "--mission", m, "--stage", "specify"), "context_sha256")
			spec := strings.ReplaceAll(missionfx.Read(t, "calc/authored/spec.md"), missionfx.LocalSlug, slug)
			req := submitRequest(t, ctxSHA, nil, map[string]any{"kind": "specification", "content": spec, "expected_sha256": expected(s, run)})
			return []string{api, "artifact-submit", "--mission", m, "--actor", "me", "--policy", testPolicy, "--request-json", req}
		}
	}
	stale := func(s *testkit.Sandbox, run func(...string) testkit.Result) string { return strings.Repeat("0", 64) }
	current := func(s *testkit.Sandbox, run func(...string) testkit.Result) string {
		return dataField(t, run(api, "artifact-read", "--mission", m, "--kind", "specification"), "sha256")
	}
	step(t, p, api, "artifact-submit", "--mission", m, "--actor", "me", "--request-json", "{}")
	step(t, p, api, "artifact-submit", "--mission", m, "--actor", "me", "--policy", testPolicy, "--request-json", "{}")
	step(t, p, api, "artifact-submit", "--mission", m, "--actor", "me", "--policy", testPolicy, "--request-json",
		`{"artifacts": [{"kind": "bogus", "content": "x", "expected_sha256": "absent"}], "context_sha256": "absent"}`)
	stepF(t, p, submitSpec(stale))
	stepF(t, p, submitSpec(current))
	compareGit(t, p, "after spec submit")
	step(t, p, api, "artifact-read", "--mission", m, "--kind", "specification")
	step(t, p, api, "design-validate", "--mission", m, "--stage", "specify")
	step(t, p, api, "design-status", "--mission", m)
	step(t, p, api, "plan", "--mission", m)
	step(t, p, api, "plan", "--mission", m, "--policy", testPolicy)
	compareGit(t, p, "after plan")
	step(t, p, api, "interview-record", "--mission", m, "--stage", "plan", "--answers",
		`{"approach": "add functions", "risks": "none", "dependencies": "stdlib"}`, "--actor", "me", "--policy", testPolicy)
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		slug := missionfx.MissionSlug(t, s)
		ctxSHA := dataField(t, run(api, "design-context", "--mission", m, "--stage", "plan"), "context_sha256")
		specSHA := dataField(t, run(api, "artifact-read", "--mission", m, "--kind", "specification"), "sha256")
		planSHA := dataField(t, run(api, "artifact-read", "--mission", m, "--kind", "plan"), "sha256")
		plan := strings.ReplaceAll(missionfx.Read(t, "calc/authored/plan.md"), missionfx.LocalSlug, slug)
		req := submitRequest(t, ctxSHA, map[string]string{"specification": specSHA},
			map[string]any{"kind": "plan", "content": plan, "expected_sha256": planSHA},
			map[string]any{"kind": "research", "content": "# Research\n\nNone needed.\n", "expected_sha256": "absent"})
		return []string{api, "artifact-submit", "--mission", m, "--actor", "me", "--policy", testPolicy, "--request-json", req}
	})
	compareGit(t, p, "after plan submit")
	step(t, p, api, "design-validate", "--mission", m, "--stage", "plan")
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		ctxSHA := dataField(t, run(api, "design-context", "--mission", m, "--stage", "tasks"), "context_sha256")
		specSHA := dataField(t, run(api, "artifact-read", "--mission", m, "--kind", "specification"), "sha256")
		planSHA := dataField(t, run(api, "artifact-read", "--mission", m, "--kind", "plan"), "sha256")
		req := submitRequest(t, ctxSHA, map[string]string{"specification": specSHA, "plan": planSHA},
			map[string]any{"kind": "outline", "content": outlineYAML, "expected_sha256": "absent"},
			map[string]any{"kind": "work_package", "artifact_id": "WP01", "content": wpDraft("WP01", "Subtraction and shared helper", "[]", "src/calc.py", "FR-001", "T001"), "expected_sha256": "absent"},
			map[string]any{"kind": "work_package", "artifact_id": "WP02", "content": wpDraft("WP02", "Multiply", "[WP01]", "src/calc_multiply.py", "FR-002", "T002"), "expected_sha256": "absent"})
		return []string{api, "artifact-submit", "--mission", m, "--actor", "me", "--policy", testPolicy, "--request-json", req}
	})
	compareGit(t, p, "after outline submit")
	step(t, p, api, "artifact-read", "--mission", m, "--kind", "work_package", "--artifact-id", "WP02")
	step(t, p, api, "design-validate", "--mission", m, "--stage", "tasks")
	step(t, p, api, "design-status", "--mission", m)
	// Decision Moment ledger verbs.
	step(t, p, api, "open-decision", "--mission", m, "--origin", "tasks", "--input-key", "k", "--question", "Q?", "--actor", "me", "--policy", testPolicy)
	step(t, p, api, "open-decision", "--mission", m, "--origin", "plan", "--input-key", "k", "--question", "Q?", "--actor", "me", "--policy", testPolicy)
	step(t, p, api, "open-decision", "--mission", m, "--origin", "plan", "--input-key", "k", "--question", "Q?", "--options", "x", "--slot-key", "s", "--actor", "me", "--policy", testPolicy)
	step(t, p, api, "open-decision", "--mission", m, "--origin", "plan", "--input-key", "k", "--question", "Q?", "--options", `{"a": 1}`, "--slot-key", "s", "--actor", "me", "--policy", testPolicy)
	step(t, p, api, "open-decision", "--mission", m, "--origin", "plan", "--input-key", "db", "--question", "Which DB?", "--options", `["sqlite", "pg"]`, "--slot-key", "db", "--actor", "me", "--policy", testPolicy)
	step(t, p, api, "open-decision", "--mission", m, "--origin", "plan", "--input-key", "db", "--question", "Which DB?", "--options", `["sqlite", "pg"]`, "--slot-key", "db", "--actor", "me", "--policy", testPolicy)
	step(t, p, api, "design-status", "--mission", m)
	step(t, p, api, "resolve-decision", "--mission", m, "--decision-id", "01ZZZZZZZZZZZZZZZZZZZZZZZZ", "--final-answer", "x", "--actor", "me", "--policy", testPolicy)
	step(t, p, api, "defer-decision", "--mission", m, "--decision-id", "x", "--rationale", "  ", "--actor", "me", "--policy", testPolicy)
	step(t, p, api, "cancel-decision", "--mission", m, "--decision-id", "x", "--rationale", "r", "--actor", "me", "--policy", testPolicy, "--owned-checkout", "/nope")
	decisionID := func(s *testkit.Sandbox, run func(...string) testkit.Result) string {
		return dataField(t, run(api, "open-decision", "--mission", m, "--origin", "plan", "--input-key", "db", "--question", "Which DB?",
			"--options", `["sqlite", "pg"]`, "--slot-key", "db", "--actor", "me", "--policy", testPolicy), "decision_id")
	}
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		return []string{api, "resolve-decision", "--mission", m, "--decision-id", decisionID(s, run), "--final-answer", "sqlite", "--rationale", "simple", "--actor", "me", "--policy", testPolicy}
	})
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		return []string{api, "resolve-decision", "--mission", m, "--decision-id", decisionID(s, run), "--final-answer", "sqlite", "--rationale", "simple", "--actor", "me", "--policy", testPolicy}
	})
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		return []string{api, "cancel-decision", "--mission", m, "--decision-id", decisionID(s, run), "--rationale", "changed", "--actor", "me", "--policy", testPolicy}
	})
	step(t, p, api, "open-decision", "--mission", m, "--origin", "specify", "--input-key", "auth", "--question", "Auth?", "--step-id", "specify.auth", "--actor", "me", "--policy", testPolicy)
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		id := dataField(t, run(api, "open-decision", "--mission", m, "--origin", "specify", "--input-key", "auth", "--question", "Auth?", "--step-id", "specify.auth", "--actor", "me", "--policy", testPolicy), "decision_id")
		return []string{api, "defer-decision", "--mission", m, "--decision-id", id, "--rationale", "later", "--resolved-by", "lead", "--actor", "me", "--policy", testPolicy}
	})
	step(t, p, api, "design-status", "--mission", m)
	compareGit(t, p, "after decisions")
}

// TestDiffDesignPhaseNative covers tasks / check-prerequisites /
// record-analysis on a natively authored mission.
func TestDiffDesignPhaseNative(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
		missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s))
	})
	m := "calc-ops"
	step(t, p, api, "check-prerequisites", "--mission", m, "--include-tasks")
	step(t, p, api, "design-status", "--mission", m)
	step(t, p, api, "tasks", "--mission", m)
	step(t, p, api, "tasks", "--mission", m, "--policy", testPolicy)
	compareGit(t, p, "after tasks")
	step(t, p, api, "design-status", "--mission", m)
	step(t, p, api, "plan", "--mission", m, "--policy", testPolicy)
	step(t, p, api, "record-analysis", "--mission", m)
	step(t, p, api, "record-analysis", "--mission", m, "--policy", testPolicy, "--input-file", "/nonexistent/report.md")
	stepStdin(t, p, "   \n", api, "record-analysis", "--mission", m, "--policy", testPolicy)
	stepStdin(t, p, "---\nschema: analysis-findings/v1\nfindings: 3\n---\nbody\n", api, "record-analysis", "--mission", m, "--policy", testPolicy)
	stepStdin(t, p, "# Report\n\nNo carrier.\n", api, "record-analysis", "--mission", m, "--policy", testPolicy)
	compareGit(t, p, "after unknown-verdict record-analysis")
	step(t, p, api, "record-analysis", "--mission", m, "--policy", testPolicy, "--input-file", missionfx.AnalysisReport(), "--agent", "claude")
	compareGit(t, p, "after record-analysis")
	step(t, p, api, "design-status", "--mission", m)
	step(t, p, api, "next", "--mission", m)
	step(t, p, api, "next", "--mission", m, "--agent", "claude", "--result", "success")
	step(t, p, api, "next", "--mission", m, "--agent", "claude", "--result", "success", "--policy", testPolicy)
	step(t, p, api, "answer-decision", "--mission", m, "--agent", "claude", "--answer", "yes", "--policy", testPolicy)
	step(t, p, api, "answer-decision", "--mission", m, "--agent", "claude", "--answer", "yes", "--result", "bogus", "--policy", testPolicy)
	step(t, p, api, "answer-decision", "--mission", m, "--agent", "claude", "--answer", "yes", "--result", "success", "--policy", testPolicy)
	step(t, p, api, "answer-decision", "--mission", m, "--agent", "claude", "--answer", "yes", "--result", "success", "--decision-id", "d1", "--policy", testPolicy)
}
