package closeout_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// scaffoldSHA is the commit that introduced the mission corpus on the
// target branch (a valid single-parent PR landing commit).
func scaffoldSHA(t *testing.T, s *testkit.Sandbox) string {
	out := s.Git("log", "--format=%H", "--diff-filter=A", "--", filepath.Join("kitty-specs", slugOf(t, s), "meta.json"))
	f := strings.Fields(out)
	if len(f) == 0 {
		t.Fatal("no scaffold commit")
	}
	return f[len(f)-1]
}

// comparePairSHA is comparePair with "$SHA" substituted per side.
func comparePairSHA(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g := subst(args, slugOf(t, p.Go))
	o := subst(args, slugOf(t, p.Oracle))
	gs, os_ := scaffoldSHA(t, p.Go), scaffoldSHA(t, p.Oracle)
	for i := range g {
		g[i] = strings.ReplaceAll(g[i], "$SHA", gs)
		o[i] = strings.ReplaceAll(o[i], "$SHA", os_)
	}
	gr, or := p.Go.Run(g...), p.Oracle.RunOracle(o...)
	if gr.Code != or.Code {
		t.Errorf("%v: exit go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", args, gr.Code, or.Code, gr.Stdout, gr.Stderr, or.Stdout, or.Stderr)
	}
	for _, pair := range [][2]string{{gr.Stdout, or.Stdout}, {gr.Stderr, or.Stderr}} {
		a, b := digitSHA(p.Go.Normalize(reCommit.ReplaceAllString(pair[0], "Commit: <C>"))), digitSHA(p.Oracle.Normalize(reCommit.ReplaceAllString(pair[1], "Commit: <C>")))
		if a != b {
			t.Errorf("%v: output differs\n%s", args, testkit.Diff(b, a))
		}
	}
}

// TestDiffAcceptMergeCommit covers accept --merge-commit (PR-merge baseline
// recording, verified against git) and the origin freshness check against a
// local bare remote (--origin-check, read-only modes).
func TestDiffAcceptMergeCommit(t *testing.T) {
	p := approvedPair(t, "approved", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	recordVerdictsAndDocs(t, p, true)
	for _, c := range [][]string{
		{"accept", "--mission", "$M", "--mode", "local", "--merge-commit", "$SHA", "--json"},
		{"accept", "--mission", "$M", "--mode", "pr", "--merge-commit", "deadbeef", "--json"},
		{"accept", "--mission", "$M", "--mode", "pr", "--merge-commit", "not-a-sha"},
		{"accept", "--mission", "$M", "--mode", "pr", "--merge-commit", "$SHA", "--json"},
		{"accept", "--mission", "$M", "--mode", "pr", "--merge-commit", "$SHA", "--target-branch", "main", "--attest-first-landing-commit", "--json"},
		{"accept", "--mission", "$M", "--mode", "pr", "--merge-commit", "$SHA", "--attest-first-landing-commit", "--no-commit", "--json"},
	} {
		comparePairSHA(t, p, c...)
	}
	// origin: the remote's status log moves ahead of this clone
	addBareRemote(t, p, "feat/calc-ops")
	p.Both(func(s *testkit.Sandbox) {
		clone := filepath.Join(s.Base, "teammate")
		if r := s.Exec("", "git", "clone", "-q", "-b", "feat/calc-ops", filepath.Join(s.Base, "origin.git"), clone); r.Code != 0 {
			t.Fatal(r.Stderr)
		}
		log := filepath.Join(clone, "kitty-specs", slugOf(t, s), "status.events.jsonl")
		b, _ := os.ReadFile(log)
		os.WriteFile(log, append(b, []byte("")...), 0o644)
		s.Exec(clone, "git", "commit", "-q", "--allow-empty", "-m", "unrelated")
		f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o644)
		f.WriteString("\n")
		f.Close()
		s.Exec(clone, "git", "commit", "-q", "-am", "teammate status")
		if r := s.Exec(clone, "git", "push", "-q", "origin", "feat/calc-ops"); r.Code != 0 {
			t.Fatal(r.Stderr)
		}
	})
	for _, c := range [][]string{
		{"accept", "--mission", "$M", "--json"},
		{"accept", "--mission", "$M"},
		{"accept", "--mission", "$M", "--no-commit", "--json"},
		{"agent", "mission", "accept", "--mission", "$M", "--diagnose", "--json"},
		{"accept", "--mission", "$M", "--origin-check", "off", "--no-commit", "--json"},
	} {
		comparePairSHA(t, p, c...)
	}
	comparePairSHA(t, p, "accept", "--mission", "$M", "--mode", "pr", "--merge-commit", "$SHA", "--attest-first-landing-commit", "--origin-check", "warn", "--json")
	compareGit(t, p)
	compareMissionTree(t, p, false)
}
