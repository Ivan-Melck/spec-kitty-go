package controljournal

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

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
