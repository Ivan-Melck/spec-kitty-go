// Package missionfx provides shared mission fixtures for end-to-end and
// differential tests: an initialised project (Go-only or via the oracle's
// init) and the two-WP "calc" mission at its authored and finalized stages.
// The fixtures live in testdata/ next to this file.
package missionfx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// LocalSlug is the mission directory name recorded in the calc fixtures.
const LocalSlug = "calc-ops-01M54Y16"

// Path returns the absolute path of a fixture file (rel to testdata/).
func Path(rel string) string {
	return filepath.Join(testkit.ModuleRoot(), "internal", "testkit", "missionfx", "testdata", rel)
}

// Read returns the content of a fixture file.
func Read(t testing.TB, rel string) string {
	t.Helper()
	b, err := os.ReadFile(Path(rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// AnalysisReport is the path of a valid analysis report for the calc mission
// (input for `agent mission record-analysis --input-file`).
func AnalysisReport() string { return Path("calc/analysis-report.md") }

// SeedRepo adds the demo source file used by the calc fixture to the initial
// commit's tree (owned_files must exist for finalize-tasks).
func SeedRepo(s *testkit.Sandbox) {
	s.WriteFile("src/calc.py", "def add(a, b):\n    return a + b\n")
	s.Git("add", "src/calc.py")
	s.Git("commit", "-q", "-m", "add calc")
}

// NewProject builds a Go-only project fixture equivalent to an oracle
// `init --ai claude --non-interactive` (the files the core commands read),
// committed on main.
func NewProject(t testing.TB) *testkit.Sandbox {
	t.Helper()
	s := testkit.NewGitSandbox(t)
	SeedRepo(s)
	s.WriteFile(".kittify/config.yaml", Read(t, "config.yaml"))
	s.WriteFile(".kittify/metadata.yaml", Read(t, "metadata.yaml"))
	s.WriteFile(".gitignore", Read(t, "gitignore.txt"))
	s.WriteFile(".gitattributes", Read(t, "gitattributes.txt"))
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "Initialize spec-kitty")
	return s
}

// OracleInit runs the ORACLE's init in s and commits the result, so both
// sides of a differential test start from an identical project.
func OracleInit(t testing.TB, s *testkit.Sandbox) {
	t.Helper()
	r := s.Exec("", testkit.Oracle(t), "init", ".", "--ai", "claude", "--non-interactive")
	if r.Code != 0 {
		t.Fatalf("oracle init: %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "Initialize spec-kitty")
}

// NewOraclePair returns a differential pair whose two projects were both
// initialised by the oracle.
func NewOraclePair(t testing.TB) *testkit.Pair {
	t.Helper()
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) {
		SeedRepo(s)
		OracleInit(t, s)
	})
	return p
}

// RunOracle runs the oracle in s and fails on a non-zero exit.
func RunOracle(t testing.TB, s *testkit.Sandbox, args ...string) testkit.Result {
	t.Helper()
	r := s.Exec("", testkit.Oracle(t), args...)
	if r.Code != 0 {
		t.Fatalf("oracle %s: exit %d\n%s\n%s", strings.Join(args, " "), r.Code, r.Stdout, r.Stderr)
	}
	return r
}

// MissionSlug returns the single mission directory name in s.
func MissionSlug(t testing.TB, s *testkit.Sandbox) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.Dir, "kitty-specs"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no mission in %s", s.Dir)
	}
	return entries[0].Name()
}

// AuthorCalc writes the calc spec/plan/tasks/WP files for slug and commits.
func AuthorCalc(t testing.TB, s *testkit.Sandbox, slug string) {
	t.Helper()
	for _, f := range []string{"spec.md", "plan.md", "tasks.md", "tasks/WP01-subtract-helper.md", "tasks/WP02-multiply.md"} {
		body := strings.ReplaceAll(Read(t, "calc/authored/"+f), LocalSlug, slug)
		s.WriteFile(filepath.Join("kitty-specs", slug, f), body)
	}
	s.Git("add", "-A", "kitty-specs")
	s.Git("commit", "-q", "-m", "Add planning artifacts")
}

// OracleCalcMission creates the calc mission with the ORACLE (create +
// author + finalize-tasks) in both sandboxes of p, on a feature branch.
func OracleCalcMission(t testing.TB, p *testkit.Pair, createArgs ...string) (goSlug, oracleSlug string) {
	t.Helper()
	if len(createArgs) == 0 {
		createArgs = []string{"--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops"}
	}
	slugs := []string{}
	p.Both(func(s *testkit.Sandbox) {
		RunOracle(t, s, append([]string{"agent", "mission", "create", "calc-ops", "--json"}, createArgs...)...)
		slug := MissionSlug(t, s)
		AuthorCalc(t, s, slug)
		RunOracle(t, s, "agent", "mission", "finalize-tasks", "--mission", slug, "--json")
		slugs = append(slugs, slug)
	})
	return slugs[0], slugs[1]
}

// NewLocalMission builds (without the oracle) a finalized calc mission in a
// lanes topology on branch feat/calc-ops, from the recorded fixtures.
func NewLocalMission(t testing.TB) *testkit.Sandbox {
	t.Helper()
	s := NewProject(t)
	s.Git("switch", "-q", "-c", "feat/calc-ops")
	dir := filepath.Join("kitty-specs", LocalSlug)
	for _, f := range []string{"spec.md", "plan.md", "tasks.md"} {
		s.WriteFile(filepath.Join(dir, f), Read(t, "calc/authored/"+f))
	}
	for _, f := range []string{"lanes.json", "status.events.jsonl", "status.json", "tasks/WP01-subtract-helper.md", "tasks/WP02-multiply.md"} {
		s.WriteFile(filepath.Join(dir, f), Read(t, "calc/finalized/"+f))
	}
	meta, err := jsonx.DecodeObject([]byte(Read(t, "calc/finalized/meta.json")))
	if err != nil {
		t.Fatal(err)
	}
	meta.Delete("coordination_branch")
	meta.Set("topology", "lanes")
	s.WriteFile(filepath.Join(dir, "meta.json"), jsonx.Dumps(meta, jsonx.Options{Indent: 2, SortKeys: true})+"\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "finalized calc mission")
	return s
}
