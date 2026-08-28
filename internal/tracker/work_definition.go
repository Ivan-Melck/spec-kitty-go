package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

const WorkDefinitionMetadataSchemaV1 uint16 = 1

type ScopeKind string

const (
	ScopePlanConcern     ScopeKind = "plan_concern"
	ScopeRequiredOutcome ScopeKind = "required_outcome"
	ScopeOptionalOutcome ScopeKind = "optional_outcome"
)

func (kind ScopeKind) valid() bool {
	return kind == ScopePlanConcern || kind == ScopeRequiredOutcome || kind == ScopeOptionalOutcome
}

// ScopeRef is bounded provenance into the exact approved plan/spec scope. It
// deliberately carries no prose or transcript content.
type ScopeRef struct {
	ID   string    `json:"id"`
	Kind ScopeKind `json:"kind"`
}

func (reference ScopeRef) Validate() error {
	if !stableToken(reference.ID) || !reference.Kind.valid() {
		return fmt.Errorf("tracker work provenance: stable scope identity and kind are required")
	}
	return nil
}

type ScopeAccess string

const (
	ScopeReadOnly       ScopeAccess = "read_only"
	ScopeWriteExclusive ScopeAccess = "write_exclusive"
)

// WriteScope is an explicit repository-relative ownership declaration.
// Domain is semantic ownership; Path is the concrete mutation boundary.
type WriteScope struct {
	Path             string      `json:"path"`
	Domain           string      `json:"domain"`
	Access           ScopeAccess `json:"access"`
	SerializationKey string      `json:"serialization_key,omitempty"`
}

func (scope WriteScope) Validate() error {
	if scope.Path == "" || path.IsAbs(scope.Path) || path.Clean(scope.Path) != scope.Path || scope.Path == "." || strings.HasPrefix(scope.Path, "../") || strings.Contains(scope.Path, "\\") {
		return fmt.Errorf("tracker write scope: canonical repository-relative path is required")
	}
	if !stableToken(scope.Domain) || (scope.Access != ScopeReadOnly && scope.Access != ScopeWriteExclusive) {
		return fmt.Errorf("tracker write scope: stable domain and access are required")
	}
	if scope.SerializationKey != "" && !stableToken(scope.SerializationKey) {
		return fmt.Errorf("tracker write scope: serialization key is invalid")
	}
	return nil
}

type WorkDefinitionParams struct {
	ProjectID         ids.ProjectID
	WorkID            ids.WorkID
	Title             string
	Description       string
	DesignContext     string
	CrossDomainReason string
	Criteria          []WorkCriterion
	Dependencies      []ids.WorkID
	WriteScopes       []WriteScope
	Provenance        []ScopeRef
	MetadataSchema    uint16
	ProviderMetadata  OpaqueMetadata
}

// WorkCriterion extends the established provider-neutral Criterion with the
// observable outcome required for executable decomposition quality.
type WorkCriterion struct {
	ID           string       `json:"id"`
	Text         string       `json:"text"`
	Outcome      string       `json:"outcome"`
	Verifier     VerifierKind `json:"verifier"`
	Required     bool         `json:"required"`
	EvidenceHint string       `json:"evidence_hint,omitempty"`
}

func (criterion WorkCriterion) catalogCriterion() Criterion {
	return Criterion{ID: criterion.ID, Text: criterion.Text, Verifier: criterion.Verifier, Required: criterion.Required, EvidenceHint: criterion.EvidenceHint}
}

// WorkDefinition is the complete mutable-planning value. Collections are
// private so provider and caller memory cannot rewrite accepted input.
type WorkDefinition struct {
	projectID         ids.ProjectID
	workID            ids.WorkID
	title             string
	description       string
	designContext     string
	crossDomainReason string
	criteria          []WorkCriterion
	dependencies      []ids.WorkID
	writeScopes       []WriteScope
	provenance        []ScopeRef
	metadataSchema    uint16
	providerMetadata  OpaqueMetadata
}

func NewWorkDefinition(params WorkDefinitionParams) (WorkDefinition, error) {
	if _, err := ids.ParseProjectID(params.ProjectID.String()); err != nil {
		return WorkDefinition{}, fmt.Errorf("tracker work definition: invalid project identity: %w", err)
	}
	if _, err := ids.ParseWorkID(params.WorkID.String()); err != nil {
		return WorkDefinition{}, fmt.Errorf("tracker work definition: invalid work identity: %w", err)
	}
	if !boundedText(params.Title, 160) || !boundedText(params.Description, 4096) || !boundedText(params.DesignContext, 4096) {
		return WorkDefinition{}, fmt.Errorf("tracker work definition: title, description, and design context are required and bounded")
	}
	if params.CrossDomainReason != "" && !boundedText(params.CrossDomainReason, 512) {
		return WorkDefinition{}, fmt.Errorf("tracker work definition: cross-domain reason is invalid")
	}
	if params.MetadataSchema != WorkDefinitionMetadataSchemaV1 {
		return WorkDefinition{}, fmt.Errorf("tracker work definition: unsupported metadata schema %d", params.MetadataSchema)
	}
	if len(params.Criteria) == 0 || len(params.WriteScopes) == 0 || len(params.Provenance) == 0 {
		return WorkDefinition{}, fmt.Errorf("tracker work definition: criteria, write scopes, and provenance are required")
	}
	criteria := append([]WorkCriterion(nil), params.Criteria...)
	criterionIDs := make(map[string]struct{}, len(criteria))
	for _, criterion := range criteria {
		if err := criterion.catalogCriterion().Validate(); err != nil || !boundedText(criterion.Outcome, 1024) {
			return WorkDefinition{}, fmt.Errorf("tracker work definition: criterion is invalid")
		}
		if _, duplicate := criterionIDs[criterion.ID]; duplicate {
			return WorkDefinition{}, fmt.Errorf("tracker work definition: duplicate criterion identity")
		}
		criterionIDs[criterion.ID] = struct{}{}
	}
	dependencies := append([]ids.WorkID(nil), params.Dependencies...)
	dependencyIDs := make(map[ids.WorkID]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		if _, err := ids.ParseWorkID(dependency.String()); err != nil || dependency == params.WorkID {
			return WorkDefinition{}, fmt.Errorf("tracker work definition: dependency identity is invalid")
		}
		if _, duplicate := dependencyIDs[dependency]; duplicate {
			return WorkDefinition{}, fmt.Errorf("tracker work definition: duplicate dependency identity")
		}
		dependencyIDs[dependency] = struct{}{}
	}
	scopes := append([]WriteScope(nil), params.WriteScopes...)
	scopeKeys := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if err := scope.Validate(); err != nil {
			return WorkDefinition{}, err
		}
		key := scope.Path + "\x00" + string(scope.Access)
		if _, duplicate := scopeKeys[key]; duplicate {
			return WorkDefinition{}, fmt.Errorf("tracker work definition: duplicate write scope")
		}
		scopeKeys[key] = struct{}{}
	}
	provenance := append([]ScopeRef(nil), params.Provenance...)
	provenanceKeys := make(map[string]struct{}, len(provenance))
	for _, reference := range provenance {
		if err := reference.Validate(); err != nil {
			return WorkDefinition{}, err
		}
		key := string(reference.Kind) + "\x00" + reference.ID
		if _, duplicate := provenanceKeys[key]; duplicate {
			return WorkDefinition{}, fmt.Errorf("tracker work definition: duplicate provenance reference")
		}
		provenanceKeys[key] = struct{}{}
	}
	metadata, err := NewOpaqueMetadata(params.ProviderMetadata.Bytes())
	if err != nil {
		return WorkDefinition{}, err
	}
	return WorkDefinition{
		projectID: params.ProjectID, workID: params.WorkID, title: params.Title,
		description: params.Description, designContext: params.DesignContext,
		crossDomainReason: params.CrossDomainReason, criteria: criteria,
		dependencies: dependencies, writeScopes: scopes, provenance: provenance,
		metadataSchema: params.MetadataSchema, providerMetadata: metadata,
	}, nil
}

func (definition WorkDefinition) ProjectID() ids.ProjectID  { return definition.projectID }
func (definition WorkDefinition) WorkID() ids.WorkID        { return definition.workID }
func (definition WorkDefinition) Title() string             { return definition.title }
func (definition WorkDefinition) Description() string       { return definition.description }
func (definition WorkDefinition) DesignContext() string     { return definition.designContext }
func (definition WorkDefinition) CrossDomainReason() string { return definition.crossDomainReason }
func (definition WorkDefinition) Criteria() []WorkCriterion {
	return append([]WorkCriterion(nil), definition.criteria...)
}
func (definition WorkDefinition) Dependencies() []ids.WorkID {
	return append([]ids.WorkID(nil), definition.dependencies...)
}
func (definition WorkDefinition) WriteScopes() []WriteScope {
	return append([]WriteScope(nil), definition.writeScopes...)
}
func (definition WorkDefinition) Provenance() []ScopeRef {
	return append([]ScopeRef(nil), definition.provenance...)
}
func (definition WorkDefinition) MetadataSchema() uint16 { return definition.metadataSchema }
func (definition WorkDefinition) ProviderMetadata() OpaqueMetadata {
	copy, _ := NewOpaqueMetadata(definition.providerMetadata.Bytes())
	return copy
}

func (definition WorkDefinition) CanonicalData() []byte {
	criteria := definition.Criteria()
	dependencies := definition.Dependencies()
	scopes := definition.WriteScopes()
	provenance := definition.Provenance()
	sort.Slice(criteria, func(i, j int) bool { return criteria[i].ID < criteria[j].ID })
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i] < dependencies[j] })
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].Path == scopes[j].Path {
			return scopes[i].Access < scopes[j].Access
		}
		return scopes[i].Path < scopes[j].Path
	})
	sort.Slice(provenance, func(i, j int) bool {
		if provenance[i].ID == provenance[j].ID {
			return provenance[i].Kind < provenance[j].Kind
		}
		return provenance[i].ID < provenance[j].ID
	})
	encoded, _ := json.Marshal(struct {
		ProjectID         ids.ProjectID   `json:"project_id"`
		WorkID            ids.WorkID      `json:"work_id"`
		Title             string          `json:"title"`
		Description       string          `json:"description"`
		DesignContext     string          `json:"design_context"`
		CrossDomainReason string          `json:"cross_domain_reason,omitempty"`
		Criteria          []WorkCriterion `json:"criteria"`
		Dependencies      []ids.WorkID    `json:"dependencies"`
		WriteScopes       []WriteScope    `json:"write_scopes"`
		Provenance        []ScopeRef      `json:"provenance"`
		MetadataSchema    uint16          `json:"metadata_schema"`
		ProviderMetadata  json.RawMessage `json:"provider_metadata"`
	}{definition.projectID, definition.workID, definition.title, definition.description, definition.designContext, definition.crossDomainReason, criteria, dependencies, scopes, provenance, definition.metadataSchema, definition.providerMetadata.Bytes()})
	return encoded
}

type WorkDefinitionObservation struct {
	definition WorkDefinition
	reference  ExternalWorkRef
	provider   ProviderObservation
	observedAt time.Time
}

func NewWorkDefinitionObservation(definition WorkDefinition, reference ExternalWorkRef, observation ProviderObservation, observedAt time.Time) (WorkDefinitionObservation, error) {
	if len(definition.CanonicalData()) == 0 || reference.Validate() != nil || observation.Revision == 0 || observedAt.IsZero() {
		return WorkDefinitionObservation{}, fmt.Errorf("tracker work definition observation: complete definition, mapping, provider observation, and time are required")
	}
	if _, err := ids.ParseOperationID(reference.CreatedByOperation.String()); err != nil {
		return WorkDefinitionObservation{}, fmt.Errorf("tracker work definition observation: creation operation attribution is required")
	}
	if definition.ProjectID() != reference.ProjectID || definition.WorkID() != reference.WorkID || reference.ExternalID != observation.ExternalID || reference.LastObservation != observation.Revision || reference.MetadataSchema != definition.MetadataSchema() {
		return WorkDefinitionObservation{}, fmt.Errorf("tracker work definition observation: definition, mapping, and provider identities disagree")
	}
	return WorkDefinitionObservation{definition: definition, reference: reference, provider: cloneProviderObservation(observation), observedAt: observedAt.UTC()}, nil
}

func (observation WorkDefinitionObservation) Definition() WorkDefinition {
	return cloneDefinition(observation.definition)
}
func (observation WorkDefinitionObservation) ExternalRef() ExternalWorkRef {
	return observation.reference
}
func (observation WorkDefinitionObservation) ProviderObservation() ProviderObservation {
	return cloneProviderObservation(observation.provider)
}
func (observation WorkDefinitionObservation) ObservedAt() time.Time { return observation.observedAt }
func (observation WorkDefinitionObservation) CanonicalData() []byte {
	encoded, _ := json.Marshal(struct {
		Definition        json.RawMessage `json:"definition"`
		Provider          string          `json:"provider"`
		ExternalID        string          `json:"external_id"`
		ProviderRevision  Revision        `json:"provider_revision"`
		ProviderCanonical []byte          `json:"provider_canonical"`
		ObservedAt        time.Time       `json:"observed_at"`
	}{observation.definition.CanonicalData(), observation.reference.Provider, observation.reference.ExternalID, observation.provider.Revision, observation.provider.CanonicalFields(), observation.observedAt})
	return encoded
}

type WorkDefinitionCapabilities struct {
	Create        bool
	GuardedUpdate bool
	BlockingGraph bool
}

func (capabilities WorkDefinitionCapabilities) Validate() error {
	if !capabilities.Create || !capabilities.GuardedUpdate || !capabilities.BlockingGraph {
		return &Error{Kind: ErrorCapabilityUnsupported, Message: "complete work-definition mutation capabilities are required", RecoveryCode: "use_supported_capability"}
	}
	return nil
}

type CreateWorkDefinitionRequest struct {
	OperationID ids.OperationID
	Definition  WorkDefinition
}

type UpdateWorkDefinitionRequest struct {
	OperationID         ids.OperationID
	ExternalRef         ExternalWorkRef
	ExpectedObservation Revision
	Definition          WorkDefinition
}

type WorkDefinitionMutation struct {
	Observation WorkDefinitionObservation
	Replayed    bool
	Noop        bool
}

// WorkDefinitionPort is the narrow mutable-planning port. It is deliberately
// separate from the legacy Provider compatibility interface.
type WorkDefinitionPort interface {
	DefinitionCapabilities(context.Context) (WorkDefinitionCapabilities, error)
	PrepareDefinitionOperation(context.Context, ids.OperationID, string) error
	CreateDefinition(context.Context, CreateWorkDefinitionRequest) (WorkDefinitionMutation, error)
	UpdateDefinition(context.Context, UpdateWorkDefinitionRequest) (WorkDefinitionMutation, error)
	ObserveDefinition(context.Context, ExternalWorkRef) (WorkDefinitionObservation, error)
}

func cloneDefinition(definition WorkDefinition) WorkDefinition {
	copy, _ := NewWorkDefinition(WorkDefinitionParams{
		ProjectID: definition.ProjectID(), WorkID: definition.WorkID(), Title: definition.Title(), Description: definition.Description(),
		DesignContext: definition.DesignContext(), CrossDomainReason: definition.CrossDomainReason(), Criteria: definition.Criteria(),
		Dependencies: definition.Dependencies(), WriteScopes: definition.WriteScopes(), Provenance: definition.Provenance(),
		MetadataSchema: definition.MetadataSchema(), ProviderMetadata: definition.ProviderMetadata(),
	})
	return copy
}

func cloneProviderObservation(observation ProviderObservation) ProviderObservation {
	copy := observation
	copy.canonical = observation.CanonicalFields()
	return copy
}

func boundedText(value string, limit int) bool {
	if value == "" || len(value) > limit || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 && character != '\n' && character != '\t' {
			return false
		}
	}
	return true
}

func stableToken(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("_.:-", character) {
			continue
		}
		return false
	}
	return true
}
