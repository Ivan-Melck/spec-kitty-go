package gapdb

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type workspacePlanCaptureV2 struct {
	execution execution.ExecutionSlotObservationV2
	plan      execution.WorkspaceAuthorityPlanV2
}

type workspaceStagePlanCaptureV2 struct {
	workspace workspace.StageObservationV2
	assertion journalv2.AuthorityAssertionV2
	plan      workspace.StageAuthorityPlanV2
}

func (p *workspaceStagePlanCaptureV2) ResolveWorkspaceOperation(context.Context, journalv2.OperationBindingV2) (workspace.StageOperationResolutionV2, error) {
	return workspace.StageOperationResolutionV2{Kind: workspace.StageOperationAbsentV2}, nil
}
func (p *workspaceStagePlanCaptureV2) ObserveWorkspaceExecutionAssertion(context.Context, workspace.BindingV2) (journalv2.AuthorityAssertionV2, error) {
	return p.assertion, nil
}
func (p *workspaceStagePlanCaptureV2) ObserveWorkspaceStage(context.Context, journalv2.AggregateCoordinate) (workspace.StageObservationV2, error) {
	return p.workspace, nil
}
func (p *workspaceStagePlanCaptureV2) PublishWorkspaceStage(_ context.Context, plan workspace.StageAuthorityPlanV2) (workspace.StageAuthorityAckV2, error) {
	p.plan = plan
	return workspace.StageAuthorityAckV2{}, nil
}

func (p *workspacePlanCaptureV2) ResolveExecutionOperation(context.Context, journalv2.OperationBindingV2) (execution.ExecutionOperationResolutionV2, error) {
	return execution.ExecutionOperationResolutionV2{Kind: execution.ExecutionOperationAbsentV2}, nil
}
func (p *workspacePlanCaptureV2) ObserveExecutionSlot(context.Context, journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	return p.execution, nil
}
func (p *workspacePlanCaptureV2) ObserveWorkspace(context.Context, journalv2.AggregateCoordinate) (execution.WorkspaceAuthorityObservationV2, error) {
	return execution.WorkspaceAuthorityObservationV2{}, nil
}
func (p *workspacePlanCaptureV2) PublishWorkspace(_ context.Context, plan execution.WorkspaceAuthorityPlanV2) (execution.WorkspaceAuthorityAckV2, error) {
	p.plan = plan
	return execution.WorkspaceAuthorityAckV2{}, nil
}

func TestWorkspaceAuthorityCommitsBindingAndPermanentPathClaimThroughGapDBV2(t *testing.T) {
	admission, slot, request, _ := gapdbExecutionAdmissionPlanFixtureV2(t, "workspace", "work-workspace")
	guard := admitPublicationGuardV2(t, admission)
	memory := newV2AtomicMemory()
	memory.revision = 17
	guardKey, _ := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
	memory.records[guardKey.String()] = gapdbapi.NewRecord(guardKey.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
	provider := memory.provider()
	provider.statusFn = gapdbMemoryStatusV2(memory, "db-workspace")
	adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := workspaceProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	if err != nil {
		t.Fatal(err)
	}
	execAuthority, err := newExecutionAuthorityV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = execAuthority.PublishExecution(t.Context(), admission); err != nil {
		t.Fatal(err)
	}
	authority, err := newWorkspaceAuthorityV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	service, err := execution.NewWorkspaceAuthorityServiceV2(authority, gapdbEpochV2{epoch: slot.RuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	binding := gapdbWorkspaceBindingV2(t, slot, "workspace-gapdb", "/srv/spec-kitty/workspaces/gapdb")
	contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: binding, EffectID: "effect-workspace-create", EventID: "event-workspace-create", OccurredAt: request.ObservedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(t.Context(), execution.WorkspaceAuthorityRequestV2{Kind: execution.WorkspaceBindV2, OperationID: "operation-workspace-bind", OperationEventID: "event-operation-workspace-bind", ExecutionEventID: "event-execution-workspace-bind", Governance: slot.Governance, Contribution: contribution, ObservedAt: request.ObservedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.WorkspaceID != binding.WorkspaceID {
		t.Fatal("execution head is not bound")
	}
	claimKey, _ := v2WorkspacePathClaimKey(binding.PathClaimDigest)
	if record := memory.records[claimKey.String()]; record.Revision == 0 || record.ExpiresAt != nil {
		t.Fatal("path claim is not durable/non-expiring")
	}
	workspaceCoordinate, _ := workspace.WorkspaceCoordinateV2(binding)
	observed, err := authority.ObserveWorkspace(t.Context(), workspaceCoordinate)
	if err != nil || observed.State == nil || observed.State.Binding.WorkspaceID != binding.WorkspaceID {
		t.Fatalf("observe=%+v err=%v", observed, err)
	}
	if memory.batchCalls != 2 {
		t.Fatalf("want one admission plus one workspace batch, calls=%d", memory.batchCalls)
	}
	executionCoordinate, _ := execution.ExecutionSlotCoordinateV2(slot.ProjectID, slot.MissionID, slot.WorkRevisionID, slot.WorkID)
	executionBefore, err := authority.ObserveExecutionSlot(t.Context(), executionCoordinate)
	if err != nil {
		t.Fatal(err)
	}
	transition, err := workspace.NewTransitionContributionV2(workspace.TransitionContributionInputV2{Prior: *observed.State, RecordRevision: observed.RecordRevision, EventID: "event-workspace-observed", NextStage: workspace.StageWorktreeObservedV2, Observation: workspace.ObservationV2{Registered: true, Head: binding.PinnedBase}, OccurredAt: request.ObservedAt.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	stageService, err := workspace.NewStageAuthorityServiceV2(authority, gapdbEpochV2{epoch: slot.RuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	stageResult, err := stageService.Execute(t.Context(), workspace.StageAuthorityRequestV2{OperationID: "operation-workspace-observed", OperationEventID: "event-operation-workspace-observed", Contribution: transition, PolicyRevision: slot.Governance.PolicyRevision, DoctrineRevision: slot.Governance.DoctrineRevision, ProfileRevision: slot.Governance.ProfileRevision, ObservedAt: request.ObservedAt.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if stageResult.State.Stage != workspace.StageWorktreeObservedV2 {
		t.Fatalf("stage=%s", stageResult.State.Stage)
	}
	executionAfter, err := authority.ObserveExecutionSlot(t.Context(), executionCoordinate)
	if err != nil {
		t.Fatal(err)
	}
	if executionAfter.RecordRevision != executionBefore.RecordRevision {
		t.Fatal("unchanged execution authority was rewritten instead of asserted")
	}
	if memory.batchCalls != 3 {
		t.Fatalf("workspace stage did not use one atomic batch: %d", memory.batchCalls)
	}
	current := stageResult.State
	for index, step := range []struct {
		stage  workspace.StageV2
		effect journalv2.EffectID
	}{
		{workspace.StageBaseVerifiedV2, ""}, {workspace.StageDependenciesRequestedV2, "effect-workspace-dependencies"}, {workspace.StageDependenciesObservedV2, ""}, {workspace.StageOwnershipVerifiedV2, ""}, {workspace.StageReadyV2, ""},
	} {
		observedStage, observeErr := authority.ObserveWorkspaceStage(t.Context(), workspaceCoordinate)
		if observeErr != nil {
			t.Fatal(observeErr)
		}
		observation := workspace.ObservationV2{Registered: true, Head: binding.PinnedBase, Branch: binding.OwnedBranch, GitDirIdentity: "gitdir-gapdb", IndexIdentity: "index-gapdb", Clean: true, DependencyHead: binding.PinnedBase, TargetBefore: binding.PinnedBase, TargetAfter: binding.PinnedBase}
		contribution, buildErr := workspace.NewTransitionContributionV2(workspace.TransitionContributionInputV2{Prior: current, RecordRevision: observedStage.RecordRevision, EventID: journalv2.EventID(fmt.Sprintf("event-workspace-stage-%d", index)), NextStage: step.stage, Observation: observation, EffectID: step.effect, OccurredAt: request.ObservedAt.Add(time.Duration(index+3) * time.Second)})
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		result, executeErr := stageService.Execute(t.Context(), workspace.StageAuthorityRequestV2{OperationID: journalv2.OperationID(fmt.Sprintf("operation-workspace-stage-%d", index)), OperationEventID: journalv2.EventID(fmt.Sprintf("event-operation-workspace-stage-%d", index)), Contribution: contribution, PolicyRevision: slot.Governance.PolicyRevision, DoctrineRevision: slot.Governance.DoctrineRevision, ProfileRevision: slot.Governance.ProfileRevision, ObservedAt: request.ObservedAt.Add(time.Duration(index+3) * time.Second)})
		if executeErr != nil {
			t.Fatalf("stage %s: %v", step.stage, executeErr)
		}
		current = result.State
	}
	if current.Stage != workspace.StageReadyV2 {
		t.Fatalf("final stage=%s", current.Stage)
	}
	executionAfterReady, err := authority.ObserveExecutionSlot(t.Context(), executionCoordinate)
	if err != nil {
		t.Fatal(err)
	}
	if executionAfterReady.RecordRevision != executionBefore.RecordRevision {
		t.Fatal("preparation rewrote execution head")
	}
	readyObserved, err := authority.ObserveWorkspace(t.Context(), workspaceCoordinate)
	if err != nil || readyObserved.State == nil {
		t.Fatalf("observe ready workspace: %+v %v", readyObserved, err)
	}
	expiresAt := request.Deadline.Add(time.Second)
	quarantine, err := workspace.NewQuarantineContributionV2(workspace.QuarantineContributionInputV2{Prior: *readyObserved.State, RecordRevision: readyObserved.RecordRevision, EventID: "event-workspace-quarantine", Reason: workspace.QuarantineLeaseExpired, OccurredAt: expiresAt})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := service.Execute(t.Context(), execution.WorkspaceAuthorityRequestV2{Kind: execution.WorkspaceQuarantineV2, OperationID: "operation-workspace-quarantine", OperationEventID: "event-operation-workspace-quarantine", ExecutionEventID: "event-execution-workspace-quarantine", Governance: slot.Governance, Contribution: quarantine, ObservedAt: expiresAt})
	if err != nil {
		t.Fatal(err)
	}
	if expired.Execution.Status != execution.ExecutionExpiredV2 || expired.Workspace.Stage != workspace.StageQuarantinedV2 {
		t.Fatalf("expiry did not atomically quarantine: %+v", expired)
	}
	executionRecord := memory.records[headKeyForTestV2(t, executionCoordinate)]
	workspaceRecord := memory.records[headKeyForTestV2(t, workspaceCoordinate)]
	if executionRecord.Revision == 0 || executionRecord.Revision != workspaceRecord.Revision {
		t.Fatalf("execution/workspace quarantine split revisions: %d/%d", executionRecord.Revision, workspaceRecord.Revision)
	}
	if claim := memory.records[claimKey.String()]; claim.Revision == 0 || claim.ExpiresAt != nil {
		t.Fatal("quarantine removed or expired the permanent path claim")
	}
}

func headKeyForTestV2(t *testing.T, coordinate journalv2.AggregateCoordinate) string {
	t.Helper()
	key, err := v2AggregateHeadKey(coordinate)
	if err != nil {
		t.Fatal(err)
	}
	return key.String()
}

func TestWorkspacePathClaimContentionAndIndependentProgressV2(t *testing.T) {
	// The provider primitive is already qualified; this test freezes workspace's
	// host-global coordinate and claim construction independently of work scope.
	left := workspaceBindingForContentionV2(t, "workspace-left", "work-left", "/srv/spec-kitty/workspaces/shared")
	right := workspaceBindingForContentionV2(t, "workspace-right", "work-right", "/srv/spec-kitty/workspaces/shared")
	if left.PathClaimDigest != right.PathClaimDigest {
		t.Fatal("same repository path did not share permanent claim")
	}
	leftCoordinate, _ := workspace.WorkspaceCoordinateV2(left)
	rightCoordinate, _ := workspace.WorkspaceCoordinateV2(right)
	if leftCoordinate == rightCoordinate || leftCoordinate.Scope.Kind != journalv2.ScopeHost || rightCoordinate.Scope.Kind != journalv2.ScopeHost {
		t.Fatal("workspace identity is not host-global and independent")
	}
}

func TestWorkspacePathClaimAndIndependentWorkspaceCampaignV2(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		memory := newV2AtomicMemory()
		memory.revision = 17
		provider := memory.provider()
		provider.statusFn = gapdbMemoryStatusV2(memory, fmt.Sprintf("db-path-race-%03d", trial))
		adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		sharedPath := fmt.Sprintf("/srv/spec-kitty/workspaces/shared-%03d", trial)
		plans := make([]journalv2.SealedCommitPlanV2, 3)
		bindings := make([]workspace.BindingV2, 3)
		for index, path := range []string{sharedPath, sharedPath, fmt.Sprintf("/srv/spec-kitty/workspaces/independent-%03d", trial)} {
			plans[index], bindings[index] = gapdbWorkspaceBindPlanV2(t, adapter, memory, fmt.Sprintf("path-%03d-%d", trial, index), fmt.Sprintf("work-path-%d", index), path)
		}
		errs := make([]error, len(plans))
		var wg sync.WaitGroup
		wg.Add(len(plans))
		for index := range plans {
			go func() {
				defer wg.Done()
				_, errs[index] = adapter.CommitV2(t.Context(), plans[index])
			}()
		}
		wg.Wait()
		if errs[2] != nil {
			t.Fatalf("trial %d unrelated workspace blocked: %v", trial, errs[2])
		}
		sharedWinners := 0
		for _, index := range []int{0, 1} {
			if errs[index] == nil {
				sharedWinners++
			}
		}
		if sharedWinners != 1 {
			t.Fatalf("trial %d shared winners=%d errors=%v", trial, sharedWinners, errs)
		}
		sharedClaimKey, _ := v2WorkspacePathClaimKey(bindings[0].PathClaimDigest)
		independentClaimKey, _ := v2WorkspacePathClaimKey(bindings[2].PathClaimDigest)
		if memory.records[sharedClaimKey.String()].Revision == 0 || memory.records[independentClaimKey.String()].Revision == 0 {
			t.Fatalf("trial %d missing durable path claim", trial)
		}
		for index := range bindings {
			workspaceKey := headKeyForTestV2(t, mustWorkspaceCoordinateV2(t, bindings[index]))
			_, exists := memory.records[workspaceKey]
			if exists != (errs[index] == nil) {
				t.Fatalf("trial %d index %d partial workspace result", trial, index)
			}
		}
	}
}

func gapdbWorkspaceBindPlanV2(t *testing.T, adapter *Adapter, memory *v2AtomicMemory, suffix, workID, path string) (journalv2.SealedCommitPlanV2, workspace.BindingV2) {
	t.Helper()
	admission, slot, request, _ := gapdbExecutionAdmissionPlanFixtureV2(t, suffix, workID)
	guard := admitPublicationGuardV2(t, admission)
	guardKey, _ := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
	memory.records[guardKey.String()] = gapdbapi.NewRecord(guardKey.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
	if _, err := adapter.CommitV2(t.Context(), mustExecutionPublicationV2(t, admission)); err != nil {
		t.Fatal(err)
	}
	coordinate, _ := execution.ExecutionSlotCoordinateV2(slot.ProjectID, slot.MissionID, slot.WorkRevisionID, slot.WorkID)
	headKey, _ := v2AggregateHeadKey(coordinate)
	binding := gapdbWorkspaceBindingV2(t, slot, ids.WorkspaceID("workspace-"+suffix).String(), path)
	contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: binding, EffectID: journalv2.EffectID("effect-" + suffix), EventID: journalv2.EventID("event-workspace-" + suffix), OccurredAt: request.ObservedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	capture := &workspacePlanCaptureV2{execution: execution.ExecutionSlotObservationV2{State: &slot, RecordRevision: uint64(memory.records[headKey.String()].Revision)}}
	service, err := execution.NewWorkspaceAuthorityServiceV2(capture, gapdbEpochV2{epoch: slot.RuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = service.Execute(t.Context(), execution.WorkspaceAuthorityRequestV2{Kind: execution.WorkspaceBindV2, OperationID: journalv2.OperationID("operation-bind-" + suffix), OperationEventID: journalv2.EventID("event-operation-bind-" + suffix), ExecutionEventID: journalv2.EventID("event-execution-bind-" + suffix), Governance: slot.Governance, Contribution: contribution, ObservedAt: request.ObservedAt.Add(time.Second)})
	sealed, err := capture.plan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	return sealed, binding
}

func TestWorkspaceBindVersusExpiryAtomicCampaignV2(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		suffix := fmt.Sprintf("bind-expire-%03d", trial)
		admission, slot, baseRequest, _ := gapdbExecutionAdmissionPlanFixtureV2(t, suffix, "work-race")
		guard := admitPublicationGuardV2(t, admission)
		memory := newV2AtomicMemory()
		memory.revision = 17
		key, _ := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
		memory.records[key.String()] = gapdbapi.NewRecord(key.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
		provider := memory.provider()
		provider.statusFn = gapdbMemoryStatusV2(memory, "db-"+suffix)
		adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
		if _, err := adapter.CommitV2(t.Context(), mustExecutionPublicationV2(t, admission)); err != nil {
			t.Fatal(err)
		}
		executionCoordinate, _ := execution.ExecutionSlotCoordinateV2(slot.ProjectID, slot.MissionID, slot.WorkRevisionID, slot.WorkID)
		headKey, _ := v2AggregateHeadKey(executionCoordinate)
		activeRevision := uint64(memory.records[headKey.String()].Revision)
		binding := gapdbWorkspaceBindingV2(t, slot, "workspace-"+suffix, "/srv/spec-kitty/workspaces/"+suffix)
		contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: binding, EffectID: journalv2.EffectID("effect-" + suffix), EventID: journalv2.EventID("event-workspace-" + suffix), OccurredAt: baseRequest.ObservedAt.Add(time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		capture := &workspacePlanCaptureV2{execution: execution.ExecutionSlotObservationV2{State: &slot, RecordRevision: activeRevision}}
		service, _ := execution.NewWorkspaceAuthorityServiceV2(capture, gapdbEpochV2{epoch: slot.RuntimeEpochID}, journalv2.DefaultEventRegistryV2())
		_, _ = service.Execute(t.Context(), execution.WorkspaceAuthorityRequestV2{Kind: execution.WorkspaceBindV2, OperationID: journalv2.OperationID("operation-bind-" + suffix), OperationEventID: journalv2.EventID("event-operation-bind-" + suffix), ExecutionEventID: journalv2.EventID("event-execution-bind-" + suffix), Governance: slot.Governance, Contribution: contribution, ObservedAt: baseRequest.ObservedAt.Add(time.Second)})
		bindPlan, err := capture.plan.Publication()
		if err != nil {
			t.Fatal(err)
		}
		expireRequest := executionRequestFromStateV2(baseRequest, slot, execution.ExecutionExpireV2, suffix+"-expire")
		expireRequest.ObservedAt = baseRequest.Deadline.Add(time.Second)
		expirePlan, _ := gapdbCaptureExecutionPlanV2(t, expireRequest, workgraph.ObservedEligibilityShardV2{}, execution.ExecutionSlotObservationV2{State: &slot, RecordRevision: activeRevision})
		expireSealed := mustExecutionPublicationV2(t, expirePlan)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, errs[0] = adapter.CommitV2(t.Context(), bindPlan) }()
		go func() { defer wg.Done(); _, errs[1] = adapter.CommitV2(t.Context(), expireSealed) }()
		wg.Wait()
		winners := 0
		for _, e := range errs {
			if e == nil {
				winners++
			}
		}
		if winners != 1 {
			t.Fatalf("trial %d winners=%d errs=%v", trial, winners, errs)
		}
		workspaceKey, _ := v2AggregateHeadKey(mustWorkspaceCoordinateV2(t, binding))
		_, workspaceExists := memory.records[workspaceKey.String()]
		claimKey, _ := v2WorkspacePathClaimKey(binding.PathClaimDigest)
		_, claimExists := memory.records[claimKey.String()]
		if workspaceExists != claimExists {
			t.Fatalf("trial %d split workspace/path claim", trial)
		}
		if errs[0] == nil && !workspaceExists {
			t.Fatalf("trial %d bind acknowledged without workspace", trial)
		}
		if errs[1] == nil && workspaceExists {
			t.Fatalf("trial %d expiry won but workspace partially bound", trial)
		}
	}
}

func TestWorkspaceConflictingStageTransitionCampaignV2(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		suffix := fmt.Sprintf("stage-race-%03d", trial)
		memory := newV2AtomicMemory()
		memory.revision = 17
		provider := memory.provider()
		provider.statusFn = gapdbMemoryStatusV2(memory, "db-"+suffix)
		adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		bindPlan, binding := gapdbWorkspaceBindPlanV2(t, adapter, memory, suffix, "work-stage-race", "/srv/spec-kitty/workspaces/"+suffix)
		if _, err := adapter.CommitV2(t.Context(), bindPlan); err != nil {
			t.Fatal(err)
		}
		projection, err := workspaceProjectionTrackerV2()
		if err != nil {
			t.Fatal(err)
		}
		coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
		if err != nil {
			t.Fatal(err)
		}
		authority, err := newWorkspaceAuthorityV2(adapter, coordinator)
		if err != nil {
			t.Fatal(err)
		}
		coordinate := mustWorkspaceCoordinateV2(t, binding)
		observed, err := authority.ObserveWorkspace(t.Context(), coordinate)
		if err != nil || observed.State == nil {
			t.Fatalf("trial %d observe workspace: %+v %v", trial, observed, err)
		}
		assertion, err := authority.ObserveWorkspaceExecutionAssertion(t.Context(), binding)
		if err != nil {
			t.Fatal(err)
		}
		plans := make([]journalv2.SealedCommitPlanV2, 2)
		for index := range plans {
			contribution, buildErr := workspace.NewTransitionContributionV2(workspace.TransitionContributionInputV2{Prior: *observed.State, RecordRevision: observed.RecordRevision, EventID: journalv2.EventID(fmt.Sprintf("event-workspace-stage-%03d-%d", trial, index)), NextStage: workspace.StageWorktreeObservedV2, Observation: workspace.ObservationV2{Registered: true}, OccurredAt: time.Date(2026, 9, 4, 12, 1, index, 0, time.UTC)})
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			capture := &workspaceStagePlanCaptureV2{workspace: workspace.StageObservationV2{State: observed.State, RecordRevision: observed.RecordRevision, LastPublicationID: observed.LastPublicationID}, assertion: assertion}
			service, buildErr := workspace.NewStageAuthorityServiceV2(capture, gapdbEpochV2{epoch: binding.RuntimeEpochID}, journalv2.DefaultEventRegistryV2())
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			_, _ = service.Execute(t.Context(), workspace.StageAuthorityRequestV2{OperationID: journalv2.OperationID(fmt.Sprintf("operation-stage-%03d-%d", trial, index)), OperationEventID: journalv2.EventID(fmt.Sprintf("event-operation-stage-%03d-%d", trial, index)), Contribution: contribution, PolicyRevision: observed.State.Binding.WorkRevisionID.String(), DoctrineRevision: "doctrine-stage", ProfileRevision: "profile-stage", ObservedAt: time.Date(2026, 9, 4, 12, 1, index, 0, time.UTC)})
			plans[index], err = capture.plan.Publication()
			if err != nil {
				t.Fatal(err)
			}
		}
		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		for index := range plans {
			go func() {
				defer wg.Done()
				_, errs[index] = adapter.CommitV2(t.Context(), plans[index])
			}()
		}
		wg.Wait()
		winners := 0
		for _, commitErr := range errs {
			if commitErr == nil {
				winners++
			}
		}
		if winners != 1 {
			t.Fatalf("trial %d winners=%d errors=%v", trial, winners, errs)
		}
		after, err := authority.ObserveWorkspace(t.Context(), coordinate)
		if err != nil || after.State == nil || after.State.LogicalVersion != observed.State.LogicalVersion+1 || after.State.Stage != workspace.StageWorktreeObservedV2 {
			t.Fatalf("trial %d partial/invalid stage: %+v %v", trial, after, err)
		}
	}
}

func mustExecutionPublicationV2(t *testing.T, plan execution.ExecutionAuthorityPlanV2) journalv2.SealedCommitPlanV2 {
	t.Helper()
	sealed, err := plan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}
func mustWorkspaceCoordinateV2(t *testing.T, b workspace.BindingV2) journalv2.AggregateCoordinate {
	t.Helper()
	c, err := workspace.WorkspaceCoordinateV2(b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func gapdbWorkspaceBindingV2(t *testing.T, slot execution.ExecutionSlotStateV2, workspaceID, canonicalPath string) workspace.BindingV2 {
	t.Helper()
	claim, err := workspace.WorkspacePathClaimDigestV2("repository-gapdb", "/srv/spec-kitty/workspaces", canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	return workspace.BindingV2{WorkspaceID: ids.WorkspaceID(workspaceID), ProjectID: slot.ProjectID, MissionID: slot.MissionID, WorkRevisionID: slot.WorkRevisionID, WorkID: slot.WorkID, RunID: slot.RunID, LeaseID: slot.LeaseID, Generation: slot.Generation, Fence: slot.Fence, RuntimeEpochID: slot.RuntimeEpochID, ActorID: slot.ActorID, ProcessID: slot.ProcessID, RepositoryID: "repository-gapdb", LaneID: "lane-gapdb", ManagedRoot: "/srv/spec-kitty/workspaces", CanonicalPath: canonicalPath, PathIdentity: verifiedGapDBPathIdentityV2(), PathClaimDigest: claim, OwnedBranch: "kitty/workspace-gapdb", PinnedBase: strings.Repeat("a", 40)}
}

func workspaceBindingForContentionV2(t *testing.T, workspaceID, workID, canonicalPath string) workspace.BindingV2 {
	t.Helper()
	claim, err := workspace.WorkspacePathClaimDigestV2("repository-gapdb", "/srv/spec-kitty/workspaces", canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	return workspace.BindingV2{WorkspaceID: ids.WorkspaceID(workspaceID), ProjectID: "project-gapdb", MissionID: "mission-gapdb", WorkRevisionID: "revision-gapdb", WorkID: ids.WorkID(workID), RunID: "run-gapdb", LeaseID: "lease-gapdb", Generation: 1, Fence: strings.Repeat("a", 32), RuntimeEpochID: "epoch-gapdb", ActorID: "actor-gapdb", ProcessID: "process-gapdb", RepositoryID: "repository-gapdb", LaneID: "lane-gapdb", ManagedRoot: "/srv/spec-kitty/workspaces", CanonicalPath: canonicalPath, PathIdentity: verifiedGapDBPathIdentityV2(), PathClaimDigest: claim, OwnedBranch: "kitty/" + workID, PinnedBase: strings.Repeat("a", 40)}
}

func verifiedGapDBPathIdentityV2() workspace.PathIdentityV2 {
	return workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}
}

func workspaceProjectionTrackerV2() (*journalv2.ProjectionTrackerV2, error) {
	registry := journalv2.DefaultEventRegistryV2()
	owned := append(execution.ExecutionReducerRegistrationsV2(), workspace.ReducerRegistrationsV2()...)
	byFamily := map[string]journalv2.ReducerRegistration{}
	for _, r := range owned {
		byFamily[r.Family] = r
	}
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		if tuple.Family == "operation" {
			registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: journalv2.OperationReducerImplementationDigestV2(), Reduce: journalv2.ReduceOperationStateV2})
			continue
		}
		if r, ok := byFamily[tuple.Family]; ok {
			registrations = append(registrations, r)
			continue
		}
		sum := sha256.Sum256([]byte("test-unused/" + tuple.Family))
		digest, _ := journalv2.ParseDigest("test", fmt.Sprintf("%x", sum))
		registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: digest, Reduce: func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil }})
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		return nil, err
	}
	return journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
}
