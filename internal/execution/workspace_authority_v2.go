package execution

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

const workspaceAuthorityResultSchemaV2 = uint32(1)

type WorkspaceAuthorityObservationV2 struct {
	State             *workspace.StateV2
	RecordRevision    uint64
	LastPublicationID journalv2.PublicationID
}

type WorkspaceAuthorityPortV2 interface {
	ResolveExecutionOperation(context.Context, journalv2.OperationBindingV2) (ExecutionOperationResolutionV2, error)
	ObserveExecutionSlot(context.Context, journalv2.AggregateCoordinate) (ExecutionSlotObservationV2, error)
	ObserveWorkspace(context.Context, journalv2.AggregateCoordinate) (WorkspaceAuthorityObservationV2, error)
	PublishWorkspace(context.Context, WorkspaceAuthorityPlanV2) (WorkspaceAuthorityAckV2, error)
}

type WorkspaceAuthorityPlanV2 struct{ sealed journalv2.SealedCommitPlanV2 }

func (p WorkspaceAuthorityPlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return journalv2.SealedCommitPlanV2{}, executionV2Error(KindInvalidCommand, "workspace_plan", nil)
	}
	return p.sealed, nil
}

type WorkspaceAuthorityAckV2 struct {
	publicationID    journalv2.PublicationID
	planDigest       journalv2.Digest
	mutationCount    int
	providerRevision uint64
}

func NewWorkspaceAuthorityAckV2(plan WorkspaceAuthorityPlanV2, ack journalv2.AcknowledgedPublicationV2) (WorkspaceAuthorityAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil || ack.PublicationID() != sealed.PublicationID() || ack.PlanDigest() != sealed.Digest() || ack.MutationCount() != sealed.MutationCount() || ack.ProviderRevision() == 0 || ack.DurableThroughRevision() < ack.ProviderRevision() {
		return WorkspaceAuthorityAckV2{}, executionV2Error(KindAuthorityConflict, "workspace_ack", err)
	}
	return WorkspaceAuthorityAckV2{ack.PublicationID(), ack.PlanDigest(), ack.MutationCount(), ack.ProviderRevision()}, nil
}

func (a WorkspaceAuthorityAckV2) matches(plan WorkspaceAuthorityPlanV2) bool {
	sealed, err := plan.Publication()
	return err == nil && a.providerRevision > 0 && a.publicationID == sealed.PublicationID() && a.planDigest == sealed.Digest() && a.mutationCount == sealed.MutationCount()
}

type WorkspaceAuthorityCommandKindV2 string

const (
	WorkspaceBindV2       WorkspaceAuthorityCommandKindV2 = "bind"
	WorkspaceQuarantineV2 WorkspaceAuthorityCommandKindV2 = "expire_and_quarantine"
	WorkspaceRebindV2     WorkspaceAuthorityCommandKindV2 = "rebind_runtime_epoch"
)

type WorkspaceAuthorityRequestV2 struct {
	Kind             WorkspaceAuthorityCommandKindV2
	OperationID      journalv2.OperationID
	OperationEventID journalv2.EventID
	ExecutionEventID journalv2.EventID
	Governance       ExecutionGovernanceV2
	Contribution     workspace.PlanContributionV2
	ObservedAt       time.Time
}

type WorkspaceAuthorityResultV2 struct {
	Execution ExecutionSlotStateV2
	Workspace workspace.StateV2
	Replayed  bool
}

type WorkspaceAuthorityServiceV2 struct {
	port     WorkspaceAuthorityPortV2
	epochs   ExecutionEpochSourceV2
	registry journalv2.EventRegistryV2
}

func NewWorkspaceAuthorityServiceV2(port WorkspaceAuthorityPortV2, epochs ExecutionEpochSourceV2, registry journalv2.EventRegistryV2) (*WorkspaceAuthorityServiceV2, error) {
	if port == nil || epochs == nil {
		return nil, executionV2Error(KindInvalidCommand, "workspace_authority_dependencies", nil)
	}
	for _, tuple := range [][3]any{{"operation", uint32(2), uint32(2)}, {"execution_slot", uint32(1), uint32(1)}, {"workspace", uint32(1), uint32(1)}} {
		if _, err := registry.Resolve(tuple[0].(string), tuple[1].(uint32), tuple[2].(uint32)); err != nil {
			return nil, err
		}
	}
	return &WorkspaceAuthorityServiceV2{port: port, epochs: epochs, registry: registry}, nil
}

func (s *WorkspaceAuthorityServiceV2) Execute(ctx context.Context, request WorkspaceAuthorityRequestV2) (WorkspaceAuthorityResultV2, error) {
	workspaceState, err := request.Contribution.State()
	if err != nil || request.OperationID == "" || request.OperationEventID == "" || request.ExecutionEventID == "" || request.ObservedAt.IsZero() {
		return WorkspaceAuthorityResultV2{}, executionV2Error(KindInvalidCommand, "workspace_request", err)
	}
	binding, err := workspaceOperationBindingV2(request, workspaceState)
	if err != nil {
		return WorkspaceAuthorityResultV2{}, err
	}
	resolution, err := s.port.ResolveExecutionOperation(ctx, binding)
	if err != nil {
		return WorkspaceAuthorityResultV2{}, err
	}
	if resolution.Kind == ExecutionOperationConflictV2 {
		return WorkspaceAuthorityResultV2{}, executionV2Error(KindIdempotencyConflict, "operation_id", nil)
	}
	if resolution.Kind == ExecutionOperationExactV2 {
		if resolution.State.Status() != journalv2.StatusResolved || resolution.State.ResultKind() != "workspace.result" || resolution.State.ResultSchemaVersion() != workspaceAuthorityResultSchemaV2 {
			return WorkspaceAuthorityResultV2{}, executionV2Error(KindDecisionRequired, "operation_progress", nil)
		}
		var result WorkspaceAuthorityResultV2
		if err := strictExecutionJSONV2(resolution.State.Result(), &result); err != nil || result.Execution.Validate() != nil || result.Workspace.Validate() != nil {
			return WorkspaceAuthorityResultV2{}, executionV2Error(KindAuthorityConflict, "workspace_result", err)
		}
		result.Replayed = true
		return result, nil
	}
	current, err := s.epochs.CurrentRuntimeEpoch(ctx)
	if err != nil || current != workspaceState.Binding.RuntimeEpochID {
		return WorkspaceAuthorityResultV2{}, executionV2Error(KindStaleAuthority, "runtime_epoch", err)
	}
	executionCoordinate, err := ExecutionSlotCoordinateV2(workspaceState.Binding.ProjectID, workspaceState.Binding.MissionID, workspaceState.Binding.WorkRevisionID, workspaceState.Binding.WorkID)
	if err != nil {
		return WorkspaceAuthorityResultV2{}, err
	}
	executionObserved, err := s.port.ObserveExecutionSlot(ctx, executionCoordinate)
	if err != nil || executionObserved.State == nil {
		return WorkspaceAuthorityResultV2{}, executionV2Error(KindStaleAuthority, "execution_slot", err)
	}
	if request.Governance.validate() != nil || !equalExecutionGovernanceV2(request.Governance, executionObserved.State.Governance) {
		return WorkspaceAuthorityResultV2{}, executionV2Error(KindStaleAuthority, "governance_context", nil)
	}
	workspaceCoordinate, _ := workspace.WorkspaceCoordinateV2(workspaceState.Binding)
	workspaceObserved, err := s.port.ObserveWorkspace(ctx, workspaceCoordinate)
	if err != nil {
		return WorkspaceAuthorityResultV2{}, err
	}
	nextExecution, err := composeWorkspaceExecutionTransitionV2(request.Kind, request.ObservedAt, *executionObserved.State, workspaceState, workspaceObserved)
	if err != nil {
		return WorkspaceAuthorityResultV2{}, err
	}
	plan, err := buildWorkspaceAuthorityPlanV2(request, binding, nextExecution, executionObserved, workspaceObserved, s.registry)
	if err != nil {
		return WorkspaceAuthorityResultV2{}, err
	}
	ack, err := s.port.PublishWorkspace(ctx, plan)
	if err != nil || !ack.matches(plan) {
		return WorkspaceAuthorityResultV2{}, executionV2Error(KindControlStoreBusy, "workspace_publication", err)
	}
	return WorkspaceAuthorityResultV2{Execution: nextExecution, Workspace: workspaceState}, nil
}

func composeWorkspaceExecutionTransitionV2(kind WorkspaceAuthorityCommandKindV2, observedAt time.Time, prior ExecutionSlotStateV2, nextWorkspace workspace.StateV2, observed WorkspaceAuthorityObservationV2) (ExecutionSlotStateV2, error) {
	b := nextWorkspace.Binding
	if prior.ProjectID != b.ProjectID || prior.MissionID != b.MissionID || prior.WorkRevisionID != b.WorkRevisionID || prior.WorkID != b.WorkID || prior.RunID != b.RunID || prior.LeaseID != b.LeaseID || prior.Generation != b.Generation || prior.Fence != b.Fence || prior.ActorID != b.ActorID || prior.ProcessID != b.ProcessID {
		return ExecutionSlotStateV2{}, executionV2Error(KindStaleAuthority, "workspace_binding", nil)
	}
	next := prior
	next.LogicalVersion++
	switch kind {
	case WorkspaceBindV2:
		if prior.RuntimeEpochID != b.RuntimeEpochID {
			return ExecutionSlotStateV2{}, executionV2Error(KindStaleAuthority, "workspace_binding", nil)
		}
		if prior.Status != ExecutionActiveV2 || prior.WorkspaceID != "" || !prior.WorkspacePathClaimDigest.IsZero() || observed.State != nil || nextWorkspace.Stage != workspace.StageCreationRequestedV2 || nextWorkspace.LogicalVersion != 1 || !observedAt.Before(mustExecutionTimeV2(prior.Deadline)) {
			return ExecutionSlotStateV2{}, executionV2Error(KindStaleAuthority, "workspace_bind", nil)
		}
		next.WorkspaceID, next.WorkspacePathClaimDigest = b.WorkspaceID, b.PathClaimDigest
	case WorkspaceQuarantineV2:
		if prior.RuntimeEpochID != b.RuntimeEpochID {
			return ExecutionSlotStateV2{}, executionV2Error(KindStaleAuthority, "workspace_binding", nil)
		}
		if prior.Status != ExecutionActiveV2 || prior.WorkspaceID != b.WorkspaceID || prior.WorkspacePathClaimDigest != b.PathClaimDigest || observed.State == nil || observed.RecordRevision == 0 || nextWorkspace.Stage != workspace.StageQuarantinedV2 || observedAt.Before(mustExecutionTimeV2(prior.Deadline)) {
			return ExecutionSlotStateV2{}, executionV2Error(KindStaleAuthority, "workspace_expiry", nil)
		}
		next.Status = ExecutionExpiredV2
	case WorkspaceRebindV2:
		if prior.Status != ExecutionActiveV2 || prior.RuntimeEpochID == b.RuntimeEpochID || prior.WorkspaceID != b.WorkspaceID || prior.WorkspacePathClaimDigest != b.PathClaimDigest || observed.State == nil || observed.RecordRevision == 0 || observed.State.LogicalVersion+1 != nextWorkspace.LogicalVersion || observed.State.Stage != nextWorkspace.Stage || observed.State.Binding.RuntimeEpochID != prior.RuntimeEpochID || !observedAt.Before(mustExecutionTimeV2(prior.Deadline)) {
			return ExecutionSlotStateV2{}, executionV2Error(KindStaleAuthority, "workspace_epoch_rebind", nil)
		}
		next.RuntimeEpochID = b.RuntimeEpochID
		next.HeartbeatAt, next.Deadline = prior.HeartbeatAt, prior.Deadline
	default:
		return ExecutionSlotStateV2{}, executionV2Error(KindInvalidCommand, "workspace_command", nil)
	}
	if err := next.Validate(); err != nil {
		return ExecutionSlotStateV2{}, err
	}
	return next, nil
}

func workspaceOperationBindingV2(request WorkspaceAuthorityRequestV2, state workspace.StateV2) (journalv2.OperationBindingV2, error) {
	coordinate, err := workspace.WorkspaceCoordinateV2(state.Binding)
	if err != nil {
		return journalv2.OperationBindingV2{}, err
	}
	command, _ := journalv2.CanonicalObject(mustJSONV2(struct {
		Kind      WorkspaceAuthorityCommandKindV2 `json:"kind"`
		Workspace []byte                          `json:"workspace"`
	}{request.Kind, state.CanonicalBytes()}))
	digest, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "workspace." + string(request.Kind), CommandSchemaVersion: 1, Scope: coordinate.Scope, Command: command})
	if err != nil {
		return journalv2.OperationBindingV2{}, err
	}
	b := state.Binding
	governance := request.Governance
	if err := governance.validate(); err != nil {
		return journalv2.OperationBindingV2{}, executionV2Error(KindInvalidCommand, "governance_context", err)
	}
	return journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: request.OperationID, CommandKind: "workspace." + string(request.Kind), CommandSchemaVersion: 1, CanonicalIntentDigest: digest, Scope: coordinate.Scope, ActorID: journalv2.ActorID(b.ActorID), ProcessID: journalv2.ProcessID(b.ProcessID), DoctrineRevision: governance.DoctrineRevision, ProfileRevision: governance.ProfileRevision, PolicyRevision: governance.PolicyRevision, OriginatingRuntimeEpochID: b.RuntimeEpochID, ObservedCreatedAt: request.ObservedAt.UTC().Truncate(time.Second).Format(time.RFC3339)})
}

func buildWorkspaceAuthorityPlanV2(request WorkspaceAuthorityRequestV2, binding journalv2.OperationBindingV2, nextExecution ExecutionSlotStateV2, executionObserved ExecutionSlotObservationV2, workspaceObserved WorkspaceAuthorityObservationV2, registry journalv2.EventRegistryV2) (WorkspaceAuthorityPlanV2, error) {
	input, err := workspaceAuthorityPlanInputV2(request, binding, nextExecution, executionObserved, workspaceObserved)
	if err != nil {
		return WorkspaceAuthorityPlanV2{}, err
	}
	if err := validateWorkspaceAuthorityPlanInputV2(request.Kind, input); err != nil {
		return WorkspaceAuthorityPlanV2{}, err
	}
	sealed, err := journalv2.SealCommitPlanV2(input, registry)
	if err != nil {
		return WorkspaceAuthorityPlanV2{}, err
	}
	return WorkspaceAuthorityPlanV2{sealed: sealed}, nil
}

func workspaceAuthorityPlanInputV2(request WorkspaceAuthorityRequestV2, binding journalv2.OperationBindingV2, nextExecution ExecutionSlotStateV2, executionObserved ExecutionSlotObservationV2, workspaceObserved WorkspaceAuthorityObservationV2) (journalv2.CommitPlanInput, error) {
	workspaceState, _ := request.Contribution.State()
	resultBytes, _ := journalv2.CanonicalObject(mustJSONV2(WorkspaceAuthorityResultV2{Execution: nextExecution, Workspace: workspaceState}))
	eventIDs := []journalv2.EventID{request.OperationEventID, request.ExecutionEventID}
	// The contribution's event ID is added by AppendPlanContributionV2.
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: binding.OperationID(), LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "workspace.result", ResultSchemaVersion: workspaceAuthorityResultSchemaV2, CanonicalResult: resultBytes, EventIDs: sortWorkspaceEventsV2(append(eventIDs, contributionEventIDV2(request.Contribution))), EffectIDs: contributionEffectIDsV2(request.Contribution)})
	if err != nil {
		return journalv2.CommitPlanInput{}, err
	}
	opPayload, err := journalv2.EncodeOperationStateEventPayloadV2(operation)
	if err != nil {
		return journalv2.CommitPlanInput{}, err
	}
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(binding.OperationID()), journalv2.Scope{Kind: journalv2.ScopeHost})
	execCoordinate, _ := ExecutionSlotCoordinateV2(nextExecution.ProjectID, nextExecution.MissionID, nextExecution.WorkRevisionID, nextExecution.WorkID)
	execStateBytes := nextExecution.CanonicalBytes()
	execDigest, _ := journalv2.ReducerStateDigestV2(execStateBytes)
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	kind := "workspace_bound"
	if request.Kind == WorkspaceQuarantineV2 {
		kind = "lease_expired"
	} else if request.Kind == WorkspaceRebindV2 {
		kind = "runtime_epoch_rebound"
	}
	pred := executionCoordinatesV2(*executionObserved.State)
	execEventBytes, err := encodeExecutionEventV2(ExecutionSlotEventV2{SchemaVersion: 1, Kind: kind, ObservedAt: request.ObservedAt.UTC().Format(time.RFC3339Nano), Predecessor: &pred, State: nextExecution})
	if err != nil {
		return journalv2.CommitPlanInput{}, err
	}
	governance := nextExecution.Governance.journalRevisions()
	events := []journalv2.EventDraftV2{
		{EventID: request.OperationEventID, EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: nextExecution.RuntimeEpochID, ActorID: journalv2.ActorID(nextExecution.ActorID), ProcessID: journalv2.ProcessID(nextExecution.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(nextExecution.WorkRevisionID), PolicyRevision: nextExecution.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: opPayload},
		{EventID: request.ExecutionEventID, EventType: "execution_slot." + kind, Family: "execution_slot", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: execCoordinate, AggregateLogicalVersion: journalv2.LogicalVersion(nextExecution.LogicalVersion), CommittedRuntimeEpochID: nextExecution.RuntimeEpochID, ActorID: journalv2.ActorID(nextExecution.ActorID), ProcessID: journalv2.ProcessID(nextExecution.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(nextExecution.WorkRevisionID), PolicyRevision: nextExecution.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: execEventBytes},
	}
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: opCoordinate, NextLogicalVersion: 1, EventID: request.OperationEventID, CommittedRuntimeEpochID: nextExecution.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: []byte(`{"status":"resolved"}`)},
		{Coordinate: execCoordinate, ExpectedLogicalVersion: journalv2.LogicalVersion(executionObserved.State.LogicalVersion), NextLogicalVersion: journalv2.LogicalVersion(nextExecution.LogicalVersion), EventID: request.ExecutionEventID, ExpectedRecordRevision: executionObserved.RecordRevision, CommittedRuntimeEpochID: nextExecution.RuntimeEpochID, CanonicalStateDigest: execDigest, AuthoritySummary: execStateBytes},
	}
	conditions := make([]journalv2.ProviderCondition, 0, 3)
	for _, head := range heads {
		condition, e := journalv2.ConditionForHeadTransitionV2(head)
		if e != nil {
			return journalv2.CommitPlanInput{}, e
		}
		conditions = append(conditions, condition)
	}
	input := journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: operation, Events: events, Heads: heads, Conditions: conditions}
	input, err = workspace.AppendPlanContributionV2(input, request.Contribution)
	if err != nil {
		return journalv2.CommitPlanInput{}, err
	}
	for i := range input.Events {
		if input.Events[i].Family == "workspace" {
			input.Events[i].PolicyRevision = nextExecution.Governance.PolicyRevision
			input.Events[i].GovernanceRevisions = append([]string(nil), governance...)
		}
	}
	return input, nil
}

func validateWorkspaceAuthorityPlanInputV2(kind WorkspaceAuthorityCommandKindV2, input journalv2.CommitPlanInput) error {
	if len(input.Events) != 3 || len(input.Heads) != 3 || len(input.Conditions) != 3 || len(input.Assertions) != 0 {
		return executionV2Error(KindInvalidCommand, "workspace_plan_membership", nil)
	}
	expectedTypes := map[string]string{"operation": "operation.resolved"}
	switch kind {
	case WorkspaceBindV2:
		expectedTypes["execution_slot"] = "execution_slot.workspace_bound"
		expectedTypes["workspace"] = "workspace.preparation_requested"
		if input.WorkspacePathClaim == nil || len(input.EffectIntents) != 1 {
			return executionV2Error(KindInvalidCommand, "workspace_bind_membership", nil)
		}
	case WorkspaceQuarantineV2:
		expectedTypes["execution_slot"] = "execution_slot.lease_expired"
		expectedTypes["workspace"] = "workspace.quarantined"
		if input.WorkspacePathClaim != nil || len(input.EffectIntents) != 0 {
			return executionV2Error(KindInvalidCommand, "workspace_quarantine_membership", nil)
		}
	case WorkspaceRebindV2:
		expectedTypes["execution_slot"] = "execution_slot.runtime_epoch_rebound"
		expectedTypes["workspace"] = "workspace.runtime_epoch_rebound"
		if input.WorkspacePathClaim != nil || len(input.EffectIntents) != 0 {
			return executionV2Error(KindInvalidCommand, "workspace_rebind_membership", nil)
		}
	default:
		return executionV2Error(KindInvalidCommand, "workspace_command", nil)
	}
	events := make(map[string]journalv2.EventDraftV2, 3)
	for _, event := range input.Events {
		if _, duplicate := events[event.Family]; duplicate || expectedTypes[event.Family] != event.EventType || string(event.Coordinate.Kind) != event.Family {
			return executionV2Error(KindInvalidCommand, "workspace_plan_event", nil)
		}
		events[event.Family] = event
	}
	if len(events) != len(expectedTypes) {
		return executionV2Error(KindInvalidCommand, "workspace_plan_event", nil)
	}
	heads := make(map[string]journalv2.HeadTransitionV2, 3)
	for _, head := range input.Heads {
		family := string(head.Coordinate.Kind)
		event, ok := events[family]
		if !ok || head.EventID != event.EventID || head.Coordinate != event.Coordinate {
			return executionV2Error(KindInvalidCommand, "workspace_plan_head", nil)
		}
		heads[family] = head
	}
	if len(heads) != 3 {
		return executionV2Error(KindInvalidCommand, "workspace_plan_head", nil)
	}
	wantConditions := make(map[journalv2.ConditionTarget]journalv2.ProviderCondition, 3)
	for _, head := range input.Heads {
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil {
			return err
		}
		wantConditions[condition.Target] = condition
	}
	seenConditions := make(map[journalv2.ConditionTarget]struct{}, 3)
	for _, condition := range input.Conditions {
		_, duplicate := seenConditions[condition.Target]
		if want, ok := wantConditions[condition.Target]; duplicate || !ok || want != condition {
			return executionV2Error(KindInvalidCommand, "workspace_plan_condition", nil)
		}
		seenConditions[condition.Target] = struct{}{}
	}
	eventIDs := input.OperationState.EventIDs()
	if len(eventIDs) != 3 {
		return executionV2Error(KindInvalidCommand, "workspace_operation_membership", nil)
	}
	wantIDs := make([]journalv2.EventID, 0, 3)
	for _, event := range input.Events {
		wantIDs = append(wantIDs, event.EventID)
	}
	wantIDs = sortWorkspaceEventsV2(wantIDs)
	for index := range wantIDs {
		if eventIDs[index] != wantIDs[index] {
			return executionV2Error(KindInvalidCommand, "workspace_operation_membership", nil)
		}
	}
	return nil
}

func contributionEventIDV2(contribution workspace.PlanContributionV2) journalv2.EventID {
	input, err := workspace.AppendPlanContributionV2(journalv2.CommitPlanInput{}, contribution)
	if err != nil || len(input.Events) != 1 {
		return ""
	}
	return input.Events[0].EventID
}

func contributionEffectIDsV2(contribution workspace.PlanContributionV2) []journalv2.EffectID {
	input, err := workspace.AppendPlanContributionV2(journalv2.CommitPlanInput{}, contribution)
	if err != nil || len(input.EffectIntents) == 0 {
		return nil
	}
	result := make([]journalv2.EffectID, len(input.EffectIntents))
	for i := range input.EffectIntents {
		result[i] = input.EffectIntents[i].EffectID
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func mustJSONV2(value any) []byte {
	raw, _ := json.Marshal(value)
	canonical, _ := journalv2.CanonicalObject(raw)
	return canonical
}

func sortWorkspaceEventsV2(events []journalv2.EventID) []journalv2.EventID {
	result := append([]journalv2.EventID(nil), events...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
