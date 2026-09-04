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
	return WorkspaceAuthorityAckV2{sealed.PublicationID(), sealed.Digest(), sealed.MutationCount(), 19}, nil
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
	result, err := service.Execute(t.Context(), WorkspaceAuthorityRequestV2{Kind: WorkspaceBindV2, OperationID: "operation-workspace-bind", OperationEventID: "event-operation-bind", ExecutionEventID: "event-execution-bind", Governance: active.Governance, Contribution: contribution, ObservedAt: request.ObservedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.WorkspaceID != binding.WorkspaceID || result.Execution.WorkspacePathClaimDigest != binding.PathClaimDigest || len(port.plans) != 1 {
		t.Fatalf("binding not composed: %+v", result.Execution)
	}
	sealed, _ := port.plans[0].Publication()
	if len(sealed.Events()) != 3 || len(sealed.Heads()) != 3 || sealed.WorkspacePathClaim() == nil || len(sealed.Assertions()) != 0 {
		t.Fatalf("bad bind membership events=%d heads=%d assertions=%d", len(sealed.Events()), len(sealed.Heads()), len(sealed.Assertions()))
	}
	port.resolution = ExecutionOperationResolutionV2{Kind: ExecutionOperationExactV2, State: sealed.OperationState(), RecordRevision: 19}
	replayed, err := service.Execute(t.Context(), WorkspaceAuthorityRequestV2{Kind: WorkspaceBindV2, OperationID: "operation-workspace-bind", OperationEventID: "event-operation-bind", ExecutionEventID: "event-execution-bind", Governance: active.Governance, Contribution: contribution, ObservedAt: request.ObservedAt.Add(time.Second)})
	if err != nil || !replayed.Replayed || replayed.Execution.WorkspaceID != binding.WorkspaceID || len(port.plans) != 1 {
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
	expired, err := service.Execute(t.Context(), WorkspaceAuthorityRequestV2{Kind: WorkspaceQuarantineV2, OperationID: "operation-workspace-expire", OperationEventID: "event-operation-expire", ExecutionEventID: "event-execution-expire", Governance: bound.Governance, Contribution: quarantine, ObservedAt: expiredAt})
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
}

func workspaceBindingForExecutionV2(t *testing.T, state ExecutionSlotStateV2, workspaceID ids.WorkspaceID, workspacePath string) workspace.BindingV2 {
	t.Helper()
	claim, err := workspace.WorkspacePathClaimDigestV2("repository-1", "/srv/spec-kitty/workspaces", workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	return workspace.BindingV2{WorkspaceID: workspaceID, ProjectID: state.ProjectID, MissionID: state.MissionID, WorkRevisionID: state.WorkRevisionID, WorkID: state.WorkID, RunID: state.RunID, LeaseID: state.LeaseID, Generation: state.Generation, Fence: state.Fence, RuntimeEpochID: state.RuntimeEpochID, ActorID: state.ActorID, ProcessID: state.ProcessID, RepositoryID: "repository-1", LaneID: "lane-1", ManagedRoot: "/srv/spec-kitty/workspaces", CanonicalPath: workspacePath, PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, PathClaimDigest: claim, OwnedBranch: "kitty/workspace-1", PinnedBase: strings.Repeat("a", 40)}
}
