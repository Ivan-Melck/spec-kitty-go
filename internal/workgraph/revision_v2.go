package workgraph

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

const (
	RevisionSchemaV2         uint16           = 2
	DependencyApprovedOrDone DependencyPolicy = "approved_or_done"

	CriterionMetadataOutcome      = "spec_kitty.outcome"
	CriterionMetadataEvidenceHint = "spec_kitty.evidence_hint"
)

type DependencyPolicy string

type MissionDefinitionIdentity struct {
	Key     string
	Version uint64
	Digest  string
}

type CriterionV2Params struct {
	ID           string
	Text         string
	VerifierKind string
	Required     bool
	Outcome      string
	EvidenceHint string
	Metadata     map[string]any
}

type CriterionV2 struct {
	id           string
	text         string
	verifierKind string
	required     bool
	metadata     map[string]any
}

func (criterion CriterionV2) ID() string           { return criterion.id }
func (criterion CriterionV2) Text() string         { return criterion.text }
func (criterion CriterionV2) VerifierKind() string { return criterion.verifierKind }
func (criterion CriterionV2) Required() bool       { return criterion.required }
func (criterion CriterionV2) Metadata() map[string]any {
	return cloneMetadata(criterion.metadata)
}

type NormalizedWriteScope struct {
	Path             string
	Domain           string
	Access           ScopeAccess
	SerializationKey string
}

type WorkItemV2Params struct {
	WorkID        ids.WorkID
	Title         string
	Description   string
	DesignContext string
	Criteria      []CriterionV2Params
	Dependencies  []ids.WorkID
	WriteScopes   []NormalizedWriteScope
}

type WorkItemV2 struct {
	workID        ids.WorkID
	title         string
	description   string
	designContext string
	criteria      []CriterionV2
	dependencies  []ids.WorkID
	writeScopes   []NormalizedWriteScope
}

func (item WorkItemV2) WorkID() ids.WorkID      { return item.workID }
func (item WorkItemV2) Title() string           { return item.title }
func (item WorkItemV2) Description() string     { return item.description }
func (item WorkItemV2) DesignContext() string   { return item.designContext }
func (item WorkItemV2) Criteria() []CriterionV2 { return cloneCriteriaV2(item.criteria) }
func (item WorkItemV2) Dependencies() []ids.WorkID {
	return append([]ids.WorkID(nil), item.dependencies...)
}
func (item WorkItemV2) WriteScopes() []NormalizedWriteScope {
	return append([]NormalizedWriteScope(nil), item.writeScopes...)
}

type ProviderObservationV2 struct {
	WorkID     ids.WorkID
	Provider   string
	ExternalID string
	Revision   string
}

type RevisionV2Params struct {
	ID                   ids.WorkRevisionID
	ProjectID            ids.ProjectID
	MissionID            ids.MissionID
	Generation           uint64
	PredecessorID        ids.WorkRevisionID
	PlanningSourceCommit string
	MissionDefinition    MissionDefinitionIdentity
	DependencyPolicy     DependencyPolicy
	WorkItems            []WorkItemV2Params
	Lanes                []LaneV2
	ProviderObservations []ProviderObservationV2
	FinalizedBy          ids.ActorID
	FinalizedAt          time.Time
	GovernanceContextID  ids.GovernanceContextID
}

// RevisionV2 is the immutable executable graph for one exact generation.
type RevisionV2 struct {
	id                   ids.WorkRevisionID
	projectID            ids.ProjectID
	missionID            ids.MissionID
	generation           uint64
	predecessorID        ids.WorkRevisionID
	planningSourceCommit string
	missionDefinition    MissionDefinitionIdentity
	dependencyPolicy     DependencyPolicy
	workItems            []WorkItemV2
	lanes                []LaneV2
	providerObservations []ProviderObservationV2
	graphDigest          string
	criteriaDigest       string
	finalizedBy          ids.ActorID
	finalizedAt          time.Time
	governanceContextID  ids.GovernanceContextID
	canonicalGraph       []byte
	canonicalCriteria    []byte
}

// StoredRevision is an explicit read-only version dispatch result. It never
// invents v2-only facts for a legacy record.
type StoredRevision struct {
	schemaVersion uint16
	legacy        *Revision
	v2            *RevisionV2
}

func (stored StoredRevision) SchemaVersion() uint16 { return stored.schemaVersion }
func (stored StoredRevision) Legacy() (Revision, bool) {
	if stored.legacy == nil {
		return Revision{}, false
	}
	return *stored.legacy, true
}
func (stored StoredRevision) V2() (RevisionV2, bool) {
	if stored.v2 == nil {
		return RevisionV2{}, false
	}
	return *stored.v2, true
}

// DecodeStoredRevision dispatches only exact legacy envelopes or explicit v2
// envelopes. Missing version is not itself sufficient to classify arbitrary
// JSON as legacy.
func DecodeStoredRevision(encoded []byte) (StoredRevision, error) {
	if err := rejectDuplicateJSONKeys(encoded); err != nil {
		return StoredRevision{}, invalidV2("encoded", "decode persisted WorkRevision", err)
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return StoredRevision{}, invalidV2("encoded", "decode persisted WorkRevision", err)
	}
	versionBytes, versioned := fields["schema_version"]
	if !versioned {
		legacy, err := DecodeRevision(encoded)
		if err != nil {
			return StoredRevision{}, err
		}
		return StoredRevision{schemaVersion: 1, legacy: &legacy}, nil
	}
	var version uint16
	if err := json.Unmarshal(versionBytes, &version); err != nil || version != RevisionSchemaV2 {
		return StoredRevision{}, invalidV2("schema_version", "unsupported WorkRevision schema version", err)
	}
	v2, err := DecodeRevisionV2(encoded)
	if err != nil {
		return StoredRevision{}, err
	}
	return StoredRevision{schemaVersion: RevisionSchemaV2, v2: &v2}, nil
}

func NewRevisionV2(params RevisionV2Params) (RevisionV2, error) {
	return constructRevisionV2(params, "", "")
}

func constructRevisionV2(params RevisionV2Params, claimedGraphDigest, claimedCriteriaDigest string) (RevisionV2, error) {
	if _, err := ids.ParseWorkRevisionID(params.ID.String()); err != nil {
		return RevisionV2{}, invalidV2("work_revision_id", "work revision identity is invalid", err)
	}
	if _, err := ids.ParseProjectID(params.ProjectID.String()); err != nil {
		return RevisionV2{}, invalidV2("project_id", "project identity is invalid", err)
	}
	if _, err := ids.ParseMissionID(params.MissionID.String()); err != nil {
		return RevisionV2{}, invalidV2("mission_id", "mission identity is invalid", err)
	}
	if params.Generation == 0 || (params.Generation == 1 && params.PredecessorID != "") || (params.Generation > 1 && params.PredecessorID == "") {
		return RevisionV2{}, invalidV2("generation", "generation and predecessor do not form a valid revision lineage", nil)
	}
	if params.PredecessorID != "" {
		if _, err := ids.ParseWorkRevisionID(params.PredecessorID.String()); err != nil || params.PredecessorID == params.ID {
			return RevisionV2{}, invalidV2("predecessor_id", "predecessor identity is invalid", err)
		}
	}
	if !gitObjectID(params.PlanningSourceCommit) {
		return RevisionV2{}, invalidV2("planning_source_commit", "planning source must be an exact lowercase Git object identity", nil)
	}
	if params.MissionDefinition.Key != "software-development" || params.MissionDefinition.Version != 1 || !lowerDigest(params.MissionDefinition.Digest) {
		return RevisionV2{}, invalidV2("mission_definition", "mission definition identity is unsupported or incomplete", nil)
	}
	if params.DependencyPolicy != DependencyApprovedOrDone {
		return RevisionV2{}, invalidV2("dependency_policy", "dependency policy is unsupported", nil)
	}
	if _, err := ids.ParseActorID(params.FinalizedBy.String()); err != nil {
		return RevisionV2{}, invalidV2("finalized_by", "finalizer identity is invalid", err)
	}
	if params.FinalizedAt.IsZero() || params.FinalizedAt.Location() != time.UTC {
		return RevisionV2{}, invalidV2("finalized_at", "finalized time must be non-zero UTC", nil)
	}
	if _, err := ids.ParseGovernanceContextID(params.GovernanceContextID.String()); err != nil {
		return RevisionV2{}, invalidV2("governance_context_id", "governance context identity is invalid", err)
	}
	items, err := buildWorkItemsV2(params.WorkItems)
	if err != nil {
		return RevisionV2{}, err
	}
	lanes, err := validateAndCloneLanesV2(params.Lanes, items)
	if err != nil {
		return RevisionV2{}, err
	}
	observations, err := validateAndCloneProviderObservations(params.ProviderObservations, items)
	if err != nil {
		return RevisionV2{}, err
	}
	graph, criteria, err := canonicalizeV2(params.ProjectID, params.MissionID, params.PlanningSourceCommit, params.MissionDefinition, params.DependencyPolicy, items, lanes, observations)
	if err != nil {
		return RevisionV2{}, invalidV2("canonical", "work revision cannot be canonicalized", err)
	}
	graphDigest := digestV2("spec-kitty/work-revision-v2/graph", graph)
	criteriaDigest := digestV2("spec-kitty/work-revision-v2/criteria", criteria)
	if (claimedGraphDigest != "" && claimedGraphDigest != graphDigest) || (claimedCriteriaDigest != "" && claimedCriteriaDigest != criteriaDigest) {
		return RevisionV2{}, invalidV2("digest", "persisted WorkRevision v2 digest does not match canonical content", nil)
	}
	return RevisionV2{
		id: params.ID, projectID: params.ProjectID, missionID: params.MissionID, generation: params.Generation,
		predecessorID: params.PredecessorID, planningSourceCommit: params.PlanningSourceCommit,
		missionDefinition: params.MissionDefinition, dependencyPolicy: params.DependencyPolicy,
		workItems: items, lanes: lanes, providerObservations: observations,
		graphDigest: graphDigest, criteriaDigest: criteriaDigest, finalizedBy: params.FinalizedBy,
		finalizedAt: params.FinalizedAt, governanceContextID: params.GovernanceContextID,
		canonicalGraph: graph, canonicalCriteria: criteria,
	}, nil
}

func (revision RevisionV2) SchemaVersion() uint16             { return RevisionSchemaV2 }
func (revision RevisionV2) ID() ids.WorkRevisionID            { return revision.id }
func (revision RevisionV2) ProjectID() ids.ProjectID          { return revision.projectID }
func (revision RevisionV2) MissionID() ids.MissionID          { return revision.missionID }
func (revision RevisionV2) Generation() uint64                { return revision.generation }
func (revision RevisionV2) PredecessorID() ids.WorkRevisionID { return revision.predecessorID }
func (revision RevisionV2) PlanningSourceCommit() string      { return revision.planningSourceCommit }
func (revision RevisionV2) MissionDefinition() MissionDefinitionIdentity {
	return revision.missionDefinition
}
func (revision RevisionV2) DependencyPolicy() DependencyPolicy { return revision.dependencyPolicy }
func (revision RevisionV2) WorkItems() []WorkItemV2            { return cloneWorkItemsV2(revision.workItems) }
func (revision RevisionV2) Lanes() []LaneV2                    { return cloneLanesV2(revision.lanes) }
func (revision RevisionV2) ProviderObservations() []ProviderObservationV2 {
	return append([]ProviderObservationV2(nil), revision.providerObservations...)
}
func (revision RevisionV2) GraphDigest() string      { return revision.graphDigest }
func (revision RevisionV2) CriteriaDigest() string   { return revision.criteriaDigest }
func (revision RevisionV2) FinalizedBy() ids.ActorID { return revision.finalizedBy }
func (revision RevisionV2) FinalizedAt() time.Time   { return revision.finalizedAt }
func (revision RevisionV2) GovernanceContextID() ids.GovernanceContextID {
	return revision.governanceContextID
}
func (revision RevisionV2) CanonicalGraph() []byte {
	return append([]byte(nil), revision.canonicalGraph...)
}
func (revision RevisionV2) CanonicalCriteria() []byte {
	return append([]byte(nil), revision.canonicalCriteria...)
}

type criterionV2Wire struct {
	ID           string         `json:"criterion_id"`
	Text         string         `json:"text"`
	VerifierKind string         `json:"verifier_kind"`
	Required     bool           `json:"required"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}
type workItemV2Wire struct {
	WorkID        ids.WorkID        `json:"work_id"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	DesignContext string            `json:"design_context,omitempty"`
	Criteria      []criterionV2Wire `json:"criteria"`
	Dependencies  []ids.WorkID      `json:"dependencies"`
	WriteScopes   []string          `json:"write_scopes"`
}
type laneV2Wire struct {
	LaneID            ids.LaneID   `json:"lane_id"`
	WorkIDs           []ids.WorkID `json:"work_ids"`
	DependencyLaneIDs []ids.LaneID `json:"dependency_lane_ids"`
}
type providerObservationV2Wire struct {
	WorkID     ids.WorkID `json:"work_id"`
	Provider   string     `json:"provider"`
	ExternalID string     `json:"external_id"`
	Revision   string     `json:"revision"`
}
type missionDefinitionV2Wire struct {
	Key     string `json:"key"`
	Version uint64 `json:"version"`
	Digest  string `json:"digest"`
}
type revisionV2Envelope struct {
	SchemaVersion        uint16                      `json:"schema_version"`
	ID                   ids.WorkRevisionID          `json:"work_revision_id"`
	ProjectID            ids.ProjectID               `json:"project_id"`
	MissionID            ids.MissionID               `json:"mission_id"`
	Generation           uint64                      `json:"generation"`
	PredecessorID        ids.WorkRevisionID          `json:"predecessor_id,omitempty"`
	PlanningSourceCommit string                      `json:"planning_source_commit"`
	MissionDefinition    missionDefinitionV2Wire     `json:"mission_definition"`
	DependencyPolicy     DependencyPolicy            `json:"dependency_policy"`
	WorkItems            []workItemV2Wire            `json:"work_items"`
	Lanes                []laneV2Wire                `json:"lanes"`
	ProviderObservations []providerObservationV2Wire `json:"provider_observations"`
	GraphDigest          string                      `json:"graph_digest"`
	CriteriaDigest       string                      `json:"criteria_digest"`
	FinalizedBy          ids.ActorID                 `json:"finalized_by"`
	FinalizedAt          time.Time                   `json:"finalized_at"`
	GovernanceContextID  ids.GovernanceContextID     `json:"governance_context_id"`
}

func (revision RevisionV2) Encode() ([]byte, error) { return json.Marshal(revision.wire()) }

func (revision RevisionV2) wire() revisionV2Envelope {
	return revisionV2Envelope{
		SchemaVersion: RevisionSchemaV2, ID: revision.id, ProjectID: revision.projectID, MissionID: revision.missionID,
		Generation: revision.generation, PredecessorID: revision.predecessorID, PlanningSourceCommit: revision.planningSourceCommit,
		MissionDefinition: missionDefinitionV2Wire{revision.missionDefinition.Key, revision.missionDefinition.Version, revision.missionDefinition.Digest},
		DependencyPolicy:  revision.dependencyPolicy, WorkItems: workItemsV2Wire(revision.workItems), Lanes: lanesV2Wire(revision.lanes),
		ProviderObservations: observationsV2Wire(revision.providerObservations), GraphDigest: revision.graphDigest,
		CriteriaDigest: revision.criteriaDigest, FinalizedBy: revision.finalizedBy, FinalizedAt: revision.finalizedAt,
		GovernanceContextID: revision.governanceContextID,
	}
}

func DecodeRevisionV2(encoded []byte) (RevisionV2, error) {
	var envelope revisionV2Envelope
	if err := decodeStrictJSON(encoded, &envelope); err != nil {
		return RevisionV2{}, invalidV2("encoded", "decode persisted WorkRevision v2", err)
	}
	if err := validateV2JSONShape(encoded); err != nil {
		return RevisionV2{}, invalidV2("encoded", "decode persisted WorkRevision v2", err)
	}
	if envelope.SchemaVersion != RevisionSchemaV2 {
		return RevisionV2{}, invalidV2("schema_version", "unsupported WorkRevision schema version", nil)
	}
	params, err := paramsFromV2Wire(envelope)
	if err != nil {
		return RevisionV2{}, err
	}
	return constructRevisionV2(params, envelope.GraphDigest, envelope.CriteriaDigest)
}

func validateV2JSONShape(encoded []byte) error {
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return err
	}
	work, ok := root["work_items"].([]any)
	if !ok {
		return fmt.Errorf("work_items must be an array")
	}
	lanes, ok := root["lanes"].([]any)
	if !ok {
		return fmt.Errorf("lanes must be an array")
	}
	if _, ok := root["provider_observations"].([]any); !ok {
		return fmt.Errorf("provider_observations must be an array")
	}
	for _, rawItem := range work {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return fmt.Errorf("work item must be an object")
		}
		criteria, ok := item["criteria"].([]any)
		if !ok {
			return fmt.Errorf("criteria must be an array")
		}
		if _, ok := item["dependencies"].([]any); !ok {
			return fmt.Errorf("dependencies must be an array")
		}
		if _, ok := item["write_scopes"].([]any); !ok {
			return fmt.Errorf("write_scopes must be an array")
		}
		for _, rawCriterion := range criteria {
			criterion, ok := rawCriterion.(map[string]any)
			if !ok {
				return fmt.Errorf("criterion must be an object")
			}
			if metadata, present := criterion["metadata"]; present {
				if _, ok := metadata.(map[string]any); !ok {
					return fmt.Errorf("criterion metadata must be an object")
				}
			}
		}
	}
	for _, rawLane := range lanes {
		lane, ok := rawLane.(map[string]any)
		if !ok {
			return fmt.Errorf("lane must be an object")
		}
		if _, ok := lane["work_ids"].([]any); !ok {
			return fmt.Errorf("lane work_ids must be an array")
		}
		if _, ok := lane["dependency_lane_ids"].([]any); !ok {
			return fmt.Errorf("lane dependency_lane_ids must be an array")
		}
	}
	return nil
}

func paramsFromV2Wire(envelope revisionV2Envelope) (RevisionV2Params, error) {
	items := make([]WorkItemV2Params, len(envelope.WorkItems))
	for i, item := range envelope.WorkItems {
		criteria := make([]CriterionV2Params, len(item.Criteria))
		for j, criterion := range item.Criteria {
			metadata := cloneMetadata(criterion.Metadata)
			outcome, _ := metadata[CriterionMetadataOutcome].(string)
			evidence, _ := metadata[CriterionMetadataEvidenceHint].(string)
			delete(metadata, CriterionMetadataOutcome)
			delete(metadata, CriterionMetadataEvidenceHint)
			criteria[j] = CriterionV2Params{ID: criterion.ID, Text: criterion.Text, VerifierKind: criterion.VerifierKind, Required: criterion.Required, Outcome: outcome, EvidenceHint: evidence, Metadata: metadata}
		}
		scopes := make([]NormalizedWriteScope, len(item.WriteScopes))
		for j, encodedScope := range item.WriteScopes {
			scope, err := decodeNormalizedScope(encodedScope)
			if err != nil {
				return RevisionV2Params{}, invalidV2("write_scopes", "normalized write scope is invalid", err)
			}
			scopes[j] = scope
		}
		items[i] = WorkItemV2Params{WorkID: item.WorkID, Title: item.Title, Description: item.Description, DesignContext: item.DesignContext, Criteria: criteria, Dependencies: item.Dependencies, WriteScopes: scopes}
	}
	lanes := make([]LaneV2, len(envelope.Lanes))
	for i, lane := range envelope.Lanes {
		lanes[i] = LaneV2{id: lane.LaneID, workIDs: lane.WorkIDs, dependencyLaneIDs: lane.DependencyLaneIDs}
	}
	observations := make([]ProviderObservationV2, len(envelope.ProviderObservations))
	for i, item := range envelope.ProviderObservations {
		observations[i] = ProviderObservationV2(item)
	}
	return RevisionV2Params{
		ID: envelope.ID, ProjectID: envelope.ProjectID, MissionID: envelope.MissionID, Generation: envelope.Generation,
		PredecessorID: envelope.PredecessorID, PlanningSourceCommit: envelope.PlanningSourceCommit,
		MissionDefinition: MissionDefinitionIdentity{envelope.MissionDefinition.Key, envelope.MissionDefinition.Version, envelope.MissionDefinition.Digest},
		DependencyPolicy:  envelope.DependencyPolicy, WorkItems: items, Lanes: lanes, ProviderObservations: observations,
		FinalizedBy: envelope.FinalizedBy, FinalizedAt: envelope.FinalizedAt, GovernanceContextID: envelope.GovernanceContextID,
	}, nil
}

func buildWorkItemsV2(input []WorkItemV2Params) ([]WorkItemV2, error) {
	if len(input) == 0 {
		return nil, invalidV2("work_items", "at least one work item is required", nil)
	}
	items := make([]WorkItemV2, len(input))
	seen := make(map[ids.WorkID]struct{}, len(input))
	for i, item := range input {
		if _, err := ids.ParseWorkID(item.WorkID.String()); err != nil {
			return nil, invalidV2("work_id", "work identity is invalid", err)
		}
		if _, duplicate := seen[item.WorkID]; duplicate {
			return nil, invalidV2("work_id", "work identity occurs more than once", nil)
		}
		seen[item.WorkID] = struct{}{}
		if !boundedText(item.Title, 160) || !boundedText(item.Description, 4096) || (item.DesignContext != "" && !boundedText(item.DesignContext, 4096)) {
			return nil, invalidV2("work_context", "work title, description, or design context is invalid", nil)
		}
		criteria, err := buildCriteriaV2(item.Criteria)
		if err != nil {
			return nil, err
		}
		dependencies := append([]ids.WorkID{}, item.Dependencies...)
		sort.Slice(dependencies, func(a, b int) bool { return dependencies[a] < dependencies[b] })
		for j, dependency := range dependencies {
			if _, err := ids.ParseWorkID(dependency.String()); err != nil || dependency == item.WorkID || (j > 0 && dependency == dependencies[j-1]) {
				return nil, invalidV2("dependencies", "work dependencies are invalid or duplicated", err)
			}
		}
		scopes := append([]NormalizedWriteScope(nil), item.WriteScopes...)
		if len(scopes) == 0 {
			return nil, invalidV2("write_scopes", "at least one normalized write scope is required", nil)
		}
		sort.Slice(scopes, func(a, b int) bool { return normalizedScopeString(scopes[a]) < normalizedScopeString(scopes[b]) })
		for j, scope := range scopes {
			if !validNormalizedScope(scope) || (j > 0 && normalizedScopeString(scope) == normalizedScopeString(scopes[j-1])) {
				return nil, invalidV2("write_scopes", "write scopes are invalid or duplicated", nil)
			}
		}
		items[i] = WorkItemV2{item.WorkID, item.Title, item.Description, item.DesignContext, criteria, dependencies, scopes}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].workID < items[j].workID })
	for _, item := range items {
		for _, dependency := range item.dependencies {
			if _, exists := seen[dependency]; !exists {
				return nil, invalidV2("dependencies", "dependency references unknown work", nil)
			}
		}
	}
	return items, nil
}

func buildCriteriaV2(input []CriterionV2Params) ([]CriterionV2, error) {
	if len(input) == 0 {
		return nil, invalidV2("criteria", "at least one criterion is required", nil)
	}
	criteria := make([]CriterionV2, len(input))
	seen := make(map[string]struct{}, len(input))
	for i, criterion := range input {
		if !stableToken(criterion.ID) || !boundedText(criterion.Text, 1024) || !validVerifier(criterion.VerifierKind) || !boundedText(criterion.Outcome, 1024) || (criterion.EvidenceHint != "" && !boundedText(criterion.EvidenceHint, 1024)) {
			return nil, invalidV2("criteria", "criterion identity or semantic content is invalid", nil)
		}
		if _, duplicate := seen[criterion.ID]; duplicate {
			return nil, invalidV2("criteria", "criterion identity occurs more than once", nil)
		}
		seen[criterion.ID] = struct{}{}
		metadata, err := normalizeMetadata(criterion.Metadata)
		if err != nil {
			return nil, invalidV2("criteria.metadata", "criterion metadata is invalid or unbounded", err)
		}
		if _, collision := metadata[CriterionMetadataOutcome]; collision {
			return nil, invalidV2("criteria.metadata", "criterion metadata uses a reserved semantic key", nil)
		}
		if _, collision := metadata[CriterionMetadataEvidenceHint]; collision {
			return nil, invalidV2("criteria.metadata", "criterion metadata uses a reserved semantic key", nil)
		}
		metadata[CriterionMetadataOutcome] = criterion.Outcome
		if criterion.EvidenceHint != "" {
			metadata[CriterionMetadataEvidenceHint] = criterion.EvidenceHint
		}
		if _, err := canonicalMetadata(metadata); err != nil {
			return nil, invalidV2("criteria.metadata", "criterion metadata is invalid or unbounded", err)
		}
		criteria[i] = CriterionV2{criterion.ID, criterion.Text, criterion.VerifierKind, criterion.Required, metadata}
	}
	sort.Slice(criteria, func(i, j int) bool { return criteria[i].id < criteria[j].id })
	return criteria, nil
}

func validateAndCloneLanesV2(input []LaneV2, items []WorkItemV2) ([]LaneV2, error) {
	if len(input) == 0 {
		return nil, invalidV2("lanes", "at least one lane is required", nil)
	}
	lanes := cloneLanesV2(input)
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].id < lanes[j].id })
	knownWork := make(map[ids.WorkID]struct{}, len(items))
	for _, item := range items {
		knownWork[item.workID] = struct{}{}
	}
	membership := make(map[ids.WorkID]struct{}, len(items))
	workLane := make(map[ids.WorkID]ids.LaneID, len(items))
	knownLane := make(map[ids.LaneID]struct{}, len(lanes))
	for i := range lanes {
		if _, err := ids.ParseLaneID(lanes[i].id.String()); err != nil {
			return nil, invalidV2("lane_id", "lane identity is invalid", err)
		}
		if _, duplicate := knownLane[lanes[i].id]; duplicate {
			return nil, invalidV2("lane_id", "lane identity occurs more than once", nil)
		}
		knownLane[lanes[i].id] = struct{}{}
		sort.Slice(lanes[i].workIDs, func(a, b int) bool { return lanes[i].workIDs[a] < lanes[i].workIDs[b] })
		if len(lanes[i].workIDs) == 0 {
			return nil, invalidV2("lanes", "lane membership is required", nil)
		}
		if stableLaneID(lanes[i].workIDs) != lanes[i].id {
			return nil, invalidV2("lane_id", "lane identity does not match canonical membership", nil)
		}
		for _, workID := range lanes[i].workIDs {
			if _, ok := knownWork[workID]; !ok {
				return nil, invalidV2("lanes", "lane references unknown work", nil)
			}
			if _, dup := membership[workID]; dup {
				return nil, invalidV2("lanes", "work occurs in more than one lane", nil)
			}
			membership[workID] = struct{}{}
			workLane[workID] = lanes[i].id
		}
		sort.Slice(lanes[i].dependencyLaneIDs, func(a, b int) bool { return lanes[i].dependencyLaneIDs[a] < lanes[i].dependencyLaneIDs[b] })
	}
	if len(membership) != len(items) {
		return nil, invalidV2("lanes", "lane membership does not cover every work item", nil)
	}
	expectedEdges := make(map[ids.LaneID]map[ids.LaneID]struct{}, len(lanes))
	for _, item := range items {
		blockedLane := workLane[item.workID]
		for _, dependency := range item.dependencies {
			dependencyLane := workLane[dependency]
			if dependencyLane == blockedLane {
				continue
			}
			if expectedEdges[blockedLane] == nil {
				expectedEdges[blockedLane] = make(map[ids.LaneID]struct{})
			}
			expectedEdges[blockedLane][dependencyLane] = struct{}{}
		}
	}
	edges := make(map[string][]string, len(lanes))
	nodes := make([]string, len(lanes))
	for i, lane := range lanes {
		nodes[i] = lane.id.String()
		for j, dependency := range lane.dependencyLaneIDs {
			if _, ok := knownLane[dependency]; !ok || dependency == lane.id || (j > 0 && dependency == lane.dependencyLaneIDs[j-1]) {
				return nil, invalidV2("lanes", "lane dependency is invalid or duplicated", nil)
			}
			edges[lane.id.String()] = append(edges[lane.id.String()], dependency.String())
		}
		if len(lane.dependencyLaneIDs) != len(expectedEdges[lane.id]) {
			return nil, invalidV2("lanes", "lane dependencies do not match work dependencies", nil)
		}
		for _, dependency := range lane.dependencyLaneIDs {
			if _, expected := expectedEdges[lane.id][dependency]; !expected {
				return nil, invalidV2("lanes", "lane dependencies do not match work dependencies", nil)
			}
		}
	}
	if cyclic(nodes, edges) {
		return nil, newError(KindLaneCycle, "lane dependency graph contains a cycle", nil)
	}
	return lanes, nil
}

func validateAndCloneProviderObservations(input []ProviderObservationV2, items []WorkItemV2) ([]ProviderObservationV2, error) {
	if len(input) != len(items) {
		return nil, invalidV2("provider_observations", "provider observations must exactly cover work", nil)
	}
	known := make(map[ids.WorkID]struct{}, len(items))
	for _, item := range items {
		known[item.workID] = struct{}{}
	}
	result := append([]ProviderObservationV2(nil), input...)
	seen := make(map[ids.WorkID]struct{}, len(input))
	for _, item := range result {
		if _, ok := known[item.WorkID]; !ok {
			return nil, invalidV2("provider_observations", "observation references unknown work", nil)
		}
		if _, duplicate := seen[item.WorkID]; duplicate {
			return nil, invalidV2("provider_observations", "work observation occurs more than once", nil)
		}
		seen[item.WorkID] = struct{}{}
		if !stableToken(item.Provider) || !boundedOpaque(item.ExternalID, 512) || !boundedOpaque(item.Revision, 512) {
			return nil, invalidV2("provider_observations", "provider mapping or revision is invalid", nil)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		if result[i].ExternalID != result[j].ExternalID {
			return result[i].ExternalID < result[j].ExternalID
		}
		return result[i].WorkID < result[j].WorkID
	})
	return result, nil
}

type canonicalGraphV2 struct {
	ProjectID            ids.ProjectID           `json:"project_id"`
	MissionID            ids.MissionID           `json:"mission_id"`
	PlanningSourceCommit string                  `json:"planning_source_commit"`
	MissionDefinition    missionDefinitionV2Wire `json:"mission_definition"`
	DependencyPolicy     DependencyPolicy        `json:"dependency_policy"`
	WorkItems            []workItemV2Wire        `json:"work_items"`
	Lanes                []laneV2Wire            `json:"lanes"`
	ProviderMappings     []providerMappingV2Wire `json:"provider_mappings"`
}
type providerMappingV2Wire struct {
	WorkID     ids.WorkID `json:"work_id"`
	Provider   string     `json:"provider"`
	ExternalID string     `json:"external_id"`
}
type canonicalCriteriaV2 struct {
	WorkID   ids.WorkID        `json:"work_id"`
	Criteria []criterionV2Wire `json:"criteria"`
}

func canonicalizeV2(projectID ids.ProjectID, missionID ids.MissionID, planning string, definition MissionDefinitionIdentity, policy DependencyPolicy, items []WorkItemV2, lanes []LaneV2, observations []ProviderObservationV2) ([]byte, []byte, error) {
	itemWire := workItemsV2Wire(items)
	laneWire := lanesV2Wire(lanes)
	mappings := make([]providerMappingV2Wire, len(observations))
	for i, item := range observations {
		mappings[i] = providerMappingV2Wire{item.WorkID, item.Provider, item.ExternalID}
	}
	graph, err := json.Marshal(canonicalGraphV2{projectID, missionID, planning, missionDefinitionV2Wire(definition), policy, itemWire, laneWire, mappings})
	if err != nil {
		return nil, nil, err
	}
	criteria := make([]canonicalCriteriaV2, len(items))
	for i, item := range itemWire {
		criteria[i] = canonicalCriteriaV2{item.WorkID, item.Criteria}
	}
	criteriaBytes, err := json.Marshal(criteria)
	return graph, criteriaBytes, err
}

func workItemsV2Wire(items []WorkItemV2) []workItemV2Wire {
	result := make([]workItemV2Wire, len(items))
	for i, item := range items {
		criteria := make([]criterionV2Wire, len(item.criteria))
		for j, c := range item.criteria {
			criteria[j] = criterionV2Wire{c.id, c.text, c.verifierKind, c.required, cloneMetadata(c.metadata)}
		}
		scopes := make([]string, len(item.writeScopes))
		for j, s := range item.writeScopes {
			scopes[j] = normalizedScopeString(s)
		}
		result[i] = workItemV2Wire{item.workID, item.title, item.description, item.designContext, criteria, append([]ids.WorkID{}, item.dependencies...), scopes}
	}
	return result
}
func lanesV2Wire(lanes []LaneV2) []laneV2Wire {
	result := make([]laneV2Wire, len(lanes))
	for i, l := range lanes {
		result[i] = laneV2Wire{l.id, append([]ids.WorkID{}, l.workIDs...), append([]ids.LaneID{}, l.dependencyLaneIDs...)}
	}
	return result
}
func observationsV2Wire(items []ProviderObservationV2) []providerObservationV2Wire {
	result := make([]providerObservationV2Wire, len(items))
	for i, item := range items {
		result[i] = providerObservationV2Wire(item)
	}
	return result
}

func normalizedScopeString(scope NormalizedWriteScope) string {
	encoded, _ := json.Marshal(struct {
		Path             string      `json:"path"`
		Domain           string      `json:"domain"`
		Access           ScopeAccess `json:"access"`
		SerializationKey string      `json:"serialization_key,omitempty"`
	}{scope.Path, scope.Domain, scope.Access, scope.SerializationKey})
	return string(encoded)
}
func decodeNormalizedScope(value string) (NormalizedWriteScope, error) {
	var wire struct {
		Path             string      `json:"path"`
		Domain           string      `json:"domain"`
		Access           ScopeAccess `json:"access"`
		SerializationKey string      `json:"serialization_key,omitempty"`
	}
	if err := decodeStrictJSON([]byte(value), &wire); err != nil {
		return NormalizedWriteScope{}, err
	}
	scope := NormalizedWriteScope{wire.Path, wire.Domain, wire.Access, wire.SerializationKey}
	if !validNormalizedScope(scope) {
		return NormalizedWriteScope{}, fmt.Errorf("invalid normalized scope")
	}
	return scope, nil
}
func validNormalizedScope(scope NormalizedWriteScope) bool {
	return validRepositoryPath(scope.Path) && stableToken(scope.Domain) && (scope.Access == ScopeReadOnly || scope.Access == ScopeWriteExclusive) && (scope.SerializationKey == "" || stableToken(scope.SerializationKey))
}

func canonicalMetadata(metadata map[string]any) ([]byte, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if err := validateMetadataValue(metadata, 0); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 4096 {
		return nil, fmt.Errorf("metadata exceeds 4096 bytes")
	}
	return encoded, nil
}
func normalizeMetadata(input map[string]any) (map[string]any, error) {
	if input == nil {
		return map[string]any{}, nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, fmt.Errorf("metadata must be a JSON object")
	}
	if _, err := canonicalMetadata(result); err != nil {
		return nil, err
	}
	return result, nil
}
func validateMetadataValue(value any, depth int) error {
	if depth > 8 {
		return fmt.Errorf("metadata nesting exceeds limit")
	}
	switch typed := value.(type) {
	case nil, bool, string:
		return nil
	case json.Number:
		if _, err := strconv.ParseInt(string(typed), 10, 64); err != nil {
			return fmt.Errorf("metadata numbers must be unambiguous integers")
		}
		return nil
	case []any:
		if len(typed) > 64 {
			return fmt.Errorf("metadata array exceeds limit")
		}
		for _, item := range typed {
			if err := validateMetadataValue(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		if len(typed) > 64 {
			return fmt.Errorf("metadata object exceeds limit")
		}
		for key, item := range typed {
			if !stableToken(key) {
				return fmt.Errorf("metadata key is invalid")
			}
			if err := validateMetadataValue(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("metadata contains unsupported value %T", value)
	}
}
func cloneMetadata(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if decoder.Decode(&result) != nil {
		return map[string]any{}
	}
	return result
}
func cloneCriteriaV2(input []CriterionV2) []CriterionV2 {
	result := make([]CriterionV2, len(input))
	for i, item := range input {
		result[i] = item
		result[i].metadata = cloneMetadata(item.metadata)
	}
	return result
}
func cloneWorkItemsV2(input []WorkItemV2) []WorkItemV2 {
	result := make([]WorkItemV2, len(input))
	for i, item := range input {
		result[i] = item
		result[i].criteria = cloneCriteriaV2(item.criteria)
		result[i].dependencies = append([]ids.WorkID{}, item.dependencies...)
		result[i].writeScopes = append([]NormalizedWriteScope(nil), item.writeScopes...)
	}
	return result
}
func cloneLanesV2(input []LaneV2) []LaneV2 {
	result := make([]LaneV2, len(input))
	for i, item := range input {
		result[i] = item
		result[i].workIDs = append([]ids.WorkID{}, item.workIDs...)
		result[i].dependencyLaneIDs = append([]ids.LaneID{}, item.dependencyLaneIDs...)
	}
	return result
}
func digestV2(domain string, material []byte) string {
	sum := sha256.Sum256(append(append([]byte(domain), 0), material...))
	return hex.EncodeToString(sum[:])
}
func lowerDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && value == fmt.Sprintf("%x", decoded)
}
func gitObjectID(value string) bool {
	if len(value) < 40 || len(value) > 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}
func boundedOpaque(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00')
}
func invalidV2(field, message string, cause error) *Error {
	return &Error{Kind: KindInvalidRevision, Message: message, Fields: map[string]string{"field": field}, Cause: cause}
}

func decodeStrictJSON(encoded []byte, target any) error {
	if err := rejectDuplicateJSONKeys(encoded); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}
func rejectDuplicateJSONKeys(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}
func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("unterminated JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	return nil
}
