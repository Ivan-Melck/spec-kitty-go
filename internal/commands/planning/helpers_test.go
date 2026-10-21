package planning_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// gitState renders the observable git state of a sandbox for comparison.
func gitState(s *testkit.Sandbox) string {
	var b strings.Builder
	for _, args := range [][]string{
		{"log", "--all", "--topo-order", "--format=%s%n%b%n[%D]", "--name-status"},
		{"worktree", "list", "--porcelain"},
		{"status", "--short", "--untracked-files=all"},
		{"branch", "-a"},
	} {
		r := s.Exec("", "git", args...)
		b.WriteString("$ git " + strings.Join(args, " ") + "\n" + r.Stdout)
	}
	return s.Normalize(b.String())
}

func compareGit(t *testing.T, p *testkit.Pair) {
	t.Helper()
	g, o := gitState(p.Go), gitState(p.Oracle)
	if g != o {
		t.Errorf("git state differs:\n%s", testkit.Diff(o, g))
	}
}

var shortULID = regexp.MustCompile(`\b0[0-9A-HJKMNP-TV-Z]{11}\.\.\.`)

// shellSteps runs each step with bash in both sandboxes ($SK = the side's
// binary, $M = the side's mission slug) and compares exit code, stdout and
// stderr (normalized).
func shellSteps(t *testing.T, p *testkit.Pair, goSlug, orSlug string, steps ...string) {
	t.Helper()
	for _, step := range steps {
		g := runShell(p.Go, testkit.Binary(t), goSlug, step)
		o := runShell(p.Oracle, testkit.Oracle(t), orSlug, step)
		compareResults(t, p, step, g, o)
	}
}

func runShell(s *testkit.Sandbox, bin, slug, step string) testkit.Result {
	s.Env = append(s.Env[:0:0], s.Env...)
	saved := s.Env
	s.Env = append(s.Env, "SK="+bin, "M="+slug, "TD="+missionfx.Path(""))
	r := s.Exec("", "bash", "-c", step)
	s.Env = saved
	return r
}

func compareResults(t *testing.T, p *testkit.Pair, label string, g, o testkit.Result) {
	t.Helper()
	if os.Getenv("KITTY_TEST_TRACE") != "" {
		t.Logf("%s\n== go %d ==\n%s%s== oracle %d ==\n%s%s", label, g.Code, g.Stdout, g.Stderr, o.Code, o.Stdout, o.Stderr)
	}
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d\ngo stdout:\n%s\ngo stderr:\n%s\noracle stdout:\n%s\noracle stderr:\n%s", label, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
		return
	}
	gs := shortULID.ReplaceAllString(p.Go.Normalize(g.Stdout), "<ULID12>")
	os_ := shortULID.ReplaceAllString(p.Oracle.Normalize(o.Stdout), "<ULID12>")
	if gs != os_ {
		t.Errorf("%s: stdout differs\n%s", label, testkit.Diff(os_, gs))
	}
	ge, oe := p.Go.Normalize(g.Stderr), p.Oracle.Normalize(o.Stderr)
	if ge != oe {
		t.Errorf("%s: stderr differs\n%s", label, testkit.Diff(oe, ge))
	}
}

// argSteps runs argv steps ("$M" replaced by the side's slug).
func argSteps(t *testing.T, p *testkit.Pair, goSlug, orSlug string, steps ...[]string) {
	t.Helper()
	for _, s := range steps {
		g := p.Go.Run(subst(s, goSlug)...)
		o := p.Oracle.RunOracle(subst(s, orSlug)...)
		compareResults(t, p, strings.Join(s, " "), g, o)
	}
}

func subst(args []string, slug string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "$M", slug)
	}
	return out
}
