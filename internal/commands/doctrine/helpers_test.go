package doctrine_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func td(t testing.TB, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testkit.ModuleRoot(), "internal", "commands", "doctrine", "testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newProject is a Go-only project fixture equivalent (for the doctrine
// commands) to an oracle `init --ai claude --non-interactive`.
func newProject(t testing.TB) *testkit.Sandbox {
	t.Helper()
	s := testkit.NewGitSandbox(t)
	s.WriteFile(".kittify/config.yaml", td(t, "config.yaml"))
	s.WriteFile(".gitignore", "kitty-ops/ops-index.jsonl\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "Initialize spec-kitty")
	return s
}

// oracleInit runs the ORACLE's init in s and commits the result.
func oracleInit(t testing.TB, s *testkit.Sandbox) {
	t.Helper()
	r := s.Exec("", testkit.Oracle(t), "init", ".", "--ai", "claude", "--non-interactive")
	if r.Code != 0 {
		t.Fatalf("oracle init: %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "Initialize spec-kitty")
}

// newOraclePair returns a differential pair initialised by the oracle.
func newOraclePair(t testing.TB) *testkit.Pair {
	t.Helper()
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) { oracleInit(t, s) })
	return p
}

var (
	reDurationMS = regexp.MustCompile(`"duration_ms": [0-9.e-]+`)
	reShortULID  = regexp.MustCompile(`\b0[0-9A-HJKMNP-TV-Z]{11}…`)
)

// maskDur masks the glossary chokepoint wall-clock duration and truncated
// invocation ids.
func maskDur(s string) string {
	s = reShortULID.ReplaceAllString(s, "<ID12>…")
	return reDurationMS.ReplaceAllString(s, `"duration_ms": <D>`)
}

// compareMasked compares exit code and stdout/stderr with duration masking.
func compareMasked(t *testing.T, p *testkit.Pair, stderr bool, args ...string) (testkit.Result, testkit.Result) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", strings.Join(args, " "), g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	gs, os_ := maskDur(p.Go.Normalize(g.Stdout)), maskDur(p.Oracle.Normalize(o.Stdout))
	if gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
	if stderr {
		ge, oe := maskDur(p.Go.Normalize(g.Stderr)), maskDur(p.Oracle.Normalize(o.Stderr))
		if ge != oe {
			t.Errorf("%s: stderr differs\n%s", strings.Join(args, " "), testkit.Diff(oe, ge))
		}
	}
	return g, o
}

// treeFiles lists files under rel in s.
func treeFiles(s *testkit.Sandbox, rel string) []string {
	return testkit.Files(filepath.Join(s.Dir, rel))
}

// copyFile copies a project-relative file from one sandbox to another.
func copyFile(t testing.TB, from, to *testkit.Sandbox, rel string) {
	t.Helper()
	to.WriteFile(rel, from.ReadFile(rel))
}
