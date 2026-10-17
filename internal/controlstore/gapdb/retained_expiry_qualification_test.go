//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"context"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// All fixtures here use the existing SIMULATED Unix endpoint. No Host,
// admission, actual provider expiry, whole memory or performance is proved.
func retainedExpiryPrepared(t *testing.T, acks []journalv2.AcknowledgedPublicationV2, change func(*gapdbapi.StatusResult, int), read func(*gapdbapi.Record)) (*Adapter, *RetainedObservationV2, *atomic.Int32) {
	t.Helper()
	a, captured, accepted := retainedObservationFixture(t, acks, change, read)
	s, err := a.PrepareRetainedObservationV2(t.Context(), captured, acks)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return a, &s, accepted
}
func retainedExpiryPrivate(t *testing.T, a *Adapter, s *RetainedObservationV2) context.Context {
	t.Helper()
	ctx, err := s.PrivateContext()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := a.Get(ctx, "simulated-expiry-private-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	return ctx
}
func retainedExpiryMismatch(status *gapdbapi.StatusResult, n int) {
	if n == 2 {
		status.RecordCount++
	}
}
func retainedExpiryToTerminal(t *testing.T, a *Adapter, s *RetainedObservationV2) context.Context {
	t.Helper()
	if err := s.ArmExpiryQualificationRefusal(); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadPublications(); err != nil {
		t.Fatal(err)
	}
	ctx := retainedExpiryPrivate(t, a, s)
	if pubs, err := s.Finish(6); pubs != 0 || err != errRetainedObservation || s.state.invalid.Load() || !s.state.expiry.s2Closed {
		t.Fatal("S1 negative did not close S2 while remaining live", pubs, err)
	}
	if err := s.CheckLifetime(); err != nil {
		t.Fatal(err)
	}
	return ctx
}
func retainedExpiryCannotConsume(t *testing.T, s *RetainedObservationV2, accepted *atomic.Int32) {
	t.Helper()
	before := accepted.Load()
	if err := s.ConsumeExpiryQualificationRefusal(); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("invalid negative credited/leaked", err)
	}
	if !s.state.invalid.Load() || s.state.ctx.Err() == nil || accepted.Load() != before {
		t.Fatal("failed consumption renewed or failed to invalidate")
	}
	retainedNegativeConsumed(t, *s)
}
func TestRetainedExpiryQualificationClosedNegative(t *testing.T) {
	for _, publications := range []bool{false, true} {
		for _, delta := range []int{-1, 1} {
			for _, decoy := range []int{0, math.MaxInt} {
				name := "private_only"
				if publications {
					name = "publications"
				}
				name += "/" + map[int]string{-1: "missing", 1: "extra"}[delta]
				if decoy == 0 {
					name += "/zero_outer"
				} else {
					name += "/max_outer"
				}
				t.Run(name, func(t *testing.T) {
					var acks []journalv2.AcknowledgedPublicationV2
					if publications {
						acks = []journalv2.AcknowledgedPublicationV2{retainedACKV2(t, "expiry-tag-simulation", 7)}
					}
					a, s, accepted := retainedExpiryPrepared(t, acks, func(status *gapdbapi.StatusResult, n int) {
						if n == 2 {
							status.RecordCount += delta
						} else {
							status.RecordCount = decoy
						}
					}, nil)
					private := retainedExpiryToTerminal(t, a, s)
					b := s.state.budget
					want := uint64(len(s.state.expected)) + 6 + 3
					if b.attempts != want || uint64(accepted.Load()) != want || !b.terminal || b.hashed != b.image*(want*3+1) || private.Err() != nil {
						t.Fatal("RPC/hash/terminal or live-private closure missing")
					}
					hashed := b.hashed
					if err := s.ConsumeExpiryQualificationRefusal(); err != nil {
						t.Fatal(err)
					}
					if private.Err() != context.Canceled || t.Context().Err() != nil || !s.state.expiry.consumed || b.hashed != hashed || b.attempts != want || accepted.Load() != int32(want) {
						t.Fatal("consumption failed closure, renewed work or canceled outer context")
					}
					retainedExpiryCannotConsume(t, s, accepted)
				})
			}
		}
	}
	if unsafe.Sizeof(retainedObservationState{}) != 152 || unsafe.Sizeof(retainedExpiryObservationState{}) != 16 {
		t.Fatal("tagged layout changed; ordinary layout cannot substitute")
	}
}
func TestRetainedExpiryQualificationCoordinateMatrix(t *testing.T) {
	faults := []struct {
		name   string
		mutate func(*gapdbapi.StatusResult)
	}{
		{"lifecycle", func(s *gapdbapi.StatusResult) { s.Lifecycle = gapdbapi.LifecycleDraining }},
		{"database", func(s *gapdbapi.StatusResult) { s.DatabaseID = "SECRET-db" }},
		{"pid", func(s *gapdbapi.StatusResult) { s.OwnerPID++ }},
		{"start", func(s *gapdbapi.StatusResult) { s.OwnerStartedAt = "2026-01-02T00:00:00Z" }},
		{"revision_low", func(s *gapdbapi.StatusResult) { s.CurrentRevision-- }},
		{"revision_high", func(s *gapdbapi.StatusResult) { s.CurrentRevision++ }},
		{"durable_low", func(s *gapdbapi.StatusResult) { s.DurableThroughRevision-- }},
		{"durable_high", func(s *gapdbapi.StatusResult) { s.DurableThroughRevision++ }},
	}
	for phase := 1; phase <= 3; phase++ {
		for _, fault := range faults {
			t.Run("S"+string(rune('0'+phase-1))+"/"+fault.name, func(t *testing.T) {
				a, s, accepted := retainedExpiryPrepared(t, nil, func(status *gapdbapi.StatusResult, n int) {
					retainedExpiryMismatch(status, n)
					if n == phase {
						fault.mutate(status)
					}
				}, nil)
				if err := s.ArmExpiryQualificationRefusal(); err != nil {
					t.Fatal(err)
				}
				err := s.ReadPublications()
				if phase > 1 {
					if err != nil {
						t.Fatal(err)
					}
					retainedExpiryPrivate(t, a, s)
					var pubs uint64
					pubs, err = s.Finish(6)
					if pubs != 0 {
						t.Fatal("partial positive count")
					}
				}
				want := int32(phase)
				if phase > 1 {
					want += 6
				}
				if err == nil || accepted.Load() != want || s.state.expiry.s2Closed || s.state.budget.terminal {
					t.Fatal("bad coordinate became closed negative", err, accepted.Load(), want)
				}
				retainedExpiryCannotConsume(t, s, accepted)
			})
		}
	}
}
func TestRetainedExpiryQualificationStateRefusals(t *testing.T) {
	for _, mode := range []string{"equality", "consume_first", "duplicate_arm", "late_arm", "no_terminal", "closed", "canceled", "revoked_before_terminal", "revoked_after_terminal", "lifetime_closed_after_terminal", "canceled_after_terminal", "closed_after_terminal", "budget_exhausted", "missing_terminal_flag", "wrong_pointer", "repeated_terminal", "private_cap"} {
		t.Run(mode, func(t *testing.T) {
			change := retainedExpiryMismatch
			if mode == "equality" {
				change = nil
			}
			a, s, accepted := retainedExpiryPrepared(t, nil, change, nil)
			if mode == "consume_first" {
				retainedExpiryCannotConsume(t, s, accepted)
				return
			}
			if mode == "late_arm" {
				if err := s.ReadPublications(); err != nil {
					t.Fatal(err)
				}
				if s.ArmExpiryQualificationRefusal() == nil {
					t.Fatal("late arm")
				}
				retainedExpiryCannotConsume(t, s, accepted)
				return
			}
			if err := s.ArmExpiryQualificationRefusal(); err != nil {
				t.Fatal(err)
			}
			if mode == "duplicate_arm" {
				if s.ArmExpiryQualificationRefusal() == nil {
					t.Fatal("duplicate arm")
				}
				retainedExpiryCannotConsume(t, s, accepted)
				return
			}
			if err := s.ReadPublications(); err != nil {
				t.Fatal(err)
			}
			retainedExpiryPrivate(t, a, s)
			private := uint64(6)
			if mode == "private_cap" {
				private = retainedObservationPrivateRows + 1
			}
			if pubs, err := s.Finish(private); pubs != 0 || err == nil {
				t.Fatal("armed positive proof", pubs, err)
			}
			if mode == "equality" || mode == "private_cap" || mode == "no_terminal" {
				retainedExpiryCannotConsume(t, s, accepted)
				return
			}
			switch mode {
			case "closed":
				s.Close()
			case "canceled":
				s.state.cancel()
			case "revoked_before_terminal":
				a.lifetime.Revoke()
			case "budget_exhausted":
				s.state.budget.mu.Lock()
				s.state.budget.hashed = retainedObservationHashes
				s.state.budget.mu.Unlock()
			}
			err := s.CheckLifetime()
			if mode == "closed" || mode == "canceled" || mode == "revoked_before_terminal" || mode == "budget_exhausted" {
				if err == nil {
					t.Fatal("invalid terminal accepted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "revoked_after_terminal":
					a.lifetime.Revoke()
				case "canceled_after_terminal":
					s.state.cancel()
				case "closed_after_terminal":
					s.Close()
				case "lifetime_closed_after_terminal":
					if err := a.lifetime.Close(); err != nil {
						t.Fatal(err)
					}
				case "missing_terminal_flag":
					s.state.budget.mu.Lock()
					s.state.budget.terminal = false
					s.state.budget.mu.Unlock()
				case "wrong_pointer":
					copy := *s
					if copy.ConsumeExpiryQualificationRefusal() == nil {
						t.Fatal("copied finalizer")
					}
				case "repeated_terminal":
					if s.CheckLifetime() == nil {
						t.Fatal("repeated terminal")
					}
				}
			}
			retainedExpiryCannotConsume(t, s, accepted)
		})
	}
	var zero RetainedObservationV2
	var nilHandle *RetainedObservationV2
	for _, s := range []*RetainedObservationV2{&zero, nilHandle} {
		if s.ArmExpiryQualificationRefusal() == nil || s.ConsumeExpiryQualificationRefusal() == nil {
			t.Fatal("zero/nil handle")
		}
	}
}
func TestRetainedExpiryQualificationExactReadRefusals(t *testing.T) {
	for _, mode := range []string{"value", "key", "revision", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			acks := []journalv2.AcknowledgedPublicationV2{retainedACKV2(t, "expiry-read-negative", 7)}
			_, s, accepted := retainedExpiryPrepared(t, acks, retainedExpiryMismatch, func(r *gapdbapi.Record) {
				switch mode {
				case "value":
					r.Value = []byte(`{"SECRET":"wrong"}`)
				case "key":
					r.Key = "SECRET-wrong"
				case "revision":
					r.Revision++
				case "expiry":
					x := time.Unix(1, 0)
					r.ExpiresAt = &x
				}
			})
			if err := s.ArmExpiryQualificationRefusal(); err != nil {
				t.Fatal(err)
			}
			if s.ReadPublications() == nil || s.state.expiry.s1Mismatch {
				t.Fatal("bad read became S1")
			}
			retainedExpiryCannotConsume(t, s, accepted)
		})
	}
}
func TestRetainedExpiryQualificationMutexOverlap(t *testing.T) {
	for _, mode := range []string{"arm", "consume"} {
		t.Run(mode, func(t *testing.T) {
			a, s, accepted := retainedExpiryPrepared(t, nil, retainedExpiryMismatch, nil)
			if mode == "consume" {
				retainedExpiryToTerminal(t, a, s)
			}
			s.state.mu.Lock()
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var err error
					if mode == "arm" {
						err = s.ArmExpiryQualificationRefusal()
					} else {
						err = s.ConsumeExpiryQualificationRefusal()
					}
					if err == nil {
						t.Error("overlapping method credited")
					}
				}()
			}
			wg.Wait()
			s.state.mu.Unlock()
			retainedExpiryCannotConsume(t, s, accepted)
		})
	}
}
func TestRetainedExpiryQualificationOverlappingFinish(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a, s, accepted := retainedExpiryPrepared(t, nil, func(status *gapdbapi.StatusResult, n int) {
		retainedExpiryMismatch(status, n)
		if n == 3 {
			close(entered)
			<-release
		}
	}, nil)
	if err := s.ArmExpiryQualificationRefusal(); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadPublications(); err != nil {
		t.Fatal(err)
	}
	retainedExpiryPrivate(t, a, s)
	done := make(chan error, 1)
	go func() { _, err := s.Finish(6); done <- err }()
	<-entered // Finish really holds mu and is inside the actual S2 exchange.
	err := s.ConsumeExpiryQualificationRefusal()
	close(release)
	if err == nil || <-done == nil || !s.state.invalid.Load() {
		t.Fatal("overlap credited")
	}
	retainedExpiryCannotConsume(t, s, accepted)
}
