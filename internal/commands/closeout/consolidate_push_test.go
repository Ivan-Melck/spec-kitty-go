package closeout_test

import (
	"os"
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

// remoteLog renders the bare remote's branches and their history (graph
// order: independent of same-second commit timestamps).
func remoteLog(s *testkit.Sandbox) string {
	return s.Normalize(s.GitHistory(filepath.Join(s.Base, "origin.git"), "%s%n[%D]"))
}

// TestDiffConsolidatePushRejectedResume covers the reconciliation PASS
// anchor: a push the remote rejects after the verified landing keeps the
// record, and --resume finishes the teardown without re-running the phases
// (#5021 verified landing).
func TestDiffConsolidatePushRejectedResume(t *testing.T) {
	p := acceptedPair(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	addBareRemote(t, p, "feat/calc-ops")
	hook := func(s *testkit.Sandbox) string { return filepath.Join(s.Base, "origin.git", "hooks", "pre-receive") }
	p.Both(func(s *testkit.Sandbox) {
		if err := os.WriteFile(hook(s), []byte("#!/bin/sh\necho 'remote: push refused by policy' >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	})
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes", "--push")
	compareGit(t, p)
	p.CompareTree(".kittify/runtime", true)
	p.Both(func(s *testkit.Sandbox) { os.Remove(hook(s)) })
	comparePair(t, p, "consolidate", "--mission", "$M", "--resume")
	compareGit(t, p)
	if g, o := remoteLog(p.Go), remoteLog(p.Oracle); g != o {
		t.Errorf("remote differs:\n%s", testkit.Diff(o, g))
	}
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
