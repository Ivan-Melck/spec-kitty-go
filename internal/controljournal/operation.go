package controljournal

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
)

type OperationObservationKind string

const (
	OperationProposed       OperationObservationKind = "proposed"
	OperationDurablyApplied OperationObservationKind = "durably_applied"
	OperationRefused        OperationObservationKind = "refused"
	OperationConflict       OperationObservationKind = "conflict"
	OperationAmbiguous      OperationObservationKind = "ambiguous"
	OperationReconciled     OperationObservationKind = "reconciled"
)

type DeliveryObservation string

const (
	DeliveryNotAttempted DeliveryObservation = "not_attempted"
	DeliveryReturned     DeliveryObservation = "returned"
	DeliveryDropped      DeliveryObservation = "dropped"
)

type RecoveryObservation string

const (
	RecoveryBytesUnavailable RecoveryObservation = "bytes_unavailable"
	RecoveryBytesRestored    RecoveryObservation = "bytes_restored"
	RecoveryProductReady     RecoveryObservation = "product_ready"
)

type OperationOutcomeV1 struct {
	SchemaVersion uint32
	OperationID   OperationID
	Kind          OperationObservationKind
	Resolution    OperationObservationKind
	Delivery      DeliveryObservation
	Recovery      RecoveryObservation
	ResultType    string
	result        []byte
}

func NewOperationOutcomeV1(operationID OperationID, kind OperationObservationKind, resultType string, result []byte) (OperationOutcomeV1, error) {
	if err := validateToken("operation_id", string(operationID)); err != nil {
		return OperationOutcomeV1{}, err
	}
	if !validOperationKind(kind) || kind == OperationReconciled {
		return OperationOutcomeV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "operation_kind"}
	}
	if resultType != "" {
		if err := validateToken("result_type", resultType); err != nil {
			return OperationOutcomeV1{}, err
		}
	}
	canonical, err := canonicalJSONObject(result, "operation_result")
	if err != nil {
		return OperationOutcomeV1{}, err
	}
	return OperationOutcomeV1{SchemaVersion: 1, OperationID: operationID, Kind: kind, Delivery: DeliveryNotAttempted, Recovery: RecoveryBytesUnavailable, ResultType: resultType, result: canonical}, nil
}

func (o OperationOutcomeV1) Result() []byte { return append([]byte(nil), o.result...) }

func (o OperationOutcomeV1) WithDelivery(delivery DeliveryObservation) (OperationOutcomeV1, error) {
	if delivery != DeliveryNotAttempted && delivery != DeliveryReturned && delivery != DeliveryDropped {
		return OperationOutcomeV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "delivery"}
	}
	o.result = append([]byte(nil), o.result...)
	o.Delivery = delivery
	return o, nil
}

func (o OperationOutcomeV1) WithRecovery(recovery RecoveryObservation) (OperationOutcomeV1, error) {
	if recovery != RecoveryBytesUnavailable && recovery != RecoveryBytesRestored && recovery != RecoveryProductReady {
		return OperationOutcomeV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "recovery"}
	}
	if recovery == RecoveryProductReady && o.Kind != OperationDurablyApplied && o.Resolution != OperationDurablyApplied {
		return OperationOutcomeV1{}, ValidationError{Kind: RefusalInvalidTransition, Field: "recovery"}
	}
	o.result = append([]byte(nil), o.result...)
	o.Recovery = recovery
	return o, nil
}

func ReconcileAmbiguousOperationV1(outcome OperationOutcomeV1, resolution OperationObservationKind) (OperationOutcomeV1, error) {
	if outcome.Kind != OperationAmbiguous || resolution != OperationDurablyApplied && resolution != OperationRefused {
		return OperationOutcomeV1{}, ValidationError{Kind: RefusalInvalidTransition, Field: "operation_reconciliation"}
	}
	outcome.result = append([]byte(nil), outcome.result...)
	outcome.Kind = OperationReconciled
	outcome.Resolution = resolution
	return outcome, nil
}

func validOperationKind(kind OperationObservationKind) bool {
	switch kind {
	case OperationProposed, OperationDurablyApplied, OperationRefused, OperationConflict, OperationAmbiguous, OperationReconciled:
		return true
	default:
		return false
	}
}

func validDelivery(delivery DeliveryObservation) bool {
	return delivery == DeliveryNotAttempted || delivery == DeliveryReturned || delivery == DeliveryDropped
}

func validRecovery(recovery RecoveryObservation) bool {
	return recovery == RecoveryBytesUnavailable || recovery == RecoveryBytesRestored || recovery == RecoveryProductReady
}

type OperationRecordInputV1 struct {
	OperationID                 OperationID
	CommandType                 string
	RequestDigest               [32]byte
	RuntimeEpochID              RuntimeEpochID
	ExpectedPreconditionsDigest [32]byte
	ResultType                  string
	ResultSchemaVersion         uint32
	CanonicalResult             []byte
	EventIDs                    []EventID
	EffectIDs                   []EffectID
}

type OperationRecordV1 struct {
	SchemaVersion               uint32
	OperationID                 OperationID
	CommandType                 string
	RequestDigest               [32]byte
	RuntimeEpochID              RuntimeEpochID
	ExpectedPreconditionsDigest [32]byte
	Status                      string
	ResultType                  string
	ResultSchemaVersion         uint32
	canonicalResult             []byte
	eventIDs                    []EventID
	effectIDs                   []EffectID
}

func NewOperationRecordV1(input OperationRecordInputV1) (OperationRecordV1, error) {
	if err := validateTokens(tokenField{"operation_id", string(input.OperationID)}, tokenField{"command_type", input.CommandType}, tokenField{"runtime_epoch_id", string(input.RuntimeEpochID)}, tokenField{"result_type", input.ResultType}); err != nil {
		return OperationRecordV1{}, err
	}
	if input.RequestDigest == [32]byte{} || input.ExpectedPreconditionsDigest == [32]byte{} || input.ResultSchemaVersion == 0 {
		return OperationRecordV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "operation_version_or_digest"}
	}
	result, err := canonicalJSONObject(input.CanonicalResult, "canonical_result")
	if err != nil {
		return OperationRecordV1{}, err
	}
	if len(input.EventIDs) == 0 || len(input.EventIDs) > maxEventsPerGroup || len(input.EffectIDs) > 16 {
		return OperationRecordV1{}, ValidationError{Kind: RefusalBudgetExceeded, Field: "operation_members"}
	}
	seen := make(map[string]bool, len(input.EventIDs)+len(input.EffectIDs))
	for _, id := range input.EventIDs {
		if err := validateToken("event_id", string(id)); err != nil {
			return OperationRecordV1{}, err
		}
		if seen["event:"+string(id)] {
			return OperationRecordV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "duplicate_event_id"}
		}
		seen["event:"+string(id)] = true
	}
	for _, id := range input.EffectIDs {
		if err := validateToken("effect_id", string(id)); err != nil {
			return OperationRecordV1{}, err
		}
		if seen["effect:"+string(id)] {
			return OperationRecordV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "duplicate_effect_id"}
		}
		seen["effect:"+string(id)] = true
	}
	return OperationRecordV1{
		SchemaVersion: 1, OperationID: input.OperationID, CommandType: input.CommandType,
		RequestDigest: input.RequestDigest, RuntimeEpochID: input.RuntimeEpochID,
		ExpectedPreconditionsDigest: input.ExpectedPreconditionsDigest, Status: "accepted",
		ResultType: input.ResultType, ResultSchemaVersion: input.ResultSchemaVersion,
		canonicalResult: result, eventIDs: append(make([]EventID, 0, len(input.EventIDs)), input.EventIDs...), effectIDs: append(make([]EffectID, 0, len(input.EffectIDs)), input.EffectIDs...),
	}, nil
}

func (r OperationRecordV1) CanonicalResult() []byte { return append([]byte(nil), r.canonicalResult...) }
func (r OperationRecordV1) EventIDs() []EventID {
	return append(make([]EventID, 0, len(r.eventIDs)), r.eventIDs...)
}
func (r OperationRecordV1) EffectIDs() []EffectID {
	return append(make([]EffectID, 0, len(r.effectIDs)), r.effectIDs...)
}

type EffectIntentV1 struct {
	SchemaVersion    uint32
	IntentID         IntentID
	EffectID         EffectID
	OperationID      OperationID
	Provider         string
	ExpectedRevision string
	OperationMarker  string
	expectedValue    []byte
	desiredValue     []byte
}

func NewEffectIntentV1(intentID IntentID, effectID EffectID, operationID OperationID, provider, expectedRevision, marker string, expectedValue, desiredValue []byte) (EffectIntentV1, error) {
	if err := validateTokens(tokenField{"intent_id", string(intentID)}, tokenField{"effect_id", string(effectID)}, tokenField{"operation_id", string(operationID)}, tokenField{"provider", provider}, tokenField{"expected_revision", expectedRevision}, tokenField{"operation_marker", marker}); err != nil {
		return EffectIntentV1{}, err
	}
	expected, err := canonicalJSONObject(expectedValue, "expected_value")
	if err != nil {
		return EffectIntentV1{}, err
	}
	desired, err := canonicalJSONObject(desiredValue, "desired_value")
	if err != nil {
		return EffectIntentV1{}, err
	}
	return EffectIntentV1{SchemaVersion: 1, IntentID: intentID, EffectID: effectID, OperationID: operationID, Provider: provider, ExpectedRevision: expectedRevision, OperationMarker: marker, expectedValue: expected, desiredValue: desired}, nil
}

func (i EffectIntentV1) ExpectedValue() []byte { return append([]byte(nil), i.expectedValue...) }
func (i EffectIntentV1) DesiredValue() []byte  { return append([]byte(nil), i.desiredValue...) }

func (i EffectIntentV1) valid() bool {
	if i.SchemaVersion != 1 {
		return false
	}
	if err := validateTokens(tokenField{"intent_id", string(i.IntentID)}, tokenField{"effect_id", string(i.EffectID)}, tokenField{"operation_id", string(i.OperationID)}, tokenField{"provider", i.Provider}, tokenField{"expected_revision", i.ExpectedRevision}, tokenField{"operation_marker", i.OperationMarker}); err != nil {
		return false
	}
	expected, err := canonicalJSONObject(i.expectedValue, "expected_value")
	if err != nil || !bytes.Equal(expected, i.expectedValue) {
		return false
	}
	desired, err := canonicalJSONObject(i.desiredValue, "desired_value")
	return err == nil && bytes.Equal(desired, i.desiredValue)
}

type ProviderObservationV1 struct {
	SchemaVersion   uint32
	OperationID     OperationID
	EffectID        EffectID
	Provider        string
	Revision        string
	OperationMarker string
	value           []byte
}

func NewProviderObservationV1(operationID OperationID, effectID EffectID, provider, revision, marker string, value []byte) (ProviderObservationV1, error) {
	if err := validateTokens(tokenField{"operation_id", string(operationID)}, tokenField{"effect_id", string(effectID)}, tokenField{"provider", provider}, tokenField{"revision", revision}, tokenField{"operation_marker", marker}); err != nil {
		return ProviderObservationV1{}, err
	}
	canonical, err := canonicalJSONObject(value, "provider_value")
	if err != nil {
		return ProviderObservationV1{}, err
	}
	return ProviderObservationV1{SchemaVersion: 1, OperationID: operationID, EffectID: effectID, Provider: provider, Revision: revision, OperationMarker: marker, value: canonical}, nil
}

func (o ProviderObservationV1) Value() []byte { return append([]byte(nil), o.value...) }

func (o ProviderObservationV1) valid() bool {
	if o.SchemaVersion != 1 {
		return false
	}
	if err := validateTokens(tokenField{"operation_id", string(o.OperationID)}, tokenField{"effect_id", string(o.EffectID)}, tokenField{"provider", o.Provider}, tokenField{"revision", o.Revision}, tokenField{"operation_marker", o.OperationMarker}); err != nil {
		return false
	}
	canonical, err := canonicalJSONObject(o.value, "provider_value")
	return err == nil && bytes.Equal(canonical, o.value)
}

func CanCompensateEffectV1(intent EffectIntentV1, observation ProviderObservationV1) bool {
	return intent.valid() && observation.valid() &&
		intent.OperationID == observation.OperationID &&
		intent.EffectID == observation.EffectID &&
		intent.Provider == observation.Provider &&
		intent.ExpectedRevision == observation.Revision &&
		intent.OperationMarker == observation.OperationMarker &&
		bytes.Equal(intent.desiredValue, observation.value)
}

type effectIntentWireV1 struct {
	DesiredValue     json.RawMessage `json:"desired_value"`
	EffectID         string          `json:"effect_id"`
	ExpectedRevision string          `json:"expected_revision"`
	ExpectedValue    json.RawMessage `json:"expected_value"`
	IntentID         string          `json:"intent_id"`
	OperationID      string          `json:"operation_id"`
	OperationMarker  string          `json:"operation_marker"`
	Provider         string          `json:"provider"`
	SchemaVersion    uint32          `json:"schema_version"`
}

func EncodeEffectIntentV1(intent EffectIntentV1) ([]byte, error) {
	if intent.SchemaVersion != 1 {
		return nil, ValidationError{Kind: RefusalUnsupportedVersion, Field: "effect_intent"}
	}
	validated, err := NewEffectIntentV1(intent.IntentID, intent.EffectID, intent.OperationID, intent.Provider, intent.ExpectedRevision, intent.OperationMarker, intent.expectedValue, intent.desiredValue)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(effectIntentWireV1{DesiredValue: validated.DesiredValue(), EffectID: string(validated.EffectID), ExpectedRevision: validated.ExpectedRevision, ExpectedValue: validated.ExpectedValue(), IntentID: string(validated.IntentID), OperationID: string(validated.OperationID), OperationMarker: validated.OperationMarker, Provider: validated.Provider, SchemaVersion: 1})
	if err != nil || len(encoded) > MaxOrdinaryValueBytes {
		return nil, ValidationError{Kind: RefusalBudgetExceeded, Field: "effect_intent"}
	}
	return encoded, nil
}

func DecodeEffectIntentV1(encoded []byte) (EffectIntentV1, error) {
	canonical, err := decodeRequiredObject(encoded, "effect_intent", "desired_value", "effect_id", "expected_revision", "expected_value", "intent_id", "operation_id", "operation_marker", "provider", "schema_version")
	if err != nil {
		return EffectIntentV1{}, err
	}
	var wire effectIntentWireV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || wire.SchemaVersion != 1 {
		return EffectIntentV1{}, ValidationError{Kind: RefusalUnsupportedVersion, Field: "effect_intent"}
	}
	return NewEffectIntentV1(IntentID(wire.IntentID), EffectID(wire.EffectID), OperationID(wire.OperationID), wire.Provider, wire.ExpectedRevision, wire.OperationMarker, wire.ExpectedValue, wire.DesiredValue)
}

type providerObservationWireV1 struct {
	EffectID        string          `json:"effect_id"`
	OperationID     string          `json:"operation_id"`
	OperationMarker string          `json:"operation_marker"`
	Provider        string          `json:"provider"`
	Revision        string          `json:"revision"`
	SchemaVersion   uint32          `json:"schema_version"`
	Value           json.RawMessage `json:"value"`
}

func EncodeProviderObservationV1(observation ProviderObservationV1) ([]byte, error) {
	if observation.SchemaVersion != 1 {
		return nil, ValidationError{Kind: RefusalUnsupportedVersion, Field: "provider_observation"}
	}
	validated, err := NewProviderObservationV1(observation.OperationID, observation.EffectID, observation.Provider, observation.Revision, observation.OperationMarker, observation.value)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(providerObservationWireV1{EffectID: string(validated.EffectID), OperationID: string(validated.OperationID), OperationMarker: validated.OperationMarker, Provider: validated.Provider, Revision: validated.Revision, SchemaVersion: 1, Value: validated.Value()})
	if err != nil || len(encoded) > MaxOrdinaryValueBytes {
		return nil, ValidationError{Kind: RefusalBudgetExceeded, Field: "provider_observation"}
	}
	return encoded, nil
}

func DecodeProviderObservationV1(encoded []byte) (ProviderObservationV1, error) {
	canonical, err := decodeRequiredObject(encoded, "provider_observation", "effect_id", "operation_id", "operation_marker", "provider", "revision", "schema_version", "value")
	if err != nil {
		return ProviderObservationV1{}, err
	}
	var wire providerObservationWireV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || wire.SchemaVersion != 1 {
		return ProviderObservationV1{}, ValidationError{Kind: RefusalUnsupportedVersion, Field: "provider_observation"}
	}
	return NewProviderObservationV1(OperationID(wire.OperationID), EffectID(wire.EffectID), wire.Provider, wire.Revision, wire.OperationMarker, wire.Value)
}

type operationOutcomeWireV1 struct {
	Delivery      DeliveryObservation      `json:"delivery"`
	Kind          OperationObservationKind `json:"kind"`
	OperationID   string                   `json:"operation_id"`
	Recovery      RecoveryObservation      `json:"recovery"`
	Resolution    OperationObservationKind `json:"resolution,omitempty"`
	Result        json.RawMessage          `json:"result"`
	ResultType    string                   `json:"result_type"`
	SchemaVersion uint32                   `json:"schema_version"`
}

func EncodeOperationOutcomeV1(outcome OperationOutcomeV1) ([]byte, error) {
	if outcome.SchemaVersion != 1 || !validOperationKind(outcome.Kind) || !validDelivery(outcome.Delivery) || !validRecovery(outcome.Recovery) {
		return nil, ValidationError{Kind: RefusalUnsupportedVersion, Field: "operation_outcome"}
	}
	if err := validateToken("operation_id", string(outcome.OperationID)); err != nil {
		return nil, err
	}
	if outcome.ResultType != "" {
		if err := validateToken("result_type", outcome.ResultType); err != nil {
			return nil, err
		}
	}
	if _, err := canonicalJSONObject(outcome.result, "operation_result"); err != nil {
		return nil, err
	}
	if outcome.Kind == OperationReconciled && outcome.Resolution != OperationDurablyApplied && outcome.Resolution != OperationRefused {
		return nil, ValidationError{Kind: RefusalInvalidTransition, Field: "operation_reconciliation"}
	}
	if outcome.Kind != OperationReconciled && outcome.Resolution != "" {
		return nil, ValidationError{Kind: RefusalInvalidTransition, Field: "operation_reconciliation"}
	}
	if outcome.Recovery == RecoveryProductReady && outcome.Kind != OperationDurablyApplied && outcome.Resolution != OperationDurablyApplied {
		return nil, ValidationError{Kind: RefusalInvalidTransition, Field: "recovery"}
	}
	encoded, err := json.Marshal(operationOutcomeWireV1{Delivery: outcome.Delivery, Kind: outcome.Kind, OperationID: string(outcome.OperationID), Recovery: outcome.Recovery, Resolution: outcome.Resolution, Result: outcome.Result(), ResultType: outcome.ResultType, SchemaVersion: 1})
	if err != nil || len(encoded) > MaxOrdinaryValueBytes {
		return nil, ValidationError{Kind: RefusalBudgetExceeded, Field: "operation_outcome"}
	}
	return encoded, nil
}

func DecodeOperationOutcomeV1(encoded []byte) (OperationOutcomeV1, error) {
	canonical, err := decodeRequiredObject(encoded, "operation_outcome", "delivery", "kind", "operation_id", "recovery", "result", "result_type", "schema_version")
	if err != nil {
		return OperationOutcomeV1{}, err
	}
	var wire operationOutcomeWireV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || wire.SchemaVersion != 1 || !validOperationKind(wire.Kind) {
		return OperationOutcomeV1{}, ValidationError{Kind: RefusalUnsupportedVersion, Field: "operation_outcome"}
	}
	baseKind := wire.Kind
	if baseKind == OperationReconciled {
		baseKind = OperationAmbiguous
	}
	outcome, err := NewOperationOutcomeV1(OperationID(wire.OperationID), baseKind, wire.ResultType, wire.Result)
	if err != nil {
		return OperationOutcomeV1{}, err
	}
	if wire.Kind == OperationReconciled {
		outcome, err = ReconcileAmbiguousOperationV1(outcome, wire.Resolution)
		if err != nil {
			return OperationOutcomeV1{}, err
		}
	} else if wire.Resolution != "" {
		return OperationOutcomeV1{}, ValidationError{Kind: RefusalInvalidTransition, Field: "operation_reconciliation"}
	}
	outcome, err = outcome.WithDelivery(wire.Delivery)
	if err != nil {
		return OperationOutcomeV1{}, err
	}
	return outcome.WithRecovery(wire.Recovery)
}

type operationRecordWireV1 struct {
	CanonicalResult             json.RawMessage `json:"canonical_result"`
	CommandType                 string          `json:"command_type"`
	EffectIDs                   []string        `json:"effect_ids"`
	EventIDs                    []string        `json:"event_ids"`
	ExpectedPreconditionsDigest string          `json:"expected_preconditions_digest"`
	OperationID                 string          `json:"operation_id"`
	RequestDigest               string          `json:"request_digest"`
	ResultSchemaVersion         uint32          `json:"result_schema_version"`
	ResultType                  string          `json:"result_type"`
	RuntimeEpochID              string          `json:"runtime_epoch_id"`
	SchemaVersion               uint32          `json:"schema_version"`
	Status                      string          `json:"status"`
}

func EncodeOperationRecordV1(record OperationRecordV1) ([]byte, error) {
	if record.SchemaVersion != 1 || record.Status != "accepted" {
		return nil, ValidationError{Kind: RefusalUnsupportedVersion, Field: "operation_record"}
	}
	validated, err := NewOperationRecordV1(OperationRecordInputV1{
		OperationID: record.OperationID, CommandType: record.CommandType,
		RequestDigest: record.RequestDigest, RuntimeEpochID: record.RuntimeEpochID,
		ExpectedPreconditionsDigest: record.ExpectedPreconditionsDigest,
		ResultType:                  record.ResultType, ResultSchemaVersion: record.ResultSchemaVersion,
		CanonicalResult: record.canonicalResult, EventIDs: record.eventIDs, EffectIDs: record.effectIDs,
	})
	if err != nil {
		return nil, err
	}
	wire := operationRecordWireV1{
		CanonicalResult: validated.CanonicalResult(), CommandType: validated.CommandType,
		EffectIDs:                   make([]string, 0, len(validated.effectIDs)),
		EventIDs:                    make([]string, 0, len(validated.eventIDs)),
		ExpectedPreconditionsDigest: hex.EncodeToString(validated.ExpectedPreconditionsDigest[:]),
		OperationID:                 string(validated.OperationID), RequestDigest: hex.EncodeToString(validated.RequestDigest[:]),
		ResultSchemaVersion: validated.ResultSchemaVersion, ResultType: validated.ResultType,
		RuntimeEpochID: string(validated.RuntimeEpochID), SchemaVersion: validated.SchemaVersion, Status: validated.Status,
	}
	for _, id := range validated.EventIDs() {
		wire.EventIDs = append(wire.EventIDs, string(id))
	}
	for _, id := range validated.EffectIDs() {
		wire.EffectIDs = append(wire.EffectIDs, string(id))
	}
	encoded, err := json.Marshal(wire)
	if err != nil || len(encoded) > MaxOrdinaryValueBytes {
		return nil, ValidationError{Kind: RefusalBudgetExceeded, Field: "operation_record"}
	}
	return encoded, nil
}

func DecodeOperationRecordV1(encoded []byte) (OperationRecordV1, error) {
	canonical, err := decodeRequiredObject(encoded, "operation_record", "canonical_result", "command_type", "effect_ids", "event_ids", "expected_preconditions_digest", "operation_id", "request_digest", "result_schema_version", "result_type", "runtime_epoch_id", "schema_version", "status")
	if err != nil {
		return OperationRecordV1{}, err
	}
	var wire operationRecordWireV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || wire.SchemaVersion != 1 || wire.Status != "accepted" {
		return OperationRecordV1{}, ValidationError{Kind: RefusalUnsupportedVersion, Field: "operation_record"}
	}
	requestDigest, err := parseCanonicalSHA256("request_digest", wire.RequestDigest)
	if err != nil {
		return OperationRecordV1{}, err
	}
	preconditionsDigest, err := parseCanonicalSHA256("expected_preconditions_digest", wire.ExpectedPreconditionsDigest)
	if err != nil {
		return OperationRecordV1{}, err
	}
	events := make([]EventID, len(wire.EventIDs))
	for i, id := range wire.EventIDs {
		events[i] = EventID(id)
	}
	effects := make([]EffectID, len(wire.EffectIDs))
	for i, id := range wire.EffectIDs {
		effects[i] = EffectID(id)
	}
	return NewOperationRecordV1(OperationRecordInputV1{
		OperationID: OperationID(wire.OperationID), CommandType: wire.CommandType,
		RequestDigest: requestDigest, RuntimeEpochID: RuntimeEpochID(wire.RuntimeEpochID),
		ExpectedPreconditionsDigest: preconditionsDigest, ResultType: wire.ResultType,
		ResultSchemaVersion: wire.ResultSchemaVersion, CanonicalResult: wire.CanonicalResult,
		EventIDs: events, EffectIDs: effects,
	})
}
