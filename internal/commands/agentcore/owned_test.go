package agentcore_test

import (
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// ownedSteps runs each step (with $M → the side's mission slug) from cwd
// (relative to R: "" = R, missionfx.OwnedRel = P, missionfx.SiblingRel = S)
// with Go and the oracle and compares exit code, stdout and stderr.
func ownedSteps(t *testing.T, p *testkit.Pair, cwd, goSlug, orSlug string, steps [][]string) {
	t.Helper()
	for _, s := range steps {
		ga, oa := subst(s, goSlug), subst(s, orSlug)
		g := p.Go.Exec(cwd, testkit.Binary(t), ga...)
		o := p.Oracle.Exec(cwd, testkit.Oracle(t), oa...)
		if os.Getenv("KITTY_TEST_TRACE") != "" {
			t.Logf("%v\n== go %d ==\n%s%s== oracle %d ==\n%s%s", s, g.Code, g.Stdout, g.Stderr, o.Code, o.Stdout, o.Stderr)
		}
		if g.Code != o.Code {
			t.Errorf("%v (cwd %q): exit go=%d oracle=%d\ngo stdout:\n%s\ngo stderr:\n%s\noracle stdout:\n%s\noracle stderr:\n%s", s, cwd, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
			continue
		}
		gs, os_ := shortULID.ReplaceAllString(p.Go.Normalize(g.Stdout), "<ULID12>"), shortULID.ReplaceAllString(p.Oracle.Normalize(o.Stdout), "<ULID12>")
		if gs != os_ {
			t.Errorf("%v (cwd %q): stdout differs\n%s", s, cwd, testkit.Diff(os_, gs))
		}
		ge, oe := p.Go.Normalize(g.Stderr), p.Oracle.Normalize(o.Stderr)
		if ge != oe {
			t.Errorf("%v (cwd %q): stderr differs\n%s", s, cwd, testkit.Diff(oe, ge))
		}
	}
}

// ownedGitState is gitState plus the owned and sibling checkouts' status.
func ownedGitState(s *testkit.Sandbox) string {
	var b strings.Builder
	b.WriteString(gitState(s))
	for _, dir := range []string{missionfx.OwnedDir(s), missionfx.SiblingDir(s)} {
		r := s.Exec(dir, "git", "status", "--short", "--untracked-files=all")
		b.WriteString("$ git -C " + dir + " status --short\n" + r.Stdout)
	}
	return s.Normalize(b.String())
}

func compareOwnedGit(t *testing.T, p *testkit.Pair) {
	t.Helper()
	g, o := ownedGitState(p.Go), ownedGitState(p.Oracle)
	if g != o {
		t.Errorf("git state differs:\n%s", testkit.Diff(o, g))
	}
}

func createOwnedArgs(extra ...string) []string {
	return append([]string{"agent", "mission", "create", "demo", "--mission-type", "software-dev", "--owned-checkout", missionfx.OwnedRel,
		"--target-branch", missionfx.OwnedBranch, "--branch-strategy", "already-confirmed"}, extra...)
}

// TestDiffOwnedMissionCreate covers OWN-02 (agent mission create
// --owned-checkout): the scaffold lands and is committed in P (single_branch
// by default), the payload carries owned_checkout / canonical_repo_root,
// and invalid claims are refused with the typed envelope.
func TestDiffOwnedMissionCreate(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"json-default-topology", createOwnedArgs("--json")},
		{"json-explicit", createOwnedArgs("--topology", "single_branch", "--json")},
		{"human", createOwnedArgs("--topology", "single_branch")},
		{"lanes-with-coord", createOwnedArgs("--topology", "lanes_with_coord", "--json")},
		{"repo-root-json", []string{"agent", "mission", "create", "demo", "--owned-checkout", ".", "--json"}},
		{"repo-root-human", []string{"agent", "mission", "create", "demo", "--owned-checkout", "."}},
		{"missing", []string{"agent", "mission", "create", "demo", "--owned-checkout", "/nonexistent-owned", "--json"}},
		{"nested", []string{"agent", "mission", "create", "demo", "--owned-checkout", ".kittify", "--json"}},
		{"not-a-repo", []string{"agent", "mission", "create", "demo", "--owned-checkout", "/", "--json"}},
		{"sibling", []string{"agent", "mission", "create", "demo", "--owned-checkout", missionfx.SiblingRel, "--json"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := missionfx.NewOwnedPair(t)
			ownedSteps(t, p, "", "", "", [][]string{c.args})
			p.CompareTree(missionfx.OwnedRel+"/kitty-specs", true)
			p.CompareTree("kitty-specs", true)
			compareOwnedGit(t, p)
		})
	}
}

// TestDiffOwnedTasksStatus covers OWN-01/OWN-02 for agent tasks status:
// explicit --owned-checkout from R / P / S, the refusal envelopes, flagless
// adoption from P, and the stale repository-root copy channel.
func TestDiffOwnedTasksStatus(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOwnedPair(t)
	g, o := missionfx.OracleOwnedMission(t, p)
	steps := [][]string{
		{"agent", "tasks", "status", "--owned-checkout", missionfx.OwnedRel, "--mission", "$M", "--json"},
		{"agent", "tasks", "status", "--owned-checkout", missionfx.OwnedRel, "--mission", "$M"},
		{"agent", "tasks", "status", "--owned-checkout", missionfx.OwnedRel, "--json"},
		{"agent", "tasks", "status", "--owned-checkout", missionfx.OwnedRel, "--mission", "nope", "--json"},
		{"agent", "tasks", "status", "--owned-checkout", missionfx.SiblingRel, "--mission", "$M", "--json"},
		{"agent", "tasks", "status", "--owned-checkout", "/nonexistent-owned", "--mission", "$M", "--json"},
		{"agent", "tasks", "status", "--owned-checkout", "/nonexistent-owned", "--mission", "$M"},
		{"agent", "tasks", "status", "--mission", "$M", "--json"},
		{"agent", "tasks", "status", "--json"},
	}
	for _, cwd := range []string{"", missionfx.OwnedRel, missionfx.SiblingRel} {
		ownedSteps(t, p, cwd, g, o, steps)
	}
	// R holds a stale copy of the mission (same mission_id).
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.OwnedMissionSlug(t, s)
		os.CopyFS(s.Dir+"/kitty-specs/"+slug, os.DirFS(missionfx.OwnedDir(s)+"/kitty-specs/"+slug))
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "stale copy")
	})
	ownedSteps(t, p, "", g, o, steps[:2])
	ownedSteps(t, p, missionfx.OwnedRel, g, o, [][]string{{"agent", "tasks", "status", "--mission", "$M", "--json"}})
	compareOwnedGit(t, p)
}
