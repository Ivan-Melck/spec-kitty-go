package agentcore_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
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

func TestDiffMissionCreate(t *testing.T) {
	cases := []struct {
		name  string
		setup []string // git args run in both before create
		args  []string
	}{
		{"pr-bound-start-branch", nil, []string{"calc-ops", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops"}},
		{"on-main-protected", nil, []string{"calc-ops", "--json"}},
		{"lanes-research-human", []string{"switch", "-q", "-c", "feat/x"}, []string{"lanes-one", "--topology", "lanes", "--mission-type", "research", "--friendly-name", "Lanes  One", "--purpose-tldr", "tl dr", "--purpose-context", "ctx here"}},
		{"single-branch", []string{"switch", "-q", "-c", "feat/x"}, []string{"single-one", "--topology", "single_branch", "--json"}},
		{"documentation", []string{"switch", "-q", "-c", "feat/x"}, []string{"doc-one", "--mission-type", "documentation", "--json", "--topology", "lanes"}},
		{"bad-slug-json", nil, []string{"Bad_Slug", "--json"}},
		{"bad-slug-human", nil, []string{"Bad_Slug"}},
		{"pr-gate", []string{"switch", "-q", "-c", "feat/x"}, []string{"pr-one", "--pr-bound", "--json"}},
		{"start-target-mismatch", nil, []string{"pr-three", "--pr-bound", "--json", "--start-branch", "feat/y", "--target-branch", "other"}},
		{"unknown-type", nil, []string{"nope", "--mission-type", "nonexistent", "--json"}},
		{"coord-human", []string{"switch", "-q", "-c", "feat/z"}, []string{"calc-ops"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := newOraclePair(t)
			if c.setup != nil {
				p.Both(func(s *testkit.Sandbox) { s.Git(c.setup...) })
			}
			p.Compare(false, append([]string{"agent", "mission", "create"}, c.args...)...)
			p.CompareTree("kitty-specs", true)
			p.CompareTree(".worktrees", true)
			compareGit(t, p)
		})
	}
}

func TestDiffMissionCreateDuplicate(t *testing.T) {
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) { s.Git("switch", "-q", "-c", "feat/x") })
	p.Compare(false, "agent", "mission", "create", "dup", "--json", "--topology", "lanes")
	// commit the spec so the prior is live
	p.Both(func(s *testkit.Sandbox) {
		s.Git("add", "-A", "kitty-specs")
		s.Git("commit", "-q", "-m", "spec")
	})
	p.Compare(false, "agent", "mission", "create", "dup", "--json", "--topology", "lanes")
	p.Compare(false, "agent", "mission", "create", "dup", "--topology", "lanes")
	compareGit(t, p)
}

func TestDiffBranchContext(t *testing.T) {
	p := newOraclePair(t)
	p.Compare(false, "agent", "mission", "branch-context", "--json")
	p.Compare(false, "agent", "mission", "branch-context")
	p.Compare(false, "agent", "mission", "branch-context", "--json", "--target-branch", "feat/x")
	p.Both(func(s *testkit.Sandbox) { s.Git("switch", "-q", "-c", "feat/x") })
	p.Compare(false, "agent", "mission", "branch-context")
	p.Compare(false, "agent", "mission", "branch-context", "--json")
}

// TestDiffMissionCreatePendingOrigin covers CORE-09: a staged
// .kittify/pending-origin.yaml makes create fail closed after the scaffold
// commit (PendingOriginConsumerNotRegisteredError) and rolls back the git
// side effects while keeping the on-disk scaffold.
func TestDiffMissionCreatePendingOrigin(t *testing.T) {
	cases := []struct {
		name  string
		setup []string
		args  []string
	}{
		{"coord-on-main-json", nil, []string{"calc-ops", "--json"}},
		{"coord-human", []string{"switch", "-q", "-c", "feat/z"}, []string{"calc-ops"}},
		{"lanes-json", []string{"switch", "-q", "-c", "feat/x"}, []string{"lanes-one", "--topology", "lanes", "--json"}},
		{"single-branch", []string{"switch", "-q", "-c", "feat/x"}, []string{"single-one", "--topology", "single_branch", "--json"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := newOraclePair(t)
			p.Both(func(s *testkit.Sandbox) {
				if c.setup != nil {
					s.Git(c.setup...)
				}
				s.WriteFile(".kittify/pending-origin.yaml", "provider: linear\nticket: ABC-1\n")
			})
			p.Compare(false, append([]string{"agent", "mission", "create"}, c.args...)...)
			p.CompareTree("kitty-specs", false)
			p.CompareTree(".worktrees", false)
			compareGit(t, p)
		})
	}
}
