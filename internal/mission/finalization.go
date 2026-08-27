package mission

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type FinalizationErrorKind string
type FinalizationRecovery string

const (
	FinalizationInvalidRequest     FinalizationErrorKind = "invalid_request"
	FinalizationProviderFailure    FinalizationErrorKind = "provider_failure"
	FinalizationProviderChanged    FinalizationErrorKind = "provider_changed_during_observation"
	FinalizationOperationConflict  FinalizationErrorKind = "operation_conflict"
	FinalizationCurrentConflict    FinalizationErrorKind = "current_generation_conflict"
	FinalizationPersistenceFailure FinalizationErrorKind = "persistence_failure"
	FinalizationContractFailure    FinalizationErrorKind = "persistence_contract"

	FinalizationCorrectInput       FinalizationRecovery = "correct_input"
	FinalizationRetrySameOperation FinalizationRecovery = "retry_same_operation"
	FinalizationObserveAgain       FinalizationRecovery = "observe_again"
	FinalizationReconcileCurrent   FinalizationRecovery = "reconcile_current_generation"
	FinalizationRepairPersistence  FinalizationRecovery = "repair_persistence"
)

type FinalizationError struct {
	Kind     FinalizationErrorKind
	Recovery FinalizationRecovery
	Field    string
	Message  string
	Cause    error
}

func (e *FinalizationError) Error() string {
	return fmt.Sprintf("work finalization %s: %s", e.Kind, e.Message)
}
func (e *FinalizationError) Unwrap() error { return e.Cause }

type FinalizationCommand struct {
	MissionID                 ids.MissionID
	ProjectID                 ids.ProjectID
	ExpectedMission           MissionSnapshot
	ExpectedCurrentGeneration uint64
	ProposedRevisionID        ids.WorkRevisionID
	RequestedGeneration       uint64
	PredecessorID             ids.WorkRevisionID
	PlanningSourceCommit      string
	ActorID                   ids.ActorID
	OperationID               ids.OperationID
	GovernanceContextID       ids.GovernanceContextID
	OccurredAt                time.Time
}

type FinalizationResult struct {
	Revision      workgraph.RevisionV2
	Mission       MissionSnapshot
	RequestDigest string
	Replayed      bool
}

// FinalizationObservationSource is a provider-neutral read seam. An adapter
// may consult a tracker, but the mission service receives only normalized WP04
// work plus bounded mapping/revision evidence.
type FinalizationObservationSource interface {
	ObserveFinalization(context.Context, ids.MissionID) (workgraph.FinalizationObservation, error)
}

type FinalizationCommit struct {
	ExpectedMission           MissionSnapshot
	ExpectedCurrentGeneration uint64
	OperationID               ids.OperationID
	RequestDigest             string
	Candidate                 workgraph.RevisionV2
	ActorID                   ids.ActorID
	GovernanceContextID       ids.GovernanceContextID
	OccurredAt                time.Time
}

// FinalizationStore must bind replay and expected-current CAS to the mission
// state transition in one local atomic boundary. WP08 supplies durability.
type FinalizationStore interface {
	ReplayFinalization(context.Context, ids.MissionID, ids.OperationID, string) (FinalizationResult, bool, error)
	PublishGenerationOne(context.Context, FinalizationCommit) (FinalizationResult, error)
}

type FinalizationService struct {
	observations FinalizationObservationSource
	store        FinalizationStore
}

func NewFinalizationService(observations FinalizationObservationSource, store FinalizationStore) *FinalizationService {
	return &FinalizationService{observations: observations, store: store}
}

func (service *FinalizationService) Finalize(ctx context.Context, command FinalizationCommand) (FinalizationResult, error) {
	if service == nil || service.observations == nil || service.store == nil {
		return FinalizationResult{}, finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "service", "finalization dependencies are required", nil)
	}
	if err := validateFinalizationCommand(command); err != nil {
		return FinalizationResult{}, err
	}
	requestDigest, err := finalizationCommandDigest(command)
	if err != nil {
		return FinalizationResult{}, finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "request", "finalization request cannot be committed", err)
	}
	if replay, found, replayErr := service.store.ReplayFinalization(ctx, command.MissionID, command.OperationID, requestDigest); replayErr != nil {
		return FinalizationResult{}, normalizeFinalizationStoreError(replayErr)
	} else if found {
		replay.RequestDigest = requestDigest
		replay.Replayed = true
		return replay, nil
	}
	first, err := service.observations.ObserveFinalization(ctx, command.MissionID)
	if err != nil {
		return FinalizationResult{}, finalizationError(FinalizationProviderFailure, FinalizationObserveAgain, "provider_observation", "provider work cannot be observed", nil)
	}
	candidate, err := workgraph.BuildGenerationOneCandidate(workgraph.GenerationOneCandidateParams{ID: command.ProposedRevisionID, Observation: first, PlanningSourceCommit: command.PlanningSourceCommit, MissionDefinition: workgraph.MissionDefinitionIdentity{Key: command.ExpectedMission.DefinitionID.String(), Version: uint64(command.ExpectedMission.DefinitionVersion), Digest: command.ExpectedMission.DefinitionCommitment.String()}, DependencyPolicy: workgraph.DependencyApprovedOrDone, FinalizedBy: command.ActorID, FinalizedAt: command.OccurredAt, GovernanceContextID: command.GovernanceContextID})
	if err != nil {
		return FinalizationResult{}, finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "work", "observed work cannot form a valid generation", err)
	}
	if candidate.ProjectID() != command.ProjectID || candidate.MissionID() != command.MissionID {
		return FinalizationResult{}, finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "observation_scope", "provider observation belongs to another mission", nil)
	}
	second, err := service.observations.ObserveFinalization(ctx, command.MissionID)
	if err != nil {
		return FinalizationResult{}, finalizationError(FinalizationProviderFailure, FinalizationObserveAgain, "provider_recheck", "provider work cannot be re-observed", nil)
	}
	if err := workgraph.CompareFinalizationObservations(first, second); err != nil {
		return FinalizationResult{}, finalizationError(FinalizationProviderChanged, FinalizationObserveAgain, "provider_recheck", "provider work changed during finalization", err)
	}
	result, err := service.store.PublishGenerationOne(ctx, FinalizationCommit{ExpectedMission: command.ExpectedMission.clone(), ExpectedCurrentGeneration: command.ExpectedCurrentGeneration, OperationID: command.OperationID, RequestDigest: requestDigest, Candidate: candidate, ActorID: command.ActorID, GovernanceContextID: command.GovernanceContextID, OccurredAt: command.OccurredAt})
	if err != nil {
		return FinalizationResult{}, normalizeFinalizationStoreError(err)
	}
	if err := validateFinalizationPostconditions(command, candidate, result); err != nil {
		return FinalizationResult{}, err
	}
	result.RequestDigest = requestDigest
	return result, nil
}

func validateFinalizationCommand(command FinalizationCommand) error {
	if _, err := ids.ParseMissionID(command.MissionID.String()); err != nil {
		return finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "mission_id", "mission identity is invalid", err)
	}
	if _, err := ids.ParseProjectID(command.ProjectID.String()); err != nil {
		return finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "project_id", "project identity is invalid", err)
	}
	if command.ExpectedMission.Validate() != nil || command.ExpectedMission.MissionID != command.MissionID || command.ExpectedMission.ProjectID != command.ProjectID || command.ExpectedMission.CurrentStep != StepWorkDraftReady || command.ExpectedMission.CurrentWorkRevisionID != "" {
		return finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "expected_mission", "mission is not at the exact work-draft-ready state", nil)
	}
	if command.ExpectedCurrentGeneration != 0 || command.RequestedGeneration != 1 || command.PredecessorID != "" {
		return finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "generation", "ordinary finalization may publish generation 1 into an empty slot only", nil)
	}
	if _, err := ids.ParseWorkRevisionID(command.ProposedRevisionID.String()); err != nil {
		return finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "work_revision_id", "work revision identity is invalid", err)
	}
	if _, err := ids.ParseActorID(command.ActorID.String()); err != nil {
		return finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "actor_id", "actor identity is invalid", err)
	}
	if _, err := ids.ParseOperationID(command.OperationID.String()); err != nil {
		return finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "operation_id", "operation identity is invalid", err)
	}
	if _, err := ids.ParseGovernanceContextID(command.GovernanceContextID.String()); err != nil {
		return finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "governance_context_id", "governance context identity is invalid", err)
	}
	if command.OccurredAt.IsZero() || command.OccurredAt.Location() != time.UTC {
		return finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "occurred_at", "finalization time must be non-zero UTC", nil)
	}
	if command.ExpectedMission.DefinitionID.String() != "software-development" || command.ExpectedMission.DefinitionCommitment.Validate() != nil {
		return finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "mission_definition", "mission definition commitment is unsupported", nil)
	}
	if !validPlanningSourceCommit(command.PlanningSourceCommit) {
		return finalizationError(FinalizationInvalidRequest, FinalizationCorrectInput, "planning_source_commit", "planning source must be an exact lowercase Git object identity", nil)
	}
	return nil
}

func validPlanningSourceCommit(value string) bool {
	if len(value) < 40 || len(value) > 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func finalizationCommandDigest(command FinalizationCommand) (string, error) {
	return workgraph.FinalizationRequestDigest(struct {
		MissionID                 ids.MissionID           `json:"mission_id"`
		ProjectID                 ids.ProjectID           `json:"project_id"`
		DefinitionID              ids.MissionDefinitionID `json:"definition_id"`
		DefinitionVersion         DefinitionVersion       `json:"definition_version"`
		DefinitionCommitment      DefinitionCommitment    `json:"definition_commitment"`
		Step                      StepKey                 `json:"step"`
		StateVersion              uint64                  `json:"state_version"`
		ExpectedCurrentGeneration uint64                  `json:"expected_current_generation"`
		ProposedRevisionID        ids.WorkRevisionID      `json:"proposed_revision_id"`
		RequestedGeneration       uint64                  `json:"requested_generation"`
		PredecessorID             ids.WorkRevisionID      `json:"predecessor_id"`
		PlanningSourceCommit      string                  `json:"planning_source_commit"`
		ActorID                   ids.ActorID             `json:"actor_id"`
		GovernanceContextID       ids.GovernanceContextID `json:"governance_context_id"`
		OccurredAt                string                  `json:"occurred_at"`
	}{command.MissionID, command.ProjectID, command.ExpectedMission.DefinitionID, command.ExpectedMission.DefinitionVersion, command.ExpectedMission.DefinitionCommitment, command.ExpectedMission.CurrentStep, command.ExpectedMission.StateVersion, command.ExpectedCurrentGeneration, command.ProposedRevisionID, command.RequestedGeneration, command.PredecessorID, command.PlanningSourceCommit, command.ActorID, command.GovernanceContextID, command.OccurredAt.Format(time.RFC3339Nano)})
}

func validateFinalizationPostconditions(command FinalizationCommand, candidate workgraph.RevisionV2, result FinalizationResult) error {
	candidateBytes, _ := candidate.Encode()
	resultBytes, _ := result.Revision.Encode()
	if !bytes.Equal(candidateBytes, resultBytes) || result.Mission.Validate() != nil || result.Mission.MissionID != command.MissionID || result.Mission.ProjectID != command.ProjectID || result.Mission.CurrentWorkRevisionID != candidate.ID() || result.Mission.CurrentStep != StepWorkFinalized || result.Mission.StateVersion != command.ExpectedMission.StateVersion+1 {
		return finalizationError(FinalizationContractFailure, FinalizationRepairPersistence, "store.publish", "finalization store violated publication postconditions", nil)
	}
	return nil
}
func normalizeFinalizationStoreError(err error) error {
	if typed, ok := err.(*FinalizationError); ok {
		return typed
	}
	return finalizationError(FinalizationPersistenceFailure, FinalizationRetrySameOperation, "store", "finalization persistence outcome is unavailable", nil)
}
func finalizationError(kind FinalizationErrorKind, recovery FinalizationRecovery, field, message string, cause error) *FinalizationError {
	if !boundedDiagnosticText(message, 160) {
		message = "work finalization refused"
	}
	return &FinalizationError{Kind: kind, Recovery: recovery, Field: field, Message: message, Cause: cause}
}
