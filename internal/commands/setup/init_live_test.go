package setup_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// reLiveFrame matches one frame of init's transient Live step tree: the
// title line through the "Finalize" step line (optionally with its detail),
// or through rich's centred "..." line when the frame is cropped at the
// console height. Frames are glued to the text before/after them (rich
// writes no newline after the live render).
var reLiveFrame = regexp.MustCompile(`(?:\x1b\[[0-9;]*m)*Initialize Specify Project(?:\x1b\[0m)?\n(?:[^\n]*\n)*?` +
	`(?:[^\n]*?└── [^\n]*?Finalize(?:\x1b\[0m)?(?: (?:\x1b\[[0-9;]*m)?\([^\x1b\n]*\)(?:\x1b\[0m)?)?` +
	`|(?:\x1b\[[0-9;]*m)? +\.\.\. +(?:\x1b\[0m)?)`)

// LiveFrame stands for every intermediate live frame after collapsing.
const liveFrame = "<LIVE-FRAME>"

// collapseLive canonicalizes init's Live region (between rich's hide- and
// show-cursor codes) for comparison: the start frame (drawn before the
// first refresh tick) and the final stop frame stay literal; every other
// frame becomes liveFrame, and one directly following another frame is
// dropped. Those are rich's 8-per-second auto-refresh frames, whose number
// and captured tracker state depend on wall-clock timing (print-triggered
// frames show the state of the last refresh). What stays exact: the start
// and final frames, all text printed inside the block and its order, and
// everything before and after the region.
func collapseLive(s string) string {
	a := strings.Index(s, "\x1b[?25l")
	b := strings.Index(s, "\n\x1b[?25h")
	if a < 0 || b < a {
		return s
	}
	region := s[a:b]
	locs := reLiveFrame.FindAllStringIndex(region, -1)
	if len(locs) == 0 {
		return s
	}
	var out strings.Builder
	prev := 0
	for i, l := range locs {
		text := region[prev:l[0]]
		out.WriteString(text)
		switch {
		case i == 0 || i == len(locs)-1:
			out.WriteString(region[l[0]:l[1]])
		case text != "":
			out.WriteString(liveFrame)
		}
		prev = l[1]
	}
	out.WriteString(region[prev:])
	return s[:a] + out.String() + s[b:]
}

// compareInit runs args in both sandboxes and compares exit codes and
// stdout with the live region collapsed.
func compareInit(p *testkit.Pair, args ...string) (testkit.Result, testkit.Result) {
	p.T.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		p.T.Errorf("%s: exit code go=%d oracle=%d\n--- go stderr:\n%s\n--- oracle stderr:\n%s", strings.Join(args, " "), g.Code, o.Code, g.Stderr, o.Stderr)
	}
	gs, os_ := p.Go.Normalize(collapseLive(g.Stdout)), p.Oracle.Normalize(collapseLive(o.Stdout))
	if gs != os_ {
		p.T.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
	return g, o
}

func TestCollapseLive(t *testing.T) {
	f := func(state string) string {
		return "\x1b[36mInitialize Specify Project\x1b[0m\n\x1b[37m├── \x1b[0mA " + state + "\n\x1b[37m└── \x1b[0m\x1b[2;32m○\x1b[0m \x1b[90mFinalize\x1b[0m"
	}
	final := "\x1b[36mInitialize Specify Project\x1b[0m\n\x1b[37m└── \x1b[0m\x1b[32m●\x1b[0m \x1b[37mFinalize\x1b[0m \x1b[90m(project ready)\x1b[0m"
	crop := "\x1b[36mInitialize Specify Project\x1b[0m\n\x1b[37m├── \x1b[0mA\n\x1b[1;31m     ...      \x1b[0m"
	tail := "\n\x1b[?25h\r\x1b[1A\x1b[2K\x1b[1A\x1b[2Kafter\n"
	fast := "head\n\x1b[?25l" + f("x") + "text\n" + f("x") + final + tail
	slow := "head\n\x1b[?25l" + f("x") + f("y") + crop + "text\n" + f("y") + f("z") + final + tail
	want := "head\n\x1b[?25l" + f("x") + "text\n" + liveFrame + final + tail
	if got := collapseLive(fast); got != want {
		t.Errorf("fast:\n%q\nwant\n%q", got, want)
	}
	if got := collapseLive(slow); got != want {
		t.Errorf("slow:\n%q\nwant\n%q", got, want)
	}
	if collapseLive("plain\n") != "plain\n" {
		t.Error("plain output changed")
	}
}

// PYF-14 follow-up (FIX3): with colour forced (FORCE_COLOR, non-TTY
// capture) init draws its step tree through rich Live: hidden cursor, a
// start frame, a frame after every print inside the block, the final
// frame, then the erase codes and the static final tree. Several --ai
// selections (the full set crops intermediate frames at 25 rows with the
// "..." line), LINES cropping, and NO_COLOR (frames without colour).
func TestInitLiveColourMatchesOracle(t *testing.T) {
	testkit.Oracle(t)
	all := "copilot,claude,gemini,cursor,qwen,opencode,codex,windsurf,kilocode,auggie,q,kiro,antigravity,vibe,pi,letta,llxprt"
	cases := []struct {
		name, sel string
		env       []string
	}{
		{"claude", "claude", nil},
		{"codex", "codex", nil},
		{"gemini_cursor", "gemini,cursor", nil},
		{"vibe_pi_letta", "vibe,pi,letta", nil},
		{"all", all, nil},
		{"lines10", "claude,codex", []string{"LINES=10"}},
		{"no-color", "claude", []string{"NO_COLOR=1"}},
		{"256", "copilot", []string{"TERM=xterm-256color"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := testkit.NewPair(t)
			p.Both(func(s *testkit.Sandbox) {
				s.Env = append(append([]string{"NO_COLOR=", "FORCE_COLOR=1", "TERM=xterm", "COLORTERM="}, s.Env...), c.env...)
			})
			g, o := compareInit(p, "init", ".", "--ai", c.sel, "--non-interactive")
			if !strings.Contains(o.Stdout, "\x1b[?25l") || !strings.Contains(g.Stdout, "\x1b[?25l") {
				t.Fatalf("no live region (go=%v oracle=%v)", strings.Contains(g.Stdout, "\x1b[?25l"), strings.Contains(o.Stdout, "\x1b[?25l"))
			}
			if o.Code != 0 {
				t.Fatalf("oracle exit %d: %s", o.Code, o.Stderr)
			}
		})
	}
}
