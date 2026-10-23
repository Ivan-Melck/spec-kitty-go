package rich

import (
	"strings"
	"testing"
)

// \b after numbers / booleans (rich ReprHighlighter), expected from rich 15.
func TestReprWordBoundary(t *testing.T) {
	for _, c := range [][2]string{
		{"required_always.0.artifact_key", "required_always.\x1b[1;36m0.\x1b[0martifact_key\n"},
		{"v1.2.x", "v1.\x1b[1;36m2.\x1b[0mx\n"},
		{"x 10. y", "x \x1b[1;36m10\x1b[0m. y\n"},
		{"True.x", "\x1b[3;92mTrue\x1b[0m.x\n"},
		{"a.True", "a.\x1b[3;92mTrue\x1b[0m\n"},
		{"3.14e5x", "\x1b[1;36m3.\x1b[0m14e5x\n"},
		{"42abc", "42abc\n"},
		{"0.5.", "\x1b[1;36m0.5\x1b[0m.\n"},
	} {
		var b strings.Builder
		(&Console{Width: 80, Color: Standard, Highlighter: ReprHighlighter{}}).Print(&b, c[0])
		if b.String() != c[1] {
			t.Errorf("%q: got %q want %q", c[0], b.String(), c[1])
		}
	}
}
