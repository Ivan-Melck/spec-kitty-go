package gapdb

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Development refusal ceilings, NOT certified peak-heap or hard-wall limits.
const (
	retainedObservationDuration           = 30 * time.Second
	retainedObservationCalls       uint64 = 4096 + 195 + 3
	retainedObservationFrames      uint64 = 64 << 20
	retainedObservationHashes      uint64 = 32 << 30
	retainedObservationPrivateRows uint64 = 195
)

var errRetainedObservation = errors.New("retained observation refused")

type retainedObservationBudgetKey struct{}
type retainedObservationBudget struct {
	mu                                       sync.Mutex
	transport                                *retainedTransport
	planned, attempts, frames, image, hashed uint64
	terminal                                 bool
}

// All planned RPCs are charged THREE complete executable hashes, plus a
// separately reserved terminal hash. No hash cache and no optimistic unique-row
// estimate. Checked division/subtraction also protects structural uint64 probes.
func retainedObservationWork(image, records uint64) (uint64, bool) {
	if image == 0 || records > retainedRecordLimitV2 {
		return 0, false
	}
	planned := records + retainedObservationPrivateRows + 3
	if planned > retainedObservationCalls {
		return 0, false
	}
	hashes := planned*3 + 1 // bounded above by 12883 before multiplication
	if image > retainedObservationHashes/hashes {
		return 0, false
	}
	return planned, true
}
func (b *retainedObservationBudget) attempt(t *retainedTransport) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.transport != t || b.attempts >= b.planned || b.attempts >= retainedObservationCalls || b.terminal || b.image == 0 || b.hashed > retainedObservationHashes || b.image > (retainedObservationHashes-b.hashed)/3 {
		return errRetainedObservation
	}
	b.attempts++
	b.hashed += b.image * 3
	return nil
}
func (b *retainedObservationBudget) frame(n uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.frames > retainedObservationFrames || n > retainedObservationFrames-b.frames {
		return errRetainedObservation
	}
	b.frames += n
	return nil
}
func (b *retainedObservationBudget) finishHash() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.terminal || b.hashed > retainedObservationHashes || b.image > retainedObservationHashes-b.hashed {
		return errRetainedObservation
	}
	b.terminal = true
	b.hashed += b.image
	return nil
}
func retainedBudget(ctx context.Context) *retainedObservationBudget {
	if ctx == nil {
		return nil
	}
	b, _ := ctx.Value(retainedObservationBudgetKey{}).(*retainedObservationBudget)
	return b
}
