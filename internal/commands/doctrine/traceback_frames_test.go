package doctrine_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var (
	reOracleFrame = regexp.MustCompile(`\.py:(\d+) in ?([A-Za-z_][A-Za-z0-9_]*)`)
	reGoFrame     = regexp.MustCompile(`in ([A-Za-z_][A-Za-z0-9_]*):(\d+)`)
)

// tracebackFrames extracts "name:line" frames from a rich traceback panel:
// the oracle form ("<path>.py:<line> in <name>", possibly wrapped) or the Go
// form ("in <name>:<line>", source not on disk).
func tracebackFrames(s string) []string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, "│") {
			continue
		}
		l = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(l, "│")), "│"))
		if n := len(lines); n > 0 && strings.HasSuffix(lines[n-1], " in") {
			lines[n-1] += " " + l
			continue
		}
		lines = append(lines, l)
	}
	joined := strings.Join(lines, "\n")
	var out []string
	if m := reOracleFrame.FindAllStringSubmatch(joined, -1); m != nil {
		for _, x := range m {
			out = append(out, x[2]+":"+x[1])
		}
		return out
	}
	for _, x := range reGoFrame.FindAllStringSubmatch(joined, -1) {
		out = append(out, x[1]+":"+x[2])
	}
	return out
}

// compareTracebackFrames checks both sides crashed with the same frames.
func compareTracebackFrames(t *testing.T, name string, g, o testkit.Result) {
	t.Helper()
	gf, of := tracebackFrames(g.Stderr), tracebackFrames(o.Stderr)
	if strings.Join(gf, " ") != strings.Join(of, " ") || len(of) == 0 {
		t.Errorf("%s: traceback frames differ\n oracle: %v\n go:     %v", name, of, gf)
	}
}

func TestTracebackFramesParse(t *testing.T) {
	o := "╭── Traceback ──╮\n│ /x/site-packages/a/b.py:162 in        │\n│ regenerate_graph                      │\n│                                       │\n│ /x/c.py:12 in f │\n╰──╯\n"
	g := "╭── Traceback ──╮\n│ in regenerate_graph:162   │\n│                │\n│ in f:12 │\n╰──╯\n"
	if a, b := tracebackFrames(o), tracebackFrames(g); strings.Join(a, " ") != "regenerate_graph:162 f:12" || strings.Join(b, " ") != strings.Join(a, " ") {
		t.Fatalf("%v %v", a, b)
	}
}
