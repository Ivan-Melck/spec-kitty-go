//go:build linux

package gapdb

// AUTHORED UNRUN actual test-child/value components, not admission, provider,
// genuine Host, physical kernel-fault, durable residual or containment proof.
import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRevision4ExpiredAbortCollectsPublishedWaitAndActualRollback(t *testing.T) {
	l, input := custodyChild(t, unix.EIO)
	pid := l.command.Process.Pid
	start, err := processStartTicks(pid)
	if err != nil { t.Fatal(err) }
	image, digest, err := processExecutable(pid)
	if err != nil { t.Fatal(err) }
	api := systemProcessAPI
	starts, closes := 0, 0
	api.startTicks = func(int) (uint64, error) { starts++; if starts == 2 { return start+1, nil }; return start, nil }
	api.closeFD = func(fd int) error { closes++; return withStartupCustody(unix.Close(fd), unix.ENOSPC) }
	l.processes = api
	primary := l.establishStartedProcess(ExecutedImageProvenance{Device:image.device, Inode:image.inode, Size:image.size, SHA256:digest})
	if !IsFailure(primary, FailureIdentityUnprovable) || closes != 1 || !errors.Is(primary, unix.ENOSPC) { t.Fatal("actual rollback not reached", primary) }
	if err := input.Close(); err != nil { t.Fatal(err) }
	<-l.childWait.done
	published := l.childWait.published()
	if !published.Completed || !published.ProcessStatePresent || !errors.Is(published.WaitErr, unix.EIO) { t.Fatal("actual child Wait not published", published) }
	outer, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	// Lock refusal MUST use only the immutable startupPublished snapshot. Even
	// a different later lock-protected value is not safe custody on this path.
	l.mu.Lock()
	l.pendingCustody.record(unix.EBUSY) // synthetic later mutable-only negative
	kills := 0
	first := l.abortStartBeforeWithKill(outer, l.originalNonEscalatingCleanupCutoff(outer), func(*os.Process) error { kills++; return unix.EPERM })
	l.mu.Unlock()
	var receipt *startupAbortError
	if !errors.As(first, &receipt) || !errors.Is(first, context.DeadlineExceeded) || !errors.Is(first, unix.EIO) || !errors.Is(first, unix.ENOSPC) || errors.Is(first, unix.EBUSY) { t.Fatal("early FIRST lost actual published causes or read mutable fields", first) }
	f := receipt.facts
	if kills != 0 || f.signalAttempted || !f.waitObserved || !f.reapProven || f.waitTimedOut || !f.pending.supervisorCloseAttempted || !f.waitPublication.ProcessStatePresent || f.waitPublication.PID != pid || f.waitState != nil || f.runtimeCleanupAttempted || !f.runtimeDeletionWithheld || !f.residualPossible { t.Fatal("early FIRST facts/authority differ", f) }
	if _, err := os.Lstat(l.provenance.RuntimeDirectory); err != nil { t.Fatal("expired cleanup deleted runtime", err) }
	if again := l.abortStart(); again != first || l.startupAbort.facts.waitErr != f.waitErr { t.Fatal("published FIRST changed", again) }
}

func TestRevision4ExpiredAbortAbsentAndCompletedNilStateRefuse(t *testing.T) {
	for _, mode := range []string{"absent-publication", "absent-owner", "unfinished", "completed-nil-state"} {
		t.Run(mode, func(t *testing.T) {
			l := custodyRuntime(t)
			var wait *startupChildWait
			if mode == "unfinished" || mode == "completed-nil-state" {
				// Deliberately invalid value-only COMPONENT; not a second Wait,
				// actual reap or successful no-child certificate.
				wait = &startupChildWait{done:make(chan struct{})}
				if mode == "completed-nil-state" { close(wait.done) }
			}
			if mode != "absent-publication" { l.startupPublished.Store(&startupCustodyPublication{wait:wait}) }
			outer, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			defer cancel()
			first := l.abortStartBeforeWithKill(outer, time.Now().Add(time.Second), func(*os.Process) error { t.Error("signal on unproven state"); return nil })
			var receipt *startupAbortError
			if !errors.As(first, &receipt) || !errors.Is(first, context.DeadlineExceeded) || receipt.facts.reapProven || receipt.facts.signalAttempted || receipt.facts.runtimeCleanupAttempted || !receipt.facts.runtimeDeletionWithheld { t.Fatal("absence/nil state granted authority", first) }
			if receipt.facts.waitObserved != (mode == "completed-nil-state") { t.Fatal("completion confused with state/reap", receipt.facts) }
			if mode == "completed-nil-state" {
				observed, err := observeActualChildWait(outer, wait)
				if !IsFailure(first, FailureCleanup) || !observed.observed || observed.reapProven || !IsFailure(err, FailureCleanup) { t.Fatal("completed nil-state normal/early refusal absent", first, err) }
			}
			if again := l.Stop(t.Context()); again != first { t.Fatal("later Stop repaired FIRST", again) }
		})
	}
}

func TestRevision4EarlyRefusalLateActualWaitCannotRepairFIRST(t *testing.T) {
	l, input := custodyChild(t, unix.EIO)
	outer, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	first := l.abortStartBefore(outer, time.Now().Add(time.Second))
	var receipt *startupAbortError
	if !errors.As(first, &receipt) || !errors.Is(first, context.DeadlineExceeded) || receipt.facts.waitObserved || receipt.facts.reapProven || receipt.facts.signalAttempted { t.Fatal("early refusal invented completion", first) }
	frozen := receipt.facts
	if err := input.Close(); err != nil { t.Fatal(err) }
	<-l.childWait.done
	if p := l.childWait.published(); !p.Completed || !p.ProcessStatePresent || !errors.Is(p.WaitErr, unix.EIO) { t.Fatal("late actual publication absent", p) }
	if again := l.abortStart(); again != first || errors.Is(first, unix.EIO) || receipt.facts.waitObserved != frozen.waitObserved || receipt.facts.waitErr != frozen.waitErr || l.startupAbort.facts.reapProven { t.Fatal("late actual Wait changed frozen early FIRST", again) }
}

func TestRevision4WaitExitChild(t *testing.T) {
	if os.Getenv("SPK_REVISION4_EXIT_CHILD") == "1" { os.Exit(7) }
}
func TestRevision4JoinedExitErrorAndUnexpectedActualWaitRetained(t *testing.T) {
	for _, joined := range []bool{false, true} {
		t.Run(map[bool]string{false:"direct-expected", true:"joined-unexpected"}[joined], func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRevision4WaitExitChild$")
			command.Env = append(os.Environ(), "SPK_REVISION4_EXIT_CHILD=1")
			if err := command.Start(); err != nil { t.Fatal(err) }
			l := custodyRuntime(t)
			l.command = command
			l.startChildWaitWithCall(command, func(c *exec.Cmd) error {
				actual := c.Wait() // sole actual Wait; state read only by its owner
				if joined { return errors.Join(actual, unix.EIO) }
				return actual
			})
			<-l.childWait.done
			p := l.childWait.published()
			var actualExit *exec.ExitError
			if !p.Completed || !p.ProcessStatePresent || p.Success || p.ExitCode != 7 || !errors.As(p.WaitErr, &actualExit) || p.ExpectedExitError == joined { t.Fatal("actual nonzero child publication lost", p) }
			outer, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			defer cancel()
			// Normal observer and lock-refusal collector must BOTH distinguish a
			// direct expected ExitError from a joined unexpected returned cause.
			observed, observationErr := observeActualChildWait(outer, l.childWait)
			if !observed.reapProven || observed.expectedExitError == joined || (observationErr != nil) != joined { t.Fatal("expected classification erased joined cause", observationErr) }
			first := l.abortStartBefore(outer, time.Now().Add(time.Second))
			var receipt *startupAbortError
			if !errors.As(first, &receipt) || !errors.As(first, &actualExit) || !errors.Is(first, context.DeadlineExceeded) || errors.Is(first, unix.EIO) != joined || !receipt.facts.reapProven || receipt.facts.expectedExitError == joined || receipt.facts.runtimeCleanupAttempted || receipt.facts.signalAttempted { t.Fatal("expired FIRST suppressed actual Wait", first) }
		})
	}
}
