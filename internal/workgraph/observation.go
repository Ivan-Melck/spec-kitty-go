package workgraph

import (
	"fmt"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type ApprovedScope struct {
	ID        string
	Kind      tracker.ScopeKind
	MissionID ids.MissionID
}

func (scope ApprovedScope) Validate() error {
	if scope.ID == "" || scope.Kind == "" {
		return fmt.Errorf("workgraph approved scope: identity and kind are required")
	}
	if _, err := ids.ParseMissionID(scope.MissionID.String()); err != nil {
		return fmt.Errorf("workgraph approved scope: %w", err)
	}
	for _, allowed := range []tracker.ScopeKind{tracker.ScopePlanConcern, tracker.ScopeRequiredOutcome, tracker.ScopeOptionalOutcome} {
		if scope.Kind == allowed {
			return nil
		}
	}
	return fmt.Errorf("workgraph approved scope: unsupported kind %q", scope.Kind)
}

type DraftObservation struct {
	projectID      ids.ProjectID
	missionID      ids.MissionID
	planRevisionID string
	planDigest     string
	approvedScope  []ApprovedScope
	work           []tracker.WorkDefinitionObservation
}

func NewDraftObservation(projectID ids.ProjectID, missionID ids.MissionID, planRevisionID, planDigest string, approved []ApprovedScope, work []tracker.WorkDefinitionObservation) (DraftObservation, error) {
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
		key := scope.ID
		if _, duplicate := scopeKeys[key]; duplicate {
			return DraftObservation{}, fmt.Errorf("workgraph observation: approved scope occurs more than once")
		}
		scopeKeys[key] = struct{}{}
	}
	workCopy := append([]tracker.WorkDefinitionObservation(nil), work...)
	workIDs := make(map[ids.WorkID]struct{}, len(workCopy))
	for _, item := range workCopy {
		definition := item.Definition()
		if definition.ProjectID() != projectID || item.ProviderObservation().Revision == 0 {
			return DraftObservation{}, fmt.Errorf("workgraph observation: work is outside the project or incomplete")
		}
		if _, duplicate := workIDs[definition.WorkID()]; duplicate {
			return DraftObservation{}, fmt.Errorf("workgraph observation: WorkID occurs more than once")
		}
		workIDs[definition.WorkID()] = struct{}{}
	}
	sort.Slice(approvedCopy, func(i, j int) bool {
		if approvedCopy[i].ID == approvedCopy[j].ID {
			return approvedCopy[i].Kind < approvedCopy[j].Kind
		}
		return approvedCopy[i].ID < approvedCopy[j].ID
	})
	sort.Slice(workCopy, func(i, j int) bool { return workCopy[i].Definition().WorkID() < workCopy[j].Definition().WorkID() })
	return DraftObservation{projectID: projectID, missionID: missionID, planRevisionID: planRevisionID, planDigest: planDigest, approvedScope: approvedCopy, work: workCopy}, nil
}

func (observation DraftObservation) ProjectID() ids.ProjectID { return observation.projectID }
func (observation DraftObservation) MissionID() ids.MissionID { return observation.missionID }
func (observation DraftObservation) PlanRevisionID() string   { return observation.planRevisionID }
func (observation DraftObservation) PlanDigest() string       { return observation.planDigest }
func (observation DraftObservation) ApprovedScope() []ApprovedScope {
	return append([]ApprovedScope(nil), observation.approvedScope...)
}
func (observation DraftObservation) Work() []tracker.WorkDefinitionObservation {
	return append([]tracker.WorkDefinitionObservation(nil), observation.work...)
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
