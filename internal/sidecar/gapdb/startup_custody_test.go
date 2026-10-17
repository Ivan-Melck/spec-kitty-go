//go:build linux

package gapdb

// UNRUN provider-free COMPONENT controls. Injected returned errors are not
// failed physical syscall evidence. TempDir residuals are NOT durable retention.
import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestStartupCustodyPrimaryIdentityTextAndChains(t *testing.T) {
	for _, kind := range []FailureKind{FailureStart, FailureReadiness, FailureIdentityUnprovable, FailureInvalidConfiguration} {
		cause := errors.New("raw primary")
		primary := failure(kind, "start", "original reason", cause)
		custody := startupCleanupError("component close failure", unix.EIO)
		got := withStartupCustody(primary, custody)
		var typed *Error
		var branches *startupCustodyError
		if got.Error() != primary.Error() || !IsFailure(got, kind) || !errors.As(got, &typed) || typed != primary || !errors.Is(got, cause) || !errors.Is(got, unix.EIO) || !errors.As(got, &branches) || branches.primary != primary { t.Fatal("primary changed", got) }
		if withStartupCustody(primary, nil) != primary || withStartupCustody(nil, custody) != custody || withStartupCustody(nil, nil) != nil { t.Fatal("nil identity") }
	}
}

func TestRuntimeIdentityCloseDoesNotMaskSocketBound(t *testing.T) {
	primary := failure(FailureInvalidConfiguration, "start", "configured socket exceeds Unix bound", nil)
	got := runtimeIdentityResult(strings.Repeat("x", maxSocketPathBytes+1), nil, unix.EIO)
	if got.Error() != primary.Error() || !IsFailure(got, FailureInvalidConfiguration) || !errors.Is(got, unix.EIO) { t.Fatal("pure-validation precedence", got) }
	if got := runtimeIdentityResult("/s", nil, unix.EIO); !IsFailure(got, FailureCleanup) || !errors.Is(got, unix.EIO) { t.Fatal("close-only proceeded", got) }
	if got := runtimeIdentityResult("/s", nil, nil); got != nil { t.Fatal(got) }
	identityErr := errors.New("identity read")
	got = runtimeIdentityResult(strings.Repeat("x", maxSocketPathBytes+1), identityErr, unix.EIO)
	if !IsFailure(got, FailureStart) || !errors.Is(got, identityErr) || !errors.Is(got, unix.EIO) { t.Fatal("earlier identity primary", got) }
}

func custodyRuntime(t *testing.T) *Lifecycle {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s")
	if err := os.Mkdir(path, 0700); err != nil { t.Fatal(err) }
	info, err := os.Lstat(path)
	if err != nil { t.Fatal(err) }
	id, err := identityFromInfo(info)
	if err != nil { t.Fatal(err) }
	api := systemProcessAPI
	api.startTicks = func(int) (uint64, error) { return 0, os.ErrNotExist } // COMPONENT exited observation only
	return &Lifecycle{pidfd: -1, processes: api, runtimeID: id,
		provenance: SidecarProvenance{RuntimeDirectory: path, ConfiguredSocket: filepath.Join(path, "gapdb.sock")}}
}

func TestSupervisorReturnedCloseResultNoRetry(t *testing.T) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil { t.Fatal(err) }
	calls := 0
	api := systemProcessAPI
	api.closeFD = func(owned int) error {
		calls++
		if err := unix.Close(owned); err != nil { return err }
		return unix.EIO // actual close succeeded; inject COMPONENT outward error
	}
	l := &Lifecycle{pidfd: fd, processes: api}
	first := l.closePidfd()
	if !errors.Is(first, unix.EIO) || !l.pidfdCloseAttempted || l.pidfd != -1 { t.Fatal(first) }
	if got := l.closePidfd(); got != first || calls != 1 { t.Fatal("FIRST repaired/retried", got, calls) }
	absent := &Lifecycle{pidfd: -1}
	if err := absent.closePidfd(); err != nil || absent.pidfdCloseAttempted { t.Fatal("absence != actual attempt", err) }
}

func TestCleanupEachDeferredCloseAndPrimaryOrdering(t *testing.T) {
	for _, placement := range []string{"directory", "parent", "both-with-primary"} {
		t.Run(placement, func(t *testing.T) {
			l := custodyRuntime(t)
			if placement == "both-with-primary" { if err := os.WriteFile(filepath.Join(l.provenance.RuntimeDirectory, "foreign"), []byte("owned test marker"), 0600); err != nil { t.Fatal(err) } }
			var order []string
			err := l.cleanupRuntimeWithCall(true, func(required bool) error {
				return l.cleanupRuntimeWithCloses(required, func(r *expiryCleanupReport, fd int) error {
					order = append(order, "parent")
					if err := r.closeLocalFD(fd); err != nil { return err }
					if placement != "directory" { return unix.EBADF }
					return nil
				}, func(r *expiryCleanupReport, file *os.File) error {
					order = append(order, "directory")
					if err := r.closeLocalFile(file); err != nil { return err }
					if placement != "parent" { return unix.EIO }
					return nil
				})
			})
			if len(order) != 2 || order[0] != "directory" || order[1] != "parent" { t.Fatal("close order changed", order) }
			if placement != "parent" && !errors.Is(err, unix.EIO) { t.Fatal(err) }
			if placement != "directory" && !errors.Is(err, unix.EBADF) { t.Fatal(err) }
			if placement == "both-with-primary" {
				primary := failure(FailureCleanup, "cleanup", "runtime contains foreign artifacts", nil)
				if err.Error() != primary.Error() { t.Fatal("cleanup primary changed", err) }
			}
			// Even absent directory/later success cannot re-enter actual cleanup.
			first := err
			if got := l.cleanupRuntimeWithCall(true, func(bool) error { t.Error("cleanup retried"); return nil }); got != first { t.Fatal("late nil repaired FIRST", got) }
			if got := l.Stop(t.Context()); got != first { t.Fatal("Stop lost earlier cleanup FIRST", got) }
			if got := l.Stop(t.Context()); got != first { t.Fatal("repeated Stop lost FIRST", got) }
			if got := l.CleanupStale(); got != first { t.Fatal("late CleanupStale repaired FIRST", got) }
		})
	}
}

func TestOrdinaryConcurrentFIRSTStopAndCleanupStaleRetainEntireFailure(t *testing.T) {
	l := custodyRuntime(t)
	marker := filepath.Join(l.provenance.RuntimeDirectory, "foreign")
	if err := os.WriteFile(marker, []byte("test owned"), 0600); err != nil { t.Fatal(err) }
	entered, release := make(chan struct{}), make(chan struct{})
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil { t.Fatal(err) }
	l.pidfd = fd
	var calls atomic.Int32
	l.processes.closeFD = func(fd int) error { calls.Add(1); close(entered); <-release; return unix.Close(fd) }
	results := make(chan error, 8)
	go func() { results <- l.Stop(t.Context()) }()
	<-entered // first Stop remains in its contested FIRST body
	for i := 0; i < 7; i++ { go func(i int) { if i%2 == 0 { results <- l.Stop(t.Context()) } else { results <- l.CleanupStale() } }(i) }
	close(release)
	first := <-results
	if !IsFailure(first, FailureCleanup) { t.Fatal("cleanup failure not outward", first) }
	for i := 0; i < 7; i++ { if got := <-results; got != first { t.Fatal("concurrent loser lost FIRST", got) } }
	if calls.Load() != 1 { t.Fatal("supervisor close retried") }
	if err := os.Remove(marker); err != nil { t.Fatal(err) } // only this test-owned marker
	if got := l.CleanupStale(); got != first { t.Fatal("reconciliation nil-repaired FIRST", got) }
	if _, err := os.Lstat(l.provenance.RuntimeDirectory); err != nil { t.Fatal("later cleanup ran", err) }
}

func TestFirstTeardownPublishesDeferredErrorBeforeCompletion(t *testing.T) {
	l := custodyRuntime(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	body := func() (result error) {
		calls.Add(1)
		defer func() { result = withStartupCustody(result, unix.EIO) }()
		close(entered); <-release
		return nil
	}
	results := make(chan error, 8)
	go func() { err, _ := l.firstTeardown(body); results <- err }()
	<-entered
	for i := 0; i < 7; i++ { go func() { err, _ := l.firstTeardown(func() error { t.Error("second body"); return nil }); results <- err }() }
	close(release)
	for i := 0; i < 8; i++ { if err := <-results; !errors.Is(err, unix.EIO) { t.Fatal("deferred result lost", err) } }
	if calls.Load() != 1 || !errors.Is(l.teardownCustodyErr, unix.EIO) { t.Fatal("FIRST publication") }
}

// Separate provider-free actual test child. This never mints sidecar admission,
// public status, or a Host. Parent owns stdin and the single Cmd.Wait owner.
func TestCustodyWaitChild(t *testing.T) {
	if os.Getenv("SPK_FIRST_CUSTODY_CHILD") != "1" { return }
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil { t.Fatal(err) }
}
func custodyChild(t *testing.T, waitFault error) (*Lifecycle, io.WriteCloser) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestCustodyWaitChild$")
	command.Env = append(os.Environ(), "SPK_FIRST_CUSTODY_CHILD=1")
	input, err := command.StdinPipe()
	if err != nil { t.Fatal(err) }
	if err := command.Start(); err != nil { t.Fatal(err) }
	l := custodyRuntime(t)
	l.command = command
	if waitFault == nil { l.startChildWait(command) } else {
		l.startChildWaitWithCall(command, func(c *exec.Cmd) error {
			if err := c.Wait(); err != nil { return err }
			return waitFault // actual ProcessState, injected outward error only
		})
	}
	t.Cleanup(func() {
		_ = input.Close()
		select { case <-l.childWait.done: case <-time.After(5*time.Second): t.Error("test-owned Wait remains incomplete; no repair/retry") }
	})
	return l, input
}

func TestUnobservedActualChildSingleWaitSuccess(t *testing.T) {
	l, _ := custodyChild(t, nil)
	if err := l.abortStartBefore(t.Context(), time.Now().Add(5*time.Second)); err != nil { t.Fatal(err) }
	f := l.startupAbort.facts
	if !f.signalAttempted || !f.waitObserved || !f.reapProven || !f.runtimeCleanupAttempted || f.runtimeDeletionWithheld { t.Fatal(f) }
	if got := l.abortStart(); got != nil { t.Fatal("repeat successful FIRST changed", got) }
}

func TestConcurrentFIRSTAbortMissingReapLateActualCompletionNeverRepairs(t *testing.T) {
	l, input := custodyChild(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var kills atomic.Int32
	refuseKill := func(*os.Process) error { kills.Add(1); close(entered); <-release; return unix.EPERM }
	results, winner := make(chan error, 7), make(chan error, 1)
	deadline := time.Now().Add(20*time.Millisecond)
	go func() { winner <- l.abortStartBeforeWithKill(t.Context(), deadline, refuseKill) }()
	<-entered
	for i := 0; i < 7; i++ { go func() { results <- l.abortStartBeforeWithKill(t.Context(), deadline, refuseKill) }() }
	close(release)
	first := <-winner
	for i := 0; i < 7; i++ { if err := <-results; err != first && !errors.Is(err, context.DeadlineExceeded) { t.Fatal("loser lost FIRST or explicit bounded incomplete observation", err) } }
	var outcome *startupAbortError
	if !errors.As(first, &outcome) || !errors.Is(first, unix.EPERM) || !errors.Is(first, context.DeadlineExceeded) || !outcome.facts.waitTimedOut || outcome.facts.reapProven || !outcome.facts.runtimeDeletionWithheld || outcome.facts.runtimeCleanupAttempted || kills.Load() != 1 { t.Fatal("bounded refusal failed", first) }
	if _, err := os.Lstat(l.provenance.RuntimeDirectory); err != nil { t.Fatal("unknown reap authorized cleanup", err) }
	if err := input.Close(); err != nil { t.Fatal(err) }
	<-l.childWait.done // actual late Wait state, NEVER used to repair FIRST
	if l.childWait.state == nil { t.Fatal("actual child not reaped") }
	if got := l.abortStart(); got != first { t.Fatal("late actual reap repaired FIRST", got) }
	if got := l.Stop(t.Context()); got != first { t.Fatal("late Stop repaired abort FIRST", got) }
	if got := l.CleanupStale(); got != first { t.Fatal("late reconciliation repaired abort FIRST", got) }
}

func TestActualReapCannotEraseUnexpectedReturnedWaitOrPendingCloseError(t *testing.T) {
	for _, fault := range []string{"wait", "pending-supervisor", "current-supervisor"} {
		t.Run(fault, func(t *testing.T) {
			var waitFault error
			if fault == "wait" { waitFault = unix.EIO }
			l, input := custodyChild(t, waitFault)
			if fault == "pending-supervisor" { l.pendingCustody.record(unix.EBADF) } // COMPONENT record; actual rollback paths tested below
			if fault == "current-supervisor" {
				fd, err := unix.PidfdOpen(os.Getpid(), 0)
				if err != nil { t.Fatal(err) }
				l.pidfd = fd
				l.processes.closeFD = func(fd int) error { if err := unix.Close(fd); err != nil { return err }; return unix.EBADF }
			}
			if err := input.Close(); err != nil { t.Fatal(err) }
			<-l.childWait.done
			err := l.abortStartBefore(t.Context(), time.Now().Add(time.Second))
			var outcome *startupAbortError
			if !errors.As(err, &outcome) || !outcome.facts.reapProven || !outcome.facts.runtimeDeletionWithheld || outcome.facts.runtimeCleanupAttempted { t.Fatal("actual state erased custody failure", err) }
			if fault == "wait" && !errors.Is(err, unix.EIO) { t.Fatal(err) }
			if fault != "wait" && !errors.Is(err, unix.EBADF) { t.Fatal(err) }
		})
	}
}

func TestSyntheticMissingOrClosedExitChannelIsNotActualReap(t *testing.T) {
	for _, placement := range []string{"missing", "closed", "nil-result"} {
		l := custodyRuntime(t)
		l.process.PID = 42 // deliberately inconsistent command-less COMPONENT
		if placement != "missing" { l.exit = make(chan error, 1); if placement == "closed" { close(l.exit) } else { l.exit <- nil } }
		err := l.abortStartBefore(t.Context(), time.Now().Add(time.Second))
		var outcome *startupAbortError
		if !errors.As(err, &outcome) || outcome.facts.reapProven || outcome.facts.waitObserved || !outcome.facts.runtimeDeletionWithheld { t.Fatal("poll/channel-only reap accepted", placement, err) }
	}
}

func TestAttributionRollbackActualAttemptResultsEnterPendingCustody(t *testing.T) {
	for _, stage := range []string{"second-start", "image-metadata"} {
		t.Run(stage, func(t *testing.T) {
			l, input := custodyChild(t, nil)
			pid := l.command.Process.Pid
			start, err := processStartTicks(pid)
			if err != nil { t.Fatal(err) }
			image, digest, err := processExecutable(pid)
			if err != nil { t.Fatal(err) }
			api := systemProcessAPI
			starts, closes := 0, 0
			api.startTicks = func(int) (uint64, error) { starts++; if stage == "second-start" && starts == 2 { return start+1, nil }; return start, nil }
			api.executableIdentity = func(int) (fileIdentity, error) { return fileIdentity{}, unix.EACCES }
			api.closeFD = func(fd int) error { closes++; if err := unix.Close(fd); err != nil { return err }; return unix.EIO }
			l.processes = api
			expected := ExecutedImageProvenance{Device: image.device, Inode: image.inode, Size: image.size, SHA256: digest}
			primary := l.establishStartedProcess(expected)
			if !IsFailure(primary, FailureIdentityUnprovable) || closes != 1 || !l.pendingCustody.supervisorCloseAttempted || !errors.Is(l.pendingCustody.supervisorCloseErr, unix.EIO) { t.Fatal("rollback pending loss", stage, closes, primary) }
			if err := input.Close(); err != nil { t.Fatal(err) }
			<-l.childWait.done
			outward := withStartupCustody(primary, l.abortStartBefore(t.Context(), time.Now().Add(time.Second)))
			if outward.Error() != primary.Error() || !IsFailure(outward, FailureIdentityUnprovable) || !errors.Is(outward, unix.EIO) || !l.startupAbort.facts.pending.supervisorCloseAttempted || l.startupAbort.facts.runtimeCleanupAttempted { t.Fatal("FIRST pending not immutable/outward", outward) }
		})
	}
}

func TestStartupCustodyLiveEOFFixedOwnerSource(t *testing.T) {
	raw, err := os.ReadFile("lifecycle.go")
	if err != nil { t.Fatal(err) }
	source := string(raw)
	for _, required := range []string{"lifecycle.startChildWait(command)", "lifecycle.abortStartBefore(ctx, lifecycle.originalNonEscalatingCleanupCutoff(ctx))", "runtimeIdentityResult(socket, err, runtimeCloseErr)", "closeErr = closeCall(report, connection)", "closeConnection() // joins", "unixConnection.CloseWrite()", "strictRequireUnaryEOF(connection)", "database = \"\""} {
		if !strings.Contains(source, required) { t.Error("live EOF/custody source lost", required) }
	}
	start := strings.Index(source, "runtimeIdentityResult(socket, err, runtimeCloseErr)")
	launch := strings.Index(source, "verified.command(arguments")
	if start < 0 || launch <= start { t.Fatal("command before close/pure validation") }
	owned, err := os.ReadFile("owned.go")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(owned), "func (l *Lifecycle) closeOwned() error") || strings.Contains(string(owned), "_ = retained.Close()") { t.Fatal("outer lifetime close loss") }
}

