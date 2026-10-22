package tracker

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

func TestDumpTOMLMatchesPythonToml(t *testing.T) {
	o := jsonx.NewObject(
		"tracker", jsonx.NewObject("providers", jsonx.NewObject(
			"beads", jsonx.NewObject("command", "bd", "n", int64(3), "f", 1.5e20, "b", true, "l", []any{"a", int64(1)}, "e", 1e-7),
			"fp", jsonx.NewObject(),
		)),
		"top", int64(1),
		"a b", jsonx.NewObject("c", int64(1)),
		"arr", []any{jsonx.NewObject("x", int64(1), "sub", jsonx.NewObject("y", int64(2))), jsonx.NewObject("x", int64(2))},
	)
	want := "top = 1\n[[arr]]\nx = 1\n\n[arr.sub]\ny = 2\n[[arr]]\nx = 2\n\n[\"a b\"]\nc = 1\n\n[tracker.providers.beads]\ncommand = \"bd\"\nn = 3\nf = 1.5e+20\nb = true\nl = [ \"a\", 1,]\ne = 1e-7\n\n[tracker.providers.fp]\n"
	got, err := DumpTOML(o)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDumpTOMLStrQuirks(t *testing.T) {
	cases := map[string]string{
		"a\x01b":     `"ax01b"`,
		"ab":         `"ab"`,
		"it's":       `"it's"`,
		`q"x`:        `"q\"x"`,
		`back\slash`: `"back\\slash"`,
		`a\x01`:      `"a\x01"`,
		"tab\there":  `"tab\there"`,
		"nl\nx":      `"nl\nx"`,
		"unié":       `"unié"`,
	}
	for in, want := range cases {
		got, err := dumpTOMLStr(in)
		if err != nil || got != want {
			t.Errorf("%q: got %s (%v), want %s", in, got, err, want)
		}
	}
	if _, err := dumpTOMLStr("\x7f"); err == nil {
		t.Error("expected IndexError analogue")
	}
}

func TestParseTOMLRoundTrip(t *testing.T) {
	src := "# c\ntop = 1\n[tracker.providers.beads]\ncommand = \"bd\" # x\nactor = 'me'\nlist = [1, 2,\n 3]\ninl = { a = 1, b.c = \"x\" }\nf = 1_000.5\nh = 0xff\nd = 1979-05-27T07:32:00Z\nml = \"\"\"\nline1\\\n  line2\"\"\"\n\n[[arr]]\nx = 1\n[[arr]]\nx = 2\n"
	o, err := ParseTOML(src)
	if err != nil {
		t.Fatal(err)
	}
	got := jsonx.Dumps(o, jsonx.Options{Indent: -1})
	want := `{"top": 1, "tracker": {"providers": {"beads": {"command": "bd", "actor": "me", "list": [1, 2, 3], "inl": {"a": 1, "b": {"c": "x"}}, "f": 1000.5, "h": 255, "d": "1979-05-27T07:32:00Z", "ml": "line1line2"}}}, "arr": [{"x": 1}, {"x": 2}]}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	for _, bad := range []string{"a = ", "[a]\n[a]\n", "a = 1\na = 2\n", "a = \"x\n", "= 1"} {
		if _, err := ParseTOML(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}
