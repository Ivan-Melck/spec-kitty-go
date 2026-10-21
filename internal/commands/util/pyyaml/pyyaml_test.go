package pyyaml

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Expected bytes produced by PyYAML 6 yaml.dump(..., default_flow_style=False,
// allow_unicode=True, sort_keys=False).
const want = `work_package_id: WP01
title: 'Demo: thing'
lane: planned
dependencies: []
subtasks:
- T001
- T002
activity_log: "  - timestamp: \"2026-01-01T00:00:00Z\"\n    lane: \"doing\"\n    agent:\
  \ \"system\"\n    action: \"Auto-repaired lane metadata (was: planned) [shell_pid\
  \ 1]\"\n"
n: 1
b: true
none: null
date: 2026-01-02
empty: ''
'yes': 'yes'
num: '123'
long: 'word word word word word word word word word word word word word word word
  word word word word word word word word word word word word word word word '
uni: héllo ☃
colon: 'a: b'
hash: 'a #b'
nested:
  k:
  - a: 1
    b: x
multi: 'line1

  line2'
lead: ' x'
star: '*x'
quote: it's
`

func TestDumpMatchesPyYAML(t *testing.T) {
	doc := jsonx.NewObject(
		"work_package_id", "WP01", "title", "Demo: thing", "lane", "planned", "dependencies", []any{}, "subtasks", []any{"T001", "T002"},
		"activity_log", "  - timestamp: \"2026-01-01T00:00:00Z\"\n    lane: \"doing\"\n    agent: \"system\"\n    action: \"Auto-repaired lane metadata (was: planned) [shell_pid 1]\"\n",
		"n", int64(1), "b", true, "none", nil, "date", Date{2026, 1, 2}, "empty", "", "yes", "yes", "num", "123",
		"long", strings.Repeat("word ", 30), "uni", "héllo ☃", "colon", "a: b", "hash", "a #b",
		"nested", jsonx.NewObject("k", []any{jsonx.NewObject("a", int64(1), "b", "x")}),
		"multi", "line1\nline2", "lead", " x", "star", "*x", "quote", "it's",
	)
	got := Dump(doc, Options{AllowUnicode: true})
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSafeLoadRoundTrip(t *testing.T) {
	v, err := SafeLoad("a: yes\nb: 0x1F\nc: 2026-01-02\nd: '2026-01-02'\ne: ~\nf: 1_000\ng: 1.5\nh: 2026-01-01T10:00:00Z\ni: on\nlist:\n- x\n")
	if err != nil {
		t.Fatal(err)
	}
	got := Dump(v, Options{AllowUnicode: true})
	exp := "a: true\nb: 31\nc: 2026-01-02\nd: '2026-01-02'\ne: null\nf: 1000\ng: 1.5\nh: 2026-01-01 10:00:00+00:00\ni: true\nlist:\n- x\n"
	if got != exp {
		t.Fatalf("got:\n%s\nwant:\n%s", got, exp)
	}
}
