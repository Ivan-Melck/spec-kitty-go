package retainedkernel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRetainedFileOpenRefusalAndSingleAttempt(t *testing.T) {
	for _, kind := range []string{"nil-check", "cancel", "revoke", "closed"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			open := func(string, int, uint32) (int, error) { calls++; return -1, unix.EINTR }
			var check func() error
			switch kind {
			case "cancel":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				check = ctx.Err
			case "revoke":
				l := &Lifetime{pidfd: -1}
				l.Revoke()
				check = func() error { return l.live(context.Background(), false) }
			case "closed":
				l := &Lifetime{pidfd: -1, closed: true}
				check = func() error { return l.live(context.Background(), false) }
			}
			file, err := openRetainedFileWith("/proc/self/stat", check, open)
			if file != nil || err == nil || calls != 0 {
				t.Fatal("opened after pre-refusal", file, err, calls)
			}
		})
	}
	if f, e := openRetainedFileWith("/proc/self/stat", func() error { return nil }, nil); f != nil || e == nil {
		t.Fatal("nil syscall seam accepted")
	}
	for _, code := range []error{unix.EINTR, unix.EACCES, unix.ENOENT} {
		calls := 0
		file, err := openRetainedFileWith("/proc/self/stat", func() error { return nil }, func(path string, flags int, mode uint32) (int, error) {
			calls++
			if path != "/proc/self/stat" || flags != unix.O_RDONLY|unix.O_CLOEXEC || mode != 0 {
				t.Fatal("open arguments drift")
			}
			return -1, code
		})
		if file != nil || !errors.Is(err, code) || calls != 1 {
			t.Fatal("open failed to refuse in one attempt", file, err, calls)
		}
	}
	file, err := openRetainedFileWith("/proc/self/stat", func() error { return nil }, func(string, int, uint32) (int, error) { return -1, nil })
	if file != nil || !errors.Is(err, ErrIdentity) {
		t.Fatal("negative success accepted", file, err)
	}
	// A failed syscall never transfers ownership, even with a sentinel FD.
	held, err := unix.Open("/proc/self/stat", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(held)
	file, err = openRetainedFileWith("/proc/self/stat", func() error { return nil }, func(string, int, uint32) (int, error) { return held, unix.EINTR })
	if file != nil || !errors.Is(err, unix.EINTR) {
		t.Fatal("sentinel error accepted")
	}
	if _, err := unix.FcntlInt(uintptr(held), unix.F_GETFD, 0); err != nil {
		t.Fatal("closed unowned error-associated descriptor", err)
	}
}

func TestRetainedFileOpenPostcheckCleanup(t *testing.T) {
	for _, kind := range []string{"cancel", "revoke"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			l := &Lifetime{pidfd: -1}
			checks, calls, fd := 0, 0, -1
			check := func() error {
				checks++
				if checks == 2 {
					if kind == "cancel" {
						cancel()
					} else {
						l.Revoke()
					}
				}
				return l.live(ctx, false)
			}
			open := func(path string, flags int, mode uint32) (int, error) {
				calls++
				var err error
				fd, err = unix.Open(path, flags, mode)
				return fd, err
			}
			file, err := openRetainedFileWith("/proc/self/stat", check, open)
			want := ErrRevoked
			if kind == "cancel" {
				want = context.Canceled
			}
			if file != nil || !errors.Is(err, want) || fd < 0 || checks != 2 || calls != 1 {
				t.Fatal("postcheck exposed file", file, err, checks, calls, fd)
			}
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
				t.Fatal("owned descriptor remained open", err)
			}
		})
	}
	// Synthetic nonnegative success with low32 bits -1 can never name a valid
	// Linux FD. Exercise failed-close error joining WITHOUT closing/reusing a
	// real descriptor or adding a configurable file-wrapper factory.
	if runtime.GOARCH == "amd64" {
		checks := 0
		file, err := openRetainedFileWith("synthetic-invalid-fd", func() error {
			checks++
			if checks == 2 {
				return context.Canceled
			}
			return nil
		}, func(string, int, uint32) (int, error) { return int(^uint(0) >> 1), nil })
		if file != nil || !errors.Is(err, context.Canceled) || !errors.Is(err, unix.EBADF) {
			t.Fatal("cleanup error lost", file, err)
		}
	}
}

func assertRetainedFileFlags(t *testing.T, file *os.File) {
	t.Helper()
	fd := file.Fd()
	flags, err := unix.FcntlInt(fd, unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("opened descriptor not CLOEXEC", flags, err)
	}
	status, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil || status&unix.O_ACCMODE != unix.O_RDONLY || status&unix.O_NONBLOCK != 0 {
		t.Fatal("proc/exe open flags changed", status, err)
	}
}

func TestRetainedFileOpenActualProcAndExe(t *testing.T) {
	checks := 0
	check := func() error { checks++; return nil }
	file, err := openRetainedFileOnce("/proc/self/stat", check)
	if err != nil {
		t.Fatal(err)
	}
	assertRetainedFileFlags(t, file)
	raw, err := io.ReadAll(io.LimitReader(file, maxProcBytes+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || len(raw) == 0 || len(raw) > maxProcBytes || !bytes.Contains(raw, []byte(") ")) || checks != 2 {
		t.Fatal("proc read differs", err, closeErr, checks)
	}
	if ticks, err := startTicks(os.Getpid(), func() error { return nil }); err != nil || ticks == 0 {
		t.Fatal("actual proc parser regressed", ticks, err)
	}
	file, err = openRetainedFileOnce("/proc/self/exe", func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	assertRetainedFileFlags(t, file)
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Ino == 0 || stat.Size <= 0 {
		t.Fatal("actual exe identity differs", stat, err)
	}
	hash := sha256.New()
	if err := readChunks(file, stat.Size, hash, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close() // fixture control, NOT product open helper
	other := sha256.New()
	if _, err := io.Copy(other, original); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hash.Sum(nil), other.Sum(nil)) {
		t.Fatal("opened exe digest differs from actual test image")
	}
}

func TestRetainedFileOpenFDZeroChild(t *testing.T) {
	if os.Getenv("SPK_RETAINED_OPEN_FD_ZERO") != "1" {
		return
	}
	source, err := unix.Open("/proc/self/stat", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if source != 0 {
			_ = unix.Close(source)
		}
	}() // source0 ownership transfers to the returned File
	file, err := openRetainedFileWith("/proc/self/stat", func() error { return nil }, func(string, int, uint32) (int, error) {
		// Dup3 deterministically transfers a valid FD0 in this isolated child,
		// without a close/open reuse race or mutating the parent's stdin.
		if source == 0 {
			return 0, nil
		}
		if err := unix.Dup3(source, 0, unix.O_CLOEXEC); err != nil {
			return -1, err
		}
		return 0, nil
	})
	if err != nil || file == nil || file.Fd() != 0 {
		t.Fatal("valid FD zero refused", file, err)
	}
	assertRetainedFileFlags(t, file)
	if data, err := io.ReadAll(io.LimitReader(file, maxProcBytes+1)); err != nil || len(data) == 0 {
		t.Fatal("FD0 unreadable", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedFileOpenFDZeroIsolated(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "-test.run=^TestRetainedFileOpenFDZeroChild$", "-test.v")
	command.Env = append(os.Environ(), "SPK_RETAINED_OPEN_FD_ZERO=1")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("FD0 child failed: %v %s", err, out)
	}
}

func TestRetainedFileOpenSourceBoundary(t *testing.T) {
	for _, name := range []string{"lifetime.go", "file_open.go"} {
		raw, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		// Refuse silently reintroducing the stdlib path retry loop. This is a
		// drift tripwire, not proof of all unlisted library/runtime allocations.
		if strings.Contains(string(raw), "os.Open(") {
			t.Fatal("unbounded stdlib path-open retry reintroduced", name)
		}
	}
}
