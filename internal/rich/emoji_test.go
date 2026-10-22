package rich

import (
	"strings"
	"testing"
)

// Expected values from rich 15 (Console(width=80).print(case)).
func TestEmojiReplace(t *testing.T) {
	for _, c := range [][2]string{
		{"hello :smile: world", "hello 😄 world\n"},
		{":thumbs_up-text: ok", "👍︎ ok\n"},
		{"a:b:smile:", "a🅱smile:\n"},
		{"time 10:30:45", "time 10:30:45\n"},
		{"[bold]:warning:[/bold] x", "⚠ x\n"},
		{":SMILE:", "😄\n"},
		{"no:colon", "no:colon\n"},
	} {
		var b strings.Builder
		(&Console{Width: 80, Highlighter: ReprHighlighter{}}).Print(&b, c[0])
		if b.String() != c[1] {
			t.Errorf("%q: got %q want %q", c[0], b.String(), c[1])
		}
	}
}
