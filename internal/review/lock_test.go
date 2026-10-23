package review

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestLockRefusalMatchesOracle(t *testing.T) {
	f, err := os.Open(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(f).ReadString('\n')
	f.Close()
	python := strings.TrimSpace(strings.TrimPrefix(line, "#!"))
	valid := `{"worktree_path":"fixture","wp_id":"WP01","agent":"reviewer","started_at":"2026-10-24T00:00:00Z","pid":99999999}`
	for _, scenario := range []string{"absent", "valid", "empty", "missing", "extra", "boolean", "float", "array", "null", "utf8", "tracked", "tracked-deleted", "tracked-corrupt", "missing-workspace", "missing-remote-workspace"} {
		t.Run(scenario, func(t *testing.T) {
			p := testkit.NewPair(t)
			p.Both(func(s *testkit.Sandbox) {
				s.Git("init", "-q", "-b", "fixture")
				s.WriteFile("README", "fixture\n")
				s.Git("add", ".")
				s.Git("commit", "-qm", "fixture")
				content := valid
				switch scenario {
				case "empty":
					content = ""
				case "missing":
					content = `{"extra":true}`
				case "extra":
					content = strings.TrimSuffix(valid, "}") + `,"future":true}`
				case "boolean":
					content = strings.ReplaceAll(valid, "99999999", "true")
				case "float":
					content = strings.ReplaceAll(valid, "99999999", "1.0")
				case "array":
					content = "[]"
				case "null":
					content = "null"
				case "utf8":
					content = "\xff"
				case "tracked-corrupt":
					content = "{"
				}
				if scenario != "absent" {
					s.WriteFile(LockRelPath, content)
				}
				if strings.HasPrefix(scenario, "tracked") || strings.HasPrefix(scenario, "missing-") {
					s.Git("add", ".")
					s.Git("commit", "-qm", "lock")
				}
				if scenario == "tracked-deleted" {
					os.Remove(filepath.Join(s.Dir, LockRelPath))
				}
			})
			exists := !strings.HasPrefix(scenario, "missing-")
			ref := ""
			if !exists {
				ref = "fixture"
			}
			branch := "fixture"
			if scenario == "missing-remote-workspace" {
				branch = "new-lane"
			}
			for _, claimed := range []bool{false, true} {
				worktree := p.Go.Dir
				if !exists {
					worktree = filepath.Join(worktree, "missing workspace")
				}
				got := p.Go.Normalize(LockRefusal(worktree, exists, p.Go.Dir, ref, branch, claimed)) + "\n"
				literal := func(s string) string { return "'" + s + "'" }
				pyBool := func(b bool) string {
					if b {
						return "True"
					}
					return "False"
				}
				script := "from pathlib import Path\nfrom specify_cli.review.lock import review_lock_refusal\nr=Path.cwd()\nw=r if " + pyBool(exists) + " else r/'missing workspace'\nprint(review_lock_refusal(w,exists=" + pyBool(exists) + ",repo_root=r,checkout_ref=" + literal(ref) + ",branch=" + literal(branch) + ",claimed=" + pyBool(claimed) + ") or '')"
				result := p.Oracle.Exec("", python, "-c", script)
				if result.Code != 0 {
					t.Fatal(result.Stderr)
				}
				want := p.Oracle.Normalize(result.Stdout)
				if got != want {
					t.Errorf("claimed=%v: %s", claimed, testkit.Diff(want, got))
				}
			}
		})
	}
}
