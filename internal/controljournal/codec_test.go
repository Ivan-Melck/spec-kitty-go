package controljournal

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
)

func mutateObjectField(t *testing.T, encoded []byte, field string, replacement json.RawMessage, remove bool) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	if remove {
		delete(object, field)
	} else {
		object[field] = append(json.RawMessage(nil), replacement...)
	}
	mutant, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return mutant
}

func assertRequiredFields(t *testing.T, encoded []byte, required []string, decode func([]byte) error) {
	t.Helper()
	for _, field := range required {
		t.Run("missing/"+field, func(t *testing.T) {
			if err := decode(mutateObjectField(t, encoded, field, nil, true)); err == nil {
				t.Fatalf("missing required field %s accepted", field)
			}
		})
		t.Run("null/"+field, func(t *testing.T) {
			if err := decode(mutateObjectField(t, encoded, field, json.RawMessage("null"), false)); err == nil {
				t.Fatalf("null required field %s accepted", field)
			}
		})
	}
}

func TestCodecEventGoldenRoundTrip(t *testing.T) {
	envelope := fixtureEnvelope(t, "event-1", "operation-1", 0, 1, "agg-1", 1)
	encoded, err := EncodeEventEnvelopeV1(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEventEnvelopeV1(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodeEventEnvelopeV1(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("round trip changed bytes:\n%s\n%s", encoded, reencoded)
	}
	if strings.Contains(string(encoded), "record_revision") || strings.Contains(string(encoded), "current_revision") {
		t.Fatalf("storage revision leaked into event bytes: %s", encoded)
	}
}

func TestCodecEventPreservesOptionalObservationAbsence(t *testing.T) {
	input := EventEnvelopeInputV1{EnvelopeVersion: 1, EventID: "event-1", EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1", ProjectID: "project-1", AggregateKind: "mission", AggregateID: "agg-1", AggregateLogicalVersion: 1, OperationID: "operation-1", RuntimeEpochID: "epoch-1", EventIndex: 0, EventCount: 1, Payload: []byte(`{}`)}
	envelope, err := NewEventEnvelopeV1(input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeEventEnvelopeV1(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("recorded_observations")) {
		t.Fatalf("absent optional observations were materialized: %s", encoded)
	}
	if _, err := DecodeEventEnvelopeV1(encoded); err != nil {
		t.Fatalf("absent optional observations rejected: %v", err)
	}
	input.Payload = nil
	if _, err := NewEventEnvelopeV1(input); err == nil {
		t.Fatal("missing required payload defaulted to an empty object")
	}
}

func TestCodecAggregateHeadGoldenRoundTrip(t *testing.T) {
	digest := sha256.Sum256([]byte("state"))
	head, err := ConstructAggregateHeadV1("mission", "agg-1", 7, "epoch-1", "event-7", digest)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeAggregateHeadV1(head)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAggregateHeadV1(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodeAggregateHeadV1(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("aggregate head codec changed bytes: %s != %s", encoded, reencoded)
	}
	if strings.Contains(string(encoded), "record_revision") || strings.Contains(string(encoded), "runtime_epoch_revision") {
		t.Fatalf("coordinate conflation in head: %s", encoded)
	}
}

func TestCodecCanonicalMapOrder(t *testing.T) {
	a, err := CanonicalJSONObject([]byte(`{"z":1,"nested":{"b":2,"a":1},"a":0}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalJSONObject([]byte(` { "a": 0, "nested": {"a":1,"b":2}, "z":1 } `))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("canonical map order differs: %s != %s", a, b)
	}
}

func TestCodecRefusals(t *testing.T) {
	base, err := EncodeEventEnvelopeV1(fixtureEnvelope(t, "event-1", "operation-1", 0, 1, "agg-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	unknown := append([]byte(`{"unknown":1,`), base[1:]...)
	duplicate := bytes.Replace(base, []byte(`"aggregate_id":"agg-1"`), []byte(`"aggregate_id":"agg-1","aggregate_id":"agg-2"`), 1)
	nestedDuplicate := []byte(`{"payload":{"x":1,"x":2}}`)
	invalidUTF8 := []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}
	for name, payload := range map[string][]byte{
		"unknown": unknown, "duplicate": duplicate, "nested duplicate": nestedDuplicate,
		"trailing": append(append([]byte(nil), base...), []byte(" true")...), "invalid utf8": invalidUTF8,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeEventEnvelopeV1(payload); err == nil {
				t.Fatal("invalid codec input accepted")
			}
		})
	}
	over := append([]byte(`{"value":"`), bytes.Repeat([]byte{'x'}, MaxOrdinaryValueBytes)...)
	over = append(over, []byte(`"}`)...)
	if _, err := CanonicalJSONObject(over); err == nil {
		t.Fatal("boundary plus one accepted")
	}
}

func TestCodecUnknownAndWrongDigestFailBeforePublication(t *testing.T) {
	envelope := fixtureEnvelope(t, "event-1", "operation-1", 0, 1, "agg-1", 1)
	encoded, err := EncodeEventEnvelopeV1(envelope)
	if err != nil {
		t.Fatal(err)
	}
	mutated := bytes.Replace(encoded, []byte(fixtureEventDigest), []byte(strings.Repeat("f", 64)), 1)
	if _, err := DecodeEventEnvelopeV1(mutated); err == nil {
		t.Fatal("wrong nonempty digest accepted")
	}
	version := bytes.Replace(encoded, []byte(`"envelope_version":1`), []byte(`"envelope_version":2`), 1)
	if _, err := DecodeEventEnvelopeV1(version); err == nil {
		t.Fatal("unknown envelope version accepted")
	}
}

func TestCodecEventIndexRejectsBeforeNarrowing(t *testing.T) {
	encoded, err := EncodeEventEnvelopeV1(fixtureEnvelope(t, "event-1", "operation-1", 0, 1, "agg-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"128", "255", "256", "65535", "65536"} {
		t.Run(index, func(t *testing.T) {
			mutant := mutateObjectField(t, encoded, "event_index", json.RawMessage(index), false)
			if got, err := DecodeEventEnvelopeV1(mutant); err == nil {
				t.Fatalf("wide index narrowed and accepted: %#v", got)
			}
		})
	}
}

func TestCodecEventAndHeadRequiredFields(t *testing.T) {
	eventBytes, err := EncodeEventEnvelopeV1(fixtureEnvelope(t, "event-1", "operation-1", 0, 1, "agg-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	assertRequiredFields(t, eventBytes, []string{"envelope_version", "event_type", "event_schema_version", "reducer_version", "event_id", "operation_id", "project_id", "aggregate_kind", "aggregate_id", "aggregate_logical_version", "runtime_epoch_id", "event_index", "event_count", "payload", "canonical_event_digest"}, func(value []byte) error { _, err := DecodeEventEnvelopeV1(value); return err })
	if _, err := DecodeEventEnvelopeV1(mutateObjectField(t, eventBytes, "payload", json.RawMessage("[]"), false)); err == nil {
		t.Fatal("array payload accepted")
	}
	digest := sha256.Sum256([]byte("state"))
	head, err := ConstructAggregateHeadV1("mission", "agg-1", 1, "epoch-1", "event-1", digest)
	if err != nil {
		t.Fatal(err)
	}
	headBytes, err := EncodeAggregateHeadV1(head)
	if err != nil {
		t.Fatal(err)
	}
	assertRequiredFields(t, headBytes, []string{"aggregate_id", "aggregate_kind", "last_event_id", "logical_version", "runtime_epoch_id", "schema_version", "canonical_state_digest"}, func(value []byte) error { _, err := DecodeAggregateHeadV1(value); return err })
}

func TestCodecCanonicalSHA256Grammar(t *testing.T) {
	eventBytes, _ := EncodeEventEnvelopeV1(fixtureEnvelope(t, "event-1", "operation-1", 0, 1, "agg-1", 1))
	digest := sha256.Sum256([]byte("state"))
	head, _ := ConstructAggregateHeadV1("mission", "agg-1", 1, "epoch-1", "event-1", digest)
	headBytes, _ := EncodeAggregateHeadV1(head)
	mutants := []string{strings.ToUpper(fixtureEventDigest), "A" + fixtureEventDigest[1:], fixtureEventDigest[:63], fixtureEventDigest + "0"}
	for _, mutant := range mutants {
		if _, err := DecodeEventEnvelopeV1(mutateObjectField(t, eventBytes, "canonical_event_digest", json.RawMessage(`"`+mutant+`"`), false)); err == nil {
			t.Fatalf("noncanonical event hash accepted: %q", mutant)
		}
	}
	headHash := strings.Repeat("a", 64)
	for _, mutant := range []string{strings.ToUpper(headHash), "A" + headHash[1:], headHash[:63], headHash + "0"} {
		if _, err := DecodeAggregateHeadV1(mutateObjectField(t, headBytes, "canonical_state_digest", json.RawMessage(`"`+mutant+`"`), false)); err == nil {
			t.Fatalf("noncanonical head hash accepted: %q", mutant)
		}
	}
}

func FuzzCodecCanonicalObjectRoundTrip(f *testing.F) {
	for _, corpus := range []string{`{}`, `{"a":1}`, `{"nested":{"z":2,"a":1}}`, `{"array":[3,2,1]}`} {
		f.Add(corpus)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			t.Skip()
		}
		first, err := CanonicalJSONObject([]byte(input))
		if err != nil {
			return
		}
		second, err := CanonicalJSONObject(first)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("canonicalization is not idempotent: %s != %s", first, second)
		}
	})
}
