package agentcore_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func tdPath(rel string) string {
	return filepath.Join(testkit.ModuleRoot(), "internal", "commands", "agentcore", "testdata", rel)
}

func td(t testing.TB, rel string) string {
	t.Helper()
	b, err := os.ReadFile(tdPath(rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// seedRepo adds the demo source file used by the calc fixture to the initial
// commit's tree (owned_files must exist for finalize-tasks).
func seedRepo(s *testkit.Sandbox) {
	s.WriteFile("src/calc.py", "def add(a, b):\n    return a + b\n")
	s.Git("add", "src/calc.py")
	s.Git("commit", "-q", "-m", "add calc")
}

// newProject builds a Go-only project fixture equivalent to an oracle
// `init --ai claude --non-interactive` (the files the core commands read),
// committed on main.
func newProject(t testing.TB) *testkit.Sandbox {
	t.Helper()
	s := testkit.NewGitSandbox(t)
	seedRepo(s)
	s.WriteFile(".kittify/config.yaml", td(t, "config.yaml"))
	s.WriteFile(".kittify/metadata.yaml", td(t, "metadata.yaml"))
	s.WriteFile(".gitignore", td(t, "gitignore.txt"))
	s.WriteFile(".gitattributes", td(t, "gitattributes.txt"))
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "Initialize spec-kitty")
	return s
}

// oracleInit runs the ORACLE's init in s and commits the result, so both
// sides of a differential test start from an identical project.
func oracleInit(t testing.TB, s *testkit.Sandbox) {
	t.Helper()
	r := s.Exec("", testkit.Oracle(t), "init", ".", "--ai", "claude", "--non-interactive")
	if r.Code != 0 {
		t.Fatalf("oracle init: %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "Initialize spec-kitty")
}

// newOraclePair returns a differential pair whose two projects were both
// initialised by the oracle.
func newOraclePair(t testing.TB) *testkit.Pair {
	t.Helper()
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) {
		seedRepo(s)
		oracleInit(t, s)
	})
	return p
}

// runOracle runs the oracle in s and fails on a non-zero exit.
func runOracle(t testing.TB, s *testkit.Sandbox, args ...string) testkit.Result {
	t.Helper()
	r := s.Exec("", testkit.Oracle(t), args...)
	if r.Code != 0 {
		t.Fatalf("oracle %s: exit %d\n%s\n%s", strings.Join(args, " "), r.Code, r.Stdout, r.Stderr)
	}
	return r
}

// missionSlug returns the single mission directory name in s.
func missionSlug(t testing.TB, s *testkit.Sandbox) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.Dir, "kitty-specs"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no mission in %s", s.Dir)
	}
	return entries[0].Name()
}

// authorCalc writes the calc spec/plan/tasks/WP files for slug and commits.
func authorCalc(t testing.TB, s *testkit.Sandbox, slug string) {
	t.Helper()
	for _, f := range []string{"spec.md", "plan.md", "tasks.md", "tasks/WP01-subtract-helper.md", "tasks/WP02-multiply.md"} {
		body := strings.ReplaceAll(td(t, "calc/authored/"+f), "calc-ops-01M54Y16", slug)
		s.WriteFile(filepath.Join("kitty-specs", slug, f), body)
	}
	s.Git("add", "-A", "kitty-specs")
	s.Git("commit", "-q", "-m", "Add planning artifacts")
}

// oracleCalcMission creates the calc mission with the ORACLE (create +
// author + finalize-tasks) in both sandboxes of p, on a feature branch.
func oracleCalcMission(t testing.TB, p *testkit.Pair, createArgs ...string) (goSlug, oracleSlug string) {
	t.Helper()
	if len(createArgs) == 0 {
		createArgs = []string{"--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops"}
	}
	slugs := []string{}
	p.Both(func(s *testkit.Sandbox) {
		runOracle(t, s, append([]string{"agent", "mission", "create", "calc-ops", "--json"}, createArgs...)...)
		slug := missionSlug(t, s)
		authorCalc(t, s, slug)
		runOracle(t, s, "agent", "mission", "finalize-tasks", "--mission", slug, "--json")
		slugs = append(slugs, slug)
	})
	return slugs[0], slugs[1]
}
