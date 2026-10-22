package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

const api = "orchestrator-api"

// TestDiffEnvelopeAndReads covers the envelope contract (usage errors, bare
// group, contract-version) and the read-only verbs on a finalized
// coordination-topology mission.
func TestDiffEnvelopeAndReads(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	goSlug, oSlug := missionfx.OracleCalcMission(t, p)
	cases := [][]string{
		{api},
		{api, "--bogus"},
		{api, "nosuch"},
		{api, "contract-version"},
		{api, "contract-version", "--provider-version", "0.0.1"},
		{api, "contract-version", "--provider-version", "abc"},
		{api, "contract-version", "--provider-version", "v1.0rc1"},
		{api, "contract-version", "--provider-version", "0.1.0.dev1"},
		{api, "contract-version", "--require-capability", "nope"},
		{api, "contract-version", "--require-capability", "next"},
		{api, "mission-state"},
		{api, "mission-state", "--mission"},
		{api, "mission-state", "--mission", "x", "--bogus"},
		{api, "mission-state", "--mission", "x"},
		{api, "mission-state", "--mission", "../x"},
		{api, "mission-state", "--mission", ".x"},
		{api, "mission-state", "--mission", "a b"},
		{api, "mission-state", "--mission", "calc-ops"},
		{api, "list-ready", "--mission", "calc-ops"},
		{api, "resolve-workspace", "--mission", "calc-ops", "--wp", "WP01"},
		{api, "resolve-workspace", "--mission", "calc-ops", "--wp", "WP09"},
		{api, "design-status", "--mission", "calc-ops"},
		{api, "check-prerequisites", "--mission", "calc-ops"},
		{api, "check-prerequisites", "--mission", "calc-ops", "--include-tasks"},
		{api, "design-validate", "--mission", "calc-ops", "--stage", "plan"},
		{api, "design-validate", "--mission", "calc-ops", "--stage", "bogus"},
		{api, "artifact-read", "--mission", "calc-ops", "--kind", "specification"},
		{api, "artifact-read", "--mission", "calc-ops", "--kind", "outline"},
		{api, "artifact-read", "--mission", "calc-ops", "--kind", "work_package", "--artifact-id", "WP01"},
		{api, "artifact-read", "--mission", "calc-ops", "--kind", "contract", "--artifact-id", "api.md"},
		{api, "artifact-read", "--mission", "calc-ops", "--kind", "contract", "--artifact-id", "../x"},
		{api, "artifact-read", "--mission", "calc-ops", "--kind", "bogus"},
		{api, "transition", "--mission", "calc-ops", "--wp", "WP01", "--to", "doing", "--actor", "a"},
		{api, "start-review", "--mission", "calc-ops", "--wp", "WP01", "--actor", "a"},
		{api, "start-implementation", "--mission", "calc-ops", "--wp", "WP01", "--actor", "a", "--policy", `{"orchestrator_id": 1}`},
		{api, "start-implementation", "--mission", "calc-ops", "--wp", "WP01", "--actor", "a", "--policy", `[1`},
		{api, "start-implementation", "--mission", "calc-ops", "--wp", "WP01", "--actor", "a", "--policy",
			`{"orchestrator_id":"o","orchestrator_version":"1","agent_family":"x","approval_mode":"a","sandbox_mode":"s","network_mode":"n","dangerous_flags":["--yolo"]}`},
		{api, "start-implementation", "--mission", "calc-ops", "--wp", "WP01", "--actor", "a", "--policy",
			`{"orchestrator_id":"o","orchestrator_version":"1","agent_family":"x","approval_mode":"a","sandbox_mode":"s","network_mode":"n","dangerous_flags":[],"extra":{"k":["my-token"]}}`},
		{api, "start-implementation", "--mission", "calc-ops", "--wp", "WP02", "--actor", "a", "--policy", testPolicy},
	}
	for _, c := range cases {
		step(t, p, c...)
	}
	_ = goSlug
	_ = oSlug
}

// TestDiffWPLifecycle drives start-implementation -> transition ->
// start-review -> approval and the history/state reads (coord topology).
func TestDiffWPLifecycle(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	goSlug, oSlug := missionfx.OracleCalcMission(t, p)
	m := "calc-ops"
	step(t, p, api, "start-implementation", "--mission", m, "--wp", "WP01", "--actor", "claude")
	step(t, p, api, "start-implementation", "--mission", m, "--wp", "WP01", "--actor", "claude", "--policy", testPolicy)
	compareGit(t, p, "after start-implementation")
	compareFiles(t, p, goSlug, oSlug, ".kittify/workspaces/calc-ops-lane-a.json")
	step(t, p, api, "start-implementation", "--mission", m, "--wp", "WP01", "--actor", "claude", "--policy", testPolicy)
	step(t, p, api, "start-implementation", "--mission", m, "--wp", "WP01", "--actor", "other", "--policy", testPolicy)
	step(t, p, api, "resolve-workspace", "--mission", m, "--wp", "WP01")
	step(t, p, api, "mission-state", "--mission", m)
	step(t, p, api, "list-ready", "--mission", m)
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "for_review", "--actor", "claude")
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "for_review", "--actor", "claude", "--policy", testPolicy)
	p.Both(func(s *testkit.Sandbox) {
		wt := filepath.Join(s.Dir, ".worktrees", "calc-ops-lane-a")
		if err := os.WriteFile(filepath.Join(wt, "src", "calc.py"), []byte("def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s.Exec(wt, "git", "add", "src/calc.py")
		s.Exec(wt, "git", "commit", "-q", "-m", "feat: subtract")
	})
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "for_review", "--actor", "claude", "--policy", testPolicy,
		"--subtasks-complete", "--implementation-evidence-present", "--note", "ready")
	step(t, p, api, "append-history", "--mission", m, "--wp", "WP01", "--actor", "claude", "--note", "implemented subtract")
	step(t, p, api, "append-history", "--mission", m, "--wp", "WP07", "--actor", "claude", "--note", "x")
	step(t, p, api, "start-review", "--mission", m, "--wp", "WP01", "--actor", "reviewer", "--policy", testPolicy)
	step(t, p, api, "start-review", "--mission", m, "--wp", "WP01", "--actor", "reviewer", "--policy", testPolicy)
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "approved", "--actor", "reviewer", "--policy", testPolicy,
		"--review-result-json", `{"reviewer": "reviewer", "verdict": "approved", "reference": "review-1"}`)
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "approved", "--actor", "reviewer", "--review-result-json", `{"verdict": 1}`)
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "done", "--actor", "reviewer", "--evidence-json", `[1]`)
	step(t, p, api, "transition", "--mission", m, "--wp", "WP01", "--to", "done", "--actor", "reviewer", "--evidence-json", `{`)
	step(t, p, api, "mission-state", "--mission", m)
	step(t, p, api, "list-ready", "--mission", m)
	compareGit(t, p, "after review")
	compareFiles(t, p, goSlug, oSlug,
		".worktrees/{slug}-coord/kitty-specs/{slug}/status.json",
		".worktrees/{slug}-coord/kitty-specs/{slug}/status.events.jsonl")
	step(t, p, api, "start-implementation", "--mission", m, "--wp", "WP02", "--actor", "claude", "--policy", testPolicy)
	compareGit(t, p, "after WP02 claim")
}
