package agentcore_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// comparePairSteps runs each command in both sandboxes (mission handle
// substituted per side) and compares exit code + stdout.
func comparePairSteps(t *testing.T, p *testkit.Pair, goSlug, orSlug string, steps [][]string) {
	t.Helper()
	for _, s := range steps {
		ga, oa := subst(s, goSlug), subst(s, orSlug)
		g := p.Go.Run(ga...)
		o := p.Oracle.RunOracle(oa...)
		if os.Getenv("KITTY_TEST_TRACE") != "" {
			t.Logf("%v\n== go %d ==\n%s%s== oracle %d ==\n%s%s", s, g.Code, g.Stdout, g.Stderr, o.Code, o.Stdout, o.Stderr)
		}
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d\ngo stdout:\n%s\ngo stderr:\n%s\noracle stdout:\n%s\noracle stderr:\n%s", s, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
			continue
		}
		gs, os_ := shortULID.ReplaceAllString(p.Go.Normalize(g.Stdout), "<ULID12>"), shortULID.ReplaceAllString(p.Oracle.Normalize(o.Stdout), "<ULID12>")
		if gs != os_ {
			t.Errorf("%v: stdout differs\n%s", s, testkit.Diff(os_, gs))
		}
		ge, oe := p.Go.Normalize(g.Stderr), p.Oracle.Normalize(o.Stderr)
		if ge != oe {
			t.Errorf("%v: stderr differs\n%s", s, testkit.Diff(oe, ge))
		}
	}
}

// shortULID masks the 12-char event id prefixes status emit prints (the
// "..." is a separate highlighted span in colour mode).
var shortULID = regexp.MustCompile(`\b0[0-9A-HJKMNP-TV-Z]{11}(?:\x1b\[[0-9;]*m)*\.\.\.`)

func subst(args []string, slug string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "$M" {
			a = slug
		}
		out[i] = a
	}
	return out
}

func TestDiffTasksReadAndAnnotate(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	steps := [][]string{
		{"agent", "tasks", "status", "--mission", "$M"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
		{"agent", "tasks", "status"},
		{"agent", "tasks", "status", "--json"},
		{"agent", "tasks", "status", "--mission", "nonexist"},
		{"agent", "tasks", "status", "--mission", "nonexist", "--json"},
		{"agent", "tasks", "list-tasks", "--mission", "$M", "--json"},
		{"agent", "tasks", "list-tasks", "--mission", "$M"},
		{"agent", "tasks", "list-tasks", "--mission", "$M", "--lane", "doing", "--json"},
		{"agent", "tasks", "list-tasks", "--mission", "$M", "--lane", "doing"},
		{"agent", "tasks", "list-tasks", "--mission", "$M", "--lane", "planned"},
		{"agent", "tasks", "list-tasks", "--json"},
		{"agent", "status", "materialize", "--mission", "$M", "--json"},
		{"agent", "status", "materialize", "--mission", "$M"},
		{"agent", "status", "materialize", "--json"},
		{"agent", "tasks", "add-history", "WP01", "--note", "hello note", "--mission", "$M", "--json"},
		{"agent", "tasks", "add-history", "WP01", "--note", "second", "--agent", "claude", "--shell-pid", "123", "--mission", "$M"},
		{"agent", "tasks", "add-history", "WP09", "--note", "x", "--mission", "$M", "--json"},
		{"agent", "tasks", "mark-status", "T001", "T002", "--status", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "mark-status", "T001", "--status", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "mark-status", "T003", "--status", "pending", "--mission", "$M"},
		{"agent", "tasks", "mark-status", "T004", "T005", "--status", "skipped", "--mission", "$M"},
		{"agent", "tasks", "mark-status", "T999", "--status", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "mark-status", "T999", "--status", "done", "--mission", "$M"},
		{"agent", "tasks", "mark-status", "WP01", "--status", "done", "--mission", "$M"},
		{"agent", "tasks", "mark-status", "T001", "--status", "bogus", "--mission", "$M", "--json"},
		{"agent", "tasks", "mark-status", "T001", "--wp", "WP02", "--status", "done", "--mission", "$M", "--json"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
		{"agent", "status", "materialize", "--mission", "$M", "--json"},
	}
	comparePairSteps(t, p, g, o, steps)
	p.CompareTree(".worktrees", true)
	p.CompareTree("kitty-specs", true)
	compareGit(t, p)
}
