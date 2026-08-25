package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

func TestCycle1PostProviderApplyPreObservationRecoveryPreservesCompensationEvidence(t *testing.T) {
	ctx := context.Background()
	controlPath := filepath.Join(t.TempDir(), "control.db")
	trackerPath := filepath.Join(t.TempDir(), "tracker.json")
	control, err := sqlite.Open(ctx, controlPath)
	if err != nil {
		t.Fatal(err)
	}
	providerStore, err := fake.Open(trackerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := providerStore.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "crash target", Assignee: "original"}); err != nil {
		t.Fatal(err)
	}
	failing := &failNthEffectSave{ExternalEffectRepository: control, failAt: 2}
	saga := execution.NewTrackerSaga(failing, fake.NewProvider(providerStore), fixedTrackerClock{})
	request := trackerAssignRequest("operation-cycle1-save-loss")
	if _, err := saga.Assign(ctx, request); err == nil {
		t.Fatal("post-provider observation save unexpectedly succeeded")
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	control, err = sqlite.Open(ctx, controlPath)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	saga = execution.NewTrackerSaga(control, fake.NewProvider(providerStore), fixedTrackerClock{})
	replayed, err := saga.Assign(ctx, request)
	if err != nil || !replayed.Replayed {
		t.Fatalf("pending recovery = %+v, %v", replayed, err)
	}
	effect, err := control.ExternalEffect(ctx, request.OperationID)
	if err != nil || effect.PriorRevision != 1 || effect.PriorValue != "original" || effect.AppliedRevision != 2 || effect.ActorID != request.ActorID || effect.ProcessID != request.ProcessID || effect.SchemaVersion != execution.ResultSchemaVersion {
		t.Fatalf("immutable prior evidence corrupted: %+v, %v", effect, err)
	}
	compensated, err := saga.Compensate(ctx, trackerCompensateRequest(request))
	if err != nil || compensated.Assignee != "original" {
		t.Fatalf("compensation = %+v, %v", compensated, err)
	}
	work, err := fake.NewProvider(providerStore).Read(ctx, "TK-1")
	if err != nil || work.Assignee != "original" || work.Revision != 3 {
		t.Fatalf("recovery corrupted compensation evidence: %+v, %v", work, err)
	}
}

func TestCycle1NamedTrackerCrashBoundariesRecoverAcrossProcesses(t *testing.T) {
	for _, boundary := range []string{"SR-03", "SR-04"} {
		t.Run(boundary, func(t *testing.T) {
			ctx := context.Background()
			controlPath := filepath.Join(t.TempDir(), "control.db")
			trackerPath := filepath.Join(t.TempDir(), "tracker.json")
			control, err := sqlite.Open(ctx, controlPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := control.Close(); err != nil {
				t.Fatal(err)
			}
			providerStore, err := fake.Open(trackerPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := providerStore.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "process crash", Assignee: "original"}); err != nil {
				t.Fatal(err)
			}
			barrier := t.TempDir()
			if err := os.WriteFile(filepath.Join(barrier, boundary+".action"), []byte("crash\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			command := trackerHelperCommand(controlPath, trackerPath, barrier, "process-crash", "assign")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err = command.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 86 || stdout.Len() != 0 {
				t.Fatalf("%s crash exit=%v stdout=%q stderr=%q", boundary, err, stdout.String(), stderr.String())
			}
			work, err := fake.NewProvider(providerStore).Read(ctx, "TK-1")
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "SR-03" && (work.Assignee != "original" || work.Revision != 1) {
				t.Fatalf("SR-03 crossed provider boundary: %+v", work)
			}
			if boundary == "SR-04" && (work.Assignee != "agent-a" || work.Revision != 2) {
				t.Fatalf("SR-04 did not persist provider effect: %+v", work)
			}
			if err := os.WriteFile(filepath.Join(barrier, boundary+".action"), []byte("continue\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			replay := trackerHelperCommand(controlPath, trackerPath, barrier, "process-replay", "assign")
			stdout.Reset()
			stderr.Reset()
			replay.Stdout, replay.Stderr = &stdout, &stderr
			if err := replay.Run(); err != nil {
				t.Fatalf("%s replay exit=%v stdout=%q stderr=%q", boundary, err, stdout.String(), stderr.String())
			}
			var response helpercmd.TrackerResponse
			if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || !response.OK || response.Result == nil || !response.Result.Replayed {
				t.Fatalf("%s replay response=%+v decode=%v raw=%q", boundary, response, err, stdout.String())
			}
			control, err = sqlite.Open(ctx, controlPath)
			if err != nil {
				t.Fatal(err)
			}
			effect, err := control.ExternalEffect(ctx, ids.OperationID("operation-process-crash"))
			if err != nil || effect.PriorRevision != 1 || effect.PriorValue != "original" || effect.AppliedRevision != 2 {
				t.Fatalf("%s recovered evidence=%+v err=%v", boundary, effect, err)
			}
			saga := execution.NewTrackerSaga(control, fake.NewProvider(providerStore), fixedTrackerClock{})
			_, err = saga.Compensate(ctx, trackerCompensateRequest(trackerAssignRequest("operation-process-crash")))
			_ = control.Close()
			if err != nil {
				t.Fatalf("%s compensation: %v", boundary, err)
			}
			work, err = fake.NewProvider(providerStore).Read(ctx, "TK-1")
			if err != nil || work.Assignee != "original" || work.Revision != 3 {
				t.Fatalf("%s compensation result=%+v err=%v", boundary, work, err)
			}
		})
	}
}

func TestCycle1ProviderRefusalsAlwaysReturnCompleteDiagnostic(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind execution.ErrorKind
	}{
		{name: "not found", err: &tracker.Error{Kind: tracker.ErrorNotFound, Message: "missing"}, kind: execution.KindNotFound},
		{name: "invalid", err: &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "invalid"}, kind: execution.KindInvalidCommand},
		{name: "unavailable", err: &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "offline"}, kind: execution.KindTrackerUnavailable},
		{name: "ambiguous", err: &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "unknown"}, kind: execution.KindDecisionRequired},
		{name: "revision conflict", err: &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "changed"}, kind: execution.KindDecisionRequired},
		{name: "attribution lost", err: &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "changed"}, kind: execution.KindExternalAttributionLost},
		{name: "unknown provider kind", err: &tracker.Error{Kind: tracker.ErrorKind("unknown"), Message: "unknown"}, kind: execution.KindDecisionRequired},
		{name: "non-provider error", err: errors.New("transport wrapper"), kind: execution.KindDecisionRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			control, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "control.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close()
			saga := execution.NewTrackerSaga(control, refusingProvider{err: test.err}, fixedTrackerClock{})
			request := trackerAssignRequest("operation-refusal-" + safeTestName(test.name))
			_, err = saga.Assign(ctx, request)
			assertCompleteTrackerDiagnostic(t, err, test.kind, request)
		})
	}
}

func TestCycle1TrackerMutationEnvelopeValidationIsComplete(t *testing.T) {
	base := trackerAssignRequest("operation-envelope")
	tests := []struct {
		name   string
		mutate func(*execution.TrackerAssignRequest)
	}{
		{name: "schema", mutate: func(request *execution.TrackerAssignRequest) { request.SchemaVersion = 0 }},
		{name: "correlation", mutate: func(request *execution.TrackerAssignRequest) { request.CorrelationID = "" }},
		{name: "operation", mutate: func(request *execution.TrackerAssignRequest) { request.OperationID = "../bad" }},
		{name: "actor", mutate: func(request *execution.TrackerAssignRequest) { request.ActorID = "../bad" }},
		{name: "process", mutate: func(request *execution.TrackerAssignRequest) { request.ProcessID = "../bad" }},
		{name: "timestamp", mutate: func(request *execution.TrackerAssignRequest) { request.Now = time.Time{} }},
		{name: "provider", mutate: func(request *execution.TrackerAssignRequest) { request.Provider = "" }},
		{name: "work key", mutate: func(request *execution.TrackerAssignRequest) { request.WorkKey = "" }},
		{name: "provider revision", mutate: func(request *execution.TrackerAssignRequest) { request.ExpectedRevision = 0 }},
		{name: "assignee", mutate: func(request *execution.TrackerAssignRequest) { request.Assignee = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			control, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "control.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close()
			request := base
			test.mutate(&request)
			saga := execution.NewTrackerSaga(control, refusingProvider{err: errors.New("provider must not be reached")}, fixedTrackerClock{})
			_, err = saga.Assign(ctx, request)
			var diagnostic *execution.DiagnosticError
			if !errors.As(err, &diagnostic) || diagnostic.Kind != execution.KindInvalidCommand || diagnostic.SchemaVersion != execution.ResultSchemaVersion || len(diagnostic.AggregateIDs) == 0 || len(diagnostic.ObservedState) == 0 || len(diagnostic.ExpectedState) == 0 || len(diagnostic.RecoveryActions) == 0 {
				t.Fatalf("invalid envelope diagnostic = %+v err=%v", diagnostic, err)
			}
		})
	}
}

func TestCycle1ProviderMutationRefusalsAlwaysReturnCompleteDiagnostic(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		kind execution.ErrorKind
	}{
		{name: "not found", err: &tracker.Error{Kind: tracker.ErrorNotFound, Message: "missing"}, kind: execution.KindNotFound},
		{name: "invalid", err: &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "invalid"}, kind: execution.KindInvalidCommand},
		{name: "unavailable", err: &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "offline"}, kind: execution.KindTrackerUnavailable},
		{name: "ambiguous", err: &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "unknown"}, kind: execution.KindDecisionRequired},
		{name: "revision conflict", err: &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "changed"}, kind: execution.KindDecisionRequired},
		{name: "attribution lost", err: &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "changed"}, kind: execution.KindExternalAttributionLost},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			control, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "control.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close()
			saga := execution.NewTrackerSaga(control, mutationRefusingProvider{err: test.err}, fixedTrackerClock{})
			request := trackerAssignRequest("operation-mutation-refusal-" + safeTestName(test.name))
			_, err = saga.Assign(ctx, request)
			assertCompleteTrackerDiagnostic(t, err, test.kind, request)
		})
	}
}

func TestCycle1CompensationReadRefusalReturnsCompleteDiagnostic(t *testing.T) {
	ctx := context.Background()
	control, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	request := trackerAssignRequest("operation-compensation-read-refusal")
	effect := execution.ExternalEffect{
		SchemaVersion: execution.ResultSchemaVersion, OperationID: request.OperationID, CorrelationID: request.CorrelationID,
		ActorID: request.ActorID, ProcessID: request.ProcessID, Provider: request.Provider, WorkKey: request.WorkKey,
		Marker: request.OperationID.String(), PriorRevision: 1, PriorValue: "original", AppliedRevision: 2,
		AppliedValue: request.Assignee, ResponseStatus: execution.EffectConfirmed, CompensationStatus: execution.CompensationPending,
		ObservedAt: request.Now,
	}
	if err := control.SaveExternalEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	saga := execution.NewTrackerSaga(control, compensationReadRefusingProvider{}, fixedTrackerClock{})
	_, err = saga.Compensate(ctx, trackerCompensateRequest(request))
	assertCompleteTrackerDiagnostic(t, err, execution.KindNotFound, request)
}

func TestTrackerSagaLostResponseReplayPersistsObservationWithoutAuthority(t *testing.T) {
	ctx := context.Background()
	controlPath := seedDatabase(t)
	trackerPath := filepath.Join(t.TempDir(), "tracker.json")
	providerStore, err := fake.Open(trackerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := providerStore.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "external work"}); err != nil {
		t.Fatal(err)
	}
	if err := providerStore.Inject(ctx, fake.FailureTimeoutAfterApply); err != nil {
		t.Fatal(err)
	}
	control, err := sqlite.Open(ctx, controlPath)
	if err != nil {
		t.Fatal(err)
	}
	saga := execution.NewTrackerSaga(control, fake.NewProvider(providerStore), fixedTrackerClock{})
	request := trackerAssignRequest("operation-tracker-replay")
	before, err := control.AuthoritySnapshot(ctx, ids.WorkID("work-1"), ids.WorkRevisionID("revision-1"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = saga.Assign(ctx, request)
	if !execution.IsKind(err, execution.KindDecisionRequired) {
		t.Fatalf("lost response error = %v", err)
	}
	ambiguous, err := control.ExternalEffect(ctx, request.OperationID)
	if err != nil || ambiguous.ResponseStatus != execution.EffectAmbiguous {
		t.Fatalf("ambiguous observation = %+v, %v", ambiguous, err)
	}
	confirmed, err := saga.Assign(ctx, request)
	if err != nil || !confirmed.Replayed || confirmed.AppliedRevision != 2 {
		t.Fatalf("observed replay = %+v, %v", confirmed, err)
	}
	after, err := control.AuthoritySnapshot(ctx, ids.WorkID("work-1"), ids.WorkRevisionID("revision-1"))
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("tracker mutation changed authority: before=%+v after=%+v", before, after)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}

	control, err = sqlite.Open(ctx, controlPath)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	persisted, err := control.ExternalEffect(ctx, request.OperationID)
	if err != nil || persisted.ResponseStatus != execution.EffectConfirmed || persisted.AppliedRevision != 2 {
		t.Fatalf("persisted observation = %+v, %v", persisted, err)
	}
	work, err := fake.NewProvider(providerStore).Read(ctx, "TK-1")
	if err != nil || work.Revision != 2 || work.Assignee != "agent-a" {
		t.Fatalf("same operation duplicated assignment: %+v, %v", work, err)
	}
}

func TestTrackerSagaTimeoutBeforeApplyObservesBeforeRetry(t *testing.T) {
	ctx, saga, control, providerStore := newTrackerSaga(t, "")
	defer control.Close()
	if err := providerStore.Inject(ctx, fake.FailureTimeoutBeforeApply); err != nil {
		t.Fatal(err)
	}
	request := trackerAssignRequest("operation-timeout-before")
	_, err := saga.Assign(ctx, request)
	if !execution.IsKind(err, execution.KindDecisionRequired) {
		t.Fatalf("timeout-before error = %v", err)
	}
	work, err := fake.NewProvider(providerStore).Read(ctx, "TK-1")
	if err != nil || work.Revision != 1 || work.Assignee != "" {
		t.Fatalf("ambiguous call was retried without observation: %+v, %v", work, err)
	}
	history, err := providerStore.History(ctx)
	if err != nil || len(history) != 1 || history[0].Kind != "seeded" {
		t.Fatalf("history before explicit replay = %+v, %v", history, err)
	}
	confirmed, err := saga.Assign(ctx, request)
	if err != nil || confirmed.AppliedRevision != 2 {
		t.Fatalf("observed safe replay = %+v, %v", confirmed, err)
	}
}

func TestTrackerSagaAttributionSafeCompensation(t *testing.T) {
	t.Run("later human reassignment is preserved", func(t *testing.T) {
		ctx, saga, control, providerStore := newTrackerSaga(t, "original")
		defer control.Close()
		request := trackerAssignRequest("operation-human-change")
		if _, err := saga.Assign(ctx, request); err != nil {
			t.Fatal(err)
		}
		if err := providerStore.HumanAssign(ctx, "TK-1", "human-owner"); err != nil {
			t.Fatal(err)
		}
		_, err := saga.Compensate(ctx, trackerCompensateRequest(request))
		if !execution.IsKind(err, execution.KindExternalAttributionLost) {
			t.Fatalf("compensation error = %v", err)
		}
		work, err := fake.NewProvider(providerStore).Read(ctx, "TK-1")
		if err != nil || work.Assignee != "human-owner" {
			t.Fatalf("human state overwritten: %+v, %v", work, err)
		}
		effect, err := control.ExternalEffect(ctx, request.OperationID)
		if err != nil || effect.CompensationStatus != execution.CompensationAttributionLost || effect.ConflictRevision != work.Revision || effect.ConflictValue != "human-owner" {
			t.Fatalf("conflict evidence = %+v, %v", effect, err)
		}
	})

	t.Run("attributable effect is undone once", func(t *testing.T) {
		ctx, saga, control, providerStore := newTrackerSaga(t, "original")
		defer control.Close()
		request := trackerAssignRequest("operation-valid-compensation")
		if _, err := saga.Assign(ctx, request); err != nil {
			t.Fatal(err)
		}
		first, err := saga.Compensate(ctx, trackerCompensateRequest(request))
		if err != nil || first.Noop {
			t.Fatalf("first compensation = %+v, %v", first, err)
		}
		second, err := saga.Compensate(ctx, trackerCompensateRequest(request))
		if err != nil || !second.Noop || second.Revision != first.Revision {
			t.Fatalf("second compensation = %+v, %v", second, err)
		}
		work, err := fake.NewProvider(providerStore).Read(ctx, "TK-1")
		if err != nil || work.Assignee != "original" || work.Revision != first.Revision {
			t.Fatalf("compensated work = %+v, %v", work, err)
		}
	})
}

func TestTrackerSagaProviderUnavailableIsTypedAndDurable(t *testing.T) {
	ctx, saga, control, providerStore := newTrackerSaga(t, "")
	defer control.Close()
	if err := providerStore.Inject(ctx, fake.FailureUnavailable); err != nil {
		t.Fatal(err)
	}
	request := trackerAssignRequest("operation-unavailable")
	_, err := saga.Assign(ctx, request)
	var diagnostic *execution.DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Kind != execution.KindTrackerUnavailable || diagnostic.RetrySafety != execution.RetrySafeSameOperation || len(diagnostic.AggregateIDs) == 0 || len(diagnostic.ObservedState) == 0 || len(diagnostic.ExpectedState) == 0 || len(diagnostic.RecoveryActions) < 2 {
		t.Fatalf("unavailable diagnostic = %+v", diagnostic)
	}
	effect, err := control.ExternalEffect(ctx, request.OperationID)
	if err != nil || effect.ResponseStatus != execution.EffectUnavailable {
		t.Fatalf("unavailable observation = %+v, %v", effect, err)
	}
}

func TestTrackerSagaAmbiguousCompensationReplaysWithoutDuplicateUndo(t *testing.T) {
	ctx, saga, control, providerStore := newTrackerSaga(t, "original")
	defer control.Close()
	request := trackerAssignRequest("operation-ambiguous-compensation")
	if _, err := saga.Assign(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := providerStore.Inject(ctx, fake.FailureTimeoutAfterApply); err != nil {
		t.Fatal(err)
	}
	_, err := saga.Compensate(ctx, trackerCompensateRequest(request))
	if !execution.IsKind(err, execution.KindDecisionRequired) {
		t.Fatalf("ambiguous compensation error = %v", err)
	}
	work, err := fake.NewProvider(providerStore).Read(ctx, "TK-1")
	if err != nil || work.Assignee != "original" || work.Revision != 3 {
		t.Fatalf("lost compensation response reality = %+v, %v", work, err)
	}
	replayed, err := saga.Compensate(ctx, trackerCompensateRequest(request))
	if err != nil || !replayed.Noop || replayed.Revision != 3 {
		t.Fatalf("compensation replay = %+v, %v", replayed, err)
	}
	work, err = fake.NewProvider(providerStore).Read(ctx, "TK-1")
	if err != nil || work.Revision != 3 {
		t.Fatalf("compensation duplicated on replay: %+v, %v", work, err)
	}
}

func newTrackerSaga(t *testing.T, assignee string) (context.Context, *execution.TrackerSaga, *sqlite.Store, *fake.Store) {
	t.Helper()
	ctx := context.Background()
	control, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	providerStore, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		control.Close()
		t.Fatal(err)
	}
	if err := providerStore.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "external work", Assignee: assignee}); err != nil {
		control.Close()
		t.Fatal(err)
	}
	return ctx, execution.NewTrackerSaga(control, fake.NewProvider(providerStore), fixedTrackerClock{}), control, providerStore
}

func trackerAssignRequest(operation string) execution.TrackerAssignRequest {
	return execution.TrackerAssignRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-tracker", OperationID: ids.OperationID(operation),
		ActorID: ids.ActorID("actor-tracker"), ProcessID: ids.ProcessID("process-tracker"), Now: fixedTrackerClock{}.Now(),
		Provider: "fake", WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a",
	}
}

func trackerCompensateRequest(assignment execution.TrackerAssignRequest) execution.TrackerCompensateRequest {
	return execution.TrackerCompensateRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: assignment.CorrelationID,
		OperationID: assignment.OperationID, ActorID: assignment.ActorID, ProcessID: assignment.ProcessID,
		Now: fixedTrackerClock{}.Now(), Provider: assignment.Provider, WorkKey: assignment.WorkKey,
		ExpectedRevision: assignment.ExpectedRevision + 1,
	}
}

type failNthEffectSave struct {
	execution.ExternalEffectRepository
	failAt int
	saves  int
}

func (r *failNthEffectSave) SaveExternalEffect(ctx context.Context, effect execution.ExternalEffect) error {
	r.saves++
	if r.saves == r.failAt {
		return errors.New("injected post-provider control save failure")
	}
	return r.ExternalEffectRepository.SaveExternalEffect(ctx, effect)
}

type refusingProvider struct{ err error }

func (p refusingProvider) Read(context.Context, string) (tracker.Work, error) {
	return tracker.Work{}, p.err
}

type mutationRefusingProvider struct{ err error }

func (p mutationRefusingProvider) Read(context.Context, string) (tracker.Work, error) {
	return tracker.Work{Key: "TK-1", Assignee: "original", Revision: 1}, nil
}
func (p mutationRefusingProvider) CompareAndSetAssignment(context.Context, tracker.AssignmentRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, p.err
}
func (p mutationRefusingProvider) CompareAndAddComment(context.Context, tracker.CommentRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, p.err
}
func (p mutationRefusingProvider) CompareBeforeCompensate(context.Context, tracker.CompensationRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, p.err
}

type compensationReadRefusingProvider struct{}

func (compensationReadRefusingProvider) Read(context.Context, string) (tracker.Work, error) {
	return tracker.Work{}, &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work disappeared"}
}
func (compensationReadRefusingProvider) CompareAndSetAssignment(context.Context, tracker.AssignmentRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "unexpected"}
}
func (compensationReadRefusingProvider) CompareAndAddComment(context.Context, tracker.CommentRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "unexpected"}
}
func (compensationReadRefusingProvider) CompareBeforeCompensate(context.Context, tracker.CompensationRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "attribution lost"}
}
func (p refusingProvider) CompareAndSetAssignment(context.Context, tracker.AssignmentRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, p.err
}
func (p refusingProvider) CompareAndAddComment(context.Context, tracker.CommentRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, p.err
}
func (p refusingProvider) CompareBeforeCompensate(context.Context, tracker.CompensationRequest) (tracker.Mutation, error) {
	return tracker.Mutation{}, p.err
}

func trackerHelperCommand(controlPath, trackerPath, barrier, process, action string) *exec.Cmd {
	command := exec.Command(helperBinary, "tracker", action, "--db", controlPath, "--tracker", trackerPath, "--barrier", barrier,
		"--operation", "operation-process-crash", "--correlation", "correlation-process-crash",
		"--actor", "actor-process-crash", "--process", process, "--provider", "fake",
		"--work-key", "TK-1", "--expected-revision", "1", "--assignee", "agent-a",
	)
	return command
}

func assertCompleteTrackerDiagnostic(t *testing.T, err error, kind execution.ErrorKind, request execution.TrackerAssignRequest) {
	t.Helper()
	var diagnostic *execution.DiagnosticError
	if !errors.As(err, &diagnostic) {
		t.Fatalf("provider refusal escaped execution diagnostics: %T %v", err, err)
	}
	if diagnostic.SchemaVersion != execution.ResultSchemaVersion || diagnostic.Kind != kind || diagnostic.CorrelationID != request.CorrelationID || diagnostic.OperationID != request.OperationID || diagnostic.AggregateIDs["actor_id"] != request.ActorID.String() || diagnostic.AggregateIDs["process_id"] != request.ProcessID.String() || diagnostic.AggregateIDs["provider_work_key"] != request.WorkKey || len(diagnostic.ObservedState) == 0 || len(diagnostic.ExpectedState) == 0 || len(diagnostic.RecoveryActions) == 0 || diagnostic.OccurredAt.IsZero() {
		t.Fatalf("incomplete diagnostic: %+v", diagnostic)
	}
}

func safeTestName(value string) string {
	result := make([]byte, 0, len(value))
	for _, character := range []byte(value) {
		if character >= 'a' && character <= 'z' {
			result = append(result, character)
		} else {
			result = append(result, '-')
		}
	}
	return string(result)
}

type fixedTrackerClock struct{}

func (fixedTrackerClock) Now() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) }
