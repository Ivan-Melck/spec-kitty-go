package drivers

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// PYF-14: with FORCE_COLOR (and no NO_COLOR) console.print emits rich's
// ANSI styling: markup tags plus ReprHighlighter, downgraded to the colour
// system TERM / COLORTERM select.
func TestMergeStubColorParity(t *testing.T) {
	p := testkit.NewPair(t)
	envs := [][]string{
		{"NO_COLOR=", "FORCE_COLOR=1", "TERM=xterm"},
		{"NO_COLOR=", "FORCE_COLOR=1", "TERM=xterm-256color"},
		{"NO_COLOR=", "FORCE_COLOR=1", "TERM=xterm", "COLORTERM=truecolor"},
		{"NO_COLOR=1", "FORCE_COLOR=1", "TERM=xterm"},
		{"NO_COLOR=", "FORCE_COLOR=1", "TERM=dumb"},
		{"NO_COLOR=", "TTY_COMPATIBLE=1", "TERM=xterm", "COLUMNS=40"},
	}
	for _, env := range envs {
		p.Both(func(s *testkit.Sandbox) { s.Env = append(s.Env, env...) })
		p.Compare(true, "merge")
	}
}
