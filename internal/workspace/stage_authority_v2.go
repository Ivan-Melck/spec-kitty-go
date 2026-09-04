package workspace

import (
	"context"
	"sort"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

const workspaceStageResultSchemaV2 = uint32(1)

type StageOperationResolutionKindV2 string

const (
	StageOperationAbsentV2   StageOperationResolutionKindV2 = "absent"
	StageOperationExactV2    StageOperationResolutionKindV2 = "exact"
	StageOperationConflictV2 StageOperationResolutionKindV2 = "conflict"
)

type StageOperationResolutionV2 struct {
	Kind           StageOperationResolutionKindV2
	State          journalv2.OperationStateV2
	RecordRevision uint64
}
type StageObservationV2 struct {
	State             *StateV2
	RecordRevision    uint64
	LastPublicationID journalv2.PublicationID
}

type StageAuthorityPortV2 interface {
	ResolveWorkspaceOperation(context.Context, journalv2.OperationBindingV2) (StageOperationResolutionV2, error)
	ObserveWorkspaceExecutionAssertion(context.Context, BindingV2) (journalv2.AuthorityAssertionV2, error)
	ObserveWorkspaceStage(context.Context, journalv2.AggregateCoordinate) (StageObservationV2, error)
	PublishWorkspaceStage(context.Context, StageAuthorityPlanV2) (StageAuthorityAckV2, error)
}
type StageEpochSourceV2 interface {
	CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error)
}

type StageAuthorityRequestV2 struct {
	OperationID         journalv2.OperationID
	OperationEventID    journalv2.EventID
	Contribution        PlanContributionV2
	PolicyRevision      string
	DoctrineRevision    string
	ProfileRevision     string
	GovernanceRevisions []string
	ObservedAt          time.Time
}
type StageAuthorityPlanV2 struct{ sealed journalv2.SealedCommitPlanV2 }

func (p StageAuthorityPlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return journalv2.SealedCommitPlanV2{}, workspaceV2Error(KindInvalid, "stage_plan", nil)
	}
	return p.sealed, nil
}

type StageAuthorityAckV2 struct {
	publicationID    journalv2.PublicationID
	planDigest       journalv2.Digest
	mutationCount    int
	providerRevision uint64
}

func NewStageAuthorityAckV2(plan StageAuthorityPlanV2, ack journalv2.AcknowledgedPublicationV2) (StageAuthorityAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil || ack.PublicationID() != sealed.PublicationID() || ack.PlanDigest() != sealed.Digest() || ack.MutationCount() != sealed.MutationCount() || ack.ProviderRevision() == 0 {
		return StageAuthorityAckV2{}, workspaceV2Error(KindWorkspaceConflict, "stage_ack", err)
	}
	return StageAuthorityAckV2{ack.PublicationID(), ack.PlanDigest(), ack.MutationCount(), ack.ProviderRevision()}, nil
}
func (a StageAuthorityAckV2) matches(plan StageAuthorityPlanV2) bool {
	sealed, err := plan.Publication()
	return err == nil && a.providerRevision > 0 && a.publicationID == sealed.PublicationID() && a.planDigest == sealed.Digest() && a.mutationCount == sealed.MutationCount()
}

type StageAuthorityResultV2 struct {
	State    StateV2
	Replayed bool
}
type StageAuthorityServiceV2 struct {
	port     StageAuthorityPortV2
	epochs   StageEpochSourceV2
	registry journalv2.EventRegistryV2
}

func NewStageAuthorityServiceV2(port StageAuthorityPortV2, epochs StageEpochSourceV2, registry journalv2.EventRegistryV2) (*StageAuthorityServiceV2, error) {
	if port == nil || epochs == nil {
		return nil, workspaceV2Error(KindInvalid, "stage_dependencies", nil)
	}
	if _, err := registry.Resolve("workspace", 1, 1); err != nil {
		return nil, err
	}
	return &StageAuthorityServiceV2{port, epochs, registry}, nil
}

func (s *StageAuthorityServiceV2) Execute(ctx context.Context, request StageAuthorityRequestV2) (StageAuthorityResultV2, error) {
	state, err := request.Contribution.State()
	if err != nil || request.OperationID == "" || request.OperationEventID == "" || request.ObservedAt.IsZero() {
		return StageAuthorityResultV2{}, workspaceV2Error(KindInvalid, "stage_request", err)
	}
	binding, err := stageOperationBindingV2(request, state)
	if err != nil {
		return StageAuthorityResultV2{}, err
	}
	resolution, err := s.port.ResolveWorkspaceOperation(ctx, binding)
	if err != nil {
		return StageAuthorityResultV2{}, err
	}
	if resolution.Kind == StageOperationConflictV2 {
		return StageAuthorityResultV2{}, workspaceV2Error(KindWorkspaceConflict, "operation_id", nil)
	}
	if resolution.Kind == StageOperationExactV2 {
		if resolution.State.Status() != journalv2.StatusResolved || resolution.State.ResultKind() != "workspace.stage.result" {
			return StageAuthorityResultV2{}, workspaceV2Error(KindWorkspaceConflict, "operation_progress", nil)
		}
		restored, decodeErr := DecodeStateV2(resolution.State.Result())
		if decodeErr != nil {
			return StageAuthorityResultV2{}, decodeErr
		}
		return StageAuthorityResultV2{State: restored, Replayed: true}, nil
	}
	epoch, err := s.epochs.CurrentRuntimeEpoch(ctx)
	if err != nil || epoch != state.Binding.RuntimeEpochID {
		return StageAuthorityResultV2{}, workspaceV2Error(KindStaleAuthority, "runtime_epoch", err)
	}
	coordinate, _ := WorkspaceCoordinateV2(state.Binding)
	observed, err := s.port.ObserveWorkspaceStage(ctx, coordinate)
	if err != nil || observed.State == nil || observed.RecordRevision == 0 {
		return StageAuthorityResultV2{}, workspaceV2Error(KindWorkspaceConflict, "workspace_observation", err)
	}
	if state.LogicalVersion != observed.State.LogicalVersion+1 || !bindingEqualV2(state.Binding, observed.State.Binding) {
		return StageAuthorityResultV2{}, workspaceV2Error(KindWorkspaceConflict, "workspace_transition", nil)
	}
	assertion, err := s.port.ObserveWorkspaceExecutionAssertion(ctx, state.Binding)
	if err != nil {
		return StageAuthorityResultV2{}, err
	}
	if err := validateExecutionAssertionForBindingV2(assertion, state.Binding); err != nil {
		return StageAuthorityResultV2{}, err
	}
	plan, err := buildStageAuthorityPlanV2(request, binding, state, assertion, s.registry)
	if err != nil {
		return StageAuthorityResultV2{}, err
	}
	ack, err := s.port.PublishWorkspaceStage(ctx, plan)
	if err != nil || !ack.matches(plan) {
		return StageAuthorityResultV2{}, workspaceV2Error(KindWorkspaceConflict, "stage_publication", err)
	}
	return StageAuthorityResultV2{State: state}, nil
}

func validateExecutionAssertionForBindingV2(assertion journalv2.AuthorityAssertionV2, b BindingV2) error {
	t := assertion.Target()
	scope := t.Coordinate.Scope
	if assertion.Kind() != journalv2.ConditionRecordRevision || assertion.ObservedRevision() == 0 || assertion.ExpectedLogicalVersion() == 0 || assertion.ObservedValueDigest().IsZero() || t.Kind != journalv2.ConditionTargetAggregateHead || t.Coordinate.Kind != "execution_slot" || scope.Kind != journalv2.ScopeWork || scope.ProjectID != journalv2.ProjectID(b.ProjectID) || scope.MissionID != journalv2.MissionID(b.MissionID) || scope.WorkRevisionID != journalv2.WorkRevisionID(b.WorkRevisionID) || scope.WorkID != journalv2.WorkID(b.WorkID) {
		return workspaceV2Error(KindStaleAuthority, "execution_assertion", nil)
	}
	return nil
}
func stageOperationBindingV2(request StageAuthorityRequestV2, state StateV2) (journalv2.OperationBindingV2, error) {
	coordinate, _ := WorkspaceCoordinateV2(state.Binding)
	command := mustJSONV2(struct {
		Workspace []byte `json:"workspace"`
	}{state.CanonicalBytes()})
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "workspace.stage", CommandSchemaVersion: 1, Scope: coordinate.Scope, Command: command})
	if err != nil {
		return journalv2.OperationBindingV2{}, err
	}
	b := state.Binding
	return journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: request.OperationID, CommandKind: "workspace.stage", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: coordinate.Scope, ActorID: journalv2.ActorID(b.ActorID), ProcessID: journalv2.ProcessID(b.ProcessID), DoctrineRevision: request.DoctrineRevision, ProfileRevision: request.ProfileRevision, PolicyRevision: request.PolicyRevision, OriginatingRuntimeEpochID: b.RuntimeEpochID, ObservedCreatedAt: request.ObservedAt.UTC().Truncate(time.Second).Format(time.RFC3339)})
}
func buildStageAuthorityPlanV2(request StageAuthorityRequestV2, binding journalv2.OperationBindingV2, state StateV2, assertion journalv2.AuthorityAssertionV2, registry journalv2.EventRegistryV2) (StageAuthorityPlanV2, error) {
	contributionInput, err := AppendPlanContributionV2(journalv2.CommitPlanInput{}, request.Contribution)
	if err != nil {
		return StageAuthorityPlanV2{}, err
	}
	workspaceEvent := contributionInput.Events[0]
	eventIDs := []journalv2.EventID{request.OperationEventID, workspaceEvent.EventID}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	effects := make([]journalv2.EffectID, len(contributionInput.EffectIntents))
	for i := range contributionInput.EffectIntents {
		effects[i] = contributionInput.EffectIntents[i].EffectID
	}
	sort.Slice(effects, func(i, j int) bool { return effects[i] < effects[j] })
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: request.OperationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "workspace.stage.result", ResultSchemaVersion: workspaceStageResultSchemaV2, CanonicalResult: state.CanonicalBytes(), EventIDs: eventIDs, EffectIDs: effects})
	if err != nil {
		return StageAuthorityPlanV2{}, err
	}
	opPayload, err := journalv2.EncodeOperationStateEventPayloadV2(operation)
	if err != nil {
		return StageAuthorityPlanV2{}, err
	}
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(request.OperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	governance := append([]string(nil), request.GovernanceRevisions...)
	sort.Strings(governance)
	workspaceEvent.PolicyRevision = request.PolicyRevision
	workspaceEvent.GovernanceRevisions = append([]string(nil), governance...)
	opEvent := journalv2.EventDraftV2{EventID: request.OperationEventID, EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: state.Binding.RuntimeEpochID, ActorID: journalv2.ActorID(state.Binding.ActorID), ProcessID: journalv2.ProcessID(state.Binding.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(state.Binding.WorkRevisionID), PolicyRevision: request.PolicyRevision, GovernanceRevisions: governance, Payload: opPayload}
	opHead := journalv2.HeadTransitionV2{Coordinate: opCoordinate, NextLogicalVersion: 1, EventID: request.OperationEventID, CommittedRuntimeEpochID: state.Binding.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: []byte(`{"status":"resolved"}`)}
	opCondition, _ := journalv2.ConditionForHeadTransitionV2(opHead)
	input := journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: operation, Events: []journalv2.EventDraftV2{opEvent, workspaceEvent}, Heads: []journalv2.HeadTransitionV2{opHead, contributionInput.Heads[0]}, EffectIntents: contributionInput.EffectIntents, Conditions: []journalv2.ProviderCondition{opCondition, contributionInput.Conditions[0]}, Assertions: []journalv2.AuthorityAssertionV2{assertion}}
	sealed, err := journalv2.SealCommitPlanV2(input, registry)
	if err != nil {
		return StageAuthorityPlanV2{}, err
	}
	return StageAuthorityPlanV2{sealed}, nil
}
