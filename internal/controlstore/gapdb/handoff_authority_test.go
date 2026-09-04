package gapdb

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type handoffPlanCapturePortV2 struct {
	authority  handoff.AuthorityObservationV2
	plans      []handoff.AuthorityPlanV2
	state      *handoff.StateV2
	op         journalv2.OperationStateV2
	revision   uint64
	optimistic bool
}

func (p *handoffPlanCapturePortV2) ResolveHandoffOperation(context.Context, journalv2.OperationBindingV2) (handoff.OperationResolutionV2, error) {
	if p.state == nil {
		return handoff.OperationResolutionV2{Kind: handoff.OperationAbsentV2}, nil
	}
	return handoff.OperationResolutionV2{Kind: handoff.OperationExactV2, State: p.op, RecordRevision: p.revision}, nil
}
func (p *handoffPlanCapturePortV2) ObserveHandoff(context.Context, journalv2.AggregateCoordinate) (handoff.ObservationV2, error) {
	return handoff.ObservationV2{State: p.state, RecordRevision: p.revision, LastPublicationID: p.op.LastPublicationID()}, nil
}
func (p *handoffPlanCapturePortV2) ObserveHandoffAuthority(context.Context, handoff.AuthorityRequestV2) (handoff.AuthorityObservationV2, error) {
	return p.authority, nil
}
func (p *handoffPlanCapturePortV2) PublishHandoff(_ context.Context, plan handoff.AuthorityPlanV2) (handoff.AuthorityAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil {
		return handoff.AuthorityAckV2{}, err
	}
	if p.optimistic {
		return handoff.AuthorityAckV2{PublicationID: sealed.PublicationID(), ProviderRevision: 0, PlanDigest: sealed.Digest(), MutationCount: sealed.MutationCount()}, nil
	}
	p.plans = append(p.plans, plan)
	p.revision++
	p.op = sealed.OperationState()
	for _, event := range sealed.Events() {
		if event.Family() != "handoff_slot" {
			continue
		}
		var prior []byte
		if p.state != nil {
			prior = p.state.CanonicalBytes()
		}
		next, reduceErr := handoff.ReduceEventV2(prior, event)
		if reduceErr != nil {
			return handoff.AuthorityAckV2{}, reduceErr
		}
		state, decodeErr := handoff.DecodeStateV2(next)
		if decodeErr != nil {
			return handoff.AuthorityAckV2{}, decodeErr
		}
		p.state = &state
	}
	return handoff.AuthorityAckV2{PublicationID: sealed.PublicationID(), ProviderRevision: p.revision, PlanDigest: sealed.Digest(), MutationCount: sealed.MutationCount()}, nil
}

type gapdbHandoffEpochV2 journalv2.RuntimeEpochID

func (e gapdbHandoffEpochV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	return journalv2.RuntimeEpochID(e), nil
}

type gapdbCandidateVerifierV2 struct{}

func (gapdbCandidateVerifierV2) Verify(_ context.Context, scope handoff.AuthorityScope, candidate string) (handoff.CandidateEvidence, error) {
	return handoff.CandidateEvidence{Candidate: candidate, PinnedBase: scope.PinnedBase, OwnedBranch: scope.OwnedBranch, ObservedBranchHead: candidate}, nil
}
func (gapdbCandidateVerifierV2) RevalidateRefusal(context.Context, handoff.Refusal) error { return nil }

func TestHandoffGapDBPublisherCommitsOneCompleteBatchAndContendsV2(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		left := captureFirstHandoffPlanV2(t, fmt.Sprintf("left-%03d", trial), "work-handoff-contention")
		right := captureFirstHandoffPlanV2(t, fmt.Sprintf("right-%03d", trial), "work-handoff-contention")
		memory, authority := handoffPublisherHarnessV2(t, left, right)
		plans := []handoff.AuthorityPlanV2{left, right}
		errs := make([]error, 2)
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		for i := range plans {
			go func(i int) {
				defer workers.Done()
				<-start
				_, errs[i] = authority.PublishHandoff(t.Context(), plans[i])
			}(i)
		}
		close(start)
		workers.Wait()
		winners := 0
		for _, err := range errs {
			if err == nil {
				winners++
			}
		}
		if winners != 1 || memory.batchCalls != 2 {
			t.Fatalf("trial %d winners=%d calls=%d errors=%v", trial, winners, memory.batchCalls, errs)
		}
	}
}

func TestHandoffGapDBUnrelatedWorkProgressesV2(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		left := captureFirstHandoffPlanV2(t, fmt.Sprintf("unrelated-left-%03d", trial), fmt.Sprintf("work-left-%03d", trial))
		leftSealed, _ := left.Publication()
		var right handoff.AuthorityPlanV2
		for attempt := 0; attempt < 256; attempt++ {
			candidate := captureFirstHandoffPlanV2(t, fmt.Sprintf("unrelated-right-%03d-%03d", trial, attempt), fmt.Sprintf("work-right-%03d-%03d", trial, attempt))
			candidateSealed, _ := candidate.Publication()
			if !sameAuthorityEligibilityAddressV2(leftSealed, candidateSealed) {
				right = candidate
				break
			}
		}
		if _, err := right.Publication(); err != nil {
			t.Fatalf("trial %d: no disjoint eligibility shard", trial)
		}
		memory, authority := handoffPublisherHarnessV2(t, left, right)
		errs := make([]error, 2)
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		for i, plan := range []handoff.AuthorityPlanV2{left, right} {
			go func(i int, plan handoff.AuthorityPlanV2) {
				defer workers.Done()
				<-start
				_, errs[i] = authority.PublishHandoff(t.Context(), plan)
			}(i, plan)
		}
		close(start)
		workers.Wait()
		if errs[0] != nil || errs[1] != nil || memory.batchCalls != 2 {
			t.Fatalf("trial %d unrelated publication failed: %v calls=%d", trial, errs, memory.batchCalls)
		}
	}
}

func TestHandoffGapDBRegisteredLostAcknowledgementReconcilesExactPublicationV2(t *testing.T) {
	capability := captureFirstHandoffPlanV2(t, "lost-ack", "work-handoff-lost-ack")
	plan, _ := capability.Publication()
	memory := newV2AtomicMemory()
	memory.revision = 100
	seedAuthorityPlanDependenciesV2(t, memory, plan)
	provider := memory.provider()
	commit := provider.batchFn
	provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		_, _ = commit(ctx, batch)
		return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
	}
	provider.statusFn = gapdbMemoryStatusV2(memory, "db-handoff-lost-ack")
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	projection, _ := handoffReviewProjectionTrackerV2()
	coordinator, _ := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	authority := &handoffAuthorityV2{adapter: adapter, coordinator: coordinator}
	ack, err := authority.PublishHandoff(t.Context(), capability)
	if err != nil || ack.PublicationID != plan.PublicationID() || coordinator.UnresolvedCount() != 0 || memory.batchCalls != 1 {
		t.Fatalf("ack=%+v unresolved=%d calls=%d err=%v", ack, coordinator.UnresolvedCount(), memory.batchCalls, err)
	}
}

func TestHandoffGapDBPartialPublicationRemainsUnresolvedV2(t *testing.T) {
	assertHandoffPartialPublicationRejectedV2(t)
}

func assertHandoffPartialPublicationRejectedV2(t *testing.T) {
	t.Helper()
	capability := captureFirstHandoffPlanV2(t, "partial", "work-handoff-partial")
	plan, _ := capability.Publication()
	memory := newV2AtomicMemory()
	memory.revision = 100
	seedAuthorityPlanDependenciesV2(t, memory, plan)
	provider := memory.provider()
	provider.batchFn = func(_ context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		memory.batchCalls++
		memory.revision++
		mutation := batch.Mutations[0]
		memory.records[mutation.Key] = gapdbapi.NewRecord(mutation.Key, mutation.Value, memory.revision, nil)
		return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
	}
	provider.statusFn = gapdbMemoryStatusV2(memory, "db-handoff-partial")
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	projection, _ := handoffReviewProjectionTrackerV2()
	coordinator, _ := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	authority := &handoffAuthorityV2{adapter: adapter, coordinator: coordinator}
	if _, err := authority.PublishHandoff(t.Context(), capability); err == nil || coordinator.UnresolvedCount() != 1 || memory.batchCalls != 1 {
		t.Fatalf("partial publication escaped: unresolved=%d calls=%d err=%v", coordinator.UnresolvedCount(), memory.batchCalls, err)
	}
}

func sameAuthorityEligibilityAddressV2(left, right journalv2.SealedCommitPlanV2) bool {
	a, b := left.EligibilityGuards()[0], right.EligibilityGuards()[0]
	return a.MissionID == b.MissionID && a.ShardID == b.ShardID
}

func captureFirstHandoffPlanV2(t *testing.T, suffix, workID string) handoff.AuthorityPlanV2 {
	t.Helper()
	plans, _, _, _ := captureHandoffPlansV2(t, suffix, workID)
	return plans[0]
}

func captureHandoffPlansV2(t *testing.T, suffix, workID string) ([]handoff.AuthorityPlanV2, handoff.StateV2, handoff.AuthorityRequestV2, handoff.AuthorityObservationV2) {
	t.Helper()
	_, slot, executionRequest, eligibility := gapdbExecutionAdmissionPlanFixtureV2(t, "handoff-"+suffix, workID)
	claim, err := workspace.WorkspacePathClaimDigestV2("repository-handoff", "/srv/spec-kitty", "/srv/spec-kitty/"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	binding := workspace.BindingV2{WorkspaceID: ids.WorkspaceID("workspace-" + suffix), ProjectID: slot.ProjectID, MissionID: slot.MissionID, WorkRevisionID: slot.WorkRevisionID, WorkID: slot.WorkID, RunID: slot.RunID, LeaseID: slot.LeaseID, Generation: slot.Generation, Fence: slot.Fence, RuntimeEpochID: slot.RuntimeEpochID, ActorID: slot.ActorID, ProcessID: slot.ProcessID, RepositoryID: "repository-handoff", LaneID: ids.LaneID("lane-" + suffix), ManagedRoot: "/srv/spec-kitty", CanonicalPath: "/srv/spec-kitty/" + suffix, PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, PathClaimDigest: claim, OwnedBranch: "kitty/" + suffix, PinnedBase: strings.Repeat("a", 40)}
	workspaceState := workspace.StateV2{SchemaVersion: 1, LogicalVersion: 7, Binding: binding, Stage: workspace.StageReadyV2, Observation: workspace.ObservationV2{Registered: true, Head: binding.PinnedBase, DependencyHead: binding.PinnedBase, Branch: binding.OwnedBranch, GitDirIdentity: "gitdir-" + suffix, IndexIdentity: "index-" + suffix, Clean: true, TargetBefore: binding.PinnedBase, TargetAfter: binding.PinnedBase}, OccurredAt: executionRequest.ObservedAt.Format(time.RFC3339Nano)}
	slot.WorkspaceID, slot.WorkspacePathClaimDigest = binding.WorkspaceID, binding.PathClaimDigest
	execCoordinate, _ := execution.ExecutionSlotCoordinateV2(slot.ProjectID, slot.MissionID, slot.WorkRevisionID, slot.WorkID)
	workspaceCoordinate, _ := workspace.WorkspaceCoordinateV2(binding)
	authority := handoff.AuthorityObservationV2{Eligibility: eligibility, Execution: execution.ExecutionSlotObservationV2{State: &slot, RecordRevision: 21}, Workspace: execution.WorkspaceAuthorityObservationV2{State: &workspaceState, RecordRevision: 22}, Assertions: []journalv2.AuthorityAssertionV2{gapdbAuthorityAssertionV2(t, execCoordinate, 21, journalv2.LogicalVersion(slot.LogicalVersion), slot.CanonicalBytes()), gapdbAuthorityAssertionV2(t, workspaceCoordinate, 22, 7, workspaceState.CanonicalBytes())}}
	criterionIDs := []string{"criterion-1", "criterion-2"}
	criteriaDigest, _ := handoff.CriterionSetDigestV2(criterionIDs)
	request := handoff.AuthorityRequestV2{OperationID: journalv2.OperationID("operation-handoff-" + suffix), HandoffID: ids.HandoffID("handoff-" + suffix), ProjectID: slot.ProjectID, MissionID: slot.MissionID, WorkRevisionID: slot.WorkRevisionID, WorkID: slot.WorkID, RunID: slot.RunID, RuntimeEpochID: slot.RuntimeEpochID, Workspace: binding, LeaseID: slot.LeaseID, LeaseGeneration: slot.Generation, Fence: slot.Fence, ActorID: slot.ActorID, ProcessID: slot.ProcessID, RepositoryID: binding.RepositoryID, RepositoryPath: "/repo", LaneID: binding.LaneID, OwnedBranch: binding.OwnedBranch, PinnedBase: binding.PinnedBase, Candidate: strings.Repeat("b", 40), Contributors: []ids.ActorID{slot.ActorID, "actor-second"}, CriterionIDs: criterionIDs, CriteriaDigest: criteriaDigest, EvidenceDigest: strings.Repeat("d", 64), Governance: slot.Governance, ObservedAt: executionRequest.ObservedAt}
	capture := &handoffPlanCapturePortV2{authority: authority, revision: 40}
	service, err := handoff.NewAuthorityServiceV2(capture, gapdbHandoffEpochV2(slot.RuntimeEpochID), gapdbCandidateVerifierV2{}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Submit(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	return append([]handoff.AuthorityPlanV2(nil), capture.plans...), *capture.state, request, authority
}

func handoffPublisherHarnessV2(t *testing.T, plans ...handoff.AuthorityPlanV2) (*v2AtomicMemory, *handoffAuthorityV2) {
	t.Helper()
	memory := newV2AtomicMemory()
	memory.revision = 100
	for _, capability := range plans {
		plan, err := capability.Publication()
		if err != nil {
			t.Fatal(err)
		}
		seedAuthorityPlanDependenciesV2(t, memory, plan)
	}
	adapter, err := newAdapter(memory.provider(), "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := handoffReviewProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	if err != nil {
		t.Fatal(err)
	}
	config := workspaceRestoreConfigV2(t)
	port, err := adapter.HandoffAuthorityPortV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets)
	if err != nil {
		t.Fatal(err)
	}
	return memory, port.(*handoffAuthorityV2)
}

func seedAuthorityPlanDependenciesV2(t *testing.T, memory *v2AtomicMemory, plan journalv2.SealedCommitPlanV2) {
	t.Helper()
	for _, assertion := range plan.Assertions() {
		key, err := v2KeyForAuthorityAssertion(assertion)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := memory.records[key.String()]; !exists {
			memory.records[key.String()] = gapdbapi.NewRecord(key.String(), []byte(`{}`), gapdbapi.Revision(assertion.ObservedRevision()), nil)
		}
	}
	for _, guard := range plan.EligibilityGuards() {
		key, err := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := memory.records[key.String()]; !exists {
			memory.records[key.String()] = gapdbapi.NewRecord(key.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
		}
	}
}

func gapdbAuthorityAssertionV2(t *testing.T, coordinate journalv2.AggregateCoordinate, revision uint64, logical journalv2.LogicalVersion, value []byte) journalv2.AuthorityAssertionV2 {
	t.Helper()
	// The transport fixture deliberately uses one canonical placeholder value;
	// domain services have already checked the typed state before sealing it.
	// This lets reconciliation independently prove exact assertion bytes.
	_ = value
	digest, _ := journalv2.ReducerStateDigestV2([]byte(`{}`))
	assertion, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: journalv2.ConditionTarget{Kind: journalv2.ConditionTargetAggregateHead, Coordinate: coordinate}, Kind: journalv2.ConditionRecordRevision, ObservedRevision: revision, ExpectedLogicalVersion: logical, ObservedValueDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	return assertion
}

func handoffReviewProjectionTrackerV2() (*journalv2.ProjectionTrackerV2, error) {
	registry := journalv2.DefaultEventRegistryV2()
	owned := append(execution.ExecutionReducerRegistrationsV2(), workspace.ReducerRegistrationsV2()...)
	owned = append(owned, handoff.ReducerRegistrationsV2()...)
	owned = append(owned, review.ReducerRegistrationsV2()...)
	byFamily := map[string]journalv2.ReducerRegistration{}
	for _, registration := range owned {
		byFamily[registration.Family] = registration
	}
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		if tuple.Family == "operation" {
			registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: journalv2.OperationReducerImplementationDigestV2(), Reduce: journalv2.ReduceOperationStateV2})
		} else if registration, ok := byFamily[tuple.Family]; ok {
			registrations = append(registrations, registration)
		} else {
			digest, _ := journalv2.ParseDigest("test_reducer", fmt.Sprintf("%064x", len(registrations)+1))
			registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: digest, Reduce: func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil }})
		}
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		return nil, err
	}
	return journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
}
