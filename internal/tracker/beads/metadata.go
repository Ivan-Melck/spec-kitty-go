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
	maxWorkDependencies          = 4096
	maxWorkScopes                = 4096
	maxWorkProvenance            = 4096
)

type criterionMetadata struct {
	ID           string               `json:"id"`
	Text         string               `json:"text"`
	Verifier     tracker.VerifierKind `json:"verifier"`
	Required     bool                 `json:"required"`
	EvidenceHint string               `json:"evidence_hint,omitempty"`
	Outcome      string               `json:"outcome,omitempty"`
}

type kittyMetadata struct {
	SchemaVersion      uint16
	ProjectID          ids.ProjectID
	WorkID             ids.WorkID
	CreatedByOperation ids.OperationID
	LastOperation      ids.OperationID
	AssignmentMarker   ids.OperationID
	Criteria           []tracker.Criterion
	WorkCriteria       []tracker.WorkCriterion
	DesignContext      string
	CrossDomainReason  string
	Dependencies       []ids.WorkID
	WriteScopes        []tracker.WriteScope
	Provenance         []tracker.ScopeRef
	ContentDigest      string
	completeDefinition bool
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
		ProjectID          string               `json:"project_id"`
		WorkID             string               `json:"work_id"`
		CreatedByOperation string               `json:"created_by_operation"`
		LastOperation      string               `json:"last_operation"`
		AssignmentMarker   string               `json:"assignment_marker"`
		Criteria           []criterionMetadata  `json:"criteria"`
		DesignContext      string               `json:"design_context"`
		CrossDomainReason  string               `json:"cross_domain_reason"`
		Dependencies       []ids.WorkID         `json:"dependencies"`
		WriteScopes        []tracker.WriteScope `json:"write_scopes"`
		Provenance         []tracker.ScopeRef   `json:"provenance"`
		ContentDigest      string               `json:"content_digest"`
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
	workCriteria := make([]tracker.WorkCriterion, len(wire.Criteria))
	for index, criterion := range wire.Criteria {
		criteria[index] = tracker.Criterion{ID: criterion.ID, Text: criterion.Text, Verifier: criterion.Verifier, Required: criterion.Required, EvidenceHint: criterion.EvidenceHint}
		workCriteria[index] = tracker.WorkCriterion{ID: criterion.ID, Text: criterion.Text, Outcome: criterion.Outcome, Verifier: criterion.Verifier, Required: criterion.Required, EvidenceHint: criterion.EvidenceHint}
	}
	if err := validateCriteria(criteria); err != nil {
		return kittyMetadata{}, metadataProtocolError("spec_kitty criteria are invalid", err)
	}
	phase3Fields := []string{"cross_domain_reason", "dependencies", "write_scopes", "provenance"}
	phase3Present := 0
	for _, field := range phase3Fields {
		if _, present := namespace[field]; present {
			phase3Present++
		}
	}
	if phase3Present != 0 && phase3Present != len(phase3Fields) {
		return kittyMetadata{}, definitionSchemaError("complete work-definition metadata fields are required together", nil)
	}
	complete := phase3Present == len(phase3Fields)
	if complete {
		if len(workCriteria) == 0 || len(wire.WriteScopes) == 0 || len(wire.Provenance) == 0 || len(wire.Dependencies) > maxWorkDependencies || len(wire.WriteScopes) > maxWorkScopes || len(wire.Provenance) > maxWorkProvenance {
			return kittyMetadata{}, definitionSchemaError("complete work-definition metadata is incomplete or exceeds bounds", nil)
		}
		for _, criterion := range workCriteria {
			if criterion.Outcome == "" {
				return kittyMetadata{}, definitionSchemaError("complete work-definition criteria require outcomes", nil)
			}
		}
		if err := validateDefinitionCollections(wire.Dependencies, wire.WriteScopes, wire.Provenance); err != nil {
			return kittyMetadata{}, definitionSchemaError("complete work-definition metadata is invalid", err)
		}
	} else {
		workCriteria = nil
	}
	return kittyMetadata{
		SchemaVersion: schema, ProjectID: projectID, WorkID: workID,
		CreatedByOperation: created, LastOperation: last, AssignmentMarker: assignment,
		Criteria: criteria, WorkCriteria: workCriteria, DesignContext: wire.DesignContext,
		CrossDomainReason: wire.CrossDomainReason, Dependencies: append([]ids.WorkID(nil), wire.Dependencies...),
		WriteScopes: append([]tracker.WriteScope(nil), wire.WriteScopes...), Provenance: append([]tracker.ScopeRef(nil), wire.Provenance...),
		ContentDigest: wire.ContentDigest, completeDefinition: complete,
	}, nil
}

func validateDefinitionCollections(dependencies []ids.WorkID, scopes []tracker.WriteScope, provenance []tracker.ScopeRef) error {
	seenDependencies := make(map[ids.WorkID]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		if _, err := ids.ParseWorkID(dependency.String()); err != nil {
			return err
		}
		if _, duplicate := seenDependencies[dependency]; duplicate {
			return fmt.Errorf("duplicate dependency")
		}
		seenDependencies[dependency] = struct{}{}
	}
	seenScopes := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if err := scope.Validate(); err != nil {
			return err
		}
		key := scope.Path + "\x00" + string(scope.Access)
		if _, duplicate := seenScopes[key]; duplicate {
			return fmt.Errorf("duplicate write scope")
		}
		seenScopes[key] = struct{}{}
	}
	seenProvenance := make(map[string]struct{}, len(provenance))
	for _, reference := range provenance {
		if err := reference.Validate(); err != nil {
			return err
		}
		key := string(reference.Kind) + "\x00" + reference.ID
		if _, duplicate := seenProvenance[key]; duplicate {
			return fmt.Errorf("duplicate provenance")
		}
		seenProvenance[key] = struct{}{}
	}
	return nil
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
	if desired.completeDefinition {
		wireCriteria = make([]criterionMetadata, len(desired.WorkCriteria))
		for index, criterion := range desired.WorkCriteria {
			wireCriteria[index] = criterionMetadata{ID: criterion.ID, Text: criterion.Text, Verifier: criterion.Verifier, Required: criterion.Required, EvidenceHint: criterion.EvidenceHint, Outcome: criterion.Outcome}
		}
	} else {
		for index, criterion := range desired.Criteria {
			wireCriteria[index] = criterionMetadata{ID: criterion.ID, Text: criterion.Text, Verifier: criterion.Verifier, Required: criterion.Required, EvidenceHint: criterion.EvidenceHint}
		}
	}
	setJSON(namespace, "schema_version", desired.SchemaVersion)
	setJSON(namespace, "project_id", desired.ProjectID.String())
	setJSON(namespace, "work_id", desired.WorkID.String())
	setJSON(namespace, "created_by_operation", desired.CreatedByOperation.String())
	setJSON(namespace, "last_operation", desired.LastOperation.String())
	setJSON(namespace, "criteria", wireCriteria)
	setJSON(namespace, "design_context", desired.DesignContext)
	setJSON(namespace, "content_digest", desired.ContentDigest)
	if desired.completeDefinition {
		setJSON(namespace, "cross_domain_reason", desired.CrossDomainReason)
		setJSON(namespace, "dependencies", desired.Dependencies)
		setJSON(namespace, "write_scopes", desired.WriteScopes)
		setJSON(namespace, "provenance", desired.Provenance)
	} else {
		delete(namespace, "cross_domain_reason")
		delete(namespace, "dependencies")
		delete(namespace, "write_scopes")
		delete(namespace, "provenance")
	}
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
	if value.completeDefinition {
		if len(value.WorkCriteria) == 0 || len(value.WriteScopes) == 0 || len(value.Provenance) == 0 {
			return invalidCatalogRequest("complete work-definition metadata is incomplete", nil)
		}
		for _, criterion := range value.WorkCriteria {
			catalogCriterion := tracker.Criterion{ID: criterion.ID, Text: criterion.Text, Verifier: criterion.Verifier, Required: criterion.Required, EvidenceHint: criterion.EvidenceHint}
			if err := catalogCriterion.Validate(); err != nil || criterion.Outcome == "" {
				return invalidCatalogRequest("complete work-definition criterion is invalid", err)
			}
		}
		if err := validateDefinitionCollections(value.Dependencies, value.WriteScopes, value.Provenance); err != nil {
			return invalidCatalogRequest("complete work-definition metadata is invalid", err)
		}
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

func definitionSchemaError(message string, cause error) error {
	return &tracker.Error{Kind: tracker.ErrorSchemaSkew, RecoveryCode: "repair_complete_work_definition", Message: message, Cause: cause}
}

func mappingConflict(message string) error {
	return &tracker.Error{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "reconcile_mapping", Message: message}
}

func invalidCatalogRequest(message string, cause error) error {
	return &tracker.Error{Kind: tracker.ErrorInvalidRequest, RecoveryCode: "repair_request", Message: message, Cause: cause}
}
