package gapdb

import (
	"context"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

// acceptanceAuthorityV2 is deliberately a thin provider adapter. The
// integration package owns readiness policy; GapDB only supplies exact heads,
// compare-before-publish assertions, and durable publication.
type acceptanceAuthorityV2 struct {
	adapter     *Adapter
	coordinator *journalv2.PublicationCoordinatorV2
	review      *reviewAuthorityV2
}

var _ integration.AcceptanceAuthorityPortV2 = (*acceptanceAuthorityV2)(nil)

func (a *Adapter) AcceptanceAuthorityPortV2(coordinator *journalv2.PublicationCoordinatorV2, eventRegistry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2, hostAuthorityID journalv2.HostAuthorityID, budgets restore.BudgetsV2) (integration.AcceptanceAuthorityPortV2, error) {
	if a == nil || coordinator == nil {
		return nil, invalidV2Plan("acceptance authority dependencies")
	}
	base, err := a.ReviewAuthorityPortV2(coordinator, eventRegistry, reducers, hostAuthorityID, budgets)
	if err != nil {
		return nil, err
	}
	r, ok := base.(*reviewAuthorityV2)
	if !ok {
		return nil, invalidV2Plan("acceptance review dependency")
	}
	return &acceptanceAuthorityV2{adapter: a, coordinator: coordinator, review: r}, nil
}

func (a *Adapter) AcceptanceAuthorityServiceV2(coordinator *journalv2.PublicationCoordinatorV2, eventRegistry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2, hostAuthorityID journalv2.HostAuthorityID, budgets restore.BudgetsV2, epochs integration.AcceptanceEpochSourceV2) (*integration.AcceptanceAuthorityServiceV2, error) {
	port, err := a.AcceptanceAuthorityPortV2(coordinator, eventRegistry, reducers, hostAuthorityID, budgets)
	if err != nil {
		return nil, err
	}
	return integration.NewAcceptanceAuthorityServiceV2(port, epochs, eventRegistry)
}

func (a *acceptanceAuthorityV2) ResolveAcceptanceOperation(ctx context.Context, candidate journalv2.OperationBindingV2) (integration.AcceptanceOperationResolutionV2, error) {
	resolved, err := resolveAuthorityOperationV2(ctx, a.adapter, candidate)
	if err != nil {
		return integration.AcceptanceOperationResolutionV2{}, err
	}
	return integration.AcceptanceOperationResolutionV2{Kind: integration.AcceptanceOperationResolutionKindV2(resolved.kind), State: resolved.state, RecordRevision: resolved.recordRevision}, nil
}

func (a *acceptanceAuthorityV2) ObserveAcceptance(ctx context.Context, coordinate journalv2.AggregateCoordinate) (integration.AcceptanceObservationV2, error) {
	head, record, err := readAuthorityHeadV2(ctx, a.adapter, coordinate)
	if err != nil {
		return integration.AcceptanceObservationV2{}, err
	}
	state, err := integration.DecodeAcceptanceStateV2(head.AuthoritySummary())
	if err != nil || journalv2.LogicalVersion(state.LogicalVersion) != head.LogicalVersion() {
		return integration.AcceptanceObservationV2{}, invalidV2Plan("acceptance authority summary")
	}
	digest, _ := journalv2.ReducerStateDigestV2(state.CanonicalBytes())
	if digest != head.CanonicalStateDigest() {
		return integration.AcceptanceObservationV2{}, invalidV2Plan("acceptance authority digest")
	}
	return integration.AcceptanceObservationV2{State: &state, RecordRevision: record.RecordRevision(), LastPublicationID: head.LastPublicationID()}, nil
}

func (a *acceptanceAuthorityV2) ObserveAcceptanceAuthority(ctx context.Context, request integration.AcceptanceAuthorityRequestV2) (integration.AcceptanceAuthorityObservationV2, error) {
	shardID, err := workgraph.NewEligibilityPolicy().ShardFor(request.MissionID, request.WorkID)
	if err != nil {
		return integration.AcceptanceAuthorityObservationV2{}, err
	}
	eligibility, err := a.review.handoff.execution.ObserveExecutionEligibility(ctx, request.MissionID, shardID)
	if err != nil {
		return integration.AcceptanceAuthorityObservationV2{}, err
	}

	handoffCoordinate, err := handoff.CoordinateV2(request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID)
	if err != nil {
		return integration.AcceptanceAuthorityObservationV2{}, err
	}
	handoffObservation, err := a.review.handoff.ObserveHandoff(ctx, handoffCoordinate)
	if err != nil {
		return integration.AcceptanceAuthorityObservationV2{}, err
	}
	handoffAssertion, revision, err := observeHeadAssertionV2(ctx, a.adapter, handoffCoordinate)
	if err != nil || revision != handoffObservation.RecordRevision {
		return integration.AcceptanceAuthorityObservationV2{}, invalidV2Plan("acceptance handoff assertion")
	}
	result := integration.AcceptanceAuthorityObservationV2{
		Eligibility: eligibility,
		Handoff:     handoffObservation,
		Assertions:  []journalv2.AuthorityAssertionV2{handoffAssertion},
	}

	reviewIDs := append([]ids.ReviewID(nil), request.ReviewIDs...)
	for _, reviewID := range reviewIDs {
		coordinate, coordinateErr := review.CoordinateV2(request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID, reviewID)
		if coordinateErr != nil {
			return integration.AcceptanceAuthorityObservationV2{}, coordinateErr
		}
		observation, observeErr := a.review.ObserveReview(ctx, coordinate)
		if observeErr != nil {
			return integration.AcceptanceAuthorityObservationV2{}, observeErr
		}
		assertion, assertionRevision, assertionErr := observeHeadAssertionV2(ctx, a.adapter, coordinate)
		if assertionErr != nil || assertionRevision != observation.RecordRevision {
			return integration.AcceptanceAuthorityObservationV2{}, invalidV2Plan("acceptance review assertion")
		}
		result.Reviews = append(result.Reviews, observation)
		result.Assertions = append(result.Assertions, assertion)
	}

	for _, expected := range request.Evidence {
		if expected.Disposition != integration.EvidenceSatisfiedV2 {
			continue
		}
		head, record, readErr := readAuthorityHeadV2(ctx, a.adapter, expected.Coordinate)
		if readErr != nil {
			return integration.AcceptanceAuthorityObservationV2{}, readErr
		}
		assertion, assertionRevision, assertionErr := observeHeadAssertionV2(ctx, a.adapter, expected.Coordinate)
		if assertionErr != nil || assertionRevision != record.RecordRevision() {
			return integration.AcceptanceAuthorityObservationV2{}, invalidV2Plan("acceptance evidence assertion")
		}
		actual := expected
		actual.LogicalVersion = head.LogicalVersion()
		actual.RecordRevision = record.RecordRevision()
		actual.StateDigestSHA256 = head.CanonicalStateDigest().String()
		result.Evidence = append(result.Evidence, integration.AcceptanceEvidenceObservationV2{Evidence: actual, Assertion: assertion})
		result.Assertions = append(result.Assertions, assertion)
	}
	return result, nil
}

func (a *acceptanceAuthorityV2) PublishAcceptance(ctx context.Context, capability integration.AcceptancePlanV2) (integration.AcceptanceAckV2, error) {
	plan, err := capability.Publication()
	if err != nil || !acceptanceTransportMembershipV2(plan) {
		return integration.AcceptanceAckV2{}, invalidV2Plan("acceptance authority plan")
	}
	ack, err := publishAuthorityPlanV2(ctx, a.adapter, a.coordinator, plan)
	if err != nil {
		return integration.AcceptanceAckV2{}, err
	}
	return integration.AcceptanceAckV2{PublicationID: ack.PublicationID(), ProviderRevision: ack.ProviderRevision(), PlanDigest: ack.PlanDigest(), MutationCount: ack.MutationCount()}, nil
}

func (a *acceptanceAuthorityV2) ReconcileAcceptance(ctx context.Context, capability integration.AcceptancePlanV2) (integration.AcceptanceAckV2, error) {
	plan, err := capability.Publication()
	if err != nil || !acceptanceTransportMembershipV2(plan) {
		return integration.AcceptanceAckV2{}, invalidV2Plan("acceptance reconciliation plan")
	}
	ack, err := reconcileObservedAuthorityPublicationV2(ctx, a.adapter, a.coordinator, plan)
	if err != nil {
		return integration.AcceptanceAckV2{}, err
	}
	return integration.AcceptanceAckV2{PublicationID: ack.PublicationID(), ProviderRevision: ack.ProviderRevision(), PlanDigest: ack.PlanDigest(), MutationCount: ack.MutationCount()}, nil
}

func acceptanceTransportMembershipV2(plan journalv2.SealedCommitPlanV2) bool {
	assertions := len(plan.Assertions())
	return assertions >= 2 && authorityTransportMembershipV2(plan, "acceptance", 2, 2, 3, assertions, 1)
}
