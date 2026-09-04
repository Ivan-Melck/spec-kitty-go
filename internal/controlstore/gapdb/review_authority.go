package gapdb

import (
	"context"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/review"
)

type reviewAuthorityV2 struct {
	adapter     *Adapter
	coordinator *journalv2.PublicationCoordinatorV2
	handoff     *handoffAuthorityV2
}

var _ review.AuthorityPortV2 = (*reviewAuthorityV2)(nil)

func (a *Adapter) ReviewAuthorityPortV2(coordinator *journalv2.PublicationCoordinatorV2, eventRegistry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2, hostAuthorityID journalv2.HostAuthorityID, budgets restore.BudgetsV2) (review.AuthorityPortV2, error) {
	base, err := a.HandoffAuthorityPortV2(coordinator, eventRegistry, reducers, hostAuthorityID, budgets)
	if err != nil {
		return nil, err
	}
	h, ok := base.(*handoffAuthorityV2)
	if !ok {
		return nil, invalidV2Plan("review handoff dependency")
	}
	return &reviewAuthorityV2{a, coordinator, h}, nil
}

func (a *reviewAuthorityV2) ResolveReviewOperation(ctx context.Context, candidate journalv2.OperationBindingV2) (review.OperationResolutionV2, error) {
	resolved, err := resolveAuthorityOperationV2(ctx, a.adapter, candidate)
	if err != nil {
		return review.OperationResolutionV2{}, err
	}
	return review.OperationResolutionV2{Kind: review.OperationResolutionKindV2(resolved.kind), State: resolved.state, RecordRevision: resolved.recordRevision}, nil
}

func (a *reviewAuthorityV2) ObserveReview(ctx context.Context, coordinate journalv2.AggregateCoordinate) (review.ObservationV2, error) {
	head, record, err := readAuthorityHeadV2(ctx, a.adapter, coordinate)
	if err != nil {
		return review.ObservationV2{}, err
	}
	state, err := review.DecodeStateV2(head.AuthoritySummary())
	if err != nil || journalv2.LogicalVersion(state.LogicalVersion) != head.LogicalVersion() {
		return review.ObservationV2{}, invalidV2Plan("review authority summary")
	}
	digest, _ := journalv2.ReducerStateDigestV2(state.CanonicalBytes())
	if digest != head.CanonicalStateDigest() {
		return review.ObservationV2{}, invalidV2Plan("review authority digest")
	}
	return review.ObservationV2{State: &state, RecordRevision: record.RecordRevision(), LastPublicationID: head.LastPublicationID()}, nil
}

func (a *reviewAuthorityV2) ObserveReviewAuthority(ctx context.Context, r review.AuthorityRequestV2) (review.AuthorityObservationV2, error) {
	baseRequest := handoff.AuthorityRequestV2{OperationID: r.OperationID, HandoffID: r.HandoffID, ProjectID: r.ProjectID, MissionID: r.MissionID, WorkRevisionID: r.WorkRevisionID, WorkID: r.WorkID, RunID: r.Workspace.RunID, RuntimeEpochID: r.RuntimeEpochID, Workspace: r.Workspace, LeaseID: r.Workspace.LeaseID, LeaseGeneration: r.Workspace.Generation, Fence: r.Workspace.Fence, ActorID: r.Workspace.ActorID, ProcessID: r.Workspace.ProcessID, RepositoryID: r.Workspace.RepositoryID, LaneID: r.Workspace.LaneID, OwnedBranch: r.Workspace.OwnedBranch, PinnedBase: r.Workspace.PinnedBase, Candidate: r.Candidate, CriteriaDigest: r.HandoffContributorDigest, EvidenceDigest: r.HandoffContributorDigest, Governance: r.Governance, ObservedAt: r.ObservedAt}
	base, err := a.handoff.ObserveHandoffAuthority(ctx, baseRequest)
	if err != nil {
		return review.AuthorityObservationV2{}, err
	}
	handoffCoordinate, err := handoff.CoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID)
	if err != nil {
		return review.AuthorityObservationV2{}, err
	}
	handoffObserved, err := a.handoff.ObserveHandoff(ctx, handoffCoordinate)
	if err != nil {
		return review.AuthorityObservationV2{}, err
	}
	handoffAssertion, revision, err := observeHeadAssertionV2(ctx, a.adapter, handoffCoordinate)
	if err != nil || revision != handoffObserved.RecordRevision {
		return review.AuthorityObservationV2{}, invalidV2Plan("review handoff assertion")
	}
	result := review.AuthorityObservationV2{Eligibility: base.Eligibility, Execution: base.Execution, Workspace: base.Workspace, Handoff: handoffObserved, Assertions: append(base.Assertions, handoffAssertion)}
	if r.Exception != nil {
		coordinate, e := review.ExceptionCoordinateV2(*r.Exception)
		if e != nil {
			return review.AuthorityObservationV2{}, e
		}
		head, record, e := readAuthorityHeadV2(ctx, a.adapter, coordinate)
		if e != nil {
			return review.AuthorityObservationV2{}, e
		}
		exception, e := review.DecodeIndependenceExceptionV2(head.AuthoritySummary())
		if e != nil || head.CanonicalStateDigest() != exception.Digest() {
			return review.AuthorityObservationV2{}, invalidV2Plan("review policy exception")
		}
		assertion, assertionRevision, e := observeHeadAssertionV2(ctx, a.adapter, coordinate)
		if e != nil || assertionRevision != record.RecordRevision() {
			return review.AuthorityObservationV2{}, invalidV2Plan("review policy assertion")
		}
		result.Exception = review.ExceptionObservationV2{State: &exception, RecordRevision: record.RecordRevision(), Assertion: assertion}
	}
	return result, nil
}

func (a *reviewAuthorityV2) PublishReview(ctx context.Context, capability review.AuthorityPlanV2) (review.AuthorityAckV2, error) {
	plan, err := capability.Publication()
	if err != nil || !reviewTransportMembershipV2(plan) {
		return review.AuthorityAckV2{}, invalidV2Plan("review authority plan")
	}
	ack, err := publishAuthorityPlanV2(ctx, a.adapter, a.coordinator, plan)
	if err != nil {
		return review.AuthorityAckV2{}, err
	}
	return review.AuthorityAckV2{PublicationID: ack.PublicationID(), ProviderRevision: ack.ProviderRevision(), PlanDigest: ack.PlanDigest(), MutationCount: ack.MutationCount()}, nil
}
func reviewTransportMembershipV2(plan journalv2.SealedCommitPlanV2) bool {
	assertions := len(plan.Assertions())
	if assertions != 3 && assertions != 4 {
		return false
	}
	return authorityTransportMembershipV2(plan, "review_slot", 2, 2, 3, assertions, 1)
}
