package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// Policy is a valid --policy payload.
const testPolicy = `{"orchestrator_id":"orch","orchestrator_version":"1.0","agent_family":"claude","approval_mode":"auto","sandbox_mode":"workspace","network_mode":"none","dangerous_flags":[]}`

var (
	reCorr  = regexp.MustCompile(`(corr|hist)-[0-9a-f]{32}`)
	reRunID = regexp.MustCompile(`"run_id": "[0-9a-f]{32}"`)
	// rePyTime masks python-style "2026-10-21 22:59:16.123+00:00" too.
	reGenAt = regexp.MustCompile(`"(timestamp|generated_at|accepted_at|reread_generated_at)": "[^"]*"`)
	// prompt directories are keyed by a project-path hash; templateless
	// step prompts carry a mkstemp suffix
	rePromptDir  = regexp.MustCompile(`spec-kitty-prompts/[0-9a-f]{16}/`)
	reStepPrompt = regexp.MustCompile(`(spec-kitty-step-[^"/]*)-(?:[a-z0-9_]{8}|<SHORTSHA>)\.md`)
)

func normEnvelope(s *testkit.Sandbox, out string) string {
	out = reCorr.ReplaceAllString(out, "$1-X")
	out = reRunID.ReplaceAllString(out, `"run_id": "<RUN>"`)
	out = reGenAt.ReplaceAllString(out, `"$1": "<TS>"`)
	out = rePromptDir.ReplaceAllString(out, "spec-kitty-prompts/<H>/")
	out = reStepPrompt.ReplaceAllString(out, "$1-<RND>.md")
	return s.Normalize(out)
}

// step runs args in both sandboxes and compares exit code + normalized stdout.
func step(t *testing.T, p *testkit.Pair, args ...string) (testkit.Result, testkit.Result) {
	t.Helper()
	g, o := p.Run(args...)
	if os.Getenv("KITTY_TEST_TRACE") == "1" {
		t.Logf("$ %s\n[oracle %d] %s%s\n[go %d] %s%s", strings.Join(args, " "), o.Code, o.Stdout, o.Stderr, g.Code, g.Stdout, g.Stderr)
	}
	if g.Code != o.Code {
		t.Errorf("%s: exit code go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", strings.Join(args, " "), g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	gs, os_ := normEnvelope(p.Go, g.Stdout), normEnvelope(p.Oracle, o.Stdout)
	if gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
	return g, o
}

// stepIn is step with stdin content.
func stepStdin(t *testing.T, p *testkit.Pair, stdin string, args ...string) {
	t.Helper()
	g := execStdin(t, p.Go, testkit.Binary(t), stdin, args...)
	o := execStdin(t, p.Oracle, testkit.Oracle(t), stdin, args...)
	if os.Getenv("KITTY_TEST_TRACE") == "1" {
		t.Logf("$ %s < %q\n[oracle %d] %s%s\n[go %d] %s%s", strings.Join(args, " "), stdin, o.Code, o.Stdout, o.Stderr, g.Code, g.Stdout, g.Stderr)
	}
	if g.Code != o.Code {
		t.Errorf("%s: exit code go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", strings.Join(args, " "), g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	gs, os_ := normEnvelope(p.Go, g.Stdout), normEnvelope(p.Oracle, o.Stdout)
	if gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
}

func execStdin(t *testing.T, s *testkit.Sandbox, exe, stdin string, args ...string) testkit.Result {
	t.Helper()
	f := filepath.Join(s.Base, "stdin.txt")
	if err := os.WriteFile(f, []byte(stdin), 0o644); err != nil {
		t.Fatal(err)
	}
	return s.Exec("", "/bin/sh", append([]string{"-c", `exec "$0" "$@" < "` + f + `"`, exe}, args...)...)
}

// gitState renders refs, commit graph (subjects + file sets), worktrees and
// status for comparison.
func gitState(s *testkit.Sandbox) string {
	var b strings.Builder
	// Per-branch first-parent histories are deterministic; --all
	// --topo-order reorders same-second commits on independent branches.
	for _, ref := range strings.Fields(s.Git("for-each-ref", "--format=%(refname)", "refs/heads")) {
		b.WriteString("## " + ref + "\n")
		b.WriteString(s.Git("log", "--first-parent", "--format=%s%n%b--", "--name-status", ref))
		b.WriteString("\n")
	}
	all := strings.Split(s.Git("log", "--all", "--format=%s"), "\n")
	sort.Strings(all)
	b.WriteString("## all subjects\n" + strings.Join(all, "\n"))
	b.WriteString("\n## branches\n")
	b.WriteString(s.Git("branch", "-a", "--format=%(refname)"))
	b.WriteString("\n## refs\n")
	b.WriteString(s.Git("for-each-ref", "--format=%(refname)", "refs/spec-kitty"))
	b.WriteString("\n## worktrees\n")
	for _, l := range strings.Split(s.Git("worktree", "list", "--porcelain"), "\n") {
		if strings.HasPrefix(l, "worktree ") || strings.HasPrefix(l, "branch ") {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("## status\n")
	b.WriteString(s.Git("status", "--porcelain", "--untracked-files=all"))
	return s.Normalize(b.String())
}

func compareGit(t *testing.T, p *testkit.Pair, label string) {
	t.Helper()
	g, o := gitState(p.Go), gitState(p.Oracle)
	if g != o {
		t.Errorf("%s: git state differs\n%s", label, testkit.Diff(o, g))
	}
}

// compareFiles compares normalized contents of files (paths relative to the
// project, with {slug} replaced by each side's mission slug).
func compareFiles(t *testing.T, p *testkit.Pair, goSlug, oSlug string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		gb, gerr := os.ReadFile(filepath.Join(p.Go.Dir, strings.ReplaceAll(rel, "{slug}", goSlug)))
		ob, oerr := os.ReadFile(filepath.Join(p.Oracle.Dir, strings.ReplaceAll(rel, "{slug}", oSlug)))
		if (gerr == nil) != (oerr == nil) {
			t.Errorf("%s: existence differs (go err %v, oracle err %v)", rel, gerr, oerr)
			continue
		}
		gn, on := normEnvelope(p.Go, string(gb)), normEnvelope(p.Oracle, string(ob))
		if gn != on {
			t.Errorf("%s differs:\n%s", rel, testkit.Diff(on, gn))
		}
	}
}
