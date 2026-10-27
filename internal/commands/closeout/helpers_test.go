package closeout_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// subst replaces $M with the side's mission slug.
func subst(args []string, slug string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "$M", slug)
	}
	return out
}

func slugOf(t testing.TB, s *testkit.Sandbox) string { return missionfx.MissionSlug(t, s) }

// oracleBoth runs an oracle command ($M substituted) in both sandboxes.
func oracleBoth(t testing.TB, p *testkit.Pair, args ...string) {
	t.Helper()
	p.Both(func(s *testkit.Sandbox) { missionfx.RunOracle(t, s, subst(args, slugOf(t, s))...) })
}

func laneCommit(t testing.TB, s *testkit.Sandbox, lane string, files map[string]string, msg string) {
	t.Helper()
	wt := filepath.Join(".worktrees", slugOf(t, s)+"-"+lane)
	var names []string
	for rel, body := range files {
		s.WriteFile(filepath.Join(wt, rel), body)
		names = append(names, rel)
	}
	if r := s.Exec(wt, "git", append([]string{"add"}, names...)...); r.Code != 0 {
		t.Fatalf("git add: %s", r.Stderr)
	}
	if r := s.Exec(wt, "git", "commit", "-q", "-m", msg); r.Code != 0 {
		t.Fatalf("git commit: %s", r.Stderr)
	}
}

// approvedPair builds (with the oracle, on both sides) the calc mission with
// WP01 and WP02 approved. stage "for_review" stops with WP02 in for_review.
func approvedPair(t *testing.T, stage string, createArgs ...string) *testkit.Pair {
	t.Helper()
	return approvedPairExtra(t, stage, nil, createArgs...)
}

// approvedPairExtra is approvedPair with extra files in lane-a's commit.
func approvedPairExtra(t *testing.T, stage string, extraLaneA map[string]string, createArgs ...string) *testkit.Pair {
	t.Helper()
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p, createArgs...)
	oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--input-file", missionfx.AnalysisReport(), "--json")
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	p.Both(func(s *testkit.Sandbox) {
		files := map[string]string{
			"src/calc.py":            "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n",
			"tests/test_subtract.py": "from src.calc import subtract\n",
		}
		for k, v := range extraLaneA {
			files[k] = v
		}
		laneCommit(t, s, "lane-a", files, "feat(WP01): subtract")
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "ready")
	oracleBoth(t, p, "agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "approved", "--mission", "$M", "--agent", "claude", "--note", "Review passed")
	oracleBoth(t, p, "agent", "action", "implement", "WP02", "--agent", "claude", "--mission", "$M")
	p.Both(func(s *testkit.Sandbox) {
		laneCommit(t, s, "lane-b", map[string]string{
			"src/calc_multiply.py":   "from src.calc import add\n\n\ndef multiply(a, b):\n    return a * b\n",
			"tests/test_multiply.py": "from src.calc_multiply import multiply\n",
		}, "feat(WP02): multiply")
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T004", "T005", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "ready")
	if stage == "for_review" {
		return p
	}
	oracleBoth(t, p, "agent", "action", "review", "WP02", "--agent", "claude", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP02", "--to", "approved", "--mission", "$M", "--agent", "claude", "--note", "Review passed")
	return p
}

// acceptedPair extends approvedPair with verdicts, docs/contracts and an
// oracle accept on both sides.
func acceptedPair(t *testing.T, createArgs ...string) *testkit.Pair {
	t.Helper()
	p := approvedPair(t, "approved", createArgs...)
	recordVerdictsAndDocs(t, p, true)
	oracleBoth(t, p, "agent", "mission", "accept", "--mission", "$M", "--json")
	return p
}

func recordVerdictsAndDocs(t *testing.T, p *testkit.Pair, oracle bool) {
	t.Helper()
	for _, fr := range []string{"FR-001", "FR-002"} {
		args := []string{"agent", "acceptance-verdict", "--mission", "$M", "--criterion", fr, "--result", "pass",
			"--verification-method", "automated_test", "--actor", "claude", "--json"}
		if oracle {
			oracleBoth(t, p, args...)
		} else {
			comparePair(t, p, args...)
		}
	}
	p.Both(func(s *testkit.Sandbox) {
		slug := slugOf(t, s)
		s.WriteFile("docs/README.md", "# docs\n")
		s.WriteFile(filepath.Join("kitty-specs", slug, "contracts", "api.md"), "# api\n")
		s.Git("add", "docs", filepath.Join("kitty-specs", slug, "contracts"))
		s.Git("commit", "-q", "-m", "docs and contracts")
	})
}

// comparePair runs args ($M substituted per side) in both and compares
// exit code and normalized stdout+stderr.
func comparePair(t *testing.T, p *testkit.Pair, args ...string) (testkit.Result, testkit.Result) {
	t.Helper()
	g := p.Go.Run(subst(args, slugOf(t, p.Go))...)
	o := p.Oracle.RunOracle(subst(args, slugOf(t, p.Oracle))...)
	name := strings.Join(args, " ")
	if os.Getenv("KITTY_TEST_TRACE") != "" {
		t.Logf("%s\n== go %d ==\n%s%s== oracle %d ==\n%s%s", name, g.Code, g.Stdout, g.Stderr, o.Code, o.Stdout, o.Stderr)
	}
	g.Stdout, o.Stdout = reCommit.ReplaceAllString(g.Stdout, "Commit: <C>"), reCommit.ReplaceAllString(o.Stdout, "Commit: <C>")
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", name, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	if gs, os := digitSHA(p.Go.Normalize(g.Stdout)), digitSHA(p.Oracle.Normalize(o.Stdout)); gs != os {
		t.Errorf("%s: stdout differs\n%s", name, testkit.Diff(os, gs))
	}
	if gs, os := digitSHA(p.Go.Normalize(g.Stderr)), digitSHA(p.Oracle.Normalize(o.Stderr)); gs != os {
		t.Errorf("%s: stderr differs\n%s", name, testkit.Diff(os, gs))
	}
	return g, o
}

// gitState renders the observable git state of a sandbox.
func gitState(s *testkit.Sandbox) string {
	var b strings.Builder
	// graph-ordered history: `log --all --topo-order` interleaves
	// independent branches by commit date (same-second flakes)
	b.WriteString("$ history\n" + s.GitHistory("", "%s%n%b%n[%D]", "--name-status"))
	for _, args := range [][]string{
		{"worktree", "list", "--porcelain"},
		{"status", "--short", "--untracked-files=all"},
		{"branch", "-a"},
		{"for-each-ref", "--format=%(refname)"},
	} {
		r := s.Exec("", "git", args...)
		b.WriteString("$ git " + strings.Join(args, " ") + "\n" + r.Stdout)
	}
	return s.Normalize(b.String())
}

func compareGit(t *testing.T, p *testkit.Pair) {
	t.Helper()
	if g, o := gitState(p.Go), gitState(p.Oracle); g != o {
		if d := os.Getenv("CLOSEOUT_DUMP_DIR"); d != "" {
			base := filepath.Join(d, strings.ReplaceAll(t.Name(), "/", "_"))
			os.WriteFile(base+".git.go", []byte(g), 0o644)
			os.WriteFile(base+".git.oracle", []byte(o), 0o644)
		}
		t.Errorf("git state differs:\n%s", testkit.Diff(o, g))
	}
}

var reCommit = regexp.MustCompile(`Commit: [0-9a-f]{7}\b`)

// reDigitSHA masks all-digit short SHAs that testkit.Normalize keeps.
var reDigitSHA = regexp.MustCompile(`\b[0-9]{7}(?:[0-9]{3})?\b`)

// (all-letter short SHAs are masked by testkit.Normalize itself)
func digitSHA(s string) string {
	return maskWrappedMid8(reDigitSHA.ReplaceAllString(s, "<SHORTSHA>"))
}

// reWrappedMid8 is a mid8 that rich wrapped across two lines of a long
// path, which testkit.Normalize cannot see as one token.
var reWrappedMid8 = regexp.MustCompile(`-([0-9A-HJKMNP-TV-Z]{1,7})([ \t]*\n[ \t]*)([0-9A-HJKMNP-TV-Z]{1,7})-`)

func maskWrappedMid8(s string) string {
	return reWrappedMid8.ReplaceAllStringFunc(s, func(m string) string {
		g := reWrappedMid8.FindStringSubmatch(m)
		if len(g[1])+len(g[3]) != 8 {
			return m
		}
		return "-<MID8a>" + g[2] + "<MID8b>-"
	})
}

// compareMissionTree compares kitty-specs (and optionally .worktrees),
// dropping the oracle-written analysis-report.md whose issue_counts key
// order is nondeterministic.
func compareMissionTree(t *testing.T, p *testkit.Pair, worktrees bool) {
	t.Helper()
	p.Both(func(s *testkit.Sandbox) {
		matches, _ := filepath.Glob(filepath.Join(s.Dir, "kitty-specs", "*", "analysis-report.md"))
		more, _ := filepath.Glob(filepath.Join(s.Dir, ".worktrees", "*", "kitty-specs", "*", "analysis-report.md"))
		for _, m := range append(matches, more...) {
			os.Remove(m)
		}
	})
	p.CompareTree("kitty-specs", true)
	if worktrees {
		p.CompareTree(".worktrees", true)
	}
}
