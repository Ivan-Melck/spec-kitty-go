package execution

import (
	"context"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestRecoverStartRunUsesOnlyOperationIDAndReprobesLiveStages(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	request := startRunRequestFixture(now)
	repository := &recoveryRepository{
		record:  StartRecoveryRecord{Request: request, OperationStatus: "succeeded", LeaseStatus: "active", WorkspaceStatus: "preparing", WorkspaceStage: workspace.StageWorktreeCreated},
		capsule: RunCapsule{SchemaVersion: ResultSchemaVersion, Kind: "run_started", CorrelationID: request.Claim.CorrelationID, OperationID: request.Claim.OperationID, AggregateIDs: map[string]string{}, RunID: "run-fixed", LeaseID: "lease-fixed", Generation: 7, Token: "fence-token", Deadline: now.Add(time.Minute), Replayed: true},
	}
	authority := NewService(repository, fixedGenerator{}, fixedClock{now: now})
	provider := &startProvider{work: trackerWorkFixture(), mutation: trackerMutationFixture()}
	preparer := &startPreparer{record: readyRecoveryWorkspace(now)}
	saga := NewStartSaga(authority, NewTrackerSaga(&startEffectRepository{}, provider, fixedClock{now: now}), preparer)

	decision, err := saga.Recover(context.Background(), StartRecoveryRequest{SchemaVersion: ResultSchemaVersion, CorrelationID: "recover-correlation", OperationID: "operation-1", Now: now.Add(10 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != RecoveryComplete || decision.Capsule == nil || decision.Capsule.OperationID != "operation-1" || repository.loaded != "operation-1" {
		t.Fatalf("decision=%+v loaded=%s", decision, repository.loaded)
	}
	if repository.claim.ActorID != "actor-1" || repository.claim.ProcessID != "process-1" || repository.claim.GovernanceContext.ID() != request.Claim.GovernanceContext.ID() || provider.assignCalls != 1 || preparer.bindingCalls != 1 || preparer.calls != 1 {
		t.Fatalf("recovery did not use stored envelope/probes: claim=%+v tracker=%d binding=%d ready=%d", repository.claim, provider.assignCalls, preparer.bindingCalls, preparer.calls)
	}
	if repository.completed == nil || repository.completed.WorkspaceStage != workspace.StageReady {
		t.Fatalf("terminal result not recorded: %+v", repository.completed)
	}
}

func TestRecoverStartRunReturnsTypedDecisionForChangedGitFacts(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	request := startRunRequestFixture(now)
	repository := &recoveryRepository{
		record:  StartRecoveryRecord{Request: request, OperationStatus: "succeeded", LeaseStatus: "active", WorkspaceStatus: "preparing", WorkspaceStage: workspace.StageBaseVerified},
		capsule: RunCapsule{SchemaVersion: ResultSchemaVersion, OperationID: request.Claim.OperationID, AggregateIDs: map[string]string{}, RunID: "run-fixed", LeaseID: "lease-fixed", Generation: 7, Token: "fence-token", Deadline: now.Add(time.Minute), Replayed: true},
	}
	preparer := &startPreparer{err: &workspace.Error{Kind: workspace.KindTargetMoved, Message: "dependency head changed", Observed: map[string]string{"head": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, Expected: map[string]string{"head": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
	saga := NewStartSaga(NewService(repository, fixedGenerator{}, fixedClock{now: now}), NewTrackerSaga(&startEffectRepository{}, &startProvider{work: trackerWorkFixture(), mutation: trackerMutationFixture()}, fixedClock{now: now}), preparer)

	decision, err := saga.Recover(context.Background(), StartRecoveryRequest{SchemaVersion: ResultSchemaVersion, CorrelationID: "recover-decision", OperationID: "operation-1", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != RecoveryDecisionRequired || decision.Diagnostic == nil || decision.Diagnostic.Kind != KindTargetChanged || decision.RetrySafety != RetryDecisionRequired || decision.StoredStage != workspace.StageBaseVerified || len(decision.RecoveryActions) == 0 {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestRecoverStartRunReturnsExpiredWithoutInventingReplacement(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	request := startRunRequestFixture(now)
	stale := NewDiagnostic(KindStaleAuthority, "generation expired", RetryDecisionRequired, request.Claim.CorrelationID, request.Claim.OperationID, map[string]string{"lease_id": "lease-old", "generation": "17"}, map[string]string{"lease_status": "expired"}, map[string]string{"lease_status": "active"}, now)
	repository := &recoveryRepository{record: StartRecoveryRecord{Request: request, OperationStatus: "succeeded", LeaseStatus: "expired", WorkspaceStatus: "quarantined", WorkspaceStage: workspace.StageReady}, claimErr: stale}
	saga := NewStartSaga(NewService(repository, fixedGenerator{}, fixedClock{now: now}), NewTrackerSaga(&startEffectRepository{}, &startProvider{}, fixedClock{now: now}), &startPreparer{})

	decision, err := saga.Recover(context.Background(), StartRecoveryRequest{SchemaVersion: ResultSchemaVersion, CorrelationID: "recover-expired", OperationID: "operation-1", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != RecoveryExpired || decision.Diagnostic == nil || decision.Diagnostic.Kind != KindStaleAuthority || decision.Capsule != nil {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestRecoverStartRunExpiresDueAuthorityAndDoesNotResumeWorkspace(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	request := startRunRequestFixture(now)
	repository := &recoveryRepository{record: StartRecoveryRecord{Request: request, OperationStatus: "succeeded", RunID: "run-old", LeaseID: "lease-old", Generation: 17, Deadline: now.Add(time.Minute), Token: "fence-old", LeaseStatus: "active", WorkspaceStatus: "preparing", WorkspaceStage: workspace.StageReserved}}
	saga := NewStartSaga(NewService(repository, fixedGenerator{}, fixedClock{now: now}), NewTrackerSaga(&startEffectRepository{}, &startProvider{}, fixedClock{now: now}), &startPreparer{})

	decision, err := saga.Recover(context.Background(), StartRecoveryRequest{SchemaVersion: ResultSchemaVersion, CorrelationID: "recover-due", OperationID: "operation-1", Now: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != RecoveryExpired || repository.expired == nil || repository.expired.Generation != 17 || repository.expired.Token != "fence-old" || repository.claim.OperationID != "" {
		t.Fatalf("decision=%+v expire=%+v claim=%+v", decision, repository.expired, repository.claim)
	}
}

type recoveryRepository struct {
	record    StartRecoveryRecord
	capsule   RunCapsule
	claimErr  error
	loaded    ids.OperationID
	claim     StartRequest
	completed *RunCapsule
	expired   *ExpireRequest
}

func (r *recoveryRepository) LookupStartOperation(context.Context, StartRequest) (bool, error) {
	return true, nil
}
func (r *recoveryRepository) Claim(_ context.Context, request StartRequest, _ ids.RunID, _ ids.LeaseID) (RunCapsule, error) {
	r.claim = request
	return r.capsule, r.claimErr
}
func (*recoveryRepository) Heartbeat(context.Context, HeartbeatRequest) (time.Time, error) {
	return time.Time{}, nil
}
func (r *recoveryRepository) Expire(_ context.Context, request ExpireRequest) error {
	r.expired = &request
	return nil
}
func (*recoveryRepository) PrepareStartRun(context.Context, StartRunRequest) error { return nil }
func (r *recoveryRepository) LoadStartRun(_ context.Context, operationID ids.OperationID) (StartRecoveryRecord, error) {
	r.loaded = operationID
	return r.record, nil
}
func (r *recoveryRepository) CompleteStartRun(_ context.Context, capsule RunCapsule, _ time.Time) error {
	r.completed = &capsule
	return nil
}

func readyRecoveryWorkspace(now time.Time) workspace.Record {
	return workspace.Record{
		Reservation:   workspace.Reservation{OperationID: "operation-1", RunID: "run-fixed", WorkspaceID: "workspace-1", RepositoryID: "repository-1", LaneID: "lane-1", Authority: workspace.Authority{LeaseID: "lease-fixed", Generation: 7, Token: "fence-token"}, OwnedBranch: "runs/work-1", PinnedBase: "0123456789abcdef0123456789abcdef01234567", Now: now},
		CanonicalPath: "/runtime/workspaces/workspace-1", Stage: workspace.StageReady, Status: "ready", Registered: true,
		ObservedHead: "abcdef0123456789abcdef0123456789abcdef01", DependencyHead: "abcdef0123456789abcdef0123456789abcdef01",
		GitDir: "/repo/.git/worktrees/workspace-1", IndexPath: "/repo/.git/worktrees/workspace-1/index",
	}
}

func trackerWorkFixture() tracker.Work {
	return tracker.Work{Key: "TK-1", Revision: 1}
}

func trackerMutationFixture() tracker.Mutation {
	return tracker.Mutation{WorkKey: "TK-1", Assignee: "agent-1", Marker: "operation-1", Revision: 2}
}
