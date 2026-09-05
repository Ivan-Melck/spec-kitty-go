package execution

import (
	"context"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type workspacePortProbeV2 struct {
	resolution ExecutionOperationResolutionV2
	execution  ExecutionSlotObservationV2
	workspace  WorkspaceAuthorityObservationV2
	plans      []WorkspaceAuthorityPlanV2
}

func (p *workspacePortProbeV2) ResolveExecutionOperation(context.Context, journalv2.OperationBindingV2) (ExecutionOperationResolutionV2, error) {
	return p.resolution, nil
}
func (p *workspacePortProbeV2) ObserveExecutionSlot(context.Context, journalv2.AggregateCoordinate) (ExecutionSlotObservationV2, error) {
	return p.execution, nil
}
func (p *workspacePortProbeV2) ObserveWorkspace(context.Context, journalv2.AggregateCoordinate) (WorkspaceAuthorityObservationV2, error) {
	return p.workspace, nil
}
func (p *workspacePortProbeV2) PublishWorkspace(_ context.Context, plan WorkspaceAuthorityPlanV2) (WorkspaceAuthorityAckV2, error) {
	p.plans = append(p.plans, plan)
	sealed, _ := plan.Publication()
	return WorkspaceAuthorityAckV2{sealed.PublicationID(), sealed.Digest(), sealed.MutationCount(), 19, 19}, nil
}

func TestWorkspaceBindingAndExpiryComposeAtomicallyWithExecutionV2(t *testing.T) {
	request, _ := executionRequestFixtureV2(t, ExecutionAdmitV2)
	request.Fence = strings.Repeat("a", 32)
	governance, err := ExecutionGovernanceFromContextV2(request.Governance)
	if err != nil {
		t.Fatal(err)
	}
	active, err := decideExecutionTransitionV2(request, governance, nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := workspaceBindingForExecutionV2(t, active, "workspace-1", "/srv/spec-kitty/workspaces/w1")
	contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: binding, EffectID: "effect-create-workspace", EventID: "event-workspace-bind", OccurredAt: request.ObservedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	port := &workspacePortProbeV2{resolution: ExecutionOperationResolutionV2{Kind: ExecutionOperationAbsentV2}, execution: ExecutionSlotObservationV2{State: &active, RecordRevision: 7}}
	service, err := NewWorkspaceAuthorityServiceV2(port, &executionEpochProbeV2{epoch: active.RuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	bindRequest := WorkspaceAuthorityRequestV2{Kind: WorkspaceBindV2, OperationID: "operation-workspace-bind", OperationEventID: "event-operation-bind", ExecutionEventID: "event-execution-bind", Governance: active.Governance, Contribution: contribution, ObservedAt: request.ObservedAt.Add(time.Second)}
	result, err := service.Execute(t.Context(), bindRequest)
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.WorkspaceID != binding.WorkspaceID || result.Execution.WorkspacePathClaimDigest != binding.PathClaimDigest || result.RecordRevision != 19 || len(port.plans) != 1 {
		t.Fatalf("binding not composed: %+v", result.Execution)
	}
	sealed, _ := port.plans[0].Publication()
	if len(sealed.Events()) != 3 || len(sealed.Heads()) != 3 || sealed.WorkspacePathClaim() == nil || len(sealed.Assertions()) != 0 {
		t.Fatalf("bad bind membership events=%d heads=%d assertions=%d", len(sealed.Events()), len(sealed.Heads()), len(sealed.Assertions()))
	}
	port.resolution = ExecutionOperationResolutionV2{Kind: ExecutionOperationExactV2, State: sealed.OperationState(), RecordRevision: 19}
	replayed, err := service.Execute(t.Context(), bindRequest)
	if err != nil || !replayed.Replayed || replayed.Execution.WorkspaceID != binding.WorkspaceID || replayed.RecordRevision != 19 || len(port.plans) != 1 {
		t.Fatalf("exact replay did not precede authority reads: %+v %v", replayed, err)
	}

	bound := result.Execution
	priorWorkspace := result.Workspace
	expiredAt := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	quarantine, err := workspace.NewQuarantineContributionV2(workspace.QuarantineContributionInputV2{Prior: priorWorkspace, RecordRevision: 11, EventID: "event-workspace-quarantine", Reason: workspace.QuarantineLeaseExpired, OccurredAt: expiredAt})
	if err != nil {
		t.Fatal(err)
	}
	port.execution = ExecutionSlotObservationV2{State: &bound, RecordRevision: 9}
	port.workspace = WorkspaceAuthorityObservationV2{State: &priorWorkspace, RecordRevision: 11}
	port.resolution = ExecutionOperationResolutionV2{Kind: ExecutionOperationAbsentV2}
	expiryRequest := WorkspaceAuthorityRequestV2{Kind: WorkspaceQuarantineV2, OperationID: "operation-workspace-expire", OperationEventID: "event-operation-expire", ExecutionEventID: "event-execution-expire", Governance: bound.Governance, Contribution: quarantine, ObservedAt: expiredAt}
	expired, err := service.Execute(t.Context(), expiryRequest)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Execution.Status != ExecutionExpiredV2 || expired.Workspace.Stage != workspace.StageQuarantinedV2 {
		t.Fatalf("not atomically expired/quarantined: %+v", expired)
	}
	expiryPlan, _ := port.plans[1].Publication()
	if len(expiryPlan.Events()) != 3 || len(expiryPlan.Heads()) != 3 || expiryPlan.WorkspacePathClaim() != nil {
		t.Fatal("bad expiry membership")
	}

	bindOperation, err := workspaceOperationBindingV2(bindRequest, result.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	bindInput, err := workspaceAuthorityPlanInputV2(bindRequest, bindOperation, result.Execution, ExecutionSlotObservationV2{State: &active, RecordRevision: 7}, WorkspaceAuthorityObservationV2{})
	if err != nil {
		t.Fatal(err)
	}
	expiryOperation, err := workspaceOperationBindingV2(expiryRequest, expired.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	expiryInput, err := workspaceAuthorityPlanInputV2(expiryRequest, expiryOperation, expired.Execution, ExecutionSlotObservationV2{State: &bound, RecordRevision: 9}, WorkspaceAuthorityObservationV2{State: &priorWorkspace, RecordRevision: 11})
	if err != nil {
		t.Fatal(err)
	}
	assertWorkspaceCompositionControlledMutantsV2(t, bindInput, expiryInput)
}

func assertWorkspaceCompositionControlledMutantsV2(t *testing.T, bindControl, expiryControl journalv2.CommitPlanInput) {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	for kind, control := range map[WorkspaceAuthorityCommandKindV2]journalv2.CommitPlanInput{WorkspaceBindV2: bindControl, WorkspaceQuarantineV2: expiryControl} {
		if err := validateWorkspaceAuthorityPlanInputV2(kind, control); err != nil {
			t.Fatalf("%s control rejected: %v", kind, err)
		}
		if _, err := journalv2.SealCommitPlanV2(control, registry); err != nil {
			t.Fatalf("%s control did not seal: %v", kind, err)
		}
	}
	mutants := map[string]struct {
		kind   WorkspaceAuthorityCommandKindV2
		mutate func(journalv2.CommitPlanInput) journalv2.CommitPlanInput
	}{
		"M-WORKSPACE-EXPIRY-WITHOUT-QUARANTINE": {WorkspaceQuarantineV2, func(input journalv2.CommitPlanInput) journalv2.CommitPlanInput {
			return removeWorkspaceCompositionMembersV2(input)
		}},
		"M-WORKSPACE-EXECUTION-EXPIRES-BINDING-PUBLISHES": {WorkspaceBindV2, func(input journalv2.CommitPlanInput) journalv2.CommitPlanInput {
			for index := range input.Events {
				if input.Events[index].Family == "execution_slot" {
					input.Events[index].EventType = "execution_slot.lease_expired"
				}
			}
			return input
		}},
		"M-WORKSPACE-BINDING-WINS-EXPIRY-OMITS-QUARANTINE": {WorkspaceQuarantineV2, func(input journalv2.CommitPlanInput) journalv2.CommitPlanInput {
			input = removeWorkspaceCompositionMembersV2(input)
			for index := range input.Events {
				if input.Events[index].Family == "execution_slot" {
					input.Events[index].EventType = "execution_slot.workspace_bound"
				}
			}
			return input
		}},
	}
	if len(mutants) != 3 {
		t.Fatalf("composition mutant census=%d", len(mutants))
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			control := bindControl
			if mutant.kind == WorkspaceQuarantineV2 {
				control = expiryControl
			}
			input := cloneWorkspaceAuthorityPlanInputV2(control)
			if err := validateWorkspaceAuthorityPlanInputV2(mutant.kind, mutant.mutate(input)); !IsKind(err, KindInvalidCommand) {
				t.Fatalf("mutated production composition survived: %v", err)
			}
		})
	}
}

func cloneWorkspaceAuthorityPlanInputV2(input journalv2.CommitPlanInput) journalv2.CommitPlanInput {
	input.Events = append([]journalv2.EventDraftV2(nil), input.Events...)
	input.Heads = append([]journalv2.HeadTransitionV2(nil), input.Heads...)
	input.Conditions = append([]journalv2.ProviderCondition(nil), input.Conditions...)
	input.Assertions = append([]journalv2.AuthorityAssertionV2(nil), input.Assertions...)
	return input
}

func removeWorkspaceCompositionMembersV2(input journalv2.CommitPlanInput) journalv2.CommitPlanInput {
	events := input.Events[:0]
	for _, event := range input.Events {
		if event.Family != "workspace" {
			events = append(events, event)
		}
	}
	input.Events = events
	heads := input.Heads[:0]
	for _, head := range input.Heads {
		if head.Coordinate.Kind != "workspace" {
			heads = append(heads, head)
		}
	}
	input.Heads = heads
	conditions := input.Conditions[:0]
	for _, condition := range input.Conditions {
		if condition.Target.Coordinate.Kind != "workspace" {
			conditions = append(conditions, condition)
		}
	}
	input.Conditions = conditions
	return input
}

func workspaceBindingForExecutionV2(t *testing.T, state ExecutionSlotStateV2, workspaceID ids.WorkspaceID, workspacePath string) workspace.BindingV2 {
	t.Helper()
	claim, err := workspace.WorkspacePathClaimDigestV2("repository-1", "/srv/spec-kitty/workspaces", workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	return workspace.BindingV2{WorkspaceID: workspaceID, ProjectID: state.ProjectID, MissionID: state.MissionID, WorkRevisionID: state.WorkRevisionID, WorkID: state.WorkID, RunID: state.RunID, LeaseID: state.LeaseID, Generation: state.Generation, Fence: state.Fence, RuntimeEpochID: state.RuntimeEpochID, ActorID: state.ActorID, ProcessID: state.ProcessID, RepositoryID: "repository-1", LaneID: "lane-1", ManagedRoot: "/srv/spec-kitty/workspaces", CanonicalPath: workspacePath, PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, PathClaimDigest: claim, OwnedBranch: "kitty/workspace-1", PinnedBase: strings.Repeat("a", 40)}
}
