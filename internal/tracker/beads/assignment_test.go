package beads

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestAssignmentObservationProjectionReplayAndSafeRefusal(t *testing.T) {
	ctx := context.Background()
	bound, runner, reference := assignmentFixture(t, "original")
	before, err := bound.ObserveAssignment(ctx, reference)
	if err != nil {
		t.Fatal(err)
	}
	if before.Assignee != "original" || before.OperationMarker != "" || before.Observation.Revision == 0 || before.Observation.ProviderUpdatedAt.IsZero() {
		t.Fatalf("incomplete initial assignment observation: %+v", before)
	}
	request := tracker.ProjectAssignmentRequest{
		ExternalRef: reference, OperationID: "operation-assignment", Assignee: "actor-a",
		ExpectedObservation: before.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded,
	}
	mutations := runner.mutationCount()
	applied, err := bound.ProjectAssignment(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Guarantee != tracker.AssignmentUnguarded || applied.Observation.Assignee != "actor-a" || applied.Observation.OperationMarker != request.OperationID.String() || applied.Observation.Observation.Revision == before.Observation.Revision {
		t.Fatalf("assignment was not independently confirmed: %+v", applied)
	}
	if got := runner.mutationCount(); got != mutations+1 {
		t.Fatalf("assignment mutation count = %d, want %d", got, mutations+1)
	}
	replayed, err := bound.ProjectAssignment(ctx, request)
	if err != nil || !replayed.Replayed || !replayed.Noop || runner.mutationCount() != mutations+1 {
		t.Fatalf("same-operation replay duplicated mutation: result=%+v err=%v mutations=%d", replayed, err, runner.mutationCount())
	}

	compensation := tracker.CompensateAssignmentRequest{
		ExternalRef: reference, OperationID: "operation-recovery", ExpectedObservation: applied.Observation.Observation.Revision,
		ExpectedAssignee: "actor-a", ExpectedMarker: request.OperationID.String(), RestoreAssignee: "original",
	}
	observed, err := bound.CompensateAssignment(ctx, compensation)
	if !tracker.IsKind(err, tracker.ErrorSafeCompensationUnsupported) || observed.Observation.Assignee != "actor-a" || observed.Observation.OperationMarker != request.OperationID.String() {
		t.Fatalf("matching state did not produce safe refusal with evidence: result=%+v err=%v", observed, err)
	}
	if runner.mutationCount() != mutations+1 {
		t.Fatalf("safe compensation refusal wrote provider state: %d", runner.mutationCount())
	}
}

func TestAssignmentCapabilityDiscoveryReportsOnlyTheImplementedGuarantee(t *testing.T) {
	ctx := context.Background()
	bound, _, _ := assignmentFixture(t, "original")
	capabilities, err := bound.Capabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Assignment != tracker.AssignmentUnguarded || capabilities.SafeCompensation != tracker.CompensationUnsupported || capabilities.ObservationRevision != tracker.ObservationRevisionContentDerived {
		t.Fatalf("capability reporter overstated or hid assignment guarantees: %+v", capabilities)
	}
	inspection, err := bound.Inspect(ctx, tracker.InspectRequest{ProjectID: bound.binding.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Capabilities.Assignment != tracker.AssignmentUnguarded || inspection.Capabilities.SafeCompensation != tracker.CompensationUnsupported || inspection.Capabilities.ObservationRevision != tracker.ObservationRevisionContentDerived {
		t.Fatalf("inspection disagrees with assignment contract: %+v", inspection.Capabilities)
	}
}

func TestAssignmentRefusesUnsupportedGuaranteesBeforePreflightOrLock(t *testing.T) {
	ctx := context.Background()
	bound, runner, reference := assignmentFixture(t, "original")
	observed, err := bound.ObserveAssignment(ctx, reference)
	if err != nil {
		t.Fatal(err)
	}
	locker := &countingAssignmentLocker{}
	bound.locker = locker
	calls := len(runner.snapshotCalls())
	_, err = bound.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{
		ExternalRef: reference, OperationID: "operation-atomic", Assignee: "provider-actor",
		ExpectedObservation: observed.Observation.Revision, RequiredGuarantee: tracker.AssignmentAtomicClaim,
	})
	if !tracker.IsKind(err, tracker.ErrorGuardedUpdateUnsupported) || len(runner.snapshotCalls()) != calls || locker.calls != 0 {
		t.Fatalf("atomic claim/marker mismatch crossed refusal boundary: err=%v calls=%d/%d locks=%d", err, len(runner.snapshotCalls()), calls, locker.calls)
	}

	conflicting := reference
	conflicting.ProjectID = "project-other"
	_, err = bound.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{ExternalRef: conflicting, OperationID: "operation-conflict", Assignee: "actor-a", ExpectedObservation: observed.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded})
	if !tracker.IsKind(err, tracker.ErrorRevisionConflict) || len(runner.snapshotCalls()) != calls || locker.calls != 0 {
		t.Fatalf("mapping conflict crossed refusal boundary: err=%v calls=%d/%d locks=%d", err, len(runner.snapshotCalls()), calls, locker.calls)
	}
}

func TestAssignmentAmbiguityRecoveryDoesNotDuplicateOrOverstate(t *testing.T) {
	for _, test := range []struct {
		name        string
		apply       bool
		wantSuccess bool
	}{
		{name: "timeout before apply remains ambiguous", apply: false},
		{name: "timeout after apply is confirmed by observation", apply: true, wantSuccess: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			bound, runner, reference := assignmentFixture(t, "original")
			before, err := bound.ObserveAssignment(ctx, reference)
			if err != nil {
				t.Fatal(err)
			}
			runner.ambiguousKind, runner.applyBeforeAmbiguous = CommandUpdate, test.apply
			request := tracker.ProjectAssignmentRequest{ExternalRef: reference, OperationID: "operation-ambiguous", Assignee: "actor-a", ExpectedObservation: before.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded}
			result, err := bound.ProjectAssignment(ctx, request)
			if test.wantSuccess {
				if err != nil || !exactAssignment(result.Observation, "actor-a", request.OperationID.String()) {
					t.Fatalf("applied timeout was not recovered from observation: %+v %v", result, err)
				}
			} else if !tracker.IsKind(err, tracker.ErrorAmbiguous) || result.Observation.Assignee != "original" || result.Observation.OperationMarker != "" {
				t.Fatalf("pre-apply timeout was overstated: %+v %v", result, err)
			}
			if runner.mutationCount() != 2 { // fixture create plus one attempted update
				t.Fatalf("ambiguous operation duplicated provider mutation: %d", runner.mutationCount())
			}
		})
	}
}

func TestAssignmentHumanChangesSurviveOneHundredRecoveryAttempts(t *testing.T) {
	ctx := context.Background()
	bound, runner, reference := assignmentFixture(t, "original")
	before, err := bound.ObserveAssignment(ctx, reference)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := bound.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{ExternalRef: reference, OperationID: "operation-human-race", Assignee: "actor-a", ExpectedObservation: before.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded})
	if err != nil {
		t.Fatal(err)
	}
	baselineWrites := runner.mutationCount()
	for repetition := 0; repetition < 100; repetition++ {
		human := "human-owner"
		runner.mu.Lock()
		issue := runner.issues[reference.ExternalID]
		issue.Assignee = human
		issue.UpdatedAt = runner.tick()
		runner.issues[reference.ExternalID] = issue
		runner.mu.Unlock()
		result, compensateErr := bound.CompensateAssignment(ctx, tracker.CompensateAssignmentRequest{
			ExternalRef: reference, OperationID: ids.OperationID("operation-human-recovery"), ExpectedObservation: applied.Observation.Observation.Revision,
			ExpectedAssignee: "actor-a", ExpectedMarker: "operation-human-race", RestoreAssignee: "original",
		})
		if !tracker.IsKind(compensateErr, tracker.ErrorAttributionLost) || result.Observation.Assignee != human {
			t.Fatalf("repetition %d lost human evidence: result=%+v err=%v", repetition, result, compensateErr)
		}
	}
	if runner.mutationCount() != baselineWrites {
		t.Fatalf("recovery overwrote human state: before=%d after=%d", baselineWrites, runner.mutationCount())
	}
	current, err := bound.ObserveAssignment(ctx, reference)
	if err != nil || current.Assignee != "human-owner" {
		t.Fatalf("human assignment was not preserved: %+v %v", current, err)
	}
}

func assignmentFixture(t *testing.T, assignee string) (*BoundAdapter, *catalogRunner, tracker.ExternalWorkRef) {
	t.Helper()
	bound, runner := newCatalogTestAdapter(t)
	created, err := bound.CreateWork(context.Background(), tracker.CreateWorkRequest{
		ProjectID: "project-1", WorkID: "work-assignment", OperationID: "operation-create-assignment",
		Title: "assignment", MetadataSchema: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	reference := created.Record.ExternalRef
	if assignee != "" {
		runner.mu.Lock()
		issue := runner.issues[reference.ExternalID]
		issue.Assignee = assignee
		issue.UpdatedAt = runner.tick()
		runner.issues[reference.ExternalID] = issue
		runner.mu.Unlock()
	}
	return bound, runner, reference
}

type countingAssignmentLocker struct {
	mu    sync.Mutex
	calls int
}

func (locker *countingAssignmentLocker) acquire(context.Context, tracker.TrackerBinding) (acquiredWriterLock, error) {
	locker.mu.Lock()
	locker.calls++
	locker.mu.Unlock()
	return noopAssignmentLock{}, nil
}

type noopAssignmentLock struct{}

func (noopAssignmentLock) release() {}

func TestAssignmentDiagnosticsDoNotExposeProviderPayload(t *testing.T) {
	err := ambiguousAssignment("bounded assignment outcome", errors.New("secret body and raw transcript"))
	if err.Error() != "tracker ambiguous: bounded assignment outcome" {
		t.Fatalf("diagnostic leaked cause: %q", err)
	}
}
