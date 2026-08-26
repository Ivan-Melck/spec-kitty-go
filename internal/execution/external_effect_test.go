package execution

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
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

func TestProjectionSagaPersistsActualObservationAndReplaysWithoutMutation(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	reference := projectionReference()
	prior := projectionObservation(t, reference, 11, "human", "", now)
	applied := projectionObservation(t, reference, 12, "actor-a", "operation-projection", now.Add(time.Second))
	provider := &projectionProvider{observed: prior, projected: tracker.AssignmentMutation{Observation: applied, Guarantee: tracker.AssignmentUnguarded}}
	repository := &cycle3Repository{}
	request := projectionAssignRequest(now, reference)
	saga := NewTrackerSaga(repository, provider, SystemClock{})
	result, err := saga.Assign(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != 12 || result.Assignee != "actor-a" || repository.effect.ObservationRevision != 12 || repository.effect.ObservationValue != "actor-a" || repository.effect.ObservationMarker != "operation-projection" {
		t.Fatalf("projection result did not use actual provider evidence: result=%+v effect=%+v", result, repository.effect)
	}
	if provider.projects != 1 {
		t.Fatalf("projection mutations = %d", provider.projects)
	}
	replayed, err := saga.Assign(context.Background(), request)
	if err != nil || !replayed.Replayed || !replayed.Noop || provider.projects != 1 {
		t.Fatalf("durable replay duplicated projection: result=%+v err=%v projects=%d", replayed, err, provider.projects)
	}
	changed := request
	changed.Assignee = "actor-b"
	if _, err := saga.Assign(context.Background(), changed); !IsKind(err, KindIdempotencyConflict) || provider.projects != 1 {
		t.Fatalf("operation semantic conflict crossed provider boundary: err=%v projects=%d", err, provider.projects)
	}
}

func TestProjectionSagaSafeCompensationUnsupportedIsDurableDecision(t *testing.T) {
	now := time.Date(2026, 8, 26, 11, 0, 0, 0, time.UTC)
	reference := projectionReference()
	actual := projectionObservation(t, reference, 12, "actor-a", "operation-projection", now)
	effect := projectionEffect(now, reference, actual)
	repository := &cycle3Repository{effect: &effect}
	provider := &projectionProvider{
		compensated:   tracker.AssignmentMutation{Observation: actual, Guarantee: tracker.AssignmentUnguarded, Noop: true},
		compensateErr: &tracker.Error{Kind: tracker.ErrorSafeCompensationUnsupported, RecoveryCode: "reconcile_without_mutation", Message: "conditional primitive absent"},
	}
	saga := NewTrackerSaga(repository, provider, SystemClock{})
	request := projectionCompensateRequest(now, reference)
	_, err := saga.Compensate(context.Background(), request)
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Kind != KindSafeCompensationUnsupported || diagnostic.RetrySafety != RetryNever {
		t.Fatalf("unsupported compensation diagnostic = %#v %v", diagnostic, err)
	}
	if repository.effect.CompensationStatus != CompensationUnsupported || repository.effect.CompensationRevision != 0 || repository.effect.ObservationRevision != 12 || provider.compensations != 1 {
		t.Fatalf("unsupported compensation evidence = effect=%+v calls=%d", repository.effect, provider.compensations)
	}
	_, err = saga.Compensate(context.Background(), request)
	if !errors.As(err, &diagnostic) || diagnostic.RetrySafety != RetryNever || provider.compensations != 1 {
		t.Fatalf("automatic replay retried unsupported mutation: diagnostic=%+v calls=%d", diagnostic, provider.compensations)
	}
}

func TestProjectionSagaAttributionLossPreservesExactHumanObservation(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	reference := projectionReference()
	human := projectionObservation(t, reference, 13, "human-owner", "", now)
	effect := projectionEffect(now, reference, projectionObservation(t, reference, 12, "actor-a", "operation-projection", now.Add(-time.Second)))
	repository := &cycle3Repository{effect: &effect}
	provider := &projectionProvider{
		compensated:   tracker.AssignmentMutation{Observation: human, Guarantee: tracker.AssignmentUnguarded, Noop: true},
		compensateErr: &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "human changed assignment"},
	}
	_, err := NewTrackerSaga(repository, provider, SystemClock{}).Compensate(context.Background(), projectionCompensateRequest(now, reference))
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Kind != KindExternalAttributionLost || diagnostic.RetrySafety != RetryNever {
		t.Fatalf("attribution diagnostic = %+v %v", diagnostic, err)
	}
	if repository.effect.CompensationStatus != CompensationAttributionLost || repository.effect.ObservationRevision != 13 || repository.effect.ObservationValue != "human-owner" || repository.effect.CompensationRevision != 0 {
		t.Fatalf("human observation was spliced or lost: %+v", repository.effect)
	}
}

func TestProjectionSagaUsesStrongFakeConditionalCompensationEndToEnd(t *testing.T) {
	ctx := context.Background()
	store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Seed(ctx, fake.WorkSeed{Key: "external-strong", Title: "strong compensation", Assignee: "original"}); err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(store)
	reference := tracker.ExternalWorkRef{
		ProjectID: "project-strong", WorkID: "work-strong", Provider: "durable_fake", ExternalID: "external-strong",
		MetadataSchema: 1, CreatedByOperation: "operation-create-strong",
	}
	prior, err := provider.ObserveAssignment(ctx, reference)
	if err != nil {
		t.Fatal(err)
	}
	reference.LastObservation = prior.Observation.Revision
	now := time.Date(2026, 8, 26, 12, 15, 0, 0, time.UTC)
	assign := TrackerAssignRequest{
		SchemaVersion: ResultSchemaVersion, CorrelationID: "correlation-strong", OperationID: "operation-strong",
		ActorID: "actor-strong", ProcessID: "process-strong", Now: now, ExpectedRevision: prior.Observation.Revision,
		Assignee: "actor-strong", ExternalRef: reference, RequiredGuarantee: tracker.AssignmentAtomicClaim,
	}
	repository := &cycle3Repository{}
	saga := NewTrackerSaga(repository, provider, SystemClock{})
	applied, err := saga.Assign(ctx, assign)
	if err != nil || applied.Revision == 0 || applied.Assignee != "actor-strong" {
		t.Fatalf("strong assignment result=%+v err=%v", applied, err)
	}
	compensated, err := saga.Compensate(ctx, TrackerCompensateRequest{
		SchemaVersion: assign.SchemaVersion, CorrelationID: assign.CorrelationID, OperationID: assign.OperationID,
		ActorID: assign.ActorID, ProcessID: assign.ProcessID, Now: now.Add(time.Second), ExpectedRevision: applied.Revision,
		ExternalRef: reference, RequiredGuarantee: assign.RequiredGuarantee,
	})
	if err != nil || compensated.Kind != "tracker_effect_compensated" || compensated.Assignee != "original" || compensated.Revision <= applied.Revision {
		t.Fatalf("strong compensation result=%+v err=%v", compensated, err)
	}
	observed, err := provider.ObserveAssignment(ctx, reference)
	if err != nil || observed.Assignee != "original" || observed.OperationMarker != "" || observed.Observation.Revision != compensated.Revision {
		t.Fatalf("strong provider state=%+v err=%v", observed, err)
	}
}

func TestProjectionReplayRejectsEveryImmutableIntentMutationBeforeProvider(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 30, 0, 0, time.UTC)
	reference := projectionReference()
	baseRequest := projectionAssignRequest(now, reference)
	baseEffect := projectionEffect(now, reference, projectionObservation(t, reference, 12, "actor-a", "operation-projection", now))
	mutations := []struct {
		name   string
		mutate func(*TrackerAssignRequest, *ExternalEffect)
	}{
		{"correlation", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.CorrelationID = "correlation-other" }},
		{"actor", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.ActorID = "actor-other" }},
		{"process", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.ProcessID = "process-other" }},
		{"project mapping", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.ExternalRef.ProjectID = "project-other" }},
		{"work mapping", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.ExternalRef.WorkID = "work-other" }},
		{"provider mapping", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.ExternalRef.Provider = "portable-other" }},
		{"external mapping", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.ExternalRef.ExternalID = "external-other" }},
		{"metadata schema", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.ExternalRef.MetadataSchema = 2 }},
		{"creation provenance", func(r *TrackerAssignRequest, _ *ExternalEffect) {
			r.ExternalRef.CreatedByOperation = "operation-create-other"
		}},
		{"mapping observation", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.ExternalRef.LastObservation = 99 }},
		{"desired value", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.Assignee = "actor-other" }},
		{"prior observation", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.ExpectedRevision = 99 }},
		{"marker", func(_ *TrackerAssignRequest, e *ExternalEffect) { e.Marker = "operation-other" }},
		{"guarantee", func(r *TrackerAssignRequest, _ *ExternalEffect) { r.RequiredGuarantee = tracker.AssignmentAtomicClaim }},
	}
	for _, status := range []EffectResponseStatus{EffectPending, EffectConfirmed} {
		for _, test := range mutations {
			t.Run(string(status)+"/"+test.name, func(t *testing.T) {
				request, effect := baseRequest, baseEffect
				effect.ResponseStatus = status
				test.mutate(&request, &effect)
				provider := &projectionProvider{observed: projectionObservation(t, request.ExternalRef, request.ExpectedRevision, "human", "", now)}
				repository := &cycle3Repository{effect: &effect}
				_, err := NewTrackerSaga(repository, provider, SystemClock{}).Assign(context.Background(), request)
				if !IsKind(err, KindIdempotencyConflict) || provider.projects != 0 {
					t.Fatalf("mutated %s replay crossed provider boundary: err=%v projects=%d", test.name, err, provider.projects)
				}
			})
		}
	}
}

func TestProjectionCompensationTerminalReplayRejectsEnvelopeMappingAndGuaranteeMutation(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 45, 0, 0, time.UTC)
	reference := projectionReference()
	baseRequest := projectionCompensateRequest(now, reference)
	baseEffect := projectionEffect(now, reference, projectionObservation(t, reference, 12, "actor-a", "operation-projection", now))
	baseEffect.CompensationStatus = CompensationUnsupported
	mutations := []struct {
		name   string
		mutate func(*TrackerCompensateRequest, *ExternalEffect)
	}{
		{"correlation", func(r *TrackerCompensateRequest, _ *ExternalEffect) { r.CorrelationID = "correlation-other" }},
		{"actor", func(r *TrackerCompensateRequest, _ *ExternalEffect) { r.ActorID = "actor-other" }},
		{"process", func(r *TrackerCompensateRequest, _ *ExternalEffect) { r.ProcessID = "process-other" }},
		{"project mapping", func(r *TrackerCompensateRequest, _ *ExternalEffect) { r.ExternalRef.ProjectID = "project-other" }},
		{"work mapping", func(r *TrackerCompensateRequest, _ *ExternalEffect) { r.ExternalRef.WorkID = "work-other" }},
		{"provider mapping", func(r *TrackerCompensateRequest, _ *ExternalEffect) { r.ExternalRef.Provider = "portable-other" }},
		{"external mapping", func(r *TrackerCompensateRequest, _ *ExternalEffect) { r.ExternalRef.ExternalID = "external-other" }},
		{"metadata schema", func(r *TrackerCompensateRequest, _ *ExternalEffect) { r.ExternalRef.MetadataSchema = 2 }},
		{"creation provenance", func(r *TrackerCompensateRequest, _ *ExternalEffect) {
			r.ExternalRef.CreatedByOperation = "operation-create-other"
		}},
		{"mapping observation", func(r *TrackerCompensateRequest, _ *ExternalEffect) { r.ExternalRef.LastObservation = 99 }},
		{"applied observation", func(r *TrackerCompensateRequest, _ *ExternalEffect) { r.ExpectedRevision = 99 }},
		{"marker", func(_ *TrackerCompensateRequest, e *ExternalEffect) { e.Marker = "operation-other" }},
		{"guarantee", func(r *TrackerCompensateRequest, _ *ExternalEffect) {
			r.RequiredGuarantee = tracker.AssignmentAtomicClaim
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			request, effect := baseRequest, baseEffect
			test.mutate(&request, &effect)
			provider := &projectionProvider{}
			_, err := NewTrackerSaga(&cycle3Repository{effect: &effect}, provider, SystemClock{}).Compensate(context.Background(), request)
			if !IsKind(err, KindIdempotencyConflict) || provider.compensations != 0 {
				t.Fatalf("mutated terminal compensation crossed provider boundary: err=%v calls=%d", err, provider.compensations)
			}
		})
	}
}

func TestProjectionDishonestTerminalErrorsCannotReuseHistoricalObservation(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 55, 0, 0, time.UTC)
	reference := projectionReference()
	for _, test := range []struct {
		name string
		err  error
		obs  func(*testing.T) tracker.AssignmentObservation
	}{
		{name: "unsupported empty", err: &tracker.Error{Kind: tracker.ErrorSafeCompensationUnsupported}, obs: func(*testing.T) tracker.AssignmentObservation { return tracker.AssignmentObservation{} }},
		{name: "attribution empty", err: &tracker.Error{Kind: tracker.ErrorAttributionLost}, obs: func(*testing.T) tracker.AssignmentObservation { return tracker.AssignmentObservation{} }},
		{name: "unsupported partial", err: &tracker.Error{Kind: tracker.ErrorSafeCompensationUnsupported}, obs: func(t *testing.T) tracker.AssignmentObservation {
			partial := projectionObservation(t, reference, 13, "human", "", now)
			partial.Assignee = "spliced-value"
			return partial
		}},
		{name: "attribution partial", err: &tracker.Error{Kind: tracker.ErrorAttributionLost}, obs: func(t *testing.T) tracker.AssignmentObservation {
			partial := projectionObservation(t, reference, 13, "human", "", now)
			partial.OperationMarker = "spliced-marker"
			return partial
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			historical := projectionObservation(t, reference, 12, "actor-a", "operation-projection", now.Add(-time.Second))
			effect := projectionEffect(now, reference, historical)
			repository := &cycle3Repository{effect: &effect}
			provider := &projectionProvider{compensated: tracker.AssignmentMutation{Observation: test.obs(t)}, compensateErr: test.err}
			_, err := NewTrackerSaga(repository, provider, SystemClock{}).Compensate(context.Background(), projectionCompensateRequest(now, reference))
			var diagnostic *DiagnosticError
			if !errors.As(err, &diagnostic) || diagnostic.Kind != KindDecisionRequired || diagnostic.RetrySafety != RetryNever {
				t.Fatalf("dishonest terminal result was not reconciled: %+v %v", diagnostic, err)
			}
			stored := repository.effect
			if stored.CompensationStatus != CompensationReconciliation || stored.ObservationRevision != 0 || stored.ObservationValue != "" || stored.ObservationMarker != "" || stored.CompensationRevision != 0 {
				t.Fatalf("historical observation was relabeled as fresh: %+v", stored)
			}
		})
	}
}

type projectionProvider struct {
	observed      tracker.AssignmentObservation
	projected     tracker.AssignmentMutation
	projectErr    error
	compensated   tracker.AssignmentMutation
	compensateErr error
	projects      int
	compensations int
}

func (provider *projectionProvider) ObserveAssignment(context.Context, tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	return provider.observed, nil
}

func (provider *projectionProvider) ProjectAssignment(_ context.Context, _ tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	provider.projects++
	return provider.projected, provider.projectErr
}

func (provider *projectionProvider) CompensateAssignment(_ context.Context, _ tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	provider.compensations++
	return provider.compensated, provider.compensateErr
}

func projectionReference() tracker.ExternalWorkRef {
	return tracker.ExternalWorkRef{ProjectID: "project-projection", WorkID: "work-projection", Provider: "portable", ExternalID: "external-projection", MetadataSchema: 1, CreatedByOperation: "operation-create", LastObservation: 11}
}

func projectionObservation(t *testing.T, reference tracker.ExternalWorkRef, revision tracker.Revision, assignee, marker string, at time.Time) tracker.AssignmentObservation {
	t.Helper()
	observation, err := tracker.NewProviderObservation(reference.ExternalID, []byte(fmt.Sprintf("%s-%d-%s-%s", reference.ExternalID, revision, assignee, marker)), revision, "", "", at)
	if err != nil {
		t.Fatal(err)
	}
	observation.Assignee, observation.OperationMarker = assignee, marker
	return tracker.AssignmentObservation{ExternalRef: reference, Assignee: assignee, OperationMarker: marker, Observation: observation}
}

func projectionAssignRequest(now time.Time, reference tracker.ExternalWorkRef) TrackerAssignRequest {
	return TrackerAssignRequest{
		SchemaVersion: ResultSchemaVersion, CorrelationID: "correlation-projection", OperationID: "operation-projection",
		ActorID: "actor-projection", ProcessID: "process-projection", Now: now, ExpectedRevision: 11,
		Assignee: "actor-a", ExternalRef: reference, RequiredGuarantee: tracker.AssignmentUnguarded,
	}
}

func projectionCompensateRequest(now time.Time, reference tracker.ExternalWorkRef) TrackerCompensateRequest {
	return TrackerCompensateRequest{
		SchemaVersion: ResultSchemaVersion, CorrelationID: "correlation-projection", OperationID: "operation-projection",
		ActorID: "actor-projection", ProcessID: "process-projection", Now: now, ExpectedRevision: 12, ExternalRef: reference, RequiredGuarantee: tracker.AssignmentUnguarded,
	}
}

func projectionEffect(now time.Time, reference tracker.ExternalWorkRef, actual tracker.AssignmentObservation) ExternalEffect {
	return ExternalEffect{
		SchemaVersion: ResultSchemaVersion, OperationID: "operation-projection", CorrelationID: "correlation-projection",
		ActorID: "actor-projection", ProcessID: "process-projection", Provider: reference.Provider, WorkKey: projectionIntentWorkKey(reference, tracker.AssignmentUnguarded),
		Marker: "operation-projection", PriorRevision: 11, PriorValue: "human", AppliedRevision: 12, AppliedValue: "actor-a",
		ResponseStatus: EffectConfirmed, CompensationStatus: CompensationPending,
		ObservationRevision: actual.Observation.Revision, ObservationValue: actual.Assignee, ObservationMarker: actual.OperationMarker, ObservedAt: now,
	}
}
