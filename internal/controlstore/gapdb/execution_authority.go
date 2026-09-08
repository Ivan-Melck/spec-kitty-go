package gapdb

import (
	"context"
	"errors"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type executionPublicationPortV2 interface {
	Publish(context.Context, journalv2.SealedCommitPlanV2) (journalv2.AcknowledgedPublicationV2, error)
}

// executionAuthorityV2 is deliberately unexported. It translates the exact
// execution capability to the common journal reader/coordinator and exposes no
// raw key/value or generic store operation.
type executionAuthorityV2 struct {
	adapter     *Adapter
	publisher   executionPublicationPortV2
	coordinator *journalv2.PublicationCoordinatorV2
}

func newExecutionAuthorityV2(adapter *Adapter, coordinator *journalv2.PublicationCoordinatorV2) (*executionAuthorityV2, error) {
	if adapter == nil || coordinator == nil {
		return nil, invalidV2Plan("execution authority dependencies")
	}
	return &executionAuthorityV2{adapter: adapter, publisher: coordinator, coordinator: coordinator}, nil
}

// ExecutionAuthorityPortV2 exposes the execution-owned semantic port while
// retaining the provider implementation and raw database handle in this
// package.
func (a *Adapter) ExecutionAuthorityPortV2(coordinator *journalv2.PublicationCoordinatorV2) (execution.ExecutionAuthorityPortV2, error) {
	return newExecutionAuthorityV2(a, coordinator)
}

func newExecutionAuthorityV2ForTest(publisher executionPublicationPortV2) *executionAuthorityV2 {
	return &executionAuthorityV2{publisher: publisher}
}

func (a *executionAuthorityV2) PublishExecution(ctx context.Context, capability execution.ExecutionAuthorityPlanV2) (execution.ExecutionPublicationAckV2, error) {
	if a == nil || a.publisher == nil {
		return execution.ExecutionPublicationAckV2{}, invalidV2Plan("execution authority plan membership")
	}
	plan, err := capability.Publication()
	if err != nil || !executionPlanMembershipV2(plan) {
		return execution.ExecutionPublicationAckV2{}, invalidV2Plan("execution authority plan membership")
	}
	ack, err := a.publisher.Publish(ctx, plan)
	if err != nil {
		if a.adapter == nil || a.coordinator == nil || !isAuthorityDeliveryFailureV2(err) {
			return execution.ExecutionPublicationAckV2{}, err
		}
		ack, err = reconcileRegisteredPublicationV2(ctx, a.adapter, a.coordinator, plan)
		if err != nil {
			return execution.ExecutionPublicationAckV2{}, err
		}
	}
	return execution.NewExecutionPublicationAckV2(capability, ack)
}

func isAuthorityDeliveryFailureV2(err error) bool {
	var providerError *Error
	return errors.As(err, &providerError)
}

func reconcileRegisteredPublicationV2(ctx context.Context, adapter *Adapter, coordinator *journalv2.PublicationCoordinatorV2, plan journalv2.SealedCommitPlanV2) (journalv2.AcknowledgedPublicationV2, error) {
	observation, err := adapter.ReconcileCommitV2(ctx, plan)
	if err == nil {
		return coordinator.ReconcileComplete(plan, observation)
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.Recovery != RecoveryRetrySameOperation {
		return journalv2.AcknowledgedPublicationV2{}, err
	}
	observation, err = adapter.CommitV2(ctx, plan)
	if err != nil {
		return journalv2.AcknowledgedPublicationV2{}, err
	}
	return coordinator.ReconcileComplete(plan, observation)
}

func executionPlanMembershipV2(plan journalv2.SealedCommitPlanV2) bool {
	guards := len(plan.EligibilityGuards())
	if plan.BindingCreate() == nil || len(plan.Events()) != 2 || len(plan.Heads()) != 2 || (guards != 0 && guards != 1) || len(plan.Conditions()) != 2+guards || len(plan.EffectIntents()) != 0 || len(plan.Checkpoints()) != 0 {
		return false
	}
	operation, slot := 0, 0
	for _, event := range plan.Events() {
		switch event.Family() {
		case "operation":
			operation++
		case "execution_slot":
			slot++
		default:
			return false
		}
	}
	return operation == 1 && slot == 1
}

func (a *executionAuthorityV2) ResolveExecutionOperation(ctx context.Context, candidate journalv2.OperationBindingV2) (execution.ExecutionOperationResolutionV2, error) {
	if a == nil || a.adapter == nil {
		return execution.ExecutionOperationResolutionV2{}, invalidV2Plan("execution authority reader")
	}
	revision, err := a.adapter.CaptureRevisionV2(ctx)
	if err != nil {
		return execution.ExecutionOperationResolutionV2{}, err
	}
	bindingAddress, err := journalv2.NewOperationBindingAddressV2(candidate.OperationID())
	if err != nil {
		return execution.ExecutionOperationResolutionV2{}, err
	}
	bindingRequest, _ := journalv2.NewReadRequestV2(bindingAddress, revision)
	bindingRecord, err := a.adapter.ReadV2(ctx, bindingRequest)
	if err != nil {
		if journalv2.IsKind(err, journalv2.ErrorNotFound) {
			if verifyErr := a.adapter.VerifyRevisionV2(ctx, revision); verifyErr != nil {
				return execution.ExecutionOperationResolutionV2{}, verifyErr
			}
			return execution.ExecutionOperationResolutionV2{Kind: execution.ExecutionOperationAbsentV2}, nil
		}
		return execution.ExecutionOperationResolutionV2{}, err
	}
	resolution, err := journalv2.ResolveOperationBinding(candidate, bindingRecord.CanonicalValue(), true)
	if resolution == journalv2.BindingConflict {
		return execution.ExecutionOperationResolutionV2{Kind: execution.ExecutionOperationConflictV2}, nil
	}
	if err != nil || resolution != journalv2.BindingExact {
		return execution.ExecutionOperationResolutionV2{}, err
	}
	headAddress, _ := journalv2.NewOperationHeadAddressV2(candidate.OperationID())
	headRequest, _ := journalv2.NewReadRequestV2(headAddress, revision)
	headRecord, err := a.adapter.ReadV2(ctx, headRequest)
	if err != nil {
		return execution.ExecutionOperationResolutionV2{}, invalidV2Plan("bound execution operation missing state")
	}
	state, err := journalv2.DecodeOperationStateV2(headRecord.CanonicalValue())
	if err != nil || state.OperationID() != candidate.OperationID() {
		return execution.ExecutionOperationResolutionV2{}, invalidV2Plan("execution operation state")
	}
	if err := a.adapter.VerifyRevisionV2(ctx, revision); err != nil {
		return execution.ExecutionOperationResolutionV2{}, err
	}
	return execution.ExecutionOperationResolutionV2{Kind: execution.ExecutionOperationExactV2, State: state, RecordRevision: headRecord.RecordRevision()}, nil
}

func (a *executionAuthorityV2) ObserveExecutionEligibility(ctx context.Context, missionID ids.MissionID, shardID journalv2.EligibilityShardID) (workgraph.ObservedEligibilityShardV2, error) {
	if a == nil || a.adapter == nil {
		return workgraph.ObservedEligibilityShardV2{}, invalidV2Plan("execution eligibility reader")
	}
	key, err := v2EligibilityGuardKey(journalv2.MissionID(missionID), shardID)
	if err != nil {
		return workgraph.ObservedEligibilityShardV2{}, err
	}
	requestCtx, cancel := a.adapter.requestContext(ctx)
	defer cancel()
	record, err := a.adapter.client.Get(requestCtx, key.String())
	if err != nil {
		return workgraph.ObservedEligibilityShardV2{}, translateRecoveryError("execution_eligibility", err)
	}
	if record.Revision == 0 || record.ExpiresAt != nil {
		return workgraph.ObservedEligibilityShardV2{}, invalidV2Plan("execution eligibility observation")
	}
	return workgraph.NewObservedEligibilityShardV2(record.Value, uint64(record.Revision))
}

func (a *executionAuthorityV2) ObserveExecutionSlot(ctx context.Context, coordinate journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	if a == nil || a.adapter == nil {
		return execution.ExecutionSlotObservationV2{}, invalidV2Plan("execution slot reader")
	}
	revision, err := a.adapter.CaptureRevisionV2(ctx)
	if err != nil {
		return execution.ExecutionSlotObservationV2{}, err
	}
	address, err := journalv2.NewAggregateHeadAddressV2(coordinate)
	if err != nil {
		return execution.ExecutionSlotObservationV2{}, err
	}
	request, _ := journalv2.NewReadRequestV2(address, revision)
	record, err := a.adapter.ReadV2(ctx, request)
	if err != nil {
		if journalv2.IsKind(err, journalv2.ErrorNotFound) {
			if verifyErr := a.adapter.VerifyRevisionV2(ctx, revision); verifyErr != nil {
				return execution.ExecutionSlotObservationV2{}, verifyErr
			}
			return execution.ExecutionSlotObservationV2{}, nil
		}
		return execution.ExecutionSlotObservationV2{}, err
	}
	head, err := journalv2.DecodeAggregateHeadV2(record.CanonicalValue())
	if err != nil || head.Coordinate() != coordinate || head.Tombstone() {
		return execution.ExecutionSlotObservationV2{}, invalidV2Plan("execution slot head")
	}
	state, err := execution.DecodeExecutionSlotStateV2(head.AuthoritySummary())
	if err != nil {
		return execution.ExecutionSlotObservationV2{}, err
	}
	digest, _ := journalv2.ReducerStateDigestV2(state.CanonicalBytes())
	if digest != head.CanonicalStateDigest() || head.LogicalVersion() != journalv2.LogicalVersion(state.LogicalVersion) || head.CommittedRuntimeEpochID() != state.RuntimeEpochID || head.LastEventID() == "" || head.LastPublicationID() == "" {
		return execution.ExecutionSlotObservationV2{}, invalidV2Plan("execution slot digest")
	}
	if err := a.adapter.VerifyRevisionV2(ctx, revision); err != nil {
		return execution.ExecutionSlotObservationV2{}, err
	}
	return execution.ExecutionSlotObservationV2{State: &state, RecordRevision: record.RecordRevision(), LastPublicationID: head.LastPublicationID()}, nil
}

var _ execution.ExecutionAuthorityPortV2 = (*executionAuthorityV2)(nil)
