package invocation

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestOpCommitPolicyMatchesOracle(t *testing.T) {
	f, err := os.Open(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(f).ReadString('\n')
	f.Close()
	python := strings.TrimSpace(strings.TrimPrefix(line, "#!"))
	const record = "kitty-ops/01ARZ3NDEKTSV4RRFFQ69G5FAV.jsonl"
	for _, scenario := range []string{"untracked-primary", "tracked-primary", "ignored", "tracked-ignored", "topic-only", "unresolved", "remote-primary", "dangling-origin-head"} {
		t.Run(scenario, func(t *testing.T) {
			s := testkit.NewSandbox(t)
			branch := "main"
			if scenario == "unresolved" {
				branch = "topic"
			}
			s.Git("init", "-q", "-b", branch)
			s.WriteFile("README", "fixture\n")
			s.Git("add", ".")
			s.Git("commit", "-qm", "base")
			if scenario == "dangling-origin-head" {
				s.Git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/missing")
			}
			if scenario == "tracked-primary" || scenario == "tracked-ignored" || scenario == "remote-primary" {
				s.WriteFile(record, "{}\n")
				s.Git("add", "kitty-ops")
				s.Git("commit", "-qm", "audit trail")
			}
			if scenario == "remote-primary" {
				s.Git("update-ref", "refs/remotes/origin/main", "HEAD")
				s.Git("checkout", "-qb", "topic")
				s.Git("branch", "-D", "main")
			}
			if scenario == "topic-only" {
				s.Git("checkout", "-qb", "topic")
				s.WriteFile(record, "{}\n")
				s.Git("add", "kitty-ops")
				s.Git("commit", "-qm", "topic audit")
			}
			if scenario == "ignored" || scenario == "tracked-ignored" {
				s.WriteFile(".gitignore", "kitty-ops/\n")
			}
			tracking, e := ProbePrimaryTracking(s.Dir)
			if e != nil {
				t.Fatal(e)
			}
			reason, e := OpCommitSkipReason(s.Dir, record, tracking)
			if e != nil {
				t.Fatal(e)
			}
			var primary, skip any
			if tracking.Primary != "" {
				primary = tracking.Primary
			}
			if reason != "" {
				skip = reason
			}
			got := jsonx.Dumps(jsonx.NewObject("primary", primary, "tracked", tracking.Tracked, "skip", skip), jsonx.SortedPretty) + "\n"
			script := "import json\nfrom pathlib import Path\nfrom specify_cli.invocation.op_commit_policy import probe_primary_tracking,op_commit_skip_reason\nr=Path.cwd()\nt=probe_primary_tracking(r)\nprint(json.dumps(dict(primary=t.primary,tracked=t.tracked,skip=op_commit_skip_reason(r,'" + record + "',tracking=t)),sort_keys=True,indent=2))"
			oracle := s.Exec("", python, "-c", script)
			if oracle.Code != 0 {
				t.Fatal(oracle.Stderr)
			}
			if got != oracle.Stdout {
				t.Error(testkit.Diff(oracle.Stdout, got))
			}
		})
	}
}
