package testkit

import (
	"os/exec"
	"strings"
	"testing"
)

// TestGitHistoryIgnoresCommitDates builds the same two-branch graph twice
// with the branch commits' dates swapped: `log --all --topo-order` orders
// them differently, GitHistory renders both identically.
func TestGitHistoryIgnoresCommitDates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	build := func(dateA, dateB string) *Sandbox {
		s := NewGitSandbox(t)
		commit := func(file, date string) {
			s.WriteFile(file, file+"\n")
			s.Git("add", file)
			if r := s.Exec("", "env", "GIT_COMMITTER_DATE="+date, "GIT_AUTHOR_DATE="+date, "git", "commit", "-q", "-m", "add "+file); r.Code != 0 {
				t.Fatal(r.Stderr)
			}
		}
		s.Git("checkout", "-q", "-b", "a")
		commit("a.txt", dateA)
		s.Git("checkout", "-q", "main")
		s.Git("checkout", "-q", "-b", "b")
		commit("b.txt", dateB)
		s.Git("checkout", "-q", "main")
		return s
	}
	x := build("2026-01-01T00:00:01Z", "2026-01-01T00:00:02Z")
	y := build("2026-01-01T00:00:02Z", "2026-01-01T00:00:01Z")
	topo := func(s *Sandbox) string {
		return s.Exec("", "git", "log", "--all", "--topo-order", "--format=%s").Stdout
	}
	if topo(x) == topo(y) { // independent branches: git orders them by date
		t.Logf("git log --topo-order happened to agree; the dates did not reorder it:\n%s", topo(x))
	}
	gx, gy := x.GitHistory("", "%s%n[%D]", "--name-status"), y.GitHistory("", "%s%n[%D]", "--name-status")
	if gx != gy {
		t.Fatalf("GitHistory depends on commit dates:\n%s", Diff(gx, gy))
	}
	for _, want := range []string{"@ refs/heads/a\n", "@ refs/heads/b\n", "add a.txt\n", "add b.txt\n"} {
		if !strings.Contains(gx, want) {
			t.Errorf("history lacks %q:\n%s", want, gx)
		}
	}
}
