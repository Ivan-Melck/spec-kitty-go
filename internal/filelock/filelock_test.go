package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

func TestAcquireWritesRecordAndReleaseTruncates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spec-kitty-locks")
	path := filepath.Join(dir, "x.lock")
	l, err := Acquire(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := jsonx.DecodeObject(b)
	if err != nil {
		t.Fatalf("record %q: %v", b, err)
	}
	if got := rec.Keys(); len(got) != 5 || got[0] != "host" || got[4] != "version" {
		t.Fatalf("record keys %v", got)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
			t.Errorf("new lock dir mode %o, want 700", st.Mode().Perm())
		}
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Errorf("lock file mode %o, want 600", st.Mode().Perm())
		}
	}
	l.Release()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("release must keep the lock file: %v", err)
	}
	if st.Size() != 0 {
		t.Errorf("release must truncate the record, size %d", st.Size())
	}
}

func TestExistingDirectoryIsNotNarrowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(filepath.Join(dir, "x.lock"), 0)
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o755 {
		t.Errorf("shared dir narrowed to %o", st.Mode().Perm())
	}
}

func TestContendedAcquireTimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	l, err := Acquire(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	start := time.Now()
	_, err = Acquire(path, 250*time.Millisecond)
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("want *TimeoutError, got %v", err)
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Errorf("gave up after %v", time.Since(start))
	}
	if want := "Could not acquire machine lock at " + pyjson.ReprString(path) + " within bounded wait"; err.Error() != want {
		t.Errorf("message %q", err.Error())
	}
}

func TestReentrantAcquireIsCountedPerProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	outer, err := AcquireReentrant(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := AcquireReentrant(path, 0)
	if err != nil {
		t.Fatalf("nested acquire: %v", err)
	}
	if !HeldByProcess(path) {
		t.Fatal("HeldByProcess = false while held")
	}
	inner.Release()
	if _, err := Acquire(path, 0); err == nil {
		t.Fatal("inner release dropped the OS lock")
	}
	outer.Release()
	if HeldByProcess(path) {
		t.Fatal("still registered after the outermost release")
	}
	l, err := Acquire(path, 0)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	l.Release()
}

func TestSymlinkLockPathIsRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "payload")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "x.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	_, err := Acquire(link, 0)
	var se *SymlinkError
	if !errors.As(err, &se) {
		t.Fatalf("want *SymlinkError, got %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatalf("symlink target changed: %q", b)
	}
}

func TestRealPathResolvesExistingPrefix(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	want := filepath.Join(RealPath(real), "missing", "file")
	if got := RealPath(filepath.Join(link, "missing", "file")); got != want {
		t.Fatalf("RealPath = %q, want %q", got, want)
	}
}
