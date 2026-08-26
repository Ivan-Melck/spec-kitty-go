package execution

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestStartValidatesBeforeRepositoryCall(t *testing.T) {
	t.Parallel()
	repo := &recordingRepository{}
	service := NewService(repo, fixedGenerator{}, fixedClock{time.Unix(1, 0)})

	_, err := service.Start(context.Background(), StartRequest{})
	if !IsKind(err, KindInvalidCommand) {
		t.Fatalf("Start error = %v", err)
	}
	if repo.called {
		t.Fatal("repository called for invalid command")
	}
}

func TestStartUsesInjectedIdentityAndClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	repo := &recordingRepository{result: RunCapsule{RunID: ids.RunID("run-fixed")}}
	service := NewService(repo, fixedGenerator{}, fixedClock{now})
	req := validRequest()
	req.Now = time.Time{}

	if _, err := service.Start(context.Background(), req); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if repo.request.Now != now || repo.runID != ids.RunID("run-fixed") || repo.leaseID != ids.LeaseID("lease-fixed") || repo.request.GovernanceContext.ID() != ids.GovernanceContextID("context-test") || repo.request.CommandDigest == "" {
		t.Fatalf("repository call = %+v %s %s", repo.request, repo.runID, repo.leaseID)
	}
}

func TestStartReplayAndReplacementKeepTheOriginallyBoundGovernanceContext(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	request := validRequest()
	request.Now = now
	request.CommandDigest = "broader-start-command"
	repository := &statefulGovernanceRepository{}
	service := NewService(repository, fixedGenerator{}, fixedClock{now})

	first, err := service.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.RunID != first.RunID || replay.GovernanceContextID != first.GovernanceContextID || replay.GovernanceContextAggregate != first.GovernanceContextAggregate {
		t.Fatalf("replay=%+v first=%+v", replay, first)
	}

	changedReplay := request
	changedReplay.GovernanceContext = governanceContextFixtureAtRevision("context-test", "revision-2", now)
	if _, err := service.Start(context.Background(), changedReplay); !IsKind(err, KindIdempotencyConflict) {
		t.Fatalf("same-ID/different-content replay error=%v", err)
	}
	if repository.digestConflicts != 1 {
		t.Fatalf("changed content did not participate in caller-supplied command digest: conflicts=%d", repository.digestConflicts)
	}
	replayAfterConflict, err := service.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayAfterConflict.GovernanceContextID != first.GovernanceContextID || replayAfterConflict.GovernanceContextAggregate != first.GovernanceContextAggregate {
		t.Fatalf("failed replay conflict rebound run context: replay=%+v first=%+v", replayAfterConflict, first)
	}

	replacement := request
	replacement.OperationID = "operation-2"
	replacement.CorrelationID = "operation-2"
	replaced, err := service.Start(context.Background(), replacement)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Replayed || replaced.RunID != first.RunID || replaced.Generation != first.Generation+1 || replaced.GovernanceContextID != first.GovernanceContextID || replaced.GovernanceContextAggregate != first.GovernanceContextAggregate {
		t.Fatalf("replacement=%+v first=%+v", replaced, first)
	}

	changedReplacement := replacement
	changedReplacement.OperationID = "operation-3"
	changedReplacement.CorrelationID = "operation-3"
	changedReplacement.GovernanceContext = governanceContextFixtureAtRevision("context-test", "revision-2", now)
	if _, err := service.Start(context.Background(), changedReplacement); !IsKind(err, KindIdempotencyConflict) {
		t.Fatalf("same-ID/different-content replacement error=%v", err)
	}

	replacementAfterConflict := replacement
	replacementAfterConflict.OperationID = "operation-4"
	replacementAfterConflict.CorrelationID = "operation-4"
	replacedAfterConflict, err := service.Start(context.Background(), replacementAfterConflict)
	if err != nil {
		t.Fatal(err)
	}
	if replacedAfterConflict.RunID != first.RunID || replacedAfterConflict.Generation != replaced.Generation+1 || replacedAfterConflict.GovernanceContextID != first.GovernanceContextID || replacedAfterConflict.GovernanceContextAggregate != first.GovernanceContextAggregate {
		t.Fatalf("failed replacement conflict rebound run context: replacement=%+v first=%+v", replacedAfterConflict, first)
	}
}

func TestStartRejectsReplayOrReplacementWithoutRepositoryBoundGovernanceContext(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	for name, result := range map[string]RunCapsule{
		"replay":      {RunID: "run-fixed", Generation: 1, Replayed: true},
		"replacement": {RunID: "run-fixed", Generation: 2},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &recordingRepository{result: result}
			_, err := NewService(repository, fixedGenerator{}, fixedClock{now}).Start(context.Background(), validRequest())
			if !IsKind(err, KindIdempotencyConflict) {
				t.Fatalf("Start error=%v", err)
			}
		})
	}
}

func TestStartRejectsRepositoryContextMismatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	request := validRequest()
	request.Now = now
	other := governanceContextFixture("context-other", now)
	repository := &recordingRepository{result: RunCapsule{GovernanceContextID: other.ID(), GovernanceContextAggregate: other.Aggregate()}}
	_, err := NewService(repository, fixedGenerator{}, fixedClock{now}).Start(context.Background(), request)
	if !IsKind(err, KindIdempotencyConflict) {
		t.Fatalf("Start error=%v", err)
	}
}

func TestStartRejectsInvalidTypedIDBeforeRepositoryCall(t *testing.T) {
	t.Parallel()
	repo := &recordingRepository{}
	service := NewService(repo, fixedGenerator{}, fixedClock{time.Unix(1, 0)})
	request := validRequest()
	request.ProcessID = ids.ProcessID("../escape")

	_, err := service.Start(context.Background(), request)
	if !IsKind(err, KindInvalidCommand) || repo.called {
		t.Fatalf("Start error=%v repository_called=%v", err, repo.called)
	}
}

func TestIdentityEntropyFailureIsTypedAndDoesNotPanic(t *testing.T) {
	t.Parallel()
	repo := &recordingRepository{}
	service := NewService(repo, RandomIdentities{Reader: failingReader{}}, fixedClock{time.Unix(1, 0)})

	_, err := service.Start(context.Background(), validRequest())
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Kind != KindInvalidCommand || diagnostic.RetrySafety != RetrySafeNewOperation || repo.called {
		t.Fatalf("Start error=%#v repository_called=%v", err, repo.called)
	}
}

func TestDiagnosticEnvelopeCarriesRequiredFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	diagnostic := NewDiagnostic(KindAuthorityConflict, "claimed", RetrySafeSameOperation, "correlation-1", ids.OperationID("operation-1"), map[string]string{"work_id": "work-1"}, map[string]string{"lease": "active"}, map[string]string{"lease": "unclaimed"}, now)
	if diagnostic.SchemaVersion != ResultSchemaVersion || diagnostic.CorrelationID == "" || diagnostic.OperationID == "" || diagnostic.RetrySafety == "" || diagnostic.OccurredAt.IsZero() || len(diagnostic.ObservedState) == 0 || len(diagnostic.ExpectedState) == 0 {
		t.Fatalf("incomplete diagnostic: %+v", diagnostic)
	}
}

func TestDiagnosticKindsAreStable(t *testing.T) {
	t.Parallel()
	err := NewError(KindStaleAuthority, "expired token", RetryNever, map[string]string{"work_id": "work-1"})
	if !IsKind(err, KindStaleAuthority) || errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected diagnostic behavior: %v", err)
	}
}

type recordingRepository struct {
	called         bool
	request        StartRequest
	runID          ids.RunID
	leaseID        ids.LeaseID
	result         RunCapsule
	lookupExisting bool
	lookupErr      error
}

func (r *recordingRepository) LookupStartOperation(context.Context, StartRequest) (bool, error) {
	return r.lookupExisting, r.lookupErr
}

func (r *recordingRepository) Claim(_ context.Context, request StartRequest, runID ids.RunID, leaseID ids.LeaseID) (RunCapsule, error) {
	r.called, r.request, r.runID, r.leaseID = true, request, runID, leaseID
	return r.result, nil
}

func (r *recordingRepository) Heartbeat(_ context.Context, request HeartbeatRequest) (time.Time, error) {
	return request.Now.Add(request.LeaseTTL), nil
}

func (r *recordingRepository) Expire(context.Context, ExpireRequest) error { return nil }

type statefulGovernanceRepository struct {
	operations      map[ids.OperationID]governanceOperation
	runID           ids.RunID
	contextID       ids.GovernanceContextID
	aggregate       governancecontext.Commitment
	generation      int64
	digestConflicts int
}

type governanceOperation struct {
	digest  string
	capsule RunCapsule
}

func (r *statefulGovernanceRepository) LookupStartOperation(_ context.Context, request StartRequest) (bool, error) {
	operation, ok := r.operations[request.OperationID]
	if !ok {
		return false, nil
	}
	if operation.digest != request.CommandDigest {
		return false, governanceReplayConflict(request, "operation command digest changed")
	}
	return true, nil
}

func (r *statefulGovernanceRepository) Claim(_ context.Context, request StartRequest, runID ids.RunID, leaseID ids.LeaseID) (RunCapsule, error) {
	if r.operations == nil {
		r.operations = make(map[ids.OperationID]governanceOperation)
	}
	if operation, ok := r.operations[request.OperationID]; ok {
		if operation.digest != request.CommandDigest {
			r.digestConflicts++
			return RunCapsule{}, governanceReplayConflict(request, "operation command digest changed")
		}
		capsule := operation.capsule
		capsule.Replayed = true
		return capsule, nil
	}
	if r.runID == "" {
		r.runID = runID
		r.contextID = request.GovernanceContext.ID()
		r.aggregate = request.GovernanceContext.Aggregate()
	} else if r.contextID != request.GovernanceContext.ID() || r.aggregate != request.GovernanceContext.Aggregate() {
		return RunCapsule{}, governanceReplayConflict(request, "replacement lease governance context changed")
	}
	r.generation++
	capsule := RunCapsule{
		RunID: r.runID, LeaseID: leaseID, Generation: r.generation,
		GovernanceContextID: r.contextID, GovernanceContextAggregate: r.aggregate,
	}
	r.operations[request.OperationID] = governanceOperation{digest: request.CommandDigest, capsule: capsule}
	return capsule, nil
}

func (*statefulGovernanceRepository) Heartbeat(_ context.Context, request HeartbeatRequest) (time.Time, error) {
	return request.Now.Add(request.LeaseTTL), nil
}

func (*statefulGovernanceRepository) Expire(context.Context, ExpireRequest) error { return nil }

func governanceReplayConflict(request StartRequest, message string) error {
	return NewDiagnostic(KindIdempotencyConflict, message, RetryNever, request.CorrelationID, request.OperationID,
		map[string]string{"operation_id": request.OperationID.String()}, nil, nil, request.Now)
}

type fixedGenerator struct{}

func (fixedGenerator) RunID() (ids.RunID, error)     { return ids.RunID("run-fixed"), nil }
func (fixedGenerator) LeaseID() (ids.LeaseID, error) { return ids.LeaseID("lease-fixed"), nil }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func validRequest() StartRequest {
	return StartRequest{
		OperationID: ids.OperationID("operation-1"), ProjectID: ids.ProjectID("project-1"),
		MissionID: ids.MissionID("mission-1"), WorkRevisionID: ids.WorkRevisionID("revision-1"),
		WorkID: ids.WorkID("work-1"), ActorID: ids.ActorID("actor-1"), ProcessID: ids.ProcessID("process-1"),
		LeaseTTL: time.Minute, GovernanceContext: governanceContextFixture("context-test", time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)),
	}
}

func governanceContextFixture(contextID ids.GovernanceContextID, now time.Time) governancecontext.Context {
	return governanceContextFixtureAtRevision(contextID, "revision-1", now)
}

func governanceContextFixtureAtRevision(contextID ids.GovernanceContextID, revision string, now time.Time) governancecontext.Context {
	commitment, err := governancecontext.CommitSHA256([]byte("synthetic test governance"))
	if err != nil {
		panic(err)
	}
	entry, err := governancecontext.NewEntry(governancecontext.KindConfig, "test-config", revision, commitment)
	if err != nil {
		panic(err)
	}
	value, err := governancecontext.NewContext(contextID, governancecontext.SchemaVersion, []governancecontext.Entry{entry}, now)
	if err != nil {
		panic(err)
	}
	return value
}
