package missionwrite

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const row1 = `{"event_id": "01AAAAAAAAAAAAAAAAAAAAAAAA", "wp_id": "WP01"}` + "\n"
const row2 = `{"event_id": "01BBBBBBBBBBBBBBBBBBBBBBBB", "wp_id": "WP01"}` + "\n"
const row3 = `{"event_id": "01CCCCCCCCCCCCCCCCCCCCCCCC", "wp_id": "WP01"}` + "\n"

func setup(t *testing.T) (string, *RollbackPoint) {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "status.events.jsonl"), []byte(row1), 0o644)
	os.WriteFile(filepath.Join(dir, "status.json"), []byte("{\"v\": 1}\n"), 0o644)
	return dir, Capture(dir)
}

func appendRows(t *testing.T, dir string, rows ...string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, "status.events.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range rows {
		f.WriteString(r)
	}
	os.WriteFile(filepath.Join(dir, "status.json"), []byte("{\"v\": 2}\n"), 0o644)
}

func read(dir, f string) string {
	b, _ := os.ReadFile(filepath.Join(dir, f))
	return string(b)
}

func TestRestoreCutsOwnRows(t *testing.T) {
	dir, rb := setup(t)
	appendRows(t, dir, row2, row3)
	ids, ok := rb.AppendedEventIDs()
	if !ok || len(ids) != 2 || ids[0] != "01BBBBBBBBBBBBBBBBBBBBBBBB" {
		t.Fatalf("appended ids = %v %v", ids, ok)
	}
	var w bytes.Buffer
	out := rb.Restore(&w, []string{"01CCCCCCCCCCCCCCCCCCCCCCCC", "01BBBBBBBBBBBBBBBBBBBBBBBB"}, true)
	if !out.RolledBack || out.Message() != "" || w.Len() != 0 {
		t.Fatalf("outcome %+v log %q", out, w.String())
	}
	if read(dir, "status.events.jsonl") != row1 || read(dir, "status.json") != "{\"v\": 1}\n" {
		t.Fatal("files not restored")
	}
}

func TestRestoreRefusals(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(dir string)
		want    RollbackRefusal
		ids     []string
		hasExp  bool
		message string
	}{
		{name: "not owned", mutate: func(d string) { appendRows(t, d, row2) }, want: RefusalTailNotOwned, ids: []string{"01CCCCCCCCCCCCCCCCCCCCCCCC"}, hasExp: true},
		{name: "torn", mutate: func(d string) { appendRows(t, d, strings.TrimSuffix(row2, "\n")) }, want: RefusalTailUnparseable},
		{name: "shrank", mutate: func(d string) { os.WriteFile(filepath.Join(d, "status.events.jsonl"), nil, 0o644) }, want: RefusalLogShrank},
		{name: "vanished", mutate: func(d string) { os.Remove(filepath.Join(d, "status.events.jsonl")) }, want: RefusalLogVanished},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, rb := setup(t)
			c.mutate(dir)
			before, beforeStatus := read(dir, "status.events.jsonl"), read(dir, "status.json")
			var w bytes.Buffer
			out := rb.Restore(&w, c.ids, c.hasExp)
			if out.RolledBack || out.Refusal != c.want {
				t.Fatalf("outcome %+v", out)
			}
			if !strings.HasPrefix(out.Message(), "STATUS_ROLLBACK_REFUSED: "+string(c.want)+"; ") || !strings.Contains(out.Message(), "left unchanged. Inspect with: git diff HEAD -- ") {
				t.Fatalf("message %q", out.Message())
			}
			if !strings.HasPrefix(w.String(), "WARNING  STATUS_ROLLBACK_REFUSED") {
				t.Fatalf("log %q", w.String())
			}
			if read(dir, "status.events.jsonl") != before || read(dir, "status.json") != beforeStatus {
				t.Fatal("refusal touched the files")
			}
		})
	}
}

func TestRestoreRefusesCommittedTail(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir, rb := setup(t)
	appendRows(t, dir, row2)
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@e", "commit", "-q", "-m", "c"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	out := rb.Restore(nil, nil, false)
	if out.Refusal != RefusalTailAlreadyCommitted {
		t.Fatalf("outcome %+v", out)
	}
	if read(dir, "status.events.jsonl") != row1+row2 {
		t.Fatal("log changed")
	}
}

func TestRestoreUnlinksLogCreatedAfterCapture(t *testing.T) {
	dir := t.TempDir()
	rb := Capture(dir)
	os.WriteFile(filepath.Join(dir, "status.events.jsonl"), []byte(row1), 0o644)
	os.WriteFile(filepath.Join(dir, "status.json"), []byte("{}\n"), 0o644)
	if out := rb.Restore(nil, nil, false); !out.RolledBack {
		t.Fatalf("outcome %+v", out)
	}
	for _, f := range []string{"status.events.jsonl", "status.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Fatalf("%s survived", f)
		}
	}
}
