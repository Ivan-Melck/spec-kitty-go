package execution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type workspacePortProbeV2 struct {
	resolution    ExecutionOperationResolutionV2
	resolutionErr error
	execution     ExecutionSlotObservationV2
	executionErr  error
	workspace     WorkspaceAuthorityObservationV2
	workspaceErr  error
	plans         []WorkspaceAuthorityPlanV2
}

func (p *workspacePortProbeV2) ResolveExecutionOperation(context.Context, journalv2.OperationBindingV2) (ExecutionOperationResolutionV2, error) {
	return p.resolution, p.resolutionErr
}
func (p *workspacePortProbeV2) ObserveExecutionSlot(context.Context, journalv2.AggregateCoordinate) (ExecutionSlotObservationV2, error) {
	return p.execution, p.executionErr
}
func (p *workspacePortProbeV2) ObserveWorkspace(context.Context, journalv2.AggregateCoordinate) (WorkspaceAuthorityObservationV2, error) {
	return p.workspace, p.workspaceErr
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
	bindRequest := WorkspaceAuthorityRequestV2{Kind: WorkspaceBindV2, OperationID: "operation-workspace-bind", OperationEventID: "event-operation-bind", ExecutionEventID: "event-execution-bind", Governance: active.Governance, Contribution: contribution, ObservedAt: request.ObservedAt.Add(time.Second)}
	result, err := service.Execute(t.Context(), bindRequest)
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
	replayed, err := service.Execute(t.Context(), bindRequest)
	if err != nil || !replayed.Replayed || replayed.Execution.WorkspaceID != binding.WorkspaceID || len(port.plans) != 1 {
		t.Fatalf("exact replay did not precede authority reads: %+v %v", replayed, err)
	}

	boundBeforeRebind := result.Execution
	workspaceBeforeRebind := result.Workspace
	rebindAt := request.ObservedAt.Add(2 * time.Second)
	port.resolution = ExecutionOperationResolutionV2{Kind: ExecutionOperationAbsentV2}
	port.execution = ExecutionSlotObservationV2{State: &boundBeforeRebind, RecordRevision: 19}
	port.workspace = WorkspaceAuthorityObservationV2{State: &workspaceBeforeRebind, RecordRevision: 19}
	service, err = NewWorkspaceAuthorityServiceV2(port, &executionEpochProbeV2{epoch: "epoch-2"}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	rebindRequest := WorkspaceEpochRebindRequestV2{
		ProjectID: boundBeforeRebind.ProjectID, MissionID: boundBeforeRebind.MissionID, WorkRevisionID: boundBeforeRebind.WorkRevisionID, WorkID: boundBeforeRebind.WorkID,
		ActorID: boundBeforeRebind.ActorID, ProcessID: boundBeforeRebind.ProcessID, OriginatingRuntimeEpochID: "epoch-2",
		OperationID: "operation-workspace-runtime-epoch-rebind", OperationEventID: "event-operation-runtime-epoch-rebind", ExecutionEventID: "event-execution-runtime-epoch-rebind", WorkspaceEventID: "event-workspace-runtime-epoch-rebind",
		Governance: boundBeforeRebind.Governance, ObservedAt: rebindAt,
	}
	rebound, err := service.RebindRuntimeEpoch(t.Context(), rebindRequest)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.Execution.RuntimeEpochID != "epoch-2" || rebound.Workspace.Binding.RuntimeEpochID != "epoch-2" || rebound.Execution.WorkspaceID != boundBeforeRebind.WorkspaceID || rebound.Execution.LeaseID != boundBeforeRebind.LeaseID || rebound.Execution.Fence != boundBeforeRebind.Fence || rebound.Execution.LogicalVersion != boundBeforeRebind.LogicalVersion+1 || rebound.Workspace.LogicalVersion != workspaceBeforeRebind.LogicalVersion+1 {
		t.Fatalf("workspace epoch rebind changed lease/workspace identity: %+v", rebound)
	}
	rebindPlan, _ := port.plans[1].Publication()
	if len(rebindPlan.Events()) != 3 || len(rebindPlan.Heads()) != 3 || rebindPlan.WorkspacePathClaim() != nil {
		t.Fatal("bad epoch rebind membership")
	}
	port.resolution = ExecutionOperationResolutionV2{Kind: ExecutionOperationExactV2, State: rebindPlan.OperationState(), RecordRevision: 77}
	port.executionErr, port.workspaceErr = errors.New("must not observe after exact replay"), errors.New("must not observe after exact replay")
	rotatedService, _ := NewWorkspaceAuthorityServiceV2(port, &executionEpochProbeV2{epoch: "epoch-3"}, journalv2.DefaultEventRegistryV2())
	replayedAcrossRotation, err := rotatedService.RebindRuntimeEpoch(t.Context(), rebindRequest)
	if err != nil || !replayedAcrossRotation.Replayed || replayedAcrossRotation.Execution.RuntimeEpochID != "epoch-2" || len(port.plans) != 2 {
		t.Fatalf("exact rebind did not replay before post-restart observations: result=%+v plans=%d err=%v", replayedAcrossRotation, len(port.plans), err)
	}
	port.resolution = ExecutionOperationResolutionV2{Kind: ExecutionOperationConflictV2}
	if _, err := rotatedService.RebindRuntimeEpoch(t.Context(), rebindRequest); !IsKind(err, KindIdempotencyConflict) || len(port.plans) != 2 {
		t.Fatalf("rebind conflict did not precede post-restart observations: plans=%d err=%v", len(port.plans), err)
	}
	port.executionErr, port.workspaceErr = nil, nil

	bound := rebound.Execution
	priorWorkspace := rebound.Workspace
	expiredAt := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	quarantine, err := workspace.NewQuarantineContributionV2(workspace.QuarantineContributionInputV2{Prior: priorWorkspace, RecordRevision: 19, EventID: "event-workspace-quarantine", Reason: workspace.QuarantineLeaseExpired, OccurredAt: expiredAt})
	if err != nil {
		t.Fatal(err)
	}
	port.execution = ExecutionSlotObservationV2{State: &bound, RecordRevision: 19}
	port.workspace = WorkspaceAuthorityObservationV2{State: &priorWorkspace, RecordRevision: 19}
	port.resolution = ExecutionOperationResolutionV2{Kind: ExecutionOperationAbsentV2}
	expiryRequest := WorkspaceAuthorityRequestV2{Kind: WorkspaceQuarantineV2, OperationID: "operation-workspace-expire", OperationEventID: "event-operation-expire", ExecutionEventID: "event-execution-expire", Governance: bound.Governance, Contribution: quarantine, ObservedAt: expiredAt}
	expired, err := service.Execute(t.Context(), expiryRequest)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Execution.Status != ExecutionExpiredV2 || expired.Workspace.Stage != workspace.StageQuarantinedV2 {
		t.Fatalf("not atomically expired/quarantined: %+v", expired)
	}
	expiryPlan, _ := port.plans[2].Publication()
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
	expiryInput, err := workspaceAuthorityPlanInputV2(expiryRequest, expiryOperation, expired.Execution, ExecutionSlotObservationV2{State: &bound, RecordRevision: 19}, WorkspaceAuthorityObservationV2{State: &priorWorkspace, RecordRevision: 19})
	if err != nil {
		t.Fatal(err)
	}
	rebindContribution, err := workspace.NewEpochRebindContributionV2(workspace.EpochRebindContributionInputV2{
		Prior: workspaceBeforeRebind, RecordRevision: 19, EventID: rebindRequest.WorkspaceEventID,
		RuntimeEpochID: "epoch-2", Observation: workspaceBeforeRebind.Observation, OccurredAt: rebindAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	rebindInternalRequest := WorkspaceAuthorityRequestV2{Kind: WorkspaceRebindV2, OperationID: rebindRequest.OperationID, OperationEventID: rebindRequest.OperationEventID, ExecutionEventID: rebindRequest.ExecutionEventID, Governance: rebindRequest.Governance, Contribution: rebindContribution, ObservedAt: rebindRequest.ObservedAt}
	rebindOperation, err := workspaceEpochRebindOperationBindingV2(rebindRequest)
	if err != nil {
		t.Fatal(err)
	}
	rebindInput, err := workspaceAuthorityPlanInputV2(rebindInternalRequest, rebindOperation, rebound.Execution, ExecutionSlotObservationV2{State: &boundBeforeRebind, RecordRevision: 19}, WorkspaceAuthorityObservationV2{State: &workspaceBeforeRebind, RecordRevision: 19})
	if err != nil {
		t.Fatal(err)
	}
	assertWorkspaceCompositionControlledMutantsV2(t, bindInput, expiryInput, rebindInput)
}

func TestWorkspaceEpochRebindRejectsCallerConstructedWorkspaceBeforePublicationV2(t *testing.T) {
	bound, prior, governance := boundWorkspaceFixtureV2(t)
	mutants := map[string]func(*workspace.StateV2){
		"lane":   func(state *workspace.StateV2) { state.Binding.LaneID = "lane-forged" },
		"branch": func(state *workspace.StateV2) { state.Binding.OwnedBranch = "kitty/forged" },
		"base":   func(state *workspace.StateV2) { state.Binding.PinnedBase = strings.Repeat("b", 40) },
		"dependency": func(state *workspace.StateV2) {
			state.Binding.Dependencies = []workspace.DependencyV2{{WorkID: "work-dependency", Commit: strings.Repeat("c", 40)}}
		},
		"observation": func(state *workspace.StateV2) { state.Observation = workspace.ObservationV2{} },
		"path": func(state *workspace.StateV2) {
			state.Binding.CanonicalPath = "/srv/spec-kitty/workspaces/forged"
			state.Binding.PathClaimDigest, _ = workspace.WorkspacePathClaimDigestV2(state.Binding.RepositoryID, state.Binding.ManagedRoot, state.Binding.CanonicalPath)
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			forged := prior
			forged.Binding.Dependencies = append([]workspace.DependencyV2(nil), prior.Binding.Dependencies...)
			mutate(&forged)
			contribution, err := workspace.NewEpochRebindContributionV2(workspace.EpochRebindContributionInputV2{
				Prior: forged, RecordRevision: 41, EventID: "event-forged-workspace", RuntimeEpochID: "epoch-2", Observation: forged.Observation, OccurredAt: time.Date(2026, 9, 5, 12, 1, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatalf("forged contribution did not reach service boundary: %v", err)
			}
			port := &workspacePortProbeV2{execution: ExecutionSlotObservationV2{State: &bound, RecordRevision: 40}, workspace: WorkspaceAuthorityObservationV2{State: &prior, RecordRevision: 41}}
			service, _ := NewWorkspaceAuthorityServiceV2(port, &executionEpochProbeV2{epoch: "epoch-2"}, journalv2.DefaultEventRegistryV2())
			_, err = service.Execute(t.Context(), WorkspaceAuthorityRequestV2{
				Kind: WorkspaceRebindV2, OperationID: journalv2.OperationID("operation-forged-" + name), OperationEventID: "event-forged-operation", ExecutionEventID: "event-forged-execution",
				Governance: governance, Contribution: contribution, ObservedAt: time.Date(2026, 9, 5, 12, 1, 0, 0, time.UTC),
			})
			if !IsKind(err, KindInvalidCommand) || len(port.plans) != 0 {
				t.Fatalf("caller-forged %s authority reached publication: plans=%d err=%v", name, len(port.plans), err)
			}
		})
	}
}

func TestWorkspaceEpochRebindRefusesObservedMismatchBeforePublicationV2(t *testing.T) {
	bound, prior, governance := boundWorkspaceFixtureV2(t)
	request := WorkspaceEpochRebindRequestV2{
		ProjectID: bound.ProjectID, MissionID: bound.MissionID, WorkRevisionID: bound.WorkRevisionID, WorkID: bound.WorkID,
		ActorID: bound.ActorID, ProcessID: bound.ProcessID, OriginatingRuntimeEpochID: "epoch-2",
		OperationID: "operation-rebind-mismatch", OperationEventID: "event-rebind-mismatch-operation", ExecutionEventID: "event-rebind-mismatch-execution", WorkspaceEventID: "event-rebind-mismatch-workspace",
		Governance: governance, ObservedAt: time.Date(2026, 9, 5, 12, 1, 0, 0, time.UTC),
	}
	mutants := map[string]func(*ExecutionSlotStateV2, *workspace.StateV2){
		"run":        func(_ *ExecutionSlotStateV2, state *workspace.StateV2) { state.Binding.RunID = "run-forged" },
		"lease":      func(_ *ExecutionSlotStateV2, state *workspace.StateV2) { state.Binding.LeaseID = "lease-forged" },
		"fence":      func(_ *ExecutionSlotStateV2, state *workspace.StateV2) { state.Binding.Fence = strings.Repeat("f", 32) },
		"generation": func(_ *ExecutionSlotStateV2, state *workspace.StateV2) { state.Binding.Generation++ },
		"actor":      func(_ *ExecutionSlotStateV2, state *workspace.StateV2) { state.Binding.ActorID = "actor-forged" },
		"process":    func(_ *ExecutionSlotStateV2, state *workspace.StateV2) { state.Binding.ProcessID = "process-forged" },
		"workspace": func(_ *ExecutionSlotStateV2, state *workspace.StateV2) {
			state.Binding.WorkspaceID = "workspace-forged"
		},
		"path-claim": func(_ *ExecutionSlotStateV2, state *workspace.StateV2) {
			state.Binding.PathClaimDigest = mustExecutionDigestV2(t)
		},
		"epoch":       func(_ *ExecutionSlotStateV2, state *workspace.StateV2) { state.Binding.RuntimeEpochID = "epoch-2" },
		"expired":     func(execution *ExecutionSlotStateV2, _ *workspace.StateV2) { execution.Status = ExecutionExpiredV2 },
		"quarantined": func(_ *ExecutionSlotStateV2, state *workspace.StateV2) { state.Stage = workspace.StageQuarantinedV2 },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			executionState, workspaceState := bound, prior
			workspaceState.Binding.Dependencies = append([]workspace.DependencyV2(nil), prior.Binding.Dependencies...)
			mutate(&executionState, &workspaceState)
			port := &workspacePortProbeV2{
				resolution: ExecutionOperationResolutionV2{Kind: ExecutionOperationAbsentV2},
				execution:  ExecutionSlotObservationV2{State: &executionState, RecordRevision: 40},
				workspace:  WorkspaceAuthorityObservationV2{State: &workspaceState, RecordRevision: 41},
			}
			service, _ := NewWorkspaceAuthorityServiceV2(port, &executionEpochProbeV2{epoch: "epoch-2"}, journalv2.DefaultEventRegistryV2())
			_, err := service.RebindRuntimeEpoch(t.Context(), request)
			if !IsKind(err, KindStaleAuthority) || len(port.plans) != 0 {
				t.Fatalf("observed %s mismatch reached publication: plans=%d err=%v", name, len(port.plans), err)
			}
		})
	}
}

func boundWorkspaceFixtureV2(t *testing.T) (ExecutionSlotStateV2, workspace.StateV2, ExecutionGovernanceV2) {
	t.Helper()
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
	contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: binding, EffectID: "effect-fixture", EventID: "event-fixture", OccurredAt: request.ObservedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	state, _ := contribution.State()
	active.LogicalVersion++
	active.WorkspaceID = binding.WorkspaceID
	active.WorkspacePathClaimDigest = binding.PathClaimDigest
	if err := active.Validate(); err != nil {
		t.Fatal(err)
	}
	return active, state, governance
}

func assertWorkspaceCompositionControlledMutantsV2(t *testing.T, bindControl, expiryControl, rebindControl journalv2.CommitPlanInput) {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	for kind, control := range map[WorkspaceAuthorityCommandKindV2]journalv2.CommitPlanInput{WorkspaceBindV2: bindControl, WorkspaceQuarantineV2: expiryControl, WorkspaceRebindV2: rebindControl} {
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
