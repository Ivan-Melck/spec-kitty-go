package agentcore_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var reParentsCore = regexp.MustCompile(`\[parents:([0-9a-f ]*)\]`)

// refGitState renders each ref's first-parent history (immune to the
// same-second --topo-order reordering of independent branches), the
// worktree list and the status.
func refGitState(s *testkit.Sandbox) string {
	var b strings.Builder
	refs := strings.Fields(s.Exec("", "git", "for-each-ref", "--format=%(refname)", "refs/heads", "refs/spec-kitty").Stdout)
	for _, ref := range refs {
		r := s.Exec("", "git", "log", "--first-parent", "--format=%s%n%b[parents:%p]", "--name-status", ref)
		out := reParentsCore.ReplaceAllStringFunc(r.Stdout, func(m string) string {
			return "[parents:" + strconv.Itoa(len(strings.Fields(reParentsCore.FindStringSubmatch(m)[1]))) + "]"
		})
		b.WriteString("$ " + ref + "\n" + out)
	}
	for _, args := range [][]string{{"worktree", "list", "--porcelain"}, {"status", "--short", "--untracked-files=all"}} {
		r := s.Exec("", "git", args...)
		b.WriteString("$ git " + strings.Join(args, " ") + "\n" + r.Stdout)
	}
	return s.Normalize(b.String())
}

func compareRefGit(t *testing.T, p *testkit.Pair) {
	t.Helper()
	if g, o := refGitState(p.Go), refGitState(p.Oracle); g != o {
		t.Errorf("git state differs:\n%s", testkit.Diff(o, g))
	}
}

// dropAnalysis removes the analysis report (nondeterministic key order
// upstream) before tree comparisons.
func dropAnalysis(t *testing.T, p *testkit.Pair) {
	p.Both(func(s *testkit.Sandbox) {
		os.Remove(filepath.Join(s.Dir, "kitty-specs", missionSlug(t, s), "analysis-report.md"))
	})
}

// gatePair builds an analyzed calc mission with WP01 claimed by the oracle
// and appends review: config lines (committed nowhere, like a local edit).
func gatePair(t *testing.T, review string) (*testkit.Pair, string, string) {
	t.Helper()
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--input-file", tdPath("calc/analysis-report.md"), "--json")
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	if review != "" {
		p.Both(func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+"review:\n"+review)
		})
	}
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M")
	return p, g, o
}

// TestDiffPreReviewGateDeclared covers CORE-07: a declared
// review.test_command runs the lazy baseline capture at the lane fork point
// (committed baseline-tests.json), the head run, the baseline diff, the
// opt-in block and the --force bypass.
func TestDiffPreReviewGateDeclared(t *testing.T) {
	t.Parallel()
	p, g, o := gatePair(t, `  test_command: "printf 'FAIL old_test: boom\\n'; test -f src/new.py && printf 'FAIL new_test: regressed\\n'; exit 1"`+"\n  fail_on_pre_review_regression: true\n")
	laneWrite(p, t, "src/new.py", "x = 1\n")
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--force", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--force"},
	})
	dropAnalysis(t, p)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareRefGit(t, p)
}

// TestDiffPreReviewGateNoNewFailures: a clean head over a failing baseline
// (pre-existing only) proceeds and records no_new_failures.
func TestDiffPreReviewGateNoNewFailures(t *testing.T) {
	t.Parallel()
	p, g, o := gatePair(t, `  test_command: "printf 'FAIL old_test: boom\\n' >&2; exit 1"`+"\n")
	laneWrite(p, t, "src/calc.py", "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n")
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
	})
	dropAnalysis(t, p)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareRefGit(t, p)
}

// TestDiffPreReviewGateUnverifiedBaseline: a declared command with no
// verifiable output leaves the -1 sentinel (not persisted, logged warning)
// and an unverified_baseline verdict.
func TestDiffPreReviewGateUnverifiedBaseline(t *testing.T) {
	t.Parallel()
	p, g, o := gatePair(t, "  test_command: \"echo ok\"\n")
	laneWrite(p, t, "src/calc.py", "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n")
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--force", "--json"},
	})
	dropAnalysis(t, p)
	p.CompareTree("kitty-specs", true)
	compareRefGit(t, p)
}

// TestDiffPreReviewGateJUnit: an {output_file} JUnit artifact is parsed
// (junit_xml source identity) and the failing testcase is new at head.
func TestDiffPreReviewGateJUnit(t *testing.T) {
	t.Parallel()
	p, g, o := gatePair(t, `  test_command: "if test -f src/new.py; then f='<failure message=\"bad&#10;more\"/>'; else f=''; fi; printf '<testsuites><testsuite><testcase classname=\"pkg.mod\" name=\"t1\" file=\"t.py\" line=\"3\">%s</testcase><testcase name=\"t2\"><skipped/></testcase></testsuite></testsuites>' \"$f\" > {output_file}"`+"\n")
	laneWrite(p, t, "src/new.py", "x = 1\n")
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
	})
	dropAnalysis(t, p)
	p.CompareTree("kitty-specs", true)
	compareRefGit(t, p)
}

// TestDiffPreReviewGateEdges: no binding for the for_review->for_review
// edge (no_coverage with the resolver's reason), the empty override scope,
// the not-declared quiet skip and the escape hatch.
func TestDiffPreReviewGateEdges(t *testing.T) {
	t.Parallel()
	p, g, o := gatePair(t, "")
	laneWrite(p, t, "src/calc.py", "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n")
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--force"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--force", "--skip-pre-review-gate", "--json"},
	})
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+"review:\n  pre_review_test_command: \"   \"\n  fail_on_pre_review_regression: true\n")
	})
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--force"},
		{"agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--force", "--json"},
	})
	dropAnalysis(t, p)
	p.CompareTree("kitty-specs", true)
	compareRefGit(t, p)
}
