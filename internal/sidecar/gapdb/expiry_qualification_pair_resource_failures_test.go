//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const expiryPairResourceHelper = "TestExpiryPairResourceFailureHelper"
const expiryPairResourceLogLimit = 64 << 10

// These are private pair-allocation controls, not admitted executable, Host,
// protocol R/A, or full lifecycle qualification. Only the helper changes limits.
func TestExpiryPairResourceFailures(t *testing.T) {
	for _, role := range []string{"socketpair-emfile", "fileconn-emfile"} {
		t.Run(role, func(t *testing.T) { expiryPairResourceRunHelper(t, role) })
	}
}

func expiryPairResourceRunHelper(t *testing.T, role string) {
	t.Helper()
	image, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(image)
	if err != nil || !filepath.IsAbs(image) || info == nil || !info.Mode().IsRegular() {
		t.Fatalf("own test image must be an absolute regular path: %v", err)
	}
	file, err := os.Open(image)
	if err != nil {
		t.Fatal(err)
	}
	var magic [4]byte
	_, readErr := io.ReadFull(file, magic[:])
	firstImageClose := file.Close()
	if readErr != nil || firstImageClose != nil || string(magic[:]) != "\x7fELF" {
		t.Fatalf("own test ELF: read=%v firstClose=%v magic=%q", readErr, firstImageClose, magic)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	logs, err := os.MkdirTemp(cache, "expiry-pair-resource-"+role+"-")
	if err != nil {
		t.Fatal(err)
	}
	// Preserve ordinary files, including the first failure, outside the repo.
	t.Logf("helper logs: %s", logs)
	var captures [2]*os.File
	var firstSync, firstClose [2]error
	finished := false
	finish := func() {
		if finished {
			return
		}
		finished = true
		for i, f := range captures {
			if f != nil {
				firstSync[i] = f.Sync()
				firstClose[i] = f.Close()
				if firstSync[i] != nil || firstClose[i] != nil {
					t.Errorf("capture %d first fsync=%v first close=%v", i, firstSync[i], firstClose[i])
				}
			}
		}
	}
	defer finish()
	for i, name := range []string{"stdout", "stderr"} {
		captures[i], err = os.OpenFile(filepath.Join(logs, name), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, image, "-test.run=^"+expiryPairResourceHelper+"$", "-test.v", "-test.timeout=15s", "--", role)
	cmd.Env = make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "SPEC_KITTY_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Stdout, cmd.Stderr = captures[0], captures[1]
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// No pipe-copy workers; the actual first Wait joins this exact unit helper.
	firstWait := cmd.Wait()
	finish()
	for _, name := range []string{"stdout", "stderr"} {
		path := filepath.Join(logs, name)
		stat, statErr := os.Stat(path)
		if statErr != nil || stat == nil || stat.Size() > expiryPairResourceLogLimit {
			t.Errorf("unbounded/unavailable capture %s: %v", name, statErr)
			continue
		}
		capture, openErr := os.Open(path)
		if openErr != nil {
			t.Error(openErr)
			continue
		}
		contents, readErr := io.ReadAll(io.LimitReader(capture, expiryPairResourceLogLimit+1))
		firstReadClose := capture.Close()
		if readErr != nil || firstReadClose != nil || len(contents) > expiryPairResourceLogLimit {
			t.Errorf("capture read=%v first close=%v bytes=%d", readErr, firstReadClose, len(contents))
		} else {
			t.Logf("%s:\n%s", name, contents)
			if name == "stdout" && !strings.Contains(string(contents), "PAIR-RESOURCE-OBSERVED "+role) {
				t.Error("helper did not publish its post-cleanup observation")
			}
		}
	}
	if firstWait != nil {
		t.Fatalf("unit helper first Wait: %v (context=%v; logs=%s)", firstWait, ctx.Err(), logs)
	}
}

// No environment selector: default discovery returns normally, never skips.
// The parent's anchored -test.run executes only this test in the own test ELF.
func TestExpiryPairResourceFailureHelper(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return
	}
	if separator != len(os.Args)-2 {
		t.Fatal("unit helper requires exactly one literal argv role after --")
	}
	role := os.Args[separator+1]
	if role != "socketpair-emfile" && role != "fileconn-emfile" {
		t.Fatal("unknown unit helper role")
	}
	// Captures are ordinary files, not unbounded bytes.Buffer/pipe workers.
	// This child-only cap remains in force through the test runner's final output.
	var sizeLimit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &sizeLimit); err != nil {
		t.Fatal(err)
	}
	if sizeLimit.Cur > expiryPairResourceLogLimit {
		sizeLimit.Cur = expiryPairResourceLogLimit
	}
	if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &sizeLimit); err != nil {
		t.Fatal(err)
	}

	expiryPairResourceWarmNetpoll(t)
	seed, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if first := unix.Close(seed); first != nil {
			t.Errorf("seed first close: %v", first)
		}
	}()
	var original unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &original); err != nil {
		t.Fatal(err)
	}
	limited := original
	if limited.Cur > 128 {
		limited.Cur = 128
	}
	if limited.Cur < 32 {
		t.Fatal("OPEN: insufficient original descriptor limit for deterministic control")
	}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &limited); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &original); err != nil {
			t.Errorf("restore original soft NOFILE (unchanged hard): %v", err)
		}
		var restored unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &restored); err != nil || restored != original {
			t.Errorf("restored NOFILE=%+v want=%+v: %v", restored, original, err)
		}
	}()
	var effective unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &effective); err != nil || effective != limited {
		t.Fatalf("effective NOFILE=%+v want=%+v: %v", effective, limited, err)
	}
	var fills []expiryPairResourceFD
	defer func() {
		for i := range fills {
			if first := fills[i].close(); first != nil {
				t.Errorf("fill %d original first close: %v", i, first)
			}
		}
	}()
	for {
		fd, dupErr := unix.FcntlInt(uintptr(seed), unix.F_DUPFD_CLOEXEC, 0)
		if errors.Is(dupErr, unix.EMFILE) {
			break
		}
		if dupErr != nil {
			t.Fatal(dupErr)
		}
		fills = append(fills, expiryPairResourceFD{fd: fd})
		if len(fills) > 128 {
			t.Fatal("OPEN: descriptor fill did not reach EMFILE under bounded limit")
		}
	}
	spares := 0
	reason := "fixed socket pair unavailable"
	if role == "fileconn-emfile" {
		spares, reason = 2, "fixed gate connection cannot be owned"
	}
	if len(fills) < spares {
		t.Fatal("OPEN: insufficient owned fill descriptors")
	}
	for i := 0; i < spares; i++ {
		if first := fills[i].close(); first != nil {
			t.Fatalf("release spare %d first close: %v", i, first)
		}
	}
	// Fresh real report and a deliberately unauthenticated private identity.
	// No source/image acquisition, lifecycle binding, retirement or R capability.
	r := new(expiryCleanupReport)
	v := &VerifiedExecutable{expiry: verifiedExpiryState{report: r,
		identity: ExpiryTechnicalIdentity{InputRecordSHA256: "unit-pair-resource-only-not-authenticated"}}}
	closed := false
	var firstVerifiedClose error
	closeVerified := func() {
		if !closed {
			closed = true
			firstVerifiedClose = v.Close()
		}
	}
	defer func() {
		closeVerified()
		if firstVerifiedClose != nil {
			t.Errorf("verified original first Close: %v", firstVerifiedClose)
		}
	}()
	zero := expiryPairResourceSnapshot{}
	before := expiryPairResourceSample(r)
	if before != zero || r.firstError() != nil || r.cleanupError() != nil {
		t.Fatal("fresh private report not empty")
	}
	pairErr := v.makeExpiryPair(context.Background())
	var failure *Error
	if !errors.As(pairErr, &failure) || failure.Kind != FailureStart || failure.Operation != "expiry_pair" ||
		failure.Reason != reason || !errors.Is(pairErr, unix.EMFILE) {
		t.Fatalf("actual pair failure differs: %#v; %v", failure, pairErr)
	}
	want := zero
	childFD := -1
	if spares == 0 {
		if v.expiry.gate != nil || v.expiry.child != nil {
			t.Fatal("failed socketpair allocated pair ownership")
		}
	} else {
		var fileError *net.OpError
		var syscallError *os.SyscallError
		if !errors.As(pairErr, &fileError) || fileError.Op != "file" ||
			!errors.As(pairErr, &syscallError) || syscallError.Syscall != "fcntl" {
			t.Fatalf("FileConn duplicate failure not reached: %#v", pairErr)
		}
		g := v.expiry.gate
		if g == nil || g.report != r || !g.invalid.Load() || g.ctx.Err() == nil ||
			g.parent != nil || g.watch != nil || g.lifecycle.Load() != nil || v.expiry.child == nil {
			t.Fatal("partial pair did not revoke without parent/watch/lifecycle")
		}
		childFD = int(v.expiry.child.Fd())
		if _, err := unix.FcntlInt(uintptr(childFD), unix.F_GETFD, 0); err != nil {
			t.Fatal("child must remain owned before actual verified Close", err)
		}
		want.counts[expiryTemporary] = [3]uint64{1, 1, 1}
		want.counts[expiryChild] = [3]uint64{1, 0, 0}
		if !IsFailure(r.cleanupError(), FailureCleanup) {
			t.Fatal("unclosed actual child falsely certified complete")
		}
	}
	afterPair := expiryPairResourceSample(r)
	if afterPair != want || r.firstError() != nil {
		t.Fatalf("actual after-pair report=%+v want=%+v", afterPair, want)
	}
	closeVerified() // original actual Close owns child closure and gate rundown
	if childFD >= 0 {
		if _, err := unix.FcntlInt(uintptr(childFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
			t.Fatal("actual verified Close did not close owned child", err)
		}
		want.counts[expiryChild] = [3]uint64{1, 1, 1}
	}
	afterClose := expiryPairResourceSample(r)
	if firstVerifiedClose != nil || afterClose != want || r.firstError() != nil ||
		r.gateTeardownError() != nil || r.cleanupError() != nil || v.expiry.report != r {
		t.Fatalf("actual Close=%v report=%+v want=%+v full=%v", firstVerifiedClose, afterClose, want, r.cleanupError())
	}
	// No test workers exist; neither failure starts a production cancel watch.
	// Deferred fill closes and limit restoration finish before helper PASS/Wait.
	t.Logf("PAIR-RESOURCE-OBSERVED %s spares=%d before=%+v afterPair=%+v afterClose=%+v", role, spares, before, afterPair, afterClose)
}

// Cache the ORIGINAL result even when explicit spare release precedes cleanup.
// Never retry an fd integer that might since have been reused by socketpair.
type expiryPairResourceFD struct {
	fd     int
	closed bool
	first  error
}

func (f *expiryPairResourceFD) close() error {
	if !f.closed {
		f.closed = true
		f.first = unix.Close(f.fd)
	}
	return f.first
}

func expiryPairResourceWarmNetpoll(t *testing.T) {
	t.Helper()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	left := os.NewFile(uintptr(pair[0]), "unit-netpoll-left")
	right := os.NewFile(uintptr(pair[1]), "unit-netpoll-right")
	defer func() {
		firstLeft, firstRight := left.Close(), right.Close()
		if firstLeft != nil || firstRight != nil {
			t.Errorf("warmup original file closes: left=%v right=%v", firstLeft, firstRight)
		}
	}()
	conn, err := net.FileConn(left) // initializes Unix netpoll BEFORE fd pressure
	if err != nil {
		t.Fatal(err)
	}
	deadlineErr := conn.SetDeadline(time.Now().Add(time.Second))
	firstConnClose := conn.Close()
	if deadlineErr != nil || firstConnClose != nil {
		t.Fatalf("warmup deadline=%v original connection close=%v", deadlineErr, firstConnClose)
	}
}

type expiryPairResourceSnapshot struct {
	counts                                       [expiryCleanupCategories][3]uint64
	first                                        *expiryCleanupFailure
	operation, startup, teardown, bound, retired bool
	pending                                      uint64
}

func expiryPairResourceSample(r *expiryCleanupReport) expiryPairResourceSnapshot {
	s := expiryPairResourceSnapshot{first: r.first.Load(), operation: r.operationPending.Load(),
		startup: r.startupPending.Load(), teardown: r.teardownPending.Load(),
		bound: r.lifecycleBound.Load(), retired: r.lifecycleRetired.Load(), pending: r.lifecyclePending.Load()}
	for i := range r.counts {
		c := &r.counts[i]
		s.counts[i] = [3]uint64{c.required.Load(), c.recorded.Load(), c.completed.Load()}
	}
	return s
}
