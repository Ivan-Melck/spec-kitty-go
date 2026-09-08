package workgraph

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

const maxRevisionV3ArtifactObservations = 16

// ArtifactObservationV3 pins an immutable definition artifact by exact
// revision and content. Kind is a closed stable token at this boundary; the
// artifact package remains an independent capability owner.
type ArtifactObservationV3 struct {
	Kind               string `json:"kind"`
	ArtifactRevisionID string `json:"artifact_revision_id"`
	ContentDigest      string `json:"content_digest"`
}

// ProviderObservationV3 retains complete attributable observation evidence.
// Provider revision remains provenance: it participates in the WorkRevision
// content identity but not the semantic graph digest.
type ProviderObservationV3 struct {
	WorkID                 ids.WorkID        `json:"work_id"`
	Provider               string            `json:"provider"`
	ExternalID             string            `json:"external_id"`
	Revision               string            `json:"revision"`
	MetadataSchema         uint16            `json:"metadata_schema"`
	DefinitionDigest       string            `json:"definition_digest"`
	CriterionEvidenceHints map[string]string `json:"criterion_evidence_hints"`
}

type RevisionV3Params struct {
	ProjectID            ids.ProjectID
	MissionID            ids.MissionID
	Generation           uint64
	PredecessorID        ids.WorkRevisionID
	PlanningSourceCommit string
	MissionDefinition    MissionDefinitionIdentity
	DependencyPolicy     DependencyPolicy
	WorkItems            []WorkItemV2Params
	Lanes                []LaneV2
	ArtifactObservations []ArtifactObservationV3
	ProviderEvidence     []ProviderObservationV3
	FinalizedBy          ids.ActorID
	FinalizedAt          time.Time
	GovernanceContextID  ids.GovernanceContextID
}

// NewRevisionV3 constructs the M2 executable WorkRevision. Its identity is
// derived from complete canonical content; callers cannot select it.
func NewRevisionV3(params RevisionV3Params) (RevisionV2, error) {
	artifacts, err := validateArtifactObservationsV3(params.ArtifactObservations)
	if err != nil {
		return RevisionV2{}, err
	}
	evidence, providers, err := validateProviderEvidenceV3(params.ProviderEvidence, params.WorkItems)
	if err != nil {
		return RevisionV2{}, err
	}
	baseParams := RevisionV2Params{
		ID: "work-revision-v3-candidate", ProjectID: params.ProjectID, MissionID: params.MissionID,
		Generation: params.Generation, PredecessorID: params.PredecessorID, PlanningSourceCommit: params.PlanningSourceCommit,
		MissionDefinition: params.MissionDefinition, DependencyPolicy: params.DependencyPolicy, WorkItems: params.WorkItems,
		Lanes: params.Lanes, ProviderObservations: providers, FinalizedBy: params.FinalizedBy,
		FinalizedAt: params.FinalizedAt, GovernanceContextID: params.GovernanceContextID,
	}
	base, err := constructRevisionV2(baseParams, "", "")
	if err != nil {
		return RevisionV2{}, err
	}
	semantic, err := json.Marshal(struct {
		SchemaVersion        uint16                  `json:"schema_version"`
		BaseGraph            json.RawMessage         `json:"base_graph"`
		ArtifactObservations []ArtifactObservationV3 `json:"artifact_observations"`
	}{RevisionSchemaV3, base.CanonicalGraph(), artifacts})
	if err != nil {
		return RevisionV2{}, invalidV2("canonical", "WorkRevision v3 semantic graph cannot be canonicalized", err)
	}
	graphDigest := digestV2("spec-kitty/work-revision-v3/graph", semantic)
	content, err := json.Marshal(struct {
		SchemaVersion       uint16                  `json:"schema_version"`
		Generation          uint64                  `json:"generation"`
		PredecessorID       ids.WorkRevisionID      `json:"predecessor_id,omitempty"`
		SemanticGraph       json.RawMessage         `json:"semantic_graph"`
		ProviderEvidence    []ProviderObservationV3 `json:"provider_evidence"`
		CriteriaDigest      string                  `json:"criteria_digest"`
		FinalizedBy         ids.ActorID             `json:"finalized_by"`
		FinalizedAt         time.Time               `json:"finalized_at"`
		GovernanceContextID ids.GovernanceContextID `json:"governance_context_id"`
	}{RevisionSchemaV3, params.Generation, params.PredecessorID, semantic, evidence, base.CriteriaDigest(), params.FinalizedBy, params.FinalizedAt, params.GovernanceContextID})
	if err != nil {
		return RevisionV2{}, invalidV2("canonical", "WorkRevision v3 content cannot be canonicalized", err)
	}
	contentDigest := digestV2("spec-kitty/work-revision-v3/content", content)
	baseParams.ID = ids.WorkRevisionID("wrv3-" + contentDigest)
	base, err = constructRevisionV2(baseParams, "", "")
	if err != nil {
		return RevisionV2{}, err
	}
	base.schemaVersion = RevisionSchemaV3
	base.artifactObservations = artifacts
	base.providerEvidence = evidence
	base.graphDigest = graphDigest
	base.canonicalGraph = append([]byte(nil), semantic...)
	base.contentDigest = contentDigest
	return base, nil
}

func validateArtifactObservationsV3(input []ArtifactObservationV3) ([]ArtifactObservationV3, error) {
	if len(input) == 0 || len(input) > maxRevisionV3ArtifactObservations {
		return nil, invalidV2("artifact_observations", "complete bounded artifact observations are required", nil)
	}
	result := append([]ArtifactObservationV3(nil), input...)
	sort.Slice(result, func(i, j int) bool { return result[i].Kind < result[j].Kind })
	hasPlan := false
	for index, item := range result {
		if !stableToken(item.Kind) || !stableToken(item.ArtifactRevisionID) || !lowerDigest(item.ContentDigest) || (index > 0 && item.Kind == result[index-1].Kind) {
			return nil, invalidV2("artifact_observations", "artifact observations are invalid or duplicated", nil)
		}
		hasPlan = hasPlan || item.Kind == "plan"
	}
	if !hasPlan {
		return nil, invalidV2("artifact_observations", "the exact plan artifact observation is required", nil)
	}
	return result, nil
}

func validateProviderEvidenceV3(input []ProviderObservationV3, work []WorkItemV2Params) ([]ProviderObservationV3, []ProviderObservationV2, error) {
	if len(input) != len(work) || len(input) == 0 {
		return nil, nil, invalidV2("provider_evidence", "provider evidence must exactly cover work", nil)
	}
	known := make(map[ids.WorkID]map[string]struct{}, len(work))
	for _, item := range work {
		criteria := make(map[string]struct{}, len(item.Criteria))
		for _, criterion := range item.Criteria {
			criteria[criterion.ID] = struct{}{}
		}
		known[item.WorkID] = criteria
	}
	result := make([]ProviderObservationV3, len(input))
	seen := make(map[ids.WorkID]struct{}, len(input))
	providers := make([]ProviderObservationV2, len(input))
	for index, item := range input {
		criteria, exists := known[item.WorkID]
		if _, duplicate := seen[item.WorkID]; !exists || duplicate || !stableToken(item.Provider) || !boundedOpaque(item.ExternalID, 512) || !boundedOpaque(item.Revision, 512) || item.MetadataSchema == 0 || !lowerDigest(item.DefinitionDigest) {
			return nil, nil, invalidV2("provider_evidence", "provider evidence is invalid, duplicated, or outside work", nil)
		}
		seen[item.WorkID] = struct{}{}
		hints := make(map[string]string, len(item.CriterionEvidenceHints))
		for criterionID, hint := range item.CriterionEvidenceHints {
			if _, ok := criteria[criterionID]; !ok || !boundedText(hint, 1024) {
				return nil, nil, invalidV2("provider_evidence", "criterion evidence hint is invalid", nil)
			}
			hints[criterionID] = hint
		}
		item.CriterionEvidenceHints = hints
		result[index] = item
		providers[index] = ProviderObservationV2{WorkID: item.WorkID, Provider: item.Provider, ExternalID: item.ExternalID, Revision: item.Revision}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].WorkID < result[j].WorkID })
	sort.Slice(providers, func(i, j int) bool { return providers[i].WorkID < providers[j].WorkID })
	return result, providers, nil
}

type revisionV3Envelope struct {
	SchemaVersion        uint16                  `json:"schema_version"`
	ID                   ids.WorkRevisionID      `json:"work_revision_id"`
	ProjectID            ids.ProjectID           `json:"project_id"`
	MissionID            ids.MissionID           `json:"mission_id"`
	Generation           uint64                  `json:"generation"`
	PredecessorID        ids.WorkRevisionID      `json:"predecessor_id,omitempty"`
	PlanningSourceCommit string                  `json:"planning_source_commit"`
	MissionDefinition    missionDefinitionV2Wire `json:"mission_definition"`
	DependencyPolicy     DependencyPolicy        `json:"dependency_policy"`
	WorkItems            []workItemV2Wire        `json:"work_items"`
	Lanes                []laneV2Wire            `json:"lanes"`
	ArtifactObservations []ArtifactObservationV3 `json:"artifact_observations"`
	ProviderEvidence     []ProviderObservationV3 `json:"provider_evidence"`
	GraphDigest          string                  `json:"graph_digest"`
	CriteriaDigest       string                  `json:"criteria_digest"`
	ContentDigest        string                  `json:"content_digest"`
	FinalizedBy          ids.ActorID             `json:"finalized_by"`
	FinalizedAt          time.Time               `json:"finalized_at"`
	GovernanceContextID  ids.GovernanceContextID `json:"governance_context_id"`
}

func (revision RevisionV2) encodeV3() ([]byte, error) {
	if revision.schemaVersion != RevisionSchemaV3 || revision.contentDigest == "" {
		return nil, invalidV2("schema_version", "WorkRevision v3 is incomplete", nil)
	}
	encoded, err := json.Marshal(revisionV3Envelope{RevisionSchemaV3, revision.id, revision.projectID, revision.missionID, revision.generation, revision.predecessorID, revision.planningSourceCommit,
		missionDefinitionV2Wire(revision.missionDefinition), revision.dependencyPolicy, workItemsV2Wire(revision.workItems), lanesV2Wire(revision.lanes),
		revision.ArtifactObservations(), revision.ProviderEvidence(), revision.graphDigest, revision.criteriaDigest, revision.contentDigest, revision.finalizedBy, revision.finalizedAt, revision.governanceContextID})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func decodeRevisionV3(encoded []byte) (RevisionV2, error) {
	var envelope revisionV3Envelope
	if err := decodeStrictJSON(encoded, &envelope); err != nil || envelope.SchemaVersion != RevisionSchemaV3 || len(envelope.ArtifactObservations) == 0 || len(envelope.ProviderEvidence) == 0 {
		return RevisionV2{}, invalidV2("encoded", "decode persisted WorkRevision v3", err)
	}
	v2Envelope := revisionV2Envelope{SchemaVersion: RevisionSchemaV2, ID: envelope.ID, ProjectID: envelope.ProjectID, MissionID: envelope.MissionID, Generation: envelope.Generation, PredecessorID: envelope.PredecessorID,
		PlanningSourceCommit: envelope.PlanningSourceCommit, MissionDefinition: envelope.MissionDefinition, DependencyPolicy: envelope.DependencyPolicy, WorkItems: envelope.WorkItems, Lanes: envelope.Lanes,
		ProviderObservations: make([]providerObservationV2Wire, len(envelope.ProviderEvidence)), GraphDigest: envelope.GraphDigest, CriteriaDigest: envelope.CriteriaDigest, FinalizedBy: envelope.FinalizedBy, FinalizedAt: envelope.FinalizedAt, GovernanceContextID: envelope.GovernanceContextID}
	for index, item := range envelope.ProviderEvidence {
		v2Envelope.ProviderObservations[index] = providerObservationV2Wire{item.WorkID, item.Provider, item.ExternalID, item.Revision}
	}
	params, err := paramsFromV2Wire(v2Envelope)
	if err != nil {
		return RevisionV2{}, err
	}
	result, err := NewRevisionV3(RevisionV3Params{ProjectID: params.ProjectID, MissionID: params.MissionID, Generation: params.Generation, PredecessorID: params.PredecessorID, PlanningSourceCommit: params.PlanningSourceCommit, MissionDefinition: params.MissionDefinition, DependencyPolicy: params.DependencyPolicy, WorkItems: params.WorkItems, Lanes: params.Lanes, ArtifactObservations: envelope.ArtifactObservations, ProviderEvidence: envelope.ProviderEvidence, FinalizedBy: params.FinalizedBy, FinalizedAt: params.FinalizedAt, GovernanceContextID: params.GovernanceContextID})
	if err != nil {
		return RevisionV2{}, err
	}
	if result.ID() != envelope.ID || result.GraphDigest() != envelope.GraphDigest || result.CriteriaDigest() != envelope.CriteriaDigest || result.ContentDigest() != envelope.ContentDigest {
		return RevisionV2{}, invalidV2("digest", "persisted WorkRevision v3 identity or digest is mismatched", nil)
	}
	reencoded, err := result.encodeV3()
	if err != nil || !bytes.Equal(reencoded, encoded) {
		return RevisionV2{}, invalidV2("encoded", "persisted WorkRevision v3 is not canonical", err)
	}
	return result, nil
}

func cloneProviderEvidenceV3(input []ProviderObservationV3) []ProviderObservationV3 {
	result := make([]ProviderObservationV3, len(input))
	for index, item := range input {
		result[index] = item
		result[index].CriterionEvidenceHints = make(map[string]string, len(item.CriterionEvidenceHints))
		for key, value := range item.CriterionEvidenceHints {
			result[index].CriterionEvidenceHints[key] = value
		}
	}
	return result
}
