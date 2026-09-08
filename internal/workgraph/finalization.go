package workgraph

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

const KindProviderChanged ErrorKind = "provider_changed_during_observation"

// ObservationEvidence is provider-neutral audit evidence for one exact mapped
// work item. DefinitionDigest and MetadataSchema participate in the bounded
// recheck; only provider/external mapping participates in semantic graph identity.
type ObservationEvidence struct {
	WorkID                 ids.WorkID
	Provider               string
	ExternalID             string
	Revision               string
	MetadataSchema         uint16
	DefinitionDigest       string
	CriterionEvidenceHints map[string]string
}

type FinalizationObservation struct {
	draft     DraftObservation
	evidence  []ObservationEvidence
	canonical []byte
}

func NewFinalizationObservation(draft DraftObservation, evidence []ObservationEvidence) (FinalizationObservation, error) {
	work := draft.Work()
	if len(work) == 0 || len(evidence) != len(work) {
		return FinalizationObservation{}, invalidV2("provider_observations", "provider evidence must exactly cover observed work", nil)
	}
	known := make(map[ids.WorkID]WorkObservation, len(work))
	for _, item := range work {
		known[item.WorkID()] = item
	}
	copyEvidence := make([]ObservationEvidence, len(evidence))
	seen := make(map[ids.WorkID]struct{}, len(evidence))
	mappings := make(map[string]ids.WorkID, len(evidence))
	for i, item := range evidence {
		if _, exists := known[item.WorkID]; !exists {
			return FinalizationObservation{}, invalidV2("provider_observations", "provider evidence references unknown work", nil)
		}
		if _, duplicate := seen[item.WorkID]; duplicate {
			return FinalizationObservation{}, invalidV2("provider_observations", "provider evidence occurs more than once", nil)
		}
		seen[item.WorkID] = struct{}{}
		if !stableToken(item.Provider) || !boundedOpaque(item.ExternalID, 512) || !boundedOpaque(item.Revision, 512) || item.MetadataSchema == 0 || !lowerDigest(item.DefinitionDigest) {
			return FinalizationObservation{}, invalidV2("provider_observations", "provider evidence is incomplete", nil)
		}
		mappingKey := item.Provider + "\x00" + item.ExternalID
		if owner, duplicate := mappings[mappingKey]; duplicate {
			return FinalizationObservation{}, newError(KindInvalidRevision, "provider mapping identity is assigned to more than one work item", map[string]string{"provider": item.Provider, "work_id": owner.String(), "related_work_id": item.WorkID.String()})
		}
		mappings[mappingKey] = item.WorkID
		criteria := make(map[string]struct{}, len(known[item.WorkID].Criteria()))
		for _, criterion := range known[item.WorkID].Criteria() {
			criteria[criterion.ID] = struct{}{}
		}
		hints := make(map[string]string, len(item.CriterionEvidenceHints))
		for criterionID, hint := range item.CriterionEvidenceHints {
			if _, exists := criteria[criterionID]; !exists || !boundedText(hint, 1024) {
				return FinalizationObservation{}, invalidV2("criterion_evidence", "criterion evidence hint is invalid", nil)
			}
			hints[criterionID] = hint
		}
		copyEvidence[i] = item
		copyEvidence[i].CriterionEvidenceHints = hints
	}
	sort.Slice(copyEvidence, func(i, j int) bool { return copyEvidence[i].WorkID < copyEvidence[j].WorkID })
	canonical, err := canonicalFinalizationObservation(draft, copyEvidence)
	if err != nil {
		return FinalizationObservation{}, invalidV2("provider_observations", "provider evidence cannot be canonicalized", err)
	}
	return FinalizationObservation{draft: draft, evidence: copyEvidence, canonical: canonical}, nil
}

func (observation FinalizationObservation) Draft() DraftObservation { return observation.draft }
func (observation FinalizationObservation) Evidence() []ObservationEvidence {
	result := make([]ObservationEvidence, len(observation.evidence))
	for i, item := range observation.evidence {
		result[i] = item
		result[i].CriterionEvidenceHints = cloneStringMap(item.CriterionEvidenceHints)
	}
	return result
}
func (observation FinalizationObservation) CanonicalEvidence() []byte {
	return append([]byte(nil), observation.canonical...)
}

// CompareFinalizationObservations enforces the exact second observation. It
// deliberately cannot fence a provider change after this bounded comparison;
// such a later mutation is drift against the pinned published evidence.
func CompareFinalizationObservations(first, second FinalizationObservation) error {
	if len(first.canonical) == 0 || len(second.canonical) == 0 || !bytes.Equal(first.canonical, second.canonical) {
		return newError(KindProviderChanged, "provider work changed during finalization observation", nil)
	}
	return nil
}

type GenerationOneCandidateParams struct {
	ID                   ids.WorkRevisionID
	Observation          FinalizationObservation
	PlanningSourceCommit string
	MissionDefinition    MissionDefinitionIdentity
	DependencyPolicy     DependencyPolicy
	FinalizedBy          ids.ActorID
	FinalizedAt          time.Time
	GovernanceContextID  ids.GovernanceContextID
}

type GenerationOneCandidateV3Params struct {
	Observation          FinalizationObservation
	PlanningSourceCommit string
	MissionDefinition    MissionDefinitionIdentity
	DependencyPolicy     DependencyPolicy
	FinalizedBy          ids.ActorID
	FinalizedAt          time.Time
	GovernanceContextID  ids.GovernanceContextID
}

// BuildGenerationOneCandidateV3 retains the exact definition artifact and all
// attributable provider evidence that the frozen v2 candidate dropped. New M2
// authority must use this constructor; the v2 constructor remains historical.
func BuildGenerationOneCandidateV3(params GenerationOneCandidateV3Params) (RevisionV2, error) {
	findings := ValidateDraft(params.Observation.draft, DefaultQualityPolicy())
	if len(findings) != 0 {
		return RevisionV2{}, invalidV2("work", fmt.Sprintf("work observation has %d validation finding(s)", len(findings)), nil)
	}
	lanes, err := CollapseLanes(params.Observation.draft)
	if err != nil {
		return RevisionV2{}, err
	}
	evidenceByWork := make(map[ids.WorkID]ObservationEvidence, len(params.Observation.evidence))
	providerEvidence := make([]ProviderObservationV3, len(params.Observation.evidence))
	for index, item := range params.Observation.evidence {
		evidenceByWork[item.WorkID] = item
		providerEvidence[index] = ProviderObservationV3{WorkID: item.WorkID, Provider: item.Provider, ExternalID: item.ExternalID, Revision: item.Revision, MetadataSchema: item.MetadataSchema, DefinitionDigest: item.DefinitionDigest, CriterionEvidenceHints: cloneStringMap(item.CriterionEvidenceHints)}
	}
	work := params.Observation.draft.Work()
	items := make([]WorkItemV2Params, len(work))
	for index, item := range work {
		criteria := make([]CriterionV2Params, len(item.Criteria()))
		for criterionIndex, criterion := range item.Criteria() {
			criteria[criterionIndex] = CriterionV2Params{ID: criterion.ID, Text: criterion.Text, VerifierKind: criterion.Verifier, Required: criterion.Requirement == CriterionRequired, Outcome: criterion.Outcome, EvidenceHint: evidenceByWork[item.WorkID()].CriterionEvidenceHints[criterion.ID]}
		}
		scopes := make([]NormalizedWriteScope, len(item.WriteScopes()))
		for scopeIndex, scope := range item.WriteScopes() {
			scopes[scopeIndex] = NormalizedWriteScope(scope)
		}
		items[index] = WorkItemV2Params{WorkID: item.WorkID(), Title: item.Title(), Description: item.Description(), DesignContext: item.DesignContext(), Criteria: criteria, Dependencies: item.Dependencies(), WriteScopes: scopes}
	}
	return NewRevisionV3(RevisionV3Params{ProjectID: params.Observation.draft.ProjectID(), MissionID: params.Observation.draft.MissionID(), Generation: 1, PlanningSourceCommit: params.PlanningSourceCommit,
		MissionDefinition: params.MissionDefinition, DependencyPolicy: params.DependencyPolicy, WorkItems: items, Lanes: lanes,
		ArtifactObservations: []ArtifactObservationV3{{Kind: "plan", ArtifactRevisionID: params.Observation.draft.PlanRevisionID(), ContentDigest: params.Observation.draft.PlanDigest()}}, ProviderEvidence: providerEvidence,
		FinalizedBy: params.FinalizedBy, FinalizedAt: params.FinalizedAt, GovernanceContextID: params.GovernanceContextID})
}

func BuildGenerationOneCandidate(params GenerationOneCandidateParams) (RevisionV2, error) {
	findings := ValidateDraft(params.Observation.draft, DefaultQualityPolicy())
	if len(findings) != 0 {
		return RevisionV2{}, invalidV2("work", fmt.Sprintf("work observation has %d validation finding(s)", len(findings)), nil)
	}
	lanes, err := CollapseLanes(params.Observation.draft)
	if err != nil {
		return RevisionV2{}, err
	}
	evidenceByWork := make(map[ids.WorkID]ObservationEvidence, len(params.Observation.evidence))
	observations := make([]ProviderObservationV2, len(params.Observation.evidence))
	for i, item := range params.Observation.evidence {
		evidenceByWork[item.WorkID] = item
		observations[i] = ProviderObservationV2{WorkID: item.WorkID, Provider: item.Provider, ExternalID: item.ExternalID, Revision: item.Revision}
	}
	work := params.Observation.draft.Work()
	items := make([]WorkItemV2Params, len(work))
	for i, item := range work {
		criteria := make([]CriterionV2Params, len(item.Criteria()))
		for j, criterion := range item.Criteria() {
			criteria[j] = CriterionV2Params{ID: criterion.ID, Text: criterion.Text, VerifierKind: criterion.Verifier, Required: criterion.Requirement == CriterionRequired, Outcome: criterion.Outcome, EvidenceHint: evidenceByWork[item.WorkID()].CriterionEvidenceHints[criterion.ID]}
		}
		scopes := make([]NormalizedWriteScope, len(item.WriteScopes()))
		for j, scope := range item.WriteScopes() {
			scopes[j] = NormalizedWriteScope(scope)
		}
		items[i] = WorkItemV2Params{WorkID: item.WorkID(), Title: item.Title(), Description: item.Description(), DesignContext: item.DesignContext(), Criteria: criteria, Dependencies: item.Dependencies(), WriteScopes: scopes}
	}
	return NewRevisionV2(RevisionV2Params{ID: params.ID, ProjectID: params.Observation.draft.ProjectID(), MissionID: params.Observation.draft.MissionID(), Generation: 1, PlanningSourceCommit: params.PlanningSourceCommit, MissionDefinition: params.MissionDefinition, DependencyPolicy: params.DependencyPolicy, WorkItems: items, Lanes: lanes, ProviderObservations: observations, FinalizedBy: params.FinalizedBy, FinalizedAt: params.FinalizedAt, GovernanceContextID: params.GovernanceContextID})
}

func canonicalFinalizationObservation(draft DraftObservation, evidence []ObservationEvidence) ([]byte, error) {
	type criterion struct {
		ID          string               `json:"id"`
		Text        string               `json:"text"`
		Outcome     string               `json:"outcome"`
		Verifier    string               `json:"verifier"`
		Requirement CriterionRequirement `json:"requirement"`
	}
	type scope struct {
		Path             string      `json:"path"`
		Domain           string      `json:"domain"`
		Access           ScopeAccess `json:"access"`
		SerializationKey string      `json:"serialization_key,omitempty"`
	}
	type reference struct {
		ID   string    `json:"id"`
		Kind ScopeKind `json:"kind"`
	}
	type workItem struct {
		WorkID            ids.WorkID   `json:"work_id"`
		Title             string       `json:"title"`
		Description       string       `json:"description"`
		DesignContext     string       `json:"design_context"`
		CrossDomainReason string       `json:"cross_domain_reason,omitempty"`
		Criteria          []criterion  `json:"criteria"`
		Dependencies      []ids.WorkID `json:"dependencies"`
		WriteScopes       []scope      `json:"write_scopes"`
		Provenance        []reference  `json:"provenance"`
	}
	work := draft.Work()
	canonicalWork := make([]workItem, len(work))
	for i, item := range work {
		criteria := make([]criterion, len(item.Criteria()))
		for j, c := range item.Criteria() {
			criteria[j] = criterion(c)
		}
		sort.Slice(criteria, func(a, b int) bool { return criteria[a].ID < criteria[b].ID })
		scopes := make([]scope, len(item.WriteScopes()))
		for j, s := range item.WriteScopes() {
			scopes[j] = scope(s)
		}
		sort.Slice(scopes, func(a, b int) bool {
			left, _ := json.Marshal(scopes[a])
			right, _ := json.Marshal(scopes[b])
			return string(left) < string(right)
		})
		provenance := make([]reference, len(item.Provenance()))
		for j, r := range item.Provenance() {
			provenance[j] = reference(r)
		}
		sort.Slice(provenance, func(a, b int) bool {
			if provenance[a].ID == provenance[b].ID {
				return provenance[a].Kind < provenance[b].Kind
			}
			return provenance[a].ID < provenance[b].ID
		})
		dependencies := item.Dependencies()
		sort.Slice(dependencies, func(a, b int) bool { return dependencies[a] < dependencies[b] })
		canonicalWork[i] = workItem{item.WorkID(), item.Title(), item.Description(), item.DesignContext(), item.CrossDomainReason(), criteria, dependencies, scopes, provenance}
	}
	sort.Slice(canonicalWork, func(i, j int) bool { return canonicalWork[i].WorkID < canonicalWork[j].WorkID })
	type evidenceWire struct {
		WorkID                 ids.WorkID        `json:"work_id"`
		Provider               string            `json:"provider"`
		ExternalID             string            `json:"external_id"`
		Revision               string            `json:"revision"`
		MetadataSchema         uint16            `json:"metadata_schema"`
		DefinitionDigest       string            `json:"definition_digest"`
		CriterionEvidenceHints map[string]string `json:"criterion_evidence_hints"`
	}
	evidenceWireValues := make([]evidenceWire, len(evidence))
	for i, item := range evidence {
		evidenceWireValues[i] = evidenceWire{item.WorkID, item.Provider, item.ExternalID, item.Revision, item.MetadataSchema, item.DefinitionDigest, cloneStringMap(item.CriterionEvidenceHints)}
	}
	approved := draft.ApprovedScope()
	sort.Slice(approved, func(i, j int) bool {
		if approved[i].ID == approved[j].ID {
			return approved[i].Kind < approved[j].Kind
		}
		return approved[i].ID < approved[j].ID
	})
	return json.Marshal(struct {
		ProjectID      ids.ProjectID   `json:"project_id"`
		MissionID      ids.MissionID   `json:"mission_id"`
		PlanRevisionID string          `json:"plan_revision_id"`
		PlanDigest     string          `json:"plan_digest"`
		Approved       []ApprovedScope `json:"approved_scope"`
		Work           []workItem      `json:"work"`
		Evidence       []evidenceWire  `json:"evidence"`
	}{draft.ProjectID(), draft.MissionID(), draft.PlanRevisionID(), draft.PlanDigest(), approved, canonicalWork, evidenceWireValues})
}

func FinalizationRequestDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("spec-kitty/finalization-request/v1\x00"), encoded...))
	return hex.EncodeToString(sum[:]), nil
}
func cloneStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
