package gapdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/artifact"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/checkpoint"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func wp10RestoreConfigV2(t *testing.T) WorkspaceRestoreConfigurationV2 {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	owned := append([]journalv2.ReducerRegistration{}, artifact.ReducerRegistrationsV2()...)
	owned = append(owned, execution.ExecutionReducerRegistrationsV2()...)
	owned = append(owned, execution.EffectReducerRegistrationsV2()...)
	owned = append(owned, workspace.ReducerRegistrationsV2()...)
	owned = append(owned, handoff.ReducerRegistrationsV2()...)
	owned = append(owned, review.ReducerRegistrationsV2()...)
	owned = append(owned, workgraph.ReducerRegistrationsV2()...)
	owned = append(owned, integration.AcceptanceReducerRegistrationsV2()...)
	owned = append(owned, integration.IntegrationReducerRegistrationsV2()...)
	byFamily := make(map[string]journalv2.ReducerRegistration, len(owned))
	for _, registration := range owned {
		byFamily[registration.Family] = registration
	}
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		switch tuple.Family {
		case "operation":
			registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: journalv2.OperationReducerImplementationDigestV2(), Reduce: journalv2.ReduceOperationStateV2})
		case "mission":
			registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: mission.MissionReducerImplementationDigestV2(), Reduce: mission.ReduceMissionEventV2})
		default:
			if registration, ok := byFamily[tuple.Family]; ok {
				registrations = append(registrations, registration)
				continue
			}
			// runtime_host and outbox have no v2 reducer implementation yet and
			// are deliberately absent from every WP10 publication. All shipped
			// capability families, especially acceptance and integration, use
			// their production reducers above.
			sum := sha256.Sum256([]byte("wp10-unused/" + tuple.Family))
			digest, _ := journalv2.ParseDigest("wp10_unused", fmt.Sprintf("%x", sum))
			registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: digest, Reduce: func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil }})
		}
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	return WorkspaceRestoreConfigurationV2{EventRegistry: registry, Reducers: reducers, HostAuthorityID: "host-wp10"}
}

type wp10GapDBScenarioV2 struct {
	memory            *v2AtomicMemory
	provider          *fakeProvider
	adapter           *Adapter
	coordinator       *journalv2.PublicationCoordinatorV2
	config            WorkspaceRestoreConfigurationV2
	acceptance        integration.AcceptanceResultV2
	acceptRequest     integration.AcceptanceAuthorityRequestV2
	policyFact        mission.MissionFactStateV2
	policyRevision    uint64
	policyAssertions  int
	acceptanceBatches int
	acceptanceAckLost bool
	request           integration.IntegrationAuthorityRequestV2
}

func newWP10GapDBScenarioV2(t *testing.T, suffix string, publishAcceptance bool) *wp10GapDBScenarioV2 {
	t.Helper()
	missionID, workID := "mission-wp10-"+suffix, "work-wp10-"+suffix
	workgraphPlan := gapdbWorkgraphPlanForV2(t, "wp10-"+suffix, missionID, workID)
	sealedWorkgraph, err := workgraphPlan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	var revision workgraph.RevisionV2
	var workgraphCoordinate journalv2.AggregateCoordinate
	for _, head := range sealedWorkgraph.Heads() {
		if head.Coordinate.Kind != "workgraph" {
			continue
		}
		state, decodeErr := workgraph.DecodeWorkgraphStateV2(head.AuthoritySummary)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		revision, err = workgraph.DecodeRevisionV2(state.CurrentRevision)
		if err != nil {
			t.Fatal(err)
		}
		workgraphCoordinate = head.Coordinate
	}
	if revision.ID() == "" {
		t.Fatal("workgraph plan has no revision")
	}

	memory := newV2AtomicMemory()
	provider := memory.provider()
	enableWorkspaceRecoveryScansV2(memory, provider)
	provider.statusFn = gapdbMemoryStatusV2(memory, "db-wp10-"+suffix)
	adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	config := wp10RestoreConfigV2(t)
	projection, err := journalv2.NewProjectionTrackerV2(config.Reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	if err != nil {
		t.Fatal(err)
	}
	workgraphAuthority, err := newWorkgraphAuthorityV2(coordinator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workgraphAuthority.Publish(t.Context(), workgraphPlan); err != nil {
		t.Fatalf("workgraph publication: %v", err)
	}

	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	executionRequest := execution.ExecutionAuthorityRequestV2{Kind: execution.ExecutionAdmitV2, OperationID: journalv2.OperationID("operation-wp10-execution-" + suffix), ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), WorkRevisionID: revision.ID(), WorkID: ids.WorkID(workID), RunID: ids.RunID("run-wp10-" + suffix), ActorID: ids.ActorID("actor-wp10-contributor"), ProcessID: ids.ProcessID("process-wp10-contributor-" + suffix), LeaseID: ids.LeaseID("lease-wp10-" + suffix), Generation: 1, ObservedAt: now, Deadline: now.Add(time.Hour), RuntimeEpochID: "epoch-gapdb", Governance: gapdbExecutionContextV2(t), OperationEventID: journalv2.EventID("event-operation-wp10-execution-" + suffix), SlotEventID: journalv2.EventID("event-slot-wp10-execution-" + suffix)}
	executionPort, err := newExecutionAuthorityV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	executionService, err := execution.NewExecutionAuthorityServiceWithFenceSourceV2(executionPort, gapdbEpochV2{epoch: executionRequest.RuntimeEpochID}, gapdbFenceSourceV2(executionRequest.OperationID), config.EventRegistry)
	if err != nil {
		t.Fatal(err)
	}
	executionResult, err := executionService.Execute(t.Context(), executionRequest)
	if err != nil {
		t.Fatalf("execution admission: %v", err)
	}
	slot := executionResult.State
	binding := gapdbWorkspaceBindingV2(t, slot, "workspace-wp10-"+suffix, "/srv/spec-kitty/workspaces/wp10-"+suffix)
	publishReviewWorkspaceReadyV2(t, adapter, coordinator, config, slot, binding, now)
	handoffRequest := reviewPolicyHandoffRequestV2(t, "wp10-"+suffix, revision, slot, binding, now.Add(15*time.Second))
	handoffService, err := adapter.HandoffAuthorityServiceV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets, gapdbHandoffEpochV2(handoffRequest.RuntimeEpochID), gapdbCandidateVerifierV2{})
	if err != nil {
		t.Fatal(err)
	}
	handoffResult, err := handoffService.Submit(t.Context(), handoffRequest)
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}

	reviewRequest := reviewRequestFromHandoffV2(t, "wp10-"+suffix, handoffResult.State, handoffRequest)
	reviewRequest.ReviewerActorID = ids.ActorID("actor-wp10-independent")
	reviewRequest.ReviewerProcessID = ids.ProcessID("process-wp10-independent-" + suffix)
	reviewRequest.Exception = nil

	workgraphHead, workgraphRecord, err := readAuthorityHeadV2(t.Context(), adapter, workgraphCoordinate)
	if err != nil {
		t.Fatal(err)
	}
	acceptRequest := integration.AcceptanceAuthorityRequestV2{OperationID: journalv2.OperationID("operation-wp10-acceptance-" + suffix), AcceptanceID: "acceptance-wp10-" + suffix, ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), WorkRevisionID: revision.ID(), WorkID: ids.WorkID(workID), HandoffID: handoffResult.State.HandoffID, RepositoryID: binding.RepositoryID, Candidate: handoffResult.State.Candidate, ContributorDigest: handoffResult.State.ContributorDigest, ReviewIDs: []ids.ReviewID{reviewRequest.ReviewID}, Evidence: []integration.AcceptanceEvidenceV2{{CriterionID: handoffResult.State.CriterionIDs[0], Kind: "workgraph", Disposition: integration.EvidenceSatisfiedV2, Producer: "workgraph.authority", PolicyBasis: "current work revision criterion", EvidenceID: "evidence-wp10-" + suffix, Coordinate: workgraphCoordinate, LogicalVersion: workgraphHead.LogicalVersion(), RecordRevision: workgraphRecord.RecordRevision(), StateDigestSHA256: workgraphHead.CanonicalStateDigest().String()}}, PolicyRevision: slot.Governance.PolicyRevision, PolicyFactID: "policy-wp10-" + suffix, RuntimeEpochID: slot.RuntimeEpochID, ActorID: ids.ActorID("actor-wp10-acceptor"), ProcessID: ids.ProcessID("process-wp10-acceptor-" + suffix), Governance: slot.Governance, ObservedAt: reviewRequest.ObservedAt.Add(time.Minute)}

	// The mission helper publishes the exact resolved durable decision used by
	// the acceptance adapter; its request-local exception is only a convenient
	// typed coordinate carrier and is not supplied to the independent review.
	policyRequest := reviewRequest
	policyRequest.Exception = &review.IndependenceExceptionV2{SchemaVersion: 1, ExceptionID: acceptRequest.PolicyFactID, PolicyRevision: acceptRequest.PolicyRevision, ProjectID: acceptRequest.ProjectID, MissionID: acceptRequest.MissionID, WorkRevisionID: acceptRequest.WorkRevisionID, WorkID: acceptRequest.WorkID, HandoffID: acceptRequest.HandoffID, Candidate: acceptRequest.Candidate, ReviewerActorID: reviewRequest.ReviewerActorID, Reason: "acceptance policy revision", AuthorizerActorID: ids.ActorID("actor-wp10-policy"), EffectiveFrom: acceptRequest.ObservedAt.Add(-time.Hour).Format(time.RFC3339Nano), EffectiveThrough: acceptRequest.ObservedAt.Add(time.Hour).Format(time.RFC3339Nano)}
	if err = policyRequest.Exception.Validate(); err != nil {
		t.Fatal(err)
	}
	policyFact, policyRevision := publishReviewMissionDecisionV2(t, adapter, coordinator, config, policyRequest, mission.MissionFactResolvedV2, integration.AcceptancePolicyRevisionDigestV2(acceptRequest))

	reviewService, err := adapter.ReviewAuthorityServiceV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets, gapdbHandoffEpochV2(reviewRequest.RuntimeEpochID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reviewService.Submit(t.Context(), reviewRequest); err != nil {
		t.Fatalf("review: %v", err)
	}
	acceptanceService, err := adapter.AcceptanceAuthorityServiceV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets, gapdbHandoffEpochV2(acceptRequest.RuntimeEpochID))
	if err != nil {
		t.Fatal(err)
	}
	accepted := integration.AcceptanceResultV2{}
	policyAssertions, acceptanceBatches := 0, 0
	acceptanceAckLost := false
	if publishAcceptance {
		acceptanceCoordinate, _ := journalv2.NewAggregateCoordinate("acceptance", journalv2.AggregateID(acceptRequest.AcceptanceID), journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: journalv2.ProjectID(acceptRequest.ProjectID), MissionID: journalv2.MissionID(acceptRequest.MissionID), WorkRevisionID: journalv2.WorkRevisionID(acceptRequest.WorkRevisionID), WorkID: journalv2.WorkID(acceptRequest.WorkID)})
		acceptanceKey, _ := v2AggregateHeadKey(acceptanceCoordinate)
		policyCoordinate, _ := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID("decision:"+acceptRequest.PolicyFactID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(acceptRequest.ProjectID), MissionID: journalv2.MissionID(acceptRequest.MissionID)})
		policyKey, _ := v2AggregateHeadKey(policyCoordinate)
		baseBatch := provider.batchFn
		provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
			if batchMutatesKeyV2(batch, acceptanceKey.String()) {
				acceptanceBatches++
				for _, assertion := range batch.Assertions {
					if assertion.Key == policyKey.String() && assertion.Condition.Kind == gapdbapi.ConditionRevision && uint64(assertion.Condition.ExpectedRevision) == policyRevision {
						policyAssertions++
					}
				}
			}
			result, batchErr := baseBatch(ctx, batch)
			if batchErr == nil && batchMutatesKeyV2(batch, acceptanceKey.String()) && !acceptanceAckLost {
				acceptanceAckLost = true
				return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
			}
			return result, batchErr
		}
		accepted, err = acceptanceService.Accept(t.Context(), acceptRequest)
		provider.batchFn = baseBatch
		if err != nil {
			t.Fatalf("acceptance: %v", err)
		}
	}
	request := integration.IntegrationAuthorityRequestV2{OperationID: journalv2.OperationID("operation-wp10-integration-" + suffix), AttemptID: "attempt-wp10-" + suffix, ProjectID: acceptRequest.ProjectID, MissionID: acceptRequest.MissionID, WorkRevisionID: acceptRequest.WorkRevisionID, WorkID: acceptRequest.WorkID, AcceptanceID: acceptRequest.AcceptanceID, RepositoryID: acceptRequest.RepositoryID, TargetRef: "refs/heads/main", Candidate: acceptRequest.Candidate, RuntimeEpochID: acceptRequest.RuntimeEpochID, ActorID: ids.ActorID("actor-wp10-integrator"), ProcessID: ids.ProcessID("process-wp10-integrator-" + suffix), Governance: acceptRequest.Governance, ObservedAt: acceptRequest.ObservedAt.Add(time.Minute)}
	return &wp10GapDBScenarioV2{memory: memory, provider: provider, adapter: adapter, coordinator: coordinator, config: config, acceptance: accepted, acceptRequest: acceptRequest, policyFact: policyFact, policyRevision: policyRevision, policyAssertions: policyAssertions, acceptanceBatches: acceptanceBatches, acceptanceAckLost: acceptanceAckLost, request: request}
}

type wp10GitV2 struct {
	mu           sync.Mutex
	target       string
	result       string
	fences       map[journalv2.OperationID]integration.IntegrationFenceObservationV2
	publications map[journalv2.EffectID]integration.IntegrationPublishObservationV2
	cleanups     map[journalv2.EffectID]integration.IntegrationCleanupObservationV2
	calls        int
	lostCAS      bool
	lostCleanup  bool
	conflict     bool
}

type captureAcceptancePlanPortV2 struct {
	integration.AcceptanceAuthorityPortV2
	plan integration.AcceptancePlanV2
}

func (p *captureAcceptancePlanPortV2) PublishAcceptance(_ context.Context, plan integration.AcceptancePlanV2) (integration.AcceptanceAckV2, error) {
	p.plan = plan
	return integration.AcceptanceAckV2{}, errors.New("capture acceptance plan")
}
func (p *captureAcceptancePlanPortV2) ReconcileAcceptance(context.Context, integration.AcceptancePlanV2) (integration.AcceptanceAckV2, error) {
	return integration.AcceptanceAckV2{}, errors.New("captured acceptance plan is not published")
}

type captureIntegrationPlanPortV2 struct {
	integration.IntegrationAuthorityPortV2
	plan integration.IntegrationPlanV2
}

type captureCleanupPlanPortV2 struct {
	integration.IntegrationCleanupAuthorityPortV2
	plan integration.IntegrationCleanupPlanV2
}

func (p *captureCleanupPlanPortV2) PublishIntegrationCleanup(_ context.Context, plan integration.IntegrationCleanupPlanV2) (integration.IntegrationAckV2, error) {
	p.plan = plan
	return integration.IntegrationAckV2{}, errors.New("capture cleanup plan")
}
func (p *captureCleanupPlanPortV2) ReconcileIntegrationCleanup(context.Context, integration.IntegrationCleanupPlanV2) (integration.IntegrationAckV2, error) {
	return integration.IntegrationAckV2{}, errors.New("captured cleanup plan is not published")
}

func (p *captureIntegrationPlanPortV2) PublishIntegration(_ context.Context, plan integration.IntegrationPlanV2) (integration.IntegrationAckV2, error) {
	p.plan = plan
	return integration.IntegrationAckV2{}, errors.New("capture integration plan")
}
func (p *captureIntegrationPlanPortV2) ReconcileIntegration(context.Context, integration.IntegrationPlanV2) (integration.IntegrationAckV2, error) {
	return integration.IntegrationAckV2{}, errors.New("captured integration plan is not published")
}

func newWP10GitV2() *wp10GitV2 {
	return &wp10GitV2{target: strings.Repeat("a", 40), result: strings.Repeat("c", 40), fences: map[journalv2.OperationID]integration.IntegrationFenceObservationV2{}, publications: map[journalv2.EffectID]integration.IntegrationPublishObservationV2{}, cleanups: map[journalv2.EffectID]integration.IntegrationCleanupObservationV2{}}
}
func (g *wp10GitV2) TargetHead(context.Context, ids.RepositoryID, string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return g.target, nil
}
func (g *wp10GitV2) InstallFence(_ context.Context, intent integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	result := integration.IntegrationFenceObservationV2{TokenDigest: strings.Repeat("f", 64), Generation: intent.Generation}
	g.fences[intent.OperationID] = result
	return result, nil
}
func (g *wp10GitV2) ObserveFence(_ context.Context, intent integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	result, ok := g.fences[intent.OperationID]
	return result, ok, nil
}
func (g *wp10GitV2) EnsureWorkspace(_ context.Context, intent integration.IntegrationWorkspaceIntentV2) (integration.WorkspaceFacts, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return integration.WorkspaceFacts{Path: "/managed/wp10", Registered: true, Detached: true, Clean: true, Head: intent.ExpectedOld, CandidateReachable: true}, nil
}
func (g *wp10GitV2) MergeCandidate(_ context.Context, intent integration.IntegrationMergeIntentV2, workspace integration.WorkspaceFacts) (string, *integration.ConflictEvidence, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.conflict {
		return "", &integration.ConflictEvidence{Base: intent.ExpectedOld, Candidate: intent.Candidate, Workspace: workspace.Path, ConflictFiles: []string{"conflict.go"}}, errors.New("merge conflict")
	}
	return g.result, nil, nil
}
func (g *wp10GitV2) CompareAndSwapTarget(_ context.Context, intent integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.target != intent.ExpectedOld {
		return integration.IntegrationPublishObservationV2{}, errors.New("target moved")
	}
	g.target = intent.Result
	observation := integration.IntegrationPublishObservationV2{EffectID: intent.EffectID, OperationID: intent.OperationID, RepositoryID: intent.RepositoryID, TargetRef: intent.TargetRef, ExpectedOld: intent.ExpectedOld, Result: intent.Result, ObservedTarget: intent.Result, Generation: intent.Generation, FenceTokenHash: intent.FenceTokenHash, ProviderMarker: "wp10-cas:" + string(intent.EffectID)}
	g.publications[intent.EffectID] = observation
	if g.lostCAS {
		g.lostCAS = false
		return integration.IntegrationPublishObservationV2{}, errors.New("lost compare-and-swap acknowledgement")
	}
	return observation, nil
}
func (g *wp10GitV2) ObserveTargetPublication(_ context.Context, intent integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	result, ok := g.publications[intent.EffectID]
	return result, ok, nil
}
func (g *wp10GitV2) CleanupWorkspace(_ context.Context, intent integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	observation := integration.IntegrationCleanupObservationV2{EffectID: intent.EffectID, OperationID: intent.OperationID, AttemptID: intent.AttemptID, RepositoryID: intent.RepositoryID, TargetRef: intent.TargetRef, WorkspacePath: intent.WorkspacePath, Generation: intent.Generation, FenceTokenHash: intent.FenceTokenHash, ProviderMarker: "wp10-cleanup:" + string(intent.EffectID)}
	g.cleanups[intent.EffectID] = observation
	if g.lostCleanup {
		g.lostCleanup = false
		return integration.IntegrationCleanupObservationV2{}, errors.New("lost cleanup acknowledgement")
	}
	return observation, nil
}
func (g *wp10GitV2) ObserveCleanup(_ context.Context, intent integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	result, ok := g.cleanups[intent.EffectID]
	return result, ok, nil
}
func (g *wp10GitV2) IsAncestor(context.Context, ids.RepositoryID, string, string) (bool, error) {
	return true, nil
}

func TestAcceptanceAndIntegrationPublishThroughRealGapDBAndReconcileLostCASV2(t *testing.T) {
	scenario := newWP10GapDBScenarioV2(t, "production", true)
	if scenario.acceptance.State.Stage != integration.AcceptanceGrantedV2 || scenario.acceptance.PublicationID == "" {
		t.Fatalf("acceptance result=%+v", scenario.acceptance)
	}
	if scenario.acceptanceBatches != 2 || scenario.policyAssertions != 2 || !scenario.acceptanceAckLost || scenario.coordinator.UnresolvedCount() != 0 {
		t.Fatalf("acceptance policy/ack census batches=%d assertions=%d lost=%v unresolved=%d", scenario.acceptanceBatches, scenario.policyAssertions, scenario.acceptanceAckLost, scenario.coordinator.UnresolvedCount())
	}
	origin := restoreWP10V2(t, scenario, "origin")
	assertRestoredStateV2(t, origin, "acceptance", scenario.acceptance.State.CanonicalBytes())
	prefix, err := scenario.coordinator.SealProjectionPrefix()
	if err != nil {
		t.Fatal(err)
	}
	checkpointValue, err := checkpoint.BuildV2("checkpoint-wp10-production", prefix)
	if err != nil {
		t.Fatal(err)
	}
	checkpointSlot, slotBytes, err := checkpoint.NewSlotV2("slot-wp10-production", checkpointValue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scenario.coordinator.Publish(t.Context(), workspaceCheckpointWriterPlanV2(t, checkpointValue, checkpointSlot, slotBytes)); err != nil {
		t.Fatalf("checkpoint writer: %v", err)
	}
	git := newWP10GitV2()
	git.lostCAS = true
	baseBatch := scenario.provider.batchFn
	lostAuthorityAck := false
	scenario.provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		result, err := baseBatch(ctx, batch)
		if err == nil && !lostAuthorityAck {
			lostAuthorityAck = true
			return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
		}
		return result, err
	}
	port, portErr := scenario.adapter.IntegrationAuthorityPortV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets)
	if portErr != nil {
		t.Fatal(portErr)
	}
	if observed, observeErr := port.ObserveIntegrationAuthority(t.Context(), scenario.request); observeErr != nil || observed.Acceptance.State == nil {
		t.Fatalf("integration authority observation=%+v err=%v", observed, observeErr)
	} else {
		if _, tokenErr := observed.Eligibility.Token(scenario.request.WorkID, scenario.request.WorkRevisionID, workgraph.EligibilityIntegration); tokenErr != nil {
			t.Fatalf("integration eligibility: %v", tokenErr)
		}
		state := *observed.Acceptance.State
		if state.Stage != integration.AcceptanceGrantedV2 || state.AcceptanceID != scenario.request.AcceptanceID || state.ProjectID != scenario.request.ProjectID || state.MissionID != scenario.request.MissionID || state.WorkRevisionID != scenario.request.WorkRevisionID || state.WorkID != scenario.request.WorkID || state.RepositoryID != scenario.request.RepositoryID || state.Candidate != scenario.request.Candidate {
			t.Fatalf("acceptance mismatch state=%+v request=%+v", state, scenario.request)
		}
		if len(observed.Assertions) != 1 {
			t.Fatalf("integration assertions=%d", len(observed.Assertions))
		}
		coordinate, _ := integration.IntegrationTargetCoordinateV2(scenario.request.RepositoryID, scenario.request.TargetRef)
		_ = coordinate
		acceptanceCoordinate, _ := journalv2.NewAggregateCoordinate("acceptance", journalv2.AggregateID(state.AcceptanceID), journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: journalv2.ProjectID(state.ProjectID), MissionID: journalv2.MissionID(state.MissionID), WorkRevisionID: journalv2.WorkRevisionID(state.WorkRevisionID), WorkID: journalv2.WorkID(state.WorkID)})
		assertion := observed.Assertions[0]
		if assertion.Target().Coordinate != acceptanceCoordinate || assertion.ObservedRevision() != observed.Acceptance.RecordRevision || assertion.ExpectedLogicalVersion() != journalv2.LogicalVersion(state.LogicalVersion) {
			t.Fatalf("acceptance assertion=%+v coordinate=%+v state=%+v", assertion, acceptanceCoordinate, state)
		}
	}
	service, err := scenario.adapter.IntegrationAuthorityServiceV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets, gapdbHandoffEpochV2(scenario.request.RuntimeEpochID), git)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Integrate(t.Context(), scenario.request)
	if err != nil || result.Stage != integration.IntegrationAttemptCompletedV2 || result.Result != git.result {
		var typed *integration.Error
		_ = errors.As(err, &typed)
		if typed != nil {
			t.Fatalf("integration=%+v err=%v kind=%s observed=%v expected=%v", result, err, typed.Kind, typed.Observed, typed.Expected)
		}
		t.Fatalf("integration=%+v err=%v", result, err)
	}
	if !lostAuthorityAck || scenario.coordinator.UnresolvedCount() != 0 {
		t.Fatalf("lost authority acknowledgement was not reconciled: lost=%v unresolved=%d", lostAuthorityAck, scenario.coordinator.UnresolvedCount())
	}
	if result.CleanupStatus != integration.CleanupEligible {
		t.Fatalf("terminal integration did not expose cleanup authority: %+v", result)
	}
	git.lostCleanup = true
	cleanupLostAck := false
	scenario.provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		result, err := baseBatch(ctx, batch)
		if err == nil && !cleanupLostAck {
			cleanupLostAck = true
			return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
		}
		return result, err
	}
	cleanupRequest := integration.IntegrationCleanupRequestV2{CleanupOperationID: journalv2.OperationID("operation-wp10-cleanup-production"), IntegrationOperationID: scenario.request.OperationID, ProjectID: scenario.request.ProjectID, MissionID: scenario.request.MissionID, WorkRevisionID: scenario.request.WorkRevisionID, WorkID: scenario.request.WorkID, AttemptID: scenario.request.AttemptID, RepositoryID: scenario.request.RepositoryID, TargetRef: scenario.request.TargetRef, RuntimeEpochID: scenario.request.RuntimeEpochID, ActorID: scenario.request.ActorID, ProcessID: scenario.request.ProcessID, Governance: scenario.request.Governance, ObservedAt: scenario.request.ObservedAt.Add(time.Hour)}
	cleanupPort, err := scenario.adapter.IntegrationCleanupAuthorityPortV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets)
	if err != nil {
		t.Fatal(err)
	}
	if observed, observeErr := cleanupPort.ObserveIntegrationCleanupAuthority(t.Context(), cleanupRequest); observeErr != nil {
		t.Fatalf("cleanup authority observation: %v observed=%+v", observeErr, observed)
	}
	captureCleanup := &captureCleanupPlanPortV2{IntegrationCleanupAuthorityPortV2: cleanupPort}
	captureCleanupService, err := integration.NewIntegrationCleanupServiceV2(captureCleanup, gapdbHandoffEpochV2(scenario.request.RuntimeEpochID), git, scenario.config.EventRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = captureCleanupService.Cleanup(t.Context(), cleanupRequest); err == nil {
		t.Fatal("capture cleanup service unexpectedly published")
	}
	capturedCleanup, err := captureCleanup.plan.Publication()
	if err != nil || !integrationCleanupTransportMembershipV2(capturedCleanup) || len(capturedCleanup.Assertions()) != 0 || len(capturedCleanup.EffectIntents()) != 0 {
		t.Fatalf("cleanup plan membership events=%d heads=%d conditions=%d assertions=%d effects=%d err=%v", len(capturedCleanup.Events()), len(capturedCleanup.Heads()), len(capturedCleanup.Conditions()), len(capturedCleanup.Assertions()), len(capturedCleanup.EffectIntents()), err)
	}
	cleanupService, err := scenario.adapter.IntegrationCleanupServiceV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets, gapdbHandoffEpochV2(scenario.request.RuntimeEpochID), git)
	if err != nil {
		t.Fatal(err)
	}
	cleaned, err := cleanupService.Cleanup(t.Context(), cleanupRequest)
	if err != nil || cleaned.CleanupStatus != integration.CleanupComplete || cleaned.CleanupMarker == "" || !cleanupLostAck || scenario.coordinator.UnresolvedCount() != 0 {
		t.Fatalf("cleanup=%+v lost_ack=%v unresolved=%d err=%v", cleaned, cleanupLostAck, scenario.coordinator.UnresolvedCount(), err)
	}
	beforeCleanupReplayGit := git.calls
	cleanupReplay, err := cleanupService.Cleanup(t.Context(), cleanupRequest)
	if err != nil || !bytes.Equal(cleanupReplay.CanonicalBytes(), cleaned.CanonicalBytes()) || git.calls != beforeCleanupReplayGit {
		t.Fatalf("cleanup replay=%+v err=%v git_calls=%d/%d", cleanupReplay, err, git.calls, beforeCleanupReplayGit)
	}
	beforeRestoreGit := git.calls
	restored := restoreWP10V2(t, scenario, "checkpoint-suffix-cleanup")
	if git.calls != beforeRestoreGit {
		t.Fatalf("restore called Git: before=%d after=%d", beforeRestoreGit, git.calls)
	}
	assertRestoredStateV2(t, restored, "acceptance", scenario.acceptance.State.CanonicalBytes())
	assertRestoredStateV2(t, restored, "integration_attempt", cleaned.CanonicalBytes())
	if !hasRestoredKindV2(restored, "integration_target") {
		t.Fatal("checkpoint+suffix restore omitted integration target authority")
	}
	before := git.calls
	replayed, err := service.Integrate(t.Context(), scenario.request)
	if err != nil || replayed.CanonicalBytes() == nil || git.calls != before {
		t.Fatalf("replay=%+v err=%v git_calls=%d/%d", replayed, err, git.calls, before)
	}
}

func restoreWP10V2(t *testing.T, scenario *wp10GapDBScenarioV2, suffix string) restore.ResultV2 {
	t.Helper()
	result, err := restore.RestoreV2(t.Context(), restore.InputV2{Reader: scenario.adapter, EventRegistry: scenario.config.EventRegistry, Reducers: scenario.config.Reducers, HostAuthorityID: scenario.config.HostAuthorityID, PriorRuntimeEpochID: scenario.request.RuntimeEpochID, RecoveryAttemptID: "wp10-" + suffix, Budgets: scenario.config.Budgets})
	if err != nil {
		t.Fatalf("restore %s: %v", suffix, err)
	}
	if _, err = result.FreshEpochAdmissionRequestV2(); err != nil {
		t.Fatalf("sealed restore %s: %v", suffix, err)
	}
	return result
}

func assertRestoredStateV2(t *testing.T, result restore.ResultV2, kind string, want []byte) {
	t.Helper()
	matches := 0
	for _, state := range result.Playback().States() {
		if string(state.Coordinate().Kind) != kind {
			continue
		}
		if bytes.Equal(state.CanonicalState(), want) {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("restored %s exact-state matches=%d", kind, matches)
	}
}

func hasRestoredKindV2(result restore.ResultV2, kind string) bool {
	for _, state := range result.Playback().States() {
		if string(state.Coordinate().Kind) == kind {
			return true
		}
	}
	return false
}

func TestIntegrationRepairRequiredRestoresThroughCheckpointSuffixWithoutGitV2(t *testing.T) {
	scenario := newWP10GapDBScenarioV2(t, "repair", true)
	prefix, err := scenario.coordinator.SealProjectionPrefix()
	if err != nil {
		t.Fatal(err)
	}
	checkpointValue, err := checkpoint.BuildV2("checkpoint-wp10-repair", prefix)
	if err != nil {
		t.Fatal(err)
	}
	checkpointSlot, slotBytes, err := checkpoint.NewSlotV2("slot-wp10-repair", checkpointValue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scenario.coordinator.Publish(t.Context(), workspaceCheckpointWriterPlanV2(t, checkpointValue, checkpointSlot, slotBytes)); err != nil {
		t.Fatal(err)
	}
	git := newWP10GitV2()
	git.conflict = true
	service, err := scenario.adapter.IntegrationAuthorityServiceV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets, gapdbHandoffEpochV2(scenario.request.RuntimeEpochID), git)
	if err != nil {
		t.Fatal(err)
	}
	repair, err := service.Integrate(t.Context(), scenario.request)
	if err != nil || repair.Stage != integration.IntegrationAttemptRepairRequiredV2 || repair.Conflict == nil || repair.RepairAction == "" {
		t.Fatalf("repair=%+v err=%v", repair, err)
	}
	before := git.calls
	restored := restoreWP10V2(t, scenario, "repair")
	if git.calls != before {
		t.Fatalf("repair restore called Git: %d/%d", git.calls, before)
	}
	assertRestoredStateV2(t, restored, "acceptance", scenario.acceptance.State.CanonicalBytes())
	assertRestoredStateV2(t, restored, "integration_attempt", repair.CanonicalBytes())
	if !hasRestoredKindV2(restored, "integration_target") {
		t.Fatal("repair restore omitted active integration target")
	}
}

func captureAcceptancePlanThroughProductionV2(t *testing.T, scenario *wp10GapDBScenarioV2) integration.AcceptancePlanV2 {
	t.Helper()
	base, err := scenario.adapter.AcceptanceAuthorityPortV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets)
	if err != nil {
		t.Fatal(err)
	}
	port := &captureAcceptancePlanPortV2{AcceptanceAuthorityPortV2: base}
	service, err := integration.NewAcceptanceAuthorityServiceV2(port, gapdbHandoffEpochV2(scenario.acceptRequest.RuntimeEpochID), scenario.config.EventRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Accept(t.Context(), scenario.acceptRequest); err == nil {
		t.Fatal("capture port unexpectedly acknowledged acceptance")
	}
	if _, err = port.plan.Publication(); err != nil {
		t.Fatalf("acceptance plan was not captured: %v", err)
	}
	return port.plan
}

func captureIntegrationPlanThroughProductionV2(t *testing.T, scenario *wp10GapDBScenarioV2) integration.IntegrationPlanV2 {
	t.Helper()
	base, err := scenario.adapter.IntegrationAuthorityPortV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets)
	if err != nil {
		t.Fatal(err)
	}
	port := &captureIntegrationPlanPortV2{IntegrationAuthorityPortV2: base}
	git := newWP10GitV2()
	service, err := integration.NewIntegrationAuthorityServiceV2(port, gapdbHandoffEpochV2(scenario.request.RuntimeEpochID), git, scenario.config.EventRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Integrate(t.Context(), scenario.request); err == nil {
		t.Fatal("capture port unexpectedly acknowledged integration")
	}
	if _, err = port.plan.Publication(); err != nil {
		t.Fatalf("integration plan was not captured: %v", err)
	}
	return port.plan
}

func TestAcceptanceAndIntegrationGapDBRejectPartialAndMixedPublicationsV2(t *testing.T) {
	for _, capability := range []string{"acceptance", "integration", "cleanup"} {
		for name, write := range map[string]func(*v2AtomicMemory, journalv2.SealedCommitPlanV2, gapdbapi.Batch){
			"heads_without_events":     writeAuthorityHeadsWithoutEventsV2,
			"mixed_provider_revisions": writeAuthorityMixedRevisionsV2,
		} {
			t.Run(capability+"/"+name, func(t *testing.T) {
				scenario := newWP10GapDBScenarioV2(t, capability+"-"+name, capability != "acceptance")
				var sealed journalv2.SealedCommitPlanV2
				var publish func() error
				if capability == "acceptance" {
					captured := captureAcceptancePlanThroughProductionV2(t, scenario)
					sealed, _ = captured.Publication()
					port, err := scenario.adapter.AcceptanceAuthorityPortV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets)
					if err != nil {
						t.Fatal(err)
					}
					publish = func() error { _, err := port.PublishAcceptance(t.Context(), captured); return err }
				} else if capability == "integration" {
					captured := captureIntegrationPlanThroughProductionV2(t, scenario)
					sealed, _ = captured.Publication()
					port, err := scenario.adapter.IntegrationAuthorityPortV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets)
					if err != nil {
						t.Fatal(err)
					}
					publish = func() error { _, err := port.PublishIntegration(t.Context(), captured); return err }
				} else {
					git := newWP10GitV2()
					service, err := scenario.adapter.IntegrationAuthorityServiceV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets, gapdbHandoffEpochV2(scenario.request.RuntimeEpochID), git)
					if err != nil {
						t.Fatal(err)
					}
					completed, err := service.Integrate(t.Context(), scenario.request)
					if err != nil || completed.CleanupStatus != integration.CleanupEligible {
						t.Fatalf("integration precondition=%+v err=%v", completed, err)
					}
					cleanupRequest := integration.IntegrationCleanupRequestV2{CleanupOperationID: journalv2.OperationID("operation-wp10-cleanup-" + name), IntegrationOperationID: scenario.request.OperationID, ProjectID: scenario.request.ProjectID, MissionID: scenario.request.MissionID, WorkRevisionID: scenario.request.WorkRevisionID, WorkID: scenario.request.WorkID, AttemptID: scenario.request.AttemptID, RepositoryID: scenario.request.RepositoryID, TargetRef: scenario.request.TargetRef, RuntimeEpochID: scenario.request.RuntimeEpochID, ActorID: scenario.request.ActorID, ProcessID: scenario.request.ProcessID, Governance: scenario.request.Governance, ObservedAt: scenario.request.ObservedAt.Add(time.Hour)}
					port, err := scenario.adapter.IntegrationCleanupAuthorityPortV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets)
					if err != nil {
						t.Fatal(err)
					}
					capture := &captureCleanupPlanPortV2{IntegrationCleanupAuthorityPortV2: port}
					captureService, err := integration.NewIntegrationCleanupServiceV2(capture, gapdbHandoffEpochV2(cleanupRequest.RuntimeEpochID), git, scenario.config.EventRegistry)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = captureService.Cleanup(t.Context(), cleanupRequest); err == nil {
						t.Fatal("capture cleanup unexpectedly published")
					}
					captured := capture.plan
					sealed, _ = captured.Publication()
					publish = func() error { _, err := port.PublishIntegrationCleanup(t.Context(), captured); return err }
				}
				scenario.provider.batchFn = func(_ context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
					scenario.memory.mu.Lock()
					defer scenario.memory.mu.Unlock()
					scenario.memory.batchCalls++
					write(scenario.memory, sealed, batch)
					return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
				}
				beforeCalls := scenario.memory.batchCalls
				if err := publish(); err == nil || scenario.coordinator.UnresolvedCount() != 1 || scenario.memory.batchCalls != beforeCalls+1 {
					t.Fatalf("incomplete %s publication escaped: unresolved=%d calls=%d/%d err=%v", capability, scenario.coordinator.UnresolvedCount(), scenario.memory.batchCalls, beforeCalls, err)
				}
			})
		}
	}
}

func TestAcceptanceGapDBRequiresExactCurrentPolicyDecisionBeforePublicationV2(t *testing.T) {
	for _, test := range []struct {
		name   string
		status mission.MissionFactStatusV2
		wrong  bool
		kind   integration.ErrorKind
	}{
		{name: "wrong_detail", status: mission.MissionFactResolvedV2, wrong: true, kind: integration.KindPolicyRefused},
		{name: "canceled", status: mission.MissionFactCanceledV2, kind: integration.KindEvidenceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			scenario := newWP10GapDBScenarioV2(t, "policy-"+test.name, false)
			detail := integration.AcceptancePolicyRevisionDigestV2(scenario.acceptRequest)
			if test.wrong {
				detail, _ = journalv2.ReducerStateDigestV2([]byte(`{"policy":"substituted"}`))
			}
			wrong, err := mission.NewMissionFactStateV2(mission.MissionFactStateInputV2{ProjectID: journalv2.ProjectID(scenario.acceptRequest.ProjectID), MissionID: journalv2.MissionID(scenario.acceptRequest.MissionID), FactID: scenario.acceptRequest.PolicyFactID, Kind: mission.MissionFactDecisionV2, Status: test.status, LogicalVersion: scenario.policyFact.LogicalVersion(), DetailDigest: detail, LastOperationID: journalv2.OperationID("operation-wp10-policy-" + test.name), ObservedAt: scenario.acceptRequest.ObservedAt.Add(-time.Second).Format(time.RFC3339Nano)})
			if err != nil {
				t.Fatal(err)
			}
			coordinate, _ := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID("decision:"+scenario.acceptRequest.PolicyFactID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(scenario.acceptRequest.ProjectID), MissionID: journalv2.MissionID(scenario.acceptRequest.MissionID)})
			seedReviewTypedHeadV2(t, scenario.memory, wrong.CanonicalBytes(), coordinate, scenario.policyRevision, wrong.LogicalVersion(), journalv2.PublicationID(strings.Repeat("e", 64)), scenario.acceptRequest.RuntimeEpochID)
			before := countRecordsContainingV2(scenario.memory, scenario.acceptRequest.AcceptanceID)
			service, err := scenario.adapter.AcceptanceAuthorityServiceV2(scenario.coordinator, scenario.config.EventRegistry, scenario.config.Reducers, scenario.config.HostAuthorityID, scenario.config.Budgets, gapdbHandoffEpochV2(scenario.acceptRequest.RuntimeEpochID))
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Accept(t.Context(), scenario.acceptRequest)
			if !integration.IsKind(err, test.kind) {
				t.Fatalf("policy decision refusal=%v want=%s", err, test.kind)
			}
			if after := countRecordsContainingV2(scenario.memory, scenario.acceptRequest.AcceptanceID); after != before {
				t.Fatalf("refused acceptance mutated records: before=%d after=%d", before, after)
			}
		})
	}
}

func countRecordsContainingV2(memory *v2AtomicMemory, needle string) int {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	count := 0
	for key := range memory.records {
		if strings.Contains(key, needle) {
			count++
		}
	}
	return count
}

func TestWP10GapDBCompositionRejectsPlaceholderCapabilityReducersV2(t *testing.T) {
	config := wp10RestoreConfigV2(t)
	if err := requireAuthorityReducerRegistrationsV2(config.Reducers, integration.AcceptanceReducerRegistrationsV2()); err != nil {
		t.Fatalf("production acceptance reducers: %v", err)
	}
	if err := requireAuthorityReducerRegistrationsV2(config.Reducers, integration.IntegrationReducerRegistrationsV2()); err != nil {
		t.Fatalf("production integration reducers: %v", err)
	}

	placeholder := reviewMissionRestoreConfigV2(t)
	if err := requireAuthorityReducerRegistrationsV2(placeholder.Reducers, integration.AcceptanceReducerRegistrationsV2()); err == nil {
		t.Fatal("acceptance adapter admitted a placeholder reducer")
	}
	if err := requireAuthorityReducerRegistrationsV2(placeholder.Reducers, integration.IntegrationReducerRegistrationsV2()); err == nil {
		t.Fatal("integration adapter admitted placeholder reducers")
	}
}
