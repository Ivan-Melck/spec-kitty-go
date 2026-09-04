package gapdb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type gapdbEffectPlanProbeV2 struct {
	plan              execution.EffectAuthorityPlanV2
	slot              execution.ExecutionSlotStateV2
	executionRevision uint64
	resolution        execution.EffectOperationResolutionV2
	effect            execution.EffectStateObservationV2
}

func (p *gapdbEffectPlanProbeV2) ResolveEffectOperation(context.Context, journalv2.OperationBindingV2) (execution.EffectOperationResolutionV2, error) {
	if p.resolution.Kind == "" {
		return execution.EffectOperationResolutionV2{Kind: execution.ExecutionOperationAbsentV2}, nil
	}
	return p.resolution, nil
}
func (p *gapdbEffectPlanProbeV2) ObserveEffectState(context.Context, journalv2.AggregateCoordinate) (execution.EffectStateObservationV2, error) {
	return p.effect, nil
}
func (p *gapdbEffectPlanProbeV2) ObserveEffectExecution(context.Context, journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	return execution.ExecutionSlotObservationV2{State: &p.slot, RecordRevision: p.executionRevision}, nil
}
func (p *gapdbEffectPlanProbeV2) PublishEffect(_ context.Context, plan execution.EffectAuthorityPlanV2) (execution.EffectPublicationAckV2, error) {
	p.plan = plan
	return execution.EffectPublicationAckV2{}, fmt.Errorf("capture")
}

type splitBatchMutantPortV2 struct{ calls int }

func (p *splitBatchMutantPortV2) Publish(_ context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.AcknowledgedPublicationV2, error) {
	for range plan.Records() {
		p.calls++
	}
	return journalv2.AcknowledgedPublicationV2{}, fmt.Errorf("mutant split publication into %d writes", p.calls)
}

type gapdbAssignmentProjectionProbeV2 struct {
	observation     tracker.AssignmentObservation
	mutation        tracker.AssignmentMutation
	mutationErr     error
	observeCalls    int
	applyCalls      int
	compensateCalls int
}

func (p *gapdbAssignmentProjectionProbeV2) ObserveAssignment(context.Context, tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	p.observeCalls++
	return p.observation, nil
}
func (p *gapdbAssignmentProjectionProbeV2) ProjectAssignment(context.Context, tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	p.applyCalls++
	return p.mutation, p.mutationErr
}
func (p *gapdbAssignmentProjectionProbeV2) CompensateAssignment(context.Context, tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	p.compensateCalls++
	return p.mutation, p.mutationErr
}

type gapdbEpochV2 struct{ epoch journalv2.RuntimeEpochID }

func (e gapdbEpochV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	return e.epoch, nil
}

func TestEffectAuthorityAdmitsOnlyExactAtomicSemanticPlanV2(t *testing.T) {
	plan := gapdbEffectPlanFixtureV2(t)
	sealed, _ := plan.Publication()
	if !effectPlanMembershipV2(sealed) {
		t.Fatal("valid effect plan rejected")
	}
	port := &recordingAuthorityPortV2{}
	authority := newEffectAuthorityV2ForTest(port)
	_, err := authority.PublishEffect(t.Context(), plan)
	if port.calls != 1 || port.plan.Digest() != sealed.Digest() {
		t.Fatalf("delegation calls=%d err=%v", port.calls, err)
	}
	if _, err := authority.PublishEffect(t.Context(), execution.EffectAuthorityPlanV2{}); !IsKind(err, OutcomeInvalid) || port.calls != 1 {
		t.Fatalf("unminted plan accepted calls=%d err=%v", port.calls, err)
	}
	mutantPort := &splitBatchMutantPortV2{}
	_, _ = newEffectAuthorityV2ForTest(mutantPort).PublishEffect(t.Context(), plan)
	if mutantPort.calls != sealed.MutationCount() || mutantPort.calls <= 1 {
		t.Fatalf("M-SPLIT-BATCH did not execute: calls=%d mutations=%d", mutantPort.calls, sealed.MutationCount())
	}
	attempt := gapdbEffectAttemptPlanFixtureV2(t)
	attemptSealed, err := attempt.Publication()
	if err != nil || attempt.ValidatePublicationV2() != nil || !effectPlanMembershipV2(attemptSealed) {
		t.Fatalf("production rejected attempt capability: %v", err)
	}
	adapterPolicyMutant := func(candidate journalv2.SealedCommitPlanV2) bool {
		for _, event := range candidate.Events() {
			if event.Family() == "external_effect" && event.EventType() != "external_effect.record_intent" {
				return false
			}
		}
		return effectPlanMembershipV2(candidate)
	}
	if adapterPolicyMutant(attemptSealed) {
		t.Fatal("M-ADAPTER-POLICY-INTERPRETATION did not execute")
	}
	ids := []string{"M-ADAPTER-POLICY-INTERPRETATION", "M-SPLIT-BATCH"}
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	if got := fmt.Sprintf("%x", sum); got != "011a6a6fe71480f358f09461e45a0f76576551ee8a00719c634874811854c412" {
		t.Fatalf("mutant census count=%d digest=%s", len(ids), got)
	}
}

func TestWP07RequiredMutantManifestV2(t *testing.T) {
	type executableMutant struct {
		id   string
		kill func(*testing.T)
	}
	mutants := []executableMutant{
		{"M-ADAPTER-POLICY-INTERPRETATION", killAdapterPolicyInterpretationMutantV2},
		{"M-BLIND-RETRY", killBlindRetryMutantV2},
		{"M-EPOCH-FIRST-REPLAY", killEpochFirstReplayMutantV2},
		{"M-NAKED-INTENT", killNakedIntentMutantV2},
		{"M-OPTIMISTIC-EFFECT-COMPLETION", killOptimisticEffectCompletionMutantV2},
		{"M-PROCESS-ONLY-IDENTITY", func(t *testing.T) { killExecutionIdentityMutantV2(t, false) }},
		{"M-RESULT-BEFORE-DURABLE-ACK", killResultBeforeDurableAckMutantV2},
		{"M-SPLIT-BATCH", killSplitBatchMutantV2},
		{"M-STALE-LEASE", func(t *testing.T) { killExecutionIdentityMutantV2(t, true) }},
		{"M-UNSAFE-COMPENSATION", killUnsafeCompensationMutantV2},
	}
	ids := make([]string, len(mutants))
	for index, mutant := range mutants {
		if mutant.kill == nil {
			t.Fatalf("required mutant %s has no executable kill", mutant.id)
		}
		ids[index] = mutant.id
		t.Run(mutant.id, mutant.kill)
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("required mutant manifest is not canonical: %v", ids)
	}
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	if got := fmt.Sprintf("%x", sum); got != "08dfc5a22736400f64ada33a547ba1b828df8258cb9b82e6aa01508cdff5dc6d" {
		t.Fatalf("required mutant manifest count=%d digest=%s", len(ids), got)
	}
}

func killAdapterPolicyInterpretationMutantV2(t *testing.T) {
	t.Helper()
	plan := gapdbEffectAttemptPlanFixtureV2(t)
	sealed, err := plan.Publication()
	if err != nil || plan.ValidatePublicationV2() != nil || !effectPlanMembershipV2(sealed) {
		t.Fatalf("production transport contract rejected valid attempt: %v", err)
	}
	policyMutantAdmits := true
	for _, event := range sealed.Events() {
		if event.Family() == "external_effect" && event.EventType() != "external_effect.record_intent" {
			policyMutantAdmits = false
		}
	}
	if policyMutantAdmits {
		t.Fatal("adapter policy mutant survived the production admission contract")
	}
}

func exactAtomicPublicationContractV2(calls int, acknowledged bool) bool {
	return calls == 1 && acknowledged
}

func killSplitBatchMutantV2(t *testing.T) {
	t.Helper()
	if !productionEffectPublicationContractV2(t) {
		t.Fatal("production publisher did not acknowledge one atomic batch")
	}
	plan := gapdbEffectPlanFixtureV2(t)
	sealed, err := plan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	mutant := &splitBatchMutantPortV2{}
	_, mutantErr := mutant.Publish(t.Context(), sealed)
	if exactAtomicPublicationContractV2(mutant.calls, mutantErr == nil) {
		t.Fatal("split-batch mutant satisfied the one-batch acknowledgement contract")
	}
}

func productionEffectPublicationContractV2(t *testing.T) bool {
	t.Helper()
	executionCapability, slot, _, _ := gapdbExecutionAdmissionPlanFixtureV2(t, "mutant-atomic", "work-mutant-atomic")
	executionPlan, _ := executionCapability.Publication()
	effectCapability := gapdbEffectPlanFixtureForSlotV2(t, "mutant-atomic", slot, 18)
	memory := newV2AtomicMemory()
	memory.revision = 17
	for _, guard := range executionPlan.EligibilityGuards() {
		key, err := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
		if err != nil {
			t.Fatal(err)
		}
		memory.records[key.String()] = gapdbapi.NewRecord(key.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
	}
	provider := memory.provider()
	provider.statusFn = gapdbMemoryStatusV2(memory, "db-mutant-atomic")
	adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := gapdbExecutionProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	if err != nil {
		t.Fatal(err)
	}
	executionAuthority, _ := newExecutionAuthorityV2(adapter, coordinator)
	effectAuthority, _ := newEffectAuthorityV2(adapter, coordinator)
	if _, err := executionAuthority.PublishExecution(t.Context(), executionCapability); err != nil {
		t.Fatal(err)
	}
	before := memory.batchCalls
	_, err = effectAuthority.PublishEffect(t.Context(), effectCapability)
	return exactAtomicPublicationContractV2(memory.batchCalls-before, err == nil)
}

type gapdbCountingEpochV2 struct {
	epoch journalv2.RuntimeEpochID
	calls int
}

func (e *gapdbCountingEpochV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	e.calls++
	return e.epoch, nil
}

func killEpochFirstReplayMutantV2(t *testing.T) {
	t.Helper()
	plan, _, request, _ := gapdbExecutionAdmissionPlanFixtureV2(t, "mutant-replay", "work-mutant-replay")
	sealed, _ := plan.Publication()
	probe := &gapdbExecutionPlanProbeV2{resolution: execution.ExecutionOperationResolutionV2{Kind: execution.ExecutionOperationExactV2, State: sealed.OperationState()}}
	epoch := &gapdbCountingEpochV2{epoch: "epoch-rotated"}
	service, err := execution.NewExecutionAuthorityServiceWithFenceSourceV2(probe, epoch, gapdbFenceSourceV2("mutant-replay"), journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(t.Context(), request)
	if err != nil || !result.Replayed || epoch.calls != 0 {
		t.Fatalf("epoch-first replay mutant survived: result=%+v epoch_calls=%d err=%v", result, epoch.calls, err)
	}
}

func killExecutionIdentityMutantV2(t *testing.T, staleFence bool) {
	t.Helper()
	_, active, request, _ := gapdbExecutionAdmissionPlanFixtureV2(t, "mutant-identity", "work-mutant-identity")
	heartbeat := executionRequestFromStateV2(request, active, execution.ExecutionHeartbeatV2, "mutant-identity-heartbeat")
	heartbeat.ObservedAt = request.ObservedAt.Add(time.Minute)
	heartbeat.Deadline = request.Deadline.Add(time.Minute)
	if staleFence {
		heartbeat.Fence = "stale-mutant-fence-0123456789"
	} else {
		heartbeat.ActorID = "actor-process-only-mutant"
	}
	probe := &gapdbExecutionPlanProbeV2{observation: execution.ExecutionSlotObservationV2{State: &active, RecordRevision: 17}}
	service, err := execution.NewExecutionAuthorityServiceWithFenceSourceV2(probe, gapdbEpochV2{active.RuntimeEpochID}, gapdbFenceSourceV2("mutant-identity"), journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Execute(t.Context(), heartbeat); !execution.IsKind(err, execution.KindStaleAuthority) {
		t.Fatalf("execution identity mutant survived: %v", err)
	}
}

type gapdbEffectServiceHarnessV2 struct {
	service    *execution.EffectAuthorityServiceV2
	request    execution.EffectAuthorityRequestV2
	projection *gapdbAssignmentProjectionProbeV2
}

func newGapdbEffectServiceHarnessV2(t *testing.T, suffix string, guarantee tracker.AssignmentLevel) gapdbEffectServiceHarnessV2 {
	t.Helper()
	executionCapability, _, executionRequest, _ := gapdbExecutionAdmissionPlanFixtureV2(t, suffix, "work-"+suffix)
	executionPlan, _ := executionCapability.Publication()
	memory := newV2AtomicMemory()
	memory.revision = 17
	for _, guard := range executionPlan.EligibilityGuards() {
		key, err := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
		if err != nil {
			t.Fatal(err)
		}
		memory.records[key.String()] = gapdbapi.NewRecord(key.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
	}
	provider := memory.provider()
	provider.statusFn = gapdbMemoryStatusV2(memory, "db-"+suffix)
	adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	projectionTracker, err := gapdbExecutionProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projectionTracker)
	if err != nil {
		t.Fatal(err)
	}
	executionAuthority, _ := newExecutionAuthorityV2(adapter, coordinator)
	executionService, err := execution.NewExecutionAuthorityServiceWithFenceSourceV2(executionAuthority, gapdbEpochV2{executionRequest.RuntimeEpochID}, gapdbFenceSourceV2(suffix), journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := executionService.Execute(t.Context(), executionRequest)
	if err != nil {
		t.Fatalf("execution admission failed: %#v unwrap=%#v", err, errors.Unwrap(err))
	}
	effectAuthority, _ := newEffectAuthorityV2(adapter, coordinator)
	effectService, err := execution.NewEffectAuthorityServiceV2(effectAuthority, gapdbEpochV2{admitted.State.RuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	request := gapdbEffectRequestForSlotV2(t, suffix, admitted.State)
	request.Intent.RequiredGuarantee = guarantee
	request.Intent.CanonicalRequestDigest, err = execution.EffectRequestDigestV2(request.Intent)
	if err != nil {
		t.Fatal(err)
	}
	return gapdbEffectServiceHarnessV2{service: effectService, request: request, projection: &gapdbAssignmentProjectionProbeV2{}}
}

func TestEffectDispatchAuthorizationIsDurableOneShotThroughGapDBV2(t *testing.T) {
	harness := newGapdbEffectServiceHarnessV2(t, "one-shot", tracker.AssignmentAtomicClaim)
	intent, err := harness.service.Execute(t.Context(), harness.request)
	if err != nil {
		t.Fatal(err)
	}
	request := harness.request
	request.ExecutionSlot = intent.ExecutionSlot
	request.Action = execution.EffectStartAttemptV2
	request.OperationEventID = "event-one-shot-attempt-operation"
	request.EffectEventID = "event-one-shot-attempt-effect"

	absent := gapdbAssignmentObservationV2(t, request.Intent.Target, 1, "", "")
	harness.projection.observation = absent
	harness.projection.mutationErr = &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "lost response"}
	dispatcher, err := execution.NewAssignmentEffectDispatcherV2(harness.projection)
	if err != nil {
		t.Fatal(err)
	}
	command := execution.EffectDispatchCommandV2{
		Authorization:               request,
		ObservationOperationEventID: "event-one-shot-unknown-operation",
		ObservationEffectEventID:    "event-one-shot-unknown-effect",
		ObservationAt:               request.ObservedAt.Add(time.Second),
	}
	unknown, err := harness.service.ExecuteDispatchV2(t.Context(), command, dispatcher)
	if !execution.IsKind(err, execution.KindDecisionRequired) || unknown.State.Stage != execution.EffectObservationRecordedV2 || unknown.State.Observation == nil || unknown.State.Observation.Outcome != "unknown" || harness.projection.observeCalls != 1 || harness.projection.applyCalls != 1 {
		t.Fatalf("first dispatch result=%+v observes=%d applies=%d err=%v", unknown, harness.projection.observeCalls, harness.projection.applyCalls, err)
	}

	// A copied authorization is no longer authority after its attempt and
	// observation publications. It must be rejected before another provider
	// observation or mutation, even when the provider has recovered.
	harness.projection.mutationErr = nil
	beforeObserve, beforeApply := harness.projection.observeCalls, harness.projection.applyCalls
	if _, err := harness.service.ExecuteDispatchV2(t.Context(), command, dispatcher); err == nil {
		t.Fatal("copied dispatch authorization was reusable")
	}
	if harness.projection.observeCalls != beforeObserve || harness.projection.applyCalls != beforeApply {
		t.Fatalf("reused authorization reached provider: observes=%d applies=%d", harness.projection.observeCalls-beforeObserve, harness.projection.applyCalls-beforeApply)
	}

	request.ExecutionSlot = unknown.ExecutionSlot
	request.Action = execution.EffectRequireReconcileV2
	request.OperationEventID = "event-one-shot-reconcile-unknown-operation"
	request.EffectEventID = "event-one-shot-reconcile-unknown-effect"
	reconciling, err := harness.service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionSlot = reconciling.ExecutionSlot
	request.Action = execution.EffectRecordObservationV2
	request.OperationEventID = "event-one-shot-absent-operation"
	request.EffectEventID = "event-one-shot-absent-effect"
	request.Observation = effectObservationFromAssignmentForGapDBV2(t, request.Intent.Target, absent)
	observed, err := harness.service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionSlot = observed.ExecutionSlot
	request.Action = execution.EffectRequireReconcileV2
	request.OperationEventID = "event-one-shot-reconcile-absent-operation"
	request.EffectEventID = "event-one-shot-reconcile-absent-effect"
	request.Observation = nil
	reconciled, err := harness.service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	request.ExecutionSlot = reconciled.ExecutionSlot
	request.Action = execution.EffectAllowRetryV2
	request.OperationEventID = "event-one-shot-retry-operation"
	request.EffectEventID = "event-one-shot-retry-effect"
	request.Observation = observed.State.Observation
	request.PermissionMarker = string(request.Intent.OperationID)
	appliedTarget := request.Intent.Target
	appliedTarget.LastObservation = 2
	applied := gapdbAssignmentObservationV2(t, appliedTarget, 2, request.Intent.IntendedValue, string(request.Intent.OperationID))
	harness.projection.observation = absent
	harness.projection.mutation = tracker.AssignmentMutation{Observation: applied, Guarantee: tracker.AssignmentAtomicClaim}
	retry, err := harness.service.ExecuteDispatchV2(t.Context(), execution.EffectDispatchCommandV2{
		Authorization:               request,
		ObservationOperationEventID: "event-one-shot-applied-operation",
		ObservationEffectEventID:    "event-one-shot-applied-effect",
		ObservationAt:               request.ObservedAt.Add(2 * time.Second),
	}, dispatcher)
	if err != nil || retry.State.AppliedObservation == nil || harness.projection.applyCalls != beforeApply+1 {
		t.Fatalf("journaled retry result=%+v applies=%d err=%v", retry, harness.projection.applyCalls, err)
	}
}

func effectObservationFromAssignmentForGapDBV2(t *testing.T, target tracker.ExternalWorkRef, observed tracker.AssignmentObservation) *execution.EffectProviderObservationV2 {
	t.Helper()
	probe := &gapdbAssignmentProjectionProbeV2{observation: observed}
	dispatcher, err := execution.NewAssignmentEffectDispatcherV2(probe)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := dispatcher.ObserveEffect(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	return &observation
}

func killNakedIntentMutantV2(t *testing.T) {
	harness := newGapdbEffectServiceHarnessV2(t, "mutant-naked", tracker.AssignmentAtomicClaim)
	result, err := harness.service.Execute(t.Context(), harness.request)
	if err != nil || result.State.Stage != execution.EffectIntentRecordedV2 || harness.projection.observeCalls != 0 || harness.projection.applyCalls != 0 {
		t.Fatalf("naked intent mutant survived: result=%+v err=%v", result, err)
	}
}

func killOptimisticEffectCompletionMutantV2(t *testing.T) {
	harness := newGapdbEffectServiceHarnessV2(t, "mutant-optimistic", tracker.AssignmentAtomicClaim)
	intent, err := harness.service.Execute(t.Context(), harness.request)
	if err != nil {
		t.Fatal(err)
	}
	request := harness.request
	request.ExecutionSlot = intent.ExecutionSlot
	request.Action = execution.EffectResolveV2
	request.Outcome = execution.EffectOutcomeAppliedV2
	request.OperationEventID, request.EffectEventID = "event-mutant-optimistic-operation", "event-mutant-optimistic-effect"
	if _, err := harness.service.Execute(t.Context(), request); !execution.IsKind(err, execution.KindDecisionRequired) {
		t.Fatalf("optimistic completion mutant survived: %v", err)
	}
}

func killResultBeforeDurableAckMutantV2(t *testing.T) {
	harness := newGapdbEffectServiceHarnessV2(t, "mutant-result-before-ack", tracker.AssignmentAtomicClaim)
	dispatcher, err := execution.NewAssignmentEffectDispatcherV2(harness.projection)
	if err != nil {
		t.Fatal(err)
	}
	_, err = harness.service.ExecuteDispatchV2(t.Context(), execution.EffectDispatchCommandV2{Authorization: harness.request, ObservationOperationEventID: "event-mutant-no-ack-observation-operation", ObservationEffectEventID: "event-mutant-no-ack-observation-effect", ObservationAt: harness.request.ObservedAt.Add(time.Second)}, dispatcher)
	if !execution.IsKind(err, execution.KindDecisionRequired) || harness.projection.observeCalls != 0 || harness.projection.applyCalls != 0 {
		t.Fatalf("result-before-ack mutant survived: observe=%d apply=%d err=%v", harness.projection.observeCalls, harness.projection.applyCalls, err)
	}
}

func killBlindRetryMutantV2(t *testing.T) {
	harness := newGapdbEffectServiceHarnessV2(t, "mutant-blind", tracker.AssignmentAtomicClaim)
	intent, err := harness.service.Execute(t.Context(), harness.request)
	if err != nil {
		t.Fatal(err)
	}
	request := harness.request
	request.ExecutionSlot = intent.ExecutionSlot
	request.Action = execution.EffectStartAttemptV2
	request.OperationEventID, request.EffectEventID = "event-mutant-blind-attempt-operation", "event-mutant-blind-attempt-effect"
	harness.projection.observation = gapdbAssignmentObservationV2(t, request.Intent.Target, 2, "human-owner", "human-marker")
	dispatcher, _ := execution.NewAssignmentEffectDispatcherV2(harness.projection)
	_, err = harness.service.ExecuteDispatchV2(t.Context(), execution.EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-mutant-blind-observation-operation", ObservationEffectEventID: "event-mutant-blind-observation-effect", ObservationAt: request.ObservedAt.Add(time.Second)}, dispatcher)
	if !execution.IsKind(err, execution.KindDecisionRequired) || harness.projection.applyCalls != 0 {
		t.Fatalf("blind retry mutant survived: apply=%d err=%v", harness.projection.applyCalls, err)
	}
}

func killUnsafeCompensationMutantV2(t *testing.T) {
	harness := newGapdbEffectServiceHarnessV2(t, "mutant-compensation", tracker.AssignmentUnguarded)
	intent, err := harness.service.Execute(t.Context(), harness.request)
	if err != nil {
		t.Fatal(err)
	}
	request := harness.request
	request.ExecutionSlot = intent.ExecutionSlot
	request.Action = execution.EffectStartAttemptV2
	request.OperationEventID, request.EffectEventID = "event-mutant-compensation-attempt-operation", "event-mutant-compensation-attempt-effect"
	expected := gapdbAssignmentObservationV2(t, request.Intent.Target, 1, "", "")
	appliedTarget := request.Intent.Target
	appliedTarget.LastObservation = 2
	applied := gapdbAssignmentObservationV2(t, appliedTarget, 2, request.Intent.IntendedValue, string(request.Intent.OperationID))
	harness.projection.observation = expected
	harness.projection.mutation = tracker.AssignmentMutation{Observation: applied, Guarantee: tracker.AssignmentAtomicClaim}
	dispatcher, _ := execution.NewAssignmentEffectDispatcherV2(harness.projection)
	observed, err := harness.service.ExecuteDispatchV2(t.Context(), execution.EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-mutant-compensation-observation-operation", ObservationEffectEventID: "event-mutant-compensation-observation-effect", ObservationAt: request.ObservedAt.Add(time.Second)}, dispatcher)
	if err != nil || observed.State.AppliedObservation == nil {
		t.Fatalf("failed to establish applied effect: result=%+v err=%v", observed, err)
	}
	request.ExecutionSlot = observed.ExecutionSlot
	request.Action = execution.EffectRequireReconcileV2
	request.OperationEventID, request.EffectEventID = "event-mutant-compensation-reconcile-operation", "event-mutant-compensation-reconcile-effect"
	reconciling, err := harness.service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionSlot = reconciling.ExecutionSlot
	request.Action = execution.EffectAllowCompensationV2
	request.OperationEventID, request.EffectEventID = "event-mutant-compensation-allow-operation", "event-mutant-compensation-allow-effect"
	request.Observation = reconciling.State.Observation
	request.PermissionMarker = string(request.Intent.OperationID)
	harness.projection.observation = applied
	before := harness.projection.compensateCalls
	_, err = harness.service.ExecuteDispatchV2(t.Context(), execution.EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-mutant-compensation-final-operation", ObservationEffectEventID: "event-mutant-compensation-final-effect", ObservationAt: request.ObservedAt.Add(2 * time.Second)}, dispatcher)
	if !execution.IsKind(err, execution.KindDecisionRequired) || harness.projection.compensateCalls != before {
		t.Fatalf("unsafe compensation mutant survived: compensations=%d err=%v", harness.projection.compensateCalls-before, err)
	}
}

func gapdbAssignmentObservationV2(t *testing.T, target tracker.ExternalWorkRef, revision tracker.Revision, assignee, marker string) tracker.AssignmentObservation {
	t.Helper()
	provider, err := tracker.NewProviderObservation(target.ExternalID, []byte(fmt.Sprintf("%s-%d-%s-%s", target.ExternalID, revision, assignee, marker)), revision, "", "provider-token", time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	provider.Assignee, provider.OperationMarker = assignee, marker
	target.LastObservation = revision
	return tracker.AssignmentObservation{ExternalRef: target, Assignee: assignee, OperationMarker: marker, Observation: provider}
}

func gapdbMemoryStatusV2(memory *v2AtomicMemory, databaseID string) func(context.Context) (gapdbapi.StatusResult, error) {
	return func(context.Context) (gapdbapi.StatusResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		return gapdbapi.StatusResult{DatabaseID: databaseID, CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
	}
}

func TestEffectAuthorityCommitsWholePlanThroughGapDBOnceV2(t *testing.T) {
	plan := gapdbEffectPlanFixtureV2(t)
	sealed, _ := plan.Publication()
	memory := newV2AtomicMemory()
	memory.revision = 11
	for _, head := range sealed.Heads() {
		if head.Coordinate.Kind != "execution_slot" {
			continue
		}
		key, err := v2AggregateHeadKey(head.Coordinate)
		if err != nil {
			t.Fatal(err)
		}
		memory.records[key.String()] = gapdbapi.NewRecord(key.String(), []byte(`{"seed":true}`), gapdbapi.Revision(head.ExpectedRecordRevision), nil)
	}
	adapter, err := newAdapter(memory.provider(), "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := adapter.CommitV2(t.Context(), sealed)
	if err != nil {
		t.Fatal(err)
	}
	if observation.PublicationID() != sealed.PublicationID() || memory.batchCalls != 1 {
		t.Fatalf("publication=%s calls=%d", observation.PublicationID(), memory.batchCalls)
	}
	for _, record := range sealed.Records() {
		key, err := v2KeyForSealedRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := memory.records[key.String()]; !ok {
			t.Fatalf("missing committed record %s", key.String())
		}
	}
}

func TestExecutionAndEffectAuthoritiesProductionConstructorsCommitAndRecoverAmbiguityV2(t *testing.T) {
	for _, test := range []struct {
		name  string
		fault func(gapdbapi.MutationResult) (gapdbapi.MutationResult, error)
	}{
		{name: "acknowledged"},
		{name: "dropped_ack", fault: func(_ gapdbapi.MutationResult) (gapdbapi.MutationResult, error) {
			return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: errors.New("dropped acknowledgement")}
		}},
		{name: "timed_out_ack", fault: func(_ gapdbapi.MutationResult) (gapdbapi.MutationResult, error) {
			return gapdbapi.MutationResult{}, context.DeadlineExceeded
		}},
		{name: "cancelled_ack", fault: func(_ gapdbapi.MutationResult) (gapdbapi.MutationResult, error) {
			return gapdbapi.MutationResult{}, context.Canceled
		}},
		{name: "eof_ack", fault: func(_ gapdbapi.MutationResult) (gapdbapi.MutationResult, error) {
			return gapdbapi.MutationResult{}, io.EOF
		}},
		{name: "malformed_ack", fault: func(applied gapdbapi.MutationResult) (gapdbapi.MutationResult, error) {
			applied.MutationCount--
			return applied, nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			executionCapability, slot, _, _ := gapdbExecutionAdmissionPlanFixtureV2(t, "production-"+test.name, "work-production-"+test.name)
			executionPlan, err := executionCapability.Publication()
			if err != nil {
				t.Fatal(err)
			}
			capability := gapdbEffectPlanFixtureForSlotV2(t, "production-"+test.name, slot, 18)
			sealed, err := capability.Publication()
			if err != nil {
				t.Fatal(err)
			}
			memory := newV2AtomicMemory()
			memory.revision = 17
			for _, guard := range executionPlan.EligibilityGuards() {
				key, keyErr := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
				if keyErr != nil {
					t.Fatal(keyErr)
				}
				memory.records[key.String()] = gapdbapi.NewRecord(key.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
			}
			provider := memory.provider()
			if test.fault != nil {
				commit := provider.batchFn
				provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
					applied, _ := commit(ctx, batch)
					return test.fault(applied)
				}
				provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
					memory.mu.Lock()
					defer memory.mu.Unlock()
					return gapdbapi.StatusResult{DatabaseID: "db-effect-production", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
				}
			}
			adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			projection, err := gapdbExecutionProjectionTrackerV2()
			if err != nil {
				t.Fatal(err)
			}
			coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
			if err != nil {
				t.Fatal(err)
			}
			authority, err := newEffectAuthorityV2(adapter, coordinator)
			if err != nil {
				t.Fatal(err)
			}
			executionAuthority, err := newExecutionAuthorityV2(adapter, coordinator)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := executionAuthority.PublishExecution(t.Context(), executionCapability); err != nil {
				t.Fatal(err)
			}
			ack, err := authority.PublishEffect(t.Context(), capability)
			if err != nil {
				t.Fatal(err)
			}
			if coordinator.UnresolvedCount() != 0 || memory.batchCalls != 2 {
				t.Fatalf("unresolved=%d batch_calls=%d ack=%+v", coordinator.UnresolvedCount(), memory.batchCalls, ack)
			}
			prefix, err := coordinator.SealProjectionPrefix()
			if err != nil || prefix.CoveredThroughPublicationID() != sealed.PublicationID() {
				t.Fatalf("covered=%s want=%s err=%v", prefix.CoveredThroughPublicationID(), sealed.PublicationID(), err)
			}
		})
	}
}

func gapdbExecutionProjectionTrackerV2() (*journalv2.ProjectionTrackerV2, error) {
	registry := journalv2.DefaultEventRegistryV2()
	owned := append(execution.ExecutionReducerRegistrationsV2(), execution.EffectReducerRegistrationsV2()...)
	byFamily := make(map[string]journalv2.ReducerRegistration, len(owned))
	for _, registration := range owned {
		byFamily[registration.Family] = registration
	}
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		if tuple.Family == "operation" {
			registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: journalv2.OperationReducerImplementationDigestV2(), Reduce: journalv2.ReduceOperationStateV2})
			continue
		}
		if registration, ok := byFamily[tuple.Family]; ok {
			registrations = append(registrations, registration)
			continue
		}
		sum := sha256.Sum256([]byte("test-only-unused-reducer/" + tuple.Family))
		digest, _ := journalv2.ParseDigest("test_reducer", fmt.Sprintf("%x", sum))
		registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: digest, Reduce: func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil }})
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		return nil, err
	}
	return journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
}

func TestEffectAuthoritySameSlotAndUnrelatedSlotContentionCampaignV2(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		left := gapdbEffectPlanFixtureNamedV2(t, fmt.Sprintf("left-%03d", trial), "work-shared")
		right := gapdbEffectPlanFixtureNamedV2(t, fmt.Sprintf("right-%03d", trial), "work-shared")
		leftSealed, _ := left.Publication()
		rightSealed, _ := right.Publication()
		memory := newV2AtomicMemory()
		seedEffectExecutionHeadsV2(t, memory, leftSealed, rightSealed)
		adapter, _ := newAdapter(memory.provider(), "/explicit/socket", time.Second)
		errs := commitEffectPlansTogetherV2(t, adapter, leftSealed, rightSealed)
		winners := 0
		var winner journalv2.SealedCommitPlanV2
		for index, err := range errs {
			if err == nil {
				winners++
				winner = []journalv2.SealedCommitPlanV2{leftSealed, rightSealed}[index]
			}
		}
		if winners != 1 {
			t.Fatalf("trial %d same-slot winners=%d errors=%v", trial, winners, errs)
		}
		var executionHead journalv2.HeadTransitionV2
		for _, head := range winner.Heads() {
			if head.Coordinate.Kind == "execution_slot" {
				executionHead = head
			}
		}
		key, _ := v2AggregateHeadKey(executionHead.Coordinate)
		if record := memory.records[key.String()]; record.Revision == 0 {
			t.Fatalf("trial %d lost winning execution authority", trial)
		}

		unrelatedLeft := gapdbEffectPlanFixtureNamedV2(t, fmt.Sprintf("unrelated-left-%03d", trial), "work-left")
		unrelatedRight := gapdbEffectPlanFixtureNamedV2(t, fmt.Sprintf("unrelated-right-%03d", trial), "work-right")
		unrelatedLeftSealed, _ := unrelatedLeft.Publication()
		unrelatedRightSealed, _ := unrelatedRight.Publication()
		memory = newV2AtomicMemory()
		seedEffectExecutionHeadsV2(t, memory, unrelatedLeftSealed, unrelatedRightSealed)
		adapter, _ = newAdapter(memory.provider(), "/explicit/socket", time.Second)
		for index, err := range commitEffectPlansTogetherV2(t, adapter, unrelatedLeftSealed, unrelatedRightSealed) {
			if err != nil {
				t.Fatalf("trial %d unrelated plan %d failed: %v", trial, index, err)
			}
		}
	}
}

func TestAuthorityAdapterReconcilesRegisteredAmbiguousPublicationV2(t *testing.T) {
	artifactPlan := gapdbArtifactPlanV2(t)
	plan, err := artifactPlan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	memory := newV2AtomicMemory()
	provider := memory.provider()
	commit := provider.batchFn
	provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		_, _ = commit(ctx, batch)
		return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
	}
	provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		return gapdbapi.StatusResult{DatabaseID: "db-authority-reconcile", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
	}
	adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := gapdbAuthorityProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Publish(t.Context(), plan); err == nil || coordinator.UnresolvedCount() != 1 {
		t.Fatalf("ambiguous publish err=%v unresolved=%d", err, coordinator.UnresolvedCount())
	}
	ack, err := reconcileRegisteredPublicationV2(t.Context(), adapter, coordinator, plan)
	if err != nil || ack.PublicationID() != plan.PublicationID() || coordinator.UnresolvedCount() != 0 {
		t.Fatalf("ack=%s unresolved=%d err=%v", ack.PublicationID(), coordinator.UnresolvedCount(), err)
	}
	prefix, err := coordinator.SealProjectionPrefix()
	if err != nil || prefix.CoveredThroughPublicationID() != plan.PublicationID() {
		t.Fatalf("prefix covered=%s err=%v", prefix.CoveredThroughPublicationID(), err)
	}
}

func TestAuthorityAdapterNeverDeliversAfterPreRegistrationRefusalV2(t *testing.T) {
	capability := gapdbEffectPlanFixtureV2(t)
	plan, _ := capability.Publication()
	batchCalls := 0
	provider := &fakeProvider{batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		batchCalls++
		return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	projection, err := gapdbAuthorityProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Publish(t.Context(), plan); err == nil || batchCalls != 1 {
		t.Fatalf("failed to establish registered publication: calls=%d err=%v", batchCalls, err)
	}
	authority, err := newEffectAuthorityV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.PublishEffect(t.Context(), capability); err == nil || batchCalls != 1 {
		t.Fatalf("pre-registration refusal reached provider: calls=%d err=%v", batchCalls, err)
	}
}

func commitEffectPlansTogetherV2(t *testing.T, adapter *Adapter, plans ...journalv2.SealedCommitPlanV2) []error {
	t.Helper()
	start := make(chan struct{})
	errs := make([]error, len(plans))
	var workers sync.WaitGroup
	workers.Add(len(plans))
	for index := range plans {
		go func(index int) {
			defer workers.Done()
			<-start
			_, errs[index] = adapter.CommitV2(t.Context(), plans[index])
		}(index)
	}
	close(start)
	workers.Wait()
	return errs
}

func seedEffectExecutionHeadsV2(t *testing.T, memory *v2AtomicMemory, plans ...journalv2.SealedCommitPlanV2) {
	t.Helper()
	memory.revision = 11
	for _, plan := range plans {
		for _, head := range plan.Heads() {
			if head.Coordinate.Kind != "execution_slot" {
				continue
			}
			key, err := v2AggregateHeadKey(head.Coordinate)
			if err != nil {
				t.Fatal(err)
			}
			memory.records[key.String()] = gapdbapi.NewRecord(key.String(), []byte(`{"seed":true}`), gapdbapi.Revision(head.ExpectedRecordRevision), nil)
		}
	}
}

func gapdbEffectPlanFixtureV2(t *testing.T) execution.EffectAuthorityPlanV2 {
	return gapdbEffectPlanFixtureNamedV2(t, "gapdb", "work-gapdb")
}

func gapdbEffectPlanFixtureNamedV2(t *testing.T, suffix string, workID string) execution.EffectAuthorityPlanV2 {
	t.Helper()
	governance := gapdbExecutionGovernanceV2(t)
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	slot := execution.ExecutionSlotStateV2{SchemaVersion: 1, ProjectID: "project-gapdb", MissionID: "mission-gapdb", WorkRevisionID: "revision-gapdb", WorkID: ids.WorkID(workID), LogicalVersion: 1, RunID: "run-gapdb", ActorID: "actor-gapdb", ProcessID: "process-gapdb", LeaseID: "lease-gapdb", Generation: 1, Fence: "gapdb-fence-0123456789", HeartbeatAt: now.Format(time.RFC3339Nano), Deadline: now.Add(time.Hour).Format(time.RFC3339Nano), RuntimeEpochID: "epoch-gapdb", Status: execution.ExecutionActiveV2, Governance: governance}
	return gapdbEffectPlanFixtureForSlotV2(t, suffix, slot, 11)
}

func gapdbEffectAttemptPlanFixtureV2(t *testing.T) execution.EffectAuthorityPlanV2 {
	t.Helper()
	initial := gapdbEffectPlanFixtureNamedV2(t, "adapter-policy", "work-adapter-policy")
	sealed, err := initial.Publication()
	if err != nil {
		t.Fatal(err)
	}
	var state execution.ExternalEffectStateV2
	var slot execution.ExecutionSlotStateV2
	for _, event := range sealed.Events() {
		if event.Family() == "external_effect" {
			state, err = execution.DecodeExternalEffectStateV2(event.Payload())
		}
	}
	for _, head := range sealed.Heads() {
		if head.Coordinate.Kind == "execution_slot" {
			slot, err = execution.DecodeExecutionSlotStateV2(head.AuthoritySummary)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	probe := &gapdbEffectPlanProbeV2{slot: slot, executionRevision: 21, resolution: execution.EffectOperationResolutionV2{Kind: execution.ExecutionOperationExactV2, State: sealed.OperationState(), RecordRevision: 21}, effect: execution.EffectStateObservationV2{State: &state, RecordRevision: 21, LastPublicationID: sealed.PublicationID()}}
	service, err := execution.NewEffectAuthorityServiceV2(probe, gapdbEpochV2{slot.RuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	observedAt, _ := time.Parse(time.RFC3339Nano, slot.HeartbeatAt)
	request := execution.EffectAuthorityRequestV2{Action: execution.EffectStartAttemptV2, Intent: state.Intent, RuntimeEpochID: slot.RuntimeEpochID, ExecutionSlot: slot, ObservedAt: observedAt.Add(2 * time.Second), OperationEventID: "event-adapter-policy-attempt-operation", EffectEventID: "event-adapter-policy-attempt-effect"}
	_, _ = service.Execute(t.Context(), request)
	if _, err := probe.plan.Publication(); err != nil {
		t.Fatal(err)
	}
	return probe.plan
}

func gapdbEffectPlanFixtureForSlotV2(t *testing.T, suffix string, slot execution.ExecutionSlotStateV2, executionRevision uint64) execution.EffectAuthorityPlanV2 {
	t.Helper()
	request := gapdbEffectRequestForSlotV2(t, suffix, slot)
	probe := &gapdbEffectPlanProbeV2{slot: slot, executionRevision: executionRevision}
	service, err := execution.NewEffectAuthorityServiceV2(probe, gapdbEpochV2{slot.RuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = service.Execute(t.Context(), request)
	if _, err := probe.plan.Publication(); err != nil {
		t.Fatal(err)
	}
	return probe.plan
}

func gapdbEffectRequestForSlotV2(t *testing.T, suffix string, slot execution.ExecutionSlotStateV2) execution.EffectAuthorityRequestV2 {
	t.Helper()
	now, err := time.Parse(time.RFC3339Nano, slot.HeartbeatAt)
	if err != nil {
		t.Fatal(err)
	}
	governance := slot.Governance
	target := tracker.ExternalWorkRef{ProjectID: slot.ProjectID, WorkID: slot.WorkID, Provider: "fake", ExternalID: "external-" + suffix, MetadataSchema: 1, CreatedByOperation: ids.OperationID("operation-create-" + suffix), LastObservation: 1}
	intent := execution.EffectIntentV2{EffectID: journalv2.EffectID("effect-" + suffix), OperationID: journalv2.OperationID("operation-effect-" + suffix), Target: target, IntendedValue: "actor-gapdb", ExpectedProviderRevision: 1, ExpectedOperationMarker: "", RequiredGuarantee: tracker.AssignmentAtomicClaim, ActorID: slot.ActorID, ProcessID: slot.ProcessID, Governance: governance, OriginRuntimeEpochID: slot.RuntimeEpochID, OriginObservedCreatedAt: now.Add(time.Second).Format(time.RFC3339)}
	intent.CanonicalRequestDigest, _ = execution.EffectRequestDigestV2(intent)
	return execution.EffectAuthorityRequestV2{Action: execution.EffectRecordIntentV2, Intent: intent, RuntimeEpochID: slot.RuntimeEpochID, ExecutionSlot: slot, ObservedAt: now.Add(time.Second), OperationEventID: journalv2.EventID("event-effect-" + suffix + "-op"), EffectEventID: journalv2.EventID("event-effect-" + suffix)}
}

func gapdbExecutionGovernanceV2(t *testing.T) execution.ExecutionGovernanceV2 {
	t.Helper()
	entries := make([]governancecontext.Entry, 0, 4)
	for _, item := range []struct {
		kind          governancecontext.EntryKind
		ref, revision string
	}{{governancecontext.KindConfig, "config", "policy-r1"}, {governancecontext.KindDoctrine, "doctrine", "doctrine-r1"}, {governancecontext.KindProfile, "profile", "profile-r1"}, {governancecontext.KindMissionType, "mission", "mission-r1"}} {
		sum := sha256.Sum256([]byte(item.ref))
		commitment, _ := governancecontext.NewCommitment(governancecontext.AlgorithmSHA256, fmt.Sprintf("%x", sum))
		entry, err := governancecontext.NewEntry(item.kind, item.ref, item.revision, commitment)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	ctx, err := governancecontext.NewContext("governance-gapdb", governancecontext.SchemaVersion, entries, time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	value, err := execution.ExecutionGovernanceFromContextV2(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
