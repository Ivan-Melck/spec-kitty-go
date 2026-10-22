package orchestrator

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// authorThroughPlan drives specify -> spec submit -> plan -> plan submit via
// the API (comparing every step) so the next submit is the tasks stage.
func authorThroughPlan(t *testing.T, p *testkit.Pair, m string) {
	t.Helper()
	p.Both(func(s *testkit.Sandbox) { s.Git("switch", "-q", "-c", "feat/calc-ops") })
	step(t, p, api, "specify", "--mission", m, "--mission-type", "software-dev", "--policy", testPolicy)
	step(t, p, api, "interview-record", "--mission", m, "--stage", "specify", "--answers",
		`{"problem_statement": "Calc lacks ops", "success_criteria": "ops work", "scope_boundaries": "no division"}`, "--actor", "me", "--policy", testPolicy)
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		slug := missionfx.MissionSlug(t, s)
		ctxSHA := dataField(t, run(api, "design-context", "--mission", m, "--stage", "specify"), "context_sha256")
		cur := dataField(t, run(api, "artifact-read", "--mission", m, "--kind", "specification"), "sha256")
		spec := strings.ReplaceAll(missionfx.Read(t, "calc/authored/spec.md"), missionfx.LocalSlug, slug)
		req := submitRequest(t, ctxSHA, nil, map[string]any{"kind": "specification", "content": spec, "expected_sha256": cur})
		return []string{api, "artifact-submit", "--mission", m, "--actor", "me", "--policy", testPolicy, "--request-json", req}
	})
	step(t, p, api, "plan", "--mission", m, "--policy", testPolicy)
	step(t, p, api, "interview-record", "--mission", m, "--stage", "plan", "--answers",
		`{"approach": "add functions", "risks": "none", "dependencies": "stdlib"}`, "--actor", "me", "--policy", testPolicy)
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		slug := missionfx.MissionSlug(t, s)
		ctxSHA := dataField(t, run(api, "design-context", "--mission", m, "--stage", "plan"), "context_sha256")
		specSHA := dataField(t, run(api, "artifact-read", "--mission", m, "--kind", "specification"), "sha256")
		planSHA := dataField(t, run(api, "artifact-read", "--mission", m, "--kind", "plan"), "sha256")
		plan := strings.ReplaceAll(missionfx.Read(t, "calc/authored/plan.md"), missionfx.LocalSlug, slug)
		req := submitRequest(t, ctxSHA, map[string]string{"specification": specSHA},
			map[string]any{"kind": "plan", "content": plan, "expected_sha256": planSHA})
		return []string{api, "artifact-submit", "--mission", m, "--actor", "me", "--policy", testPolicy, "--request-json", req}
	})
	compareGit(t, p, "after plan submit")
}

// wpDraftOwned is wpDraft plus the ownership fields finalize requires.
func wpDraftOwned(id, title, deps, owned, ref, sub string) string {
	return strings.Replace(wpDraft(id, title, deps, owned, ref, sub), "execution_mode: code_change\n",
		"execution_mode: code_change\nauthoritative_surface: "+owned+"\ncreate_intent: ["+owned+"]\n", 1)
}

func outlineSubmit(t *testing.T, m string, withWPs bool) func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
	return outlineSubmitWith(t, m, withWPs, wpDraft)
}

func outlineSubmitWith(t *testing.T, m string, withWPs bool, draft func(id, title, deps, owned, ref, sub string) string) func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
	return func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		ctxSHA := dataField(t, run(api, "design-context", "--mission", m, "--stage", "tasks"), "context_sha256")
		specSHA := dataField(t, run(api, "artifact-read", "--mission", m, "--kind", "specification"), "sha256")
		planSHA := dataField(t, run(api, "artifact-read", "--mission", m, "--kind", "plan"), "sha256")
		arts := []map[string]any{{"kind": "outline", "content": outlineYAML, "expected_sha256": "absent"}}
		if withWPs {
			arts = append(arts,
				map[string]any{"kind": "work_package", "artifact_id": "WP01", "content": draft("WP01", "Subtraction and shared helper", "[]", "src/calc.py", "FR-001", "T001"), "expected_sha256": "absent"},
				map[string]any{"kind": "work_package", "artifact_id": "WP02", "content": draft("WP02", "Multiply", "[WP01]", "src/calc_multiply.py", "FR-002", "T002"), "expected_sha256": "absent"})
		}
		req := submitRequest(t, ctxSHA, map[string]string{"specification": specSHA, "plan": planSHA, "outline": sha256Hex(outlineYAML)}, arts...)
		return []string{api, "artifact-submit", "--mission", m, "--actor", "me", "--policy", testPolicy, "--request-json", req}
	}
}

// TestDiffDesignTasksFromOutline finalizes an API-authored mission whose
// tasks stage is only the wps.yaml outline plus WP prompts (no hand-written
// tasks.md): the outline-only validation, the `tasks` verb (tasks.md
// regenerated from wps.yaml, receipts re-stamped) and the native
// finalize-tasks API-authored scope.
func TestDiffDesignTasksFromOutline(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	m := "calc-ops"
	authorThroughPlan(t, p, m)
	stepF(t, p, outlineSubmit(t, m, false))
	compareGit(t, p, "after outline-only submit")
	step(t, p, api, "design-status", "--mission", m)
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		return []string{"agent", "mission", "finalize-tasks", "--mission", missionfx.MissionSlug(t, s), "--validate-only", "--json"}
	})
	step(t, p, api, "tasks", "--mission", m, "--policy", testPolicy)
	compareGit(t, p, "after outline-only tasks")
	stepF(t, p, outlineSubmit(t, m, true))
	compareGit(t, p, "after outline submit")
	// A WP prompt edited outside its receipt: the native finalize refuses
	// (API-authored scope) before any write, like the tasks verb.
	p.Both(func(s *testkit.Sandbox) {
		s.Exec("", "bash", "-c", "echo extra >> kitty-specs/*/tasks/WP02*.md")
	})
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		return []string{"agent", "mission", "finalize-tasks", "--mission", missionfx.MissionSlug(t, s), "--json"}
	})
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		return []string{"agent", "mission", "finalize-tasks", "--mission", missionfx.MissionSlug(t, s)}
	})
	step(t, p, api, "tasks", "--mission", m, "--policy", testPolicy)
	p.Both(func(s *testkit.Sandbox) { s.Exec("", "bash", "-c", "git checkout -q -- kitty-specs") })
	step(t, p, api, "tasks", "--mission", m, "--policy", testPolicy)
	compareGit(t, p, "after tasks")
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".kittify/derived", true)
	step(t, p, api, "design-status", "--mission", m)
	step(t, p, api, "tasks", "--mission", m, "--policy", testPolicy)
	compareGit(t, p, "after re-run tasks")
}

// TestDiffNativeFinalizeAPIAuthored runs the native finalize-tasks on an
// API-authored mission (native_finalization_scope, #5879): success re-stamps
// the work-package receipts.
func TestDiffNativeFinalizeAPIAuthored(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	m := "calc-ops"
	authorThroughPlan(t, p, m)
	stepF(t, p, outlineSubmitWith(t, m, true, wpDraftOwned))
	stepF(t, p, func(s *testkit.Sandbox, run func(...string) testkit.Result) []string {
		return []string{"agent", "mission", "finalize-tasks", "--mission", missionfx.MissionSlug(t, s), "--json"}
	})
	compareGit(t, p, "after native finalize")
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".kittify/derived", true)
	step(t, p, api, "design-status", "--mission", m)
	step(t, p, api, "design-validate", "--mission", m, "--stage", "tasks")
}
