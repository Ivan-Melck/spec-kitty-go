package gapdb

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type reviewPlanCapturePortV2 struct {
	authority review.AuthorityObservationV2
	plans     []review.AuthorityPlanV2
	state     *review.StateV2
	op        journalv2.OperationStateV2
	revision  uint64
	dropAckAt int
	publishes int
}

func (p *reviewPlanCapturePortV2) ResolveReviewOperation(context.Context, journalv2.OperationBindingV2) (review.OperationResolutionV2, error) {
	if p.state == nil {
		return review.OperationResolutionV2{Kind: review.OperationAbsentV2}, nil
	}
	return review.OperationResolutionV2{Kind: review.OperationExactV2, State: p.op, RecordRevision: p.revision}, nil
}
func (p *reviewPlanCapturePortV2) ObserveReview(context.Context, journalv2.AggregateCoordinate) (review.ObservationV2, error) {
	return review.ObservationV2{State: p.state, RecordRevision: p.revision, LastPublicationID: p.op.LastPublicationID()}, nil
}
func (p *reviewPlanCapturePortV2) ObserveReviewAuthority(context.Context, review.AuthorityRequestV2) (review.AuthorityObservationV2, error) {
	return p.authority, nil
}
func (p *reviewPlanCapturePortV2) PublishReview(_ context.Context, plan review.AuthorityPlanV2) (review.AuthorityAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil {
		return review.AuthorityAckV2{}, err
	}
	p.plans = append(p.plans, plan)
	p.revision++
	p.publishes++
	p.op = sealed.OperationState()
	for _, event := range sealed.Events() {
		if event.Family() != "review_slot" {
			continue
		}
		var prior []byte
		if p.state != nil {
			prior = p.state.CanonicalBytes()
		}
		next, reduceErr := review.ReduceEventV2(prior, event)
		if reduceErr != nil {
			return review.AuthorityAckV2{}, reduceErr
		}
		state, decodeErr := review.DecodeStateV2(next)
		if decodeErr != nil {
			return review.AuthorityAckV2{}, decodeErr
		}
		p.state = &state
	}
	if p.publishes == p.dropAckAt {
		return review.AuthorityAckV2{}, fmt.Errorf("simulated lost acknowledgement")
	}
	return review.AuthorityAckV2{PublicationID: sealed.PublicationID(), ProviderRevision: p.revision, PlanDigest: sealed.Digest(), MutationCount: sealed.MutationCount()}, nil
}

func TestReviewGapDBPublisherCommitsOneCompleteBatchAndContendsV2(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		left := captureFirstReviewPlanV2(t, fmt.Sprintf("left-%03d", trial), "review-shared")
		right := captureFirstReviewPlanV2(t, fmt.Sprintf("right-%03d", trial), "review-shared")
		memory, authority := reviewPublisherHarnessV2(t, left, right)
		plans := []review.AuthorityPlanV2{left, right}
		errs := make([]error, 2)
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		for i := range plans {
			go func(i int) {
				defer workers.Done()
				<-start
				_, errs[i] = authority.PublishReview(t.Context(), plans[i])
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

func TestReviewGapDBRegisteredLostAcknowledgementReconcilesExactPublicationV2(t *testing.T) {
	capability := captureFirstReviewPlanV2(t, "lost-ack", "review-lost-ack")
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
	provider.statusFn = gapdbMemoryStatusV2(memory, "db-review-lost-ack")
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	projection, _ := handoffReviewProjectionTrackerV2()
	coordinator, _ := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	authority := &reviewAuthorityV2{adapter: adapter, coordinator: coordinator}
	ack, err := authority.PublishReview(t.Context(), capability)
	if err != nil || ack.PublicationID != plan.PublicationID() || coordinator.UnresolvedCount() != 0 || memory.batchCalls != 1 {
		t.Fatalf("ack=%+v unresolved=%d calls=%d err=%v", ack, coordinator.UnresolvedCount(), memory.batchCalls, err)
	}
}

func TestReviewGapDBPartialPublicationRemainsUnresolvedV2(t *testing.T) {
	capability := captureFirstReviewPlanV2(t, "partial", "review-partial")
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
	provider.statusFn = gapdbMemoryStatusV2(memory, "db-review-partial")
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	projection, _ := handoffReviewProjectionTrackerV2()
	coordinator, _ := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	authority := &reviewAuthorityV2{adapter: adapter, coordinator: coordinator}
	if _, err := authority.PublishReview(t.Context(), capability); err == nil || coordinator.UnresolvedCount() != 1 || memory.batchCalls != 1 {
		t.Fatalf("partial publication escaped: unresolved=%d calls=%d err=%v", coordinator.UnresolvedCount(), memory.batchCalls, err)
	}
}

func captureFirstReviewPlanV2(t *testing.T, suffix, reviewID string) review.AuthorityPlanV2 {
	t.Helper()
	plans := captureReviewPlansV2(t, suffix, reviewID)
	return plans[0]
}

func captureReviewPlansV2(t *testing.T, suffix, reviewID string) []review.AuthorityPlanV2 {
	t.Helper()
	capture, _ := reviewCaptureFixtureV2(t, suffix, reviewID)
	return append([]review.AuthorityPlanV2(nil), capture.plans...)
}

func reviewCaptureFixtureV2(t *testing.T, suffix, reviewID string) (*reviewPlanCapturePortV2, review.AuthorityRequestV2) {
	t.Helper()
	_, handoffState, handoffRequest, handoffAuthority := captureHandoffPlansV2(t, "review-"+suffix, "work-review-contention")
	reviewerActor := "actor-reviewer-" + suffix
	criteria := make([]review.CriterionEvidenceV2, len(handoffState.CriterionIDs))
	for index, criterionID := range handoffState.CriterionIDs {
		criteria[index] = review.CriterionEvidenceV2{CriterionID: criterionID, Status: review.Passed, EvidenceDigest: fmt.Sprintf("%064x", index+14), ArtifactDigest: fmt.Sprintf("%064x", index+31), Candidate: handoffState.Candidate, PolicyRevision: handoffState.Governance.PolicyRevision}
	}
	request := review.AuthorityRequestV2{OperationID: journalv2.OperationID("operation-review-" + suffix), ReviewID: ids.ReviewID(reviewID), HandoffID: handoffState.HandoffID, ProjectID: handoffState.ProjectID, MissionID: handoffState.MissionID, WorkRevisionID: handoffState.WorkRevisionID, WorkID: handoffState.WorkID, RuntimeEpochID: handoffState.RuntimeEpochID, Workspace: handoffRequest.Workspace, ReviewerActorID: ids.ActorID(reviewerActor), ReviewerProcessID: ids.ProcessID("process-" + suffix), Candidate: handoffState.Candidate, HandoffContributorDigest: handoffState.ContributorDigest, Criteria: criteria, Verdict: review.Approved, PolicyRevision: handoffState.Governance.PolicyRevision, Governance: handoffState.Governance, ObservedAt: handoffRequest.ObservedAt}
	handoffCoordinate, _ := handoff.CoordinateV2(handoffState.ProjectID, handoffState.MissionID, handoffState.WorkRevisionID, handoffState.WorkID)
	assertions := append([]journalv2.AuthorityAssertionV2(nil), handoffAuthority.Assertions...)
	assertions = append(assertions, gapdbAuthorityAssertionV2(t, handoffCoordinate, 23, journalv2.LogicalVersion(handoffState.LogicalVersion), handoffState.CanonicalBytes()))
	authority := review.AuthorityObservationV2{Eligibility: handoffAuthority.Eligibility, Execution: handoffAuthority.Execution, Workspace: handoffAuthority.Workspace, Handoff: handoff.ObservationV2{State: &handoffState, RecordRevision: 23}, Assertions: assertions}
	capture := &reviewPlanCapturePortV2{authority: authority, revision: 60}
	service, err := review.NewAuthorityServiceV2(capture, gapdbHandoffEpochV2(handoffState.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Submit(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	return capture, request
}

func TestReviewGapDBTransportDoesNotInterpretVerdictPolicyV2(t *testing.T) {
	plans := captureReviewPlansV2(t, "transport-only", "review-transport")
	if len(plans) != 4 { // attempt, two criteria, terminal verdict
		t.Fatalf("plans=%d", len(plans))
	}
	for index, capability := range plans {
		plan, err := capability.Publication()
		if err != nil || !reviewTransportMembershipV2(plan) {
			t.Fatalf("transport rejected sealed domain stage %d: %v", index, err)
		}
	}
}

func TestWP09ControlledMutantCensusV2(t *testing.T) {
	kills := map[string]func(*testing.T){
		"M-WP09-SPLIT-BATCH": func(t *testing.T) { assertHandoffPartialPublicationRejectedV2(t) },
		"M-WP09-OPTIMISTIC-RESULT": func(t *testing.T) {
			_, _, request, authority := captureHandoffPlansV2(t, "mutant-optimistic", "work-mutant-optimistic")
			port := &handoffPlanCapturePortV2{authority: authority, optimistic: true}
			service, _ := handoff.NewAuthorityServiceV2(port, gapdbHandoffEpochV2(request.RuntimeEpochID), gapdbCandidateVerifierV2{}, journalv2.DefaultEventRegistryV2())
			if _, err := service.Submit(t.Context(), request); !handoff.IsKind(err, handoff.KindConflict) || len(port.plans) != 0 {
				t.Fatalf("optimistic result survived: %v", err)
			}
		},
		"M-WP09-STALE-ELIGIBILITY": func(t *testing.T) {
			_, _, request, authority := captureHandoffPlansV2(t, "mutant-eligibility", "work-mutant-eligibility")
			authority.Eligibility = workgraph.ObservedEligibilityShardV2{}
			assertHandoffAuthorityMutationRefusedV2(t, request, authority)
		},
		"M-WP09-STALE-LEASE": func(t *testing.T) {
			_, _, request, authority := captureHandoffPlansV2(t, "mutant-lease", "work-mutant-lease")
			state := *authority.Execution.State
			state.Deadline = request.ObservedAt.Format(time.RFC3339Nano)
			authority.Execution.State = &state
			assertHandoffAuthorityMutationRefusedV2(t, request, authority)
		},
		"M-WP09-PREDECESSOR-WORKSPACE": func(t *testing.T) {
			_, _, request, authority := captureHandoffPlansV2(t, "mutant-workspace", "work-mutant-workspace")
			state := *authority.Workspace.State
			state.Binding.WorkspaceID = "workspace-predecessor"
			authority.Workspace.State = &state
			assertHandoffAuthorityMutationRefusedV2(t, request, authority)
		},
		"M-WP09-MUTABLE-CANDIDATE": func(t *testing.T) {
			plans, terminal, request, _ := captureHandoffPlansV2(t, "mutant-candidate", "work-mutant-candidate")
			request.Candidate = strings.Repeat("8", 40)
			assertTerminalHandoffMutationRefusedV2(t, plans, terminal, request)
		},
		"M-WP09-PARTIAL-CONTRIBUTORS": func(t *testing.T) {
			plans, terminal, request, _ := captureHandoffPlansV2(t, "mutant-contributors", "work-mutant-contributors")
			request.Contributors = request.Contributors[:1]
			assertTerminalHandoffMutationRefusedV2(t, plans, terminal, request)
		},
		"M-WP09-PROCESS-ID-INDEPENDENCE": func(t *testing.T) {
			control, request := reviewCaptureFixtureV2(t, "mutant-process", "review-mutant-process")
			request.ReviewerActorID = control.authority.Handoff.State.Contributors[0]
			request.ReviewerProcessID = "different-process"
			assertReviewMutationRefusedV2(t, request, control.authority, review.KindReviewerNotIndependent)
		},
		"M-WP09-IMPLICIT-EXCEPTION": func(t *testing.T) {
			control, request := reviewCaptureFixtureV2(t, "mutant-exception", "review-mutant-exception")
			request.ReviewerActorID = control.authority.Handoff.State.Contributors[0]
			request.ReviewerProcessID = control.authority.Execution.State.ProcessID
			assertReviewMutationRefusedV2(t, request, control.authority, review.KindReviewerNotIndependent)
		},
		"M-WP09-PARTIAL-CRITERIA": func(t *testing.T) {
			control, request := reviewCaptureFixtureV2(t, "mutant-criteria", "review-mutant-criteria")
			request.Criteria = request.Criteria[:1]
			assertReviewMutationRefusedV2(t, request, control.authority, review.KindCandidateMismatch)
		},
		"M-WP09-CHANGED-VERDICT": func(t *testing.T) {
			control, request := reviewCaptureFixtureV2(t, "mutant-verdict", "review-mutant-verdict")
			request.Criteria[0].Status = review.Failed
			request.Verdict = review.ChangesRequested
			port := &reviewPlanCapturePortV2{authority: control.authority, state: control.state, op: control.op, revision: control.revision}
			service, _ := review.NewAuthorityServiceV2(port, gapdbHandoffEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
			if _, err := service.Submit(t.Context(), request); !review.IsKind(err, review.KindConflict) {
				t.Fatalf("changed terminal verdict replayed: %v", err)
			}
		},
		"M-WP09-DROPPED-ACK-DUPLICATION": func(t *testing.T) {
			control, request := reviewCaptureFixtureV2(t, "mutant-drop", "review-mutant-drop")
			port := &reviewPlanCapturePortV2{authority: control.authority, dropAckAt: 2, revision: 70}
			service, _ := review.NewAuthorityServiceV2(port, gapdbHandoffEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
			result, err := service.Submit(t.Context(), request)
			if err != nil || result.State.Stage != review.StageVerdictRecordedV2 || len(port.plans) != len(request.Criteria)+2 || port.publishes != len(port.plans) {
				t.Fatalf("dropped ack duplicated: result=%+v plans=%d publishes=%d err=%v", result, len(port.plans), port.publishes, err)
			}
		},
		"M-WP09-ADAPTER-POLICY-INTERPRETATION": func(t *testing.T) {
			plans := captureReviewPlansV2(t, "mutant-policy", "review-mutant-policy")
			mutantRejected := false
			for _, capability := range plans {
				plan, _ := capability.Publication()
				if !reviewTransportMembershipV2(plan) {
					t.Fatal("production transport rejected valid sealed stage")
				}
				if !policyInterpretingTransportMutantV2(plan) {
					mutantRejected = true
				}
			}
			if !mutantRejected {
				t.Fatal("policy-interpreting adapter mutant was not killed")
			}
		},
	}
	if len(kills) != 13 {
		t.Fatalf("mutant census=%d", len(kills))
	}
	ids := make([]string, 0, len(kills))
	for id, kill := range kills {
		if kill == nil {
			t.Fatalf("mutant %s lacks an executable kill", id)
		}
		ids = append(ids, id)
		t.Run(id, kill)
	}
	sort.Strings(ids)
	digest := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	const sealed = "12e11f049052fd14c79b083fe3cec2b9d80012d0db06f307d8c74fc708689201"
	if got := fmt.Sprintf("%x", digest); got != sealed {
		t.Fatalf("mutant census digest changed: %s", got)
	}
}

func assertHandoffAuthorityMutationRefusedV2(t *testing.T, request handoff.AuthorityRequestV2, authority handoff.AuthorityObservationV2) {
	t.Helper()
	port := &handoffPlanCapturePortV2{authority: authority}
	service, _ := handoff.NewAuthorityServiceV2(port, gapdbHandoffEpochV2(request.RuntimeEpochID), gapdbCandidateVerifierV2{}, journalv2.DefaultEventRegistryV2())
	if _, err := service.Submit(t.Context(), request); err == nil || len(port.plans) != 0 {
		t.Fatalf("authority mutation accepted: %v plans=%d", err, len(port.plans))
	}
}

func assertTerminalHandoffMutationRefusedV2(t *testing.T, plans []handoff.AuthorityPlanV2, terminal handoff.StateV2, request handoff.AuthorityRequestV2) {
	t.Helper()
	sealed, _ := plans[len(plans)-1].Publication()
	port := &handoffPlanCapturePortV2{state: &terminal, op: sealed.OperationState(), revision: 44}
	service, _ := handoff.NewAuthorityServiceV2(port, gapdbHandoffEpochV2(request.RuntimeEpochID), gapdbCandidateVerifierV2{}, journalv2.DefaultEventRegistryV2())
	if _, err := service.Submit(t.Context(), request); !handoff.IsKind(err, handoff.KindConflict) {
		t.Fatalf("terminal mutation replayed: %v", err)
	}
}

func assertReviewMutationRefusedV2(t *testing.T, request review.AuthorityRequestV2, authority review.AuthorityObservationV2, kind review.ErrorKind) {
	t.Helper()
	port := &reviewPlanCapturePortV2{authority: authority}
	service, _ := review.NewAuthorityServiceV2(port, gapdbHandoffEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if _, err := service.Submit(t.Context(), request); !review.IsKind(err, kind) || len(port.plans) != 0 {
		t.Fatalf("review mutation accepted: %v plans=%d", err, len(port.plans))
	}
}

func policyInterpretingTransportMutantV2(plan journalv2.SealedCommitPlanV2) bool {
	if !reviewTransportMembershipV2(plan) {
		return false
	}
	for _, event := range plan.Events() {
		if event.Family() != "review_slot" {
			continue
		}
		decoded, err := review.DecodeEventV2(event.Payload())
		if err == nil && decoded.State.Stage == review.StageCriterionRecordedV2 {
			return false
		}
	}
	return true
}

func reviewPublisherHarnessV2(t *testing.T, plans ...review.AuthorityPlanV2) (*v2AtomicMemory, *reviewAuthorityV2) {
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
	port, err := adapter.ReviewAuthorityPortV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets)
	if err != nil {
		t.Fatal(err)
	}
	return memory, port.(*reviewAuthorityV2)
}
