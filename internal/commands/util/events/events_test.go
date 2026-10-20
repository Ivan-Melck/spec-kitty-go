package events

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

const meta = `{"mission_id": "01KZ0000000000000000000000", "mission_slug": "001-demo"}` + "\n"

const log = `{"event_id": "01KZ0000000000000000000001", "wp_id": "WP01", "note": "héllo"}
{"b":1,"a":2.50,"tail_offset":"x"}
{"partial": tr`

func TestPollAndResume(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "status.events.jsonl")
	os.WriteFile(p, []byte(log), 0o644)
	r, err := PollOnce(p, Cursor{0, EmptyDigest})
	if err != nil || len(r.Events) != 2 {
		t.Fatalf("poll: %v %v", r, err)
	}
	if got := jsonx.Dumps(r.Events[1], jsonx.Options{Indent: -1, EnsureASCII: true}); !strings.HasPrefix(got, `{"b": 1, "a": 2.5, "tail_offset": `) {
		t.Fatalf("got %s", got)
	}
	// Resume at the returned cursor: nothing new.
	c, err := ValidateResume(p, r.Cursor.Offset, &r.Cursor.Invariant)
	if err != nil || c != r.Cursor {
		t.Fatalf("resume: %v %v", c, err)
	}
	if _, err := ValidateResume(p, 3, nil); err == nil || err.Error() != "resume refused: misaligned" {
		t.Fatalf("misaligned: %v", err)
	}
	// Truncate: the next poll signals log_truncated.
	os.WriteFile(p, []byte("{}\n"), 0o644)
	r2, _ := PollOnce(p, r.Cursor)
	if len(r2.Events) != 1 || r2.Events[0].GetString("reason") != "size_shrink" {
		t.Fatalf("truncation: %+v", r2)
	}
	// Bounded stream with an injected sleep: the log grows between polls.
	os.WriteFile(p, []byte(log), 0o644)
	n, sleeps := 0, 0
	err = Tail(p, Cursor{0, EmptyDigest}, 3, func(time.Duration) {
		sleeps++
		f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString("ue}\n")
		f.Close()
	}, func(*jsonx.Object) { n++ })
	if err != nil || n != 3 || sleeps != 1 {
		t.Fatalf("tail: n=%d sleeps=%d err=%v", n, sleeps, err)
	}
}

func fixture(s *testkit.Sandbox) {
	s.WriteFile("kitty-specs/001-demo/meta.json", meta)
	s.WriteFile("kitty-specs/001-demo/status.events.jsonl", log)
	s.WriteFile("kitty-specs/002-bad/meta.json", `{"mission_id": "01KZ1111111111111111111111"}`)
	s.WriteFile("kitty-specs/002-bad/status.events.jsonl", "{\"a\":1}\n[1]\n")
	s.WriteFile("kitty-specs/003-blank/meta.json", `{"mission_id": "01KZ2222222222222222222222"}`)
	s.WriteFile("kitty-specs/003-blank/status.events.jsonl", "{\"a\":1}\n\n")
}

func TestTailParity(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(fixture)
	for _, args := range [][]string{
		{"events", "tail", "--mission", "001-demo", "--once"},
		{"events", "tail", "--mission", "demo", "--max-events", "2"},
		{"events", "tail", "--mission", "01KZ0000", "--max-events", "0"},
		{"events", "tail", "--mission", "nope", "--once"},
		{"events", "tail", "--mission", "001-demo", "--from-invariant", "abc"},
		{"events", "tail", "--mission", "001-demo", "--from-offset", "5", "--once"},
		{"events", "tail", "--mission", "001-demo", "--from-offset", "-1", "--once"},
		{"events", "tail", "--mission", "001-demo", "--from-offset", "99999", "--once"},
		{"events", "tail", "--mission", "001-demo", "--from-offset", "0", "--from-invariant", "00", "--once"},
		{"events", "tail", "--mission", "001-demo", "--from-offset", "0", "--once"},
		{"events", "tail", "--mission", "002-bad", "--once"},
		{"events", "tail", "--mission", "003-blank", "--once"},
		{"events", "tail", "--once"},
	} {
		p.Compare(true, args...)
	}
}

func TestTailParityResume(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(fixture)
	g, _ := p.Compare(false, "events", "tail", "--mission", "001-demo", "--once")
	lines := strings.Split(strings.TrimSpace(g.Stdout), "\n")
	first, _ := jsonx.DecodeObject([]byte(lines[0]))
	off, _ := first.GetInt("tail_offset")
	inv := first.GetString("tail_invariant")
	p.Compare(true, "events", "tail", "--mission", "001-demo", "--once", "--from-offset", itoa(off), "--from-invariant", inv)
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestTailE2E(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	fixture(s)
	r := s.Run("events", "tail", "--mission", "001-demo", "--once")
	if r.Code != 0 || strings.Count(r.Stdout, "\n") != 2 || !strings.Contains(r.Stdout, "\"note\": \"h\\u00e9llo\"") {
		t.Fatalf("got %d %q %q", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("events", "tail", "--mission", "002-bad", "--once")
	if r.Code != 1 || !strings.Contains(r.Stderr, "StoreError: Invalid event structure in tail stream: expected JSON object") || r.Stdout != "" {
		t.Fatalf("got %d %q %q", r.Code, r.Stdout, r.Stderr)
	}
}
