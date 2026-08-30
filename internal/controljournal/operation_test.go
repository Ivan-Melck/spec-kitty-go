package controljournal

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestOperationIdentityStableAcrossRetry(t *testing.T) {
	one, err := NewOperationOutcomeV1("operation-1", OperationAmbiguous, "command.result", []byte(`{"accepted":true}`))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := NewOperationOutcomeV1("operation-1", OperationAmbiguous, "command.result", []byte(`{"accepted":true}`))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := NewOperationOutcomeV1("operation-2", OperationAmbiguous, "command.result", []byte(`{"accepted":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if one.OperationID != retry.OperationID {
		t.Fatal("stable retry identity changed")
	}
	if one.OperationID == changed.OperationID {
		t.Fatal("changed operation identity was hidden")
	}
}

func TestOperationAmbiguousReconciliation(t *testing.T) {
	ambiguous, err := NewOperationOutcomeV1("operation-1", OperationAmbiguous, "command.result", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, resolution := range []OperationObservationKind{OperationDurablyApplied, OperationRefused} {
		resolved, err := ReconcileAmbiguousOperationV1(ambiguous, resolution)
		if err != nil || resolved.Kind != OperationReconciled || resolved.Resolution != resolution {
			t.Fatalf("resolution %s: %#v %v", resolution, resolved, err)
		}
	}
	if _, err := ReconcileAmbiguousOperationV1(ambiguous, OperationConflict); err == nil {
		t.Fatal("ambiguous reconciled to unsupported observation")
	}
}

func TestOperationAcceptanceDeliveryAndReadinessAreSeparate(t *testing.T) {
	outcome, err := NewOperationOutcomeV1("operation-1", OperationDurablyApplied, "command.result", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	dropped, err := outcome.WithDelivery(DeliveryDropped)
	if err != nil {
		t.Fatal(err)
	}
	if dropped.Kind != OperationDurablyApplied || dropped.Delivery != DeliveryDropped {
		t.Fatal("response drop changed durable acceptance")
	}
	bytesOnly, err := dropped.WithRecovery(RecoveryBytesRestored)
	if err != nil {
		t.Fatal(err)
	}
	if bytesOnly.Recovery == RecoveryProductReady {
		t.Fatal("byte recovery implied product readiness")
	}
}

func TestOperationCompareBeforeCompensateAttribution(t *testing.T) {
	intent, err := NewEffectIntentV1("intent-1", "effect-1", "operation-1", "github", "rev-7", "marker-1", []byte(`{"assignee":"old"}`), []byte(`{"assignee":"actor-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	matching, err := NewProviderObservationV1("github", "rev-7", "marker-1", []byte(`{"assignee":"actor-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !CanCompensateEffectV1(intent, matching) {
		t.Fatal("attributed provider state not compensable")
	}
	for _, observation := range []ProviderObservationV1{
		mustObservation(t, "github", "rev-8", "marker-1", `{"assignee":"actor-a"}`),
		mustObservation(t, "github", "rev-7", "human", `{"assignee":"actor-a"}`),
		mustObservation(t, "github", "rev-7", "marker-1", `{"assignee":"actor-b"}`),
	} {
		if CanCompensateEffectV1(intent, observation) {
			t.Fatalf("unattributed provider state became compensable: %#v", observation)
		}
	}
}

func TestOperationEffectAndObservationCodecRoundTrips(t *testing.T) {
	intent, err := NewEffectIntentV1("intent-1", "effect-1", "operation-1", "github", "rev-7", "marker-1", []byte(`{"assignee":"old"}`), []byte(`{"assignee":"actor-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	intentBytes, err := EncodeEffectIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	decodedIntent, err := DecodeEffectIntentV1(intentBytes)
	if err != nil {
		t.Fatal(err)
	}
	reencodedIntent, err := EncodeEffectIntentV1(decodedIntent)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(intentBytes, reencodedIntent) {
		t.Fatalf("effect intent codec changed bytes: %s != %s", intentBytes, reencodedIntent)
	}

	observation := mustObservation(t, "github", "rev-7", "marker-1", `{"assignee":"actor-a"}`)
	observationBytes, err := EncodeProviderObservationV1(observation)
	if err != nil {
		t.Fatal(err)
	}
	decodedObservation, err := DecodeProviderObservationV1(observationBytes)
	if err != nil {
		t.Fatal(err)
	}
	reencodedObservation, err := EncodeProviderObservationV1(decodedObservation)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(observationBytes, reencodedObservation) {
		t.Fatalf("provider observation codec changed bytes: %s != %s", observationBytes, reencodedObservation)
	}
	if !CanCompensateEffectV1(decodedIntent, decodedObservation) {
		t.Fatal("codec lost compensation attribution")
	}
}

func TestOperationOutcomeCodecRoundTripsEveryKind(t *testing.T) {
	for _, kind := range []OperationObservationKind{OperationProposed, OperationDurablyApplied, OperationRefused, OperationConflict, OperationAmbiguous} {
		t.Run(string(kind), func(t *testing.T) {
			outcome, err := NewOperationOutcomeV1("operation-1", kind, "command.result", []byte(`{"value":1}`))
			if err != nil {
				t.Fatal(err)
			}
			outcome, err = outcome.WithDelivery(DeliveryReturned)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeOperationOutcomeV1(outcome)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeOperationOutcomeV1(encoded)
			if err != nil {
				t.Fatal(err)
			}
			reencoded, err := EncodeOperationOutcomeV1(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(encoded, reencoded) {
				t.Fatalf("outcome codec changed bytes: %s != %s", encoded, reencoded)
			}
		})
	}
	ambiguous, _ := NewOperationOutcomeV1("operation-1", OperationAmbiguous, "command.result", []byte(`{}`))
	reconciled, _ := ReconcileAmbiguousOperationV1(ambiguous, OperationDurablyApplied)
	encoded, err := EncodeOperationOutcomeV1(reconciled)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOperationOutcomeV1(encoded)
	if err != nil || decoded.Kind != OperationReconciled || decoded.Resolution != OperationDurablyApplied {
		t.Fatalf("reconciled outcome = %#v %v", decoded, err)
	}
}

func mustObservation(t *testing.T, provider, revision, marker, value string) ProviderObservationV1 {
	t.Helper()
	observation, err := NewProviderObservationV1(provider, revision, marker, []byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func TestOperationCodecGoldenRoundTripAndCopies(t *testing.T) {
	request := sha256.Sum256([]byte("request"))
	preconditions := sha256.Sum256([]byte("preconditions"))
	result := []byte(`{"z":2,"a":1}`)
	eventIDs := []EventID{"event-1"}
	effectIDs := []EffectID{"effect-1"}
	record, err := NewOperationRecordV1(OperationRecordInputV1{OperationID: "operation-1", CommandType: "mission.start", RequestDigest: request, RuntimeEpochID: "epoch-1", ExpectedPreconditionsDigest: preconditions, ResultType: "mission.started", ResultSchemaVersion: 1, CanonicalResult: result, EventIDs: eventIDs, EffectIDs: effectIDs})
	if err != nil {
		t.Fatal(err)
	}
	result[2] = 'X'
	eventIDs[0] = "changed"
	effectIDs[0] = "changed"
	encoded, err := EncodeOperationRecordV1(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOperationRecordV1(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodeOperationRecordV1(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("operation codec changed bytes:\n%s\n%s", encoded, reencoded)
	}
	if decoded.EventIDs()[0] != "event-1" || decoded.EffectIDs()[0] != "effect-1" || string(decoded.CanonicalResult()) != `{"a":1,"z":2}` {
		t.Fatal("operation input mutation escaped copies")
	}
}
