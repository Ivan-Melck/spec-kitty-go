//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"context"
	"errors"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
	"golang.org/x/sys/unix"
)

func (l *Lifecycle) expiryCleanupReport() *expiryCleanupReport {
	if l == nil {
		return nil
	}
	return l.expiry.report
}
func (r *expiryCleanupReport) localOpened() { r.acquired(expiryLifecycleMetadata) }
func (r *expiryCleanupReport) localClosed(err error) {
	r.closing(expiryLifecycleMetadata)
	r.closed(expiryLifecycleMetadata, err)
}

// Fixed actual-close sites. Production routes pass only the literal syscall;
// the unexported component helper retains no configurable closer or selector.
// The nil ordinary route retains its historical outward result.
func (r *expiryCleanupReport) closeLocalFile(file *os.File) error {
	err := file.Close()
	r.localClosed(err)
	return err
}
func (r *expiryCleanupReport) closeLocalFD(fd int) error {
	return r.closeLocalFDWith(fd, unix.Close)
}

// The fixed production wrapper above is the only production caller. Component
// tests can model returned-close errors without re-closing an unowned raw fd.
func (r *expiryCleanupReport) closeLocalFDWith(fd int, closeFD func(int) error) error {
	err := closeFD(fd)
	r.localClosed(err)
	return err
}
func (r *expiryCleanupReport) closeLocalConnection(connection net.Conn) error {
	err := connection.Close()
	r.localClosed(err)
	return err
}
func (r *expiryCleanupReport) supervisorOpened() { r.acquired(expirySupervisor) }
func (r *expiryCleanupReport) supervisorClosed(err error) {
	r.closing(expirySupervisor)
	r.closed(expirySupervisor, err)
}

type expiryLifecycleWork struct {
	// Admission and rundown are guarded by the EXISTING Lifecycle.mu. The
	// teardown claim is checked before that mutex and again during admission.
	owner       atomic.Bool
	retired     bool
	active      uint64
	drained     chan struct{}
	drainClosed bool
	done        chan struct{}
}

func newExpiryLifecycleWork() *expiryLifecycleWork {
	return &expiryLifecycleWork{drained: make(chan struct{}), done: make(chan struct{})}
}

type expiryLifecycleToken struct {
	lifecycle *Lifecycle
	work      *expiryLifecycleWork
	report    *expiryCleanupReport
	completed bool // Lifecycle.mu
}

func (t *expiryLifecycleToken) cleanupReport() *expiryCleanupReport {
	if t == nil {
		return nil
	}
	return t.report
}
func (l *Lifecycle) expiryAdmissionRetired() bool {
	if l.ownedRevoked.Load() { return true }
	g := l.expiry.gate
	return g != nil && (g.invalid.Load() || (g.ctx != nil && g.ctx.Err() != nil))
}
func (l *Lifecycle) beginExpiryWork() (*expiryLifecycleToken, error) {
	if l == nil || l.expiry.work == nil {
		return nil, nil
	}
	w := l.expiry.work
	if l.expiryAdmissionRetired() || w.owner.Load() {
		return nil, failure(FailureOwnership, "expiry_work", "lifecycle admission retired", nil)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.expiryAdmissionRetired() || w.owner.Load() || w.retired || w.active == ^uint64(0) {
		return nil, failure(FailureOwnership, "expiry_work", "lifecycle admission retired or exhausted", nil)
	}
	w.active++
	l.expiry.report.increment(&l.expiry.report.lifecyclePending, expiryLifecycleMetadata)
	return &expiryLifecycleToken{lifecycle: l, work: w, report: l.expiry.report}, nil
}
func (l *Lifecycle) finishExpiryWork(t *expiryLifecycleToken) {
	if t == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.expiry.work
	if t.lifecycle != l || t.work != w || t.report != l.expiry.report || t.completed || w.active == 0 || t.report.lifecyclePending.Load() == 0 {
		l.expiry.report.fail(expiryLifecycleMetadata, errors.New("invalid lifecycle work completion"))
		return
	}
	t.completed = true
	w.active--
	t.report.lifecyclePending.Add(^uint64(0))
	if w.retired && w.active == 0 && !w.drainClosed {
		w.drainClosed = true
		close(w.drained) // all actual closes and result publications precede this
	}
}

type expiryLifecycleTeardown struct{ work *expiryLifecycleWork }

// Shared admission retirement/revocation already precedes this owner claim.
// A losing observer performs no second body/close/open and joins outside locks.
func (l *Lifecycle) initRuntimeCleanupReport(report *expiryCleanupReport) { l.expiry.report = report }
func (v *VerifiedExecutable) bindExpiryFailedCleanup(ctx context.Context) {
	if v != nil && v.expiry.report != nil { v.expiry.report.ownCleanupCutoff(ctx, time.Now().Add(expiryCleanupBudget)) }
}
func (l *Lifecycle) bindExpiryCleanupCutoff(ctx context.Context, cutoff time.Time) time.Time {
	if l.expiry.report == nil { return intersectCleanupDeadline(ctx, cutoff) }
	return l.expiry.report.ownCleanupCutoff(ctx, cutoff)
}
func (l *Lifecycle) expiryCleanupLimit(budget time.Duration) time.Duration {
	if l.expiry.report != nil && budget > expiryCleanupBudget { return expiryCleanupBudget }
	return budget
}
func (l *Lifecycle) beginExpiryTeardown() (*expiryLifecycleTeardown, bool, error) {
	ctx, cancel := context.WithDeadline(context.Background(), l.originalCleanupCutoff(context.Background()))
	defer cancel()
	return l.beginExpiryTeardownBefore(ctx)
}
func (l *Lifecycle) beginExpiryTeardownBefore(ctx context.Context) (*expiryLifecycleTeardown, bool, error) {
	if l == nil || l.expiry.work == nil {
		return nil, true, nil
	}
	w := l.expiry.work
	if w.owner.CompareAndSwap(false, true) {
		l.expiry.report.teardownPending.Store(true)
		return &expiryLifecycleTeardown{work: w}, true, nil
	}
	if completionBeforeDeadline(ctx, w.done) { return nil, false, nil }
	err := failure(FailureCleanup, "expiry_work", "winning lifecycle teardown incomplete", ctx.Err())
	l.expiry.report.fail(expiryLifecycleMetadata, err)
	return nil, false, err
}
func (l *Lifecycle) drainExpiryTeardown(t *expiryLifecycleTeardown) error {
	ctx, cancel := context.WithDeadline(context.Background(), l.originalCleanupCutoff(context.Background()))
	defer cancel()
	history, unsafe := l.drainExpiryTeardownBefore(ctx, t)
	return withStartupCustody(unsafe, history)
}
// The second result alone gates safe process teardown. A completed failed close
// remains historical custody even when every actual work owner has drained.
func (l *Lifecycle) drainExpiryTeardownBefore(ctx context.Context, t *expiryLifecycleTeardown) (history, unsafe error) {
	if t == nil { return nil, nil }
	if g := l.expiry.gate; g != nil {
		unsafe = g.teardownBefore(ctx) // rundown only; no restarted 5s/10s timer
		history = g.report.firstError()
		if unsafe == nil { unsafe = g.report.gateQuiescenceError() }
		if unsafe != nil { return history, unsafe }
	}
	if err := l.lockCleanup(ctx); err != nil { return history, err }
	w := l.expiry.work
	if t.work != w || !w.owner.Load() {
		l.mu.Unlock()
		return history, failure(FailureCleanup, "expiry_work", "teardown identity differs", nil)
	}
	w.retired = true
	l.expiry.report.lifecycleRetired.Store(true)
	if w.active == 0 && !w.drainClosed {
		w.drainClosed = true
		close(w.drained)
	}
	l.mu.Unlock()
	if !completionBeforeDeadline(ctx, w.drained) {
		return history, failure(FailureCleanup, "expiry_work", "admitted lifecycle work incomplete", ctx.Err())
	}
	// Never wait for an unclaimed Lifetime: the winning body's closeOwned
	// defer must claim it. Only a preexisting actual Close can be joined here.
	if claim := l.expiry.claim.Load(); claim != nil && claim.state.Load() == expiryLifetimeInFlight {
		if !completionBeforeDeadline(ctx, claim.done) {
			return history, failure(FailureCleanup, "expiry_work", "actual lifetime close incomplete", ctx.Err())
		}
	}
	return joinStartupCustody(history, l.expiry.report.firstError()), nil
}
func (l *Lifecycle) finishExpiryTeardown(t *expiryLifecycleTeardown, resultErr error) {
	if t == nil {
		return
	}
	// Registered before the original closeOwned defer: actual close/report
	// publication precedes completion even on a failing body. No join here.
	l.expiry.report.fail(expiryLifecycleMetadata, resultErr)
	l.expiry.report.teardownPending.Store(false)
	close(t.work.done)
}

// Preserve the existing E postretirement operation primary without admitting
// new work, reopening metadata or losing the original entire custody result.
func (l *Lifecycle) expiryStaleCustodyResult(prior error) error {
	if w := l.expiry.work; w != nil && w.owner.Load() {
		return withStartupCustody(failure(FailureOwnership, "expiry_work", "lifecycle admission retired", nil), prior)
	}
	return prior
}

const (
	expiryLifetimeUnclaimed uint32 = iota
	expiryLifetimeInFlight
	expiryLifetimeCompleted
)

type expiryLifetimeClaim struct {
	err error // immutable actual FIRST result; done publishes this field
	lifetime *retainedkernel.Lifetime
	report   *expiryCleanupReport
	state    atomic.Uint32
	done     chan struct{}
}

// Called under l.mu at the successful New boundary, BEFORE atomic Store and
// its revoked-acquisition postcheck. Transfers do not register a second owner.
func (l *Lifecycle) registerExpiryLifetime(lifetime *retainedkernel.Lifetime) {
	r := l.expiry.report
	if r == nil {
		return
	}
	c := &expiryLifetimeClaim{lifetime: lifetime, report: r, done: make(chan struct{})}
	if lifetime == nil || !l.expiry.claim.CompareAndSwap(nil, c) {
		r.fail(expiryLifetime, errors.New("duplicate or absent successful lifetime"))
		return
	}
	r.acquired(expiryLifetime)
}

// Both original owned.go close sites enter this exact claim. Losers neither
// invoke frozen Close nor publish repeat nil, and never wait under l.mu.
func (l *Lifecycle) closeExpiryLifetime(lifetime *retainedkernel.Lifetime) bool {
	return l.closeExpiryLifetimeWith(lifetime, (*retainedkernel.Lifetime).Close)
}

// Fixed method expression above is the only production closer. The component
// seam stores no closer and never changes the canonical lifetime claim.
func (l *Lifecycle) closeExpiryLifetimeWith(lifetime *retainedkernel.Lifetime, closeLifetime func(*retainedkernel.Lifetime) error) bool {
	if l.expiry.report == nil {
		return false
	}
	c := l.expiry.claim.Load()
	if c == nil || c.lifetime != lifetime || c.report != l.expiry.report {
		l.expiry.report.fail(expiryLifetime, errors.New("lifetime close lacks canonical claim"))
		return true
	}
	if !c.state.CompareAndSwap(expiryLifetimeUnclaimed, expiryLifetimeInFlight) {
		return true
	}
	c.report.closing(expiryLifetime)
	err := closeLifetime(lifetime)
	c.err = err
	c.report.closed(expiryLifetime, err)
	c.state.Store(expiryLifetimeCompleted)
	close(c.done) // completion LAST, after actual first outcome
	return true
}

// Called only outside l.mu, unlike the claim-only acquisition postcheck.
func (l *Lifecycle) expiryLifetimeCloseResult(lifetime *retainedkernel.Lifetime) error {
	c := l.expiry.claim.Load()
	if c == nil || c.lifetime != lifetime || c.report != l.expiry.report {
		return failure(FailureCleanup, "close_owned", "lifetime close lacks canonical claim", nil)
	}
	ctx, cancel := context.WithDeadline(context.Background(), l.originalCleanupCutoff(context.Background()))
	defer cancel()
	return l.expiryLifetimeCloseResultBefore(ctx, lifetime)
}
func (l *Lifecycle) expiryLifetimeCloseResultBefore(ctx context.Context, lifetime *retainedkernel.Lifetime) error {
	c := l.expiry.claim.Load()
	if c == nil || c.lifetime != lifetime || c.report != l.expiry.report {
		return failure(FailureCleanup, "close_owned", "lifetime close lacks canonical claim", nil)
	}
	if !completionBeforeDeadline(ctx, c.done) { return failure(FailureCleanup, "close_owned", "actual lifetime FIRST close incomplete", ctx.Err()) }
	return c.err
}
