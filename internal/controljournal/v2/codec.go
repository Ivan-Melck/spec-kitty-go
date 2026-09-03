package v2

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// EncodeKeyComponent is the sole v2 path-component codec. The leading marker
// prevents an encoded component from being confused with a legacy raw token.
func EncodeKeyComponent(value string) (string, error) {
	if err := validateIdentity("key_component", value); err != nil {
		return "", invalid(ErrorInvalidKeyComponent, "key_component")
	}
	return "k" + hex.EncodeToString([]byte(value)), nil
}

func DecodeKeyComponent(value string) (string, error) {
	if len(value) < 3 || value[0] != 'k' || len(value)%2 != 1 {
		return "", invalid(ErrorInvalidKeyComponent, "key_component")
	}
	for _, char := range []byte(value[1:]) {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return "", invalid(ErrorInvalidKeyComponent, "key_component")
		}
	}
	decoded, err := hex.DecodeString(value[1:])
	if err != nil || !utf8.Valid(decoded) || validateIdentity("key_component", string(decoded)) != nil {
		return "", invalid(ErrorInvalidKeyComponent, "key_component")
	}
	canonical, _ := EncodeKeyComponent(string(decoded))
	if canonical != value {
		return "", invalid(ErrorInvalidKeyComponent, "key_component")
	}
	return string(decoded), nil
}

func ParseLogicalVersion(value string) (LogicalVersion, error) {
	if len(value) != 16 {
		return 0, invalid(ErrorInvalidVersion, "logical_version")
	}
	for _, char := range []byte(value) {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return 0, invalid(ErrorInvalidVersion, "logical_version")
		}
	}
	number, err := strconv.ParseUint(value, 16, 64)
	if err != nil || number == 0 {
		return 0, invalid(ErrorInvalidVersion, "logical_version")
	}
	return LogicalVersion(number), nil
}

// CanonicalObject rejects duplicate object keys at every depth and returns a
// stable JSON representation. It validates its byte budget before decoding.
func CanonicalObject(input []byte) ([]byte, error) {
	return canonicalObjectBounded(input, MaxCanonicalPayloadBytes)
}

func canonicalObjectBounded(input []byte, maximum int) ([]byte, error) {
	if maximum <= 0 || len(input) == 0 || len(input) > maximum || !utf8.Valid(input) || !json.Valid(input) {
		kind := ErrorInvalidCanonicalData
		if len(input) > maximum {
			kind = ErrorBudgetExceeded
		}
		return nil, invalid(kind, "canonical_object")
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	value, err := decodeUniqueValue(decoder)
	if err != nil {
		return nil, invalid(ErrorInvalidCanonicalData, "canonical_object")
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, invalid(ErrorInvalidCanonicalData, "canonical_object")
	}
	if len(bytes.TrimSpace(input[decoder.InputOffset():])) != 0 {
		return nil, invalid(ErrorInvalidCanonicalData, "canonical_object")
	}
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) > maximum {
		return nil, invalid(ErrorBudgetExceeded, "canonical_object")
	}
	return canonical, nil
}

func decodeUniqueValue(decoder *json.Decoder) (any, error) {
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
		result := make(map[string]any)
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return nil, fmt.Errorf("duplicate object key")
			}
			seen[key] = true
			value, err := decodeUniqueValue(decoder)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		if close, err := decoder.Token(); err != nil || close != json.Delim('}') {
			return nil, fmt.Errorf("object close")
		}
		return result, nil
	case '[':
		result := make([]any, 0)
		for decoder.More() {
			value, err := decodeUniqueValue(decoder)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		if close, err := decoder.Token(); err != nil || close != json.Delim(']') {
			return nil, fmt.Errorf("array close")
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter")
	}
}

func marshalCanonical(value any, maximum int) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, invalid(ErrorInvalidCanonicalData, "canonical_bytes")
	}
	if len(encoded) > maximum {
		return nil, invalid(ErrorBudgetExceeded, "canonical_bytes")
	}
	return encoded, nil
}

func decodeStrict(encoded []byte, maximum int, target any) error {
	if len(encoded) == 0 || len(encoded) > maximum || !utf8.Valid(encoded) {
		kind := ErrorInvalidCanonicalData
		if len(encoded) > maximum {
			kind = ErrorBudgetExceeded
		}
		return invalid(kind, "canonical_bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalid(ErrorInvalidCanonicalData, "canonical_bytes")
	}
	if decoder.More() {
		return invalid(ErrorInvalidCanonicalData, "canonical_bytes")
	}
	return nil
}

type publicationWitnessWireV2 struct {
	SchemaVersion   uint32 `json:"schema_version"`
	PublicationID   string `json:"publication_id"`
	OperationID     string `json:"operation_id"`
	LogicalVersion  uint64 `json:"operation_logical_version"`
	PlanSHA256      string `json:"plan_sha256"`
	RecordSetSHA256 string `json:"authority_record_set_sha256"`
	RecordCount     uint32 `json:"authority_record_count"`
	WitnessSHA256   string `json:"witness_sha256"`
}

// ValidatePublicationWitnessV2 validates both semantic coordinates and the
// self-digest. It accepts no provider revision or persistence key.
func ValidatePublicationWitnessV2(witness PublicationWitnessV2) error {
	want, err := publicationWitnessDigest(witness)
	if err != nil || witness.witnessDigest.IsZero() || witness.witnessDigest != want {
		return invalid(ErrorJournalCorrupt, "publication_witness")
	}
	return nil
}

func EncodePublicationWitnessV2(witness PublicationWitnessV2) ([]byte, error) {
	if err := ValidatePublicationWitnessV2(witness); err != nil {
		return nil, err
	}
	return marshalCanonical(publicationWitnessWire(witness), MaxCanonicalPayloadBytes)
}

func publicationWitnessWire(witness PublicationWitnessV2) publicationWitnessWireV2 {
	return publicationWitnessWireV2{witness.schemaVersion, string(witness.publicationID), string(witness.operationID),
		uint64(witness.logicalVersion), witness.planDigest.String(), witness.recordSetDigest.String(), witness.recordCount,
		witness.witnessDigest.String()}
}

func DecodePublicationWitnessV2(encoded []byte) (PublicationWitnessV2, error) {
	var wire publicationWitnessWireV2
	if err := decodeStrict(encoded, MaxCanonicalPayloadBytes, &wire); err != nil {
		return PublicationWitnessV2{}, invalid(ErrorJournalCorrupt, "publication_witness")
	}
	planDigest, planErr := ParseDigest("plan_sha256", wire.PlanSHA256)
	recordSetDigest, recordSetErr := ParseDigest("authority_record_set_sha256", wire.RecordSetSHA256)
	witnessDigest, witnessErr := ParseDigest("witness_sha256", wire.WitnessSHA256)
	if planErr != nil || recordSetErr != nil || witnessErr != nil {
		return PublicationWitnessV2{}, invalid(ErrorJournalCorrupt, "publication_witness")
	}
	witness := PublicationWitnessV2{schemaVersion: wire.SchemaVersion, publicationID: PublicationID(wire.PublicationID),
		operationID: OperationID(wire.OperationID), logicalVersion: LogicalVersion(wire.LogicalVersion),
		planDigest: planDigest, recordSetDigest: recordSetDigest, recordCount: wire.RecordCount, witnessDigest: witnessDigest}
	if err := ValidatePublicationWitnessV2(witness); err != nil {
		return PublicationWitnessV2{}, err
	}
	canonical, err := EncodePublicationWitnessV2(witness)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return PublicationWitnessV2{}, invalid(ErrorJournalCorrupt, "publication_witness")
	}
	return witness, nil
}
