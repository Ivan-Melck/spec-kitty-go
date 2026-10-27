package eventlog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
}

func TestLockPathOutsideGitSitsBesideTheAnchor(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "mission-events.jsonl")
	got, err := LockPath(log)
	if err != nil {
		t.Fatal(err)
	}
	if want := filelock.RealPath(log) + ".event-log.lock"; got != want {
		t.Fatalf("LockPath = %q, want %q", got, want)
	}
}

func TestLockPathInGitIsADigestUnderTheCommonDir(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	log := filepath.Join(root, "kitty-specs", "001-x", "decisions.events.jsonl")
	if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := LockPath(log)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(filelock.RealPath(log)))
	want := filepath.Join(filelock.RealPath(filepath.Join(root, ".git")), "spec-kitty-locks", "event-logs", hex.EncodeToString(sum[:])[:32]+".lock")
	if got != want {
		t.Fatalf("LockPath = %q, want %q", got, want)
	}
	// A log inside .git is not in a project checkout: the lock sits beside it.
	inside := filepath.Join(root, ".git", "x.jsonl")
	if got, _ := LockPath(inside); got != filelock.RealPath(inside)+NonGitLockSuffix {
		t.Fatalf("LockPath(.git interior) = %q", got)
	}
}

func TestAppendFramesTheUnitAndAppliesTheUmask(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "run.events.jsonl")
	if err := Append(log, []string{`{"a":1}`, `{"b":2}`}, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := Append(log, []string{`{"c":3}`}, Options{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	if string(b) != "{\"a\":1}\n{\"b\":2}\n{\"c\":3}\n" {
		t.Fatalf("log %q", b)
	}
	checkNewLogMode(t, log)
}

func TestTornTailIsRefusedAndNothingIsWritten(t *testing.T) {
	log := filepath.Join(t.TempDir(), "decisions.events.jsonl")
	if err := os.WriteFile(log, []byte("{\"a\":1}\n{\"torn"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Append(log, []string{`{"b":2}`}, Options{})
	if !IsCode(err, CodeTornTail) {
		t.Fatalf("want EVENT_LOG_TORN_TAIL, got %v", err)
	}
	want := log + ": the last line is torn (no trailing newline), so the append was refused. Inspect the file and cut the bytes after its last newline, then retry. Error code: EVENT_LOG_TORN_TAIL."
	if err.Error() != want {
		t.Fatalf("message:\n got %q\nwant %q", err.Error(), want)
	}
	if b, _ := os.ReadFile(log); string(b) != "{\"a\":1}\n{\"torn" {
		t.Fatalf("log changed: %q", b)
	}
}

func TestRefusalsBeforeTheLock(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "x.jsonl")
	if err := Append(log, nil, Options{}); err == nil {
		t.Error("empty unit accepted")
	}
	if err := Append(log, []string{"a\nb"}, Options{}); err == nil {
		t.Error("embedded LF accepted")
	}
	if err := Append(log, []string{"a\rb"}, Options{}); err == nil {
		t.Error("embedded CR accepted")
	}
	missing := filepath.Join(dir, "no", "such", "x.jsonl")
	if err := Append(missing, []string{"{}"}, Options{}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing parent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "no")); err == nil {
		t.Error("a missing parent was created without CreateParents")
	}
	if err := Append(missing, []string{"{}"}, Options{CreateParents: true}); err != nil {
		t.Errorf("CreateParents: %v", err)
	}
	if err := Append(filepath.Join(dir, "absent.jsonl"), []string{"{}"}, Options{NoCreate: true}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("NoCreate: %v", err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("a refused append created the log")
	}
}

func TestNoFollowRefusesASymlinkedLog(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.jsonl")
	os.WriteFile(target, nil, 0o644)
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := Append(link, []string{"{}"}, Options{NoFollow: true}); !IsCode(err, CodeSymlink) {
		t.Fatalf("want EVENT_LOG_SYMLINK, got %v", err)
	}
	if err := Append(link, []string{"{}"}, Options{}); err != nil {
		t.Fatalf("following append: %v", err)
	}
}

func TestPreconditionSeesTheLogAndCanRefuse(t *testing.T) {
	log := filepath.Join(t.TempDir(), "x.jsonl")
	os.WriteFile(log, []byte("{\"closed\":true}\n"), 0o644)
	var seen string
	refuse := errors.New("already closed")
	err := Append(log, []string{"{}"}, Options{Precondition: func(b []byte) error {
		seen = string(b)
		return refuse
	}})
	if !errors.Is(err, refuse) {
		t.Fatalf("precondition error not returned: %v", err)
	}
	if seen != "{\"closed\":true}\n" {
		t.Fatalf("precondition saw %q", seen)
	}
	if b, _ := os.ReadFile(log); string(b) != "{\"closed\":true}\n" {
		t.Fatalf("log changed: %q", b)
	}
}

func TestHeldLockTimesOutWithItsCode(t *testing.T) {
	log := filepath.Join(t.TempDir(), "x.jsonl")
	lockPath, err := LockPath(log)
	if err != nil {
		t.Fatal(err)
	}
	held, err := filelock.Acquire(lockPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	err = Append(log, []string{"{}"}, Options{Timeout: 200 * time.Millisecond})
	if !IsCode(err, CodeLockTimeout) {
		t.Fatalf("want EVENT_LOG_LOCK_TIMEOUT, got %v", err)
	}
	want := fmt.Sprintf("%s: event-log lock %s was not acquired within 0.2s because another writer holds it. Retry, or stop the other writer. Error code: EVENT_LOG_LOCK_TIMEOUT.", log, lockPath)
	if err.Error() != want {
		t.Fatalf("message:\n got %q\nwant %q", err.Error(), want)
	}
}

func TestGitUnavailableRefuses(t *testing.T) {
	log := filepath.Join(t.TempDir(), "x.jsonl")
	t.Setenv("PATH", t.TempDir())
	err := Append(log, []string{"{}"}, Options{})
	if !IsCode(err, CodeLockUnavailable) {
		t.Fatalf("want EVENT_LOG_LOCK_UNAVAILABLE, got %v", err)
	}
	if !strings.HasSuffix(err.Error(), "Make git runnable in this checkout, then retry. Error code: EVENT_LOG_LOCK_UNAVAILABLE.") {
		t.Fatalf("message %q", err.Error())
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("log written without the lock")
	}
}

const (
	childLog = "SK_TEST_EVENTLOG_PATH"
	childID  = "SK_TEST_EVENTLOG_ID"
	childGo  = "SK_TEST_EVENTLOG_GO"
)

// TestHelperEventLogChild is the child of TestConcurrentProcessesKeepEveryLine.
func TestHelperEventLogChild(t *testing.T) {
	log := os.Getenv(childLog)
	if log == "" {
		t.Skip("child process helper")
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, err := os.Stat(os.Getenv(childGo)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no start signal")
		}
	}
	id := os.Getenv(childID)
	// A two-line unit with a long body, so an unlocked buffered write would interleave.
	body := strings.Repeat("x", 8192)
	if err := Append(log, []string{`{"id":"` + id + `-1","pad":"` + body + `"}`, `{"id":"` + id + `-2"}`}, Options{Timeout: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentProcessesKeepEveryLine(t *testing.T) {
	if os.Getenv(childLog) != "" {
		t.Skip("running as a child")
	}
	const writers = 40
	root := t.TempDir()
	gitInit(t, root)
	log := filepath.Join(root, "kitty-specs", "001-x", "mission-events.jsonl")
	os.MkdirAll(filepath.Dir(log), 0o755)
	signal := filepath.Join(root, "go")
	cmds := make([]*exec.Cmd, writers)
	for i := range cmds {
		c := exec.Command(os.Args[0], "-test.run=^TestHelperEventLogChild$", "-test.count=1")
		c.Env = append(os.Environ(), childLog+"="+log, childID+"="+strconv.Itoa(i), childGo+"="+signal)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[i] = c
	}
	time.Sleep(200 * time.Millisecond)
	os.WriteFile(signal, nil, 0o644)
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Errorf("child %d: %v", i, err)
		}
	}
	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 2*writers {
		t.Fatalf("%d lines, want %d", len(lines), 2*writers)
	}
	for i := 0; i < len(lines); i += 2 {
		a, z := lines[i], lines[i+1]
		var id string
		if _, err := fmt.Sscanf(a, `{"id":"%s`, &id); err != nil || !strings.Contains(a, `-1","pad"`) {
			t.Fatalf("line %d is not a unit head: %.60q", i, a)
		}
		id = strings.SplitN(id, "-", 2)[0]
		if z != `{"id":"`+id+`-2"}` {
			t.Fatalf("unit %s split or interleaved: next line %.60q", id, z)
		}
	}
}
