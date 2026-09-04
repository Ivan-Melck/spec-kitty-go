package gapdb

import (
	"context"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type handoffAuthorityV2 struct {
	adapter     *Adapter
	coordinator *journalv2.PublicationCoordinatorV2
	execution   *executionAuthorityV2
	workspace   *workspaceAuthorityV2
}

var _ handoff.AuthorityPortV2 = (*handoffAuthorityV2)(nil)

func (a *Adapter) HandoffAuthorityPortV2(coordinator *journalv2.PublicationCoordinatorV2, eventRegistry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2, hostAuthorityID journalv2.HostAuthorityID, budgets restore.BudgetsV2) (handoff.AuthorityPortV2, error) {
	if a == nil || coordinator == nil {
		return nil, invalidV2Plan("handoff authority dependencies")
	}
	config := WorkspaceRestoreConfigurationV2{EventRegistry: eventRegistry, Reducers: reducers, HostAuthorityID: hostAuthorityID, Budgets: budgets}
	exec, err := newExecutionAuthorityV2(a, coordinator)
	if err != nil {
		return nil, err
	}
	workspaces, err := newWorkspaceAuthorityV2(a, coordinator, config)
	if err != nil {
		return nil, err
	}
	return &handoffAuthorityV2{a, coordinator, exec, workspaces}, nil
}

// HandoffAuthorityServiceV2 is the production composition seam. It makes the
// complete v2 authority callable without retaining the legacy store service.
func (a *Adapter) HandoffAuthorityServiceV2(coordinator *journalv2.PublicationCoordinatorV2, eventRegistry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2, hostAuthorityID journalv2.HostAuthorityID, budgets restore.BudgetsV2, epochs handoff.RuntimeEpochSourceV2, verifier handoff.CandidateVerifier) (*handoff.AuthorityServiceV2, error) {
	port, err := a.HandoffAuthorityPortV2(coordinator, eventRegistry, reducers, hostAuthorityID, budgets)
	if err != nil {
		return nil, err
	}
	return handoff.NewAuthorityServiceV2(port, epochs, verifier, eventRegistry)
}

func (a *handoffAuthorityV2) ResolveHandoffOperation(ctx context.Context, candidate journalv2.OperationBindingV2) (handoff.OperationResolutionV2, error) {
	resolved, err := resolveAuthorityOperationV2(ctx, a.adapter, candidate)
	if err != nil {
		return handoff.OperationResolutionV2{}, err
	}
	return handoff.OperationResolutionV2{Kind: handoff.OperationResolutionKindV2(resolved.kind), State: resolved.state, RecordRevision: resolved.recordRevision}, nil
}

func (a *handoffAuthorityV2) ObserveHandoff(ctx context.Context, coordinate journalv2.AggregateCoordinate) (handoff.ObservationV2, error) {
	head, record, err := readAuthorityHeadV2(ctx, a.adapter, coordinate)
	if err != nil {
		return handoff.ObservationV2{}, err
	}
	state, err := handoff.DecodeStateV2(head.AuthoritySummary())
	if err != nil || journalv2.LogicalVersion(state.LogicalVersion) != head.LogicalVersion() {
		return handoff.ObservationV2{}, invalidV2Plan("handoff authority summary")
	}
	digest, _ := journalv2.ReducerStateDigestV2(state.CanonicalBytes())
	if digest != head.CanonicalStateDigest() {
		return handoff.ObservationV2{}, invalidV2Plan("handoff authority digest")
	}
	return handoff.ObservationV2{State: &state, RecordRevision: record.RecordRevision(), LastPublicationID: head.LastPublicationID()}, nil
}

func (a *handoffAuthorityV2) ObserveHandoffAuthority(ctx context.Context, r handoff.AuthorityRequestV2) (handoff.AuthorityObservationV2, error) {
	shardID, err := workgraph.NewEligibilityPolicy().ShardFor(r.MissionID, r.WorkID)
	if err != nil {
		return handoff.AuthorityObservationV2{}, err
	}
	eligibility, err := a.execution.ObserveExecutionEligibility(ctx, r.MissionID, shardID)
	if err != nil {
		return handoff.AuthorityObservationV2{}, err
	}
	execCoordinate, err := execution.ExecutionSlotCoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID)
	if err != nil {
		return handoff.AuthorityObservationV2{}, err
	}
	executionState, executionRevision, executionAssertion, err := observeExecutionAssertionV2(ctx, a.adapter, execCoordinate)
	if err != nil {
		return handoff.AuthorityObservationV2{}, err
	}
	workspaceCoordinate, err := workspace.WorkspaceCoordinateV2(r.Workspace)
	if err != nil {
		return handoff.AuthorityObservationV2{}, err
	}
	workspaceObserved, err := a.workspace.ObserveWorkspace(ctx, workspaceCoordinate)
	if err != nil {
		return handoff.AuthorityObservationV2{}, err
	}
	workspaceAssertion, assertionRevision, err := observeHeadAssertionV2(ctx, a.adapter, workspaceCoordinate)
	if err != nil || assertionRevision != workspaceObserved.RecordRevision {
		return handoff.AuthorityObservationV2{}, invalidV2Plan("handoff workspace assertion")
	}
	revision, revisionRecord, revisionAssertion, err := observeWorkRevisionV2(ctx, a.adapter, r.ProjectID, r.MissionID)
	if err != nil {
		return handoff.AuthorityObservationV2{}, err
	}
	return handoff.AuthorityObservationV2{Eligibility: eligibility, Execution: execution.ExecutionSlotObservationV2{State: &executionState, RecordRevision: executionRevision}, Workspace: workspaceObserved, WorkRevision: handoff.WorkRevisionObservationV2{Revision: revision, RecordRevision: revisionRecord, Assertion: revisionAssertion}, Assertions: []journalv2.AuthorityAssertionV2{executionAssertion, workspaceAssertion, revisionAssertion}}, nil
}

func (a *handoffAuthorityV2) PublishHandoff(ctx context.Context, capability handoff.AuthorityPlanV2) (handoff.AuthorityAckV2, error) {
	plan, err := capability.Publication()
	if err != nil || !handoffTransportMembershipV2(plan) {
		return handoff.AuthorityAckV2{}, invalidV2Plan("handoff authority plan")
	}
	ack, err := publishAuthorityPlanV2(ctx, a.adapter, a.coordinator, plan)
	if err != nil {
		return handoff.AuthorityAckV2{}, err
	}
	return handoff.AuthorityAckV2{PublicationID: ack.PublicationID(), ProviderRevision: ack.ProviderRevision(), PlanDigest: ack.PlanDigest(), MutationCount: ack.MutationCount()}, nil
}
func (a *handoffAuthorityV2) ReconcileHandoff(ctx context.Context, capability handoff.AuthorityPlanV2) (handoff.AuthorityAckV2, error) {
	plan, err := capability.Publication()
	if err != nil || !handoffTransportMembershipV2(plan) {
		return handoff.AuthorityAckV2{}, invalidV2Plan("handoff reconciliation plan")
	}
	ack, err := reconcileObservedAuthorityPublicationV2(ctx, a.adapter, a.coordinator, plan)
	if err != nil {
		return handoff.AuthorityAckV2{}, err
	}
	return handoff.AuthorityAckV2{PublicationID: ack.PublicationID(), ProviderRevision: ack.ProviderRevision(), PlanDigest: ack.PlanDigest(), MutationCount: ack.MutationCount()}, nil
}

// reconcileObservedAuthorityPublicationV2 only accepts durable exact-plan
// evidence. Unlike the registered delivery recovery path, it never retries an
// absent plan: the capability service cannot infer that a Publish error arose
// after coordinator registration.
func reconcileObservedAuthorityPublicationV2(ctx context.Context, adapter *Adapter, coordinator *journalv2.PublicationCoordinatorV2, plan journalv2.SealedCommitPlanV2) (journalv2.AcknowledgedPublicationV2, error) {
	observation, err := adapter.ReconcileCommitV2(ctx, plan)
	if err != nil {
		return journalv2.AcknowledgedPublicationV2{}, err
	}
	return coordinator.ReconcileComplete(plan, observation)
}

func handoffTransportMembershipV2(plan journalv2.SealedCommitPlanV2) bool {
	return authorityTransportMembershipV2(plan, "handoff_slot", 2, 2, 3, 3, 1)
}

func observeWorkRevisionV2(ctx context.Context, a *Adapter, projectID ids.ProjectID, missionID ids.MissionID) (workgraph.RevisionV2, uint64, journalv2.AuthorityAssertionV2, error) {
	coordinate, err := journalv2.NewAggregateCoordinate("workgraph", journalv2.AggregateID(missionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(projectID), MissionID: journalv2.MissionID(missionID)})
	if err != nil {
		return workgraph.RevisionV2{}, 0, journalv2.AuthorityAssertionV2{}, err
	}
	head, record, err := readAuthorityHeadV2(ctx, a, coordinate)
	if err != nil {
		return workgraph.RevisionV2{}, 0, journalv2.AuthorityAssertionV2{}, err
	}
	state, err := workgraph.DecodeWorkgraphStateV2(head.AuthoritySummary())
	digest, digestErr := journalv2.ReducerStateDigestV2(head.AuthoritySummary())
	if err != nil || digestErr != nil || digest != head.CanonicalStateDigest() || state.ProjectID != projectID || state.MissionID != missionID || journalv2.LogicalVersion(state.LogicalVersion) != head.LogicalVersion() {
		return workgraph.RevisionV2{}, 0, journalv2.AuthorityAssertionV2{}, invalidV2Plan("workgraph authority summary")
	}
	revision, err := workgraph.DecodeRevisionV2(state.CurrentRevision)
	if err != nil || revision.ProjectID() != projectID || revision.MissionID() != missionID {
		return workgraph.RevisionV2{}, 0, journalv2.AuthorityAssertionV2{}, invalidV2Plan("work revision authority")
	}
	valueDigest, _ := journalv2.ReducerStateDigestV2(record.CanonicalValue())
	assertion, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: journalv2.ConditionTarget{Kind: journalv2.ConditionTargetAggregateHead, Coordinate: coordinate}, Kind: journalv2.ConditionRecordRevision, ObservedRevision: record.RecordRevision(), ExpectedLogicalVersion: head.LogicalVersion(), ObservedValueDigest: valueDigest})
	return revision, record.RecordRevision(), assertion, err
}

type authorityOperationResolutionV2 struct {
	kind           uint8
	state          journalv2.OperationStateV2
	recordRevision uint64
}

func resolveAuthorityOperationV2(ctx context.Context, a *Adapter, candidate journalv2.OperationBindingV2) (authorityOperationResolutionV2, error) {
	attempt, err := a.CaptureRevisionV2(ctx)
	if err != nil {
		return authorityOperationResolutionV2{}, err
	}
	address, err := journalv2.NewOperationBindingAddressV2(candidate.OperationID())
	if err != nil {
		return authorityOperationResolutionV2{}, err
	}
	request, _ := journalv2.NewReadRequestV2(address, attempt)
	record, err := a.ReadV2(ctx, request)
	if err != nil {
		if journalv2.IsKind(err, journalv2.ErrorNotFound) {
			if e := a.VerifyRevisionV2(ctx, attempt); e != nil {
				return authorityOperationResolutionV2{}, e
			}
			return authorityOperationResolutionV2{kind: 1}, nil
		}
		return authorityOperationResolutionV2{}, err
	}
	resolution, resolveErr := journalv2.ResolveOperationBinding(candidate, record.CanonicalValue(), true)
	if resolution == journalv2.BindingConflict {
		if e := a.VerifyRevisionV2(ctx, attempt); e != nil {
			return authorityOperationResolutionV2{}, e
		}
		return authorityOperationResolutionV2{kind: 3}, nil
	}
	if resolveErr != nil || resolution != journalv2.BindingExact || record.ExpiresAt() != "" {
		return authorityOperationResolutionV2{}, invalidV2Plan("authority operation binding")
	}
	headAddress, _ := journalv2.NewOperationHeadAddressV2(candidate.OperationID())
	headRequest, _ := journalv2.NewReadRequestV2(headAddress, attempt)
	headRecord, err := a.ReadV2(ctx, headRequest)
	if err != nil || headRecord.ExpiresAt() != "" {
		return authorityOperationResolutionV2{}, invalidV2Plan("authority operation head")
	}
	state, err := journalv2.DecodeOperationStateV2(headRecord.CanonicalValue())
	if err != nil || state.OperationID() != candidate.OperationID() {
		return authorityOperationResolutionV2{}, invalidV2Plan("authority operation state")
	}
	if err := a.VerifyRevisionV2(ctx, attempt); err != nil {
		return authorityOperationResolutionV2{}, err
	}
	return authorityOperationResolutionV2{kind: 2, state: state, recordRevision: headRecord.RecordRevision()}, nil
}

func readAuthorityHeadV2(ctx context.Context, a *Adapter, coordinate journalv2.AggregateCoordinate) (journalv2.AggregateHeadV2, journalv2.RecoveryRecordV2, error) {
	attempt, err := a.CaptureRevisionV2(ctx)
	if err != nil {
		return journalv2.AggregateHeadV2{}, journalv2.RecoveryRecordV2{}, err
	}
	address, err := journalv2.NewAggregateHeadAddressV2(coordinate)
	if err != nil {
		return journalv2.AggregateHeadV2{}, journalv2.RecoveryRecordV2{}, err
	}
	request, _ := journalv2.NewReadRequestV2(address, attempt)
	record, err := a.ReadV2(ctx, request)
	if err != nil {
		return journalv2.AggregateHeadV2{}, journalv2.RecoveryRecordV2{}, err
	}
	head, err := journalv2.DecodeAggregateHeadV2(record.CanonicalValue())
	if err != nil || head.Coordinate() != coordinate || head.Tombstone() || record.ExpiresAt() != "" || head.LastPublicationID() == "" {
		return journalv2.AggregateHeadV2{}, journalv2.RecoveryRecordV2{}, invalidV2Plan("authority head")
	}
	if err := a.VerifyRevisionV2(ctx, attempt); err != nil {
		return journalv2.AggregateHeadV2{}, journalv2.RecoveryRecordV2{}, err
	}
	return head, record, nil
}

func observeHeadAssertionV2(ctx context.Context, a *Adapter, coordinate journalv2.AggregateCoordinate) (journalv2.AuthorityAssertionV2, uint64, error) {
	head, record, err := readAuthorityHeadV2(ctx, a, coordinate)
	if err != nil {
		return journalv2.AuthorityAssertionV2{}, 0, err
	}
	digest, _ := journalv2.ReducerStateDigestV2(record.CanonicalValue())
	assertion, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: journalv2.ConditionTarget{Kind: journalv2.ConditionTargetAggregateHead, Coordinate: coordinate}, Kind: journalv2.ConditionRecordRevision, ObservedRevision: record.RecordRevision(), ExpectedLogicalVersion: head.LogicalVersion(), ObservedValueDigest: digest})
	return assertion, record.RecordRevision(), err
}
func observeExecutionAssertionV2(ctx context.Context, a *Adapter, coordinate journalv2.AggregateCoordinate) (execution.ExecutionSlotStateV2, uint64, journalv2.AuthorityAssertionV2, error) {
	head, record, err := readAuthorityHeadV2(ctx, a, coordinate)
	if err != nil {
		return execution.ExecutionSlotStateV2{}, 0, journalv2.AuthorityAssertionV2{}, err
	}
	state, err := execution.DecodeExecutionSlotStateV2(head.AuthoritySummary())
	if err != nil || journalv2.LogicalVersion(state.LogicalVersion) != head.LogicalVersion() {
		return execution.ExecutionSlotStateV2{}, 0, journalv2.AuthorityAssertionV2{}, invalidV2Plan("execution authority state")
	}
	stateDigest, _ := journalv2.ReducerStateDigestV2(state.CanonicalBytes())
	if stateDigest != head.CanonicalStateDigest() {
		return execution.ExecutionSlotStateV2{}, 0, journalv2.AuthorityAssertionV2{}, invalidV2Plan("execution authority digest")
	}
	valueDigest, _ := journalv2.ReducerStateDigestV2(record.CanonicalValue())
	assertion, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: journalv2.ConditionTarget{Kind: journalv2.ConditionTargetAggregateHead, Coordinate: coordinate}, Kind: journalv2.ConditionRecordRevision, ObservedRevision: record.RecordRevision(), ExpectedLogicalVersion: head.LogicalVersion(), ObservedValueDigest: valueDigest})
	return state, record.RecordRevision(), assertion, err
}

func publishAuthorityPlanV2(ctx context.Context, a *Adapter, c *journalv2.PublicationCoordinatorV2, plan journalv2.SealedCommitPlanV2) (journalv2.AcknowledgedPublicationV2, error) {
	ack, err := c.Publish(ctx, plan)
	if err == nil {
		return ack, nil
	}
	if !isAuthorityDeliveryFailureV2(err) {
		return journalv2.AcknowledgedPublicationV2{}, err
	}
	return reconcileRegisteredPublicationV2(ctx, a, c, plan)
}

func authorityTransportMembershipV2(plan journalv2.SealedCommitPlanV2, family string, events, heads, conditions, assertions, guards int) bool {
	if len(plan.Events()) != events || len(plan.Heads()) != heads || len(plan.Conditions()) != conditions || len(plan.Assertions()) != assertions || len(plan.EligibilityGuards()) != guards || len(plan.Checkpoints()) != 0 {
		return false
	}
	families := map[string]int{}
	for _, event := range plan.Events() {
		families[event.Family()]++
	}
	return families["operation"] == 1 && families[family] == 1 && len(families) == 2
}
