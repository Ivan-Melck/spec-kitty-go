//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/projection"
)

// checkCurrent does not require held phase: it also protects baseline matched
// observations and pre-TTL restart. It does not hash or issue observation RPCs.
func (r *expiryHostRun) checkCurrent(ctx context.Context) (result error) {
	if r == nil || r.lease == nil {
		return expiryFailure("expiry_generation")
	}
	work, snapshot, err := r.admitMetadata(ctx)
	if err != nil {
		return err
	} // BEFORE any root-pointer access or metadata open
	l := r.lease
	defer func() {
		result = errors.Join(result, l.cleanup.outcome(false)) // actual deferred Close has returned
		work.complete(result)                                  // nested token complete LAST, before outer operation
	}()
	if err := l.checkPathSnapshot(snapshot); err != nil {
		return err
	}
	if _, ok := r.host.retainedCapture(r.composition); !ok {
		return expiryFailure("expiry_composition")
	}
	l.mu.Lock()
	valid := r.currentLocked(ctx)
	l.mu.Unlock()
	if !valid {
		return expiryFailure("expiry_generation_lease")
	}
	return nil
}

func (r *expiryHostRun) checkHeld(ctx context.Context, expected projection.AttributionV2) error {
	if err := r.checkCurrent(ctx); err != nil {
		return err
	}
	if err := r.gate.CheckHeld(ctx); err != nil {
		return err
	}
	closing, ok := r.host.retainedCapture(r.composition)
	if !ok || closing != expected || ctx.Err() != nil || r.ctx.Err() != nil || r.invalid.Load() {
		return expiryFailure("expiry_frozen_composition")
	}
	return nil
}

// observeS1Refusal returns only sanitized refusal/success bookkeeping. It cannot
// mint a public proof or export private counts, keys, rows, or reason strings.
func (r *expiryHostRun) observeS1Refusal(parent context.Context, acknowledgements []journalv2.AcknowledgedPublicationV2) (resultErr error) {
	if r == nil || r.lease == nil {
		return retainedRecordsError(parent, nil)
	}
	work, err := r.admitOperation(parent)
	if err != nil {
		return r.fail(retainedRecordsError(parent, err))
	}
	defer work.complete() // registered FIRST: session.Close, unlock, all joins/results precede it
	if !r.operationMu.TryLock() {
		return r.fail(retainedRecordsError(parent, nil))
	}
	var cancel context.CancelFunc
	var watcherDone, callbackDone chan struct{}
	var stopRun func() bool
	defer func() {
		// All local joins are OUTSIDE operation/Host/lifecycle/gate/lease locks.
		// Intentional teardown cancellation cannot retroactively change resultErr.
		if resultErr != nil {
			r.invalid.Store(true)
		}
		r.operationMu.Unlock()
		if resultErr != nil && r.cancel != nil {
			r.cancel()
		}
		if cancel != nil {
			cancel()
		}
		if stopRun != nil && !stopRun() {
			<-callbackDone
		}
		if watcherDone != nil {
			<-watcherDone
		}
		if resultErr != nil {
			resultErr = r.fail(retainedRecordsError(parent, resultErr))
		}
	}()
	if parent == nil || parent.Err() != nil {
		return retainedRecordsError(parent, nil)
	}
	// Done is error-only cancellation notification, never liveness authority.
	// A nil/zero/already-canceled gate refuses synchronously BEFORE observer
	// work, RPC, or hashing; do not depend on eventual watcher scheduling.
	gateDone := r.gate.Done()
	if gateDone == nil {
		return retainedRecordsError(parent, nil)
	}
	select {
	case <-gateDone:
		return retainedRecordsError(parent, nil)
	default:
	}
	if err := r.checkCurrent(parent); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	// Preserve independent run/caller propagation, NEVER PrivateContext.
	callbackDone = make(chan struct{})
	stopRun = context.AfterFunc(r.ctx, func() { defer close(callbackDone); cancel() })
	watcherDone = make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-gateDone:
			cancel()
		case <-ctx.Done():
		}
	}()
	if err := r.checkCurrent(ctx); err != nil {
		return err
	}
	r.lease.mu.Lock()
	repeated := r.negativeClosed || r.released
	r.lease.mu.Unlock()
	if repeated {
		return expiryFailure("expiry_negative_repeated")
	}
	attribution, ok := r.host.retainedCapture(r.composition)
	if !ok {
		return retainedRecordsError(ctx, nil)
	}
	if err := r.checkHeld(ctx, attribution); err != nil {
		return err
	}
	captured, err := journalv2.NewStoreRevisionV2(attribution.DatabaseID, attribution.Revision, attribution.DurableThrough)
	if err != nil {
		return err
	}
	// Prepare freezes opaque actual ACK values before its first context call.
	session, err := r.composition.adapter.PrepareRetainedObservationV2(ctx, captured, acknowledgements)
	if err != nil {
		return err
	}
	defer session.Close()
	// LOCAL canonical pointer, before S0. Never copy into a second finalizer.
	if err := session.ArmExpiryQualificationRefusal(); err != nil {
		return err
	}
	if err := session.ReadPublications(); err != nil {
		return err
	}
	privateContext, err := session.PrivateContext()
	if err != nil {
		return err
	}
	private, err := observeRetainedPrivateRecords(privateContext, r.composition.adapter, r.composition.binding, r.composition.epoch, captured, defaultRetainedPrivatePolicy())
	if err != nil {
		return err
	}
	pubs, refusal := session.Finish(uint64(len(private.rows)))
	if pubs != 0 || refusal == nil {
		return retainedRecordsError(ctx, nil)
	}
	// No string/reason/countMismatch inference. S1 mismatch+S2+terminal is
	// authenticated ONLY by the frozen infrastructure's canonical Consume.
	closing, ok := r.host.retainedCapture(r.composition)
	if !ok || closing != attribution {
		return retainedRecordsError(ctx, nil)
	}
	if err := session.CheckLifetime(); err != nil {
		return err
	}
	closing, ok = r.host.retainedCapture(r.composition)
	if !ok || closing != attribution || parent.Err() != nil || ctx.Err() != nil || r.ctx.Err() != nil {
		return retainedRecordsError(parent, ctx.Err())
	}
	if err := r.checkHeld(ctx, attribution); err != nil {
		return err
	}
	if err := session.ConsumeExpiryQualificationRefusal(); err != nil {
		return err
	}
	// Consume canceled PrivateContext/session by design. No inner ctx liveness,
	// renewed hash, RPC, retry, or replacement session beyond this point.
	if parent.Err() != nil || ctx.Err() != nil || r.ctx.Err() != nil {
		return retainedRecordsError(parent, ctx.Err())
	}
	if err := r.checkHeld(ctx, attribution); err != nil {
		return err
	}
	r.lease.mu.Lock()
	defer r.lease.mu.Unlock()
	if r.lease.current != r || r.lease.phase != expiryLeaseRunning || r.lease.control != nil || r.window.retired || r.lease.failed || r.invalid.Load() ||
		parent.Err() != nil || ctx.Err() != nil || r.ctx.Err() != nil {
		return retainedRecordsError(parent, ctx.Err())
	}
	// Error-only scalar cutoff at the actual bit publication, not a future
	// snapshot/liveness guarantee. Deferred failure/cancellation runs unlocked.
	if err := r.gate.RecordedCleanupError(); err != nil {
		return err
	}
	// Original gate cancellation is a separate cooperative scalar cutoff.
	// Its watcher need not have propagated cancellation to ctx yet.
	if !expiryOriginalDoneOpen(r) {
		return retainedRecordsError(parent, ctx.Err())
	}
	r.negativeClosed = true
	return nil
}

func (r *expiryHostRun) release(ctx context.Context) (resultErr error) {
	if r == nil || r.lease == nil {
		return expiryFailure("expiry_generation")
	}
	work, err := r.admitOperation(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer work.complete()
	if !r.operationMu.TryLock() {
		return r.fail(expiryFailure("expiry_overlap"))
	}
	defer func() {
		r.operationMu.Unlock()
		if resultErr != nil {
			resultErr = r.fail(resultErr)
		}
	}()
	if err := r.checkCurrent(ctx); err != nil {
		return err
	}
	r.lease.mu.Lock()
	ready := r.negativeClosed && !r.released
	r.lease.mu.Unlock()
	if !ready {
		return expiryFailure("expiry_release_order")
	}
	if err := r.gate.Release(ctx); err != nil {
		return err
	}
	if err := r.checkCurrent(ctx); err != nil {
		return err
	}
	r.lease.mu.Lock()
	defer r.lease.mu.Unlock()
	if !r.currentLocked(ctx) {
		return expiryFailure("expiry_release_generation")
	}
	if err := r.gate.RecordedCleanupError(); err != nil {
		return err
	}
	r.released = true
	return nil
}

// This bookkeeping can be set only after an attributed post-A real durable
// revision advance and a FRESH public matched proof. It is not a proof API.
func (r *expiryHostRun) observeMatchedAfterExpiry(ctx context.Context, acknowledgements []journalv2.AcknowledgedPublicationV2, insertedRevision uint64) (resultErr error) {
	if r == nil || r.lease == nil {
		return expiryFailure("expiry_generation")
	}
	work, err := r.admitOperation(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer work.complete()
	if !r.operationMu.TryLock() {
		return r.fail(expiryFailure("expiry_overlap"))
	}
	defer func() {
		r.operationMu.Unlock()
		if resultErr != nil {
			resultErr = r.fail(resultErr)
		}
	}()
	if err := r.checkCurrent(ctx); err != nil {
		return err
	}
	r.lease.mu.Lock()
	ready := r.negativeClosed && r.released && !r.matchedAfterExpiry
	r.lease.mu.Unlock()
	if !ready || insertedRevision == 0 {
		return expiryFailure("expiry_matched_order")
	}
	inspection, err := r.owner.adapter.Inspect(ctx)
	p := r.owner.lifecycle.Provenance()
	if err != nil || !inspection.Healthy || inspection.Lifecycle != "ready" || inspection.DatabaseID != string(r.composition.database) ||
		inspection.OwnerPID != p.Process.PID || inspection.OwnerStartedAt != p.Observation.Status.OwnerStartedAt ||
		inspection.CurrentRevision <= insertedRevision || inspection.DurableThrough != inspection.CurrentRevision {
		return expiryFailure("expiry_revision_advance")
	}
	proof, err := r.host.VerifyRetainedRecords(ctx, acknowledgements)
	if err != nil {
		return err
	}
	if proof.Attribution().Revision != inspection.CurrentRevision {
		return expiryFailure("expiry_matched_revision")
	}
	if err := r.checkCurrent(ctx); err != nil {
		return err
	}
	r.lease.mu.Lock()
	defer r.lease.mu.Unlock()
	if !r.currentLocked(ctx) {
		return expiryFailure("expiry_matched_generation")
	}
	if err := r.gate.RecordedCleanupError(); err != nil {
		return err
	}
	r.matchedAfterExpiry, r.expiryRevision = true, inspection.CurrentRevision
	return nil
}
