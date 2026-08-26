package beads

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const (
	metadataSchemaV1      uint16 = 1
	metadataNamespace            = "spec_kitty"
	maxCriteriaPerWork           = 4096
	maxCriterionIDBytes          = 128
	maxCriterionTextBytes        = 64 << 10
	maxEvidenceHintBytes         = 16 << 10
)

type criterionMetadata struct {
	ID           string               `json:"id"`
	Text         string               `json:"text"`
	Verifier     tracker.VerifierKind `json:"verifier"`
	Required     bool                 `json:"required"`
	EvidenceHint string               `json:"evidence_hint,omitempty"`
}

type kittyMetadata struct {
	SchemaVersion      uint16
	ProjectID          ids.ProjectID
	WorkID             ids.WorkID
	CreatedByOperation ids.OperationID
	LastOperation      ids.OperationID
	AssignmentMarker   ids.OperationID
	Criteria           []tracker.Criterion
	DesignContext      string
	ContentDigest      string
}

type metadataDocument struct {
	root      map[string]json.RawMessage
	namespace map[string]json.RawMessage
	kitty     kittyMetadata
}

func decodeMetadata(payload []byte) (metadataDocument, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		payload = []byte(`{}`)
	}
	var root map[string]json.RawMessage
	if err := decodeJSONObject(payload, &root); err != nil {
		return metadataDocument{}, metadataProtocolError("provider metadata must be a JSON object", err)
	}
	document := metadataDocument{root: root}
	raw, exists := root[metadataNamespace]
	if !exists {
		return document, nil
	}
	var namespace map[string]json.RawMessage
	if err := decodeJSONObject(raw, &namespace); err != nil {
		return metadataDocument{}, metadataProtocolError("spec_kitty metadata must be a JSON object", err)
	}
	document.namespace = namespace
	kitty, err := decodeKittyMetadata(namespace)
	if err != nil {
		return metadataDocument{}, err
	}
	document.kitty = kitty
	return document, nil
}

func decodeJSONObject(payload []byte, target *map[string]json.RawMessage) error {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("object root required")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if *target == nil {
		return fmt.Errorf("null object")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func decodeKittyMetadata(namespace map[string]json.RawMessage) (kittyMetadata, error) {
	var schema uint16
	if err := requiredJSON(namespace, "schema_version", &schema); err != nil {
		return kittyMetadata{}, metadataProtocolError("spec_kitty schema version is missing or malformed", err)
	}
	if schema > metadataSchemaV1 {
		return kittyMetadata{}, &tracker.Error{Kind: tracker.ErrorSchemaSkew, RecoveryCode: "reconcile_metadata_schema", Message: "spec_kitty metadata schema is newer than this adapter"}
	}
	if schema != metadataSchemaV1 {
		return kittyMetadata{}, metadataProtocolError("spec_kitty metadata schema is unsupported", nil)
	}
	var wire struct {
		ProjectID          string              `json:"project_id"`
		WorkID             string              `json:"work_id"`
		CreatedByOperation string              `json:"created_by_operation"`
		LastOperation      string              `json:"last_operation"`
		AssignmentMarker   string              `json:"assignment_marker"`
		Criteria           []criterionMetadata `json:"criteria"`
		DesignContext      string              `json:"design_context"`
		ContentDigest      string              `json:"content_digest"`
	}
	encoded, err := json.Marshal(namespace)
	if err != nil {
		return kittyMetadata{}, metadataProtocolError("spec_kitty metadata fields are malformed", err)
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return kittyMetadata{}, metadataProtocolError("spec_kitty metadata fields are malformed", err)
	}
	if _, ok := namespace["criteria"]; !ok {
		return kittyMetadata{}, metadataProtocolError("spec_kitty criteria are missing", nil)
	}
	projectID, err := ids.ParseProjectID(wire.ProjectID)
	if err != nil {
		return kittyMetadata{}, metadataProtocolError("spec_kitty project identity is invalid", err)
	}
	workID, err := ids.ParseWorkID(wire.WorkID)
	if err != nil {
		return kittyMetadata{}, metadataProtocolError("spec_kitty work identity is invalid", err)
	}
	created, err := ids.ParseOperationID(wire.CreatedByOperation)
	if err != nil {
		return kittyMetadata{}, metadataProtocolError("spec_kitty creation operation is invalid", err)
	}
	last, err := ids.ParseOperationID(wire.LastOperation)
	if err != nil {
		return kittyMetadata{}, metadataProtocolError("spec_kitty last operation is invalid", err)
	}
	var assignment ids.OperationID
	if wire.AssignmentMarker != "" {
		assignment, err = ids.ParseOperationID(wire.AssignmentMarker)
		if err != nil {
			return kittyMetadata{}, metadataProtocolError("spec_kitty assignment marker is invalid", err)
		}
	}
	criteria := make([]tracker.Criterion, len(wire.Criteria))
	for index, criterion := range wire.Criteria {
		criteria[index] = tracker.Criterion{ID: criterion.ID, Text: criterion.Text, Verifier: criterion.Verifier, Required: criterion.Required, EvidenceHint: criterion.EvidenceHint}
	}
	if err := validateCriteria(criteria); err != nil {
		return kittyMetadata{}, metadataProtocolError("spec_kitty criteria are invalid", err)
	}
	return kittyMetadata{
		SchemaVersion: schema, ProjectID: projectID, WorkID: workID,
		CreatedByOperation: created, LastOperation: last, AssignmentMarker: assignment,
		Criteria: criteria, DesignContext: wire.DesignContext, ContentDigest: wire.ContentDigest,
	}, nil
}

func requiredJSON(values map[string]json.RawMessage, key string, target any) error {
	raw, ok := values[key]
	if !ok {
		return fmt.Errorf("%s absent", key)
	}
	return json.Unmarshal(raw, target)
}

func validateCriteria(criteria []tracker.Criterion) error {
	if len(criteria) > maxCriteriaPerWork {
		return fmt.Errorf("too many criteria")
	}
	seen := make(map[string]struct{}, len(criteria))
	for _, criterion := range criteria {
		if err := criterion.Validate(); err != nil {
			return err
		}
		if len(criterion.ID) > maxCriterionIDBytes || len(criterion.Text) > maxCriterionTextBytes || len(criterion.EvidenceHint) > maxEvidenceHintBytes {
			return fmt.Errorf("criterion field exceeds bound")
		}
		if _, duplicate := seen[criterion.ID]; duplicate {
			return fmt.Errorf("duplicate criterion id")
		}
		seen[criterion.ID] = struct{}{}
	}
	return nil
}

func mergeMetadata(payload []byte, desired kittyMetadata) ([]byte, error) {
	if desired.SchemaVersion != metadataSchemaV1 {
		return nil, &tracker.Error{Kind: tracker.ErrorSchemaSkew, RecoveryCode: "reconcile_metadata_schema", Message: "requested spec_kitty metadata schema is unsupported"}
	}
	if err := validateKittyMetadata(desired); err != nil {
		return nil, err
	}
	document, err := decodeMetadata(payload)
	if err != nil {
		return nil, err
	}
	if document.namespace != nil {
		if document.kitty.ProjectID != desired.ProjectID || document.kitty.WorkID != desired.WorkID || document.kitty.CreatedByOperation != desired.CreatedByOperation {
			return nil, mappingConflict("existing spec_kitty metadata belongs to another mapping")
		}
	}
	namespace := cloneRawMap(document.namespace)
	if namespace == nil {
		namespace = make(map[string]json.RawMessage)
	}
	wireCriteria := make([]criterionMetadata, len(desired.Criteria))
	for index, criterion := range desired.Criteria {
		wireCriteria[index] = criterionMetadata{ID: criterion.ID, Text: criterion.Text, Verifier: criterion.Verifier, Required: criterion.Required, EvidenceHint: criterion.EvidenceHint}
	}
	setJSON(namespace, "schema_version", desired.SchemaVersion)
	setJSON(namespace, "project_id", desired.ProjectID.String())
	setJSON(namespace, "work_id", desired.WorkID.String())
	setJSON(namespace, "created_by_operation", desired.CreatedByOperation.String())
	setJSON(namespace, "last_operation", desired.LastOperation.String())
	setJSON(namespace, "criteria", wireCriteria)
	setJSON(namespace, "design_context", desired.DesignContext)
	setJSON(namespace, "content_digest", desired.ContentDigest)
	if desired.AssignmentMarker == "" {
		delete(namespace, "assignment_marker")
	} else {
		setJSON(namespace, "assignment_marker", desired.AssignmentMarker.String())
	}
	root := cloneRawMap(document.root)
	encodedNamespace, err := json.Marshal(namespace)
	if err != nil {
		return nil, metadataProtocolError("cannot encode spec_kitty metadata", err)
	}
	root[metadataNamespace] = encodedNamespace
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, metadataProtocolError("cannot encode provider metadata", err)
	}
	return encoded, nil
}

func validateKittyMetadata(value kittyMetadata) error {
	if _, err := ids.ParseProjectID(value.ProjectID.String()); err != nil {
		return invalidCatalogRequest("project identity is invalid", err)
	}
	if _, err := ids.ParseWorkID(value.WorkID.String()); err != nil {
		return invalidCatalogRequest("work identity is invalid", err)
	}
	if _, err := ids.ParseOperationID(value.CreatedByOperation.String()); err != nil {
		return invalidCatalogRequest("creation operation is invalid", err)
	}
	if _, err := ids.ParseOperationID(value.LastOperation.String()); err != nil {
		return invalidCatalogRequest("last operation is invalid", err)
	}
	if value.AssignmentMarker != "" {
		if _, err := ids.ParseOperationID(value.AssignmentMarker.String()); err != nil {
			return invalidCatalogRequest("assignment marker is invalid", err)
		}
	}
	if err := validateCriteria(value.Criteria); err != nil {
		return invalidCatalogRequest("criteria are invalid", err)
	}
	return nil
}

func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	if source == nil {
		return make(map[string]json.RawMessage)
	}
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func setJSON(destination map[string]json.RawMessage, key string, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("beads metadata value is not JSON encodable: " + err.Error())
	}
	destination[key] = encoded
}

func metadataProtocolError(message string, cause error) error {
	return &tracker.Error{Kind: tracker.ErrorMalformedProtocol, RecoveryCode: "reconcile_provider_metadata", Message: message, Cause: cause}
}

func mappingConflict(message string) error {
	return &tracker.Error{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "reconcile_mapping", Message: message}
}

func invalidCatalogRequest(message string, cause error) error {
	return &tracker.Error{Kind: tracker.ErrorInvalidRequest, RecoveryCode: "repair_request", Message: message, Cause: cause}
}
