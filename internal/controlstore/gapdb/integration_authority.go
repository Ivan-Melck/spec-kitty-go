package gapdb

import (
	"context"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type integrationAuthorityV2 struct {
	adapter     *Adapter
	coordinator *journalv2.PublicationCoordinatorV2
	acceptance  *acceptanceAuthorityV2
}

var _ integration.IntegrationAuthorityPortV2 = (*integrationAuthorityV2)(nil)

func (a *Adapter) IntegrationAuthorityPortV2(coordinator *journalv2.PublicationCoordinatorV2, eventRegistry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2, hostAuthorityID journalv2.HostAuthorityID, budgets restore.BudgetsV2) (integration.IntegrationAuthorityPortV2, error) {
	base, err := a.AcceptanceAuthorityPortV2(coordinator, eventRegistry, reducers, hostAuthorityID, budgets)
	if err != nil {
		return nil, err
	}
	acceptance, ok := base.(*acceptanceAuthorityV2)
	if !ok {
		return nil, invalidV2Plan("integration acceptance dependency")
	}
	return &integrationAuthorityV2{adapter: a, coordinator: coordinator, acceptance: acceptance}, nil
}

func (a *Adapter) IntegrationAuthorityServiceV2(coordinator *journalv2.PublicationCoordinatorV2, eventRegistry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2, hostAuthorityID journalv2.HostAuthorityID, budgets restore.BudgetsV2, epochs integration.AcceptanceEpochSourceV2, git integration.IntegrationGitPortV2) (*integration.IntegrationAuthorityServiceV2, error) {
	port, err := a.IntegrationAuthorityPortV2(coordinator, eventRegistry, reducers, hostAuthorityID, budgets)
	if err != nil {
		return nil, err
	}
	return integration.NewIntegrationAuthorityServiceV2(port, epochs, git, eventRegistry)
}

func (a *integrationAuthorityV2) ResolveIntegrationOperation(ctx context.Context, candidate journalv2.OperationBindingV2) (integration.IntegrationOperationResolutionV2, error) {
	resolved, err := resolveAuthorityOperationV2(ctx, a.adapter, candidate)
	if err != nil {
		return integration.IntegrationOperationResolutionV2{}, err
	}
	return integration.IntegrationOperationResolutionV2{Kind: integration.AcceptanceOperationResolutionKindV2(resolved.kind), State: resolved.state, RecordRevision: resolved.recordRevision}, nil
}

func (a *integrationAuthorityV2) ObserveIntegration(ctx context.Context, targetCoordinate, attemptCoordinate journalv2.AggregateCoordinate) (integration.IntegrationObservationV2, error) {
	targetHead, targetRecord, err := readAuthorityHeadV2(ctx, a.adapter, targetCoordinate)
	if err != nil {
		if !journalv2.IsKind(err, journalv2.ErrorNotFound) {
			return integration.IntegrationObservationV2{}, err
		}
	} else {
		target, decodeErr := integration.DecodeIntegrationTargetStateV2(targetHead.AuthoritySummary())
		if decodeErr != nil || journalv2.LogicalVersion(target.LogicalVersion) != targetHead.LogicalVersion() {
			return integration.IntegrationObservationV2{}, invalidV2Plan("integration target summary")
		}
		targetDigest, _ := journalv2.ReducerStateDigestV2(target.CanonicalBytes())
		if targetDigest != targetHead.CanonicalStateDigest() {
			return integration.IntegrationObservationV2{}, invalidV2Plan("integration target digest")
		}
		result := integration.IntegrationObservationV2{Target: &target, TargetRevision: targetRecord.RecordRevision()}
		attemptHead, attemptRecord, attemptErr := readAuthorityHeadV2(ctx, a.adapter, attemptCoordinate)
		if attemptErr != nil {
			if journalv2.IsKind(attemptErr, journalv2.ErrorNotFound) {
				return result, nil
			}
			return integration.IntegrationObservationV2{}, attemptErr
		}
		attempt, decodeErr := integration.DecodeIntegrationAttemptStateV2(attemptHead.AuthoritySummary())
		if decodeErr != nil || journalv2.LogicalVersion(attempt.LogicalVersion) != attemptHead.LogicalVersion() {
			return integration.IntegrationObservationV2{}, invalidV2Plan("integration attempt summary")
		}
		attemptDigest, _ := journalv2.ReducerStateDigestV2(attempt.CanonicalBytes())
		if attemptDigest != attemptHead.CanonicalStateDigest() {
			return integration.IntegrationObservationV2{}, invalidV2Plan("integration attempt digest")
		}
		result.Attempt, result.AttemptRevision = &attempt, attemptRecord.RecordRevision()
		return result, nil
	}
	attemptHead, attemptRecord, err := readAuthorityHeadV2(ctx, a.adapter, attemptCoordinate)
	if err != nil {
		if journalv2.IsKind(err, journalv2.ErrorNotFound) {
			return integration.IntegrationObservationV2{}, nil
		}
		return integration.IntegrationObservationV2{}, err
	}
	attempt, err := integration.DecodeIntegrationAttemptStateV2(attemptHead.AuthoritySummary())
	if err != nil || journalv2.LogicalVersion(attempt.LogicalVersion) != attemptHead.LogicalVersion() {
		return integration.IntegrationObservationV2{}, invalidV2Plan("integration attempt summary")
	}
	attemptDigest, _ := journalv2.ReducerStateDigestV2(attempt.CanonicalBytes())
	if attemptDigest != attemptHead.CanonicalStateDigest() {
		return integration.IntegrationObservationV2{}, invalidV2Plan("integration attempt digest")
	}
	_ = attempt
	_ = attemptRecord
	return integration.IntegrationObservationV2{}, invalidV2Plan("integration attempt without target")
}

func (a *integrationAuthorityV2) ObserveIntegrationAuthority(ctx context.Context, request integration.IntegrationAuthorityRequestV2) (integration.IntegrationAuthorityObservationV2, error) {
	shardID, err := workgraph.NewEligibilityPolicy().ShardFor(request.MissionID, request.WorkID)
	if err != nil {
		return integration.IntegrationAuthorityObservationV2{}, err
	}
	eligibility, err := a.acceptance.review.handoff.execution.ObserveExecutionEligibility(ctx, request.MissionID, shardID)
	if err != nil {
		return integration.IntegrationAuthorityObservationV2{}, err
	}
	coordinate, err := journalv2.NewAggregateCoordinate("acceptance", journalv2.AggregateID(request.AcceptanceID), journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: journalv2.ProjectID(request.ProjectID), MissionID: journalv2.MissionID(request.MissionID), WorkRevisionID: journalv2.WorkRevisionID(request.WorkRevisionID), WorkID: journalv2.WorkID(request.WorkID)})
	if err != nil {
		return integration.IntegrationAuthorityObservationV2{}, err
	}
	accepted, err := a.acceptance.ObserveAcceptance(ctx, coordinate)
	if err != nil {
		return integration.IntegrationAuthorityObservationV2{}, err
	}
	assertion, revision, err := observeHeadAssertionV2(ctx, a.adapter, coordinate)
	if err != nil || revision != accepted.RecordRevision {
		return integration.IntegrationAuthorityObservationV2{}, invalidV2Plan("integration acceptance assertion")
	}
	return integration.IntegrationAuthorityObservationV2{Acceptance: accepted, Eligibility: eligibility, Assertions: []journalv2.AuthorityAssertionV2{assertion}}, nil
}

func (a *integrationAuthorityV2) PublishIntegration(ctx context.Context, capability integration.IntegrationPlanV2) (integration.IntegrationAckV2, error) {
	plan, err := capability.Publication()
	if err != nil || !integrationTransportMembershipV2(plan) {
		return integration.IntegrationAckV2{}, invalidV2Plan("integration authority plan")
	}
	ack, err := publishAuthorityPlanV2(ctx, a.adapter, a.coordinator, plan)
	if err != nil {
		return integration.IntegrationAckV2{}, err
	}
	return integration.IntegrationAckV2{PublicationID: ack.PublicationID(), ProviderRevision: ack.ProviderRevision(), PlanDigest: ack.PlanDigest(), MutationCount: ack.MutationCount()}, nil
}

func (a *integrationAuthorityV2) ReconcileIntegration(ctx context.Context, capability integration.IntegrationPlanV2) (integration.IntegrationAckV2, error) {
	plan, err := capability.Publication()
	if err != nil || !integrationTransportMembershipV2(plan) {
		return integration.IntegrationAckV2{}, invalidV2Plan("integration reconciliation plan")
	}
	ack, err := reconcileObservedAuthorityPublicationV2(ctx, a.adapter, a.coordinator, plan)
	if err != nil {
		return integration.IntegrationAckV2{}, err
	}
	return integration.IntegrationAckV2{PublicationID: ack.PublicationID(), ProviderRevision: ack.ProviderRevision(), PlanDigest: ack.PlanDigest(), MutationCount: ack.MutationCount()}, nil
}

func integrationTransportMembershipV2(plan journalv2.SealedCommitPlanV2) bool {
	events, heads := plan.Events(), plan.Heads()
	if len(events) != len(heads) || (len(events) != 2 && len(events) != 3) || len(plan.Conditions()) != len(heads)+1 || len(plan.EligibilityGuards()) != 1 || len(plan.Assertions()) != 1 || len(plan.Checkpoints()) != 0 || len(plan.EffectIntents()) > 2 {
		return false
	}
	families := map[string]int{}
	for _, event := range events {
		families[event.Family()]++
	}
	if families["operation"] != 1 || families["integration_attempt"] != 1 {
		return false
	}
	if len(events) == 3 {
		return families["integration_target"] == 1 && len(families) == 3
	}
	return len(families) == 2
}

var _ = ids.RepositoryID("")
