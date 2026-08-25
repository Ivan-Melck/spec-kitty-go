package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestStartSagaPublishesCapsuleOnlyAfterTrackerAndExactWorkspaceReadiness(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	authorityRepository := &recordingRepository{result: RunCapsule{SchemaVersion: ResultSchemaVersion, Kind: "run_started", OperationID: "operation-1", AggregateIDs: map[string]string{}, RunID: "run-fixed", LeaseID: "lease-fixed", Generation: 3, Token: "fence-token", Deadline: now.Add(time.Minute)}}
	authority := NewService(authorityRepository, fixedGenerator{}, fixedClock{now: now})
	effects := &startEffectRepository{}
	provider := &startProvider{work: tracker.Work{Key: "TK-1", Revision: 1}, mutation: tracker.Mutation{WorkKey: "TK-1", Assignee: "agent-1", Marker: "operation-1", Revision: 2}}
	trackerSaga := NewTrackerSaga(effects, provider, fixedClock{now: now})
	preparer := &startPreparer{record: workspace.Record{Reservation: workspace.Reservation{OperationID: "operation-1", RunID: "run-fixed", WorkspaceID: "workspace-1", RepositoryID: "repository-1", LaneID: "lane-1", Authority: workspace.Authority{LeaseID: "lease-fixed", Generation: 3, Token: "fence-token"}, OwnedBranch: "runs/work-1", PinnedBase: "0123456789abcdef0123456789abcdef01234567", Now: now}, CanonicalPath: "/runtime/workspaces/workspace-1", Stage: workspace.StageReady, Status: "ready", Registered: true, ObservedHead: "abcdef0123456789abcdef0123456789abcdef01", DependencyHead: "abcdef0123456789abcdef0123456789abcdef01", GitDir: "/repo/.git/worktrees/workspace-1", IndexPath: "/repo/.git/worktrees/workspace-1/index"}}
	request := startRunRequestFixture(now)
	capsule, err := NewStartSaga(authority, trackerSaga, preparer).Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if capsule.Kind != "run_ready" || capsule.WorkspaceID != "workspace-1" || capsule.WorkspacePath == "" || capsule.VerifiedBase != preparer.record.DependencyHead || capsule.IndexPath == "" || provider.assignCalls != 1 || preparer.calls != 1 {
		t.Fatalf("capsule=%+v provider_calls=%d workspace_calls=%d", capsule, provider.assignCalls, preparer.calls)
	}

	preparer.record.Stage = workspace.StageOwnershipVerified
	preparer.record.Status = "preparing"
	preparer.record.DependencyHead = ""
	_, err = NewStartSaga(authority, trackerSaga, preparer).Start(context.Background(), request)
	if !IsKind(err, KindWorkspaceNotReady) {
		t.Fatalf("unready workspace error=%v", err)
	}
	preparer.err = &workspace.Error{Kind: workspace.KindDependencyConflict, Message: "dependency conflict", Observed: map[string]string{"candidate": "deadbeef"}, Expected: map[string]string{"dependency_merge": "conflict-free"}}
	_, err = NewStartSaga(authority, trackerSaga, preparer).Start(context.Background(), request)
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Kind != KindDependencyConflict || diagnostic.AggregateIDs["workspace_id"] != "workspace-1" || diagnostic.ObservedState["candidate"] != "deadbeef" || diagnostic.ExpectedState["dependency_merge"] != "conflict-free" || diagnostic.RetrySafety != RetrySafeSameOperation || len(diagnostic.RecoveryActions) == 0 {
		t.Fatalf("dependency diagnostic=%+v err=%v", diagnostic, err)
	}
}

func TestStartRunDigestCoversEveryAuthorityBearingField(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	base := startRunRequestFixture(now)
	base.Workspace.Dependencies = []workspace.Dependency{{WorkID: "work-dependency-a", Candidate: "1111111111111111111111111111111111111111"}, {WorkID: "work-dependency-b", Candidate: "2222222222222222222222222222222222222222"}}
	want, err := startRunDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*StartRunRequest){
		"schema": func(r *StartRunRequest) { r.SchemaVersion++ },
		"correlation": func(r *StartRunRequest) {
			r.Claim.CorrelationID = "correlation-other"
			r.Assignment.CorrelationID = "correlation-other"
		},
		"operation": func(r *StartRunRequest) {
			r.Claim.OperationID = "operation-other"
			r.Assignment.OperationID = "operation-other"
		},
		"project":  func(r *StartRunRequest) { r.Claim.ProjectID = "project-other" },
		"mission":  func(r *StartRunRequest) { r.Claim.MissionID = "mission-other" },
		"revision": func(r *StartRunRequest) { r.Claim.WorkRevisionID = "revision-other" },
		"work":     func(r *StartRunRequest) { r.Claim.WorkID = "work-other" },
		"actor":    func(r *StartRunRequest) { r.Claim.ActorID = "actor-other"; r.Assignment.ActorID = "actor-other" },
		"process": func(r *StartRunRequest) {
			r.Claim.ProcessID = "process-other"
			r.Assignment.ProcessID = "process-other"
		},
		"timestamp": func(r *StartRunRequest) {
			r.Claim.Now = r.Claim.Now.Add(time.Second)
			r.Assignment.Now = r.Assignment.Now.Add(time.Second)
		},
		"ttl":              func(r *StartRunRequest) { r.Claim.LeaseTTL++ },
		"provider":         func(r *StartRunRequest) { r.Assignment.Provider = "other" },
		"tracker-work":     func(r *StartRunRequest) { r.Assignment.WorkKey = "TK-2" },
		"tracker-revision": func(r *StartRunRequest) { r.Assignment.ExpectedRevision++ },
		"assignee":         func(r *StartRunRequest) { r.Assignment.Assignee = "agent-other" },
		"repository":       func(r *StartRunRequest) { r.Workspace.RepositoryID = "repository-other" },
		"lane":             func(r *StartRunRequest) { r.Workspace.LaneID = "lane-other" },
		"workspace":        func(r *StartRunRequest) { r.Workspace.WorkspaceID = "workspace-other" },
		"branch":           func(r *StartRunRequest) { r.Workspace.OwnedBranch = "runs/other" },
		"base":             func(r *StartRunRequest) { r.Workspace.PinnedBase = "3333333333333333333333333333333333333333" },
		"dependency": func(r *StartRunRequest) {
			r.Workspace.Dependencies[0].Candidate = "4444444444444444444444444444444444444444"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := base
			changed.Workspace.Dependencies = append([]workspace.Dependency(nil), base.Workspace.Dependencies...)
			mutate(&changed)
			got, err := startRunDigest(changed)
			if err != nil {
				t.Fatal(err)
			}
			if got == want {
				t.Fatalf("field mutation did not change digest %s", want)
			}
		})
	}
	reordered := base
	reordered.Workspace.Dependencies = []workspace.Dependency{base.Workspace.Dependencies[1], base.Workspace.Dependencies[0]}
	if got, err := startRunDigest(reordered); err != nil || got != want {
		t.Fatalf("dependency order changed canonical digest: %s err=%v want=%s", got, err, want)
	}
}

func TestStartRunExistingOperationDigestConflictPrecedesWorkspaceSemantics(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	conflict := NewDiagnostic(KindIdempotencyConflict, "changed operation", RetryNever, "operation-1", "operation-1", nil, nil, nil, now)
	repository := &recordingRepository{lookupErr: conflict}
	preparer := &startPreparer{validateErr: &workspace.Error{Kind: workspace.KindTargetMoved, Message: "semantic preflight should not run"}}
	saga := NewStartSaga(NewService(repository, fixedGenerator{}, fixedClock{now}), NewTrackerSaga(&startEffectRepository{}, &startProvider{}, fixedClock{now}), preparer)
	_, err := saga.Start(context.Background(), startRunRequestFixture(now))
	if !IsKind(err, KindIdempotencyConflict) || preparer.validateCalls != 0 || repository.called {
		t.Fatalf("precedence error=%v validate_calls=%d claim_called=%v", err, preparer.validateCalls, repository.called)
	}
}

func TestStartRunExactReplayAuthenticatesBindingBeforeAuthorityAndTracker(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository := &recordingRepository{lookupExisting: true, result: RunCapsule{Token: "prior-token"}}
	provider := &startProvider{}
	preparer := &startPreparer{bindingErr: &workspace.Error{
		Kind: workspace.KindBindingConflict, Message: "physical repository changed",
		Observed: map[string]string{"common_dir": "/clone/.git"}, Expected: map[string]string{"common_dir": "/repo/.git"},
	}}
	saga := NewStartSaga(NewService(repository, fixedGenerator{}, fixedClock{now}), NewTrackerSaga(&startEffectRepository{}, provider, fixedClock{now}), preparer)

	capsule, err := saga.Start(context.Background(), startRunRequestFixture(now))
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Kind != KindRepositoryConflict || diagnostic.RetrySafety != RetryDecisionRequired {
		t.Fatalf("binding diagnostic=%+v err=%v", diagnostic, err)
	}
	if capsule.Token != "" || repository.called || provider.assignCalls != 0 || preparer.bindingCalls != 1 || preparer.validateCalls != 0 || preparer.calls != 0 {
		t.Fatalf("binding mismatch leaked or mutated: capsule=%+v claim=%v tracker=%d binding=%d validate=%d ready=%d", capsule, repository.called, provider.assignCalls, preparer.bindingCalls, preparer.validateCalls, preparer.calls)
	}
}

func startRunRequestFixture(now time.Time) StartRunRequest {
	return StartRunRequest{
		SchemaVersion: ResultSchemaVersion,
		Claim:         StartRequest{CorrelationID: "operation-1", OperationID: "operation-1", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-1", ActorID: "actor-1", ProcessID: "process-1", Now: now, LeaseTTL: time.Minute},
		Assignment:    TrackerAssignRequest{SchemaVersion: ResultSchemaVersion, CorrelationID: "operation-1", OperationID: "operation-1", ActorID: "actor-1", ProcessID: "process-1", Now: now, Provider: "fake", WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-1"},
		Workspace:     WorkspaceRequest{RepositoryID: "repository-1", LaneID: "lane-1", WorkspaceID: "workspace-1", OwnedBranch: "runs/work-1", PinnedBase: "0123456789abcdef0123456789abcdef01234567"},
	}
}

type startEffectRepository struct{ effect *ExternalEffect }

func (r *startEffectRepository) SaveExternalEffect(_ context.Context, effect ExternalEffect) error {
	copy := effect
	r.effect = &copy
	return nil
}
func (r *startEffectRepository) ExternalEffect(_ context.Context, operationID ids.OperationID) (ExternalEffect, error) {
	if r.effect == nil {
		return ExternalEffect{}, NewDiagnostic(KindNotFound, "missing", RetrySafeSameOperation, operationID.String(), operationID, nil, nil, nil, time.Now())
	}
	return *r.effect, nil
}

type startProvider struct {
	work        tracker.Work
	mutation    tracker.Mutation
	assignCalls int
}

func (p *startProvider) Read(context.Context, string) (tracker.Work, error) { return p.work, nil }
func (p *startProvider) CompareAndSetAssignment(_ context.Context, request tracker.AssignmentRequest) (tracker.Mutation, error) {
	p.assignCalls++
	p.work.Revision, p.work.Assignee, p.work.AssignmentMarker = p.mutation.Revision, request.Assignee, request.Marker
	return p.mutation, nil
}
func (*startProvider) CompareAndAddComment(context.Context, tracker.CommentRequest) (tracker.Mutation, error) {
	panic("unused")
}
func (*startProvider) CompareBeforeCompensate(context.Context, tracker.CompensationRequest) (tracker.Mutation, error) {
	panic("unused")
}

type startPreparer struct {
	record        workspace.Record
	calls         int
	err           error
	validateCalls int
	validateErr   error
	bindingCalls  int
	bindingErr    error
}

func (p *startPreparer) Validate(context.Context, workspace.Reservation) error {
	p.validateCalls++
	return p.validateErr
}

func (p *startPreparer) ValidateBinding(context.Context, workspace.Reservation) error {
	p.bindingCalls++
	return p.bindingErr
}

func (p *startPreparer) EnsureReady(context.Context, workspace.Reservation) (workspace.Record, error) {
	p.calls++
	return p.record, p.err
}
