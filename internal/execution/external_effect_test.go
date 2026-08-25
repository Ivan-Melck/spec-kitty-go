package execution

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestCycle3AssignRepositoryFailuresAreCompleteDiagnostics(t *testing.T) {
	sentinel := errors.New("control repository failure")
	request := cycle3AssignRequest()
	pending := cycle3Effect(request)
	confirmedProvider := cycle3Provider{work: tracker.Work{Key: "TK-1", Assignee: "original", Revision: 1}, assignment: tracker.Mutation{WorkKey: "TK-1", Assignee: "agent-a", Marker: request.OperationID.String(), Revision: 2}}
	for _, test := range []struct {
		name     string
		repo     *cycle3Repository
		provider cycle3Provider
	}{
		{name: "initial read", repo: &cycle3Repository{readErr: sentinel}, provider: confirmedProvider},
		{name: "pending save", repo: &cycle3Repository{failSaveAt: 1, saveErr: sentinel}, provider: confirmedProvider},
		{name: "confirmed save", repo: &cycle3Repository{failSaveAt: 2, saveErr: sentinel}, provider: confirmedProvider},
		{name: "ambiguity save", repo: &cycle3Repository{failSaveAt: 2, saveErr: sentinel}, provider: cycle3Provider{work: confirmedProvider.work, assignmentErr: &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "timeout"}}},
		{name: "provider refusal save", repo: &cycle3Repository{failSaveAt: 2, saveErr: sentinel}, provider: cycle3Provider{work: confirmedProvider.work, assignmentErr: &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "offline"}}},
		{name: "confirmed observation save", repo: &cycle3Repository{effect: &pending, failSaveAt: 1, saveErr: sentinel}, provider: cycle3Provider{work: tracker.Work{Key: "TK-1", Assignee: "agent-a", AssignmentMarker: request.OperationID.String(), Revision: 2}}},
		{name: "conflict observation save", repo: &cycle3Repository{effect: &pending, failSaveAt: 1, saveErr: sentinel}, provider: cycle3Provider{work: tracker.Work{Key: "TK-1", Assignee: "human", Revision: 2}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			saga := NewTrackerSaga(test.repo, test.provider, SystemClock{})
			_, err := saga.Assign(context.Background(), request)
			assertCycle3RepositoryDiagnostic(t, err, sentinel, assignEnvelope(request))
		})
	}
}

func TestCycle3CompensateRepositoryFailuresAreCompleteDiagnostics(t *testing.T) {
	sentinel := errors.New("control repository failure")
	request := cycle3CompensateRequest()
	effect := cycle3Effect(cycle3AssignRequest())
	effect.ResponseStatus = EffectConfirmed
	effect.AppliedRevision = 2
	for _, test := range []struct {
		name     string
		repo     *cycle3Repository
		provider cycle3Provider
	}{
		{name: "initial read", repo: &cycle3Repository{readErr: sentinel}},
		{name: "successful save", repo: &cycle3Repository{effect: &effect, failSaveAt: 1, saveErr: sentinel}, provider: cycle3Provider{compensation: tracker.Mutation{WorkKey: "TK-1", Assignee: "original", Revision: 3}}},
		{name: "replayed successful save", repo: &cycle3Repository{effect: &effect, failSaveAt: 1, saveErr: sentinel}, provider: cycle3Provider{compensation: tracker.Mutation{WorkKey: "TK-1", Assignee: "original", Revision: 3, Replayed: true, Noop: true}}},
		{name: "attribution loss save", repo: &cycle3Repository{effect: &effect, failSaveAt: 1, saveErr: sentinel}, provider: cycle3Provider{work: tracker.Work{Key: "TK-1", Assignee: "human", Revision: 3}, compensationErr: &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "changed"}}},
		{name: "ambiguity save", repo: &cycle3Repository{effect: &effect, failSaveAt: 1, saveErr: sentinel}, provider: cycle3Provider{compensationErr: &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "timeout"}}},
		{name: "unavailable save", repo: &cycle3Repository{effect: &effect, failSaveAt: 1, saveErr: sentinel}, provider: cycle3Provider{compensationErr: &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "offline"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			saga := NewTrackerSaga(test.repo, test.provider, SystemClock{})
			_, err := saga.Compensate(context.Background(), request)
			assertCycle3RepositoryDiagnostic(t, err, sentinel, compensateEnvelope(request))
		})
	}
}

func TestCycle3SuccessfulCompensationCarriesActualProviderObservation(t *testing.T) {
	request := cycle3CompensateRequest()
	effect := cycle3Effect(cycle3AssignRequest())
	effect.ResponseStatus = EffectConfirmed
	effect.AppliedRevision = 2
	repository := &cycle3Repository{effect: &effect}
	provider := cycle3Provider{compensation: tracker.Mutation{WorkKey: "TK-1", Assignee: "provider-actual", Marker: "provider-marker", Revision: 9}}
	saga := NewTrackerSaga(repository, provider, SystemClock{})
	if _, err := saga.Compensate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	stored := repository.effect
	if stored.ObservationRevision != 9 || stored.ObservationValue != "provider-actual" || stored.ObservationMarker != "provider-marker" {
		t.Fatalf("stale observation carried to repository: %+v", stored)
	}
}

func assertCycle3RepositoryDiagnostic(t *testing.T, err, cause error, envelope trackerEnvelope) {
	t.Helper()
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) {
		t.Fatalf("failure escaped typed diagnostics: %T %v", err, err)
	}
	if !errors.Is(err, cause) || diagnostic.SchemaVersion != ResultSchemaVersion || diagnostic.Kind != KindControlStoreBusy || diagnostic.CorrelationID != envelope.CorrelationID || diagnostic.OperationID != envelope.OperationID || diagnostic.AggregateIDs["actor_id"] != envelope.ActorID.String() || diagnostic.AggregateIDs["process_id"] != envelope.ProcessID.String() || diagnostic.AggregateIDs["expected_provider_revision"] != fmt.Sprint(envelope.ExpectedRevision) || diagnostic.OccurredAt != envelope.Now || diagnostic.RetrySafety != RetrySafeSameOperation || len(diagnostic.ObservedState) == 0 || len(diagnostic.ExpectedState) == 0 || len(diagnostic.RecoveryActions) < 2 {
		t.Fatalf("incomplete repository diagnostic: %+v cause=%v", diagnostic, err)
	}
}

type cycle3Repository struct {
	effect     *ExternalEffect
	readErr    error
	saveErr    error
	failSaveAt int
	saves      int
}

func (r *cycle3Repository) ExternalEffect(context.Context, ids.OperationID) (ExternalEffect, error) {
	if r.readErr != nil {
		return ExternalEffect{}, r.readErr
	}
	if r.effect == nil {
		return ExternalEffect{}, NewDiagnostic(KindNotFound, "missing", RetryNever, "", "", nil, nil, nil, time.Now())
	}
	return *r.effect, nil
}

func (r *cycle3Repository) SaveExternalEffect(_ context.Context, effect ExternalEffect) error {
	r.saves++
	if r.saves == r.failSaveAt {
		return r.saveErr
	}
	r.effect = &effect
	return nil
}

type cycle3Provider struct {
	work            tracker.Work
	readErr         error
	assignment      tracker.Mutation
	assignmentErr   error
	compensation    tracker.Mutation
	compensationErr error
}

func (p cycle3Provider) Read(context.Context, string) (tracker.Work, error) {
	return p.work, p.readErr
}
func (p cycle3Provider) CompareAndSetAssignment(context.Context, tracker.AssignmentRequest) (tracker.Mutation, error) {
	return p.assignment, p.assignmentErr
}
func (p cycle3Provider) CompareAndAddComment(context.Context, tracker.CommentRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, nil
}
func (p cycle3Provider) CompareBeforeCompensate(context.Context, tracker.CompensationRequest) (tracker.Mutation, error) {
	return p.compensation, p.compensationErr
}

func cycle3AssignRequest() TrackerAssignRequest {
	return TrackerAssignRequest{
		SchemaVersion: ResultSchemaVersion, CorrelationID: "correlation-cycle3", OperationID: ids.OperationID("operation-cycle3"),
		ActorID: ids.ActorID("actor-cycle3"), ProcessID: ids.ProcessID("process-cycle3"),
		Now: time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC), Provider: "fake", WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a",
	}
}

func cycle3CompensateRequest() TrackerCompensateRequest {
	assign := cycle3AssignRequest()
	return TrackerCompensateRequest{
		SchemaVersion: assign.SchemaVersion, CorrelationID: assign.CorrelationID, OperationID: assign.OperationID,
		ActorID: assign.ActorID, ProcessID: assign.ProcessID, Now: assign.Now,
		Provider: assign.Provider, WorkKey: assign.WorkKey, ExpectedRevision: 2,
	}
}

func cycle3Effect(request TrackerAssignRequest) ExternalEffect {
	return ExternalEffect{
		SchemaVersion: request.SchemaVersion, OperationID: request.OperationID, CorrelationID: request.CorrelationID,
		ActorID: request.ActorID, ProcessID: request.ProcessID, Provider: request.Provider, WorkKey: request.WorkKey,
		Marker: request.OperationID.String(), PriorRevision: 1, PriorValue: "original", AppliedValue: request.Assignee,
		ResponseStatus: EffectPending, CompensationStatus: CompensationPending, ObservedAt: request.Now,
	}
}
