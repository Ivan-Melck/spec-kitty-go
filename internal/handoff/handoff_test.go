package handoff

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestSubmitBindsVerifiedCandidateAndDurableAuthority(t *testing.T) {
	now := time.Date(2026, 8, 24, 16, 0, 0, 0, time.UTC)
	request := validRequest(now)
	scope := AuthorityScope{
		RepositoryPath: "/repo/.git", OwnedBranch: "runs/work-1",
		PinnedBase: commit('a'), LaneID: "lane-1", CriteriaDigest: digest('c'),
	}
	store := &recordingStore{scope: scope}
	verifier := &recordingVerifier{evidence: CandidateEvidence{
		Candidate: request.Candidate, PinnedBase: scope.PinnedBase,
		OwnedBranch: scope.OwnedBranch, ObservedBranchHead: request.Candidate,
	}}
	hook := &recordingHook{}
	service := NewService(store, verifier).WithFaults(hook)

	record, err := service.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if record.Candidate != request.Candidate || record.CriteriaDigest != scope.CriteriaDigest || record.EvidenceDigest != request.EvidenceDigest {
		t.Fatalf("handoff did not bind immutable evidence: %+v", record)
	}
	if !reflect.DeepEqual(hook.boundaries, []string{"HR-00", "HR-01", "HR-02"}) {
		t.Fatalf("boundaries=%v", hook.boundaries)
	}
	if verifier.scope != scope || verifier.candidate != request.Candidate {
		t.Fatalf("verification input scope=%+v candidate=%s", verifier.scope, verifier.candidate)
	}
}

func TestRequestRejectsMutableCandidateNamesAndMissingContributor(t *testing.T) {
	request := validRequest(time.Now().UTC())
	request.Candidate = "refs/heads/runs/work-1"
	if err := request.Validate(); !IsKind(err, KindInvalid) {
		t.Fatalf("moving ref error=%v", err)
	}
	request = validRequest(time.Now().UTC())
	request.Contributors = nil
	if err := request.Validate(); !IsKind(err, KindInvalid) {
		t.Fatalf("missing contributor error=%v", err)
	}
}

func TestSubmitReturnsDurableReplayBeforeLiveAuthorityAndGit(t *testing.T) {
	request := validRequest(time.Now().UTC())
	want := Record{HandoffID: request.HandoffID, Candidate: request.Candidate, SubmittedAt: request.SubmittedAt}
	store := &recordingStore{replay: &want}
	verifier := &recordingVerifier{}
	hook := &recordingHook{}
	got, err := NewService(store, verifier).WithFaults(hook).Submit(context.Background(), request)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("replay=%+v err=%v", got, err)
	}
	if !store.looked || store.eligibilityRead || store.inspected || verifier.candidate != "" || len(hook.boundaries) != 0 {
		t.Fatalf("lookup=%v eligibility=%v inspect=%v verify=%q boundaries=%v", store.looked, store.eligibilityRead, store.inspected, verifier.candidate, hook.boundaries)
	}
}

func TestSubmitRefusesReconciledWorkBeforeFaultAuthorityAndGit(t *testing.T) {
	request := validRequest(time.Now().UTC())
	store := &recordingStore{eligibility: workgraph.Consequence{Reason: "superseded work cannot hand off"}, eligibilityID: "reconciliation-handoff"}
	verifier := &recordingVerifier{}
	hook := &recordingHook{}
	_, err := NewService(store, verifier).WithFaults(hook).Submit(context.Background(), request)
	if !IsKind(err, KindRevisionIneligible) || !store.eligibilityRead || store.inspected || verifier.candidate != "" || len(hook.boundaries) != 0 {
		t.Fatalf("error=%v eligibility=%v inspect=%v verify=%q boundaries=%v", err, store.eligibilityRead, store.inspected, verifier.candidate, hook.boundaries)
	}
}

func TestSubmitConsumesCompleteMixedEligibilityWithoutLegacyDowngrade(t *testing.T) {
	now := time.Date(2026, 8, 28, 18, 0, 0, 0, time.UTC)
	expected := completeHandoffFact(t, "reconciliation-handoff-complete", "revision-2", "work-1", workgraph.Continue, workgraph.StatusActive, workgraph.EligibilityHandoff)
	tests := map[string]struct {
		expected EligibilityFact
		actual   EligibilityFact
		reader   bool
	}{
		"successor substitution":        {expected, completeHandoffFact(t, "reconciliation-handoff-complete", "revision-other", "work-1", workgraph.Continue, workgraph.StatusActive, workgraph.EligibilityHandoff), true},
		"disposition substitution":      {expected, completeHandoffFact(t, "reconciliation-handoff-complete", "revision-2", "work-1", workgraph.Fork, workgraph.StatusActive, workgraph.EligibilityHandoff), true},
		"reconciliation substitution":   {expected, completeHandoffFact(t, "reconciliation-other", "revision-2", "work-1", workgraph.Continue, workgraph.StatusActive, workgraph.EligibilityHandoff), true},
		"work substitution":             {expected, completeHandoffFact(t, "reconciliation-handoff-complete", "revision-2", "work-other", workgraph.Continue, workgraph.StatusActive, workgraph.EligibilityHandoff), true},
		"status operation substitution": {expected, completeHandoffFact(t, "reconciliation-handoff-complete", "revision-2", "work-1", workgraph.Continue, workgraph.StatusHandedOff, workgraph.EligibilityReview), true},
		"omitted expectation":           {EligibilityFact{}, expected, true},
		"reader unavailable":            {expected, EligibilityFact{}, false},
	}
	legacy, err := workgraph.NewLegacyEligibilityFact("reconciliation-handoff-complete", "revision-1", "work-1", workgraph.StatusActive, workgraph.EligibilityHandoff, workgraph.Consequence{HandoffEligible: true, Reason: "legacy permissive consequence"})
	if err != nil {
		t.Fatal(err)
	}
	tests["legacy fact returned for mixed request"] = struct {
		expected EligibilityFact
		actual   EligibilityFact
		reader   bool
	}{expected, legacy, true}
	oldSubstitution, err := workgraph.NewEligibilityFact("reconciliation-handoff-complete", "revision-other", "revision-2", "work-1", workgraph.StatusActive, workgraph.Continue, workgraph.EligibilityHandoff)
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
			verifier := &recordingVerifier{}
			hook := &recordingHook{}
			service := NewService(store, verifier).WithFaults(hook)
			reader := &recordingEligibilityReader{fact: test.actual}
			if test.reader {
				service.WithEligibilityFacts(reader)
			}
			_, submitErr := service.Submit(context.Background(), request)
			if !IsKind(submitErr, KindRevisionIneligible) || store.eligibilityRead || store.inspected || verifier.candidate != "" || len(hook.boundaries) != 0 {
				t.Fatalf("error=%v full_reads=%d legacy=%v inspect=%v verify=%q boundaries=%v", submitErr, reader.calls, store.eligibilityRead, store.inspected, verifier.candidate, hook.boundaries)
			}
		})
	}

	t.Run("exact complete fact proceeds", func(t *testing.T) {
		request := validRequest(now)
		request.ExpectedEligibility = expected
		store := &recordingStore{scope: AuthorityScope{PinnedBase: commit('a'), OwnedBranch: "runs/work-1", CriteriaDigest: digest('c')}}
		reader := &recordingEligibilityReader{fact: expected}
		verifier := &recordingVerifier{evidence: CandidateEvidence{Candidate: request.Candidate, PinnedBase: commit('a'), OwnedBranch: "runs/work-1", ObservedBranchHead: request.Candidate}}
		if _, err := NewService(store, verifier).WithEligibilityFacts(reader).Submit(context.Background(), request); err != nil || reader.calls != 1 || reader.oldRevision != request.WorkRevisionID || reader.workID != request.WorkID || reader.status != workgraph.StatusActive || reader.operation != workgraph.EligibilityHandoff || store.eligibilityRead || !store.inspected {
			t.Fatalf("error=%v reader=%+v legacy=%v inspect=%v", err, reader, store.eligibilityRead, store.inspected)
		}
	})
}

func TestSubmitReplaysBeforeCompleteEligibilityInspection(t *testing.T) {
	request := validRequest(time.Date(2026, 8, 28, 18, 0, 0, 0, time.UTC))
	store := &recordingStore{replay: &Record{HandoffID: request.HandoffID, Candidate: request.Candidate}}
	reader := &recordingEligibilityReader{}
	if _, err := NewService(store, &recordingVerifier{}).WithEligibilityFacts(reader).Submit(context.Background(), request); err != nil || reader.calls != 0 || store.eligibilityRead {
		t.Fatalf("replay inspected eligibility: err=%v complete=%d legacy=%v", err, reader.calls, store.eligibilityRead)
	}
}

func TestSubmitReturnsDurableRefusalBeforeLiveAuthorityAndGit(t *testing.T) {
	request := validRequest(time.Now().UTC())
	want := Refusal{
		OperationID: request.OperationID, Kind: KindCandidateMismatch,
		Message: "owned branch moved", Observed: map[string]string{"candidate": request.Candidate, "branch_head": commit('c'), "divergence_kind": BranchMovementDivergence},
		Expected: map[string]string{"branch_head": request.Candidate}, RecoveryActions: []string{"reconcile explicitly"}, OccurredAt: request.SubmittedAt,
	}
	store := &recordingStore{refusal: &want}
	verifier := &recordingVerifier{}
	_, err := NewService(store, verifier).Submit(context.Background(), request)
	var mismatch *Error
	if !errors.As(err, &mismatch) || mismatch.Kind != KindCandidateMismatch || !reflect.DeepEqual(mismatch.Observed, want.Observed) || !reflect.DeepEqual(mismatch.Expected, want.Expected) {
		t.Fatalf("refusal replay=%v", err)
	}
	if !store.looked || store.inspected || verifier.candidate != "" || !verifier.revalidated {
		t.Fatalf("lookup=%v inspect=%v verify=%q revalidated=%v", store.looked, store.inspected, verifier.candidate, verifier.revalidated)
	}
}

func TestSubmitReturnsEvidenceConflictInsteadOfStoredPreservationClaim(t *testing.T) {
	request := validRequest(time.Now().UTC())
	want := Refusal{OperationID: request.OperationID, Kind: KindCandidateMismatch, Message: "both Git facts were preserved", Observed: map[string]string{"candidate": request.Candidate}, OccurredAt: request.SubmittedAt}
	conflict := &Error{Kind: KindCandidateMismatch, Message: "handoff evidence ref state conflicts", Observed: map[string]string{"preservation_kind": EvidenceRefMutated}, Expected: map[string]string{"evidence_candidate_ref_head": request.Candidate}, RecoveryActions: evidenceConflictActions()}
	store := &recordingStore{refusal: &want}
	verifier := &recordingVerifier{revalidateErr: conflict}
	_, err := NewService(store, verifier).Submit(context.Background(), request)
	if !errors.Is(err, conflict) || strings.Contains(err.Error(), "preserved") {
		t.Fatalf("replay error=%v", err)
	}
}

func TestSubmitDoesNotRecordRefusalWhenEvidencePreservationFails(t *testing.T) {
	request := validRequest(time.Now().UTC())
	scope := AuthorityScope{RepositoryPath: "/repo", OwnedBranch: "runs/work-1", PinnedBase: commit('a')}
	preservation := errors.New("ref transaction failed")
	verifier := &recordingVerifier{err: &Error{
		Kind: KindCandidateMismatch, Message: "could not preserve",
		Observed:        map[string]string{"divergence_kind": BranchMovementDivergence, "evidence_ref_root": "refs/spec-kitty/handoff-evidence/x"},
		RecoveryActions: []string{"stop"}, Cause: preservation,
	}}
	store := &recordingStore{scope: scope}
	_, err := NewService(store, verifier).Submit(context.Background(), request)
	if !errors.Is(err, preservation) || store.committedRefusal {
		t.Fatalf("error=%v committed_refusal=%v", err, store.committedRefusal)
	}
}

func validRequest(now time.Time) Request {
	return Request{
		SchemaVersion: 1, CorrelationID: "correlation-1", OperationID: "operation-handoff-1",
		HandoffID: "handoff-1", ProjectID: "project-1", MissionID: "mission-1",
		WorkRevisionID: "revision-1", WorkID: "work-1", RunID: "run-1",
		WorkspaceID: "workspace-1", LeaseID: "lease-1", LeaseGeneration: 1,
		Token: "secret-token", ActorID: "actor-implementer", ProcessID: "process-implementer",
		Candidate: commit('b'), Contributors: []ids.ActorID{"actor-implementer"},
		EvidenceDigest: digest('e'), SubmittedAt: now,
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
	scope            AuthorityScope
	replay           *Record
	refusal          *Refusal
	looked           bool
	inspected        bool
	committedRefusal bool
	eligibility      workgraph.Consequence
	eligibilityID    string
	eligibilityRead  bool
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

func (r *recordingEligibilityReader) WorkEligibilityFact(_ context.Context, oldRevision ids.WorkRevisionID, workID ids.WorkID, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) (workgraph.EligibilityFact, error) {
	r.calls++
	r.oldRevision, r.workID, r.status, r.operation = oldRevision, workID, status, operation
	return r.fact, r.err
}

func completeHandoffFact(t *testing.T, reconciliation string, newRevision ids.WorkRevisionID, workID ids.WorkID, disposition workgraph.Disposition, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) EligibilityFact {
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

func (s *recordingStore) LookupHandoffRefusal(context.Context, Request) (Refusal, bool, error) {
	if s.refusal != nil {
		return *s.refusal, true, nil
	}
	return Refusal{}, false, nil
}

func (s *recordingStore) LookupHandoffOperation(context.Context, Request) (Record, bool, error) {
	s.looked = true
	if s.replay != nil {
		return *s.replay, true, nil
	}
	return Record{}, false, nil
}

func (s *recordingStore) InspectHandoffAuthority(context.Context, Request) (AuthorityScope, error) {
	s.inspected = true
	return s.scope, nil
}

func (s *recordingStore) CommitHandoffRefusal(_ context.Context, request Request, mismatch *Error) (Refusal, error) {
	s.committedRefusal = true
	return Refusal{OperationID: request.OperationID, Kind: mismatch.Kind, Message: mismatch.Message, Observed: mismatch.Observed, Expected: mismatch.Expected, RecoveryActions: mismatch.RecoveryActions, OccurredAt: request.SubmittedAt}, nil
}

func (s *recordingStore) CommitHandoff(_ context.Context, request Request, evidence CandidateEvidence) (Record, error) {
	return Record{
		HandoffID: request.HandoffID, ProjectID: request.ProjectID, MissionID: request.MissionID,
		WorkRevisionID: request.WorkRevisionID, WorkID: request.WorkID, RunID: request.RunID,
		WorkspaceID: request.WorkspaceID, LeaseID: request.LeaseID, LeaseGeneration: request.LeaseGeneration,
		LaneID: s.scope.LaneID, Candidate: evidence.Candidate, PinnedBase: evidence.PinnedBase,
		Contributors: request.Contributors, CriteriaDigest: s.scope.CriteriaDigest,
		EvidenceDigest: request.EvidenceDigest, SubmittedAt: request.SubmittedAt,
	}, nil
}

type recordingVerifier struct {
	scope         AuthorityScope
	candidate     string
	evidence      CandidateEvidence
	err           error
	revalidated   bool
	revalidateErr error
}

func (v *recordingVerifier) Verify(_ context.Context, scope AuthorityScope, candidate string) (CandidateEvidence, error) {
	v.scope, v.candidate = scope, candidate
	return v.evidence, v.err
}

func (v *recordingVerifier) RevalidateRefusal(_ context.Context, _ Refusal) error {
	v.revalidated = true
	return v.revalidateErr
}

type recordingHook struct{ boundaries []string }

func (h *recordingHook) Reach(_ context.Context, boundary string) error {
	h.boundaries = append(h.boundaries, boundary)
	return nil
}
