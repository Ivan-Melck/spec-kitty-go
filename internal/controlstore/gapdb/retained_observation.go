package gapdb

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
)

// RetainedObservationV2 is bounded infrastructure, NOT Host authority or an
// admission proof. Copies share a single consuming phase. No keys, values,
// provider counts, identities, selectors, clients, or callbacks escape.
type RetainedObservationV2 struct{ state *retainedObservationState }
type retainedObservationState struct {
	// Disabled state is empty and FIRST, preserving ordinary offsets/size.
	expiry  retainedExpiryObservationState
	mu      sync.Mutex
	invalid atomic.Bool
	phase   uint8
	// Private refusal classification: set ONLY after an authenticated S1 has
	// passed every coordinate check. No actual count or provider data escapes.
	countMismatch bool
	adapter       *Adapter
	provider      *ownedProvider
	transport     *retainedTransport
	lifetime      *retainedkernel.Lifetime
	captured      journalv2.StoreRevisionV2
	expected      retainedPublicationMapV2
	ctx           context.Context
	cancel        context.CancelFunc
	budget        *retainedObservationBudget
}

// PrepareRetainedObservationV2 performs ALL scalar preflight before any record
// clones or key encoding. Only owned fixed transport is supported. An ordinary
// Dial or infrastructure-shaped adapter cannot by itself earn a Host proof.
func (a *Adapter) PrepareRetainedObservationV2(parent context.Context, captured journalv2.StoreRevisionV2, acknowledgements []journalv2.AcknowledgedPublicationV2) (RetainedObservationV2, error) {
	if parent == nil || len(acknowledgements) > retainedPublicationLimitV2 || a == nil {
		return RetainedObservationV2{}, errRetainedObservation
	}
	// No context method may run until the bounded opaque ACK values are frozen.
	// Sealed internals are immutable: no variable-sized payload is cloned here.
	var snapshot [retainedPublicationLimitV2]journalv2.AcknowledgedPublicationV2
	copy(snapshot[:], acknowledgements)
	acknowledgements = snapshot[:len(acknowledgements)]
	if parent.Err() != nil {
		return RetainedObservationV2{}, errRetainedObservation
	}
	p, ok := a.client.(*ownedProvider)
	if !ok || p == nil || p.transport == nil || a.lifetime == nil || p.transport.lifetime != a.lifetime {
		return RetainedObservationV2{}, errRetainedObservation
	}
	t := p.transport
	if _, err := journalv2.NewStoreRevisionV2(captured.DatabaseID(), captured.CurrentRevision(), captured.DurableThroughRevision()); err != nil || string(captured.DatabaseID()) != t.database || captured.DurableThroughRevision() != captured.CurrentRevision() {
		return RetainedObservationV2{}, errRetainedObservation
	}
	order, measured, err := preflightRetainedPublicationsV2(parent, acknowledgements)
	if err != nil {
		return RetainedObservationV2{}, errRetainedObservation
	}
	for _, ack := range acknowledgements {
		if ack.ProviderRevision() > captured.CurrentRevision() || ack.DurableThroughRevision() > captured.DurableThroughRevision() {
			return RetainedObservationV2{}, errRetainedObservation
		}
	}
	image := a.lifetime.ImageSize()
	planned, valid := retainedObservationWork(image, measured.records)
	if !valid || parent.Err() != nil {
		return RetainedObservationV2{}, errRetainedObservation
	}
	ctx, cancel := context.WithTimeout(parent, retainedObservationDuration)
	b := &retainedObservationBudget{transport: t, planned: planned, image: image}
	ctx = context.WithValue(ctx, retainedObservationBudgetKey{}, b)
	expected, err := copyRetainedPublicationMapV2(ctx, acknowledgements, &order, measured)
	if err != nil {
		cancel()
		return RetainedObservationV2{}, errRetainedObservation
	}
	return RetainedObservationV2{&retainedObservationState{adapter: a, provider: p, transport: t, lifetime: a.lifetime, captured: captured, expected: expected, ctx: ctx, cancel: cancel, budget: b}}, nil
}

// begin consumes a phase before doing work. Concurrent, repeated, copied,
// out-of-order and failed uses invalidate all handles, including the active one.
func (s *retainedObservationState) begin(phase uint8) bool {
	if s == nil {
		return false
	}
	if !s.mu.TryLock() {
		s.invalid.Store(true)
		s.cancel()
		return false
	}
	if s.invalid.Load() || s.phase != phase || !s.bound() {
		s.invalid.Store(true)
		s.cancel()
		s.mu.Unlock()
		return false
	}
	s.phase++
	return true
}
func (s *retainedObservationState) bound() bool {
	return s.ctx.Err() == nil && s.adapter != nil && s.adapter.client == s.provider && s.adapter.lifetime == s.lifetime && s.provider.transport == s.transport && s.transport.lifetime == s.lifetime && s.transport.database == string(s.captured.DatabaseID()) && retainedBudget(s.ctx) == s.budget
}
func (s *retainedObservationState) end(err error) error {
	defer s.mu.Unlock()
	if err != nil || s.invalid.Load() || !s.bound() {
		if s.ctx.Err() != nil {
			err = s.ctx.Err()
		}
		s.invalid.Store(true)
		s.cancel()
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return errRetainedObservation
	}
	return nil
}
func (s *retainedObservationState) coordinate() error {
	status, err := s.transport.status(s.ctx)
	if err != nil {
		return err
	}
	if status.Lifecycle != gapdbapi.LifecycleReady || status.DatabaseID != string(s.captured.DatabaseID()) || status.OwnerPID != s.transport.ownerPID || status.OwnerStartedAt != s.transport.ownerStarted || uint64(status.CurrentRevision) != s.captured.CurrentRevision() || uint64(status.DurableThroughRevision) < s.captured.CurrentRevision() || status.DurableThroughRevision > status.CurrentRevision {
		return errRetainedObservation
	}
	return nil
}

// ReadPublications observes S0 then independently exact-reads every final row.
func (o RetainedObservationV2) ReadPublications() error {
	s := o.state
	if !s.begin(0) {
		return errRetainedObservation
	}
	err := s.coordinate()
	if err == nil {
		for _, expected := range s.expected {
			key := expected.key
			if s.ctx.Err() != nil {
				err = errRetainedObservation
				break
			}
			record, readErr := s.transport.get(s.ctx, key)
			if readErr != nil {
				err = readErr
				break
			}
			if record.Key != key || uint64(record.Revision) != expected.revision || record.ExpiresAt != nil || !bytes.Equal(record.Value, expected.canonical) {
				err = errRetainedObservation
				break
			}
		}
	}
	return s.end(err)
}

// PrivateContext supplies only observation-scoped budget bookkeeping to the
// EXISTING owner-private helper. It is not a key/client/authority port. Methods
// retain their own derived context and cannot be switched to an unbudgeted one.
func (o RetainedObservationV2) PrivateContext() (context.Context, error) {
	s := o.state
	if !s.begin(1) {
		return nil, errRetainedObservation
	}
	if err := s.end(nil); err != nil {
		return nil, err
	}
	return s.ctx, nil
}

// Finish accepts only actual visited-private cardinality from the owner helper.
// ONLY S1 count is compared. S0/S2 counts never earn closure. The return is known
// matched-publication bookkeeping, not an actual provider count or Host proof.
func (o RetainedObservationV2) Finish(privateCount uint64) (uint64, error) {
	s := o.state
	if !s.begin(2) {
		return 0, errRetainedObservation
	}
	var err error
	pubs := uint64(len(s.expected))
	if privateCount > retainedObservationPrivateRows || pubs > ^uint64(0)-privateCount {
		err = errRetainedObservation
	}
	if err == nil {
		status, readErr := s.transport.status(s.ctx)
		if readErr != nil {
			err = readErr
		} else if status.Lifecycle != gapdbapi.LifecycleReady || status.DatabaseID != string(s.captured.DatabaseID()) || status.OwnerPID != s.transport.ownerPID || status.OwnerStartedAt != s.transport.ownerStarted || uint64(status.CurrentRevision) != s.captured.CurrentRevision() || uint64(status.DurableThroughRevision) < s.captured.CurrentRevision() || status.DurableThroughRevision > status.CurrentRevision {
			err = errRetainedObservation
		} else if uint64(status.RecordCount) != pubs+privateCount {
			s.countMismatch = true
			s.noteRetainedExpiryS1Mismatch()
			err = errRetainedObservation
		}
	}
	if err == nil {
		err = s.coordinate()
	} // closing S2, ignores its count
	return s.finishRetainedObservation(pubs, err)
}

// CheckLifetime consumes the separately reserved terminal full hash AFTER the
// Host/projection close. It grants neither renewal nor context-free retry.
func (o RetainedObservationV2) CheckLifetime() error {
	s := o.state
	if !s.begin(3) {
		return errRetainedObservation
	}
	err := s.budget.finishHash()
	if err == nil {
		err = s.lifetime.Check(s.ctx)
	}
	return s.end(err)
}

// Close irreversibly invalidates all copies and releases cooperative timers.
func (o RetainedObservationV2) Close() {
	if s := o.state; s != nil {
		s.invalid.Store(true)
		s.cancel()
	}
}

// Closed role-to-codec partition. parseV2Key alone also admits owner-private
// authority/runtime records, and therefore is insufficient for publication credit.
func retainedPublicationNamespaceV2(key v2Key, role journalv2.SealedRecordRole) bool {
	if strings.HasPrefix(key.String(), "spk/runtime-admission/") {
		return false
	}
	switch role {
	case journalv2.RecordOperationBinding:
		return key.Kind() == v2KeyOperationBinding
	case journalv2.RecordOperationHead:
		return key.Kind() == v2KeyOperationHead
	case journalv2.RecordJournalEvent:
		return key.Kind() == v2KeyJournalEvent
	case journalv2.RecordAggregateHead:
		return key.Kind() == v2KeyAggregateHead
	case journalv2.RecordEffectIntent:
		return key.Kind() == v2KeyEffectIntent
	case journalv2.RecordEligibilityGuard:
		return key.Kind() == v2KeyEligibilityGuard
	case journalv2.RecordCheckpointRecord:
		return key.Kind() == v2KeyCheckpointRecord
	case journalv2.RecordCheckpointSlot:
		return key.Kind() == v2KeyCheckpointSlot
	case journalv2.RecordPublicationWitness:
		return key.Kind() == v2KeyPublicationWitness
	case journalv2.RecordWorkspacePathClaim:
		return key.Kind() == v2KeyWorkspacePathClaim
	default:
		return false
	}
}
