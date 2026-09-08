package gapdb

import (
	"context"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
)

type effectAuthorityV2 struct {
	adapter     *Adapter
	publisher   executionPublicationPortV2
	coordinator *journalv2.PublicationCoordinatorV2
}

func newEffectAuthorityV2(adapter *Adapter, coordinator *journalv2.PublicationCoordinatorV2) (*effectAuthorityV2, error) {
	if adapter == nil || coordinator == nil {
		return nil, invalidV2Plan("effect authority dependencies")
	}
	return &effectAuthorityV2{adapter: adapter, publisher: coordinator, coordinator: coordinator}, nil
}

// EffectAuthorityPortV2 exposes only the effect capability contract.
func (a *Adapter) EffectAuthorityPortV2(coordinator *journalv2.PublicationCoordinatorV2) (execution.EffectAuthorityPortV2, error) {
	return newEffectAuthorityV2(a, coordinator)
}
func newEffectAuthorityV2ForTest(publisher executionPublicationPortV2) *effectAuthorityV2 {
	return &effectAuthorityV2{publisher: publisher}
}

func (a *effectAuthorityV2) PublishEffect(ctx context.Context, capability execution.EffectAuthorityPlanV2) (execution.EffectPublicationAckV2, error) {
	if a == nil || a.publisher == nil {
		return execution.EffectPublicationAckV2{}, invalidV2Plan("effect authority plan")
	}
	plan, err := capability.Publication()
	if err != nil || capability.ValidatePublicationV2() != nil || !effectPlanMembershipV2(plan) {
		return execution.EffectPublicationAckV2{}, invalidV2Plan("effect authority plan")
	}
	ack, err := a.publisher.Publish(ctx, plan)
	if err != nil {
		if a.adapter == nil || a.coordinator == nil || !isAuthorityDeliveryFailureV2(err) {
			return execution.EffectPublicationAckV2{}, err
		}
		ack, err = reconcileRegisteredPublicationV2(ctx, a.adapter, a.coordinator, plan)
		if err != nil {
			return execution.EffectPublicationAckV2{}, err
		}
	}
	return execution.NewEffectPublicationAckV2(capability, ack)
}

func effectPlanMembershipV2(plan journalv2.SealedCommitPlanV2) bool {
	if len(plan.Events()) != 3 || len(plan.Heads()) != 3 || len(plan.Conditions()) != 3 || len(plan.EligibilityGuards()) != 0 || len(plan.Checkpoints()) != 0 {
		return false
	}
	if plan.BindingCreate() != nil {
		if len(plan.EffectIntents()) != 1 {
			return false
		}
	} else if len(plan.EffectIntents()) != 0 {
		return false
	}
	op, effect, executionSlot := 0, 0, 0
	for _, event := range plan.Events() {
		switch event.Family() {
		case "operation":
			op++
		case "external_effect":
			effect++
		case "execution_slot":
			executionSlot++
		default:
			return false
		}
	}
	if op != 1 || effect != 1 || executionSlot != 1 {
		return false
	}
	return true
}

func (a *effectAuthorityV2) ResolveEffectOperation(ctx context.Context, candidate journalv2.OperationBindingV2) (execution.EffectOperationResolutionV2, error) {
	if a == nil || a.adapter == nil {
		return execution.EffectOperationResolutionV2{}, invalidV2Plan("effect operation reader")
	}
	revision, err := a.adapter.CaptureRevisionV2(ctx)
	if err != nil {
		return execution.EffectOperationResolutionV2{}, err
	}
	bindingAddress, _ := journalv2.NewOperationBindingAddressV2(candidate.OperationID())
	bindingRequest, _ := journalv2.NewReadRequestV2(bindingAddress, revision)
	bindingRecord, err := a.adapter.ReadV2(ctx, bindingRequest)
	if err != nil {
		if journalv2.IsKind(err, journalv2.ErrorNotFound) {
			if verifyErr := a.adapter.VerifyRevisionV2(ctx, revision); verifyErr != nil {
				return execution.EffectOperationResolutionV2{}, verifyErr
			}
			return execution.EffectOperationResolutionV2{Kind: execution.ExecutionOperationAbsentV2}, nil
		}
		return execution.EffectOperationResolutionV2{}, err
	}
	resolution, err := journalv2.ResolveOperationBinding(candidate, bindingRecord.CanonicalValue(), true)
	if resolution == journalv2.BindingConflict {
		return execution.EffectOperationResolutionV2{Kind: execution.ExecutionOperationConflictV2}, nil
	}
	if err != nil || resolution != journalv2.BindingExact {
		return execution.EffectOperationResolutionV2{}, invalidV2Plan("effect operation binding")
	}
	headAddress, _ := journalv2.NewOperationHeadAddressV2(candidate.OperationID())
	headRequest, _ := journalv2.NewReadRequestV2(headAddress, revision)
	headRecord, err := a.adapter.ReadV2(ctx, headRequest)
	if err != nil {
		return execution.EffectOperationResolutionV2{}, invalidV2Plan("bound effect operation missing state")
	}
	state, err := journalv2.DecodeOperationStateV2(headRecord.CanonicalValue())
	if err != nil || state.OperationID() != candidate.OperationID() {
		return execution.EffectOperationResolutionV2{}, invalidV2Plan("effect operation state")
	}
	if err := a.adapter.VerifyRevisionV2(ctx, revision); err != nil {
		return execution.EffectOperationResolutionV2{}, err
	}
	return execution.EffectOperationResolutionV2{Kind: execution.ExecutionOperationExactV2, State: state, RecordRevision: headRecord.RecordRevision()}, nil
}

func (a *effectAuthorityV2) ObserveEffectState(ctx context.Context, coordinate journalv2.AggregateCoordinate) (execution.EffectStateObservationV2, error) {
	if a == nil || a.adapter == nil {
		return execution.EffectStateObservationV2{}, invalidV2Plan("effect state reader")
	}
	revision, err := a.adapter.CaptureRevisionV2(ctx)
	if err != nil {
		return execution.EffectStateObservationV2{}, err
	}
	address, _ := journalv2.NewAggregateHeadAddressV2(coordinate)
	request, _ := journalv2.NewReadRequestV2(address, revision)
	record, err := a.adapter.ReadV2(ctx, request)
	if err != nil {
		if journalv2.IsKind(err, journalv2.ErrorNotFound) {
			if verifyErr := a.adapter.VerifyRevisionV2(ctx, revision); verifyErr != nil {
				return execution.EffectStateObservationV2{}, verifyErr
			}
			return execution.EffectStateObservationV2{}, nil
		}
		return execution.EffectStateObservationV2{}, err
	}
	head, err := journalv2.DecodeAggregateHeadV2(record.CanonicalValue())
	if err != nil || head.Coordinate() != coordinate || head.Tombstone() {
		return execution.EffectStateObservationV2{}, invalidV2Plan("effect head")
	}
	state, err := execution.DecodeExternalEffectStateV2(head.AuthoritySummary())
	if err != nil {
		return execution.EffectStateObservationV2{}, err
	}
	digest, _ := journalv2.ReducerStateDigestV2(mustEffectStateBytesV2(state))
	if digest != head.CanonicalStateDigest() || head.LogicalVersion() != journalv2.LogicalVersion(state.LogicalVersion) || head.CommittedRuntimeEpochID() != state.RuntimeEpochID || head.LastEventID() == "" || head.LastPublicationID() == "" {
		return execution.EffectStateObservationV2{}, invalidV2Plan("effect head coherence")
	}
	if err := a.adapter.VerifyRevisionV2(ctx, revision); err != nil {
		return execution.EffectStateObservationV2{}, err
	}
	return execution.EffectStateObservationV2{State: &state, RecordRevision: record.RecordRevision(), LastPublicationID: head.LastPublicationID()}, nil
}

func mustEffectStateBytesV2(state execution.ExternalEffectStateV2) []byte {
	encoded, _ := jsonMarshalEffectStateV2(state)
	return encoded
}

// jsonMarshalEffectStateV2 deliberately round-trips through the public codec;
// the adapter neither knows nor reconstructs execution's private wire.
func jsonMarshalEffectStateV2(state execution.ExternalEffectStateV2) ([]byte, error) {
	// CanonicalResultBytesV2 is the capability-owned encoding surface.
	return state.CanonicalResultBytesV2()
}

func (a *effectAuthorityV2) ObserveEffectExecution(ctx context.Context, coordinate journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	return (&executionAuthorityV2{adapter: a.adapter}).ObserveExecutionSlot(ctx, coordinate)
}

var _ execution.EffectAuthorityPortV2 = (*effectAuthorityV2)(nil)
