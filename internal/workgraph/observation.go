package workgraph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type ScopeKind string

const (
	ScopePlanConcern     ScopeKind = "plan_concern"
	ScopeRequiredOutcome ScopeKind = "required_outcome"
	ScopeOptionalOutcome ScopeKind = "optional_outcome"
)

type ApprovedScope struct {
	ID        string
	Kind      ScopeKind
	MissionID ids.MissionID
}

func (scope ApprovedScope) Validate() error {
	if !stableToken(scope.ID) || scope.Kind == "" {
		return fmt.Errorf("workgraph approved scope: identity and kind are required")
	}
	if _, err := ids.ParseMissionID(scope.MissionID.String()); err != nil {
		return fmt.Errorf("workgraph approved scope: %w", err)
	}
	for _, allowed := range []ScopeKind{ScopePlanConcern, ScopeRequiredOutcome, ScopeOptionalOutcome} {
		if scope.Kind == allowed {
			return nil
		}
	}
	return fmt.Errorf("workgraph approved scope: unsupported kind %q", scope.Kind)
}

type CriterionObservation struct {
	ID          string
	Text        string
	Outcome     string
	Verifier    string
	Requirement CriterionRequirement
}

type CriterionRequirement string

const (
	CriterionRequired CriterionRequirement = "required"
	CriterionOptional CriterionRequirement = "optional"
)

type ScopeAccess string

const (
	ScopeReadOnly       ScopeAccess = "read_only"
	ScopeWriteExclusive ScopeAccess = "write_exclusive"
)

type WriteScopeObservation struct {
	Path             string
	Domain           string
	Access           ScopeAccess
	SerializationKey string
}

type ScopeReference struct {
	ID   string
	Kind ScopeKind
}

type WorkObservationParams struct {
	ProjectID         ids.ProjectID
	WorkID            ids.WorkID
	Title             string
	Description       string
	DesignContext     string
	CrossDomainReason string
	Criteria          []CriterionObservation
	Dependencies      []ids.WorkID
	WriteScopes       []WriteScopeObservation
	Provenance        []ScopeReference
}

// WorkObservation is the provider-free semantic projection consumed by graph
// validation. Provider mapping/revision evidence remains at the mission seam.
type WorkObservation struct {
	projectID         ids.ProjectID
	workID            ids.WorkID
	title             string
	description       string
	designContext     string
	crossDomainReason string
	criteria          []CriterionObservation
	dependencies      []ids.WorkID
	writeScopes       []WriteScopeObservation
	provenance        []ScopeReference
}

func NewWorkObservation(params WorkObservationParams) (WorkObservation, error) {
	if _, err := ids.ParseProjectID(params.ProjectID.String()); err != nil {
		return WorkObservation{}, err
	}
	if _, err := ids.ParseWorkID(params.WorkID.String()); err != nil {
		return WorkObservation{}, err
	}
	// Provider observations deliberately admit malformed semantic content. The
	// closed finding surface in ValidateDraft, rather than a generic constructor
	// error, is the authority that explains why work cannot be finalized.
	return WorkObservation{
		projectID: params.ProjectID, workID: params.WorkID, title: params.Title,
		description: params.Description, designContext: params.DesignContext,
		crossDomainReason: params.CrossDomainReason,
		criteria:          append([]CriterionObservation(nil), params.Criteria...),
		dependencies:      append([]ids.WorkID(nil), params.Dependencies...),
		writeScopes:       append([]WriteScopeObservation(nil), params.WriteScopes...),
		provenance:        append([]ScopeReference(nil), params.Provenance...),
	}, nil
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

func (work WorkObservation) ProjectID() ids.ProjectID  { return work.projectID }
func (work WorkObservation) WorkID() ids.WorkID        { return work.workID }
func (work WorkObservation) Title() string             { return work.title }
func (work WorkObservation) Description() string       { return work.description }
func (work WorkObservation) DesignContext() string     { return work.designContext }
func (work WorkObservation) CrossDomainReason() string { return work.crossDomainReason }
func (work WorkObservation) Criteria() []CriterionObservation {
	return append([]CriterionObservation(nil), work.criteria...)
}
func (work WorkObservation) Dependencies() []ids.WorkID {
	return append([]ids.WorkID(nil), work.dependencies...)
}
func (work WorkObservation) WriteScopes() []WriteScopeObservation {
	return append([]WriteScopeObservation(nil), work.writeScopes...)
}
func (work WorkObservation) Provenance() []ScopeReference {
	return append([]ScopeReference(nil), work.provenance...)
}

type DraftObservation struct {
	projectID      ids.ProjectID
	missionID      ids.MissionID
	planRevisionID string
	planDigest     string
	approvedScope  []ApprovedScope
	work           []WorkObservation
}

func NewDraftObservation(projectID ids.ProjectID, missionID ids.MissionID, planRevisionID, planDigest string, approved []ApprovedScope, work []WorkObservation) (DraftObservation, error) {
	if _, err := ids.ParseProjectID(projectID.String()); err != nil {
		return DraftObservation{}, err
	}
	if _, err := ids.ParseMissionID(missionID.String()); err != nil {
		return DraftObservation{}, err
	}
	if planRevisionID == "" || len(planDigest) != 64 || len(approved) == 0 || len(work) == 0 {
		return DraftObservation{}, fmt.Errorf("workgraph observation: exact plan evidence, approved scope, and work are required")
	}
	scopeKeys := make(map[string]struct{}, len(approved))
	approvedCopy := append([]ApprovedScope(nil), approved...)
	for _, scope := range approvedCopy {
		if err := scope.Validate(); err != nil || scope.MissionID != missionID {
			return DraftObservation{}, fmt.Errorf("workgraph observation: approved scope is invalid or belongs to another mission")
		}
		if _, duplicate := scopeKeys[scope.ID]; duplicate {
			return DraftObservation{}, fmt.Errorf("workgraph observation: approved scope occurs more than once")
		}
		scopeKeys[scope.ID] = struct{}{}
	}
	workCopy := append([]WorkObservation(nil), work...)
	workIDs := make(map[ids.WorkID]struct{}, len(workCopy))
	for _, item := range workCopy {
		if item.ProjectID() != projectID {
			return DraftObservation{}, fmt.Errorf("workgraph observation: work is outside the project")
		}
		if _, duplicate := workIDs[item.WorkID()]; duplicate {
			return DraftObservation{}, fmt.Errorf("workgraph observation: WorkID occurs more than once")
		}
		workIDs[item.WorkID()] = struct{}{}
	}
	sort.Slice(approvedCopy, func(i, j int) bool {
		if approvedCopy[i].ID == approvedCopy[j].ID {
			return approvedCopy[i].Kind < approvedCopy[j].Kind
		}
		return approvedCopy[i].ID < approvedCopy[j].ID
	})
	sort.Slice(workCopy, func(i, j int) bool { return workCopy[i].WorkID() < workCopy[j].WorkID() })
	return DraftObservation{projectID: projectID, missionID: missionID, planRevisionID: planRevisionID, planDigest: planDigest, approvedScope: approvedCopy, work: workCopy}, nil
}

func (observation DraftObservation) ProjectID() ids.ProjectID { return observation.projectID }
func (observation DraftObservation) MissionID() ids.MissionID { return observation.missionID }
func (observation DraftObservation) PlanRevisionID() string   { return observation.planRevisionID }
func (observation DraftObservation) PlanDigest() string       { return observation.planDigest }
func (observation DraftObservation) ApprovedScope() []ApprovedScope {
	return append([]ApprovedScope(nil), observation.approvedScope...)
}
func (observation DraftObservation) Work() []WorkObservation {
	return append([]WorkObservation(nil), observation.work...)
}

type ProjectedLane struct {
	ID      string
	Members []ids.WorkID
}
type ProjectedLaneEdge struct {
	BlockingLaneID string
	BlockedLaneID  string
}
type LaneProjection struct {
	Lanes []ProjectedLane
	Edges []ProjectedLaneEdge
}
