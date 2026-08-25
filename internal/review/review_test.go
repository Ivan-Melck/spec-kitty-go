package review

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/handoff"
)

func TestSubmitEvaluatesCanonicalCriteriaBeforeAtomicCommit(t *testing.T) {
	now := time.Date(2026, 8, 24, 17, 0, 0, 0, time.UTC)
	request := validRequest(now)
	store := &recordingStore{scope: Scope{
		Handoff:      handoff.Record{HandoffID: request.HandoffID, Candidate: request.Candidate},
		CriterionIDs: []string{"criterion-a", "criterion-b"},
	}}
	hook := &recordingHook{}
	result, err := NewService(store).WithFaults(hook).Submit(context.Background(), request)
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
	_, err := NewService(store).Submit(context.Background(), request)
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
	if store.inspected || store.committed || len(hook.boundaries) != 0 {
		t.Fatalf("inspect=%v commit=%v boundaries=%v", store.inspected, store.committed, hook.boundaries)
	}
}

func validRequest(now time.Time) Request {
	return Request{
		SchemaVersion: 1, CorrelationID: "correlation-review-1", OperationID: "operation-review-1",
		ReviewID: "review-1", HandoffID: "handoff-1", WorkRevisionID: "revision-1", WorkID: "work-1",
		LeaseID: "lease-1", LeaseGeneration: 1, Token: "secret-token",
		ActorID: "actor-reviewer", ProcessID: "process-reviewer", Candidate: commit('b'),
		Verdict: Approved, EvidenceDigest: digest('f'), SubmittedAt: now,
		Criteria: []CriterionResult{{CriterionID: "criterion-a", Status: Passed, Evidence: "test A"}, {CriterionID: "criterion-b", Status: Passed, Evidence: "test B"}},
	}
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
	scope     Scope
	committed bool
	replay    *Record
	inspected bool
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
