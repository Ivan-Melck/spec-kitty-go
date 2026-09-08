package review

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestSubmitEvaluatesCanonicalCriteriaBeforeAtomicCommit(t *testing.T) {
	now := time.Date(2026, 8, 24, 17, 0, 0, 0, time.UTC)
	request := validRequest(now)
	store := &recordingStore{scope: Scope{
		Handoff:      handoff.Record{HandoffID: request.HandoffID, Candidate: request.Candidate},
		CriterionIDs: []string{"criterion-a", "criterion-b"},
	}}
	hook := &recordingHook{}
	result, err := NewService(store).WithEligibilityFacts(&recordingEligibilityReader{fact: request.ExpectedEligibility}).WithFaults(hook).Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != Approved || result.Candidate != request.Candidate || len(result.Criteria) != 2 {
		t.Fatalf("review result=%+v", result)
	}
	if !reflect.DeepEqual(hook.boundaries, []string{"HR-03", "HR-04", "HR-05", "HR-06", "HR-07"}) {
		t.Fatalf("boundaries=%v", hook.boundaries)
	}
}

func TestApprovedVerdictRequiresEveryCanonicalCriterionToPass(t *testing.T) {
	request := validRequest(time.Now().UTC())
	request.Criteria = request.Criteria[:1]
	store := &recordingStore{scope: Scope{Handoff: handoff.Record{HandoffID: request.HandoffID, Candidate: request.Candidate}, CriterionIDs: []string{"criterion-a", "criterion-b"}}}
	_, err := NewService(store).WithEligibilityFacts(&recordingEligibilityReader{fact: request.ExpectedEligibility}).Submit(context.Background(), request)
	if !IsKind(err, KindInvalid) {
		t.Fatalf("partial criteria error=%v", err)
	}
	if store.committed {
		t.Fatal("partial criterion set reached persistence")
	}
}

func TestSubmitReturnsDurableReplayBeforeReviewEligibility(t *testing.T) {
	request := validRequest(time.Now().UTC())
	want := Record{ReviewID: request.ReviewID, HandoffID: request.HandoffID, Candidate: request.Candidate, Verdict: request.Verdict, SubmittedAt: request.SubmittedAt}
	store := &recordingStore{replay: &want}
	hook := &recordingHook{}
	got, err := NewService(store).WithFaults(hook).Submit(context.Background(), request)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("replay=%+v err=%v", got, err)
	}
	if store.eligibilityRead || store.inspected || store.committed || len(hook.boundaries) != 0 {
		t.Fatalf("eligibility=%v inspect=%v commit=%v boundaries=%v", store.eligibilityRead, store.inspected, store.committed, hook.boundaries)
	}
}

func TestSubmitRefusesReconciledWorkBeforeFaultInspectAndMutation(t *testing.T) {
	request := validRequest(time.Now().UTC())
	denied := mustEligibilityFact("reconciliation-review", "revision-1", "revision-2", request.WorkID, workgraph.StatusHandedOff, workgraph.Invalidate, workgraph.EligibilityReview)
	request.ExpectedEligibility = denied
	store := &recordingStore{eligibility: workgraph.Consequence{Reason: "invalidated handoff cannot be reviewed"}, eligibilityID: "reconciliation-review"}
	hook := &recordingHook{}
	_, err := NewService(store).WithEligibilityFacts(&recordingEligibilityReader{fact: denied}).WithFaults(hook).Submit(context.Background(), request)
	if !IsKind(err, KindRevisionIneligible) || store.eligibilityRead || store.inspected || store.committed || len(hook.boundaries) != 0 {
		t.Fatalf("error=%v eligibility=%v inspect=%v commit=%v boundaries=%v", err, store.eligibilityRead, store.inspected, store.committed, hook.boundaries)
	}
}

func TestSubmitConsumesCompleteMixedEligibilityBeforeReviewInspection(t *testing.T) {
	now := time.Date(2026, 8, 28, 19, 0, 0, 0, time.UTC)
	expected := completeReviewFact(t, "reconciliation-review-complete", "revision-2", "work-1", workgraph.Fork, workgraph.StatusHandedOff, workgraph.EligibilityReview)
	tests := map[string]struct {
		expected EligibilityFact
		actual   EligibilityFact
		reader   bool
	}{
		"successor substitution":        {expected, completeReviewFact(t, "reconciliation-review-complete", "revision-other", "work-1", workgraph.Fork, workgraph.StatusHandedOff, workgraph.EligibilityReview), true},
		"disposition substitution":      {expected, completeReviewFact(t, "reconciliation-review-complete", "revision-2", "work-1", workgraph.Continue, workgraph.StatusHandedOff, workgraph.EligibilityReview), true},
		"reconciliation substitution":   {expected, completeReviewFact(t, "reconciliation-other", "revision-2", "work-1", workgraph.Fork, workgraph.StatusHandedOff, workgraph.EligibilityReview), true},
		"work substitution":             {expected, completeReviewFact(t, "reconciliation-review-complete", "revision-2", "work-other", workgraph.Fork, workgraph.StatusHandedOff, workgraph.EligibilityReview), true},
		"status operation substitution": {expected, completeReviewFact(t, "reconciliation-review-complete", "revision-2", "work-1", workgraph.Fork, workgraph.StatusApproved, workgraph.EligibilityIntegration), true},
		"omitted expectation":           {EligibilityFact{}, expected, true},
		"reader unavailable":            {expected, EligibilityFact{}, false},
	}
	legacy, err := workgraph.NewLegacyEligibilityFact("reconciliation-review-complete", "revision-1", "work-1", workgraph.StatusHandedOff, workgraph.EligibilityReview, workgraph.Consequence{HandoffEligible: true, Reason: "legacy permissive consequence"})
	if err != nil {
		t.Fatal(err)
	}
	tests["legacy fact returned for mixed request"] = struct {
		expected EligibilityFact
		actual   EligibilityFact
		reader   bool
	}{expected, legacy, true}
	oldSubstitution, err := workgraph.NewEligibilityFact("reconciliation-review-complete", "revision-other", "revision-2", "work-1", workgraph.StatusHandedOff, workgraph.Fork, workgraph.EligibilityReview)
	if err != nil {
		t.Fatal(err)
	}
	tests["predecessor substitution"] = struct {
		expected EligibilityFact
		actual   EligibilityFact
		reader   bool
	}{expected, oldSubstitution, true}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			request := validRequest(now)
			request.ExpectedEligibility = test.expected
			store := &recordingStore{eligibility: workgraph.Consequence{HandoffEligible: true, Reason: "permissive legacy stub"}}
			reader := &recordingEligibilityReader{fact: test.actual}
			hook := &recordingHook{}
			service := NewService(store).WithFaults(hook)
			if test.reader {
				service.WithEligibilityFacts(reader)
			}
			_, submitErr := service.Submit(context.Background(), request)
			if !IsKind(submitErr, KindRevisionIneligible) || store.eligibilityRead || store.inspected || store.committed || len(hook.boundaries) != 0 {
				t.Fatalf("error=%v full_reads=%d legacy=%v inspect=%v commit=%v boundaries=%v", submitErr, reader.calls, store.eligibilityRead, store.inspected, store.committed, hook.boundaries)
			}
		})
	}

	t.Run("exact complete fact proceeds", func(t *testing.T) {
		request := validRequest(now)
		request.ExpectedEligibility = expected
		store := &recordingStore{scope: Scope{Handoff: handoff.Record{HandoffID: request.HandoffID, Candidate: request.Candidate}, CriterionIDs: []string{"criterion-a", "criterion-b"}}}
		reader := &recordingEligibilityReader{fact: expected}
		if _, err := NewService(store).WithEligibilityFacts(reader).Submit(context.Background(), request); err != nil || reader.calls != 1 || reader.oldRevision != request.WorkRevisionID || reader.workID != request.WorkID || reader.status != workgraph.StatusHandedOff || reader.operation != workgraph.EligibilityReview || store.eligibilityRead || !store.inspected || !store.committed {
			t.Fatalf("error=%v reader=%+v legacy=%v inspect=%v commit=%v", err, reader, store.eligibilityRead, store.inspected, store.committed)
		}
	})
}

func TestSubmitReplaysBeforeCompleteReviewEligibilityInspection(t *testing.T) {
	request := validRequest(time.Date(2026, 8, 28, 19, 0, 0, 0, time.UTC))
	store := &recordingStore{replay: &Record{ReviewID: request.ReviewID, Candidate: request.Candidate}}
	reader := &recordingEligibilityReader{}
	if _, err := NewService(store).WithEligibilityFacts(reader).Submit(context.Background(), request); err != nil || reader.calls != 0 || store.eligibilityRead {
		t.Fatalf("replay inspected eligibility: err=%v complete=%d legacy=%v", err, reader.calls, store.eligibilityRead)
	}
}

func TestCompleteReviewEligibilityConfigurationCannotBeDowngraded(t *testing.T) {
	denied := completeReviewFact(t, "reconciliation-review-denied", "revision-2", "work-1", workgraph.Invalidate, workgraph.StatusHandedOff, workgraph.EligibilityReview)
	newLegacy := func() *recordingStore {
		return &recordingStore{eligibility: workgraph.Consequence{HandoffEligible: true, RunMayContinue: true, Reason: "permissive legacy tuple"}}
	}
	assertRefused := func(t *testing.T, service *Service, legacy *recordingStore, reader *recordingEligibilityReader) {
		t.Helper()
		request := validRequest(time.Date(2026, 8, 28, 21, 0, 0, 0, time.UTC))
		request.ExpectedEligibility = denied
		_, err := service.WithEligibilityFacts(nil).Submit(context.Background(), request)
		if !IsKind(err, KindRevisionIneligible) || service.eligibility == nil || legacy.eligibilityRead || legacy.inspected || legacy.committed || reader.calls != 1 {
			t.Fatalf("error=%v full=%v full_reads=%d legacy=%v inspect=%v commit=%v", err, service.eligibility != nil, reader.calls, legacy.eligibilityRead, legacy.inspected, legacy.committed)
		}
	}
	t.Run("auto-detected reader survives nil", func(t *testing.T) {
		legacy := newLegacy()
		reader := &recordingEligibilityReader{fact: denied}
		full := &fullRecordingStore{recordingStore: legacy, recordingEligibilityReader: reader}
		var configured Store = full
		assertRefused(t, NewService(configured), legacy, reader)
	})
	t.Run("explicit reader survives nil", func(t *testing.T) {
		legacy := newLegacy()
		reader := &recordingEligibilityReader{fact: denied}
		assertRefused(t, NewService(legacy).WithEligibilityFacts(reader), legacy, reader)
	})
	t.Run("full read failure never falls back", func(t *testing.T) {
		legacy := newLegacy()
		reader := &recordingEligibilityReader{fact: denied, err: errors.New("full authority unavailable")}
		request := validRequest(time.Date(2026, 8, 28, 21, 0, 0, 0, time.UTC))
		request.ExpectedEligibility = denied
		_, err := NewService(legacy).WithEligibilityFacts(reader).Submit(context.Background(), request)
		if !IsKind(err, KindConflict) || reader.calls != 1 || legacy.eligibilityRead || legacy.inspected || legacy.committed {
			t.Fatalf("error=%v full_reads=%d legacy=%v inspect=%v commit=%v", err, reader.calls, legacy.eligibilityRead, legacy.inspected, legacy.committed)
		}
	})
}

func validRequest(now time.Time) Request {
	request := Request{
		SchemaVersion: 1, CorrelationID: "correlation-review-1", OperationID: "operation-review-1",
		ReviewID: "review-1", HandoffID: "handoff-1", WorkRevisionID: "revision-1", WorkID: "work-1",
		LeaseID: "lease-1", LeaseGeneration: 1, Token: "secret-token",
		ActorID: "actor-reviewer", ProcessID: "process-reviewer", Candidate: commit('b'),
		Verdict: Approved, EvidenceDigest: digest('f'), SubmittedAt: now,
		Criteria: []CriterionResult{{CriterionID: "criterion-a", Status: Passed, Evidence: "test A"}, {CriterionID: "criterion-b", Status: Passed, Evidence: "test B"}},
	}
	request.ExpectedEligibility = mustEligibilityFact("reconciliation-review-default", request.WorkRevisionID, "revision-2", request.WorkID, workgraph.StatusHandedOff, workgraph.Fork, workgraph.EligibilityReview)
	return request
}

func mustEligibilityFact(reconciliation string, oldRevision, newRevision ids.WorkRevisionID, workID ids.WorkID, status workgraph.WorkStatus, disposition workgraph.Disposition, operation workgraph.EligibilityOperation) EligibilityFact {
	fact, err := workgraph.NewEligibilityFact(reconciliation, oldRevision, newRevision, workID, status, disposition, operation)
	if err != nil {
		panic(err)
	}
	return fact
}

func commit(character byte) string { return string(makeBytes(character, 40)) }
func digest(character byte) string { return string(makeBytes(character, 64)) }

func makeBytes(character byte, count int) []byte {
	value := make([]byte, count)
	for index := range value {
		value[index] = character
	}
	return value
}

type recordingStore struct {
	scope           Scope
	committed       bool
	replay          *Record
	inspected       bool
	eligibility     workgraph.Consequence
	eligibilityID   string
	eligibilityRead bool
}

type recordingEligibilityReader struct {
	fact        EligibilityFact
	err         error
	calls       int
	oldRevision ids.WorkRevisionID
	workID      ids.WorkID
	status      workgraph.WorkStatus
	operation   workgraph.EligibilityOperation
}

type fullRecordingStore struct {
	*recordingStore
	*recordingEligibilityReader
}

func (r *recordingEligibilityReader) WorkEligibilityFact(_ context.Context, oldRevision ids.WorkRevisionID, workID ids.WorkID, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) (workgraph.EligibilityFact, error) {
	r.calls++
	r.oldRevision, r.workID, r.status, r.operation = oldRevision, workID, status, operation
	return r.fact, r.err
}

func completeReviewFact(t *testing.T, reconciliation string, newRevision ids.WorkRevisionID, workID ids.WorkID, disposition workgraph.Disposition, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) EligibilityFact {
	t.Helper()
	fact, err := workgraph.NewEligibilityFact(reconciliation, "revision-1", newRevision, workID, status, disposition, operation)
	if err != nil {
		t.Fatal(err)
	}
	return fact
}

func (s *recordingStore) WorkEligibility(context.Context, ids.WorkRevisionID, ids.WorkID, workgraph.WorkStatus) (workgraph.Consequence, string, error) {
	s.eligibilityRead = true
	if s.eligibility.Reason == "" {
		return workgraph.Consequence{HandoffEligible: true, IntegrationEligible: true, RunMayContinue: true, Reason: "no reconciliation restricts this WorkRevision generation"}, "", nil
	}
	return s.eligibility, s.eligibilityID, nil
}

func (s *recordingStore) LookupReviewOperation(context.Context, Request) (Record, bool, error) {
	if s.replay != nil {
		return *s.replay, true, nil
	}
	return Record{}, false, nil
}

func (s *recordingStore) InspectReview(context.Context, Request) (Scope, error) {
	s.inspected = true
	return s.scope, nil
}

func (s *recordingStore) CommitReview(_ context.Context, request Request) (Record, error) {
	s.committed = true
	return Record{ReviewID: request.ReviewID, HandoffID: request.HandoffID, Candidate: request.Candidate, ReviewerActorID: request.ActorID, ReviewerProcessID: request.ProcessID, Verdict: request.Verdict, Criteria: request.Criteria, EvidenceDigest: request.EvidenceDigest, SubmittedAt: request.SubmittedAt}, nil
}

type recordingHook struct{ boundaries []string }

func (h *recordingHook) Reach(_ context.Context, boundary string) error {
	h.boundaries = append(h.boundaries, boundary)
	return nil
}
