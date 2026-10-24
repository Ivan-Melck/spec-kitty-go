package setup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestDiffRC7LegacyGate(t *testing.T) {
	for _, scenario := range []string{"directory", "governance", "config", "checkout"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			p := testkit.NewPair(t)
			compareInit(p, "init", ".", "--ai", "codex", "--non-interactive")
			p.Both(func(s *testkit.Sandbox) {
				switch scenario {
				case "directory":
					s.WriteFile(".kittify/doctrine/legacy.yaml", "legacy: true\n")
				case "governance":
					s.WriteFile(".kittify/charter/governance.yaml", "doctrine: {}\n")
				case "config":
					s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+"\ngovernance:\n  doctrine: {}\n")
				case "checkout":
					s.Git("add", "-A")
					s.Git("commit", "-qm", "project scaffold")
					s.Git("worktree", "add", "-qb", "old-lane", "lane")
					s.WriteFile("lane/.kittify/doctrine/legacy.yaml", "legacy: true\n")
				}
			})
			commands := [][]string{
				{"agent", "config", "list"},
				{"agent", "config", "list", "--json"},
				{"agent", "config", "list", "--no-such-option"},
				{"agent", "action", "implement"},
				{"next", "--mission", "missing", "--json"},
				{"migrate", "--dry-run"},
				{"agent", "config", "list", "--help"},
			}
			for _, args := range commands {
				if scenario != "checkout" {
					p.Compare(true, args...)
					continue
				}
				g, o := p.RunIn("lane", args...)
				if g.Code != o.Code || p.Go.Normalize(g.Stdout) != p.Oracle.Normalize(o.Stdout) || p.Go.Normalize(g.Stderr) != p.Oracle.Normalize(o.Stderr) {
					t.Errorf("%v: go=%d oracle=%d\nstdout:\n%s\nstderr:\n%s", args, g.Code, o.Code,
						testkit.Diff(p.Oracle.Normalize(o.Stdout), p.Go.Normalize(g.Stdout)), testkit.Diff(p.Oracle.Normalize(o.Stderr), p.Go.Normalize(g.Stderr)))
				}
			}
			p.Both(func(s *testkit.Sandbox) {
				if scenario == "checkout" {
					os.RemoveAll(filepath.Join(s.Dir, "lane", ".kittify", "doctrine"))
				}
			})
		})
	}
}
