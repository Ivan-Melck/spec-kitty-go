package execution

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

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
	if repo.request.Now != now || repo.runID != ids.RunID("run-fixed") || repo.leaseID != ids.LeaseID("lease-fixed") {
		t.Fatalf("repository call = %+v %s %s", repo.request, repo.runID, repo.leaseID)
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
		LeaseTTL: time.Minute,
	}
}
