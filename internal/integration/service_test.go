package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
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
	mu         sync.Mutex
	record     Record
	found      bool
	doneWrites int
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
	target        string
	workspaceHead string
	updates       int
	fence         FenceFacts
}

func (g *memoryGit) TargetHead(context.Context) (string, error) { return g.target, nil }

func (g *memoryGit) EstablishIntegrationAuthority(_ context.Context, prepare func(FenceFacts) (Record, error), activate func(Record, FenceFacts) (Record, error)) (Record, error) {
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
