package gapdb

import (
	"context"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controlstore"
)

type outboxAuthorityV2 struct {
	adapter     *Adapter
	publisher   executionPublicationPortV2
	coordinator *journalv2.PublicationCoordinatorV2
}

func newOutboxAuthorityV2(adapter *Adapter, coordinator *journalv2.PublicationCoordinatorV2) (*outboxAuthorityV2, error) {
	if adapter == nil || coordinator == nil {
		return nil, invalidV2Plan("outbox authority dependencies")
	}
	return &outboxAuthorityV2{adapter: adapter, publisher: coordinator, coordinator: coordinator}, nil
}

// OutboxAuthorityPortV2 exposes the direct provider capability without
// inventing a Host route for outbox. The returned port accepts only the exact
// sealed outbox plan and retains the raw GapDB adapter inside this package.
func (a *Adapter) OutboxAuthorityPortV2(coordinator *journalv2.PublicationCoordinatorV2) (controlstore.OutboxAuthorityPortV2, error) {
	return newOutboxAuthorityV2(a, coordinator)
}

func newOutboxAuthorityV2ForTest(publisher executionPublicationPortV2) *outboxAuthorityV2 {
	return &outboxAuthorityV2{publisher: publisher}
}

func (a *outboxAuthorityV2) PublishOutbox(ctx context.Context, capability controlstore.OutboxAuthorityPlanV2) (controlstore.OutboxPublicationAckV2, error) {
	if a == nil || a.publisher == nil {
		return controlstore.OutboxPublicationAckV2{}, invalidV2Plan("outbox authority plan")
	}
	plan, err := capability.Publication()
	if err != nil || !outboxTransportMembershipV2(plan) {
		return controlstore.OutboxPublicationAckV2{}, invalidV2Plan("outbox authority plan")
	}
	ack, err := a.publisher.Publish(ctx, plan)
	if err != nil {
		if a.adapter == nil || a.coordinator == nil || !isAuthorityDeliveryFailureV2(err) {
			return controlstore.OutboxPublicationAckV2{}, err
		}
		ack, err = reconcileRegisteredPublicationV2(ctx, a.adapter, a.coordinator, plan)
		if err != nil {
			return controlstore.OutboxPublicationAckV2{}, err
		}
	}
	return controlstore.NewOutboxPublicationAckV2(capability, ack)
}

func outboxTransportMembershipV2(plan journalv2.SealedCommitPlanV2) bool {
	binding := plan.BindingCreate()
	expectedAssertions := 0
	if binding != nil && binding.CommandKind() == "outbox.start_attempt" {
		expectedAssertions = 1
	}
	if binding == nil || binding.CommandSchemaVersion() != 1 || len(binding.CommandKind()) <= len("outbox.") || binding.CommandKind()[:len("outbox.")] != "outbox." ||
		plan.OperationState().OperationID() != binding.OperationID() || plan.OperationState().Status() != journalv2.StatusResolved ||
		len(plan.EffectIntents()) != 0 || len(plan.EligibilityGuards()) != 0 || len(plan.Checkpoints()) != 0 || len(plan.Assertions()) != expectedAssertions || plan.WorkspacePathClaim() != nil || len(plan.Events()) != 2 || len(plan.Heads()) != 2 || len(plan.Conditions()) != 2 {
		return false
	}
	if expectedAssertions == 1 {
		assertion := plan.Assertions()[0]
		if assertion.Kind() != journalv2.ConditionRecordRevision || assertion.ObservedRevision() == 0 || assertion.ExpectedLogicalVersion() == 0 || assertion.ObservedValueDigest().IsZero() || assertion.Target().Kind != journalv2.ConditionTargetAggregateHead || assertion.Target().Coordinate.Kind != "external_effect" {
			return false
		}
	}
	families := map[string]int{}
	for _, event := range plan.Events() {
		families[event.Family()]++
	}
	return families["operation"] == 1 && families["outbox"] == 1 && len(families) == 2
}

func (a *outboxAuthorityV2) ResolveOutboxOperation(ctx context.Context, candidate journalv2.OperationBindingV2) (controlstore.OutboxOperationResolutionV2, error) {
	if a == nil || a.adapter == nil {
		return controlstore.OutboxOperationResolutionV2{}, invalidV2Plan("outbox operation reader")
	}
	revision, err := a.adapter.CaptureRevisionV2(ctx)
	if err != nil {
		return controlstore.OutboxOperationResolutionV2{}, err
	}
	bindingAddress, _ := journalv2.NewOperationBindingAddressV2(candidate.OperationID())
	bindingRequest, _ := journalv2.NewReadRequestV2(bindingAddress, revision)
	bindingRecord, err := a.adapter.ReadV2(ctx, bindingRequest)
	if err != nil {
		if journalv2.IsKind(err, journalv2.ErrorNotFound) {
			if verifyErr := a.adapter.VerifyRevisionV2(ctx, revision); verifyErr != nil {
				return controlstore.OutboxOperationResolutionV2{}, verifyErr
			}
			return controlstore.OutboxOperationResolutionV2{Kind: controlstore.OutboxOperationAbsentV2}, nil
		}
		return controlstore.OutboxOperationResolutionV2{}, err
	}
	resolution, err := journalv2.ResolveOperationBinding(candidate, bindingRecord.CanonicalValue(), true)
	if resolution == journalv2.BindingConflict {
		return controlstore.OutboxOperationResolutionV2{Kind: controlstore.OutboxOperationConflictV2}, nil
	}
	if err != nil || resolution != journalv2.BindingExact {
		return controlstore.OutboxOperationResolutionV2{}, invalidV2Plan("outbox operation binding")
	}
	headAddress, _ := journalv2.NewOperationHeadAddressV2(candidate.OperationID())
	headRequest, _ := journalv2.NewReadRequestV2(headAddress, revision)
	headRecord, err := a.adapter.ReadV2(ctx, headRequest)
	if err != nil {
		return controlstore.OutboxOperationResolutionV2{}, invalidV2Plan("bound outbox operation missing state")
	}
	state, err := journalv2.DecodeOperationStateV2(headRecord.CanonicalValue())
	if err != nil || state.OperationID() != candidate.OperationID() {
		return controlstore.OutboxOperationResolutionV2{}, invalidV2Plan("outbox operation state")
	}
	if err := a.adapter.VerifyRevisionV2(ctx, revision); err != nil {
		return controlstore.OutboxOperationResolutionV2{}, err
	}
	return controlstore.OutboxOperationResolutionV2{Kind: controlstore.OutboxOperationExactV2, State: state, RecordRevision: headRecord.RecordRevision()}, nil
}

func (a *outboxAuthorityV2) ObserveOutboxState(ctx context.Context, coordinate journalv2.AggregateCoordinate) (controlstore.OutboxStateObservationV2, error) {
	if a == nil || a.adapter == nil {
		return controlstore.OutboxStateObservationV2{}, invalidV2Plan("outbox state reader")
	}
	revision, err := a.adapter.CaptureRevisionV2(ctx)
	if err != nil {
		return controlstore.OutboxStateObservationV2{}, err
	}
	address, addressErr := journalv2.NewAggregateHeadAddressV2(coordinate)
	if addressErr != nil || coordinate.Kind != "outbox" {
		return controlstore.OutboxStateObservationV2{}, invalidV2Plan("outbox coordinate")
	}
	request, _ := journalv2.NewReadRequestV2(address, revision)
	record, err := a.adapter.ReadV2(ctx, request)
	if err != nil {
		if journalv2.IsKind(err, journalv2.ErrorNotFound) {
			if verifyErr := a.adapter.VerifyRevisionV2(ctx, revision); verifyErr != nil {
				return controlstore.OutboxStateObservationV2{}, verifyErr
			}
			return controlstore.OutboxStateObservationV2{}, nil
		}
		return controlstore.OutboxStateObservationV2{}, err
	}
	head, err := journalv2.DecodeAggregateHeadV2(record.CanonicalValue())
	if err != nil || head.Coordinate() != coordinate || head.Tombstone() {
		return controlstore.OutboxStateObservationV2{}, invalidV2Plan("outbox head")
	}
	state, err := controlstore.DecodeOutboxDeliveryStateV2(head.AuthoritySummary())
	stateBytes := state.CanonicalBytes()
	digest, digestErr := journalv2.ReducerStateDigestV2(stateBytes)
	if err != nil || digestErr != nil || digest != head.CanonicalStateDigest() || head.LogicalVersion() != journalv2.LogicalVersion(state.LogicalVersion) || head.LastEventID() == "" || head.LastPublicationID() == "" {
		return controlstore.OutboxStateObservationV2{}, invalidV2Plan("outbox head coherence")
	}
	if err := a.adapter.VerifyRevisionV2(ctx, revision); err != nil {
		return controlstore.OutboxStateObservationV2{}, err
	}
	return controlstore.OutboxStateObservationV2{State: &state, RecordRevision: record.RecordRevision(), LastPublicationID: head.LastPublicationID()}, nil
}

var _ controlstore.OutboxAuthorityPortV2 = (*outboxAuthorityV2)(nil)
