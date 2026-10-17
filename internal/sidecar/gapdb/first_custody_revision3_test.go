//go:build linux

package gapdb

// UNRUN source-authored conjunction controls. Actual test child + actual Wait
// and ProcessState; injected returned errors are COMPONENT, not physical failed
// syscalls, admitted sidecar, Host, durable residual or containment evidence.
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

type revision3Closer struct { calls int; err error; file *os.File }
func (c *revision3Closer) Close() error {
	c.calls++
	return withStartupCustody(c.file.Close(), c.err) // actual owned object close once
}
func TestRevision3RevokedConstructionRetainsBothCauses(t *testing.T) {
	construction := errors.New("actual construction rollback result")
	primary := failure(FailureIdentityUnprovable, "dial_owned", "lifetime revoked during construction", nil)
	for _, failed := range []bool{false, true} {
		owned, openErr := os.Open(os.Args[0])
		if openErr != nil { t.Fatal(openErr) }
		closer := &revision3Closer{err: unix.EIO, file: owned}
		var buildErr error
		if failed { buildErr = construction }
		got := revokedDialOwnedResult(buildErr, closer)
		if got.Error() != primary.Error() || !IsFailure(got, FailureIdentityUnprovable) || !errors.Is(got, unix.EIO) || closer.calls != 1 { t.Fatal(got, closer.calls) }
		if failed && !errors.Is(got, construction) { t.Fatal("construction lost", got) }
	}
	got := revokedDialOwnedResult(construction, nil)
	if !errors.Is(got, construction) || got.Error() != primary.Error() { t.Fatal("nil closer erased construction", got) }
	if got := revokedDialOwnedResult(nil, nil); got.Error() != primary.Error() { t.Fatal(got) }
}

func revision3AttributedChild(t *testing.T, waitFault error) (*Lifecycle, io.WriteCloser, *atomic.Int32) {
	t.Helper()
	l, input := custodyChild(t, waitFault)
	pid := l.command.Process.Pid
	start, err := processStartTicks(pid)
	if err != nil { t.Fatal(err) }
	image, digest, err := processExecutable(pid)
	if err != nil { t.Fatal(err) }
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil { t.Fatal(err) }
	l.process = ProcessIdentity{PID: pid, StartTicks: start, Device: image.device, Inode: image.inode, Size: image.size, SHA256: digest}
	l.pidfd, l.processImage, l.processes = fd, image, systemProcessAPI
	l.config.StopTimeout = time.Second
	calls := new(atomic.Int32)
	l.processes.pidfdSignal = func(int, unix.Signal, *unix.Siginfo, int) error {
		calls.Add(1)
		return input.Close() // COMPONENT termination via owned stdin, NOT signal credit
	}
	return l, input, calls
}
func revision3Shutdown(l *Lifecycle, mode string, ctx context.Context) error {
	switch mode { case "unclean": return l.stopOwnedUnclean(ctx); case "stale": return l.CleanupStale(); default: return l.Stop(ctx) }
}
func TestRevision3ActualChildShutdownUnexpectedWaitAndExitedEntry(t *testing.T) {
	for _, mode := range []string{"stop", "unclean", "stale"} {
		for _, alreadyExited := range []bool{false, true} {
			if mode == "stale" && !alreadyExited { continue }
			t.Run(mode+map[bool]string{false:"-live", true:"-exited"}[alreadyExited], func(t *testing.T) {
				l, input, signals := revision3AttributedChild(t, unix.EIO)
				if alreadyExited { if err := input.Close(); err != nil { t.Fatal(err) }; <-l.childWait.done }
				first := revision3Shutdown(l, mode, t.Context())
				if !errors.Is(first, unix.EIO) || !l.shutdownWait.observed || !l.shutdownWait.reapProven || l.shutdownWait.state == nil || l.shutdownWait.expectedExitError { t.Fatal("actual Wait custody lost", first, l.shutdownWait) }
				if l.pidfd != -1 { t.Fatal("safe owned supervisor close skipped") }
				if _, err := os.Lstat(l.provenance.RuntimeDirectory); err != nil { t.Fatal("unexpected Wait error authorized deletion", err) }
				if alreadyExited && signals.Load() != 0 { t.Fatal("already-exited child signalled") }
				if got := l.Stop(t.Context()); got != first { t.Fatal("FIRST changed", got) }
				p := l.childWait.published()
				if !p.Completed || !p.ProcessStatePresent || p.PID != l.process.PID || !errors.Is(p.WaitErr, unix.EIO) { t.Fatal("value publication", p) }
			})
		}
	}
}
func TestRevision3ExpectedTerminationRecordedActualProcessState(t *testing.T) {
	for _, mode := range []string{"stop", "unclean"} {
		l, _, _ := revision3AttributedChild(t, nil)
		l.processes.pidfdSignal = unix.PidfdSendSignal // actual fixture-owned child only
		if err := revision3Shutdown(l, mode, t.Context()); err != nil { t.Fatal(err) }
		if !l.shutdownWait.observed || !l.shutdownWait.reapProven || !l.shutdownWait.expectedExitError || l.shutdownWait.state == nil { t.Fatal("expected actual termination was not recorded", l.shutdownWait) }
		p := l.childWait.published()
		if !p.ExpectedExitError || p.WaitErr == nil || !p.ProcessStatePresent { t.Fatal("expected status erased", p) }
	}
}
func TestRevision3MissingWaitOwnerCannotUseExitChannel(t *testing.T) {
	for _, mode := range []string{"stop", "unclean", "stale"} {
		l := custodyRuntime(t)
		l.command = &exec.Cmd{} // inconsistent component, never a signalling authority
		l.process.PID = 42
		l.exit = make(chan error, 1)
		l.exit <- nil
		err := revision3Shutdown(l, mode, t.Context())
		if !IsFailure(err, FailureCleanup) || l.shutdownWait.observed || l.shutdownWait.reapProven { t.Fatal("synthetic reap accepted", mode, err) }
		if _, err := os.Lstat(l.provenance.RuntimeDirectory); err != nil { t.Fatal("missing owner deleted runtime", err) }
	}
}
func TestRevision3ActualLateWaitCannotRepairShutdownFIRST(t *testing.T) {
	for _, mode := range []string{"stop", "unclean"} {
		l, input, _ := revision3AttributedChild(t, nil)
		l.processes.pidfdSignal = func(int, unix.Signal, *unix.Siginfo, int) error { return nil } // COMPONENT refusal to complete, no real signal
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		first := revision3Shutdown(l, mode, ctx)
		cancel()
		if first == nil || !errors.Is(first, context.DeadlineExceeded) || !l.shutdownWait.timedOut || l.shutdownWait.reapProven { t.Fatal(first) }
		if err := input.Close(); err != nil { t.Fatal(err) }
		<-l.childWait.done
		if !l.childWait.published().ProcessStatePresent { t.Fatal("actual late Wait absent") }
		if got := l.Stop(t.Context()); got != first { t.Fatal("late completion repaired FIRST", got) }
		if _, err := os.Lstat(l.provenance.RuntimeDirectory); err != nil { t.Fatal(err) }
		// A cutoff can leave the owned supervisor open. Test-only release of the
		// known fixture descriptor is not product cleanup or residual evidence.
		l.mu.Lock(); closeErr := l.closePidfd(); l.mu.Unlock()
		if closeErr != nil { t.Fatal(closeErr) }
	}
}
func TestRevision3SpentReadinessIsNotSpentCleanupAndPublishedCompletionWins(t *testing.T) {
	l, input := custodyChild(t, nil)
	readinessDeadline := time.Now().Add(-time.Second)
	outer, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Second))
	defer cancel()
	cutoff := l.originalCleanupCutoff(outer)
	if !cutoff.After(readinessDeadline) { t.Fatal("spent readiness reused") }
	if err := input.Close(); err != nil { t.Fatal(err) }
	<-l.childWait.done
	if err := l.abortStartBefore(outer, cutoff); err != nil || !l.startupAbort.facts.reapProven || !l.startupAbort.facts.runtimeCleanupAttempted { t.Fatal("readiness expiry erased original cleanup grace", err) }
	spent, done := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer done()
	observed, err := observeActualChildWait(spent, l.childWait)
	if err != nil || !observed.observed || !observed.reapProven { t.Fatal("both-ready publication lost", observed, err) }
	pending := &startupChildWait{done: make(chan struct{})}
	observed, err = observeActualChildWait(spent, pending)
	if !errors.Is(err, context.DeadlineExceeded) || !observed.timedOut { t.Fatal("true cutoff accepted", err) }
}
func TestRevision3OnceLoserDoesNotWaitPastOuterOrRunSecondBody(t *testing.T) {
	l := custodyRuntime(t)
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		err, _ := l.firstCustodyBefore(t.Context(), time.Now().Add(time.Second), false, func(context.Context) error { close(entered); <-release; return unix.EIO })
		finished <- err
	}()
	<-entered
	short, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	err, won := l.firstCustodyBefore(short, time.Now().Add(time.Second), false, func(context.Context) error { t.Error("second body"); return nil })
	if won || !errors.Is(err, context.DeadlineExceeded) || l.teardownCustodyErr != nil { t.Fatal("loser fabricated FIRST", err) }
	close(release)
	first := <-finished
	if !errors.Is(first, unix.EIO) || l.teardownCustodyErr != first { t.Fatal(first) }
	if got := l.Stop(t.Context()); got != first { t.Fatal("published FIRST not reused", got) }
}
func TestRevision3RecordAndExecutableClosedObjectCustody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, []byte("record"), 0600); err != nil { t.Fatal(err) }
	for _, maximum := range []int64{1, 64} {
		f, err := os.Open(path)
		if err != nil { t.Fatal(err) }
		calls := 0
		encoded, digest, err := readExactRecordFile(f, maximum, func(owned *os.File) error { calls++; return withStartupCustody(owned.Close(), unix.EIO) })
		if !errors.Is(err, unix.EIO) || encoded != nil || digest != "" || calls != 1 { t.Fatal("record close dropped", err) }
		if maximum == 1 && err.Error() != "record exceeds bound or cannot be read" { t.Fatal("record primary changed", err) }
	}
	file, err := os.Open(os.Args[0])
	if err != nil { t.Fatal(err) }
	if err := file.Close(); err != nil { t.Fatal(err) }
	id, digest, err := processExecutableFileWithReport(file, nil)
	if err == nil || id != (fileIdentity{}) || digest != "" { t.Fatal("closed proc file identity escaped", err) }
	v := &VerifiedExecutable{source: file}
	first := v.Close()
	if first == nil || v.Close() != first { t.Fatal("verified FIRST close repaired", first) }
}
func TestRevision3CreatedRuntimeNeverBlindDeletesUnknown(t *testing.T) {
	for _, mode := range []string{"unknown", "known-empty", "known-foreign"} {
		path := filepath.Join(t.TempDir(), "s")
		if err := os.Mkdir(path, 0700); err != nil { t.Fatal(err) }
		info, err := os.Lstat(path)
		if err != nil { t.Fatal(err) }
		identity, err := identityFromInfo(info)
		if err != nil { t.Fatal(err) }
		if mode == "known-foreign" { if err := os.WriteFile(filepath.Join(path, "foreign"), []byte("fixture-owned"), 0600); err != nil { t.Fatal(err) } }
		if mode == "unknown" {
			err := preservedCreatedRuntime(path, fileIdentity{})
			var receipt *createdRuntimeResidual
			if !errors.As(err, &receipt) || receipt.path != path || receipt.identity != (fileIdentity{}) { t.Fatal("opaque receipt lost", err) }
		} else {
			err := cleanupCreatedRuntime(t.Context(), LifecycleConfig{StopTimeout: time.Second}, path, identity, nil)
			if mode == "known-empty" { if err != nil { t.Fatal(err) } } else if !IsFailure(err, FailureCleanup) { t.Fatal("foreign child cleanup allowed", err) }
		}
		_, statErr := os.Lstat(path)
		if mode == "known-empty" { if !errors.Is(statErr, os.ErrNotExist) { t.Fatal(statErr) } } else if statErr != nil { t.Fatal("unknown/foreign path deleted", statErr) }
	}
}
func TestRevision3ValuePublicationNeverReadsCmdUnderLifecycleMutex(t *testing.T) {
	l, input := custodyChild(t, nil)
	if p := l.childWait.published(); p.Completed || p.ProcessStatePresent { t.Fatal("unpublished child state", p) }
	if err := input.Close(); err != nil { t.Fatal(err) }
	<-l.childWait.done
	p := l.childWait.published()
	if !p.Completed || !p.ProcessStatePresent || p.PID != l.command.Process.Pid { t.Fatal(p) }
	raw, err := os.ReadFile("startup_custody.go")
	if err != nil { t.Fatal(err) }
	_, body, ok := strings.Cut(string(raw), "func (w *startupChildWait) published()")
	body, _, _ = strings.Cut(body, "func (l *Lifecycle) startChildWait")
	if !ok || strings.Contains(body, "command.ProcessState") || !strings.Contains(body, "case <-w.done:") { t.Fatal("publication contract missing") }
}
