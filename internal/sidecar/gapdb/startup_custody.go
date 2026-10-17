package gapdb

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// All added work/state/error retention is OWNED_UNQUALIFIED. Cooperative source
// cutoffs do not bound kernel calls, scheduler latency or physical containment.
type startupCustodyError struct { primary, custody error }
func (e *startupCustodyError) Error() string { return e.primary.Error() }
func (e *startupCustodyError) Unwrap() []error { return []error{e.primary, e.custody} }
func withStartupCustody(primary, custody error) error {
	if custody == nil { return primary }
	if primary == nil { return custody }
	return &startupCustodyError{primary: primary, custody: custody}
}
func joinStartupCustody(first, next error) error {
	if first == nil { return next }
	if next == nil { return first }
	return errors.Join(first, next)
}
func startupCleanupError(reason string, err error) error {
	if err == nil { return nil }
	return failure(FailureCleanup, "startup_custody", reason, err)
}

// One immutable claim, including its absolute cutoff. Losers never execute a
// second body, grant another drain budget, or mutate the owner's FIRST result.
// If a loser cannot observe publication within its applicable envelope it gets
// an incomplete-observation error, NOT a synthetic owner completion.
type teardownCustodyClaim struct {
	done chan struct{}
	cutoff time.Time
	err error // close(done) publication
}
func (l *Lifecycle) originalCleanupCutoff(ctx context.Context) time.Time {
	return l.originalCleanupCutoffFor(ctx, true)
}
func (l *Lifecycle) originalNonEscalatingCleanupCutoff(ctx context.Context) time.Time {
	return l.originalCleanupCutoffFor(ctx, false)
}
func (l *Lifecycle) originalCleanupCutoffFor(ctx context.Context, escalation bool) time.Time {
	budget := l.config.StopTimeout
	if budget <= 0 { budget = defaultStopTimeout }
	if escalation { budget += time.Second } // ordinary Stop's ORIGINAL escalation grace
	budget = l.expiryCleanupLimit(budget)
	return intersectCleanupDeadline(ctx, time.Now().Add(budget))
}
func (l *Lifecycle) startupCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil { ctx = context.Background() }
	cutoff := l.bindExpiryCleanupCutoff(ctx, l.originalNonEscalatingCleanupCutoff(ctx))
	return context.WithDeadline(ctx, cutoff)
}
// Before a readable creation identity exists there is no safe pathname-removal
// authority. Retain bounded private facts in the error, never delete a possibly
// substituted directory. This is opaque process-local receipt custody only.
type createdRuntimeResidual struct { path string; identity fileIdentity; diagnostic error }
func (e *createdRuntimeResidual) Error() string { return e.diagnostic.Error() }
func (e *createdRuntimeResidual) Unwrap() error { return e.diagnostic }
func preservedCreatedRuntime(path string, identity fileIdentity) error {
	return &createdRuntimeResidual{path: path, identity: identity, diagnostic: failure(FailureCleanup, "start", "created runtime custody incomplete; directory preserved", nil)}
}
func cleanupCreatedRuntime(ctx context.Context, config LifecycleConfig, path string, identity fileIdentity, report *expiryCleanupReport) error {
	owner := &Lifecycle{config: config, pidfd: -1, runtimeID: identity, provenance: SidecarProvenance{RuntimeDirectory: path}}
	owner.initRuntimeCleanupReport(report) // metadata reporting only, no gate transfer/work retirement
	cleanup, cancel := owner.startupCleanupContext(ctx)
	defer cancel()
	return owner.cleanupRuntimeBefore(cleanup, false)
}
func intersectCleanupDeadline(ctx context.Context, cutoff time.Time) time.Time {
	if ctx != nil { if d, ok := ctx.Deadline(); ok && d.Before(cutoff) { return d } }
	return cutoff
}
func (l *Lifecycle) firstTeardown(body func() error) (error, bool) {
	return l.firstCustody(true, body)
}
func (l *Lifecycle) firstCustody(retireExpiry bool, body func() error) (error, bool) {
	ctx := context.Background()
	return l.firstCustodyBefore(ctx, l.originalCleanupCutoff(ctx), retireExpiry, func(context.Context) error { return body() })
}
func (l *Lifecycle) firstCustodyBefore(ctx context.Context, cutoff time.Time, retireExpiry bool, body func(context.Context) error) (error, bool) {
	if ctx == nil { ctx = context.Background() }
	cutoff = l.bindExpiryCleanupCutoff(ctx, cutoff)
	// The shared atomic admission cutoff precedes cancellation notification,
	// claim/Once entry, any state mutex, and drain. It is NOT retirement proof.
	l.revokeOwned()
	claim := &teardownCustodyClaim{done: make(chan struct{}), cutoff: intersectCleanupDeadline(ctx, cutoff)}
	if !l.teardownCustody.CompareAndSwap(nil, claim) {
		owner := l.teardownCustody.Load()
		deadline := intersectCleanupDeadline(ctx, owner.cutoff)
		if claim.cutoff.Before(deadline) { deadline = claim.cutoff }
		join, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		if !completionBeforeDeadline(join, owner.done) {
			return failure(FailureCleanup, "teardown", "winning FIRST custody publication incomplete; residual unknown", join.Err()), false
		}
		return owner.err, false
	}
	cleanup, cancel := context.WithDeadline(ctx, claim.cutoff)
	defer cancel()
	var result error
	recorded := false
	defer func() {
		if !recorded {
			l.teardownCustodyErr = result
			claim.err = result
		}
		close(claim.done)
	}()
	if !retireExpiry {
		// Failed-start E is still unretired; no newly granted lifecycle drain.
		result = body(cleanup)
		return result, true
	}
	teardown, proceed, err := l.beginExpiryTeardownBefore(cleanup)
	if err != nil { result = err; return result, true }
	if !proceed {
		result = failure(FailureCleanup, "teardown", "external teardown owner has no shared custody result", nil)
		return result, true
	}
	// Historical completed-close failures are custody, not unsafe rundown.
	historical, unsafe := l.drainExpiryTeardownBefore(cleanup, teardown)
	if unsafe != nil { result = withStartupCustody(unsafe, historical) } else {
		result = withStartupCustody(body(cleanup), historical)
	}
	l.teardownCustodyErr = result
	claim.err = result
	recorded = true // no second write after E done; done is a publication edge
	l.finishExpiryTeardown(teardown, result) // whole body/close outcome before E done
	return result, true
}

// A completed actual publication wins even when cancellation is also ready.
// Recheck after ctx selection handles the both-ready select deterministically.
func completionBeforeDeadline(ctx context.Context, done <-chan struct{}) bool {
	select { case <-done: return true; default: }
	select {
	case <-done: return true
	case <-ctx.Done():
		select { case <-done: return true; default: return false }
	}
}
func (l *Lifecycle) lockCleanup(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil { return failure(FailureCleanup, "teardown", "state lock cutoff elapsed; residual unknown", err) }
		if l.mu.TryLock() { return nil }
		timer := time.NewTimer(time.Millisecond)
		select { case <-ctx.Done(): timer.Stop(); case <-timer.C: }
	}
}
func (l *Lifecycle) cleanupRuntime(requireSocketIdentity bool) error {
	return l.cleanupRuntimeWithCall(requireSocketIdentity, l.cleanupRuntimeFirst)
}
func (l *Lifecycle) cleanupRuntimeWithCall(requireSocketIdentity bool, cleanup func(bool) error) error {
	l.runtimeCleanupOnce.Do(func() { l.runtimeCleanupErr = cleanup(requireSocketIdentity) })
	return l.runtimeCleanupErr
}
func runtimeIdentityResult(socket string, identityErr, closeErr error) error {
	custody := startupCleanupError("runtime identity first close failed", closeErr)
	if identityErr != nil { return withStartupCustody(failure(FailureStart, "start", "runtime identity cannot be read", identityErr), custody) }
	if len(socket) > maxSocketPathBytes { return withStartupCustody(failure(FailureInvalidConfiguration, "start", "configured socket exceeds Unix bound", nil), custody) }
	return custody
}
type startupPendingCustody struct { supervisorCloseAttempted bool; supervisorCloseErr error }
func (p *startupPendingCustody) record(err error) {
	if p == nil { return }
	p.supervisorCloseAttempted = true
	p.supervisorCloseErr = joinStartupCustody(p.supervisorCloseErr, startupCleanupError("attribution rollback supervisor close failed", err))
}

// Sole production Cmd.Wait owner. The exit channel is only notification. It
// cannot establish reap, even if a synthetic channel is closed or contains nil.
type startupChildWait struct { done chan struct{}; err error; state *os.ProcessState }
// Value-only publication for physical-successor/source consumers. No Cmd,
// Process, ProcessState pointer, descriptor, signalling or reap capability is
// returned. WaitErr is the immutable actual error interface (borrowed cause).
// The original owner pointer must already be published by startup; l.mu is NOT
// a substitute for receiving done. Incomplete snapshots authorize nothing.
type childWaitPublication struct {
	Completed, ProcessStatePresent, Success, ExpectedExitError bool
	PID, ExitCode int
	WaitErr error
}
func (w *startupChildWait) published() childWaitPublication {
	if w == nil || w.done == nil { return childWaitPublication{} }
	select { case <-w.done: default: return childWaitPublication{} }
	p := childWaitPublication{Completed: true, ProcessStatePresent: w.state != nil, WaitErr: w.err}
	_, p.ExpectedExitError = w.err.(*exec.ExitError)
	if w.state != nil { p.PID, p.ExitCode, p.Success = w.state.Pid(), w.state.ExitCode(), w.state.Success() }
	return p
}
func (l *Lifecycle) startChildWait(command *exec.Cmd) { l.startChildWaitWithCall(command, (*exec.Cmd).Wait) }
func (l *Lifecycle) startChildWaitWithCall(command *exec.Cmd, waitCall func(*exec.Cmd) error) {
	l.childWaitOnce.Do(func() {
		w := &startupChildWait{done: make(chan struct{})}
		exit := make(chan error, 1)
		l.childWait, l.exit = w, exit
		l.startupPublished.Store(&startupCustodyPublication{wait: w, pending: l.pendingCustody})
		go func() {
			w.err = waitCall(command)
			w.state = command.ProcessState
			close(w.done)
			exit <- w.err
		}()
	})
}
type actualWaitObservation struct {
	attempted, observed, timedOut, reapProven, expectedExitError bool
	err error
	state *os.ProcessState
}
func observeActualChildWait(ctx context.Context, wait *startupChildWait) (f actualWaitObservation, result error) {
	if wait == nil || wait.done == nil { return f, failure(FailureCleanup, "child_wait", "actual child wait owner is absent; runtime preserved", nil) }
	f.attempted = true
	if !completionBeforeDeadline(ctx, wait.done) {
		f.timedOut = true
		return f, failure(FailureCleanup, "child_wait", "actual child reap cutoff elapsed; runtime preserved", ctx.Err())
	}
	f.observed, f.err, f.state = true, wait.err, wait.state
	f.reapProven = f.state != nil
	// Only the actual single ExitError result is expected termination. A joined
	// unexpected error containing an ExitError must NOT disappear as expected.
	_, f.expectedExitError = f.err.(*exec.ExitError)
	if f.err != nil && !f.expectedExitError { result = startupCleanupError("unexpected actual child Wait error; runtime preserved", f.err) }
	if !f.reapProven { result = withStartupCustody(result, failure(FailureCleanup, "child_wait", "actual ProcessState absent; reap unproven", f.err)) }
	return f, result
}
func (l *Lifecycle) shutdownTailCustody(ctx context.Context, primary error) error {
	// Even an early ownership/signal/lock refusal must consume a published
	// unexpected actual Wait result. No new deadline and no second Wait owner.
	if !l.attached && (!l.shutdownWaitChecked || l.shutdownWait.timedOut) {
		// A phase timeout is not an actual result. On an early escalation refusal
		// observe only the SAME owner/remaining cleanup cutoff, preserving the
		// already returned primary even if actual completion is now published.
		primary = withStartupCustody(primary, l.observeShutdownCompletion(ctx))
	}
	return withStartupCustody(primary, l.closeOwnedBefore(ctx))
}
func (l *Lifecycle) observeShutdownCompletion(ctx context.Context) error {
	l.shutdownWaitChecked = true
	if l.attached {
		// Ordinary Attach poll completion is observation only, not Cmd.Wait/reap
		// authority. finishExitedShutdown separately revalidates exact absence.
		if l.attachExitObserved { return l.attachExitErr }
		observed, err := observeAttachedExit(ctx, l.exit)
		if observed { l.attachExitObserved, l.attachExitErr = true, err }
		return err
	}
	if l.command == nil && l.childWait == nil && l.process.PID == 0 && l.exit == nil { return nil } // no child ever started
	var err error
	l.shutdownWait, err = observeActualChildWait(ctx, l.childWait)
	return err
}
// chan error cannot be used as a done channel: ordinary Attach consumes its
// actual monitor result explicitly and never calls the child observer.
func observeAttachedExit(ctx context.Context, exit <-chan error) (bool, error) {
	select { case err := <-exit: return true, err; default: }
	select {
	case err := <-exit: return true, err
	case <-ctx.Done():
		select { case err := <-exit: return true, err; default: return false, ctx.Err() }
	}
}

// Only startup writes these immutable snapshots: once with the installed sole
// Wait owner, then after each complete attribution/rollback attempt. Readers on
// lock refusal must not consult mutable command/process/pending/shutdown fields.
type startupCustodyPublication struct { wait *startupChildWait; pending startupPendingCustody }
func (l *Lifecycle) publishStartupPendingCustody(pending startupPendingCustody) {
	prior := l.startupPublished.Load()
	var wait *startupChildWait
	if prior != nil { wait = prior.wait }
	l.startupPublished.Store(&startupCustodyPublication{wait: wait, pending: pending})
}
func (l *Lifecycle) collectPublishedStartupCustody(f *startupAbortFacts) error {
	owner := l.startupPublished.Load()
	if owner == nil { return nil } // absent publication is NOT no-child/reap proof
	f.pending = owner.pending
	custody := f.pending.supervisorCloseErr
	p := owner.wait.published() // nonblocking, value-only, receives done first
	f.waitPublication = p
	f.waitAttempted = owner.wait != nil // observation only, NEVER another Wait
	if !p.Completed { return custody }
	f.waitObserved, f.reapProven, f.expectedExitError, f.waitErr = true, p.ProcessStatePresent, p.ExpectedExitError, p.WaitErr
	if p.WaitErr != nil && !p.ExpectedExitError {
		custody = joinStartupCustody(custody, startupCleanupError("unexpected published actual child Wait error; runtime preserved", p.WaitErr))
	}
	if !p.ProcessStatePresent {
		custody = joinStartupCustody(custody, failure(FailureCleanup, "child_wait", "published actual ProcessState absent; reap unproven", p.WaitErr))
	}
	return custody
}

type startupAbortFacts struct {
	signalAttempted bool
	signalErr error
	waitAttempted, waitObserved, waitTimedOut, reapProven, expectedExitError bool
	waitErr error
	waitState *os.ProcessState
	waitPublication childWaitPublication // early-refusal value-only facts, no state capability
	pending startupPendingCustody
	runtimeCleanupAttempted, runtimeDeletionWithheld, residualPossible bool
}
type startupAbortOutcome struct { facts startupAbortFacts; result error }
// Failed Start returns no Lifecycle. Keep its existing owner reachable through
// the immutable error receipt, without an exported resource/retry capability.
// This is process-local opaque custody, NOT durable placement or containment;
// dropping all receipts/process exit can still lose this ownership information.
// Late actual Wait may publish in owner.childWait, never repair frozen facts.
type startupAbortError struct { facts startupAbortFacts; diagnostic error; owner *Lifecycle }
func (e *startupAbortError) Error() string { return e.diagnostic.Error() }
func (e *startupAbortError) Unwrap() []error {
	causes := []error{e.diagnostic}
	if e.facts.signalErr != nil { causes = append(causes, e.facts.signalErr) }
	if e.facts.waitErr != nil { causes = append(causes, e.facts.waitErr) }
	return causes
}

// The caller supplies the independently owned original cleanup cutoff, NOT
// the readiness phase deadline. No new run/observation window is created here.
func (l *Lifecycle) abortStartFirst(ctx context.Context, deadline time.Time, killChild func(*os.Process) error) startupAbortOutcome {
	var outcome startupAbortOutcome
	f := &outcome.facts
	if err := l.lockCleanup(ctx); err != nil {
		f.residualPossible, f.runtimeDeletionWithheld = true, true
		custody := l.collectPublishedStartupCustody(f)
		outcome.result = &startupAbortError{facts: *f, owner: l, diagnostic: withStartupCustody(err, custody)}
		return outcome
	}
	command, wait := l.command, l.childWait
	f.pending = l.pendingCustody
	custody := f.pending.supervisorCloseErr
	l.stopped = true
	noChild := command == nil && l.process.PID == 0 && l.exit == nil && wait == nil
	unobserved := command != nil && command.Process != nil && l.process.PID == 0
	if !noChild && !unobserved {
		if command == nil || command.Process == nil || command.Process.Pid != l.process.PID {
			custody = joinStartupCustody(custody, failure(FailureCleanup, "abort_start", "started child custody is not attributable; runtime preserved", nil))
		} else {
			switch l.inspectOwnershipLocked().State {
			case OwnershipOwnedLive:
				f.signalAttempted = true
				f.signalErr = l.signalLocked(unix.SIGKILL)
				custody = joinStartupCustody(custody, startupCleanupError("first startup termination failed; runtime preserved", f.signalErr))
			case OwnershipOwnedExited, OwnershipStaleArtifact:
			default: custody = joinStartupCustody(custody, failure(FailureCleanup, "abort_start", "started child identity is not provable; runtime preserved", nil))
			}
		}
	}
	l.mu.Unlock()
	if unobserved {
		f.signalAttempted = true
		f.signalErr = killChild(command.Process) // original child HANDLE, no PID rescue
		if f.signalErr != nil && !errors.Is(f.signalErr, os.ErrProcessDone) { custody = joinStartupCustody(custody, startupCleanupError("first startup child termination failed; runtime preserved", f.signalErr)) }
	}
	if !noChild {
		if cap := time.Now().Add(time.Second); !unobserved && cap.Before(deadline) { deadline = cap }
		waitCtx, cancel := context.WithDeadline(ctx, deadline)
		observed, err := observeActualChildWait(waitCtx, wait)
		cancel()
		f.waitAttempted, f.waitObserved, f.waitTimedOut, f.reapProven = observed.attempted, observed.observed, observed.timedOut, observed.reapProven
		f.waitErr, f.waitState, f.expectedExitError = observed.err, observed.state, observed.expectedExitError
		custody = joinStartupCustody(custody, err)
	}
	if err := l.lockCleanup(ctx); err != nil { custody = joinStartupCustody(custody, err) } else {
		custody = joinStartupCustody(custody, l.closePidfd())
		l.mu.Unlock()
	}
	custody = joinStartupCustody(custody, l.closeOwnedBefore(ctx))
	f.runtimeDeletionWithheld = custody != nil || (!noChild && !f.reapProven) || ctx.Err() != nil
	if !f.runtimeDeletionWithheld { f.runtimeCleanupAttempted = true; custody = l.cleanupRuntimeBefore(ctx, false) }
	if ctx.Err() != nil { custody = joinStartupCustody(custody, startupCleanupError("cleanup cutoff elapsed; residual unknown", ctx.Err())) }
	f.residualPossible = custody != nil
	if custody != nil { outcome.result = &startupAbortError{facts: *f, owner: l, diagnostic: failure(FailureCleanup, "abort_start", "startup custody failed; residual resources may remain", custody)} }
	return outcome
}
