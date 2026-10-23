package charterenc_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

var (
	reTraceFrame = regexp.MustCompile(`(?m)^│.*$`)
	reHex32      = regexp.MustCompile(`\b[0-9a-f]{32}\b|spec-kitty-prompts/[0-9a-f]{16}/|_[0-9A-Z]{8}_lane`)
)

// excLines is the stderr text outside the rich traceback box.
func excLines(s string) string {
	return strings.TrimSpace(reTraceFrame.ReplaceAllString(s, ""))
}

// TestDiffPromptCommandsAmbiguousCharter: an ambiguous (undecodable)
// charter.md surfacing inside the runtime prompt commands (`next`, `agent
// action implement`, `agent action review`) follows each command's own
// upstream handling: exit code, stdout and the stderr text (the
// ensure_charter_bundle_fresh freshness / auto-sync warnings, tracebacks).
func TestDiffPromptCommandsAmbiguousCharter(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"before-implement", "before-review"} {
		variant := variant
		t.Run(variant, func(t *testing.T) {
			t.Parallel()
			p := missionfx.NewOraclePair(t)
			missionfx.OracleCalcMission(t, p)
			p.Both(func(s *testkit.Sandbox) {
				slug := missionfx.MissionSlug(t, s)
				run := func(args ...string) {
					if r := s.RunOracle(args...); r.Code != 0 {
						t.Fatalf("oracle %v: %d\n%s%s", args, r.Code, r.Stdout, r.Stderr)
					}
				}
				corrupt := func() {
					path := filepath.Join(s.Dir, ".kittify", "charter", "charter.md")
					b, _ := os.ReadFile(path)
					os.WriteFile(path, append(b, []byte("\n# plain ascii text with one stray byte \xe9 here\n")...), 0o644)
					s.Git("add", "-A")
					s.Git("commit", "-q", "-m", "ambiguous charter")
				}
				run("charter", "interview", "--defaults", "--json")
				run("charter", "generate", "--json")
				run("charter", "synthesize", "--json")
				s.Git("add", "-A")
				s.Git("commit", "-q", "-m", "charter")
				run("agent", "mission", "record-analysis", "--mission", slug, "--input-file", missionfx.AnalysisReport(), "--json")
				if variant == "before-implement" {
					corrupt()
					return
				}
				run("agent", "action", "implement", "WP01", "--agent", "claude", "--mission", slug)
				wt := filepath.Join(".worktrees", slug+"-lane-a")
				s.WriteFile(filepath.Join(wt, "src", "calc.py"), "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n")
				s.Exec(wt, "git", "add", "-A")
				s.Exec(wt, "git", "commit", "-q", "-m", "feat(WP01): subtract")
				run("agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", slug)
				run("agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", slug, "--agent", "claude", "--note", "Ready")
				corrupt()
			})
			cmds := [][]string{
				{"next", "--mission", "$M", "--json"},
				{"next", "--mission", "$M"},
				{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"},
				{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
				{"next", "--agent", "claude", "--mission", "$M", "--result", "success"},
			}
			if variant == "before-implement" {
				cmds = [][]string{
					{"next", "--mission", "$M"},
					{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"},
					{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
					{"next", "--agent", "claude", "--mission", "$M", "--result", "success"},
				}
			}
			for _, args := range cmds {
				g := p.Go.Run(subst(args, missionfx.MissionSlug(t, p.Go))...)
				o := p.Oracle.RunOracle(subst(args, missionfx.MissionSlug(t, p.Oracle))...)
				if os.Getenv("KITTY_TEST_TRACE") != "" {
					t.Logf("%v\n== go %d ==\n%s%s== oracle %d ==\n%s%s", args, g.Code, g.Stdout, g.Stderr, o.Code, o.Stdout, o.Stderr)
				}
				if g.Code != o.Code {
					t.Errorf("%v: exit go=%d oracle=%d", args, g.Code, o.Code)
				}
				if gs, os_ := reHex32.ReplaceAllString(p.Go.Normalize(g.Stdout), "<HEX32>"), reHex32.ReplaceAllString(p.Oracle.Normalize(o.Stdout), "<HEX32>"); gs != os_ {
					t.Errorf("%v: stdout differs\n%s", args, testkit.Diff(os_, gs))
				}
				if ge, oe := excLines(p.Go.Normalize(g.Stderr)), excLines(p.Oracle.Normalize(o.Stderr)); ge != oe {
					t.Errorf("%v: stderr differs\n%s", args, testkit.Diff(oe, ge))
				}
			}
		})
	}
}
