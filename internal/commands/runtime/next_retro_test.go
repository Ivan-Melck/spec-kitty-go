package runtime_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// runLockFiles lists, per run directory, whether its state.json.lock
// sidecar exists (run ids differ between the two sides).
func runLockFiles(s *testkit.Sandbox) string {
	runs, _ := os.ReadDir(filepath.Join(s.Dir, ".kittify", "runtime", "runs"))
	var out []string
	for _, r := range runs {
		_, err := os.Stat(filepath.Join(s.Dir, ".kittify", "runtime", "runs", r.Name(), "state.json.lock"))
		out = append(out, "run lock="+map[bool]string{true: "present", false: "absent"}[err == nil])
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func compareRunLocks(t *testing.T, p *testkit.Pair) {
	t.Helper()
	if g, o := runLockFiles(p.Go), runLockFiles(p.Oracle); g != o {
		t.Errorf("run lock sidecars differ:\n%s", testkit.Diff(o, g))
	}
}

// TestDiffNextTerminalRetrospective: the advance that completes a mission's
// last step runs the terminal retrospective (retrospective.yaml plus a
// RetrospectiveCaptured status event) and every advance takes the run's
// state.json.lock cursor lock. Policy sources select the skip and failure
// paths: a disabled policy captures nothing, a malformed one records
// RetrospectiveCaptureFailed, an existing record is kept, the strict
// before_completion/block policy captures as runtime_strict_gate, and
// legacy env vars are observed in the source map.
func TestDiffNextTerminalRetrospective(t *testing.T) {
	next := []string{"next", "--agent", "codex", "--mission", "$M", "--result", "success", "--json"}
	appendConfig := func(block string) func(s *testkit.Sandbox) {
		return func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+block)
		}
	}
	cases := []struct {
		name  string
		setup func(s *testkit.Sandbox)
	}{
		{"default", func(*testkit.Sandbox) {}},
		{"disabled", appendConfig("retrospective:\n  enabled: false\n")},
		{"malformed-config", appendConfig("retrospective:\n  timing: bogus\n")},
		{"existing-record", func(s *testkit.Sandbox) {
			s.WriteFile(filepath.Join("kitty-specs", missionfx.MissionSlug(s.T, s), "retrospective.yaml"), "keep: me\n")
		}},
		{"strict-config", appendConfig("retrospective:\n  timing: before_completion\n  failure_policy: block\n")},
		{"unknown-keys-strict", appendConfig("retrospective:\n  strict_keys: true\n  bogus: 1\n  permissions:\n    nope: true\n")},
		{"bad-permission", appendConfig("retrospective:\n  permissions:\n    write_record: 'yes'\n")},
		{"charter-md-precedence", func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/charter/charter.md",
				"---\nretrospective:\n  precedence: config\n  enabled: true\n---\n# Charter\n")
			appendConfig("retrospective:\n  enabled: false\n  failure_policy: block\n  permissions:\n    write_record: false\n")(s)
		}},
		{"charter-md-invalid", func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/charter/charter.md", "---\nretrospective: 3\n---\n# Charter\n")
		}},
		{"env-observed", func(s *testkit.Sandbox) {
			s.Env = append(s.Env, "SPEC_KITTY_RETROSPECTIVE=1", "SPEC_KITTY_MODE=x")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newPlanPair(t, "")
			// specify, research, plan issued/completed; review issued.
			steps(t, p, false, next, next, next, next)
			compareRunLocks(t, p)
			p.Both(tc.setup)
			// completes review (terminal transition), then a terminal re-poll
			// that must not capture again.
			steps(t, p, true, next, next)
			compareTree(t, p, "kitty-specs")
			compareTree(t, p, ".kittify/runtime")
			compareGit(t, p)
		})
	}
}

// TestDiffNextTerminalRetrospectiveSoftwareDev drives the calc mission
// (software-dev) from its approved board through the remaining runtime
// steps to terminal: the transition captures the retrospective in the
// primary kitty-specs home, and the implement step of an all-approved
// board reads "No action mapped for WP step 'implement'".
func TestDiffNextTerminalRetrospectiveSoftwareDev(t *testing.T) {
	p := approvedPair(t)
	next := []string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"}
	// discovery, specify, plan, tasks, analyze, implement (blocked: no
	// action), review, accept, the terminal transition and a re-poll.
	for i := 0; i < 10; i++ {
		steps(t, p, false, next)
	}
	compareTree(t, p, "kitty-specs", "analysis-report.md")
	compareRunLocks(t, p)
	compareGit(t, p)
}
