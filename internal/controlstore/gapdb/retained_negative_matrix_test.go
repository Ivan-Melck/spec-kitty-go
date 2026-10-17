package gapdb

import (
	"context"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// These are real Unix exchanges with the existing simulated, same-process
// endpoint. They establish adapter phase/guard behavior, NOT genuine Host,
// provider admission, hidden-expiry or performance qualification.
func retainedNegativePrivateReads(t *testing.T, a *Adapter, s RetainedObservationV2) {
	t.Helper()
	ctx, err := s.PrivateContext()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := a.Get(ctx, "simulation-private-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
}

func retainedNegativeConsumed(t *testing.T, s RetainedObservationV2) {
	t.Helper()
	if !s.state.invalid.Load() {
		t.Fatal("failed observation was not invalidated")
	}
	attempts := s.state.budget.attempts
	copy := s
	if copy.ReadPublications() == nil {
		t.Fatal("failed copy renewed S0")
	}
	if ctx, err := copy.PrivateContext(); ctx != nil || err == nil {
		t.Fatal("failed copy renewed private phase")
	}
	if count, err := copy.Finish(6); count != 0 || err == nil {
		t.Fatal("failed copy returned partial count")
	}
	if copy.CheckLifetime() == nil || s.state.budget.attempts != attempts {
		t.Fatal("failed copy performed terminal work")
	}
	copy.Close()
	copy.Close()
}

func TestRetainedNegativeCoordinatePhaseMatrix(t *testing.T) {
	faults := []struct {
		name   string
		change func(*gapdbapi.StatusResult)
	}{
		{"lifecycle", func(s *gapdbapi.StatusResult) { s.Lifecycle = gapdbapi.LifecycleDraining }},
		{"database", func(s *gapdbapi.StatusResult) { s.DatabaseID = "SECRET-substituted-db" }},
		{"owner_pid", func(s *gapdbapi.StatusResult) { s.OwnerPID++ }},
		{"owner_start", func(s *gapdbapi.StatusResult) { s.OwnerStartedAt = "2026-01-02T00:00:00Z" }},
		{"revision_low", func(s *gapdbapi.StatusResult) { s.CurrentRevision-- }},
		{"revision_high", func(s *gapdbapi.StatusResult) { s.CurrentRevision++ }},
		{"durable_low", func(s *gapdbapi.StatusResult) { s.DurableThroughRevision-- }},
		{"durable_ahead", func(s *gapdbapi.StatusResult) { s.DurableThroughRevision++ }},
	}
	for phase := 1; phase <= 3; phase++ {
		for _, fault := range faults {
			for _, countDecoy := range []bool{false, true} {
				name := "S" + string(rune('0'+phase-1)) + "/" + fault.name
				if countDecoy {
					name += "/with_bad_count"
				}
				t.Run(name, func(t *testing.T) {
					a, captured, accepted := retainedObservationFixture(t, nil, func(s *gapdbapi.StatusResult, n int) {
						if n == phase {
							fault.change(s)
							if countDecoy {
								s.RecordCount++
							}
						}
					}, nil)
					s, err := a.PrepareRetainedObservationV2(t.Context(), captured, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					err = s.ReadPublications()
					if phase > 1 {
						if err != nil {
							t.Fatal("unmutated S0 failed", err)
						}
						retainedNegativePrivateReads(t, a, s)
						var count uint64
						count, err = s.Finish(6)
						if count != 0 {
							t.Fatal("partial matched count escaped")
						}
					}
					if err == nil || strings.Contains(err.Error(), "SECRET") || s.state.countMismatch {
						t.Fatal("bad coordinate accepted, leaked, or credited as count inequality", err)
					}
					wantAttempts := int32(phase)
					if phase > 1 {
						wantAttempts += 6
					}
					if accepted.Load() != wantAttempts || s.state.budget.attempts != uint64(wantAttempts) {
						t.Fatal("fault was not reached at its exact phase", accepted.Load(), wantAttempts)
					}
					retainedNegativeConsumed(t, s)
				})
			}
		}
	}
}

func TestRetainedNegativeOnlyMiddleCountMatrix(t *testing.T) {
	for _, phase := range []int{1, 2, 3, 4} {
		for _, delta := range []int{-1, 1} {
			name := "S" + string(rune('0'+phase-1)) + "/missing"
			if delta > 0 {
				name = "S" + string(rune('0'+phase-1)) + "/extra"
			}
			if phase == 4 {
				name = "S0_and_S2_decoys/zero"
				if delta > 0 {
					name = "S0_and_S2_decoys/max_int"
				}
			}
			t.Run(name, func(t *testing.T) {
				a, captured, accepted := retainedObservationFixture(t, nil, func(s *gapdbapi.StatusResult, n int) {
					if n == phase {
						s.RecordCount += delta
					} else if phase == 4 && n != 2 {
						if delta > 0 {
							s.RecordCount = math.MaxInt
						} else {
							s.RecordCount = 0
						}
					}
				}, nil)
				s, err := a.PrepareRetainedObservationV2(t.Context(), captured, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				if err := s.ReadPublications(); err != nil {
					t.Fatal(err)
				}
				retainedNegativePrivateReads(t, a, s)
				count, err := s.Finish(6)
				if phase == 2 {
					// An authenticated S1 count refusal stops BEFORE S2. This
					// unit reason bit is not a fully closed Host expiry witness.
					if count != 0 || err == nil || !s.state.countMismatch || accepted.Load() != 8 {
						t.Fatal("S1 count refusal not reached/classified", err)
					}
					retainedNegativeConsumed(t, s)
					return
				}
				if count != 0 || err != nil || s.state.countMismatch || accepted.Load() != 9 {
					t.Fatal("S0/S2 count substituted for S1", err)
				}
				if err := s.CheckLifetime(); err != nil || !s.state.budget.terminal {
					t.Fatal("positive unit control did not close terminal hash", err)
				}
			})
		}
	}
}

func TestRetainedNegativeReadFailureCannotBecomeCountWitness(t *testing.T) {
	ack := retainedACKV2(t, "negative-read-before-count", 7)
	rows := retainedCopiedOracleV2(t, []journalv2.AcknowledgedPublicationV2{ack})
	key := ""
	for candidate := range rows {
		if key == "" || candidate < key {
			key = candidate
		}
	}
	for _, fault := range []string{"value", "revision", "key", "expiry"} {
		t.Run(fault, func(t *testing.T) {
			var statuses atomic.Int32
			a, captured, accepted := retainedObservationFixture(t, []journalv2.AcknowledgedPublicationV2{ack}, func(s *gapdbapi.StatusResult, n int) {
				statuses.Store(int32(n))
				if n == 2 {
					s.RecordCount++ // Must remain unreachable after a bad exact read.
				}
			}, func(r *gapdbapi.Record) {
				if r.Key != key {
					return
				}
				switch fault {
				case "value":
					r.Value = []byte(`{"SECRET":"wrong"}`)
				case "revision":
					r.Revision++
				case "key":
					r.Key = "SECRET-wrong-key"
				case "expiry":
					x := time.Unix(1, 0)
					r.ExpiresAt = &x
				}
			})
			s, err := a.PrepareRetainedObservationV2(context.Background(), captured, []journalv2.AcknowledgedPublicationV2{ack})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.ReadPublications(); err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatal("bad exact row accepted/leaked", err)
			}
			if s.state.countMismatch || statuses.Load() != 1 || accepted.Load() < 2 {
				t.Fatal("failed exact read became S1 witness")
			}
			before := accepted.Load()
			retainedNegativeConsumed(t, s)
			if accepted.Load() != before || statuses.Load() != 1 {
				t.Fatal("failure re-entered transport")
			}
		})
	}
}
