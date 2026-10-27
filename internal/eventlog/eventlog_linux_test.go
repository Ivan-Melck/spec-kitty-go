//go:build linux

package eventlog

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const partialChild = "SK_TEST_EVENTLOG_PARTIAL"

// TestHelperPartialWriteChild appends a unit larger than its RLIMIT_FSIZE
// allows, so the write lands partially and then fails with EFBIG.
func TestHelperPartialWriteChild(t *testing.T) {
	log := os.Getenv(partialChild)
	if log == "" {
		t.Skip("child process helper")
	}
	signal.Ignore(syscall.SIGXFSZ)
	lim := syscall.Rlimit{Cur: 4096, Max: 4096}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &lim); err != nil {
		t.Skip("setrlimit:", err)
	}
	err := Append(log, []string{`{"pad":"` + strings.Repeat("x", 16384) + `"}`}, Options{})
	if err == nil {
		t.Fatal("an over-limit append succeeded")
	}
	if !strings.Contains(err.Error(), "file too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPartialWriteIsCutBack: a write that lands partially is cut back to the
// log's size before the write, so it never leaves a torn tail.
func TestPartialWriteIsCutBack(t *testing.T) {
	if os.Getenv(partialChild) != "" {
		t.Skip("running as a child")
	}
	log := filepath.Join(t.TempDir(), "run.events.jsonl")
	before := "{\"a\":1}\n"
	if err := os.WriteFile(log, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(os.Args[0], "-test.run=^TestHelperPartialWriteChild$", "-test.count=1", "-test.v")
	c.Env = append(os.Environ(), partialChild+"="+log)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "SKIP: TestHelperPartialWriteChild") {
		t.Skipf("child skipped:\n%s", out)
	}
	if b, _ := os.ReadFile(log); string(b) != before {
		t.Fatalf("log not cut back: %d bytes", len(b))
	}
	if err := Append(log, []string{`{"b":2}`}, Options{}); err != nil {
		t.Fatalf("a later append is refused: %v", err)
	}
}
