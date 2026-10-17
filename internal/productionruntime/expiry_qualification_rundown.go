//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
)

// Authority is this private canonical pointer, NEVER phase or !failed. All
// fields below are lease.mu protected. A residual controller is never replaced.
type expiryControlKind uint8

const (
	expiryControlFresh expiryControlKind = iota
	expiryControlRestartClean
	expiryControlRestartUnclean
	expiryControlFinal
)

type expiryControlStage uint8

const (
	expiryControlReserved expiryControlStage = iota
	expiryControlAcquiringRoot
	expiryControlRetired
	expiryControlDrained
	expiryControlChecking
	expiryControlConstructing
	expiryControlValidating
	expiryControlHandedOff
	expiryControlRollback
	expiryControlRootClosing
	expiryControlTerminalClosed
	expiryControlTerminalResidual
)

// Permanent, private strong roots, not a cleanup/admission authority. Failed
// graphs and borrowed referents can accumulate without bound (OWNED/UNQUALIFIED).
// Only lease.mu -> this mutex is permitted; no traversal, disposal or retry.
var expiryResidualCustody struct {
	sync.Mutex
	head *expiryHostLease
}

type expiryLeaseControl struct {
	lease                  *expiryHostLease
	predecessor, candidate *expiryHostRun
	kind                   expiryControlKind
	stage                  expiryControlStage
	metadata               *expiryMetadataWork // controller work is NOT in the external drain
	scenario               bool                // frozen before intentional cancellation
	firstErr               error               // retained first controller failure, including constructor failure
	opaqueStartup          bool                // failed constructor returned no owner/final-report handle
}

// A generation gets exactly one window. done only means retired AND zero;
// it never means transient idle, is never reset, and cannot be reopened.
type expiryWorkWindow struct {
	retired, completed bool
	operation          *expiryOperationWork
	metadata           *expiryMetadataWork
	metadataCount      uint64
	done               chan struct{}
}
type expiryOperationWork struct {
	run       *expiryHostRun
	window    *expiryWorkWindow
	completed bool
}
type expiryMetadataWork struct {
	run       *expiryHostRun
	control   *expiryLeaseControl
	window    *expiryWorkWindow
	next      *expiryMetadataWork
	completed bool
}
type expiryPathSnapshot struct {
	parent, directory, lock                         *os.File
	parentIdentity, directoryIdentity, lockIdentity expiryRootIdentity
	root                                            string
}

func newExpiryWorkWindow() *expiryWorkWindow { return &expiryWorkWindow{done: make(chan struct{})} }
func (l *expiryHostLease) failLocked()       { l.failed = true }
func (l *expiryHostLease) signalWindowLocked(w *expiryWorkWindow) {
	if w != nil && w.retired && w.operation == nil && w.metadataCount == 0 && w.metadata == nil && !w.completed {
		w.completed = true
		close(w.done)
	}
}
func (l *expiryHostLease) retireLocked(r *expiryHostRun) {
	if r != nil && r.window != nil {
		r.window.retired = true
		l.signalWindowLocked(r.window)
	}
}
func (l *expiryHostLease) quiescentLocked(r *expiryHostRun) bool {
	return r == nil || r.window != nil && r.window.retired && r.window.completed && r.window.operation == nil && r.window.metadataCount == 0 && r.window.metadata == nil
}
func expiryOriginalDoneOpen(r *expiryHostRun) bool {
	if r == nil || r.gate == nil {
		return false
	}
	done := r.gate.Done()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// Scalar cutoffs remain separate from gate cancellation, not a joint snapshot.
func (r *expiryHostRun) currentLocked(ctx context.Context) bool {
	l := r.lease
	return expiryOriginalDoneOpen(r) && ctx != nil && ctx.Err() == nil && r.ctx != nil && r.ctx.Err() == nil && !r.invalid.Load() &&
		l.current == r && l.phase == expiryLeaseRunning && l.control == nil && !l.failed && r.window != nil && !r.window.retired &&
		l.parent != nil && l.parent.Err() == nil && l.ctx != nil && l.ctx.Err() == nil &&
		r.host != nil && r.owner != nil && r.composition != nil && expiryIdentityValid(r.identity) && r.gate.Identity() == r.identity &&
		l.config.ControlRoot == l.controlRoot && l.config.HostAuthorityID == l.hostAuthority &&
		r.owner == r.composition.owner && r.owner.expiry.run == r && r.host.expiry.run == r &&
		r.composition.binding == l.binding && r.composition.database == l.database
}
func (r *expiryHostRun) admitOperation(ctx context.Context) (*expiryOperationWork, error) {
	if r == nil || r.lease == nil {
		return nil, expiryFailure("expiry_generation")
	}
	l := r.lease
	l.mu.Lock()
	defer l.mu.Unlock()
	if !r.currentLocked(ctx) || r.window.operation != nil {
		l.failLocked()
		return nil, expiryFailure("expiry_overlap_or_generation")
	}
	work := &expiryOperationWork{run: r, window: r.window}
	r.window.operation = work // BEFORE TryLock, retained through all outside-lock joins
	return work, nil
}
func (work *expiryOperationWork) complete() {
	r := work.run
	l := r.lease
	l.mu.Lock()
	defer l.mu.Unlock()
	if work.completed || r.window != work.window || work.window.operation != work {
		l.failLocked()
		return
	}
	work.completed = true
	work.window.operation = nil
	l.signalWindowLocked(work.window) // LAST publication; nothing result-affecting follows
}
func (l *expiryHostLease) snapshotPathsLocked() expiryPathSnapshot {
	return expiryPathSnapshot{l.parentDirectory, l.directory, l.lock, l.parentIdentity, l.directoryIdentity, l.lockIdentity, l.controlRoot}
}
func (r *expiryHostRun) admitMetadata(ctx context.Context) (*expiryMetadataWork, expiryPathSnapshot, error) {
	l := r.lease
	l.mu.Lock()
	defer l.mu.Unlock()
	if !r.currentLocked(ctx) || r.window.metadataCount == ^uint64(0) {
		l.failLocked()
		return nil, expiryPathSnapshot{}, expiryFailure("expiry_metadata_admission")
	}
	// Canonical current/gate identity precedes this separate scalar cutoff;
	// refuse BEFORE admission, borrowed root-pointer access or metadata opens.
	if err := r.gate.RecordedCleanupError(); err != nil {
		l.failLocked()
		return nil, expiryPathSnapshot{}, err
	}
	w := r.window
	work := &expiryMetadataWork{run: r, window: w, next: w.metadata}
	w.metadata, w.metadataCount = work, w.metadataCount+1
	return work, l.snapshotPathsLocked(), nil // root pointer access only AFTER admission
}
func (work *expiryMetadataWork) complete(err error) {
	var l *expiryHostLease
	if work.control != nil {
		l = work.control.lease
	} else {
		l = work.run.lease
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Actual transient Close and all sticky report publication precede completion.
	l.cleanup.record(err)
	if err != nil {
		l.failLocked()
	}
	if work.completed {
		l.failLocked()
		return
	}
	if c := work.control; c != nil {
		if l.control != c || c.metadata != work {
			l.failLocked()
			return
		}
		work.completed = true
		c.metadata = nil
		return
	}
	w := work.window
	link := &w.metadata
	for *link != nil && *link != work {
		link = &(*link).next
	}
	if work.run.window != w || *link != work || w.metadataCount == 0 {
		l.failLocked()
		return
	}
	*link = work.next
	work.completed = true
	w.metadataCount--
	l.signalWindowLocked(w)
}
func (l *expiryHostLease) controlLocked(c *expiryLeaseControl, stages ...expiryControlStage) bool {
	if c == nil || c.lease != l || l.control != c || l.current != c.predecessor {
		return false
	}
	if c.candidate != nil && c.candidate.lease != l {
		return false
	}
	for _, stage := range stages {
		if c.stage == stage {
			return true
		}
	}
	return false
}
func (l *expiryHostLease) moveControl(c *expiryLeaseControl, to expiryControlStage, from ...expiryControlStage) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.controlLocked(c, from...) {
		l.failLocked()
		return expiryFailure("expiry_controller_stage")
	}
	c.stage = to
	return nil
}

// Reservation is the ONLY allocation of a controller. Called under lease.mu.
func (l *expiryHostLease) reserveControlLocked(r *expiryHostRun, kind expiryControlKind, ctx context.Context) (*expiryLeaseControl, error) {
	if l.control != nil {
		l.failLocked()
		return nil, expiryFailure("expiry_controller_owned")
	}
	if kind == expiryControlFresh {
		if r != nil || l.current != nil || l.phase != expiryLeaseFresh || l.failed {
			l.failLocked()
			return nil, expiryFailure("expiry_fresh_controller")
		}
	} else {
		if r == nil || r.lease != l || l.current != r || r.window == nil || l.phase == expiryLeaseClosed {
			l.failLocked()
			return nil, expiryFailure("expiry_stale_controller")
		}
		if kind == expiryControlRestartClean || kind == expiryControlRestartUnclean {
			if !r.currentLocked(ctx) || r.window.operation != nil {
				l.failLocked()
				return nil, expiryFailure("expiry_restart_controller")
			}
		} else if kind != expiryControlFinal {
			l.failLocked()
			return nil, expiryFailure("expiry_controller_kind")
		}
	}
	c := &expiryLeaseControl{lease: l, predecessor: r, kind: kind, stage: expiryControlReserved}
	if r != nil {
		if kind == expiryControlFinal {
			c.scenario = r.currentLocked(ctx) && r.window.operation == nil && r.negativeClosed && r.released && r.matchedAfterExpiry && r.expiryRevision != 0
			if !c.scenario {
				l.failLocked()
			}
		}
		// Restart's last ORIGINAL Done cutoff is immediately before retirement.
		if kind != expiryControlFinal && !expiryOriginalDoneOpen(r) {
			l.failLocked()
			return nil, expiryFailure("expiry_restart_revoked")
		}
		// Keep the ORIGINAL Done prerequisite above. This same-original-gate
		// error load is another scalar cutoff, never a final completeness check.
		if kind != expiryControlFinal {
			if err := r.gate.RecordedCleanupError(); err != nil {
				l.failLocked()
				return nil, err
			}
		}
		r.controlledTeardown.Store(true)
		l.retireLocked(r)
		c.stage = expiryControlRetired
		if kind == expiryControlFinal {
			l.phase = expiryLeaseClosing
		} else {
			l.phase = expiryLeaseRestarting
		}
	}
	l.control = c
	return c, nil
}
func (l *expiryHostLease) reserveControl(r *expiryHostRun, kind expiryControlKind, ctx context.Context) (*expiryLeaseControl, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reserveControlLocked(r, kind, ctx)
}

// Caller holds lease.mu. Authenticate BEFORE even the idempotent pinned test.
// The custody critical section contains only immutable linking/publication.
func (l *expiryHostLease) pinResidualLocked(c *expiryLeaseControl) bool {
	if !l.controlLocked(c, expiryControlTerminalResidual) {
		l.failLocked()
		return false
	}
	if l.residualPinned {
		return true
	}
	expiryResidualCustody.Lock()
	l.residualNext = expiryResidualCustody.head
	l.residualPinned = true
	expiryResidualCustody.head = l
	expiryResidualCustody.Unlock()
	return true
}
func (l *expiryHostLease) residual(c *expiryLeaseControl, err error) error {
	l.mu.Lock()
	l.failLocked()
	l.cleanup.record(err)
	if l.control == c && c != nil && c.lease == l && l.current == c.predecessor && (c.candidate == nil || c.candidate.lease == l) {
		if c.firstErr == nil {
			c.firstErr = err
		}
		c.stage = expiryControlTerminalResidual
		l.pinResidualLocked(c) // rooted BEFORE publication unlock/last-local return
	}
	l.mu.Unlock()
	return errors.Join(err, expiryFailure("expiry_controller_residual"))
}

// No lease/operation/Host/lifecycle/gate locks may be held here. A deadline
// cannot preempt filesystem syscalls, mutexes, or opaque borrowed callbacks.
func (l *expiryHostLease) drainControl(ctx context.Context, c *expiryLeaseControl) error {
	l.mu.Lock()
	if !l.controlLocked(c, expiryControlRetired, expiryControlRollback) {
		l.failLocked()
		l.mu.Unlock()
		return expiryFailure("expiry_controller_drain")
	}
	var waits []<-chan struct{}
	for _, r := range []*expiryHostRun{c.predecessor, c.candidate} {
		if r != nil {
			l.retireLocked(r)
			waits = append(waits, r.window.done)
		}
	}
	l.mu.Unlock()
	for _, done := range waits {
		select {
		case <-done:
		case <-ctx.Done():
			return l.residual(c, ctx.Err())
		}
	}
	if ctx.Err() != nil {
		return l.residual(c, ctx.Err())
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.controlLocked(c, expiryControlRetired, expiryControlRollback) || !l.quiescentLocked(c.predecessor) || !l.quiescentLocked(c.candidate) {
		l.failLocked()
		return expiryFailure("expiry_controller_drain")
	}
	if c.stage != expiryControlRollback {
		c.stage = expiryControlDrained
	}
	return nil
}
func (l *expiryHostLease) checkPathsForControl(c *expiryLeaseControl) (result error) {
	l.mu.Lock()
	if !l.controlLocked(c, expiryControlChecking, expiryControlValidating, expiryControlRollback) || c.metadata != nil || !l.quiescentLocked(c.predecessor) {
		l.failLocked()
		l.mu.Unlock()
		return expiryFailure("expiry_controller_metadata")
	}
	// A constructing candidate is not externally current and cannot admit work.
	if c.candidate != nil && (c.candidate.window.operation != nil || c.candidate.window.metadataCount != 0) {
		l.failLocked()
		l.mu.Unlock()
		return expiryFailure("expiry_candidate_work")
	}
	work := &expiryMetadataWork{control: c}
	c.metadata = work
	snapshot := l.snapshotPathsLocked()
	l.mu.Unlock()
	defer func() { result = errors.Join(result, l.cleanup.outcome(false)); work.complete(result) }()
	return l.checkPathSnapshot(snapshot)
}

// Only rollback may use this construction-aware union. Absence is not a
// missing close; every actually installed owner/Host/gate needs its first result.
// Normal restart/final cleanupOutcomes remains strict and unchanged.
func (r *expiryHostRun) rollbackCleanupOutcomes() error {
	l := r.lease
	l.mu.Lock()
	c := l.control
	valid := l.controlLocked(c, expiryControlRollback) && (c.predecessor == r || c.candidate == r)
	owner, host, gate := r.owner, r.host, r.gate
	ownerRecorded, hostRecorded := r.ownerCloseRecorded, r.hostCloseRecorded
	ownerErr, hostErr := r.firstOwnerCloseErr, r.firstHostCloseErr
	l.mu.Unlock()
	if !valid {
		return expiryFailure("expiry_rollback_cleanup_owner")
	}
	var result error
	if owner != nil || host != nil {
		if owner == nil || !ownerRecorded {
			result = expiryFailure("expiry_owner_cleanup_incomplete")
		}
		result = errors.Join(result, ownerErr)
	}
	// An internal startup Close may be recorded before r.host is published.
	if host != nil || hostRecorded {
		if !hostRecorded {
			result = errors.Join(result, expiryFailure("expiry_host_cleanup_incomplete"))
		}
		result = errors.Join(result, hostErr)
	}
	if gate != nil {
		if !r.gateCloseRecorded.Load() {
			result = errors.Join(result, expiryFailure("expiry_gate_cleanup_incomplete"))
		}
		if first := r.firstGateCloseErr.Load(); first != nil {
			result = errors.Join(result, first.err)
		}
		// FULL unchanged report, only after actual owner/Host close and drain.
		// A later nil can never repair the separately retained first gate error.
		result = errors.Join(result, gate.CleanupError())
	}
	return errors.Join(result, l.cleanup.outcome(false))
}
func expiryCleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// All constructor/final failure paths use the ORIGINAL control, never reserve
// another owner. Incomplete cleanup retains the pointer and the root FDs.
func (l *expiryHostLease) rollbackControl(c *expiryLeaseControl, cause error) error {
	l.mu.Lock()
	if c != nil && l.control == c && c.lease == l {
		if c.firstErr == nil {
			c.firstErr = cause
		}
		if c.stage == expiryControlTerminalResidual {
			l.pinResidualLocked(c) // includes the already-residual early return
			first := c.firstErr
			l.mu.Unlock()
			return errors.Join(cause, first)
		}
	}
	l.mu.Unlock()
	if err := l.moveControl(c, expiryControlRollback, expiryControlReserved, expiryControlAcquiringRoot, expiryControlRetired, expiryControlDrained, expiryControlChecking, expiryControlConstructing, expiryControlValidating); err != nil {
		return errors.Join(cause, err)
	}
	l.mu.Lock()
	l.failLocked()
	for _, r := range []*expiryHostRun{c.predecessor, c.candidate} {
		l.retireLocked(r)
		if r != nil {
			r.controlledTeardown.Store(true)
		}
	}
	l.mu.Unlock()
	cleanup, cancel := expiryCleanupContext()
	defer cancel()
	for _, r := range []*expiryHostRun{c.predecessor, c.candidate} {
		if r != nil {
			r.invalidate()
		}
	}
	if err := l.drainControl(cleanup, c); err != nil {
		return errors.Join(cause, err)
	}
	var outcome error
	for _, r := range []*expiryHostRun{c.predecessor, c.candidate} {
		if r == nil {
			continue
		}
		if r.host != nil {
			outcome = errors.Join(outcome, r.host.Close(cleanup))
		} else if r.owner != nil {
			outcome = errors.Join(outcome, r.owner.Close(cleanup))
		}
		// Direct outward results above AND construction-aware actual first
		// outcomes/full postdrain report below are required before root release.
		outcome = errors.Join(outcome, r.rollbackCleanupOutcomes())
	}
	outcome = errors.Join(outcome, l.cleanup.outcome(false))
	if cleanup.Err() != nil || outcome != nil {
		return l.residual(c, errors.Join(cause, outcome, cleanup.Err()))
	}
	l.mu.Lock()
	opaque := c.opaqueStartup
	l.mu.Unlock()
	if opaque {
		return l.residual(c, errors.Join(cause, expiryFailure("expiry_constructor_unknown_residual")))
	}
	// Partial roots cannot be path-certified; they are still closed under c.
	if l.parentDirectory != nil && l.directory != nil && l.lock != nil {
		// The final metadata work includes its actual transient Close and FIRST
		// publication. Failure is NOT authority to release even quiescent roots.
		outcome = errors.Join(outcome, l.checkPathsForControl(c))
	}
	// Independent cleanup context, not the intentionally cancelled run/lease
	// context. This scalar safety cutoff must follow the final metadata work
	// and precede ANY root release, including the partial-root branch below.
	// Preserve cause first; residual pins the ORIGINAL controller/owner graph.
	if cleanup.Err() != nil || outcome != nil {
		return l.residual(c, errors.Join(cause, outcome, cleanup.Err()))
	}
	outcome = errors.Join(outcome, l.closeDescriptors(c))
	l.mu.Lock()
	if l.controlLocked(c, expiryControlRootClosing) {
		c.stage, l.phase = expiryControlTerminalClosed, expiryLeaseClosed
	}
	l.mu.Unlock()
	return errors.Join(cause, outcome)
}
