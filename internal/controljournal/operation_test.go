package controljournal

import (
	"bytes"
	"crypto/sha256"
	"strings"
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
	matching, err := NewProviderObservationV1("operation-1", "effect-1", "github", "rev-7", "marker-1", []byte(`{"assignee":"actor-a"}`))
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

func TestOperationCompensationDecisionRejectsIncompleteAndTamperedValues(t *testing.T) {
	intent, err := NewEffectIntentV1("intent-1", "effect-1", "operation-1", "github", "rev-7", "marker-1", []byte(`{"assignee":"old"}`), []byte(`{"assignee":"actor-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	observation, err := NewProviderObservationV1("operation-1", "effect-1", "github", "rev-7", "marker-1", []byte(`{"assignee":"actor-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if CanCompensateEffectV1(EffectIntentV1{SchemaVersion: 1}, ProviderObservationV1{SchemaVersion: 1}) {
		t.Fatal("zero-ish values became compensable")
	}
	intentMutants := map[string]EffectIntentV1{"zero": {}, "wrong schema": intent, "missing intent": intent, "wrong operation": intent, "wrong effect": intent, "wrong provider": intent, "wrong revision": intent, "wrong marker": intent, "malformed expected": intent, "malformed desired": intent}
	mutantIntent := intentMutants["wrong schema"]
	mutantIntent.SchemaVersion = 2
	intentMutants["wrong schema"] = mutantIntent
	mutantIntent = intentMutants["missing intent"]
	mutantIntent.IntentID = ""
	intentMutants["missing intent"] = mutantIntent
	mutantIntent = intentMutants["wrong operation"]
	mutantIntent.OperationID = "operation-2"
	intentMutants["wrong operation"] = mutantIntent
	mutantIntent = intentMutants["wrong effect"]
	mutantIntent.EffectID = "effect-2"
	intentMutants["wrong effect"] = mutantIntent
	mutantIntent = intentMutants["wrong provider"]
	mutantIntent.Provider = "jira"
	intentMutants["wrong provider"] = mutantIntent
	mutantIntent = intentMutants["wrong revision"]
	mutantIntent.ExpectedRevision = "rev-8"
	intentMutants["wrong revision"] = mutantIntent
	mutantIntent = intentMutants["wrong marker"]
	mutantIntent.OperationMarker = "marker-2"
	intentMutants["wrong marker"] = mutantIntent
	mutantIntent = intentMutants["malformed expected"]
	mutantIntent.expectedValue = []byte("{")
	intentMutants["malformed expected"] = mutantIntent
	mutantIntent = intentMutants["malformed desired"]
	mutantIntent.desiredValue = []byte("{")
	intentMutants["malformed desired"] = mutantIntent
	for name, mutant := range intentMutants {
		t.Run("intent/"+name, func(t *testing.T) {
			if CanCompensateEffectV1(mutant, observation) {
				t.Fatal("intent mutant became compensable")
			}
		})
	}
	observationMutants := map[string]ProviderObservationV1{"zero": {}, "wrong schema": observation, "wrong operation": observation, "wrong effect": observation, "wrong provider": observation, "wrong revision": observation, "wrong marker": observation, "wrong value": observation, "malformed value": observation}
	mutantObservation := observationMutants["wrong schema"]
	mutantObservation.SchemaVersion = 2
	observationMutants["wrong schema"] = mutantObservation
	mutantObservation = observationMutants["wrong operation"]
	mutantObservation.OperationID = "operation-2"
	observationMutants["wrong operation"] = mutantObservation
	mutantObservation = observationMutants["wrong effect"]
	mutantObservation.EffectID = "effect-2"
	observationMutants["wrong effect"] = mutantObservation
	mutantObservation = observationMutants["wrong provider"]
	mutantObservation.Provider = "jira"
	observationMutants["wrong provider"] = mutantObservation
	mutantObservation = observationMutants["wrong revision"]
	mutantObservation.Revision = "rev-8"
	observationMutants["wrong revision"] = mutantObservation
	mutantObservation = observationMutants["wrong marker"]
	mutantObservation.OperationMarker = "marker-2"
	observationMutants["wrong marker"] = mutantObservation
	mutantObservation = observationMutants["wrong value"]
	mutantObservation.value = []byte(`{"assignee":"actor-b"}`)
	observationMutants["wrong value"] = mutantObservation
	mutantObservation = observationMutants["malformed value"]
	mutantObservation.value = []byte("{")
	observationMutants["malformed value"] = mutantObservation
	for name, mutant := range observationMutants {
		t.Run("observation/"+name, func(t *testing.T) {
			if CanCompensateEffectV1(intent, mutant) {
				t.Fatal("observation mutant became compensable")
			}
		})
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
	observation, err := NewProviderObservationV1("operation-1", "effect-1", provider, revision, marker, []byte(value))
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

func TestOperationDurableCodecRequiredFieldsAndShapes(t *testing.T) {
	intent, _ := NewEffectIntentV1("intent-1", "effect-1", "operation-1", "github", "rev-7", "marker-1", []byte(`{}`), []byte(`{"assignee":"actor-a"}`))
	intentBytes, _ := EncodeEffectIntentV1(intent)
	assertRequiredFields(t, intentBytes, []string{"desired_value", "effect_id", "expected_revision", "expected_value", "intent_id", "operation_id", "operation_marker", "provider", "schema_version"}, func(value []byte) error { _, err := DecodeEffectIntentV1(value); return err })
	observation, _ := NewProviderObservationV1("operation-1", "effect-1", "github", "rev-7", "marker-1", []byte(`{"assignee":"actor-a"}`))
	observationBytes, _ := EncodeProviderObservationV1(observation)
	assertRequiredFields(t, observationBytes, []string{"effect_id", "operation_id", "operation_marker", "provider", "revision", "schema_version", "value"}, func(value []byte) error { _, err := DecodeProviderObservationV1(value); return err })
	outcome, _ := NewOperationOutcomeV1("operation-1", OperationDurablyApplied, "command.result", []byte(`{}`))
	outcomeBytes, _ := EncodeOperationOutcomeV1(outcome)
	assertRequiredFields(t, outcomeBytes, []string{"delivery", "kind", "operation_id", "recovery", "result", "result_type", "schema_version"}, func(value []byte) error { _, err := DecodeOperationOutcomeV1(value); return err })

	request := sha256.Sum256([]byte("request"))
	preconditions := sha256.Sum256([]byte("preconditions"))
	record, err := NewOperationRecordV1(OperationRecordInputV1{OperationID: "operation-1", CommandType: "mission.start", RequestDigest: request, RuntimeEpochID: "epoch-1", ExpectedPreconditionsDigest: preconditions, ResultType: "mission.started", ResultSchemaVersion: 1, CanonicalResult: []byte(`{}`), EventIDs: []EventID{"event-1"}, EffectIDs: []EffectID{}})
	if err != nil {
		t.Fatal(err)
	}
	recordBytes, err := EncodeOperationRecordV1(record)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(recordBytes, []byte(`"effect_ids":[]`)) {
		t.Fatalf("zero effects did not encode as array: %s", recordBytes)
	}
	decodedRecord, err := DecodeOperationRecordV1(recordBytes)
	if err != nil || decodedRecord.EffectIDs() == nil || len(decodedRecord.EffectIDs()) != 0 {
		t.Fatalf("empty effect array did not round trip: %s %#v %v", recordBytes, decodedRecord.EffectIDs(), err)
	}
	assertRequiredFields(t, recordBytes, []string{"canonical_result", "command_type", "effect_ids", "event_ids", "expected_preconditions_digest", "operation_id", "request_digest", "result_schema_version", "result_type", "runtime_epoch_id", "schema_version", "status"}, func(value []byte) error { _, err := DecodeOperationRecordV1(value); return err })
	for field, shape := range map[string]string{"effect_ids": `{}`, "event_ids": `{}`, "canonical_result": `[]`} {
		if _, err := DecodeOperationRecordV1(mutateObjectField(t, recordBytes, field, []byte(shape), false)); err == nil {
			t.Fatalf("wrong shape for %s accepted", field)
		}
	}
}

func TestOperationRecordCanonicalSHA256Grammar(t *testing.T) {
	request := sha256.Sum256([]byte("request"))
	preconditions := sha256.Sum256([]byte("preconditions"))
	record, _ := NewOperationRecordV1(OperationRecordInputV1{OperationID: "operation-1", CommandType: "mission.start", RequestDigest: request, RuntimeEpochID: "epoch-1", ExpectedPreconditionsDigest: preconditions, ResultType: "mission.started", ResultSchemaVersion: 1, CanonicalResult: []byte(`{}`), EventIDs: []EventID{"event-1"}})
	encoded, _ := EncodeOperationRecordV1(record)
	base := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, field := range []string{"request_digest", "expected_preconditions_digest"} {
		for _, mutant := range []string{strings.ToUpper(base), "A" + base[1:], base[:63], base + "0"} {
			if _, err := DecodeOperationRecordV1(mutateObjectField(t, encoded, field, []byte(`"`+mutant+`"`), false)); err == nil {
				t.Fatalf("noncanonical %s accepted: %q", field, mutant)
			}
		}
	}
}
