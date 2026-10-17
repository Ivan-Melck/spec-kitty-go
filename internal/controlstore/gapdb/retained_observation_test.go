package gapdb

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// Actual Unix identity checks, but a PRIVATE simulated endpoint, NOT an admitted
// provider, historical receipt, live expiry witness, or Host authority factory.
func retainedObservationFixture(t *testing.T, acks []journalv2.AcknowledgedPublicationV2, change func(*gapdbapi.StatusResult, int), readChange func(*gapdbapi.Record)) (*Adapter, journalv2.StoreRevisionV2, *atomic.Int32) {
	t.Helper()
	rows := retainedCopiedOracleV2(t, acks)
	var statuses atomic.Int32
	transport, lifetime, accepted := retainedFixture(t, func(_ *net.UnixConn, request retainedRequest) []byte {
		if request.Operation == "status" {
			s := retainedTestStatus()
			s.CurrentRevision = 100
			s.DurableThroughRevision = 100
			s.RecordCount = len(rows) + 6
			if change != nil {
				change(&s, int(statuses.Add(1)))
			} else {
				statuses.Add(1)
			}
			return retainedSuccess(t, request, s)
		}
		raw, _ := json.Marshal(request.Arguments)
		var args struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(raw, &args)
		r := gapdbapi.Record{Key: args.Key, Value: []byte(`{"private":"simulation"}`), Revision: 1}
		if row, ok := rows[args.Key]; ok {
			r.Value, r.Revision = row.canonical, gapdbapi.Revision(row.revision)
		}
		if readChange != nil {
			readChange(&r)
		}
		return retainedSuccess(t, request, map[string]any{"record": r})
	})
	a, err := newAdapter(&ownedProvider{provider: &fakeProvider{}, transport: transport}, transport.endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a.lifetime = lifetime
	capture, err := journalv2.NewStoreRevisionV2("owned-test-db", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	return a, capture, accepted
}

func TestRetainedObservationAllRolesFinalOverwritesAndPrivateBudget(t *testing.T) {
	port := &retainedFixturePortV2{7}
	coordinator := retainedCoordinatorV2(t, port)
	first := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "observe-chain", nil, 0))
	prior := first.Plan().OperationState()
	port.revision = 29
	second := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "observe-chain", &prior, 7))
	acks := []journalv2.AcknowledgedPublicationV2{second, first}
	// Oracle is forward revision order; reverse caller ordering is preserved.
	forward := []journalv2.AcknowledgedPublicationV2{first, second}
	a, capture, accepted := retainedObservationFixture(t, forward, func(s *gapdbapi.StatusResult, n int) {
		if n != 2 {
			s.RecordCount = math.MaxInt
		}
	}, nil)
	session, err := a.PrepareRetainedObservationV2(t.Context(), capture, acks)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.ReadPublications(); err != nil {
		t.Fatal(err)
	}
	ctx, err := session.PrivateContext()
	if err != nil {
		t.Fatal(err)
	}
	if retainedBudget(ctx) != session.state.budget {
		t.Fatal("private context lost budget")
	}
	// Explicit simulated private rows; high Host uses the unchanged exact helper.
	for i := 0; i < 6; i++ {
		if _, err := a.Get(ctx, "simulated-private-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	pubs, err := session.Finish(6)
	if err != nil || pubs != uint64(len(retainedCopiedOracleV2(t, forward))) {
		t.Fatalf("inexact closure %d %v", pubs, err)
	}
	if err := session.CheckLifetime(); err != nil {
		t.Fatal(err)
	}
	b := session.state.budget
	if b.attempts != pubs+6+3 || uint64(accepted.Load()) != b.attempts || b.hashed != b.image*(b.attempts*3+1) || !b.terminal || b.frames == 0 {
		t.Fatal("private/status/hash/frame calls escaped accounting")
	}
	for _, repeat := range []func() error{session.ReadPublications, func() error { _, err := session.PrivateContext(); return err }, func() error { _, err := session.Finish(6); return err }, session.CheckLifetime} {
		if repeat() == nil {
			t.Fatal("successful session reused")
		}
	}
}

func TestRetainedObservationEmptyPublicationPrivateOnly(t *testing.T) {
	a, capture, _ := retainedObservationFixture(t, nil, nil, nil)
	s, err := a.PrepareRetainedObservationV2(t.Context(), capture, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadPublications(); err != nil {
		t.Fatal(err)
	}
	ctx, err := s.PrivateContext()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := a.Get(ctx, "simulated-private-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := s.Finish(6); err != nil || count != 0 {
		t.Fatal("empty publication closure", err)
	}
	if err := s.CheckLifetime(); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedObservationCoordinateAndMiddleCountRefusals(t *testing.T) {
	ack := retainedACKV2(t, "counts", 7)
	for _, name := range []string{"S0_revision", "S1_revision", "S2_revision", "S0_database", "S1_owner", "S2_started", "S0_state", "S1_durable_low", "S2_durable_ahead", "middle_extra", "middle_missing", "middle_hidden_expired_extra_NOT_live", "S0S2_decoy"} {
		t.Run(name, func(t *testing.T) {
			a, capture, _ := retainedObservationFixture(t, []journalv2.AcknowledgedPublicationV2{ack}, func(s *gapdbapi.StatusResult, n int) {
				switch name {
				case "S0_revision":
					if n == 1 {
						s.CurrentRevision++
					}
				case "S1_revision":
					if n == 2 {
						s.CurrentRevision++
					}
				case "S2_revision":
					if n == 3 {
						s.CurrentRevision++
					}
				case "S0_database":
					if n == 1 {
						s.DatabaseID = "SECRET-db"
					}
				case "S1_owner":
					if n == 2 {
						s.OwnerPID++
					}
				case "S2_started":
					if n == 3 {
						s.OwnerStartedAt = "2026-01-02T00:00:00Z"
					}
				case "S0_state":
					if n == 1 {
						s.Lifecycle = gapdbapi.LifecycleDraining
					}
				case "S1_durable_low":
					if n == 2 {
						s.DurableThroughRevision = 99
					}
				case "S2_durable_ahead":
					if n == 3 {
						s.DurableThroughRevision = 101
					}
				case "middle_extra", "middle_hidden_expired_extra_NOT_live", "S0S2_decoy":
					if n == 2 {
						s.RecordCount++
					}
				case "middle_missing":
					if n == 2 {
						s.RecordCount--
					}
				}
			}, nil)
			session, err := a.PrepareRetainedObservationV2(t.Context(), capture, []journalv2.AcknowledgedPublicationV2{ack})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			err = session.ReadPublications()
			if err == nil {
				_, err = session.PrivateContext()
			}
			var count uint64
			if err == nil {
				count, err = session.Finish(6)
			}
			if err == nil || count != 0 {
				t.Fatal("coordinate/count substitution accepted")
			}
			if session.ReadPublications() == nil {
				t.Fatal("failure reused")
			}
		})
	}
}

func TestRetainedObservationEveryPublicationExactNoCountSubstitution(t *testing.T) {
	ack := retainedACKV2(t, "read-negatives", 7)
	rows := retainedCopiedOracleV2(t, []journalv2.AcknowledgedPublicationV2{ack})
	for key := range rows {
		for _, fault := range []string{"value", "revision", "echo", "expiry", "extra_plus_missing_same_count_NOT_live"} {
			t.Run(key+"/"+fault, func(t *testing.T) {
				a, capture, _ := retainedObservationFixture(t, []journalv2.AcknowledgedPublicationV2{ack}, nil, func(r *gapdbapi.Record) {
					if r.Key != key {
						return
					}
					switch fault {
					case "value":
						r.Value = []byte(`{"SECRET":"wrong"}`)
					case "revision":
						r.Revision++
					case "echo", "extra_plus_missing_same_count_NOT_live":
						r.Key = "SECRET-extra-instead-of-missing"
					case "expiry":
						expiry := time.Unix(1, 0)
						r.ExpiresAt = &expiry
					}
				})
				session, err := a.PrepareRetainedObservationV2(t.Context(), capture, []journalv2.AcknowledgedPublicationV2{ack})
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				if err := session.ReadPublications(); err == nil {
					t.Fatal("inexact publication accepted")
				}
				if count, err := session.Finish(6); err == nil || count != 0 {
					t.Fatal("count replaced exact read")
				}
			})
		}
	}
}

func TestRetainedObservationCopiedConcurrentOutOfOrderFailedSessions(t *testing.T) {
	for _, phase := range []string{"private_first", "finish_first", "lifetime_first", "repeat_read", "copied", "failure", "concurrent"} {
		t.Run(phase, func(t *testing.T) {
			a, capture, accepted := retainedObservationFixture(t, nil, nil, nil)
			session, err := a.PrepareRetainedObservationV2(t.Context(), capture, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			switch phase {
			case "private_first":
				_, err = session.PrivateContext()
			case "finish_first":
				_, err = session.Finish(6)
			case "lifetime_first":
				err = session.CheckLifetime()
			case "repeat_read", "copied":
				if err := session.ReadPublications(); err != nil {
					t.Fatal(err)
				}
				copy := session
				err = copy.ReadPublications()
			case "failure":
				session.state.lifetime.Revoke()
				err = session.ReadPublications()
			case "concurrent":
				// Deterministically simulate an in-progress consuming phase, then issue
				// genuinely concurrent calls. No transport/Host authority is synthesized.
				session.state.mu.Lock()
				var wg sync.WaitGroup
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func(copy RetainedObservationV2) {
						defer wg.Done()
						if copy.ReadPublications() == nil {
							t.Error("concurrent reuse succeeded")
						}
					}(session)
				}
				wg.Wait()
				session.state.mu.Unlock()
				err = errRetainedObservation
			}
			if err == nil || session.ReadPublications() == nil {
				t.Fatal("phase/failure reusable")
			}
			if phase != "repeat_read" && phase != "copied" && accepted.Load() != 0 {
				t.Fatal("out of order/revoked phase read")
			}
		})
	}
	var zero RetainedObservationV2
	if zero.ReadPublications() == nil {
		t.Fatal("zero session accepted")
	}
	zero.Close()
}

func TestRetainedObservationPreflightNoReadNoCloneAndUnsupported(t *testing.T) {
	ack := retainedACKV2(t, "preflight", 7)
	a, capture, accepted := retainedObservationFixture(t, nil, nil, nil)
	wrongDB, _ := journalv2.NewStoreRevisionV2("SECRET", 100, 100)
	lowR, _ := journalv2.NewStoreRevisionV2(capture.DatabaseID(), 6, 6)
	lowDurable, _ := journalv2.NewStoreRevisionV2(capture.DatabaseID(), 7, 7)       // ACK durable is 8
	aheadDurable, _ := journalv2.NewStoreRevisionV2(capture.DatabaseID(), 100, 101) // foundation permits this, source protocol does not
	for _, c := range []journalv2.StoreRevisionV2{{}, wrongDB, lowR, lowDurable, aheadDurable} {
		var s RetainedObservationV2
		var err error
		allocs := testing.AllocsPerRun(1, func() {
			s, err = a.PrepareRetainedObservationV2(context.Background(), c, []journalv2.AcknowledgedPublicationV2{ack})
		})
		// One fixed opaque ACK array escapes (too large for stack); invalid
		// typed coordinates may additionally allocate two validation errors.
		// No ACK Records/CanonicalValue/key payload copies or reads may occur.
		if err == nil || !reflect.ValueOf(s).IsZero() || allocs < 1 || allocs > 3 || accepted.Load() != 0 {
			t.Fatalf("preflight read/copied: allocs=%v err=%v", allocs, err)
		}
	}
	ordinary, err := newAdapter(&fakeProvider{}, "ordinary", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, adapter := range []*Adapter{nil, ordinary, {lifetime: a.lifetime, client: &fakeProvider{}}} {
		if s, err := adapter.PrepareRetainedObservationV2(t.Context(), capture, nil); err == nil || !reflect.ValueOf(s).IsZero() {
			t.Fatal("ordinary shape accepted")
		}
	}
	session, err := a.PrepareRetainedObservationV2(t.Context(), capture, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	a.client = &fakeProvider{}
	if session.ReadPublications() == nil || accepted.Load() != 0 {
		t.Fatal("original adapter client substitution accepted")
	}
}

func TestRetainedObservationPrivateCountCapAndLifetimeRevocation(t *testing.T) {
	for _, n := range []uint64{196, math.MaxUint64} {
		a, capture, accepted := retainedObservationFixture(t, nil, nil, nil)
		s, err := a.PrepareRetainedObservationV2(t.Context(), capture, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.ReadPublications(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PrivateContext(); err != nil {
			t.Fatal(err)
		}
		if count, err := s.Finish(n); count != 0 || err == nil || accepted.Load() != 1 {
			t.Fatal("private count cap read/disclosed")
		}
	}
	a, capture, _ := retainedObservationFixture(t, nil, nil, nil)
	s, err := a.PrepareRetainedObservationV2(t.Context(), capture, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadPublications(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrivateContext(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finish(6); err != nil {
		t.Fatal(err)
	}
	a.lifetime.Revoke()
	if s.CheckLifetime() == nil {
		t.Fatal("terminal revocation accepted")
	}
}

func TestRetainedObservationAllTenCodecNamespacesExcludeAllPrivatePartitions(t *testing.T) {
	ack := retainedACKV2(t, "namespace", 7)
	roles := make(map[journalv2.SealedRecordRole]bool)
	epoch, err := v2RuntimeEpochKey("epoch-private")
	if err != nil {
		t.Fatal(err)
	}
	private := []v2Key{v2AuthorityBindingKey(), v2RuntimeCurrentKey(), epoch, {encoded: "spk/runtime-admission/v2/head"}, {encoded: "spk/runtime-admission/v2/operations/runtime-admit-private"}, {encoded: "spk/runtime-admission/v2/events/epoch-private"}}
	for _, record := range ack.Plan().Records() {
		key, err := v2KeyForSealedRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		roles[record.Role()] = true
		if !retainedPublicationNamespaceV2(key, record.Role()) {
			t.Fatal("actual role codec refused")
		}
		for _, owned := range private {
			if key.String() == owned.String() || retainedPublicationNamespaceV2(owned, record.Role()) {
				t.Fatal("publication/private partition overlaps")
			}
			// Structural ingestion cannot relabel a parseable private key either.
			if retainPublicationRecordV2(make(retainedOracleMapV2), owned.String(), retainedPublicationEntryV2{canonical: []byte(`{}`), revision: 7, role: record.Role()}, journalv2.ConditionAbsent, 0) {
				t.Fatal("private key alias ingested")
			}
		}
	}
	if len(roles) != 10 {
		t.Fatal("all ten namespaces not exercised")
	}
}

func TestRetainedObservationCancelledPrivateChildContext(t *testing.T) {
	a, capture, _ := retainedObservationFixture(t, nil, nil, nil)
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	s, err := a.PrepareRetainedObservationV2(parent, capture, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadPublications(); err != nil {
		t.Fatal(err)
	}
	ctx, err := s.PrivateContext()
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := a.Get(ctx, "private-cancel"); !errors.Is(err, context.Canceled) {
		t.Fatal("private child cancellation lost", err)
	}
	if count, err := s.Finish(6); count != 0 || err == nil {
		t.Fatal("cancelled private closure accepted")
	}
}

func TestRetainedObservationFrozenACKsAcrossContextCallbacks(t *testing.T) {
	a, capture, accepted := retainedObservationFixture(t, nil, nil, nil)
	retainedSnapshotCallbackChecksV2(t, func(ctx context.Context, input []journalv2.AcknowledgedPublicationV2) retainedPublicationMapV2 {
		var originalRecords uint64
		for _, ack := range input {
			f, err := ack.Plan().MeasureRecordFootprintV2(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			originalRecords += f.RecordCount
		}
		s, err := a.PrepareRetainedObservationV2(ctx, capture, input)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		// Occurrences include overwritten mutable rows. Measure before caller
		// callbacks and verify the original conservative hash reservation.
		planned, ok := retainedObservationWork(a.lifetime.ImageSize(), originalRecords)
		if !ok || s.state.budget.planned != planned || s.state.budget.attempts != 0 || s.state.budget.hashed != 0 || accepted.Load() != 0 {
			t.Fatal("frozen original work/RPC budget drift")
		}
		return s.state.expected
	})
}

func TestRetainedObservationFrozenCaptureRefusals(t *testing.T) {
	original := retainedACKV2(t, "snapshot-capture-original", 7)
	replacement := retainedACKV2(t, "snapshot-capture-low", 1)
	a, capture, accepted := retainedObservationFixture(t, nil, nil, nil)
	for _, r := range []uint64{6, 7} { // original R=7/durable=8 must refuse both
		low, _ := journalv2.NewStoreRevisionV2(capture.DatabaseID(), r, r)
		input := []journalv2.AcknowledgedPublicationV2{original}
		ctx := &retainedSnapshotContextV2{Context: context.Background(), method: 0, at: 1, swap: func() { input[0] = replacement }}
		s, err := a.PrepareRetainedObservationV2(ctx, low, input)
		if err != errRetainedObservation || s.state != nil || input[0].PublicationID() != replacement.PublicationID() || accepted.Load() != 0 {
			t.Fatal("replacement bypassed original capture revision/durability")
		}
	}
}
