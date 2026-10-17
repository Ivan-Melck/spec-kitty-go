//go:build linux

package retainedkernel

// UNRUN. Injection below means returned-error COMPONENT propagation, NEVER a
// failed physical kernel close. Actual owned descriptors are closed once only.
import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type retainedTypedFault struct { text string }
func (e *retainedTypedFault) Error() string { return e.text }

func TestRetainedCustodyPrimaryTextIdentityAndIsAs(t *testing.T) {
	primary := &retainedTypedFault{"primary exact"}
	closeFault := &retainedTypedFault{"raw close diagnostic"}
	for _, p := range []error{primary, context.Canceled, ErrIdentity} {
		if got := retainedCloseResult(p, "proc-exe", nil); got != p { t.Fatal("nil custody changed original object") }
		got := retainedCloseResult(p, "proc-exe", closeFault)
		var wrapper *retainedCustodyError
		var site *retainedCloseError
		if got.Error() != p.Error() || !errors.Is(got, p) || !errors.Is(got, closeFault) || !errors.As(got, &wrapper) || wrapper.primary != p || !errors.As(got, &site) || site.site != "proc-exe" { t.Fatal("dual error lost text/object/chain", got) }
	}
	got := retainedCloseResult(primary, "pidfd", closeFault)
	var first *retainedTypedFault
	if !errors.As(got, &first) || first != primary { t.Fatal("As not primary first") }
	sole := retainedCloseResult(nil, "pidfd", unix.EIO)
	if sole.Error() != unix.EIO.Error() || !errors.Is(sole, unix.EIO) { t.Fatal("sole raw syscall diagnostic changed", sole) }
	if retainedCloseResult(nil, "pidfd", nil) != nil || joinRetainedCustody(nil, primary) != primary { t.Fatal("nil controls") }
}

func TestRetainedConstructorRollbackReturnedCloseError(t *testing.T) {
	anchor, _, _ := childFixture(t, false) // real child, never provider admission
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	closes := 0
	lifetime, err := newLifetimeWithClose(ctx, anchor, func(pid, flags int) (int, error) {
		fd, err := unix.PidfdOpen(pid, flags)
		if err == nil { cancel() }
		return fd, err
	}, func(fd int) error {
		closes++
		if err := unix.Close(fd); err != nil { t.Error("actual first close", err); return err }
		return unix.EIO // COMPONENT return after actual successful close
	})
	if lifetime != nil || closes != 1 || err.Error() != context.Canceled.Error() || !errors.Is(err, context.Canceled) || !errors.Is(err, unix.EIO) { t.Fatal("rollback primary/custody", lifetime, closes, err) }
}

func TestRetainedConcurrentFIRSTCloseAndRevokeBeforeLock(t *testing.T) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil { t.Fatal(err) }
	l := &Lifetime{pidfd: fd}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	closeCall := func(owned int) error {
		if calls.Add(1) != 1 { t.Error("retried close") }
		close(entered)
		<-release
		if err := unix.Close(owned); err != nil { return err }
		return unix.EIO // returned component error, not kernel fault
	}
	results := make(chan error, 8)
	go func() { results <- l.closeWith(closeCall) }()
	<-entered
	var wg sync.WaitGroup
	for i := 0; i < 7; i++ { wg.Add(1); go func() { defer wg.Done(); results <- l.closeWith(closeCall) }() }
	if !l.revoked.Load() { t.Fatal("revoke not before blocked close") }
	close(release)
	wg.Wait()
	first := <-results
	for i := 0; i < 7; i++ { if got := <-results; got != first { t.Fatal("FIRST object replaced") } }
	if first.Error() != unix.EIO.Error() || !errors.Is(first, unix.EIO) || calls.Load() != 1 || !l.closeAttempted || l.pidfd != -1 { t.Fatal("FIRST not retained", first) }
	if got := l.Close(); got != first { t.Fatal("production repeat nil-repaired component FIRST", got) }
	absent := &Lifetime{pidfd: -1}
	if err := absent.Close(); err != nil || absent.closeAttempted || !absent.closed { t.Fatal("absence manufactured attempt", err) }

	// Check holds the same mu; Close must revoke before waiting for it.
	checking := &Lifetime{pidfd: -1}
	checking.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- checking.Close() }()
	deadline := time.Now().Add(time.Second)
	for !checking.revoked.Load() && time.Now().Before(deadline) { time.Sleep(time.Millisecond) }
	if !checking.revoked.Load() { checking.mu.Unlock(); t.Fatal("revoke waited for check mutex") }
	if err := checking.live(t.Context(), false); !errors.Is(err, ErrRevoked) { checking.mu.Unlock(); t.Fatal(err) }
	checking.mu.Unlock()
	if err := <-done; err != nil { t.Fatal(err) }
}

func TestRetainedFilePostcheckReturnedErrorWithoutFDReuse(t *testing.T) {
	checks, closes := 0, 0
	file, err := openRetainedFileWithCalls("/proc/self/stat", func() error {
		checks++
		if checks == 2 { return context.Canceled }
		return nil
	}, unix.Open, func(file *os.File) error {
		closes++
		if err := file.Close(); err != nil { return err }
		return unix.EIO
	}, unix.Close)
	if file != nil || closes != 1 || err.Error() != context.Canceled.Error() || !errors.Is(err, unix.EIO) { t.Fatal(file, closes, err) }
}

func TestRetainedProcStatCloseErrorZerosSuccessfulParse(t *testing.T) {
	closes := 0
	ticks, err := startTicksWithCalls(os.Getpid(), func() error { return nil }, openRetainedFileOnce, func(file *os.File) error {
		closes++
		if err := file.Close(); err != nil { return err }
		return unix.EIO
	})
	var site *retainedCloseError
	if ticks != 0 || closes != 1 || !errors.Is(err, unix.EIO) || !errors.As(err, &site) || site.site != "proc-stat" { t.Fatal(ticks, closes, err) }
}

func TestRetainedExeClosedObjectAndCloseErrorBothSurvive(t *testing.T) {
	anchor, _, _ := childFixture(t, false)
	l := &Lifetime{anchor: anchor, pidfd: -1}
	err := l.checkLockedWithOpen(t.Context(), false, func(path string, check func() error) (*os.File, error) {
		file, err := openRetainedFileOnce(path, check)
		if err != nil { return nil, err }
		if err := file.Close(); err != nil { t.Fatal(err) }
		return file, nil // safe CLOSED OBJECT, no retained raw fd to re-close
	})
	var primary *os.PathError
	var closeFailure *retainedCloseError
	if !errors.Is(err, os.ErrClosed) || !errors.As(err, &primary) || primary.Op != "stat" || err.Error() != primary.Error() || !errors.As(err, &closeFailure) || closeFailure.site != "proc-exe" { t.Fatal(err) }
}

func TestRetainedSocketDirectoryReturnedCloseError(t *testing.T) {
	anchor, _, _ := childFixture(t, false)
	for _, primary := range []error{nil, context.Canceled} {
		checks, closes := 0, 0
		err := configuredSocketIdentityWithCalls(anchor, func() error {
			checks++
			if checks == 4 { return primary }
			return nil
		}, unix.Openat2, func(fd int) error {
			closes++
			if err := unix.Close(fd); err != nil { return err }
			return unix.EIO
		})
		if checks != 4 || closes != 1 || !errors.Is(err, unix.EIO) { t.Fatal(checks, closes, err) }
		if primary != nil && (err.Error() != primary.Error() || !errors.Is(err, primary)) { t.Fatal("dual primary changed", err) }
	}
}

func TestRetainedCustodyFixedCallsSource(t *testing.T) {
	raw, err := os.ReadFile("lifetime.go")
	if err != nil { t.Fatal(err) }
	for _, required := range []string{"return l.closeWith(unix.Close)", "return newLifetimeWithClose(ctx, anchor, open, unix.Close)", "openFile, (*os.File).Close)", "openDirectory, unix.Close)", "l.pidfd = -1", "return l.closeErr"} {
		if !strings.Contains(string(raw), required) { t.Error("fixed call/FIRST source absent", required) }
	}
	for _, forbidden := range []string{"defer file.Close()", "defer unix.Close(fd)", "_ = l.Close()"} {
		if strings.Contains(string(raw), forbidden) { t.Error("close loss restored", forbidden) }
	}
}
