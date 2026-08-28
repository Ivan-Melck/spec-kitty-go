package integration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

const (
	testBase      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testCandidate = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testResult    = "cccccccccccccccccccccccccccccccccccccccc"
)

func TestIG00ThroughIG09RecoverByGitFact(t *testing.T) {
	for _, boundary := range []string{"IG-00", "IG-01", "IG-02", "IG-03", "IG-04", "IG-05", "IG-06", "IG-07", "IG-08", "IG-09"} {
		boundary := boundary
		t.Run(boundary, func(t *testing.T) {
			store := &memoryStore{}
			git := &memoryGit{target: testBase, workspaceHead: testBase}
			request := validRequest("operation-" + boundary)
			_, err := NewService(store, git).WithFaults(crashOnce{boundary: boundary}).Integrate(context.Background(), request)
			if !errors.Is(err, faults.ErrCrashRequested) {
				t.Fatalf("first attempt error=%v", err)
			}
			record, err := NewService(store, git).Integrate(context.Background(), request)
			if err != nil {
				t.Fatalf("recovery=%v record=%+v", err, record)
			}
			if record.Status != StatusDone || record.ResultCommit != testResult || git.target != testResult || store.doneWrites != 1 {
				t.Fatalf("record=%+v target=%s doneWrites=%d", record, git.target, store.doneWrites)
			}
			replayed, err := NewService(store, git).Integrate(context.Background(), request)
			if err != nil || replayed.Status != StatusDone || store.doneWrites != 1 || git.updates != 1 {
				t.Fatalf("replay=%+v err=%v doneWrites=%d updates=%d", replayed, err, store.doneWrites, git.updates)
			}
		})
	}
}

func TestTargetAdvanceAfterPreflightRequiresExplicitRepair(t *testing.T) {
	store := &memoryStore{}
	git := &memoryGit{target: testBase, workspaceHead: testBase}
	hook := hookFunc(func(boundary string) error {
		if boundary == "IG-03" {
			git.target = "dddddddddddddddddddddddddddddddddddddddd"
		}
		return nil
	})
	record, err := NewService(store, git).WithFaults(hook).Integrate(context.Background(), validRequest("operation-moved"))
	if !IsKind(err, KindTargetMoved) || record.Status != StatusRepairRequired || record.Evidence.Target != git.target || record.RepairAction == "" {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if git.updates != 0 {
		t.Fatalf("target updated despite movement: %d", git.updates)
	}
}

func TestRequestRejectsGitRevisionSyntaxAsTargetRef(t *testing.T) {
	for _, target := range []string{"main", "refs/heads/main^{commit}", "refs/heads/../main", "refs/heads/main.lock", "refs/heads/-main"} {
		request := validRequest("operation-invalid-target")
		request.TargetRef = target
		if err := request.Validate(); !IsKind(err, KindInvalid) {
			t.Fatalf("target %q error=%v", target, err)
		}
	}
}

func TestIntegrationRefusesReconciledWorkBeforeFaultGitAndFence(t *testing.T) {
	store := &memoryStore{eligibility: workgraph.Consequence{Reason: "superseded approval cannot integrate"}, eligibilityID: "reconciliation-integration"}
	git := &memoryGit{target: testBase, workspaceHead: testBase}
	hookCalls := 0
	_, err := NewService(store, git).WithFaults(hookFunc(func(string) error { hookCalls++; return nil })).Integrate(context.Background(), validRequest("operation-ineligible"))
	if !IsKind(err, KindRevisionIneligible) || !store.reviewRead || !store.eligibilityRead || hookCalls != 0 || git.establishCalls != 0 || git.updates != 0 {
		t.Fatalf("error=%v review=%v eligibility=%v hook=%d establish=%d updates=%d", err, store.reviewRead, store.eligibilityRead, hookCalls, git.establishCalls, git.updates)
	}
}

func TestIntegrationConsumesCompleteMixedEligibilityBeforeGitAndFence(t *testing.T) {
	expected := completeIntegrationFact(t, "reconciliation-integration-complete", "revision-2", "work-1", workgraph.Fork, workgraph.StatusApproved, workgraph.EligibilityIntegration)
	tests := map[string]struct {
		expected EligibilityFact
		actual   EligibilityFact
		reader   bool
	}{
		"successor substitution":        {expected, completeIntegrationFact(t, "reconciliation-integration-complete", "revision-other", "work-1", workgraph.Fork, workgraph.StatusApproved, workgraph.EligibilityIntegration), true},
		"disposition substitution":      {expected, completeIntegrationFact(t, "reconciliation-integration-complete", "revision-2", "work-1", workgraph.Continue, workgraph.StatusApproved, workgraph.EligibilityIntegration), true},
		"reconciliation substitution":   {expected, completeIntegrationFact(t, "reconciliation-other", "revision-2", "work-1", workgraph.Fork, workgraph.StatusApproved, workgraph.EligibilityIntegration), true},
		"work substitution":             {expected, completeIntegrationFact(t, "reconciliation-integration-complete", "revision-2", "work-other", workgraph.Fork, workgraph.StatusApproved, workgraph.EligibilityIntegration), true},
		"status operation substitution": {expected, completeIntegrationFact(t, "reconciliation-integration-complete", "revision-2", "work-1", workgraph.Fork, workgraph.StatusHandedOff, workgraph.EligibilityReview), true},
		"omitted expectation":           {EligibilityFact{}, expected, true},
		"reader unavailable":            {expected, EligibilityFact{}, false},
	}
	legacy, err := workgraph.NewLegacyEligibilityFact("reconciliation-integration-complete", "revision-1", "work-1", workgraph.StatusApproved, workgraph.EligibilityIntegration, workgraph.Consequence{IntegrationEligible: true, Reason: "legacy permissive consequence"})
	if err != nil {
		t.Fatal(err)
	}
	tests["legacy fact returned for mixed request"] = struct {
		expected EligibilityFact
		actual   EligibilityFact
		reader   bool
	}{expected, legacy, true}
	oldSubstitution, err := workgraph.NewEligibilityFact("reconciliation-integration-complete", "revision-other", "revision-2", "work-1", workgraph.StatusApproved, workgraph.Fork, workgraph.EligibilityIntegration)
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
			request := validRequest("operation-complete-" + strings.ReplaceAll(name, " ", "-"))
			request.ExpectedEligibility = test.expected
			store := &memoryStore{eligibility: workgraph.Consequence{IntegrationEligible: true, Reason: "permissive legacy stub"}}
			reader := &recordingEligibilityReader{fact: test.actual}
			git := &memoryGit{target: testBase, workspaceHead: testBase}
			hookCalls := 0
			service := NewService(store, git).WithFaults(hookFunc(func(string) error { hookCalls++; return nil }))
			if test.reader {
				service.WithEligibilityFacts(reader)
			}
			_, integrateErr := service.Integrate(context.Background(), request)
			if !IsKind(integrateErr, KindRevisionIneligible) || !store.reviewRead || store.eligibilityRead || hookCalls != 0 || git.establishCalls != 0 || git.updates != 0 {
				t.Fatalf("error=%v review=%v full_reads=%d legacy=%v hook=%d establish=%d updates=%d", integrateErr, store.reviewRead, reader.calls, store.eligibilityRead, hookCalls, git.establishCalls, git.updates)
			}
		})
	}

	t.Run("exact complete fact proceeds", func(t *testing.T) {
		request := validRequest("operation-complete-success")
		request.ExpectedEligibility = expected
		store := &memoryStore{}
		reader := &recordingEligibilityReader{fact: expected}
		git := &memoryGit{target: testBase, workspaceHead: testBase}
		record, err := NewService(store, git).WithEligibilityFacts(reader).Integrate(context.Background(), request)
		if err != nil || record.Status != StatusDone || reader.calls != 1 || reader.oldRevision != "revision-1" || reader.workID != "work-1" || reader.status != workgraph.StatusApproved || reader.operation != workgraph.EligibilityIntegration || store.eligibilityRead || git.establishCalls != 1 {
			t.Fatalf("record=%+v error=%v reader=%+v legacy=%v establish=%d", record, err, reader, store.eligibilityRead, git.establishCalls)
		}
	})
}

func TestCompletedIntegrationReplayPrecedesCompleteEligibilityInspection(t *testing.T) {
	store := &memoryStore{}
	git := &memoryGit{target: testBase, workspaceHead: testBase}
	request := validRequest("operation-complete-replay")
	if _, err := NewService(store, git).Integrate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	store.eligibilityRead = false
	reader := &recordingEligibilityReader{}
	request.ExpectedEligibility = completeIntegrationFact(t, "reconciliation-later", "revision-2", "work-1", workgraph.Invalidate, workgraph.StatusApproved, workgraph.EligibilityIntegration)
	replay, err := NewService(store, git).WithEligibilityFacts(reader).Integrate(context.Background(), request)
	if err != nil || replay.Status != StatusDone || reader.calls != 0 || store.eligibilityRead {
		t.Fatalf("replay=%+v err=%v complete=%d legacy=%v", replay, err, reader.calls, store.eligibilityRead)
	}
}

func TestCompleteIntegrationEligibilityConfigurationCannotBeDowngraded(t *testing.T) {
	denied := completeIntegrationFact(t, "reconciliation-integration-denied", "revision-2", "work-1", workgraph.Invalidate, workgraph.StatusApproved, workgraph.EligibilityIntegration)
	newLegacy := func() *memoryStore {
		return &memoryStore{eligibility: workgraph.Consequence{IntegrationEligible: true, RunMayContinue: true, Reason: "permissive legacy tuple"}}
	}
	assertRefused := func(t *testing.T, service *Service, legacy *memoryStore, reader *recordingEligibilityReader, git *memoryGit) {
		t.Helper()
		_, err := service.WithEligibilityFacts(nil).Integrate(context.Background(), validRequest("operation-nil-downgrade"))
		if !IsKind(err, KindRevisionIneligible) || service.mode != eligibilityComplete || service.eligibility == nil || !legacy.reviewRead || legacy.eligibilityRead || reader.calls != 0 || git.establishCalls != 0 || git.updates != 0 {
			t.Fatalf("error=%v mode=%v full=%v review=%v full_reads=%d legacy=%v establish=%d updates=%d", err, service.mode, service.eligibility != nil, legacy.reviewRead, reader.calls, legacy.eligibilityRead, git.establishCalls, git.updates)
		}
	}
	t.Run("auto-detected reader survives nil", func(t *testing.T) {
		legacy := newLegacy()
		reader := &recordingEligibilityReader{fact: denied}
		full := &fullMemoryStore{memoryStore: legacy, recordingEligibilityReader: reader}
		var configured Store = full
		git := &memoryGit{target: testBase, workspaceHead: testBase}
		assertRefused(t, NewService(configured, git), legacy, reader, git)
	})
	t.Run("explicit reader survives nil", func(t *testing.T) {
		legacy := newLegacy()
		reader := &recordingEligibilityReader{fact: denied}
		git := &memoryGit{target: testBase, workspaceHead: testBase}
		assertRefused(t, NewService(legacy, git).WithEligibilityFacts(reader), legacy, reader, git)
	})
	t.Run("full read failure never falls back", func(t *testing.T) {
		legacy := newLegacy()
		reader := &recordingEligibilityReader{fact: denied, err: errors.New("full authority unavailable")}
		git := &memoryGit{target: testBase, workspaceHead: testBase}
		request := validRequest("operation-full-read-failure")
		request.ExpectedEligibility = denied
		_, err := NewService(legacy, git).WithEligibilityFacts(reader).Integrate(context.Background(), request)
		if !IsKind(err, KindStoreFailure) || reader.calls != 1 || legacy.eligibilityRead || git.establishCalls != 0 || git.updates != 0 {
			t.Fatalf("error=%v full_reads=%d legacy=%v establish=%d updates=%d", err, reader.calls, legacy.eligibilityRead, git.establishCalls, git.updates)
		}
	})
}

func TestCompletedIntegrationReplayBypassesLaterIneligibility(t *testing.T) {
	store := &memoryStore{}
	git := &memoryGit{target: testBase, workspaceHead: testBase}
	request := validRequest("operation-eligible-then-reconciled")
	record, err := NewService(store, git).Integrate(context.Background(), request)
	if err != nil || record.Status != StatusDone {
		t.Fatalf("initial=%+v err=%v", record, err)
	}
	store.eligibility = workgraph.Consequence{Reason: "later invalidation"}
	store.eligibilityID = "reconciliation-later"
	store.eligibilityRead = false
	replay, err := NewService(store, git).Integrate(context.Background(), request)
	if err != nil || replay.Status != StatusDone || store.eligibilityRead {
		t.Fatalf("completed replay=%+v err=%v eligibility=%v", replay, err, store.eligibilityRead)
	}
}

func TestUnrecordedExactWorkspaceResultIsRecovered(t *testing.T) {
	store := &memoryStore{}
	request := validRequest("operation-unrecorded-result")
	record, err := store.PrepareIntegrationAuthority(context.Background(), request, testBase, FenceFacts{})
	if err != nil {
		t.Fatal(err)
	}
	record, err = store.ActivateIntegrationAuthority(context.Background(), record, fenceFactsForRecord(record))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.RecordIntegrationWorkspace(context.Background(), record, WorkspaceFacts{Path: "/managed/integration", Registered: true, Detached: true, Clean: true, Head: testBase, CandidateReachable: true})
	if err != nil {
		t.Fatal(err)
	}
	git := &memoryGit{target: testBase, workspaceHead: testResult}
	got, err := NewService(store, git).Integrate(context.Background(), request)
	if err != nil || got.Status != StatusDone || got.ResultCommit != testResult || git.updates != 1 {
		t.Fatalf("recovered=%+v err=%v updates=%d", got, err, git.updates)
	}
}

func validRequest(operation string) Request {
	return Request{SchemaVersion: 1, CorrelationID: operation, OperationID: ids.OperationID(operation), ReviewID: "review-1", RepositoryID: "repository-1", TargetRef: "refs/heads/main", ActorID: "actor-integrator", ProcessID: "process-integrator", Candidate: testCandidate, LeaseTTL: time.Minute, StartedAt: time.Now().UTC()}
}

type crashOnce struct{ boundary string }

func (h crashOnce) Reach(_ context.Context, boundary string) error {
	if boundary == h.boundary {
		return faults.ErrCrashRequested
	}
	return nil
}

type hookFunc func(string) error

func (h hookFunc) Reach(_ context.Context, boundary string) error { return h(boundary) }

type memoryStore struct {
	mu              sync.Mutex
	record          Record
	found           bool
	doneWrites      int
	eligibility     workgraph.Consequence
	eligibilityID   string
	eligibilityRead bool
	reviewRead      bool
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

type fullMemoryStore struct {
	*memoryStore
	*recordingEligibilityReader
}

func (r *recordingEligibilityReader) WorkEligibilityFact(_ context.Context, oldRevision ids.WorkRevisionID, workID ids.WorkID, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) (workgraph.EligibilityFact, error) {
	r.calls++
	r.oldRevision, r.workID, r.status, r.operation = oldRevision, workID, status, operation
	return r.fact, r.err
}

func completeIntegrationFact(t *testing.T, reconciliation string, newRevision ids.WorkRevisionID, workID ids.WorkID, disposition workgraph.Disposition, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) EligibilityFact {
	t.Helper()
	fact, err := workgraph.NewEligibilityFact(reconciliation, "revision-1", newRevision, workID, status, disposition, operation)
	if err != nil {
		t.Fatal(err)
	}
	return fact
}

func (s *memoryStore) Review(_ context.Context, reviewID ids.ReviewID) (review.Record, error) {
	s.reviewRead = true
	return review.Record{ReviewID: reviewID, HandoffID: "handoff-1", WorkRevisionID: "revision-1", WorkID: "work-1", Candidate: testCandidate, Verdict: review.Approved}, nil
}

func (s *memoryStore) WorkEligibility(context.Context, ids.WorkRevisionID, ids.WorkID, workgraph.WorkStatus) (workgraph.Consequence, string, error) {
	s.eligibilityRead = true
	if s.eligibility.Reason == "" {
		return workgraph.Consequence{HandoffEligible: true, IntegrationEligible: true, RunMayContinue: true, Reason: "no reconciliation restricts this WorkRevision generation"}, "", nil
	}
	return s.eligibility, s.eligibilityID, nil
}

func (s *memoryStore) LookupIntegrationOperation(_ context.Context, _ Request) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.record, s.found, nil
}

func (s *memoryStore) PrepareIntegrationAuthority(_ context.Context, request Request, target string, current FenceFacts) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.found {
		return s.record, nil
	}
	generation := current.Generation + 1
	if current.OperationID == request.OperationID {
		generation = current.Generation
	}
	s.record = Record{OperationID: request.OperationID, ReviewID: request.ReviewID, HandoffID: "handoff-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-1", RepositoryID: request.RepositoryID, TargetRef: request.TargetRef, ActorID: request.ActorID, ProcessID: request.ProcessID, Candidate: request.Candidate, PreflightTarget: target, MutationGeneration: generation, FencePredecessor: current.Object, FenceRequestDigest: "request-digest", Token: "token", LeaseDeadline: request.StartedAt.Add(request.LeaseTTL), Status: StatusAuthorizing, CleanupStatus: CleanupPending, StartedAt: request.StartedAt}
	return s.record, nil
}

func (s *memoryStore) ActivateIntegrationAuthority(_ context.Context, _ Record, fence FenceFacts) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.found = true
	s.record.FenceRef, s.record.FenceObject = fence.Ref, fence.Object
	return s.record, nil
}

func (s *memoryStore) RecordIntegrationWorkspace(_ context.Context, _ Record, facts WorkspaceFacts) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record.WorkspacePath = facts.Path
	if s.record.Status == StatusAuthorizing {
		s.record.Status = StatusPrepared
	}
	return s.record, nil
}

func (s *memoryStore) RevalidateIntegration(_ context.Context, _ Record, target string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target != s.record.PreflightTarget {
		return Record{}, &Error{Kind: KindTargetMoved, Message: "target moved"}
	}
	return s.record, nil
}

func (s *memoryStore) RecordIntegrationResult(_ context.Context, _ Record, result, observed string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record.ResultCommit, s.record.ObservedTarget, s.record.Status = result, observed, StatusResultReady
	return s.record, nil
}

func (s *memoryStore) RecordIntegrationRepair(_ context.Context, _ Record, evidence ConflictEvidence, kind ErrorKind, action string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record.Status, s.record.RepairKind, s.record.RepairAction, s.record.Evidence = StatusRepairRequired, string(kind), action, evidence
	return s.record, nil
}

func (s *memoryStore) CompleteIntegration(_ context.Context, _ Record, target string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.Status != StatusDone {
		s.doneWrites++
		s.record.Status, s.record.ObservedTarget, s.record.DoneAt = StatusDone, target, time.Now().UTC()
	}
	return s.record, nil
}

func (s *memoryStore) PublishIntegrationCleanupEligibility(_ context.Context, _ Record, beforeCommit func() error) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.CleanupStatus == CleanupPending {
		if beforeCommit != nil {
			if err := beforeCommit(); err != nil {
				return Record{}, err
			}
		}
		s.record.CleanupStatus = CleanupEligible
		s.record.CleanupEligibleAt = time.Now().UTC()
	}
	return s.record, nil
}

func (s *memoryStore) CompleteIntegrationCleanup(_ context.Context, _ Record) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record.CleanupStatus = CleanupComplete
	s.record.CleanupCompletedAt = time.Now().UTC()
	return s.record, nil
}

type memoryGit struct {
	target         string
	workspaceHead  string
	updates        int
	fence          FenceFacts
	establishCalls int
}

func (g *memoryGit) TargetHead(context.Context) (string, error) { return g.target, nil }

func (g *memoryGit) EstablishIntegrationAuthority(_ context.Context, prepare func(FenceFacts) (Record, error), activate func(Record, FenceFacts) (Record, error)) (Record, error) {
	g.establishCalls++
	record, err := prepare(g.fence)
	if err != nil {
		return Record{}, err
	}
	g.fence = fenceFactsForRecord(record)
	return activate(record, g.fence)
}

func (g *memoryGit) EnsureIntegrationWorkspace(_ context.Context, _, target, _ string) (WorkspaceFacts, error) {
	if g.workspaceHead == "" {
		g.workspaceHead = target
	}
	return WorkspaceFacts{Path: "/managed/integration", Registered: true, Detached: true, Clean: true, Head: g.workspaceHead, CandidateReachable: true}, nil
}

func (g *memoryGit) IntegrateCandidate(_ context.Context, _ WorkspaceFacts, _, _ string) (string, *ConflictEvidence, error) {
	g.workspaceHead = testResult
	return testResult, nil, nil
}

func (g *memoryGit) PublishIntegrationTarget(_ context.Context, _ Record, expected, target string, immediatelyBefore func() error) error {
	if immediatelyBefore != nil {
		if err := immediatelyBefore(); err != nil {
			return err
		}
	}
	if g.target != expected {
		return &Error{Kind: KindTargetMoved, Message: "expected-old comparison failed"}
	}
	g.target = target
	g.updates++
	return nil
}

func (g *memoryGit) CleanupIntegrationWorkspace(context.Context, Record) error { return nil }

func (g *memoryGit) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	return ancestor == descendant || (ancestor == testBase && descendant == testResult), nil
}

func (g *memoryGit) VerifyIntegrationResult(_ context.Context, result, target, candidate string) (bool, error) {
	return result == testResult && target == testBase && candidate == testCandidate, nil
}

func fenceFactsForRecord(record Record) FenceFacts {
	return FenceFacts{Ref: "refs/spec-kitty/fences/test", Object: testCandidate, PredecessorObject: record.FencePredecessor, Generation: record.MutationGeneration, OperationID: record.OperationID, RepositoryID: record.RepositoryID, TargetRef: record.TargetRef, RequestDigest: record.FenceRequestDigest, PreflightTarget: record.PreflightTarget, TokenDigest: "token-digest", Deadline: record.LeaseDeadline}
}
