package closeout_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// addBareRemote creates <base>/origin.git, adds it as origin and pushes
// the given branches.
func addBareRemote(t *testing.T, p *testkit.Pair, branches ...string) {
	p.Both(func(s *testkit.Sandbox) {
		bare := filepath.Join(s.Base, "origin.git")
		if r := s.Exec("", "git", "init", "-q", "--bare", bare); r.Code != 0 {
			t.Fatal(r.Stderr)
		}
		s.Git("remote", "add", "origin", bare)
		for _, b := range branches {
			b = strings.ReplaceAll(b, "$M", slugOf(t, s))
			if r := s.Exec("", "git", "push", "-q", "origin", b); r.Code != 0 {
				t.Fatalf("push %s: %s", b, r.Stderr)
			}
		}
	})
}

// remoteLog renders the bare remote's branches and their history.
func remoteLog(s *testkit.Sandbox) string {
	bare := filepath.Join(s.Base, "origin.git")
	r := s.Exec(bare, "git", "log", "--all", "--topo-order", "--format=%s%n[%D]")
	return s.Normalize(r.Stdout)
}

// TestDiffConsolidatePush covers the origin freshness gate (a lane branch
// behind origin refuses in enforce mode and is a warning in warn mode) and
// `--push` against a local bare remote, including the target-branch sync
// preflight.
func TestDiffConsolidatePush(t *testing.T) {
	p := acceptedPair(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	addBareRemote(t, p, "feat/calc-ops", "kitty/mission-$M-lane-a")
	// origin's lane-a gains a commit this clone does not have
	p.Both(func(s *testkit.Sandbox) {
		lane := "kitty/mission-" + slugOf(t, s) + "-lane-a"
		tree := strings.TrimSpace(s.Git("rev-parse", lane+"^{tree}"))
		parent := strings.TrimSpace(s.Git("rev-parse", lane))
		c := s.Exec("", "git", "commit-tree", tree, "-p", parent, "-m", "teammate work")
		if c.Code != 0 {
			t.Fatal(c.Stderr)
		}
		if r := s.Exec("", "git", "push", "-q", "origin", strings.TrimSpace(c.Stdout)+":refs/heads/"+lane); r.Code != 0 {
			t.Fatal(r.Stderr)
		}
	})
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes", "--push")
	compareGit(t, p)
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes", "--push", "--origin-check", "warn")
	compareGit(t, p)
	if g, o := remoteLog(p.Go), remoteLog(p.Oracle); g != o {
		t.Errorf("remote differs:\n%s", testkit.Diff(o, g))
	}
}
