package controljournal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const fixtureUnsignedEvent = `{"aggregate_id":"agg-1","aggregate_kind":"mission","aggregate_logical_version":1,"envelope_version":1,"event_count":1,"event_id":"event-1","event_index":0,"event_schema_version":1,"event_type":"mission.created","operation_id":"operation-1","payload":{"name":"alpha","priority":2},"project_id":"project-1","recorded_observations":{},"reducer_version":"mission-v1","runtime_epoch_id":"epoch-1"}`
const fixtureEventDigest = "44d21dfec70fd628156d3eea1b1c5cfde6aeeefb7cfacee76a63a7fafe8250f6"

func fixtureEnvelope(t *testing.T, eventID string, operation string, index, count uint16, aggregate string, logical uint64) EventEnvelopeV1 {
	t.Helper()
	envelope, err := NewEventEnvelopeV1(EventEnvelopeInputV1{
		EnvelopeVersion: 1, EventID: EventID(eventID), EventType: "mission.created",
		EventSchemaVersion: 1, ReducerVersion: "mission-v1", ProjectID: "project-1",
		AggregateKind: "mission", AggregateID: AggregateID(aggregate), AggregateLogicalVersion: logical,
		OperationID: OperationID(operation), RuntimeEpochID: "epoch-1",
		EventIndex: EventIndexV1(index), EventCount: EventCountV1(count),
		RecordedObservations: []byte(`{}`), Payload: []byte(`{"priority":2,"name":"alpha"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func fixtureRegistry(t *testing.T) EventRegistryV1 {
	t.Helper()
	registry, err := NewEventRegistryV1([]EventVersionV1{{EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestModelIdentityValidation(t *testing.T) {
	constructors := []func(string) error{
		func(v string) error { _, err := NewRunID(v); return err },
		func(v string) error { _, err := NewMissionID(v); return err },
		func(v string) error { _, err := NewActorID(v); return err },
		func(v string) error { _, err := NewProcessID(v); return err },
		func(v string) error { _, err := NewWorkspaceID(v); return err },
		func(v string) error { _, err := NewRuntimeEpochID(v); return err },
		func(v string) error { _, err := NewOperationID(v); return err },
		func(v string) error { _, err := NewEventID(v); return err },
	}
	for _, constructor := range constructors {
		for _, invalid := range []string{"", "-leading", "has space", string(make([]byte, 129))} {
			if err := constructor(invalid); err == nil {
				t.Fatalf("accepted invalid identity %q", invalid)
			}
		}
		if err := constructor("valid.token:1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewEventIndexV1(128); err == nil {
		t.Fatal("accepted index 128")
	}
	if _, err := NewEventCountV1(0); err == nil {
		t.Fatal("accepted count zero")
	}
	if _, err := NewEventCountV1(129); err == nil {
		t.Fatal("accepted count 129")
	}
}

func TestModelRootCoordinateValidatorsUseExactTokenGrammar(t *testing.T) {
	invalid := []string{"", "has space", "-leading", string([]byte{0xff}), strings.Repeat("a", 129)}
	for _, value := range invalid {
		if err := ValidateAggregateCoordinateV1(AggregateKind(value), "aggregate-1"); err == nil {
			t.Fatalf("aggregate kind accepted %q", value)
		}
		if err := ValidateAggregateCoordinateV1("mission", AggregateID(value)); err == nil {
			t.Fatalf("aggregate id accepted %q", value)
		}
		if err := ValidateReducerCoordinateV1(AggregateKind(value), "mission.updated", 1, "mission-v1"); err == nil {
			t.Fatalf("reducer aggregate kind accepted %q", value)
		}
		if err := ValidateReducerCoordinateV1("mission", EventType(value), 1, "mission-v1"); err == nil {
			t.Fatalf("event type accepted %q", value)
		}
		if err := ValidateReducerCoordinateV1("mission", "mission.updated", 1, ReducerVersion(value)); err == nil {
			t.Fatalf("reducer version accepted %q", value)
		}
	}
	if err := ValidateReducerCoordinateV1("mission", "mission.updated", 0, "mission-v1"); err == nil {
		t.Fatal("zero event schema accepted")
	}
	max := strings.Repeat("a", 128)
	if err := ValidateAggregateCoordinateV1(AggregateKind(max), AggregateID(max)); err != nil {
		t.Fatalf("128-byte aggregate coordinate rejected: %v", err)
	}
	if err := ValidateReducerCoordinateV1(AggregateKind(max), EventType(max), 1, ReducerVersion(max)); err != nil {
		t.Fatalf("128-byte reducer coordinate rejected: %v", err)
	}
}

func TestModelTrialVocabularyHasTwoActorsAndThreeProcesses(t *testing.T) {
	vocabulary, err := NewTrialImplementationActorsV1("actor-a", "actor-b", "process-a", "process-b", "process-c")
	if err != nil {
		t.Fatal(err)
	}
	if actor, ok := vocabulary.ActorForProcess("process-c"); !ok || actor != "actor-a" {
		t.Fatalf("Process C actor = %q, %v", actor, ok)
	}
	if _, err := NewTrialImplementationActorsV1("actor-a", "actor-a", "process-a", "process-b", "process-c"); err == nil {
		t.Fatal("trial accepted one actor in both roles")
	}
	if _, ok := vocabulary.ActorForProcess("process-d"); ok {
		t.Fatal("trial admitted a fourth process")
	}
}

func TestModelEnvelopeDefensiveCopiesAndIndependentDigestFixture(t *testing.T) {
	payload := []byte(`{"priority":2,"name":"alpha"}`)
	envelope := fixtureEnvelope(t, "event-1", "operation-1", 0, 1, "agg-1", 1)
	payload[2] = 'X'
	if string(envelope.Payload()) != `{"name":"alpha","priority":2}` {
		t.Fatalf("payload changed: %s", envelope.Payload())
	}
	returned := envelope.Payload()
	returned[2] = 'X'
	if string(envelope.Payload()) != `{"name":"alpha","priority":2}` {
		t.Fatal("accessor leaked payload")
	}
	unsigned, err := envelope.canonicalBytes(false)
	if err != nil {
		t.Fatal(err)
	}
	if string(unsigned) != fixtureUnsignedEvent {
		t.Fatalf("canonical fixture drift:\n%s", unsigned)
	}
	if envelope.CanonicalEventDigestHex() != fixtureEventDigest {
		t.Fatalf("digest fixture drift: %s", envelope.CanonicalEventDigestHex())
	}
}

func TestModelFieldVocabularyAndCoordinateSeparation(t *testing.T) {
	values := []any{EventEnvelopeV1{}, StoredEventV1{}, AggregateHeadV1{}, ExpectedAggregateHeadV1{}, OperationRecordV1{}, EffectIntentV1{}, OperationOutcomeV1{}}
	for _, value := range values {
		typeOf := reflect.TypeOf(value)
		for index := 0; index < typeOf.NumField(); index++ {
			name := typeOf.Field(index).Name
			forbidden := name == "StorageKey" || name == "CurrentRevision" || name == "SQLite"
			if forbidden {
				t.Fatalf("%s owns forbidden model field %s", typeOf.Name(), name)
			}
		}
	}
	if reflect.TypeOf(RuntimeEpochID("")) == reflect.TypeOf(uint64(0)) || reflect.TypeOf(EventIndexV1(0)) == reflect.TypeOf(EventCountV1(0)) {
		t.Fatal("independent coordinates were conflated")
	}
}

func TestModelRegistryIsExactAndDefensivelyCopied(t *testing.T) {
	versions := []EventVersionV1{{EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}}
	registry, err := NewEventRegistryV1(versions)
	if err != nil {
		t.Fatal(err)
	}
	versions[0].ReducerVersion = "mutated-v2"
	if _, err := ValidateJournalV1(registry, []StoredEventV1{{RecordRevision: 1, Envelope: fixtureEnvelope(t, "event-1", "operation-1", 0, 1, "agg-1", 1)}}); err != nil {
		t.Fatalf("caller mutation changed registry: %v", err)
	}
	if _, err := NewEventRegistryV1([]EventVersionV1{{EventType: "*", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}}); err == nil {
		t.Fatal("wildcard registry admitted")
	}
	if _, err := NewEventRegistryV1([]EventVersionV1{{EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}, {EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}}); err == nil {
		t.Fatal("duplicate registry tuple admitted")
	}
}

func TestEventRegistryDigestIsCanonicalAndRejectsZeroOrTamperedState(t *testing.T) {
	left, err := NewEventRegistryV1([]EventVersionV1{{EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}, {EventType: "mission.updated", EventSchemaVersion: 2, ReducerVersion: "mission-v2"}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewEventRegistryV1([]EventVersionV1{{EventType: "mission.updated", EventSchemaVersion: 2, ReducerVersion: "mission-v2"}, {EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := left.Digest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.Digest()
	if err != nil || leftDigest != rightDigest {
		t.Fatalf("registry digest order drift left=%x right=%x err=%v", leftDigest, rightDigest, err)
	}
	if _, err := (EventRegistryV1{}).Digest(); err == nil {
		t.Fatal("zero event registry exposed a digest")
	}
	tampered := left
	tampered.digest[0] ^= 1
	if _, err := tampered.Digest(); err == nil {
		t.Fatal("tampered event registry digest accepted")
	}
	tampered, _ = NewEventRegistryV1([]EventVersionV1{{EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}, {EventType: "mission.updated", EventSchemaVersion: 2, ReducerVersion: "mission-v2"}})
	delete(tampered.versions, eventVersionKey("mission.created", 1, "mission-v1"))
	if _, err := tampered.Digest(); err == nil {
		t.Fatal("tampered event registry contents accepted")
	}
}

func TestValidatedJournalCursorSealedOrderedAndCopyOnAccess(t *testing.T) {
	registry := fixtureRegistry(t)
	journal, err := ValidateJournalV1(registry, []StoredEventV1{
		{RecordRevision: 2, Envelope: fixtureEnvelope(t, "event-2", "operation-2", 0, 1, "agg-1", 2)},
		{RecordRevision: 1, Envelope: fixtureEnvelope(t, "event-1", "operation-1", 0, 1, "agg-1", 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := journal.CursorV1()
	if err != nil {
		t.Fatal(err)
	}
	first, ok, err := cursor.NextV1()
	if err != nil || !ok || first.RecordRevision != 1 {
		t.Fatalf("first cursor record=%+v ok=%v err=%v", first, ok, err)
	}
	first.Envelope.payload = []byte(`{"mutated":true}`)
	second, ok, err := cursor.NextV1()
	if err != nil || !ok || second.RecordRevision != 2 {
		t.Fatalf("second cursor record=%+v ok=%v err=%v", second, ok, err)
	}
	if _, ok, err := cursor.NextV1(); err != nil || ok {
		t.Fatalf("terminal cursor ok=%v err=%v", ok, err)
	}
	if _, _, err := (&JournalCursorV1{}).NextV1(); err == nil {
		t.Fatal("zero cursor accepted")
	}
	for name, mutate := range map[string]func(*JournalCursorV1){
		"version": func(value *JournalCursorV1) { value.seal.version++ },
		"digest":  func(value *JournalCursorV1) { value.seal.journalDigest[0] ^= 1 },
		"count":   func(value *JournalCursorV1) { value.seal.recordCount++ },
		"index":   func(value *JournalCursorV1) { value.index = len(value.journal.records) + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			value, cursorErr := journal.CursorV1()
			if cursorErr != nil {
				t.Fatal(cursorErr)
			}
			mutate(&value)
			if _, _, err := value.NextV1(); err == nil {
				t.Fatal("tampered cursor accepted")
			}
		})
	}
}

func TestOrderCanonicalizesAndGroupsAtomically(t *testing.T) {
	registry := fixtureRegistry(t)
	e0 := fixtureEnvelope(t, "event-a", "operation-1", 0, 2, "agg-1", 1)
	e1 := fixtureEnvelope(t, "event-b", "operation-1", 1, 2, "agg-1", 2)
	journal, err := ValidateJournalV1(registry, []StoredEventV1{{RecordRevision: 7, Envelope: e1}, {RecordRevision: 7, Envelope: e0}})
	if err != nil {
		t.Fatal(err)
	}
	length, err := journal.Len()
	if err != nil || length != 2 {
		t.Fatalf("Len = %d, %v", length, err)
	}
	first, err := journal.At(0)
	if err != nil || first.Envelope.EventID != "event-a" {
		t.Fatalf("first = %#v, %v", first, err)
	}
	second, _ := journal.At(1)
	if second.Envelope.EventID != "event-b" {
		t.Fatalf("second = %#v", second)
	}

	mutants := map[string][]StoredEventV1{
		"duplicate id":   {{RecordRevision: 7, Envelope: e0}, {RecordRevision: 7, Envelope: e0}},
		"missing index":  {{RecordRevision: 7, Envelope: e0}},
		"mixed revision": {{RecordRevision: 7, Envelope: e0}, {RecordRevision: 8, Envelope: e1}},
	}
	wrongCount := e1
	wrongCount.EventCount = 1
	mutants["mixed count"] = []StoredEventV1{{RecordRevision: 7, Envelope: e0}, {RecordRevision: 7, Envelope: wrongCount}}
	wrongDigest := e1
	wrongDigest.digest[0] ^= 1
	mutants["wrong digest"] = []StoredEventV1{{RecordRevision: 7, Envelope: e0}, {RecordRevision: 7, Envelope: wrongDigest}}
	for name, events := range mutants {
		t.Run(name, func(t *testing.T) {
			got, err := ValidateJournalV1(registry, events)
			if err == nil {
				t.Fatal("mutant accepted")
			}
			if _, accessErr := got.Len(); !errors.As(accessErr, new(InvalidValidatedJournalError)) {
				t.Fatalf("failure returned usable value: %v", accessErr)
			}
		})
	}
}

func TestOrderUnknownEveryVersionCoordinateFails(t *testing.T) {
	registry := fixtureRegistry(t)
	base := fixtureEnvelope(t, "event-1", "operation-1", 0, 1, "agg-1", 1)
	mutants := []EventEnvelopeV1{base, base, base, base}
	mutants[0].EnvelopeVersion = 2
	mutants[1].EventType = "mission.unknown"
	mutants[2].EventSchemaVersion = 2
	mutants[3].ReducerVersion = "mission-v2"
	for i, mutant := range mutants {
		if got, err := ValidateJournalV1(registry, []StoredEventV1{{RecordRevision: 1, Envelope: mutant}}); err == nil {
			t.Fatalf("version mutant %d accepted: %#v", i, got)
		}
	}
}

func TestModelZeroAndTamperedValidatedValuesFail(t *testing.T) {
	assertInvalid := func(t *testing.T, value ValidatedJournalV1) {
		t.Helper()
		if _, err := value.Len(); !errors.As(err, new(InvalidValidatedJournalError)) {
			t.Fatalf("Len error = %T %v", err, err)
		}
		if _, err := value.At(0); !errors.As(err, new(InvalidValidatedJournalError)) {
			t.Fatalf("At error = %T %v", err, err)
		}
	}
	var zero ValidatedJournalV1
	assertInvalid(t, zero)
	assertInvalid(t, *new(ValidatedJournalV1))
	values := []ValidatedJournalV1{zero, {seal: validationSealV1{version: 1}}, {records: []StoredEventV1{{}}}}
	for _, value := range values {
		assertInvalid(t, value)
	}
	containers := struct {
		Array    [1]ValidatedJournalV1
		Slice    []ValidatedJournalV1
		Embedded struct{ Value ValidatedJournalV1 }
	}{Slice: []ValidatedJournalV1{{}}}
	assertInvalid(t, containers.Array[0])
	assertInvalid(t, containers.Slice[0])
	assertInvalid(t, containers.Embedded.Value)

	journal, err := ValidateJournalV1(fixtureRegistry(t), []StoredEventV1{{RecordRevision: 1, Envelope: fixtureEnvelope(t, "event-1", "operation-1", 0, 1, "agg-1", 1)}})
	if err != nil {
		t.Fatal(err)
	}
	copyValue := journal
	if length, err := copyValue.Len(); err != nil || length != 1 {
		t.Fatalf("valid copy rejected: %d %v", length, err)
	}
	tampered := journal
	tampered.records[0].RecordRevision++
	assertInvalid(t, tampered)
	// Restore the shared backing array before checking the original; ordinary
	// value copies are valid, but same-package mutation is detected by the seal.
	tampered.records[0].RecordRevision--
	input := []StoredEventV1{{RecordRevision: 1, Envelope: fixtureEnvelope(t, "event-2", "operation-2", 0, 1, "agg-2", 1)}}
	defensive, err := ValidateJournalV1(fixtureRegistry(t), input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].RecordRevision = 99
	input[0].Envelope.EventID = "mutated"
	got, err := defensive.At(0)
	if err != nil {
		t.Fatal(err)
	}
	if got.RecordRevision != 1 || got.Envelope.EventID != "event-2" {
		t.Fatalf("input mutation escaped defensive copy: %#v", got)
	}
	got.Envelope.EventID = "returned-mutant"
	again, _ := defensive.At(0)
	if again.Envelope.EventID != "event-2" {
		t.Fatal("At leaked mutable record")
	}
}

func TestModelMalformedForgedCapabilityFailsClosed(t *testing.T) {
	for _, forged := range []ValidatedJournalV1{
		{seal: validationSealV1{version: 1}, records: []StoredEventV1{{RecordRevision: 1, Envelope: EventEnvelopeV1{payload: []byte("{")}}}},
		{seal: validationSealV1{version: 1, digest: sha256.Sum256([]byte("forged"))}, records: []StoredEventV1{{RecordRevision: 1, Envelope: EventEnvelopeV1{payload: []byte("{")}}}},
	} {
		if length, err := forged.Len(); !errors.As(err, new(InvalidValidatedJournalError)) {
			t.Fatalf("forged Len = %d, %T %v", length, err, err)
		}
		if record, err := forged.At(0); !errors.As(err, new(InvalidValidatedJournalError)) {
			t.Fatalf("forged At = %#v, %T %v", record, err, err)
		}
	}
}

func TestHeadAggregateLocalTransitions(t *testing.T) {
	expectedA := ExpectedAggregateHeadV1{AggregateKind: "mission", AggregateID: "a", LogicalVersion: 2, ObservedHeadRevision: 8}
	expectedB := ExpectedAggregateHeadV1{AggregateKind: "mission", AggregateID: "b", LogicalVersion: 9, ObservedHeadRevision: 13}
	headA, err := ConstructAggregateHeadV1("mission", "a", 3, "epoch-1", "event-a", sha256.Sum256([]byte("a")))
	if err != nil {
		t.Fatal(err)
	}
	headB, err := ConstructAggregateHeadV1("mission", "b", 10, "epoch-1", "event-b", sha256.Sum256([]byte("b")))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateHeadTransition(expectedA, NewAggregateHeadV1{Head: headA}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHeadTransition(expectedB, NewAggregateHeadV1{Head: headB}); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(expectedA, expectedB) {
		t.Fatal("unrelated heads conflated")
	}
}

func TestHeadTransitionRejectsOverflowAndIncompleteNextHead(t *testing.T) {
	digest := sha256.Sum256([]byte("state"))
	valid, err := ConstructAggregateHeadV1("mission", "a", 3, "epoch-1", "event-3", digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateHeadTransition(ExpectedAggregateHeadV1{AggregateKind: "mission", AggregateID: "a", LogicalVersion: ^uint64(0), ObservedHeadRevision: 8}, NewAggregateHeadV1{Head: AggregateHeadV1{SchemaVersion: 1, AggregateKind: "mission", AggregateID: "a", LogicalVersion: 0, RuntimeEpochID: "epoch-1", LastEventID: "event-3", StateDigest: digest}}); err == nil {
		t.Fatal("logical version wrapped")
	}
	mutants := map[string]AggregateHeadV1{
		"missing epoch": valid,
		"missing event": valid,
		"zero digest":   valid,
		"wrong schema":  valid,
	}
	mutant := mutants["missing epoch"]
	mutant.RuntimeEpochID = ""
	mutants["missing epoch"] = mutant
	mutant = mutants["missing event"]
	mutant.LastEventID = ""
	mutants["missing event"] = mutant
	mutant = mutants["zero digest"]
	mutant.StateDigest = [32]byte{}
	mutants["zero digest"] = mutant
	mutant = mutants["wrong schema"]
	mutant.SchemaVersion = 2
	mutants["wrong schema"] = mutant
	for name, head := range mutants {
		t.Run(name, func(t *testing.T) {
			if err := ValidateHeadTransition(ExpectedAggregateHeadV1{AggregateKind: "mission", AggregateID: "a", LogicalVersion: 2, ObservedHeadRevision: 8}, NewAggregateHeadV1{Head: head}); err == nil {
				t.Fatal("invalid next head accepted")
			}
		})
	}
}

func TestModelHardCodedDigestBytesAreIndependent(t *testing.T) {
	digest, err := hex.DecodeString(fixtureEventDigest)
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 32 {
		t.Fatal("fixture digest size")
	}
}
