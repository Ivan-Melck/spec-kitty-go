package reducer

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
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

func registry(t *testing.T) RegistryV1 {
	t.Helper()
	registry, err := NewRegistryV1([]EntryV1{{AggregateKind: "mission", EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Program: ProgramReplaceWithCanonicalEventPayloadV1}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestReducerRegistryRejectsInvalidRootCoordinates(t *testing.T) {
	valid := EntryV1{AggregateKind: "mission", EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Program: ProgramReplaceWithCanonicalEventPayloadV1}
	invalidTokens := map[string]string{
		"empty":               "",
		"space":               "bad token",
		"leading punctuation": "-bad",
		"invalid utf8":        string([]byte{0xff}),
		"129 bytes":           strings.Repeat("a", 129),
	}
	for name, value := range invalidTokens {
		for _, coordinate := range []string{"aggregate_kind", "event_type", "reducer_version"} {
			t.Run(coordinate+"/"+name, func(t *testing.T) {
				entry := valid
				switch coordinate {
				case "aggregate_kind":
					entry.AggregateKind = controljournal.AggregateKind(value)
				case "event_type":
					entry.EventType = controljournal.EventType(value)
				case "reducer_version":
					entry.ReducerVersion = controljournal.ReducerVersion(value)
				}
				if _, err := NewRegistryV1([]EntryV1{entry}); err == nil {
					t.Fatalf("accepted invalid %s %q", coordinate, value)
				}
			})
		}
	}
	zeroSchema := valid
	zeroSchema.EventSchemaVersion = 0
	if _, err := NewRegistryV1([]EntryV1{zeroSchema}); err == nil {
		t.Fatal("accepted zero event schema")
	}
	unknownProgram := valid
	unknownProgram.Program = "external_callback"
	if _, err := NewRegistryV1([]EntryV1{unknownProgram}); err == nil {
		t.Fatal("accepted unknown reducer program")
	}
	if _, err := NewRegistryV1([]EntryV1{valid, valid}); err == nil {
		t.Fatal("accepted duplicate reducer coordinate")
	}
	maxToken := controljournal.AggregateKind(strings.Repeat("a", 128))
	atBoundary := valid
	atBoundary.AggregateKind = maxToken
	atBoundary.EventType = controljournal.EventType(maxToken)
	atBoundary.ReducerVersion = controljournal.ReducerVersion(maxToken)
	if _, err := NewRegistryV1([]EntryV1{atBoundary}); err != nil {
		t.Fatalf("rejected 128-byte reducer coordinates: %v", err)
	}
}

func TestReducerRegistryDigestIsCanonicalAndRejectsZeroOrTampering(t *testing.T) {
	a := EntryV1{AggregateKind: "mission", EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Program: ProgramReplaceWithCanonicalEventPayloadV1}
	b := EntryV1{AggregateKind: "work", EventType: "work.updated", EventSchemaVersion: 2, ReducerVersion: "work-v2", Program: ProgramReplaceWithCanonicalEventPayloadV1}
	left, err := NewRegistryV1([]EntryV1{a, b})
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewRegistryV1([]EntryV1{b, a})
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := left.Digest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.Digest()
	if err != nil || leftDigest != rightDigest {
		t.Fatalf("reducer digest order drift left=%x right=%x err=%v", leftDigest, rightDigest, err)
	}
	if _, err := (RegistryV1{}).Digest(); err == nil {
		t.Fatal("zero reducer registry exposed a digest")
	}
	tampered, _ := NewRegistryV1([]EntryV1{a})
	tampered.digest[0] ^= 1
	if _, err := tampered.Digest(); err == nil {
		t.Fatal("tampered reducer digest accepted")
	}
	tampered, _ = NewRegistryV1([]EntryV1{a})
	for key := range tampered.programs {
		tampered.programs[key] = "external_callback"
	}
	if _, err := tampered.Digest(); err == nil {
		t.Fatal("tampered reducer program accepted")
	}
}

func TestReducerProjectionRejectsInvalidRootCoordinates(t *testing.T) {
	state, err := NewStateV1([]byte(`{"value":1}`))
	if err != nil {
		t.Fatal(err)
	}
	validKey := AggregateKeyV1{Kind: "mission", ID: "aggregate-1"}
	invalidTokens := map[string]string{
		"empty":               "",
		"space":               "bad token",
		"leading punctuation": "-bad",
		"invalid utf8":        string([]byte{0xff}),
		"129 bytes":           strings.Repeat("a", 129),
	}
	for name, value := range invalidTokens {
		for _, coordinate := range []string{"aggregate_kind", "aggregate_id"} {
			key := validKey
			if coordinate == "aggregate_kind" {
				key.Kind = controljournal.AggregateKind(value)
			} else {
				key.ID = controljournal.AggregateID(value)
			}
			t.Run("state/"+coordinate+"/"+name, func(t *testing.T) {
				if _, err := NewProjectionV1(map[AggregateKeyV1]StateV1{key: state}, map[AggregateKeyV1]uint64{key: 1}); err == nil {
					t.Fatalf("accepted invalid state %s %q", coordinate, value)
				}
			})
			t.Run("head/"+coordinate+"/"+name, func(t *testing.T) {
				if _, err := NewProjectionV1(map[AggregateKeyV1]StateV1{validKey: state}, map[AggregateKeyV1]uint64{key: 1}); err == nil {
					t.Fatalf("accepted invalid head %s %q", coordinate, value)
				}
			})
		}
	}
	boundary := AggregateKeyV1{Kind: controljournal.AggregateKind(strings.Repeat("a", 128)), ID: controljournal.AggregateID(strings.Repeat("b", 128))}
	if _, err := NewProjectionV1(map[AggregateKeyV1]StateV1{boundary: state}, map[AggregateKeyV1]uint64{boundary: 1}); err != nil {
		t.Fatalf("rejected 128-byte aggregate coordinate: %v", err)
	}
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
	reg := registry(t)
	eventRegistry, err := controljournal.NewEventRegistryV1([]controljournal.EventVersionV1{{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	malformed, validationErr := controljournal.ValidateJournalV1(eventRegistry, []controljournal.StoredEventV1{{RecordRevision: 0}})
	if validationErr == nil {
		t.Fatal("malformed record unexpectedly validated")
	}
	for _, invalid := range []controljournal.ValidatedJournalV1{{}, *new(controljournal.ValidatedJournalV1), malformed} {
		if _, err := ApplyV1(reg, EmptyProjectionV1(), invalid); !errors.As(err, new(controljournal.InvalidValidatedJournalError)) {
			t.Fatalf("invalid capability error = %T %v", err, err)
		}
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
	reg := registry(t)
	journal := validated(t, []controljournal.EventVersionV1{{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}}, controljournal.StoredEventV1{RecordRevision: 2, Envelope: event(t, "event-2", "operation-2", "mission.updated", "agg-1", 0, 1, 3, `{"value":"next"}`)})
	if _, err := ApplyV1(reg, initial, journal); err == nil {
		t.Fatal("logical-gap reducer failure accepted")
	}
	state, _ := initial.State(key)
	if string(state.CanonicalBytes()) != `{"value":"prior"}` {
		t.Fatal("failed reducer mutated caller state")
	}
}
