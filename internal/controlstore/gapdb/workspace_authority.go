package gapdb

import (
	"context"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type workspaceAuthorityV2 struct {
	adapter     *Adapter
	publisher   executionPublicationPortV2
	coordinator *journalv2.PublicationCoordinatorV2
	execution   *executionAuthorityV2
}

func newWorkspaceAuthorityV2(adapter *Adapter, coordinator *journalv2.PublicationCoordinatorV2) (*workspaceAuthorityV2, error) {
	executionAuthority, err := newExecutionAuthorityV2(adapter, coordinator)
	if err != nil {
		return nil, err
	}
	return &workspaceAuthorityV2{adapter: adapter, publisher: coordinator, coordinator: coordinator, execution: executionAuthority}, nil
}

func (a *workspaceAuthorityV2) ResolveExecutionOperation(ctx context.Context, binding journalv2.OperationBindingV2) (execution.ExecutionOperationResolutionV2, error) {
	if a == nil || a.execution == nil {
		return execution.ExecutionOperationResolutionV2{}, invalidV2Plan("workspace operation reader")
	}
	return a.execution.ResolveExecutionOperation(ctx, binding)
}

func (a *workspaceAuthorityV2) ResolveWorkspaceOperation(ctx context.Context, binding journalv2.OperationBindingV2) (workspace.StageOperationResolutionV2, error) {
	resolved, err := a.ResolveExecutionOperation(ctx, binding)
	if err != nil {
		return workspace.StageOperationResolutionV2{}, err
	}
	kind := workspace.StageOperationResolutionKindV2(resolved.Kind)
	return workspace.StageOperationResolutionV2{Kind: kind, State: resolved.State, RecordRevision: resolved.RecordRevision}, nil
}

func (a *workspaceAuthorityV2) ObserveExecutionSlot(ctx context.Context, coordinate journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	if a == nil || a.execution == nil {
		return execution.ExecutionSlotObservationV2{}, invalidV2Plan("workspace execution reader")
	}
	return a.execution.ObserveExecutionSlot(ctx, coordinate)
}

func (a *workspaceAuthorityV2) ObserveWorkspace(ctx context.Context, coordinate journalv2.AggregateCoordinate) (execution.WorkspaceAuthorityObservationV2, error) {
	if a == nil || a.adapter == nil || coordinate.Kind != "workspace" || coordinate.Scope.Kind != journalv2.ScopeHost {
		return execution.WorkspaceAuthorityObservationV2{}, invalidV2Plan("workspace authority reader")
	}
	revision, err := a.adapter.CaptureRevisionV2(ctx)
	if err != nil {
		return execution.WorkspaceAuthorityObservationV2{}, err
	}
	address, err := journalv2.NewAggregateHeadAddressV2(coordinate)
	if err != nil {
		return execution.WorkspaceAuthorityObservationV2{}, err
	}
	request, _ := journalv2.NewReadRequestV2(address, revision)
	record, err := a.adapter.ReadV2(ctx, request)
	if err != nil {
		if journalv2.IsKind(err, journalv2.ErrorNotFound) {
			if verifyErr := a.adapter.VerifyRevisionV2(ctx, revision); verifyErr != nil {
				return execution.WorkspaceAuthorityObservationV2{}, verifyErr
			}
			return execution.WorkspaceAuthorityObservationV2{}, nil
		}
		return execution.WorkspaceAuthorityObservationV2{}, err
	}
	head, err := journalv2.DecodeAggregateHeadV2(record.CanonicalValue())
	if err != nil || head.Coordinate() != coordinate || head.Tombstone() {
		return execution.WorkspaceAuthorityObservationV2{}, invalidV2Plan("workspace authority head")
	}
	state, err := workspace.DecodeStateV2(head.AuthoritySummary())
	if err != nil {
		return execution.WorkspaceAuthorityObservationV2{}, err
	}
	digest, _ := journalv2.ReducerStateDigestV2(state.CanonicalBytes())
	stateCoordinate, _ := workspace.WorkspaceCoordinateV2(state.Binding)
	if stateCoordinate != coordinate || digest != head.CanonicalStateDigest() || head.LogicalVersion() != journalv2.LogicalVersion(state.LogicalVersion) || head.CommittedRuntimeEpochID() != state.Binding.RuntimeEpochID || head.LastEventID() == "" || head.LastPublicationID() == "" {
		return execution.WorkspaceAuthorityObservationV2{}, invalidV2Plan("workspace authority digest")
	}
	if err := a.adapter.VerifyRevisionV2(ctx, revision); err != nil {
		return execution.WorkspaceAuthorityObservationV2{}, err
	}
	return execution.WorkspaceAuthorityObservationV2{State: &state, RecordRevision: record.RecordRevision(), LastPublicationID: head.LastPublicationID()}, nil
}

func (a *workspaceAuthorityV2) ObserveWorkspaceStage(ctx context.Context, coordinate journalv2.AggregateCoordinate) (workspace.StageObservationV2, error) {
	observed, err := a.ObserveWorkspace(ctx, coordinate)
	if err != nil {
		return workspace.StageObservationV2{}, err
	}
	return workspace.StageObservationV2{State: observed.State, RecordRevision: observed.RecordRevision, LastPublicationID: observed.LastPublicationID}, nil
}

func (a *workspaceAuthorityV2) ObserveWorkspaceExecutionAssertion(ctx context.Context, binding workspace.BindingV2) (journalv2.AuthorityAssertionV2, error) {
	if a == nil || a.adapter == nil || binding.Validate() != nil {
		return journalv2.AuthorityAssertionV2{}, invalidV2Plan("workspace execution assertion")
	}
	coordinate, err := execution.ExecutionSlotCoordinateV2(binding.ProjectID, binding.MissionID, binding.WorkRevisionID, binding.WorkID)
	if err != nil {
		return journalv2.AuthorityAssertionV2{}, err
	}
	revision, err := a.adapter.CaptureRevisionV2(ctx)
	if err != nil {
		return journalv2.AuthorityAssertionV2{}, err
	}
	address, _ := journalv2.NewAggregateHeadAddressV2(coordinate)
	request, _ := journalv2.NewReadRequestV2(address, revision)
	record, err := a.adapter.ReadV2(ctx, request)
	if err != nil {
		return journalv2.AuthorityAssertionV2{}, err
	}
	head, err := journalv2.DecodeAggregateHeadV2(record.CanonicalValue())
	if err != nil || head.Coordinate() != coordinate || head.Tombstone() {
		return journalv2.AuthorityAssertionV2{}, invalidV2Plan("workspace execution assertion head")
	}
	state, err := execution.DecodeExecutionSlotStateV2(head.AuthoritySummary())
	if err != nil {
		return journalv2.AuthorityAssertionV2{}, err
	}
	stateDigest, digestErr := journalv2.ReducerStateDigestV2(state.CanonicalBytes())
	if digestErr != nil || stateDigest != head.CanonicalStateDigest() || head.Coordinate() != coordinate || head.LogicalVersion() != journalv2.LogicalVersion(state.LogicalVersion) || head.CommittedRuntimeEpochID() != state.RuntimeEpochID || head.LastEventID() == "" || head.LastPublicationID() == "" || state.WorkspaceID != binding.WorkspaceID || state.WorkspacePathClaimDigest != binding.PathClaimDigest || state.ProjectID != binding.ProjectID || state.MissionID != binding.MissionID || state.WorkRevisionID != binding.WorkRevisionID || state.WorkID != binding.WorkID || state.RunID != binding.RunID || state.LeaseID != binding.LeaseID || state.Generation != binding.Generation || state.Fence != binding.Fence || state.RuntimeEpochID != binding.RuntimeEpochID || state.ActorID != binding.ActorID || state.ProcessID != binding.ProcessID || state.Status != execution.ExecutionActiveV2 {
		return journalv2.AuthorityAssertionV2{}, invalidV2Plan("workspace execution assertion authority")
	}
	valueDigest, _ := journalv2.ReducerStateDigestV2(record.CanonicalValue())
	assertion, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: journalv2.ConditionTarget{Kind: journalv2.ConditionTargetAggregateHead, Coordinate: coordinate}, Kind: journalv2.ConditionRecordRevision, ObservedRevision: record.RecordRevision(), ExpectedLogicalVersion: head.LogicalVersion(), ObservedValueDigest: valueDigest})
	if err != nil {
		return journalv2.AuthorityAssertionV2{}, err
	}
	if err := a.adapter.VerifyRevisionV2(ctx, revision); err != nil {
		return journalv2.AuthorityAssertionV2{}, err
	}
	return assertion, nil
}

func (a *workspaceAuthorityV2) PublishWorkspace(ctx context.Context, capability execution.WorkspaceAuthorityPlanV2) (execution.WorkspaceAuthorityAckV2, error) {
	if a == nil || a.publisher == nil {
		return execution.WorkspaceAuthorityAckV2{}, invalidV2Plan("workspace authority publisher")
	}
	plan, err := capability.Publication()
	if err != nil || !workspacePlanMembershipV2(plan) {
		return execution.WorkspaceAuthorityAckV2{}, invalidV2Plan("workspace authority plan membership")
	}
	ack, err := a.publisher.Publish(ctx, plan)
	if err != nil {
		if a.adapter == nil || a.coordinator == nil || !isAuthorityDeliveryFailureV2(err) {
			return execution.WorkspaceAuthorityAckV2{}, err
		}
		ack, err = reconcileRegisteredPublicationV2(ctx, a.adapter, a.coordinator, plan)
		if err != nil {
			return execution.WorkspaceAuthorityAckV2{}, err
		}
	}
	return execution.NewWorkspaceAuthorityAckV2(capability, ack)
}

func (a *workspaceAuthorityV2) PublishWorkspaceStage(ctx context.Context, capability workspace.StageAuthorityPlanV2) (workspace.StageAuthorityAckV2, error) {
	if a == nil || a.publisher == nil {
		return workspace.StageAuthorityAckV2{}, invalidV2Plan("workspace stage publisher")
	}
	plan, err := capability.Publication()
	if err != nil || !workspaceStagePlanMembershipV2(plan) {
		return workspace.StageAuthorityAckV2{}, invalidV2Plan("workspace stage plan membership")
	}
	ack, err := a.publisher.Publish(ctx, plan)
	if err != nil {
		if a.adapter == nil || a.coordinator == nil || !isAuthorityDeliveryFailureV2(err) {
			return workspace.StageAuthorityAckV2{}, err
		}
		ack, err = reconcileRegisteredPublicationV2(ctx, a.adapter, a.coordinator, plan)
		if err != nil {
			return workspace.StageAuthorityAckV2{}, err
		}
	}
	return workspace.NewStageAuthorityAckV2(capability, ack)
}

func workspacePlanMembershipV2(plan journalv2.SealedCommitPlanV2) bool {
	if plan.BindingCreate() == nil || len(plan.Events()) != 3 || len(plan.Heads()) != 3 || len(plan.Conditions()) != 3 || len(plan.EligibilityGuards()) != 0 || len(plan.Assertions()) != 0 || len(plan.Checkpoints()) != 0 {
		return false
	}
	families := map[string]int{}
	for _, event := range plan.Events() {
		families[event.Family()]++
	}
	if families["operation"] != 1 || families["execution_slot"] != 1 || families["workspace"] != 1 || len(families) != 3 {
		return false
	}
	hasClaim := plan.WorkspacePathClaim() != nil
	if hasClaim != (len(plan.EffectIntents()) == 1) {
		return false
	}
	if !hasClaim && len(plan.EffectIntents()) != 0 {
		return false
	}
	return true
}

func workspaceStagePlanMembershipV2(plan journalv2.SealedCommitPlanV2) bool {
	if plan.BindingCreate() == nil || len(plan.Events()) != 2 || len(plan.Heads()) != 2 || len(plan.Conditions()) != 2 || len(plan.Assertions()) != 1 || len(plan.EligibilityGuards()) != 0 || plan.WorkspacePathClaim() != nil || len(plan.Checkpoints()) != 0 {
		return false
	}
	families := map[string]int{}
	for _, event := range plan.Events() {
		families[event.Family()]++
	}
	return len(families) == 2 && families["operation"] == 1 && families["workspace"] == 1 && len(plan.EffectIntents()) <= 1
}

var _ execution.WorkspaceAuthorityPortV2 = (*workspaceAuthorityV2)(nil)
var _ workspace.StageAuthorityPortV2 = (*workspaceAuthorityV2)(nil)
