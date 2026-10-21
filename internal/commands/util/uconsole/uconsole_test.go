package uconsole

import (
	"bytes"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	for in, want := range map[string]string{
		"[red]Error:[/red] x":         "Error: x",
		"a [no-untyped-def]":          "a ",
		"[*] 1 fixable [WP01] [1]":    "[*] 1 fixable [WP01] [1]",
		`\[red] literal`:              "[red] literal",
		"[bold cyan]x[/] y":           "x y",
		"[link=https://x]here[/link]": "here",
	} {
		if got, err := Render(in); err != nil || got != want {
			t.Errorf("Render(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Render("[/red] oops"); err == nil {
		t.Fatal("expected MarkupError")
	}
	if got := Strip("[/red] oops"); got != "[/red] oops" {
		t.Fatalf("Strip kept %q", got)
	}
}

func TestLog(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	var b bytes.Buffer
	LogWarning(&b, "one two three four five six seven eight")
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "WARNING  one two") || !strings.HasPrefix(lines[1], "         ") || len(lines[0]) != 40 {
		t.Fatalf("got %q", lines)
	}
}
