//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

// Infrastructure only: no Host authority, provider count, admission or live
// expiry witness. Every field is guarded by retainedObservationState.mu.
type retainedExpiryObservationState struct {
	handle                                *RetainedObservationV2
	armed, s1Mismatch, s2Closed, consumed bool
}

func (s *retainedObservationState) noteRetainedExpiryS1Mismatch() {
	if s.expiry.armed {
		s.expiry.s1Mismatch = true
	}
}

func (s *retainedObservationState) finishRetainedObservation(pubs uint64, err error) (uint64, error) {
	if !s.expiry.armed {
		if err := s.end(err); err != nil {
			return 0, err
		}
		return pubs, nil
	}
	// A successful equality, RPC/coordinate failure or historical reason bit
	// cannot become a completed negative. Only the just-authenticated S1 branch.
	if err != errRetainedObservation || !s.expiry.s1Mismatch || s.invalid.Load() || !s.bound() {
		if err == nil {
			err = errRetainedObservation
		}
		return 0, s.end(err)
	}
	err = s.coordinate() // S2 authenticates coordinates, never its count.
	if err == nil {
		s.expiry.s2Closed = true
	}
	if err := s.end(err); err != nil {
		return 0, err
	}
	// Still phase3/live, but NEVER a positive proof or publication return.
	return 0, errRetainedObservation
}

// ArmExpiryQualificationRefusal pins one canonical handle before S0. It grants
// no Host authority; only tagged infrastructure can close a negative sequence.
func (o *RetainedObservationV2) ArmExpiryQualificationRefusal() error {
	if o == nil || o.state == nil {
		return errRetainedObservation
	}
	s := o.state
	if !s.mu.TryLock() {
		s.invalid.Store(true)
		s.cancel()
		return errRetainedObservation
	}
	if s.invalid.Load() || s.phase != 0 || !s.bound() || s.expiry.armed || s.expiry.consumed {
		return s.end(errRetainedObservation)
	}
	s.expiry.handle = o
	s.expiry.armed = true
	return s.end(nil)
}

// ConsumeExpiryQualificationRefusal requires S2 AND the successful reserved
// terminal hash. It returns no counts/keys/proof and cancels the private session.
// A future Host MUST separately close its independent outer/run/gate contexts.
func (o *RetainedObservationV2) ConsumeExpiryQualificationRefusal() error {
	if o == nil || o.state == nil {
		return errRetainedObservation
	}
	s := o.state
	if !s.begin(4) {
		return errRetainedObservation
	}
	if s.expiry.handle != o || !s.expiry.armed || !s.expiry.s1Mismatch || !s.expiry.s2Closed || s.expiry.consumed {
		return s.end(errRetainedObservation)
	}
	s.budget.mu.Lock()
	terminal := s.budget.terminal
	s.budget.mu.Unlock()
	if !terminal {
		return s.end(errRetainedObservation)
	}
	// One bounded pidfd liveness inspection, NOT another image hash or RPC.
	// Detects Revoke/Close completed before this invocation, including after
	// CheckLifetime. A later revocation is NOT an atomic joint-state snapshot.
	if err := s.lifetime.CheckExpiryQualificationLive(s.ctx); err != nil {
		return s.end(err)
	}
	if !s.bound() || !s.invalid.CompareAndSwap(false, true) {
		return s.end(errRetainedObservation)
	}
	// CAS linearizes observation consumption while mu remains held: no
	// end(nil)-then-Close gap. Overlap invalidating before CAS defeats credit.
	s.expiry.consumed = true
	s.cancel()
	s.mu.Unlock()
	return nil
}
