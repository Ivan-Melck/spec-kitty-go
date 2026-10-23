package agentcore_test

import (
	"os"
	"path/filepath"
	"regexp"
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
		ga, oa := ownedSubst(p.Go, s, goSlug), ownedSubst(p.Oracle, s, orSlug)
		g := p.Go.Exec(cwd, testkit.Binary(t), ga...)
		o := p.Oracle.Exec(cwd, testkit.Oracle(t), oa...)
		if os.Getenv("KITTY_TEST_TRACE") != "" {
			t.Logf("%v\n== go %d ==\n%s%s== oracle %d ==\n%s%s", s, g.Code, g.Stdout, g.Stderr, o.Code, o.Stdout, o.Stderr)
		}
		if g.Code != o.Code {
			t.Errorf("%v (cwd %q): exit go=%d oracle=%d\ngo stdout:\n%s\ngo stderr:\n%s\noracle stdout:\n%s\noracle stderr:\n%s", s, cwd, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
			continue
		}
		gs, os_ := ownedMask(shortULID.ReplaceAllString(p.Go.Normalize(g.Stdout), "<ULID12>")), ownedMask(shortULID.ReplaceAllString(p.Oracle.Normalize(o.Stdout), "<ULID12>"))
		if gs != os_ {
			t.Errorf("%v (cwd %q): stdout differs\n%s", s, cwd, testkit.Diff(os_, gs))
		}
		ge, oe := ownedMask(p.Go.Normalize(g.Stderr)), ownedMask(p.Oracle.Normalize(o.Stderr))
		if ge != oe {
			t.Errorf("%v (cwd %q): stderr differs\n%s", s, cwd, testkit.Diff(oe, ge))
		}
	}
}

// ownedSubst substitutes $M (the side's mission slug) and a "$MD/" prefix
// (the side's owned mission dir).
func ownedSubst(s *testkit.Sandbox, args []string, slug string) []string {
	out := subst(args, slug)
	md := filepath.Join(missionfx.OwnedDir(s), "kitty-specs", slug)
	for i, a := range out {
		if strings.HasPrefix(a, "$MD/") {
			out[i] = filepath.Join(md, strings.TrimPrefix(a, "$MD/"))
		}
	}
	return out
}

// ownedWalk is the upstream owned e2e quickstart walk
// (tests/integration/test_owned_lifecycle_acceptance_e2e.py): create ->
// spec-commit -> setup-plan -> status -> finalize-tasks -> move-task
// claimed/in_progress -> implementation commit -> for_review -> context
// resolve -> in_review/approved/done -> mark-status, from cwd, with
// --owned-checkout (flag) or by flagless adoption, optionally with a stale
// copy of the mission in R.
func ownedWalk(t *testing.T, cwd string, flag, stale bool, accept ...bool) {
	p := missionfx.NewOwnedPair(t)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.GitIn(t, s, missionfx.OwnedDir(s), "commit", "-q", "--allow-empty", "-m", "owned base")
	})
	ownedSteps(t, p, cwd, "", "", [][]string{createOwnedArgs("--topology", "single_branch", "--json")})
	g, o := missionfx.OwnedMissionSlug(t, p.Go), missionfx.OwnedMissionSlug(t, p.Oracle)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.OwnedMissionSlug(t, s)
		md := filepath.Join(missionfx.OwnedDir(s), "kitty-specs", slug)
		if stale {
			os.MkdirAll(filepath.Join(s.Dir, "kitty-specs"), 0o755)
			os.CopyFS(filepath.Join(s.Dir, "kitty-specs", slug), os.DirFS(md))
			s.WriteFile(filepath.Join("kitty-specs", slug, "tasks", "WP02-stale.md"), "---\nwork_package_id: WP02\ntitle: stale\ndependencies: []\n---\n# WP02\n")
			s.Git("add", "-A")
			s.Git("commit", "-q", "-m", "stale copy of the mission")
		}
		os.WriteFile(filepath.Join(md, "spec.md"), []byte("# Demo\n\n## Functional Requirements\n\n| ID | Requirement | Status |\n|----|-------------|--------|\n| FR-001 | The demo works. | Draft |\n"), 0o644)
	})
	var own []string
	if flag {
		own = []string{"--owned-checkout", missionfx.OwnedRel}
	}
	with := func(args ...string) []string { return append(args, own...) }
	ownedSteps(t, p, cwd, g, o, [][]string{
		with("spec-commit", "$MD/spec.md", "$MD/meta.json", "--message", "docs: spec for demo", "--mission", "$M", "--json"),
		with("agent", "mission", "setup-plan", "--mission", "$M", "--json"),
		with("agent", "tasks", "status", "--mission", "$M", "--json"),
	})
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.OwnedMissionSlug(t, s)
		owned := missionfx.OwnedSandbox(s)
		md := filepath.Join("kitty-specs", slug)
		owned.WriteFile(filepath.Join(md, "tasks.md"), "# Tasks\n\n## Work Package WP01\n\n**Dependencies**: None\n\nImplement the demo.\n")
		owned.WriteFile(filepath.Join(md, "tasks", "WP01-demo.md"), "---\nwork_package_id: WP01\ntitle: Demo work package\ndependencies: []\nrequirement_refs: [FR-001]\nsubtasks: []\nowned_files: [demo.py]\nauthoritative_surface: demo.py\nexecution_mode: code_change\ncreate_intent:\n  - demo.py\n---\n\n# WP01\n\nImplement the demo.\n")
		owned.WriteFile("demo.py", "# demo\n")
		owned.Git("add", "-A")
		owned.Git("commit", "-q", "-m", "tasks: demo work package")
	})
	ownedSteps(t, p, cwd, g, o, [][]string{
		with("agent", "mission", "finalize-tasks", "--mission", "$M", "--json"),
		with("agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude", "--json"),
		with("agent", "tasks", "move-task", "WP01", "--to", "in_progress", "--mission", "$M", "--agent", "claude", "--json"),
		with("agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"),
	})
	p.Both(func(s *testkit.Sandbox) {
		owned := missionfx.OwnedSandbox(s)
		owned.WriteFile("demo.py", "# demo\nVALUE = 1\n")
		owned.Git("add", "-A")
		owned.Git("commit", "-q", "-m", "feat: implement demo")
	})
	ownedSteps(t, p, cwd, g, o, [][]string{
		with("agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--json"),
		with("agent", "context", "resolve", "--action", "implement", "--mission", "$M", "--wp-id", "WP01", "--json"),
		with("agent", "context", "resolve", "--action", "implement", "--mission", "$M", "--wp-id", "WP01"),
		with("agent", "tasks", "status", "--mission", "$M"),
		with("agent", "tasks", "move-task", "WP01", "--to", "in_review", "--mission", "$M", "--agent", "reviewer", "--json"),
		with("agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "reviewer", "--note", "ok", "--json"),
		with("agent", "tasks", "move-task", "WP01", "--to", "done", "--mission", "$M", "--agent", "reviewer", "--json"),
		with("agent", "tasks", "mark-status", "T001", "--status", "done", "--mission", "$M", "--json"),
		with("agent", "tasks", "status", "--mission", "$M", "--json"),
	})
	if len(accept) > 0 && accept[0] {
		ownedSteps(t, p, cwd, g, o, [][]string{
			with("agent", "acceptance-verdict", "--mission", "$M", "--criterion", "FR-001", "--result", "pass", "--verification-method", "manual", "--json"),
			with("agent", "mission", "acceptance-verdict", "--mission", "$M", "--negative-invariant", "NI-001", "--description", "no todo",
				"--verification-method", "grep_absence", "--verification-command", "TODO_MARKER", "--json"),
			{"agent", "acceptance-verdict", "--mission", "$M", "--owned-checkout", ".", "--criterion", "FR-001", "--result", "pass", "--json"},
			with("accept", "--mission", "$M", "--json", "--no-commit"),
			with("accept", "--mission", "$M", "--diagnose", "--json"),
			with("accept", "--mission", "$M", "--diagnose", "--normalize-encoding", "--json"),
			with("accept", "--mission", "$M", "--json"),
			{"accept", "--mission", "$M", "--owned-checkout", ".", "--json"},
			{"accept", "--mission", "$M", "--owned-checkout", "/nonexistent-owned"},
			with("agent", "mission", "accept", "--mission", "$M", "--json"),
		})
	}
	p.CompareTree(missionfx.OwnedRel+"/kitty-specs", true)
	compareOwnedGit(t, p)
}

// TestDiffOwnedLifecycleWalk covers OWN-01/OWN-02/OWN-03/OWN-07: the owned
// lifecycle walk with --owned-checkout from R, P and S, by flagless adoption
// from P (and its non-adoption from R / S), and with a stale repository-root
// copy of the mission.
func TestDiffOwnedLifecycleWalk(t *testing.T) {
	cases := []struct {
		name        string
		cwd         string
		flag, stale bool
	}{
		{"flag-from-R", "", true, false},
		{"flag-from-P", missionfx.OwnedRel, true, false},
		{"flag-from-S", missionfx.SiblingRel, true, false},
		{"adopt-from-P", missionfx.OwnedRel, false, false},
		{"flagless-from-R", "", false, false},
		{"flagless-from-S", missionfx.SiblingRel, false, false},
		{"flag-from-R-stale", "", true, true},
		{"adopt-from-P-stale", missionfx.OwnedRel, false, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ownedWalk(t, c.cwd, c.flag, c.stale)
		})
	}
}

var (
	runIDRe      = regexp.MustCompile(`\b[0-9a-f]{32}\b`)
	promptKeyRe  = regexp.MustCompile(`spec-kitty-prompts/[0-9a-f]{16}/`)
	issueCountRe = regexp.MustCompile(`"issue_counts": \{[^}]*\}`)
)

// ownedMask hides run ids (uuid4 hex), the path-derived prompt cache key
// and issue_counts (a frozenset-ordered dict upstream).
func ownedMask(s string) string {
	s = runIDRe.ReplaceAllString(s, "<RUNID>")
	s = promptKeyRe.ReplaceAllString(s, "spec-kitty-prompts/<KEY>/")
	return issueCountRe.ReplaceAllString(s, `"issue_counts": <C>`)
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

// TestDiffOwnedNextAndActions covers OWN-05: `next --owned-checkout`
// (query, advancing with the owned advancement commit, the analyze-step
// currency check governed by R) and the `agent action implement|review
// --owned-checkout` refusals.
func TestDiffOwnedNextAndActions(t *testing.T) {
	cases := []struct {
		name string
		cwd  string
		own  []string
	}{
		{"flag-from-R", "", []string{"--owned-checkout", missionfx.OwnedRel}},
		{"adopt-from-P", missionfx.OwnedRel, nil},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := missionfx.NewOwnedPair(t)
			g, o := missionfx.OracleOwnedMissionNamed(t, p, "calc-ops")
			with := func(args ...string) []string { return append(args, c.own...) }
			ownedSteps(t, p, c.cwd, g, o, [][]string{
				{"agent", "action", "implement", "WP01", "--agent", "claude", "--owned-checkout", missionfx.OwnedRel},
				{"agent", "action", "implement", "WP01", "--agent", "claude", "--owned-checkout", missionfx.OwnedRel, "--mission", "$M"},
				{"agent", "action", "review", "WP01", "--agent", "claude", "--owned-checkout", ".", "--mission", "$M"},
				{"agent", "action", "review", "--owned-checkout", "/nonexistent-owned"},
				{"next", "--json", "--owned-checkout", "/nonexistent-owned"},
				with("next", "--json"),
				with("next", "--mission", "$M", "--json"),
				with("next", "--agent", "claude", "--mission", "$M", "--json"),
				with("next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"),
			})
			p.Both(func(s *testkit.Sandbox) {
				missionfx.AuthorCalc(t, missionfx.OwnedSandbox(s), missionfx.OwnedMissionSlug(t, s))
			})
			ownedSteps(t, p, c.cwd, g, o, [][]string{
				with("next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"),
				with("next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"),
				with("next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"),
				with("next", "--agent", "claude", "--mission", "$M", "--result", "success"),
				with("agent", "mission", "finalize-tasks", "--mission", "$M", "--json"),
				with("agent", "mission", "record-analysis", "--mission", "$M", "--input-file", missionfx.AnalysisReport(), "--json"),
				with("next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"),
				with("next", "--agent", "claude", "--mission", "$M"),
			})
			compareOwnedGit(t, p)
		})
	}
}

// TestDiffOwnedAcceptance covers OWN-06: agent [mission] acceptance-verdict
// and accept with --owned-checkout and by flagless adoption (refusals:
// repository root, --diagnose --normalize-encoding).
func TestDiffOwnedAcceptance(t *testing.T) {
	cases := []struct {
		name string
		cwd  string
		flag bool
	}{
		{"flag-from-R", "", true},
		{"adopt-from-P", missionfx.OwnedRel, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ownedWalk(t, c.cwd, c.flag, false, true)
		})
	}
}
