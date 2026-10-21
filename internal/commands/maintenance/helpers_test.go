package maintenance_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// step is one command run on both sides; "$M" is replaced by each side's
// mission slug.
type step []string

func subst(args []string, slug string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "$M", slug)
	}
	return out
}

// compareSteps runs each step with the Go binary and the oracle and diffs
// exit code, normalized stdout and stderr.
func compareSteps(t *testing.T, p *testkit.Pair, goSlug, orSlug string, steps ...step) {
	t.Helper()
	compareStepsVars(t, p, map[string]string{"$M": goSlug}, map[string]string{"$M": orSlug}, steps...)
}

func substVars(args []string, vars map[string]string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		for k, v := range vars {
			a = strings.ReplaceAll(a, k, v)
		}
		out[i] = a
	}
	return out
}

// compareStepsVars is compareSteps with arbitrary per-side placeholders.
func compareStepsVars(t *testing.T, p *testkit.Pair, goVars, orVars map[string]string, steps ...step) {
	t.Helper()
	for _, s := range steps {
		g := p.Go.Run(substVars(s, goVars)...)
		o := p.Oracle.RunOracle(substVars(s, orVars)...)
		if os.Getenv("KITTY_TEST_TRACE") != "" {
			t.Logf("%v\n== go %d ==\n%s%s== oracle %d ==\n%s%s", s, g.Code, g.Stdout, g.Stderr, o.Code, o.Stdout, o.Stderr)
		}
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d\ngo stdout:\n%s\ngo stderr:\n%s\noracle stdout:\n%s\noracle stderr:\n%s", s, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
			continue
		}
		if gs, os_ := normDiffIndex(p.Go.Normalize(g.Stdout)), normDiffIndex(p.Oracle.Normalize(o.Stdout)); gs != os_ {
			t.Errorf("%v: stdout differs\n%s", s, testkit.Diff(os_, gs))
		}
		if ge, oe := p.Go.Normalize(g.Stderr), p.Oracle.Normalize(o.Stderr); ge != oe {
			t.Errorf("%v: stderr differs\n%s", s, testkit.Diff(oe, ge))
		}
	}
}

// compareFiles diffs the normalized content of project-relative files
// ("$M" substituted per side).
func compareFiles(t *testing.T, p *testkit.Pair, goSlug, orSlug string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		g := p.Go.ReadFile(strings.ReplaceAll(rel, "$M", goSlug))
		o := p.Oracle.ReadFile(strings.ReplaceAll(rel, "$M", orSlug))
		if gn, on := p.Go.Normalize(g), p.Oracle.Normalize(o); gn != on {
			t.Errorf("file %s differs:\n%s", rel, testkit.Diff(on, gn))
		}
	}
}

// writeBoth writes the same project-relative file on both sides.
func writeBoth(p *testkit.Pair, rel, content string) {
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(rel, content) })
}

// commitBoth stages everything and commits on both sides.
func commitBoth(p *testkit.Pair, msg string) {
	p.Both(func(s *testkit.Sandbox) {
		s.Git("add", "-A")
		s.Git("commit", "-q", "--allow-empty", "-m", msg)
	})
}

// newOraclePair builds an oracle-initialised pair. Differential tests are
// independent sandboxes, so they run in parallel (oracle start-up dominates).
func newOraclePair(t *testing.T) *testkit.Pair {
	t.Helper()
	t.Parallel()
	return missionfx.NewOraclePair(t)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func join(parts ...string) string { return filepath.Join(parts...) }

// diffIndexRe matches git-diff "index a..b" headers. Blob ids hash content
// that embeds per-side ULIDs, and all-digit abbreviations escape the
// testkit SHA mask, so they are masked here.
var diffIndexRe = regexp.MustCompile(`(?m)^index [0-9a-f<>A-Z]+\.\.[0-9a-f<>A-Z]+`)

func normDiffIndex(s string) string { return diffIndexRe.ReplaceAllString(s, "index <IDX>") }
