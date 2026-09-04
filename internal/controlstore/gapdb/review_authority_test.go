package gapdb

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
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
func (p *reviewPlanCapturePortV2) ReconcileReview(_ context.Context, plan review.AuthorityPlanV2) (review.AuthorityAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil || len(p.plans) == 0 {
		return review.AuthorityAckV2{}, fmt.Errorf("incomplete review publication")
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

func TestReviewGapDBHeadsWithoutEventsAndMixedPublicationRemainUnresolvedV2(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(*v2AtomicMemory, journalv2.SealedCommitPlanV2, gapdbapi.Batch)
	}{
		{"heads_without_events", writeAuthorityHeadsWithoutEventsV2},
		{"mixed_provider_revisions", writeAuthorityMixedRevisionsV2},
	} {
		t.Run(test.name, func(t *testing.T) {
			capability := captureFirstReviewPlanV2(t, "topology-"+test.name, "review-"+test.name)
			plan, _ := capability.Publication()
			memory := newV2AtomicMemory()
			memory.revision = 100
			seedAuthorityPlanDependenciesV2(t, memory, plan)
			provider := memory.provider()
			provider.batchFn = func(_ context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
				memory.mu.Lock()
				defer memory.mu.Unlock()
				memory.batchCalls++
				test.write(memory, plan, batch)
				return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
			}
			provider.statusFn = gapdbMemoryStatusV2(memory, "db-review-topology")
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			projection, _ := handoffReviewProjectionTrackerV2()
			coordinator, _ := journalv2.NewPublicationCoordinatorV2(adapter, projection)
			authority := &reviewAuthorityV2{adapter: adapter, coordinator: coordinator}
			if _, err := authority.PublishReview(t.Context(), capability); err == nil || coordinator.UnresolvedCount() != 1 || memory.batchCalls != 1 {
				t.Fatalf("incomplete topology escaped: unresolved=%d calls=%d err=%v", coordinator.UnresolvedCount(), memory.batchCalls, err)
			}
		})
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
	request := review.AuthorityRequestV2{OperationID: journalv2.OperationID("operation-review-" + suffix), ReviewID: ids.ReviewID(reviewID), HandoffID: handoffState.HandoffID, ProjectID: handoffState.ProjectID, MissionID: handoffState.MissionID, WorkRevisionID: handoffState.WorkRevisionID, WorkID: handoffState.WorkID, RuntimeEpochID: handoffState.RuntimeEpochID, Workspace: handoffRequest.Workspace, ReviewerActorID: ids.ActorID(reviewerActor), ReviewerProcessID: ids.ProcessID("process-" + suffix), Candidate: handoffState.Candidate, HandoffContributorDigest: handoffState.ContributorDigest, WorkRevisionCriteriaDigest: handoffState.WorkRevisionCriteriaDigest, Criteria: criteria, Verdict: review.Approved, PolicyRevision: handoffState.Governance.PolicyRevision, Governance: handoffState.Governance, ObservedAt: handoffRequest.ObservedAt}
	handoffCoordinate, _ := handoff.CoordinateV2(handoffState.ProjectID, handoffState.MissionID, handoffState.WorkRevisionID, handoffState.WorkID)
	assertions := append([]journalv2.AuthorityAssertionV2(nil), handoffAuthority.Assertions...)
	assertions = append(assertions, gapdbAuthorityAssertionV2(t, handoffCoordinate, 23, journalv2.LogicalVersion(handoffState.LogicalVersion), handoffState.CanonicalBytes()))
	authority := review.AuthorityObservationV2{Eligibility: handoffAuthority.Eligibility, Execution: handoffAuthority.Execution, Workspace: handoffAuthority.Workspace, Handoff: handoff.ObservationV2{State: &handoffState, RecordRevision: 23}, WorkRevision: handoffAuthority.WorkRevision, Assertions: assertions}
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
	if len(plans) != 3 { // attempt, one criterion, terminal verdict
		t.Fatalf("plans=%d", len(plans))
	}
	for index, capability := range plans {
		plan, err := capability.Publication()
		if err != nil || !reviewTransportMembershipV2(plan) {
			t.Fatalf("transport rejected sealed domain stage %d: %v", index, err)
		}
	}
}

func TestHandoffAndReviewGapDBProductionServicesAreCallableV2(t *testing.T) {
	memory := newV2AtomicMemory()
	adapter, err := newAdapter(memory.provider(), "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	config := workspaceRestoreConfigV2(t)
	projection, err := handoffReviewProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	if err != nil {
		t.Fatal(err)
	}
	epoch := gapdbHandoffEpochV2("epoch-production-composition")
	handoffService, err := adapter.HandoffAuthorityServiceV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets, epoch, gapdbCandidateVerifierV2{})
	if err != nil || handoffService == nil {
		t.Fatalf("handoff production service=%v err=%v", handoffService, err)
	}
	reviewService, err := adapter.ReviewAuthorityServiceV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets, epoch)
	if err != nil || reviewService == nil {
		t.Fatalf("review production service=%v err=%v", reviewService, err)
	}
}

func TestReviewGapDBMissionPolicyExceptionUsesCurrentDurableFactV2(t *testing.T) {
	scenario := newReviewMissionPolicyScenarioV2(t, "positive")
	result, err := scenario.reviewService.Submit(t.Context(), scenario.request)
	if err != nil || result.State.Stage != review.StageVerdictRecordedV2 {
		t.Fatalf("same-actor review result=%+v err=%v", result, err)
	}
	if scenario.reviewBatches != len(scenario.request.Criteria)+2 {
		t.Fatalf("mission-fact conditioned review stages=%d want=%d", scenario.reviewBatches, len(scenario.request.Criteria)+2)
	}
	if scenario.factAssertionFailures != 0 {
		t.Fatalf("mission-fact assertion failures=%d", scenario.factAssertionFailures)
	}

	s := newReviewMissionPolicyScenarioV2(t, "current-head-revision-moved")
	before := reviewRecordCountV2(s.memory, s.request.ReviewID)
	base := s.provider.batchFn
	moved := false
	s.provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		if !moved && batchMutatesKeyV2(batch, s.reviewHeadKey) {
			moved = true
			s.replaceFactHead(s.fact, s.factRevision+1)
		}
		return base(ctx, batch)
	}
	_, err = s.reviewService.Submit(t.Context(), s.request)
	if !review.IsKind(err, review.KindConflict) || !moved {
		t.Fatalf("moved mission-fact head accepted: moved=%t err=%v", moved, err)
	}
	if after := reviewRecordCountV2(s.memory, s.request.ReviewID); after != before {
		t.Fatalf("review mutated after stale policy assertion: records=%d -> %d", before, after)
	}
}

func TestReviewGapDBMissionPolicyRejectsStableCanceledFactV2(t *testing.T) {
	s := newReviewMissionPolicyScenarioWithFactV2(t, "stable-canceled", mission.MissionFactCanceledV2, journalv2.Digest{})
	before := reviewRecordCountV2(s.memory, s.request.ReviewID)
	batches := s.memory.batchCalls
	if _, err := s.reviewService.Submit(t.Context(), s.request); !review.IsKind(err, review.KindStaleAuthority) {
		t.Fatalf("current canceled mission fact was not semantically refused: %v", err)
	}
	if after := reviewRecordCountV2(s.memory, s.request.ReviewID); after != before || s.memory.batchCalls != batches {
		t.Fatalf("canceled policy fact mutated review: records=%d -> %d batches=%d -> %d", before, after, batches, s.memory.batchCalls)
	}
}

func TestReviewGapDBMissionPolicyRejectsStableWrongDetailV2(t *testing.T) {
	detail, _ := journalv2.ReducerStateDigestV2([]byte(`{"different":"exception"}`))
	s := newReviewMissionPolicyScenarioWithFactV2(t, "stable-wrong-detail", mission.MissionFactResolvedV2, detail)
	before := reviewRecordCountV2(s.memory, s.request.ReviewID)
	batches := s.memory.batchCalls
	if _, err := s.reviewService.Submit(t.Context(), s.request); !review.IsKind(err, review.KindReviewerNotIndependent) {
		t.Fatalf("current wrong-detail mission fact was not semantically refused: %v", err)
	}
	if after := reviewRecordCountV2(s.memory, s.request.ReviewID); after != before || s.memory.batchCalls != batches {
		t.Fatalf("wrong-detail policy fact mutated review: records=%d -> %d batches=%d -> %d", before, after, batches, s.memory.batchCalls)
	}
}

func TestReviewGapDBMissionPolicySemanticSourceMutantsV2(t *testing.T) {
	if os.Getenv("SPEC_KITTY_RUN_WP09_POLICY_MUTANTS") != "1" {
		t.Skip("set SPEC_KITTY_RUN_WP09_POLICY_MUTANTS=1 to run bounded policy mutants")
	}
	mutants := []struct {
		name, killer, want string
		replacements       map[string][][2]string
	}{
		{
			name:   "resolved_status_checks_deleted",
			killer: "TestReviewGapDBMissionPolicyRejectsStableCanceledFactV2",
			want:   "current canceled mission fact was not semantically refused",
			replacements: map[string][][2]string{
				"internal/controlstore/gapdb/review_authority.go": {{" || fact.State.Status() != mission.MissionFactResolvedV2", ""}},
				"internal/review/authority_v2.go":                 {{" || fact.Status != \"resolved\"", ""}},
			},
		},
		{
			name:   "exception_detail_binding_deleted",
			killer: "TestReviewGapDBMissionPolicyRejectsStableWrongDetailV2",
			want:   "current wrong-detail mission fact was not semantically refused",
			replacements: map[string][][2]string{
				"internal/review/authority_v2.go": {{" || fact.DetailDigest != r.Exception.Digest()", ""}},
			},
		},
	}
	root := reviewRepositoryRootV2(t)
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			if output, err := runReviewPolicyKillerV2(t, root, "", mutant.killer); err != nil {
				t.Fatalf("production control failed: %v\n%s", err, output)
			}
			overlay := reviewPolicyMutantOverlayV2(t, root, mutant.replacements)
			output, err := runReviewPolicyKillerV2(t, root, overlay, mutant.killer)
			if err == nil {
				t.Fatalf("production policy mutant survived\n%s", output)
			}
			if !strings.Contains(output, mutant.want) {
				t.Fatalf("policy mutant failed for the wrong reason: want %q\n%s", mutant.want, output)
			}
		})
	}
}

func reviewRepositoryRootV2(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
}

func reviewPolicyMutantOverlayV2(t *testing.T, root string, replacements map[string][][2]string) string {
	t.Helper()
	replace := make(map[string]string, len(replacements))
	for relative, mutations := range replacements {
		source := filepath.Join(root, relative)
		contents, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		mutated := string(contents)
		for _, mutation := range mutations {
			if count := strings.Count(mutated, mutation[0]); count != 1 {
				t.Fatalf("mutant anchor count=%d want=1 in %s: %q", count, relative, mutation[0])
			}
			mutated = strings.Replace(mutated, mutation[0], mutation[1], 1)
		}
		target := filepath.Join(t.TempDir(), filepath.Base(source))
		if err = os.WriteFile(target, []byte(mutated), 0o600); err != nil {
			t.Fatal(err)
		}
		replace[source] = target
	}
	overlay, err := json.Marshal(struct {
		Replace map[string]string
	}{Replace: replace})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(path, overlay, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runReviewPolicyKillerV2(t *testing.T, root, overlay, killer string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	arguments := []string{"test"}
	if overlay != "" {
		arguments = append(arguments, "-overlay="+overlay)
	}
	arguments = append(arguments, "./internal/controlstore/gapdb", "-run", "^"+killer+"$", "-count=1")
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}

func TestReviewGapDBMissionPolicyTranslationIsRequiredV2(t *testing.T) {
	s := newReviewMissionPolicyScenarioV2(t, "translation-mutant")
	port, err := s.adapter.ReviewAuthorityPortV2(s.coordinator, s.config.EventRegistry, s.config.Reducers, s.config.HostAuthorityID, s.config.Budgets)
	if err != nil {
		t.Fatal(err)
	}
	production := port.(*reviewAuthorityV2)
	production.mission = reviewMissionTranslationDroppedV2{MissionAuthorityPortV2: production.mission}
	service, err := review.NewAuthorityServiceV2(production, gapdbHandoffEpochV2(s.request.RuntimeEpochID), s.config.EventRegistry)
	if err != nil {
		t.Fatal(err)
	}
	before := s.memory.batchCalls
	if _, err = service.Submit(t.Context(), s.request); !review.IsKind(err, review.KindStaleAuthority) || s.memory.batchCalls != before {
		t.Fatalf("broken production mission translation escaped: err=%v batches=%d -> %d", err, before, s.memory.batchCalls)
	}
}

func TestReviewGapDBMissionPolicyRejectsForgedTombstoneHeadV2(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *reviewMissionPolicyScenarioV2, journalv2.AggregateHeadV2) journalv2.AggregateHeadV2
	}{
		{"live_head_for_resolved_fact", func(t *testing.T, s *reviewMissionPolicyScenarioV2, head journalv2.AggregateHeadV2) journalv2.AggregateHeadV2 {
			return rebuildReviewMissionHeadV2(t, head, head.Coordinate(), false)
		}},
		{"tombstone_wrong_coordinate", func(t *testing.T, s *reviewMissionPolicyScenarioV2, head journalv2.AggregateHeadV2) journalv2.AggregateHeadV2 {
			wrong, err := journalv2.NewAggregateCoordinate("mission", "decision:unrelated-policy-fact", journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(s.request.ProjectID), MissionID: journalv2.MissionID(s.request.MissionID)})
			if err != nil {
				t.Fatal(err)
			}
			return rebuildReviewMissionHeadV2(t, head, wrong, true)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newReviewMissionPolicyScenarioV2(t, "forged-"+test.name)
			record := s.memory.records[s.factKey]
			head, err := journalv2.DecodeAggregateHeadV2(record.Value)
			if err != nil {
				t.Fatal(err)
			}
			forged := test.mutate(t, s, head)
			encoded, err := journalv2.EncodeAggregateHeadV2(forged)
			if err != nil {
				t.Fatal(err)
			}
			s.memory.records[s.factKey] = gapdbapi.NewRecord(s.factKey, encoded, record.Revision, nil)
			before := s.memory.batchCalls
			if _, err = s.reviewService.Submit(t.Context(), s.request); !review.IsKind(err, review.KindStaleAuthority) || s.memory.batchCalls != before {
				t.Fatalf("forged mission policy head escaped: err=%v batches=%d -> %d", err, before, s.memory.batchCalls)
			}
		})
	}
}

func rebuildReviewMissionHeadV2(t *testing.T, head journalv2.AggregateHeadV2, coordinate journalv2.AggregateCoordinate, tombstone bool) journalv2.AggregateHeadV2 {
	t.Helper()
	rebuilt, err := journalv2.NewAggregateHeadV2(journalv2.AggregateHeadInput{Coordinate: coordinate, LogicalVersion: head.LogicalVersion(), LastEventID: head.LastEventID(), LastPublicationID: head.LastPublicationID(), CommittedRuntimeEpochID: head.CommittedRuntimeEpochID(), CanonicalStateDigest: head.CanonicalStateDigest(), AuthoritySummary: head.AuthoritySummary(), Tombstone: tombstone})
	if err != nil {
		t.Fatal(err)
	}
	return rebuilt
}

type reviewMissionTranslationDroppedV2 struct{ mission.MissionAuthorityPortV2 }

func (reviewMissionTranslationDroppedV2) ObserveMissionFact(context.Context, journalv2.ProjectID, journalv2.MissionID, mission.MissionFactKindV2, string) (mission.MissionFactObservationV2, error) {
	return mission.MissionFactObservationV2{}, nil
}

type reviewMissionPolicyScenarioV2 struct {
	t                     *testing.T
	suffix                string
	memory                *v2AtomicMemory
	provider              *fakeProvider
	adapter               *Adapter
	coordinator           *journalv2.PublicationCoordinatorV2
	config                WorkspaceRestoreConfigurationV2
	reviewService         *review.AuthorityServiceV2
	request               review.AuthorityRequestV2
	fact                  mission.MissionFactStateV2
	factRevision          uint64
	factKey               string
	reviewHeadKey         string
	reviewBatches         int
	factAssertionFailures int
}

func newReviewMissionPolicyScenarioV2(t *testing.T, suffix string) *reviewMissionPolicyScenarioV2 {
	return newReviewMissionPolicyScenarioWithFactV2(t, suffix, mission.MissionFactResolvedV2, journalv2.Digest{})
}

func newReviewMissionPolicyScenarioWithFactV2(t *testing.T, suffix string, terminalStatus mission.MissionFactStatusV2, terminalDetail journalv2.Digest) *reviewMissionPolicyScenarioV2 {
	t.Helper()
	missionID := "mission-review-policy-" + suffix
	workID := "work-review-policy-" + suffix
	workgraphPlan := gapdbWorkgraphPlanForV2(t, "review-policy-"+suffix, missionID, workID)
	sealedWorkgraph, err := workgraphPlan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	var revision workgraph.RevisionV2
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
	}
	if revision.ID() == "" {
		t.Fatal("workgraph plan has no current revision")
	}
	memory := newV2AtomicMemory()
	provider := memory.provider()
	enableWorkspaceRecoveryScansV2(memory, provider)
	provider.statusFn = gapdbMemoryStatusV2(memory, "db-review-policy-"+suffix)
	adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	config := reviewMissionRestoreConfigV2(t)
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
		t.Fatalf("production workgraph finalization: %v", err)
	}
	executionPort, err := newExecutionAuthorityV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	executionRequest := execution.ExecutionAuthorityRequestV2{Kind: execution.ExecutionAdmitV2, OperationID: journalv2.OperationID("operation-review-policy-execution-" + suffix), ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), WorkRevisionID: revision.ID(), WorkID: ids.WorkID(workID), RunID: ids.RunID("run-review-policy-" + suffix), ActorID: "actor-review-policy-contributor", ProcessID: ids.ProcessID("process-review-policy-contributor-" + suffix), LeaseID: ids.LeaseID("lease-review-policy-" + suffix), Generation: 1, ObservedAt: now, Deadline: now.Add(time.Hour), RuntimeEpochID: "epoch-gapdb", Governance: gapdbExecutionContextV2(t), OperationEventID: journalv2.EventID("event-operation-review-policy-execution-" + suffix), SlotEventID: journalv2.EventID("event-slot-review-policy-execution-" + suffix)}
	executionService, err := execution.NewExecutionAuthorityServiceWithFenceSourceV2(executionPort, gapdbEpochV2{epoch: executionRequest.RuntimeEpochID}, gapdbFenceSourceV2(executionRequest.OperationID), config.EventRegistry)
	if err != nil {
		t.Fatal(err)
	}
	executionResult, err := executionService.Execute(t.Context(), executionRequest)
	if err != nil {
		t.Fatalf("production execution admission: %v", err)
	}
	slot := executionResult.State
	binding := gapdbWorkspaceBindingV2(t, slot, "workspace-review-policy-"+suffix, "/srv/spec-kitty/workspaces/review-policy-"+suffix)
	publishReviewWorkspaceReadyV2(t, adapter, coordinator, config, slot, binding, now)
	handoffRequest := reviewPolicyHandoffRequestV2(t, suffix, revision, slot, binding, now.Add(15*time.Second))
	handoffService, err := adapter.HandoffAuthorityServiceV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets, gapdbHandoffEpochV2(handoffRequest.RuntimeEpochID), gapdbCandidateVerifierV2{})
	if err != nil {
		t.Fatal(err)
	}
	handoffResult, err := handoffService.Submit(t.Context(), handoffRequest)
	if err != nil || handoffResult.State.Stage != handoff.StageCandidateRecordedV2 {
		t.Fatalf("production handoff=%+v err=%v", handoffResult, err)
	}
	handoffState := handoffResult.State

	request := reviewRequestFromHandoffV2(t, suffix, handoffState, handoffRequest)
	if terminalDetail.IsZero() {
		terminalDetail = request.Exception.Digest()
	}
	fact, factRevision := publishReviewMissionDecisionV2(t, adapter, coordinator, config, request, terminalStatus, terminalDetail)
	factCoordinate, _ := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID("decision:"+request.Exception.ExceptionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(request.ProjectID), MissionID: journalv2.MissionID(request.MissionID)})
	factKey, _ := v2AggregateHeadKey(factCoordinate)
	reviewCoordinate, _ := review.CoordinateV2(request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID, request.ReviewID)
	reviewHeadKey, _ := v2AggregateHeadKey(reviewCoordinate)
	service, err := adapter.ReviewAuthorityServiceV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets, gapdbHandoffEpochV2(request.RuntimeEpochID))
	if err != nil {
		t.Fatal(err)
	}
	if terminalStatus == mission.MissionFactResolvedV2 && terminalDetail == request.Exception.Digest() {
		debugPort, portErr := adapter.ReviewAuthorityPortV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets)
		if portErr != nil {
			t.Fatal(portErr)
		}
		if _, portErr = debugPort.ObserveReviewAuthority(t.Context(), request); portErr != nil {
			t.Fatalf("production review authority observation: %T %+v", portErr, portErr)
		}
	}
	s := &reviewMissionPolicyScenarioV2{t: t, suffix: suffix, memory: memory, provider: provider, adapter: adapter, coordinator: coordinator, config: config, reviewService: service, request: request, fact: fact, factRevision: factRevision, factKey: factKey.String(), reviewHeadKey: reviewHeadKey.String()}
	base := provider.batchFn
	provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		if batchMutatesKeyV2(batch, s.reviewHeadKey) {
			s.reviewBatches++
			matches := 0
			for _, assertion := range batch.Assertions {
				if assertion.Key == s.factKey && assertion.Condition.Kind == gapdbapi.ConditionRevision && uint64(assertion.Condition.ExpectedRevision) == s.factRevision {
					matches++
				}
			}
			if matches != 1 {
				s.factAssertionFailures++
				return gapdbapi.MutationResult{}, &gapdbapi.Error{Code: gapdbapi.CodeInvalidRequest}
			}
		}
		return base(ctx, batch)
	}
	return s
}

func publishReviewWorkspaceReadyV2(t *testing.T, adapter *Adapter, coordinator *journalv2.PublicationCoordinatorV2, config WorkspaceRestoreConfigurationV2, slot execution.ExecutionSlotStateV2, binding workspace.BindingV2, observedAt time.Time) {
	t.Helper()
	workspacePort, _, err := adapter.WorkspaceAuthorityPortsV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets)
	if err != nil {
		t.Fatal(err)
	}
	authority := workspacePort.(*workspaceAuthorityV2)
	service, err := execution.NewWorkspaceAuthorityServiceV2(authority, gapdbEpochV2{epoch: slot.RuntimeEpochID}, config.EventRegistry)
	if err != nil {
		t.Fatal(err)
	}
	contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: binding, EffectID: journalv2.EffectID("effect-policy-workspace-" + string(binding.WorkspaceID)), EventID: journalv2.EventID("event-policy-workspace-bind-" + string(binding.WorkspaceID)), OccurredAt: observedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Execute(t.Context(), execution.WorkspaceAuthorityRequestV2{Kind: execution.WorkspaceBindV2, OperationID: journalv2.OperationID("operation-policy-workspace-bind-" + string(binding.WorkspaceID)), OperationEventID: journalv2.EventID("event-operation-policy-workspace-bind-" + string(binding.WorkspaceID)), ExecutionEventID: journalv2.EventID("event-execution-policy-workspace-bind-" + string(binding.WorkspaceID)), Governance: slot.Governance, Contribution: contribution, ObservedAt: observedAt.Add(time.Second)}); err != nil {
		t.Fatalf("production workspace bind: %v", err)
	}
	coordinate, _ := workspace.WorkspaceCoordinateV2(binding)
	stageService, err := workspace.NewStageAuthorityServiceV2(authority, gapdbEpochV2{epoch: slot.RuntimeEpochID}, config.EventRegistry)
	if err != nil {
		t.Fatal(err)
	}
	for index, stage := range []workspace.StageV2{workspace.StageWorktreeObservedV2, workspace.StageBaseVerifiedV2, workspace.StageDependenciesRequestedV2, workspace.StageDependenciesObservedV2, workspace.StageOwnershipVerifiedV2, workspace.StageReadyV2} {
		observed, observeErr := authority.ObserveWorkspaceStage(t.Context(), coordinate)
		if observeErr != nil || observed.State == nil {
			t.Fatalf("observe workspace stage %d: %+v %v", index, observed, observeErr)
		}
		observation := workspace.ObservationV2{Registered: true, Head: binding.PinnedBase, DependencyHead: binding.PinnedBase, Branch: binding.OwnedBranch, GitDirIdentity: "gitdir-policy-" + string(binding.WorkspaceID), IndexIdentity: "index-policy-" + string(binding.WorkspaceID), Clean: true, TargetBefore: binding.PinnedBase, TargetAfter: binding.PinnedBase}
		effectID := journalv2.EffectID("")
		if stage == workspace.StageDependenciesRequestedV2 {
			effectID = journalv2.EffectID("effect-policy-dependencies-" + string(binding.WorkspaceID))
		}
		occurredAt := observedAt.Add(time.Duration(index+2) * time.Second)
		transition, buildErr := workspace.NewTransitionContributionV2(workspace.TransitionContributionInputV2{Prior: *observed.State, RecordRevision: observed.RecordRevision, EventID: journalv2.EventID(fmt.Sprintf("event-policy-workspace-stage-%s-%d", binding.WorkspaceID, index)), NextStage: stage, Observation: observation, EffectID: effectID, OccurredAt: occurredAt})
		if buildErr != nil {
			t.Fatalf("workspace transition %s: %v", stage, buildErr)
		}
		if _, executeErr := stageService.Execute(t.Context(), workspace.StageAuthorityRequestV2{OperationID: journalv2.OperationID(fmt.Sprintf("operation-policy-workspace-stage-%s-%d", binding.WorkspaceID, index)), OperationEventID: journalv2.EventID(fmt.Sprintf("event-operation-policy-workspace-stage-%s-%d", binding.WorkspaceID, index)), Contribution: transition, PolicyRevision: slot.Governance.PolicyRevision, DoctrineRevision: slot.Governance.DoctrineRevision, ProfileRevision: slot.Governance.ProfileRevision, ObservedAt: occurredAt}); executeErr != nil {
			t.Fatalf("production workspace stage %s: %v", stage, executeErr)
		}
	}
}

func reviewPolicyHandoffRequestV2(t *testing.T, suffix string, revision workgraph.RevisionV2, slot execution.ExecutionSlotStateV2, binding workspace.BindingV2, observedAt time.Time) handoff.AuthorityRequestV2 {
	t.Helper()
	criterionIDs := make([]string, 0)
	for _, item := range revision.WorkItems() {
		if item.WorkID() != slot.WorkID {
			continue
		}
		for _, criterion := range item.Criteria() {
			criterionIDs = append(criterionIDs, criterion.ID())
		}
	}
	criterionIDs, err := handoff.CanonicalCriterionIDsV2(criterionIDs)
	if err != nil || len(criterionIDs) == 0 {
		t.Fatalf("work revision criteria=%v err=%v", criterionIDs, err)
	}
	criteriaDigest, err := handoff.CriterionSetDigestV2(criterionIDs)
	if err != nil {
		t.Fatal(err)
	}
	return handoff.AuthorityRequestV2{OperationID: journalv2.OperationID("operation-review-policy-handoff-" + suffix), HandoffID: ids.HandoffID("handoff-review-policy-" + suffix), ProjectID: slot.ProjectID, MissionID: slot.MissionID, WorkRevisionID: slot.WorkRevisionID, WorkID: slot.WorkID, RunID: slot.RunID, RuntimeEpochID: slot.RuntimeEpochID, Workspace: binding, LeaseID: slot.LeaseID, LeaseGeneration: slot.Generation, Fence: slot.Fence, ActorID: slot.ActorID, ProcessID: slot.ProcessID, RepositoryID: binding.RepositoryID, RepositoryPath: "/repo", LaneID: binding.LaneID, OwnedBranch: binding.OwnedBranch, PinnedBase: binding.PinnedBase, Candidate: strings.Repeat("b", 40), Contributors: []ids.ActorID{slot.ActorID, "actor-review-policy-second"}, CriterionIDs: criterionIDs, CriteriaDigest: criteriaDigest, WorkRevisionCriteriaDigest: revision.CriteriaDigest(), EvidenceDigest: strings.Repeat("d", 64), Governance: slot.Governance, ObservedAt: observedAt}
}

func reviewMissionRestoreConfigV2(t *testing.T) WorkspaceRestoreConfigurationV2 {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	owned := append(execution.ExecutionReducerRegistrationsV2(), workspace.ReducerRegistrationsV2()...)
	owned = append(owned, handoff.ReducerRegistrationsV2()...)
	owned = append(owned, review.ReducerRegistrationsV2()...)
	owned = append(owned, workgraph.ReducerRegistrationsV2()...)
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
			sum := sha256.Sum256([]byte("unused-review-policy/" + tuple.Family))
			digest, _ := journalv2.ParseDigest("unused_review_policy", fmt.Sprintf("%x", sum))
			registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: digest, Reduce: func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil }})
		}
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	return WorkspaceRestoreConfigurationV2{EventRegistry: registry, Reducers: reducers, HostAuthorityID: "host-review-policy"}
}

func seedReviewTypedHeadV2(t *testing.T, memory *v2AtomicMemory, summary []byte, coordinate journalv2.AggregateCoordinate, revision uint64, logicalVersion journalv2.LogicalVersion, publicationID journalv2.PublicationID, epoch journalv2.RuntimeEpochID) {
	t.Helper()
	digest, err := journalv2.ReducerStateDigestV2(summary)
	if err != nil {
		t.Fatal(err)
	}
	head, err := journalv2.NewAggregateHeadV2(journalv2.AggregateHeadInput{Coordinate: coordinate, LogicalVersion: logicalVersion, LastEventID: journalv2.EventID("event-seed-" + string(coordinate.Kind)), LastPublicationID: publicationID, CommittedRuntimeEpochID: epoch, CanonicalStateDigest: digest, AuthoritySummary: summary, Tombstone: true})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := journalv2.EncodeAggregateHeadV2(head)
	if err != nil {
		t.Fatal(err)
	}
	key, err := v2AggregateHeadKey(coordinate)
	if err != nil {
		t.Fatal(err)
	}
	memory.records[key.String()] = gapdbapi.NewRecord(key.String(), encoded, gapdbapi.Revision(revision), nil)
}

func reviewRequestFromHandoffV2(t *testing.T, suffix string, state handoff.StateV2, handoffRequest handoff.AuthorityRequestV2) review.AuthorityRequestV2 {
	t.Helper()
	criteria := make([]review.CriterionEvidenceV2, len(state.CriterionIDs))
	for index, criterionID := range state.CriterionIDs {
		criteria[index] = review.CriterionEvidenceV2{CriterionID: criterionID, Status: review.Passed, EvidenceDigest: fmt.Sprintf("%064x", index+101), ArtifactDigest: fmt.Sprintf("%064x", index+201), Candidate: state.Candidate, PolicyRevision: state.Governance.PolicyRevision}
	}
	reviewer := state.Contributors[0]
	observed := handoffRequest.ObservedAt.Add(10 * time.Minute)
	exception := review.IndependenceExceptionV2{SchemaVersion: 1, ExceptionID: "exception-" + suffix, PolicyRevision: state.Governance.PolicyRevision, ProjectID: state.ProjectID, MissionID: state.MissionID, WorkRevisionID: state.WorkRevisionID, WorkID: state.WorkID, HandoffID: state.HandoffID, Candidate: state.Candidate, ReviewerActorID: reviewer, Reason: "explicit mission policy exception", AuthorizerActorID: "actor-policy-authorizer", EffectiveFrom: observed.Add(-time.Minute).Format(time.RFC3339Nano), EffectiveThrough: observed.Add(time.Hour).Format(time.RFC3339Nano)}
	if err := exception.Validate(); err != nil {
		t.Fatal(err)
	}
	return review.AuthorityRequestV2{OperationID: journalv2.OperationID("operation-review-policy-" + suffix), ReviewID: ids.ReviewID("review-policy-" + suffix), HandoffID: state.HandoffID, ProjectID: state.ProjectID, MissionID: state.MissionID, WorkRevisionID: state.WorkRevisionID, WorkID: state.WorkID, RuntimeEpochID: state.RuntimeEpochID, Workspace: handoffRequest.Workspace, ReviewerActorID: reviewer, ReviewerProcessID: ids.ProcessID("process-review-policy-" + suffix), Candidate: state.Candidate, HandoffContributorDigest: state.ContributorDigest, WorkRevisionCriteriaDigest: state.WorkRevisionCriteriaDigest, Criteria: criteria, Verdict: review.Approved, PolicyRevision: state.Governance.PolicyRevision, Exception: &exception, Governance: state.Governance, ObservedAt: observed}
}

func publishReviewMissionDecisionV2(t *testing.T, adapter *Adapter, coordinator *journalv2.PublicationCoordinatorV2, config WorkspaceRestoreConfigurationV2, request review.AuthorityRequestV2, terminalStatus mission.MissionFactStatusV2, terminalDetail journalv2.Digest) (mission.MissionFactStateV2, uint64) {
	t.Helper()
	port, err := NewMissionAuthorityPortV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	service, err := mission.NewMissionAuthorityServiceV2(port, missionEpochFixtureV2{request.RuntimeEpochID}, config.EventRegistry)
	if err != nil {
		t.Fatal(err)
	}
	detail, _ := journalv2.ReducerStateDigestV2([]byte(`{"mission":"review-policy"}`))
	baseInput := mission.MissionAuthorityStateInputV2{ProjectID: journalv2.ProjectID(request.ProjectID), MissionID: journalv2.MissionID(request.MissionID), DefinitionRevision: "definition-review-policy", DefinitionDigest: detail, Version: 1, Status: mission.AuthorityMissionActiveV2, Step: "reviewing", CurrentWorkRevisionID: journalv2.WorkRevisionID(request.WorkRevisionID), Decomposition: mission.DecompositionNoneV2, LastOperationID: journalv2.OperationID("operation-mission-create-" + request.Exception.ExceptionID), ObservedAt: request.ObservedAt.Add(-3 * time.Minute).Format(time.RFC3339Nano)}
	created, err := mission.NewMissionAuthorityStateV2(baseInput)
	if err != nil {
		t.Fatal(err)
	}
	command := reviewMissionCommandV2(request, mission.MissionEventCreatedV2, mission.MissionAuthorityStateV2{}, created, baseInput.LastOperationID, baseInput.ObservedAt)
	if _, err = service.Execute(t.Context(), command); err != nil {
		t.Fatalf("create mission authority: %v", err)
	}
	baseInput.Version = 2
	baseInput.OpenDecisionIDs = []string{request.Exception.ExceptionID}
	baseInput.LastOperationID = journalv2.OperationID("operation-policy-open-" + request.Exception.ExceptionID)
	baseInput.ObservedAt = request.ObservedAt.Add(-2 * time.Minute).Format(time.RFC3339Nano)
	opened, err := mission.NewMissionAuthorityStateV2(baseInput)
	if err != nil {
		t.Fatal(err)
	}
	openFact := reviewMissionFactV2(t, &reviewMissionPolicyScenarioV2{request: request}, mission.MissionFactOpenV2, request.Exception.Digest(), 1, string(baseInput.LastOperationID))
	command = reviewMissionCommandV2(request, mission.MissionEventDecisionOpenedV2, created, opened, baseInput.LastOperationID, baseInput.ObservedAt)
	command.RelatedFact = &mission.MissionFactTransitionV2{Successor: openFact}
	if _, err = service.Execute(t.Context(), command); err != nil {
		t.Fatalf("open mission decision: %v", err)
	}
	observedOpen, err := port.ObserveMissionFact(t.Context(), journalv2.ProjectID(request.ProjectID), journalv2.MissionID(request.MissionID), mission.MissionFactDecisionV2, request.Exception.ExceptionID)
	if err != nil || !observedOpen.Found {
		t.Fatalf("observe open decision: %+v %v", observedOpen, err)
	}
	baseInput.Version = 3
	baseInput.OpenDecisionIDs = nil
	baseInput.LastOperationID = journalv2.OperationID("operation-policy-resolve-" + request.Exception.ExceptionID)
	baseInput.ObservedAt = request.ObservedAt.Add(-time.Minute).Format(time.RFC3339Nano)
	resolved, err := mission.NewMissionAuthorityStateV2(baseInput)
	if err != nil {
		t.Fatal(err)
	}
	terminalEvent := mission.MissionEventDecisionResolvedV2
	if terminalStatus == mission.MissionFactCanceledV2 {
		terminalEvent = mission.MissionEventDecisionCanceledV2
	}
	terminalFact := reviewMissionFactV2(t, &reviewMissionPolicyScenarioV2{request: request}, terminalStatus, terminalDetail, 2, string(baseInput.LastOperationID))
	command = reviewMissionCommandV2(request, terminalEvent, opened, resolved, baseInput.LastOperationID, baseInput.ObservedAt)
	command.RelatedFact = &mission.MissionFactTransitionV2{Prior: openFact, Successor: terminalFact, ExpectedRecordRevision: observedOpen.RecordRevision}
	if _, err = service.Execute(t.Context(), command); err != nil {
		t.Fatalf("resolve mission decision: %v", err)
	}
	observed, err := port.ObserveMissionFact(t.Context(), journalv2.ProjectID(request.ProjectID), journalv2.MissionID(request.MissionID), mission.MissionFactDecisionV2, request.Exception.ExceptionID)
	if err != nil || !observed.Found || observed.State.Status() != terminalStatus || observed.State.DetailDigest() != terminalDetail {
		t.Fatalf("terminal mission decision=%+v err=%v", observed, err)
	}
	return observed.State, observed.RecordRevision
}

func reviewMissionCommandV2(request review.AuthorityRequestV2, event mission.MissionEventTypeV2, prior, successor mission.MissionAuthorityStateV2, operationID journalv2.OperationID, observedAt string) mission.MissionAuthorityCommandV2 {
	return mission.MissionAuthorityCommandV2{SchemaVersion: 1, OperationID: operationID, ProjectID: journalv2.ProjectID(request.ProjectID), MissionID: journalv2.MissionID(request.MissionID), ActorID: "actor-policy-authorizer", ProcessID: "process-policy-authorizer", OriginatingRuntimeEpochID: request.RuntimeEpochID, DoctrineRevision: request.Governance.DoctrineRevision, ProfileRevision: request.Governance.ProfileRevision, PolicyRevision: request.PolicyRevision, ObservedAt: observedAt, EventType: event, Expected: prior, Successor: successor}
}

func reviewMissionFactV2(t *testing.T, scenario *reviewMissionPolicyScenarioV2, status mission.MissionFactStatusV2, detail journalv2.Digest, version journalv2.LogicalVersion, operationID string) mission.MissionFactStateV2 {
	t.Helper()
	request := scenario.request
	fact, err := mission.NewMissionFactStateV2(mission.MissionFactStateInputV2{ProjectID: journalv2.ProjectID(request.ProjectID), MissionID: journalv2.MissionID(request.MissionID), FactID: request.Exception.ExceptionID, Kind: mission.MissionFactDecisionV2, Status: status, LogicalVersion: version, DetailDigest: detail, LastOperationID: journalv2.OperationID(operationID), ObservedAt: request.ObservedAt.Add(-time.Minute).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	return fact
}

func (s *reviewMissionPolicyScenarioV2) replaceFactHead(fact mission.MissionFactStateV2, revision uint64) {
	s.t.Helper()
	coordinate, _ := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID("decision:"+s.request.Exception.ExceptionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(s.request.ProjectID), MissionID: journalv2.MissionID(s.request.MissionID)})
	seedReviewTypedHeadV2(s.t, s.memory, fact.CanonicalBytes(), coordinate, revision, fact.LogicalVersion(), s.firstFactPublicationID(), s.request.RuntimeEpochID)
	if gapdbapi.Revision(revision) > s.memory.revision {
		s.memory.revision = gapdbapi.Revision(revision)
	}
}

func (s *reviewMissionPolicyScenarioV2) firstFactPublicationID() journalv2.PublicationID {
	record := s.memory.records[s.factKey]
	head, _ := journalv2.DecodeAggregateHeadV2(record.Value)
	return head.LastPublicationID()
}

func batchMutatesKeyV2(batch gapdbapi.Batch, key string) bool {
	for _, mutation := range batch.Mutations {
		if mutation.Key == key {
			return true
		}
	}
	return false
}

func reviewRecordCountV2(memory *v2AtomicMemory, reviewID ids.ReviewID) int {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	needle := string(reviewID)
	count := 0
	for key := range memory.records {
		if strings.Contains(key, needle) {
			count++
		}
	}
	return count
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
			request.Criteria = nil
			assertReviewMutationRefusedV2(t, request, control.authority, review.KindInvalid)
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
			attempt := plans[0]
			controlMemory, control := reviewPublisherHarnessV2(t, attempt)
			if _, err := control.PublishReview(t.Context(), attempt); err != nil {
				t.Fatalf("production transport rejected valid sealed attempt: %v", err)
			}
			if controlMemory.batchCalls != 1 {
				t.Fatalf("production publication calls=%d", controlMemory.batchCalls)
			}

			mutantMemory, mutant := reviewPublisherHarnessV2(t, attempt)
			mutant.admit = reviewPolicyInterpretingAdmissionMutantV2
			_, err := mutant.PublishReview(t.Context(), attempt)
			if err == nil || mutantMemory.batchCalls != 0 {
				t.Fatalf("policy-interpreting production admission mutant escaped: err=%v calls=%d", err, mutantMemory.batchCalls)
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

func reviewPolicyInterpretingAdmissionMutantV2(plan journalv2.SealedCommitPlanV2) bool {
	if !reviewTransportMembershipV2(plan) {
		return false
	}
	for _, event := range plan.Events() {
		if event.Family() != "review_slot" {
			continue
		}
		decoded, err := review.DecodeEventV2(event.Payload())
		if err == nil && decoded.State.Stage == review.StageAttemptRequestedV2 {
			return false
		}
	}
	return true
}

func reviewPublisherHarnessV2(t *testing.T, plans ...review.AuthorityPlanV2) (*v2AtomicMemory, *reviewAuthorityV2) {
	t.Helper()
	memory := newV2AtomicMemory()
	// The capture port acknowledges stages at revisions 61, 62, ...; use the
	// same base so later stage CAS conditions exercise the real provider path.
	memory.revision = 60
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
