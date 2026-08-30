package reducer

import (
	"bytes"
	"errors"
	"strconv"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
)

func event(t *testing.T, id, operation, eventType, aggregate string, index, count uint16, logical uint64, payload string) controljournal.EventEnvelopeV1 {
	t.Helper()
	envelope, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{
		EnvelopeVersion: 1, EventID: controljournal.EventID(id), EventType: controljournal.EventType(eventType),
		EventSchemaVersion: 1, ReducerVersion: "mission-v1", ProjectID: "project-1",
		AggregateKind: "mission", AggregateID: controljournal.AggregateID(aggregate), AggregateLogicalVersion: logical,
		OperationID: controljournal.OperationID(operation), RuntimeEpochID: "epoch-1",
		EventIndex: controljournal.EventIndexV1(index), EventCount: controljournal.EventCountV1(count),
		RecordedObservations: []byte(`{}`), Payload: []byte(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func FuzzReducerDeterminism(f *testing.F) {
	for _, value := range []int64{-100, 0, 1, 100} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value int64) {
		payload := `{"value":` + strconv.FormatInt(value, 10) + `}`
		journal := validated(t, []controljournal.EventVersionV1{{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}}, controljournal.StoredEventV1{RecordRevision: 1, Envelope: event(t, "event-1", "operation-1", "mission.updated", "agg-1", 0, 1, 1, payload)})
		left, err := ApplyV1(registry(t), EmptyProjectionV1(), journal)
		if err != nil {
			t.Fatal(err)
		}
		right, err := ApplyV1(registry(t), EmptyProjectionV1(), journal)
		if err != nil {
			t.Fatal(err)
		}
		if left.GlobalDigest() != right.GlobalDigest() {
			t.Fatal("same history produced different digest")
		}
	})
}

func validated(t *testing.T, versions []controljournal.EventVersionV1, events ...controljournal.StoredEventV1) controljournal.ValidatedJournalV1 {
	t.Helper()
	registry, err := controljournal.NewEventRegistryV1(versions)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := controljournal.ValidateJournalV1(registry, events)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

func replaceState(_ StateV1, event controljournal.EventEnvelopeV1) (StateV1, error) {
	return NewStateV1(event.Payload())
}

func registry(t *testing.T) RegistryV1 {
	t.Helper()
	registry, err := NewRegistryV1([]EntryV1{{AggregateKind: "mission", EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Reduce: replaceState}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestReducerRepeatedHistoryIsByteIdentical(t *testing.T) {
	versions := []controljournal.EventVersionV1{{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}}
	journal := validated(t, versions,
		controljournal.StoredEventV1{RecordRevision: 2, Envelope: event(t, "event-2", "operation-2", "mission.updated", "agg-1", 0, 1, 2, `{"value":2}`)},
		controljournal.StoredEventV1{RecordRevision: 1, Envelope: event(t, "event-1", "operation-1", "mission.updated", "agg-1", 0, 1, 1, `{"value":1}`)},
	)
	left, err := ApplyV1(registry(t), EmptyProjectionV1(), journal)
	if err != nil {
		t.Fatal(err)
	}
	right, err := ApplyV1(registry(t), EmptyProjectionV1(), journal)
	if err != nil {
		t.Fatal(err)
	}
	key := AggregateKeyV1{Kind: "mission", ID: "agg-1"}
	leftState, _ := left.State(key)
	rightState, _ := right.State(key)
	if !bytes.Equal(leftState.CanonicalBytes(), rightState.CanonicalBytes()) || left.GlobalDigest() != right.GlobalDigest() {
		t.Fatal("repeated reduction changed canonical result")
	}
}

func TestReducerGlobalDigestIgnoresMapIterationOrderAndGrantsNoAuthority(t *testing.T) {
	a, _ := NewStateV1([]byte(`{"value":"a"}`))
	b, _ := NewStateV1([]byte(`{"value":"b"}`))
	keyA := AggregateKeyV1{Kind: "mission", ID: "a"}
	keyB := AggregateKeyV1{Kind: "mission", ID: "b"}
	left, err := NewProjectionV1(map[AggregateKeyV1]StateV1{keyA: a, keyB: b}, map[AggregateKeyV1]uint64{keyA: 1, keyB: 9})
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewProjectionV1(map[AggregateKeyV1]StateV1{keyB: b, keyA: a}, map[AggregateKeyV1]uint64{keyB: 9, keyA: 1})
	if err != nil {
		t.Fatal(err)
	}
	if left.GlobalDigest() != right.GlobalDigest() {
		t.Fatal("global digest depends on insertion order")
	}
	// Projection exposes evidence and aggregate-local heads, but deliberately
	// has no mutation/CAS method accepting the global digest.
	if _, ok := any(left).(interface{ CompareAndSwap([32]byte) bool }); ok {
		t.Fatal("digest unexpectedly grants authority")
	}
}

func TestReducerUnknownEventReturnsNoPartialPublication(t *testing.T) {
	versions := []controljournal.EventVersionV1{
		{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"},
		{EventType: "mission.unknown", EventSchemaVersion: 1, ReducerVersion: "mission-v1"},
	}
	journal := validated(t, versions,
		controljournal.StoredEventV1{RecordRevision: 1, Envelope: event(t, "event-1", "operation-1", "mission.updated", "agg-1", 0, 1, 1, `{"value":1}`)},
		controljournal.StoredEventV1{RecordRevision: 2, Envelope: event(t, "event-2", "operation-2", "mission.unknown", "agg-1", 0, 1, 2, `{"value":2}`)},
	)
	initial := EmptyProjectionV1()
	result, err := ApplyV1(registry(t), initial, journal)
	if err == nil {
		t.Fatal("missing reducer accepted")
	}
	if _, ok := result.State(AggregateKeyV1{Kind: "mission", ID: "agg-1"}); ok {
		t.Fatal("partial state published")
	}
	if _, ok := initial.State(AggregateKeyV1{Kind: "mission", ID: "agg-1"}); ok {
		t.Fatal("initial state mutated")
	}
	applicationError, ok := err.(EventApplicationErrorV1)
	if !ok || applicationError.EventOrdinal != 1 {
		t.Fatalf("failure did not identify exact event: %T %#v", err, err)
	}
}

func TestReducerInvalidCapabilityFailsBeforeReducerOrStateChange(t *testing.T) {
	calls := 0
	reg, err := NewRegistryV1([]EntryV1{{AggregateKind: "mission", EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Reduce: func(state StateV1, event controljournal.EventEnvelopeV1) (StateV1, error) { calls++; return state, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []controljournal.ValidatedJournalV1{{}, *new(controljournal.ValidatedJournalV1)} {
		if _, err := ApplyV1(reg, EmptyProjectionV1(), invalid); !errors.As(err, new(controljournal.InvalidValidatedJournalError)) {
			t.Fatalf("invalid capability error = %T %v", err, err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid capability reached reducer %d times", calls)
	}
}

func TestHeadUnrelatedAggregatesAdvanceIndependently(t *testing.T) {
	versions := []controljournal.EventVersionV1{{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}}
	journal := validated(t, versions,
		controljournal.StoredEventV1{RecordRevision: 1, Envelope: event(t, "event-a", "operation-a", "mission.updated", "agg-a", 0, 1, 1, `{"value":"a"}`)},
		controljournal.StoredEventV1{RecordRevision: 2, Envelope: event(t, "event-b", "operation-b", "mission.updated", "agg-b", 0, 1, 1, `{"value":"b"}`)},
	)
	projection, err := ApplyV1(registry(t), EmptyProjectionV1(), journal)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []controljournal.AggregateID{"agg-a", "agg-b"} {
		if head, ok := projection.Head(AggregateKeyV1{Kind: "mission", ID: id}); !ok || head != 1 {
			t.Fatalf("head %s = %d, %v", id, head, ok)
		}
	}
}

func TestReducerFailureDoesNotMutatePriorState(t *testing.T) {
	prior, _ := NewStateV1([]byte(`{"value":"prior"}`))
	key := AggregateKeyV1{Kind: "mission", ID: "agg-1"}
	initial, _ := NewProjectionV1(map[AggregateKeyV1]StateV1{key: prior}, map[AggregateKeyV1]uint64{key: 1})
	reg, _ := NewRegistryV1([]EntryV1{{AggregateKind: "mission", EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Reduce: func(StateV1, controljournal.EventEnvelopeV1) (StateV1, error) {
		return StateV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidTransition, Field: "synthetic"}
	}}})
	journal := validated(t, []controljournal.EventVersionV1{{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}}, controljournal.StoredEventV1{RecordRevision: 2, Envelope: event(t, "event-2", "operation-2", "mission.updated", "agg-1", 0, 1, 2, `{"value":"next"}`)})
	if _, err := ApplyV1(reg, initial, journal); err == nil {
		t.Fatal("synthetic reducer failure accepted")
	}
	state, _ := initial.State(key)
	if string(state.CanonicalBytes()) != `{"value":"prior"}` {
		t.Fatal("failed reducer mutated caller state")
	}
}
