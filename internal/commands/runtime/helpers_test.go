package runtime_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

var (
	rePID        = regexp.MustCompile(`PID: \d+`)
	rePromptHash = regexp.MustCompile(`spec-kitty-prompts/[0-9a-f]{16}/`)
	reReviewDir  = regexp.MustCompile(`proj-[0-9a-f]{12}/`)
	reHex32      = regexp.MustCompile(`\b[0-9a-f]{32}\b`)
	reShortULID  = regexp.MustCompile(`\b0[0-9A-HJKMNP-TV-Z]{11}\.\.\.`)
	reCreatedAt  = regexp.MustCompile(`"shell_pid_created_at": "[0-9.]+"`)
	reDBMid8     = regexp.MustCompile(`_0[0-9A-HJKMNP-TV-Z]{7}_`)
	reParents    = regexp.MustCompile(`\[parents:([0-9a-f ]*)\]`)
)

// norm normalizes run-specific values of one sandbox's output.
func norm(s *testkit.Sandbox, text string) string {
	text = s.Normalize(text)
	text = rePID.ReplaceAllString(text, "PID: <PID>")
	text = rePromptHash.ReplaceAllString(text, "spec-kitty-prompts/<H16>/")
	text = reReviewDir.ReplaceAllString(text, "proj-<H12>/")
	text = reHex32.ReplaceAllString(text, "<HEX32>")
	text = reShortULID.ReplaceAllString(text, "<ULID12>")
	text = reCreatedAt.ReplaceAllString(text, `"shell_pid_created_at": "<CT>"`)
	text = reDBMid8.ReplaceAllString(text, "_<MID8>_")
	return text
}

func subst(args []string, slug string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "$M", slug)
	}
	return out
}

// steps runs each command on both sides (mission handle substituted) and
// compares exit code, stdout and (optionally) stderr.
func steps(t *testing.T, p *testkit.Pair, withStderr bool, cmds ...[]string) {
	t.Helper()
	for _, c := range cmds {
		g := p.Go.Run(subst(c, missionfx.MissionSlug(t, p.Go))...)
		o := p.Oracle.RunOracle(subst(c, missionfx.MissionSlug(t, p.Oracle))...)
		if os.Getenv("KITTY_TEST_TRACE") != "" {
			t.Logf("%v\n== go %d ==\n%s%s== oracle %d ==\n%s%s", c, g.Code, g.Stdout, g.Stderr, o.Code, o.Stdout, o.Stderr)
		}
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d\ngo:\n%s%s\noracle:\n%s%s", c, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
			continue
		}
		if gs, os_ := norm(p.Go, g.Stdout), norm(p.Oracle, o.Stdout); gs != os_ {
			t.Errorf("%v: stdout differs\n%s", c, testkit.Diff(os_, gs))
		}
		if withStderr {
			if ge, oe := norm(p.Go, g.Stderr), norm(p.Oracle, o.Stderr); ge != oe {
				t.Errorf("%v: stderr differs\n%s", c, testkit.Diff(oe, ge))
			}
		}
	}
}

// oracleBoth runs an oracle command in both sandboxes as setup.
func oracleBoth(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	p.Both(func(s *testkit.Sandbox) { missionfx.RunOracle(t, s, subst(args, missionfx.MissionSlug(t, s))...) })
}

// gitState renders the observable git state of a sandbox: per ref, the
// first-parent history (subject, body, parent count, name-status), plus
// worktrees and status. Independent of the cross-ref commit order.
func gitState(s *testkit.Sandbox) string {
	var b strings.Builder
	refs := strings.Fields(s.Exec("", "git", "for-each-ref", "--format=%(refname)", "refs/heads", "refs/spec-kitty").Stdout)
	for _, ref := range refs {
		r := s.Exec("", "git", "log", "--first-parent", "--format=%s%n%b[parents:%p]", "--name-status", ref)
		out := reParents.ReplaceAllStringFunc(r.Stdout, func(m string) string {
			return "[parents:" + strconv.Itoa(len(strings.Fields(reParents.FindStringSubmatch(m)[1]))) + "]"
		})
		b.WriteString("$ " + ref + "\n" + out)
	}
	for _, args := range [][]string{{"worktree", "list", "--porcelain"}, {"status", "--short", "--untracked-files=all"}} {
		r := s.Exec("", "git", args...)
		b.WriteString("$ git " + strings.Join(args, " ") + "\n" + r.Stdout)
	}
	return norm(s, b.String())
}

func compareGit(t *testing.T, p *testkit.Pair) {
	t.Helper()
	if g, o := gitState(p.Go), gitState(p.Oracle); g != o {
		t.Errorf("git state differs:\n%s", testkit.Diff(o, g))
	}
}

// compareTree compares file lists and normalized contents under rel
// (files whose path contains an ignore fragment are skipped).
func compareTree(t *testing.T, p *testkit.Pair, rel string, ignore ...string) {
	t.Helper()
	list := func(s *testkit.Sandbox) []string {
		var out []string
	outer:
		for _, f := range testkit.Files(filepath.Join(s.Dir, rel)) {
			for _, ig := range ignore {
				if strings.Contains(f, ig) {
					continue outer
				}
			}
			out = append(out, f)
		}
		return out
	}
	gf, of := list(p.Go), list(p.Oracle)
	gn, on := norm(p.Go, strings.Join(gf, "\n")), norm(p.Oracle, strings.Join(of, "\n"))
	if gn != on {
		t.Errorf("tree %s differs (oracle vs go):\n%s", rel, testkit.Diff(on, gn))
		return
	}
	for i := range gf {
		gb, _ := os.ReadFile(filepath.Join(p.Go.Dir, rel, gf[i]))
		ob, _ := os.ReadFile(filepath.Join(p.Oracle.Dir, rel, of[i]))
		if g, o := norm(p.Go, string(gb)), norm(p.Oracle, string(ob)); g != o {
			t.Errorf("file %s differs:\n%s", filepath.Join(rel, gf[i]), testkit.Diff(o, g))
		}
	}
}

// laneDir is the lane worktree dir of lane id on side s.
func laneDir(t *testing.T, s *testkit.Sandbox, lane string) string {
	return filepath.Join(".worktrees", missionfx.MissionSlug(t, s)+"-"+lane)
}

// laneCommit writes files into a lane worktree and commits them.
func laneCommit(t *testing.T, p *testkit.Pair, lane, msg string, files map[string]string) {
	p.Both(func(s *testkit.Sandbox) {
		d := laneDir(t, s, lane)
		var names []string
		for f, c := range files {
			s.WriteFile(filepath.Join(d, f), c)
			names = append(names, f)
		}
		s.Exec(d, "git", append([]string{"add"}, names...)...)
		if r := s.Exec(d, "git", "commit", "-q", "-m", msg); r.Code != 0 {
			t.Fatalf("lane commit: %s%s", r.Stdout, r.Stderr)
		}
	})
}

// newAnalyzedPair is the calc mission (oracle create + finalize) with the
// analysis report recorded on both sides.
func newAnalyzedPair(t *testing.T) *testkit.Pair {
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p)
	oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--input-file", missionfx.AnalysisReport(), "--json")
	return p
}

// dropAnalysis removes the analysis report (nondeterministic key order).
func dropAnalysis(t *testing.T, p *testkit.Pair) {
	p.Both(func(s *testkit.Sandbox) {
		os.Remove(filepath.Join(s.Dir, "kitty-specs", missionfx.MissionSlug(t, s), "analysis-report.md"))
	})
}
