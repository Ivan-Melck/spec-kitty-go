package controljournal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const MaxOrdinaryValueBytes = 256 * 1024

// CanonicalJSONObject validates one JSON object, rejects duplicate keys at
// every depth, and returns deterministic RFC 8259 bytes.
func CanonicalJSONObject(input []byte) ([]byte, error) {
	return canonicalJSONObject(input, "object")
}

func canonicalJSONObject(input []byte, field string) ([]byte, error) {
	if len(input) == 0 {
		return nil, ValidationError{Kind: RefusalInvalidValue, Field: field}
	}
	if len(input) > MaxOrdinaryValueBytes || !utf8.Valid(input) || !json.Valid(input) {
		return nil, ValidationError{Kind: RefusalInvalidValue, Field: field}
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil {
		return nil, ValidationError{Kind: RefusalInvalidValue, Field: field}
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, ValidationError{Kind: RefusalInvalidValue, Field: field}
	}
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) > MaxOrdinaryValueBytes {
		return nil, ValidationError{Kind: RefusalBudgetExceeded, Field: field}
	}
	return canonical, nil
}

func decodeRequiredObject(encoded []byte, field string, required ...string) ([]byte, error) {
	canonical, err := canonicalJSONObject(encoded, field)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		return nil, ValidationError{Kind: RefusalInvalidValue, Field: field}
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ValidationError{Kind: RefusalInvalidValue, Field: field}
		}
	}
	for _, name := range required {
		_, present := fields[name]
		if !present {
			return nil, ValidationError{Kind: RefusalInvalidValue, Field: name}
		}
	}
	return canonical, nil
}

func parseCanonicalSHA256(field, value string) ([sha256Size]byte, error) {
	if len(value) != sha256Size*2 {
		return [sha256Size]byte{}, ValidationError{Kind: RefusalInvalidValue, Field: field}
	}
	for i := 0; i < len(value); i++ {
		if !(value[i] >= '0' && value[i] <= '9' || value[i] >= 'a' && value[i] <= 'f') {
			return [sha256Size]byte{}, ValidationError{Kind: RefusalInvalidValue, Field: field}
		}
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return [sha256Size]byte{}, ValidationError{Kind: RefusalInvalidValue, Field: field}
	}
	var digest [sha256Size]byte
	copy(digest[:], decoded)
	return digest, nil
}

func decodeUniqueJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("object key")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate key")
			}
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		closeToken, err := decoder.Token()
		if err != nil || closeToken != json.Delim('}') {
			return nil, fmt.Errorf("object close")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		closeToken, err := decoder.Token()
		if err != nil || closeToken != json.Delim(']') {
			return nil, fmt.Errorf("array close")
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter")
	}
}

func EncodeEventEnvelopeV1(envelope EventEnvelopeV1) ([]byte, error) {
	if err := envelope.validate(); err != nil {
		return nil, err
	}
	unsigned, err := envelope.canonicalBytes(false)
	if err != nil || envelope.digest != digestBytes(unsigned) {
		return nil, ValidationError{Kind: RefusalJournalCorrupt, Field: "canonical_event_digest"}
	}
	encoded, err := envelope.canonicalBytes(true)
	if err != nil {
		return nil, ValidationError{Kind: RefusalInvalidValue, Field: "event_envelope"}
	}
	if len(encoded) > MaxOrdinaryValueBytes {
		return nil, ValidationError{Kind: RefusalBudgetExceeded, Field: "event_envelope"}
	}
	return encoded, nil
}

func DecodeEventEnvelopeV1(encoded []byte) (EventEnvelopeV1, error) {
	canonical, err := decodeRequiredObject(encoded, "event_envelope",
		"envelope_version", "event_type", "event_schema_version", "reducer_version",
		"event_id", "operation_id", "project_id", "aggregate_kind", "aggregate_id",
		"aggregate_logical_version", "runtime_epoch_id", "event_index", "event_count",
		"payload", "canonical_event_digest")
	if err != nil {
		return EventEnvelopeV1{}, err
	}
	var wire eventEnvelopeWireV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return EventEnvelopeV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "event_envelope"}
	}
	digest, err := parseCanonicalSHA256("canonical_event_digest", wire.CanonicalEventDigest)
	if err != nil {
		return EventEnvelopeV1{}, err
	}
	index, err := NewEventIndexV1(wire.EventIndex)
	if err != nil {
		return EventEnvelopeV1{}, err
	}
	count, err := NewEventCountV1(wire.EventCount)
	if err != nil {
		return EventEnvelopeV1{}, err
	}
	if uint16(index) >= uint16(count) {
		return EventEnvelopeV1{}, ValidationError{Kind: RefusalInvalidValue, Field: "event_position"}
	}
	input := EventEnvelopeInputV1{
		EnvelopeVersion: wire.EnvelopeVersion, EventID: EventID(wire.EventID),
		EventType: EventType(wire.EventType), EventSchemaVersion: wire.EventSchemaVersion,
		ReducerVersion: ReducerVersion(wire.ReducerVersion), ProjectID: ProjectID(wire.ProjectID),
		AggregateKind: AggregateKind(wire.AggregateKind), AggregateID: AggregateID(wire.AggregateID),
		AggregateLogicalVersion: wire.AggregateLogicalVersion,
		OperationID:             OperationID(wire.OperationID), RuntimeEpochID: RuntimeEpochID(wire.RuntimeEpochID),
		EventIndex: index, EventCount: count,
		WorkRevisionID: WorkRevisionID(wire.WorkRevisionID), LeaseID: LeaseID(wire.LeaseID),
		LeaseGeneration: wire.LeaseGeneration, FenceToken: wire.FenceToken,
		PolicyRevision: wire.PolicyRevision, DoctrineRevision: wire.DoctrineRevision,
		ProfileRevision: wire.ProfileRevision, RecordedObservations: wire.RecordedObservations,
		Payload: wire.Payload,
	}
	envelope, err := NewEventEnvelopeV1(input)
	if err != nil {
		return EventEnvelopeV1{}, err
	}
	if envelope.digest != digest {
		return EventEnvelopeV1{}, ValidationError{Kind: RefusalJournalCorrupt, Field: "canonical_event_digest"}
	}
	return envelope, nil
}

type aggregateHeadWireV1 struct {
	AggregateID    string `json:"aggregate_id"`
	AggregateKind  string `json:"aggregate_kind"`
	LastEventID    string `json:"last_event_id"`
	LogicalVersion uint64 `json:"logical_version"`
	RuntimeEpochID string `json:"runtime_epoch_id"`
	SchemaVersion  uint32 `json:"schema_version"`
	StateDigest    string `json:"canonical_state_digest"`
}

func EncodeAggregateHeadV1(head AggregateHeadV1) ([]byte, error) {
	if head.SchemaVersion != 1 {
		return nil, ValidationError{Kind: RefusalUnsupportedVersion, Field: "aggregate_head"}
	}
	if _, err := ConstructAggregateHeadV1(head.AggregateKind, head.AggregateID, head.LogicalVersion, head.RuntimeEpochID, head.LastEventID, head.StateDigest); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(aggregateHeadWireV1{AggregateID: string(head.AggregateID), AggregateKind: string(head.AggregateKind), LastEventID: string(head.LastEventID), LogicalVersion: head.LogicalVersion, RuntimeEpochID: string(head.RuntimeEpochID), SchemaVersion: head.SchemaVersion, StateDigest: hex.EncodeToString(head.StateDigest[:])})
	if err != nil || len(encoded) > MaxOrdinaryValueBytes {
		return nil, ValidationError{Kind: RefusalBudgetExceeded, Field: "aggregate_head"}
	}
	return encoded, nil
}

func DecodeAggregateHeadV1(encoded []byte) (AggregateHeadV1, error) {
	canonical, err := decodeRequiredObject(encoded, "aggregate_head", "aggregate_id", "aggregate_kind", "last_event_id", "logical_version", "runtime_epoch_id", "schema_version", "canonical_state_digest")
	if err != nil {
		return AggregateHeadV1{}, err
	}
	var wire aggregateHeadWireV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || wire.SchemaVersion != 1 {
		return AggregateHeadV1{}, ValidationError{Kind: RefusalUnsupportedVersion, Field: "aggregate_head"}
	}
	digest, err := parseCanonicalSHA256("canonical_state_digest", wire.StateDigest)
	if err != nil {
		return AggregateHeadV1{}, err
	}
	return ConstructAggregateHeadV1(AggregateKind(wire.AggregateKind), AggregateID(wire.AggregateID), wire.LogicalVersion, RuntimeEpochID(wire.RuntimeEpochID), EventID(wire.LastEventID), digest)
}

const sha256Size = 32

func digestBytes(value []byte) [sha256Size]byte {
	// Kept here so codecs and model validation use one ordinary cryptographic
	// primitive without exposing a signing bypass for journal validation.
	return sha256.Sum256(value)
}
