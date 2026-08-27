package mission

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type DecompositionErrorKind string
type DecompositionRecovery string
type DecompositionStatus string

const (
	DecompositionInvalidRequest     DecompositionErrorKind = "invalid_request"
	DecompositionMissionChanged     DecompositionErrorKind = "mission_changed"
	DecompositionPlanIneligible     DecompositionErrorKind = "plan_ineligible"
	DecompositionInvalidWork        DecompositionErrorKind = "invalid_work"
	DecompositionProviderCapability DecompositionErrorKind = "provider_capability"
	DecompositionProviderFailure    DecompositionErrorKind = "provider_failure"
	DecompositionOperationConflict  DecompositionErrorKind = "operation_conflict"

	DecompositionCorrectInput         DecompositionRecovery = "correct_input"
	DecompositionRefreshAuthority     DecompositionRecovery = "refresh_plan_authority"
	DecompositionReviseWork           DecompositionRecovery = "revise_work"
	DecompositionRepairProvider       DecompositionRecovery = "repair_provider"
	DecompositionObserveThenReconcile DecompositionRecovery = "observe_then_reconcile"
	DecompositionRetrySameOperation   DecompositionRecovery = "retry_same_operation"

	DecompositionComplete  DecompositionStatus = "complete"
	DecompositionPartial   DecompositionStatus = "partial"
	DecompositionAmbiguous DecompositionStatus = "ambiguous"
)

type DecompositionError struct {
	Kind     DecompositionErrorKind
	Recovery DecompositionRecovery
	Field    string
	Message  string
	Cause    error
}

func (e *DecompositionError) Error() string {
	return fmt.Sprintf("decomposition %s: %s", e.Kind, e.Message)
}
func (e *DecompositionError) Unwrap() error { return e.Cause }

type WorkProposal struct {
	Definition          tracker.WorkDefinition
	Existing            *tracker.ExternalWorkRef
	ExpectedObservation tracker.Revision
}

type DecompositionCommand struct {
	MissionID           ids.MissionID
	ProjectID           ids.ProjectID
	ExpectedMission     MissionSnapshot
	PlanRevisionID      string
	PlanContentDigest   string
	ApprovedScope       []workgraph.ApprovedScope
	Work                []WorkProposal
	ActorID             ids.ActorID
	OperationID         ids.OperationID
	GovernanceContextID ids.GovernanceContextID
	OccurredAt          time.Time
}

type DecompositionResult struct {
	Status        DecompositionStatus
	Recovery      DecompositionRecovery
	RequestDigest string
	Draft         workgraph.DraftObservation
	Observations  []tracker.WorkDefinitionObservation
	Findings      []workgraph.Finding
	FailedWorkID  ids.WorkID
	Replayed      bool
}

type MissionSnapshotSource interface {
	LoadMission(context.Context, ids.MissionID) (MissionSnapshot, error)
}
type PlanApprovalSource interface {
	ObserveCurrentApproval(context.Context, string, artifact.Kind) (artifact.CurrentApprovalObservation, error)
}

type DecompositionService struct {
	missions MissionSnapshotSource
	plans    PlanApprovalSource
	provider tracker.WorkDefinitionPort
	quality  workgraph.QualityPolicy
}

func NewDecompositionService(missions MissionSnapshotSource, plans PlanApprovalSource, provider tracker.WorkDefinitionPort) *DecompositionService {
	return &DecompositionService{missions: missions, plans: plans, provider: provider, quality: workgraph.DefaultQualityPolicy()}
}

func (service *DecompositionService) Decompose(ctx context.Context, command DecompositionCommand) (DecompositionResult, error) {
	if service == nil || service.missions == nil || service.plans == nil || service.provider == nil {
		return DecompositionResult{}, decompositionError(DecompositionInvalidRequest, DecompositionCorrectInput, "service", "decomposition dependencies are required", nil)
	}
	if err := validateDecompositionCommand(command); err != nil {
		return DecompositionResult{}, err
	}
	current, err := service.missions.LoadMission(ctx, command.MissionID)
	if err != nil {
		return DecompositionResult{}, decompositionError(DecompositionMissionChanged, DecompositionRefreshAuthority, "mission", "current mission state is unavailable", err)
	}
	if !equalDecompositionSnapshot(current, command.ExpectedMission) || current.MissionID != command.MissionID || current.ProjectID != command.ProjectID || current.CurrentStep != StepPlanApproved {
		return DecompositionResult{}, decompositionError(DecompositionMissionChanged, DecompositionRefreshAuthority, "expected_mission", "mission is not at the exact approved-plan state", nil)
	}
	planObservation, err := service.plans.ObserveCurrentApproval(ctx, command.MissionID.String(), artifact.KindPlan)
	if err != nil {
		return DecompositionResult{}, decompositionError(DecompositionPlanIneligible, DecompositionRefreshAuthority, "plan", "current approved plan cannot be observed", err)
	}
	revision := planObservation.CurrentRevision()
	if revision.ID() != command.PlanRevisionID || revision.MissionID() != command.MissionID.String() || revision.Kind() != artifact.KindPlan || revision.ContentDigest() != command.PlanContentDigest {
		return DecompositionResult{}, decompositionError(DecompositionPlanIneligible, DecompositionRefreshAuthority, "plan_revision", "command does not name the exact current plan content", nil)
	}
	freshness, err := artifact.EvaluateFreshness(revision.ID(), planObservation.Approvals())
	if err != nil || freshness.Status != artifact.FreshnessFresh {
		return DecompositionResult{}, decompositionError(DecompositionPlanIneligible, DecompositionRefreshAuthority, "plan_approval", "exact current plan revision is not approved", err)
	}

	definitions := make([]tracker.WorkDefinition, len(command.Work))
	for index := range command.Work {
		definitions[index] = command.Work[index].Definition
	}
	provisional, err := provisionalDraft(command, definitions)
	if err != nil {
		return DecompositionResult{}, decompositionError(DecompositionInvalidWork, DecompositionReviseWork, "work", "proposed work cannot form a complete observation", err)
	}
	findings := workgraph.ValidateDraft(provisional, service.quality)
	if len(findings) != 0 {
		return DecompositionResult{Findings: findings}, decompositionError(DecompositionInvalidWork, DecompositionReviseWork, "work", "proposed work has validation findings", nil)
	}

	capabilities, err := service.provider.DefinitionCapabilities(ctx)
	if err != nil || capabilities.Validate() != nil {
		return DecompositionResult{}, decompositionError(DecompositionProviderCapability, DecompositionRepairProvider, "provider_capabilities", "provider cannot safely persist complete work definitions", err)
	}
	requestDigest := decompositionRequestDigest(command)
	if err := service.provider.PrepareDefinitionOperation(ctx, command.OperationID, requestDigest); err != nil {
		kind := DecompositionProviderFailure
		recovery := DecompositionRetrySameOperation
		if tracker.IsKind(err, tracker.ErrorRevisionConflict) {
			kind, recovery = DecompositionOperationConflict, DecompositionCorrectInput
		}
		return DecompositionResult{RequestDigest: requestDigest}, decompositionError(kind, recovery, "operation_id", "provider refused decomposition operation preparation", err)
	}

	ordered := orderWorkProposals(command.Work)
	result := DecompositionResult{Status: DecompositionComplete, RequestDigest: requestDigest, Replayed: true}
	for _, proposal := range ordered {
		operationID := derivedWorkOperation(command.OperationID, proposal.Definition.WorkID())
		var mutation tracker.WorkDefinitionMutation
		if proposal.Existing == nil {
			mutation, err = service.provider.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: operationID, Definition: proposal.Definition})
		} else {
			mutation, err = service.provider.UpdateDefinition(ctx, tracker.UpdateWorkDefinitionRequest{OperationID: operationID, ExternalRef: *proposal.Existing, ExpectedObservation: proposal.ExpectedObservation, Definition: proposal.Definition})
		}
		if mutation.Observation.ProviderObservation().Revision != 0 {
			result.Observations = append(result.Observations, mutation.Observation)
		}
		result.Replayed = result.Replayed && mutation.Replayed
		if err != nil {
			result.FailedWorkID = proposal.Definition.WorkID()
			result.Status, result.Recovery = classifyProviderOutcome(err, len(result.Observations))
			return result, decompositionError(DecompositionProviderFailure, result.Recovery, "provider_mutation", "provider mutation did not produce an unambiguous complete decomposition", err)
		}
		observed, observeErr := service.provider.ObserveDefinition(ctx, mutation.Observation.ExternalRef())
		if observeErr != nil {
			result.FailedWorkID = proposal.Definition.WorkID()
			result.Status = DecompositionAmbiguous
			result.Recovery = DecompositionObserveThenReconcile
			return result, decompositionError(DecompositionProviderFailure, result.Recovery, "provider_observation", "applied work definition cannot be re-observed", observeErr)
		}
		result.Observations[len(result.Observations)-1] = observed
	}
	semanticWork, err := semanticWorkObservations(observationDefinitions(result.Observations))
	if err != nil {
		result.Status = DecompositionAmbiguous
		result.Recovery = DecompositionObserveThenReconcile
		return result, decompositionError(DecompositionProviderFailure, result.Recovery, "observation", "provider results cannot be projected into semantic work", err)
	}
	draft, err := workgraph.NewDraftObservation(command.ProjectID, command.MissionID, command.PlanRevisionID, command.PlanContentDigest, command.ApprovedScope, semanticWork)
	if err != nil {
		result.Status = DecompositionAmbiguous
		result.Recovery = DecompositionObserveThenReconcile
		return result, decompositionError(DecompositionProviderFailure, result.Recovery, "observation", "provider results cannot form one complete graph observation", err)
	}
	result.Findings = workgraph.ValidateDraft(draft, service.quality)
	if len(result.Findings) != 0 {
		result.Status = DecompositionAmbiguous
		result.Recovery = DecompositionObserveThenReconcile
		return result, decompositionError(DecompositionProviderFailure, result.Recovery, "observation", "re-observed provider graph differs from the validated proposal", nil)
	}
	result.Draft = draft
	return result, nil
}

func validateDecompositionCommand(command DecompositionCommand) error {
	if _, err := ids.ParseMissionID(command.MissionID.String()); err != nil {
		return decompositionError(DecompositionInvalidRequest, DecompositionCorrectInput, "mission_id", "mission identity is invalid", err)
	}
	if _, err := ids.ParseProjectID(command.ProjectID.String()); err != nil {
		return decompositionError(DecompositionInvalidRequest, DecompositionCorrectInput, "project_id", "project identity is invalid", err)
	}
	if command.ExpectedMission.Validate() != nil || command.ExpectedMission.MissionID != command.MissionID || command.ExpectedMission.ProjectID != command.ProjectID {
		return decompositionError(DecompositionInvalidRequest, DecompositionCorrectInput, "expected_mission", "expected mission snapshot is invalid", nil)
	}
	if command.PlanRevisionID == "" || len(command.PlanContentDigest) != 64 || len(command.ApprovedScope) == 0 || len(command.Work) == 0 {
		return decompositionError(DecompositionInvalidRequest, DecompositionCorrectInput, "plan", "exact plan evidence, approved scope, and work are required", nil)
	}
	if _, err := ids.ParseActorID(command.ActorID.String()); err != nil {
		return decompositionError(DecompositionInvalidRequest, DecompositionCorrectInput, "actor_id", "actor identity is invalid", err)
	}
	if _, err := ids.ParseOperationID(command.OperationID.String()); err != nil {
		return decompositionError(DecompositionInvalidRequest, DecompositionCorrectInput, "operation_id", "operation identity is invalid", err)
	}
	if _, err := ids.ParseGovernanceContextID(command.GovernanceContextID.String()); err != nil {
		return decompositionError(DecompositionInvalidRequest, DecompositionCorrectInput, "governance_context_id", "governance context is invalid", err)
	}
	if command.OccurredAt.IsZero() || command.OccurredAt.Location() != time.UTC {
		return decompositionError(DecompositionInvalidRequest, DecompositionCorrectInput, "occurred_at", "occurrence time must be non-zero UTC", nil)
	}
	seen := map[ids.WorkID]bool{}
	for _, proposal := range command.Work {
		definition := proposal.Definition
		if definition.ProjectID() != command.ProjectID || definition.WorkID() == "" || seen[definition.WorkID()] {
			return decompositionError(DecompositionInvalidRequest, DecompositionCorrectInput, "work", "work identities must be unique and scoped to the project", nil)
		}
		seen[definition.WorkID()] = true
		if proposal.Existing != nil && (proposal.Existing.Validate() != nil || proposal.ExpectedObservation == 0) {
			return decompositionError(DecompositionInvalidRequest, DecompositionCorrectInput, "existing_work", "updates require a valid mapping and expected observation", nil)
		}
	}
	return nil
}

func provisionalDraft(command DecompositionCommand, definitions []tracker.WorkDefinition) (workgraph.DraftObservation, error) {
	observations, err := semanticWorkObservations(definitions)
	if err != nil {
		return workgraph.DraftObservation{}, err
	}
	return workgraph.NewDraftObservation(command.ProjectID, command.MissionID, command.PlanRevisionID, command.PlanContentDigest, command.ApprovedScope, observations)
}

func observationDefinitions(observations []tracker.WorkDefinitionObservation) []tracker.WorkDefinition {
	definitions := make([]tracker.WorkDefinition, len(observations))
	for index := range observations {
		definitions[index] = observations[index].Definition()
	}
	return definitions
}

func semanticWorkObservations(definitions []tracker.WorkDefinition) ([]workgraph.WorkObservation, error) {
	result := make([]workgraph.WorkObservation, 0, len(definitions))
	for _, definition := range definitions {
		criteria := make([]workgraph.CriterionObservation, len(definition.Criteria()))
		for index, criterion := range definition.Criteria() {
			criteria[index] = workgraph.CriterionObservation{ID: criterion.ID, Text: criterion.Text, Outcome: criterion.Outcome, Verifier: criterion.Verifier.String(), Required: criterion.Required}
		}
		scopes := make([]workgraph.WriteScopeObservation, len(definition.WriteScopes()))
		for index, scope := range definition.WriteScopes() {
			scopes[index] = workgraph.WriteScopeObservation{Path: scope.Path, Domain: scope.Domain, Access: workgraph.ScopeAccess(scope.Access), SerializationKey: scope.SerializationKey}
		}
		provenance := make([]workgraph.ScopeReference, len(definition.Provenance()))
		for index, reference := range definition.Provenance() {
			provenance[index] = workgraph.ScopeReference{ID: reference.ID, Kind: workgraph.ScopeKind(reference.Kind)}
		}
		observed, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: definition.ProjectID(), WorkID: definition.WorkID(), Title: definition.Title(), Description: definition.Description(), DesignContext: definition.DesignContext(), CrossDomainReason: definition.CrossDomainReason(), Criteria: criteria, Dependencies: definition.Dependencies(), WriteScopes: scopes, Provenance: provenance})
		if err != nil {
			return nil, err
		}
		result = append(result, observed)
	}
	return result, nil
}
func decompositionRequestDigest(command DecompositionCommand) string {
	type semanticProposal struct {
		Definition json.RawMessage          `json:"definition"`
		Existing   *tracker.ExternalWorkRef `json:"existing,omitempty"`
		Expected   tracker.Revision         `json:"expected,omitempty"`
	}
	proposals := make([]semanticProposal, len(command.Work))
	for i, p := range command.Work {
		proposals[i] = semanticProposal{p.Definition.CanonicalData(), p.Existing, p.ExpectedObservation}
	}
	sort.Slice(proposals, func(i, j int) bool { return string(proposals[i].Definition) < string(proposals[j].Definition) })
	scope := append([]workgraph.ApprovedScope(nil), command.ApprovedScope...)
	sort.Slice(scope, func(i, j int) bool { return scope[i].ID < scope[j].ID })
	encoded, _ := json.Marshal(struct {
		MissionID            ids.MissionID             `json:"mission_id"`
		ProjectID            ids.ProjectID             `json:"project_id"`
		DefinitionID         ids.MissionDefinitionID   `json:"definition_id"`
		DefinitionVersion    DefinitionVersion         `json:"definition_version"`
		DefinitionCommitment DefinitionCommitment      `json:"definition_commitment"`
		CurrentStep          StepKey                   `json:"current_step"`
		StateVersion         uint64                    `json:"state_version"`
		PlanRevisionID       string                    `json:"plan_revision_id"`
		PlanDigest           string                    `json:"plan_digest"`
		ActorID              ids.ActorID               `json:"actor_id"`
		GovernanceContextID  ids.GovernanceContextID   `json:"governance_context_id"`
		Scope                []workgraph.ApprovedScope `json:"scope"`
		Work                 []semanticProposal        `json:"work"`
	}{
		MissionID: command.MissionID, ProjectID: command.ProjectID,
		DefinitionID: command.ExpectedMission.DefinitionID, DefinitionVersion: command.ExpectedMission.DefinitionVersion,
		DefinitionCommitment: command.ExpectedMission.DefinitionCommitment, CurrentStep: command.ExpectedMission.CurrentStep,
		StateVersion: command.ExpectedMission.StateVersion, PlanRevisionID: command.PlanRevisionID, PlanDigest: command.PlanContentDigest,
		ActorID: command.ActorID, GovernanceContextID: command.GovernanceContextID, Scope: scope, Work: proposals,
	})
	digest := sha256.Sum256(append([]byte("spec-kitty/decomposition-request/v1\x00"), encoded...))
	return fmt.Sprintf("%x", digest)
}
func derivedWorkOperation(parent ids.OperationID, workID ids.WorkID) ids.OperationID {
	digest := sha256.Sum256([]byte(parent.String() + "\x00" + workID.String()))
	return ids.OperationID(fmt.Sprintf("decompose-%x", digest[:16]))
}
func orderWorkProposals(proposals []WorkProposal) []WorkProposal {
	byID := make(map[ids.WorkID]WorkProposal, len(proposals))
	indegree := make(map[ids.WorkID]int, len(proposals))
	dependents := make(map[ids.WorkID][]ids.WorkID)
	for _, proposal := range proposals {
		workID := proposal.Definition.WorkID()
		byID[workID] = proposal
		indegree[workID] = len(proposal.Definition.Dependencies())
		for _, dependency := range proposal.Definition.Dependencies() {
			dependents[dependency] = append(dependents[dependency], workID)
		}
	}
	ready := make([]ids.WorkID, 0)
	for workID, count := range indegree {
		if count == 0 {
			ready = append(ready, workID)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	ordered := make([]WorkProposal, 0, len(proposals))
	for len(ready) > 0 {
		workID := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byID[workID])
		for _, dependent := range dependents[workID] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
				sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
			}
		}
	}
	return ordered
}
func classifyProviderOutcome(err error, observed int) (DecompositionStatus, DecompositionRecovery) {
	if tracker.IsKind(err, tracker.ErrorAmbiguous) || tracker.IsKind(err, tracker.ErrorTimeout) {
		return DecompositionAmbiguous, DecompositionObserveThenReconcile
	}
	if observed > 0 {
		return DecompositionPartial, DecompositionObserveThenReconcile
	}
	if tracker.IsKind(err, tracker.ErrorUnavailable) {
		return DecompositionPartial, DecompositionRetrySameOperation
	}
	return DecompositionPartial, DecompositionObserveThenReconcile
}
func decompositionError(kind DecompositionErrorKind, recovery DecompositionRecovery, field, message string, cause error) *DecompositionError {
	return &DecompositionError{Kind: kind, Recovery: recovery, Field: field, Message: message, Cause: cause}
}
func equalDecompositionSnapshot(left, right MissionSnapshot) bool {
	return left.MissionID == right.MissionID && left.ProjectID == right.ProjectID && left.DefinitionID == right.DefinitionID && left.DefinitionVersion == right.DefinitionVersion && left.DefinitionCommitment == right.DefinitionCommitment && left.Lifecycle == right.Lifecycle && left.CurrentStep == right.CurrentStep && left.CurrentWorkRevisionID == right.CurrentWorkRevisionID && left.StateVersion == right.StateVersion && left.CreatedAt.Equal(right.CreatedAt) && left.UpdatedAt.Equal(right.UpdatedAt) && left.TerminalOutcome == right.TerminalOutcome && equalBlockers(left.ActiveBlockers, right.ActiveBlockers) && equalDecisions(left.UnresolvedDecisions, right.UnresolvedDecisions)
}
func equalBlockers(left, right []ids.BlockerID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
func equalDecisions(left, right []ids.DecisionID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
