package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const realV122AssignmentRecord = `{"id":"skt-a1b2","status":"in_progress","assignee":"actor-a","metadata":{"foreign":{"number":9007199254740993,"preserved":true},"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-1","created_by_operation":"operation-create","last_operation":"operation-assign","assignment_marker":"operation-assign","criteria":[{"id":"criterion-1","text":"verified","verifier":"test","required":true}],"design_context":"assignment fixture","content_digest":"diagnostic-only"}},"updated_at":"2026-08-26T12:01:00Z"}`

func TestRealV122AssignmentMutationRequiresOneArrayRecord(t *testing.T) {
	record, err := decodeSingularAssignmentMutation([]byte(`[` + realV122AssignmentRecord + `]`))
	if err != nil {
		t.Fatal(err)
	}
	if record.issue.externalID != "skt-a1b2" || pointerValue(record.issue.assignee) != "actor-a" || pointerValue(record.status) != "in_progress" || record.issue.document.kitty.AssignmentMarker != "operation-assign" {
		t.Fatalf("assignment record lost visible state: %#v", record)
	}
	if !bytes.Contains(record.issue.metadata, []byte(`9007199254740993`)) || !bytes.Contains(record.issue.metadata, []byte(`"preserved":true`)) {
		t.Fatalf("assignment record lost foreign metadata: %s", record.issue.metadata)
	}
	for _, payload := range []string{
		realV122AssignmentRecord,
		`[]`,
		`[` + realV122AssignmentRecord + `,` + realV122AssignmentRecord + `]`,
	} {
		if _, err := decodeSingularAssignmentMutation([]byte(payload)); !tracker.IsKind(err, tracker.ErrorMalformedProtocol) {
			t.Fatalf("singular assignment mutant %s error=%v", payload, err)
		}
	}
}

func TestAssignmentObservationProjectionReplayAndSafeRefusal(t *testing.T) {
	ctx := context.Background()
	bound, runner, reference := assignmentFixture(t, "original")
	preserveAssignmentForeignMetadata(t, runner, reference.ExternalID)
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
	runner.mu.Lock()
	preservedMetadata := append([]byte(nil), runner.issues[reference.ExternalID].Metadata...)
	runner.mu.Unlock()
	if !bytes.Contains(preservedMetadata, []byte(`9007199254740993`)) || !bytes.Contains(preservedMetadata, []byte(`"preserved":true`)) {
		t.Fatalf("assignment update lost foreign metadata: %s", preservedMetadata)
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

func TestAssignmentRejectsNonSingularMutationResponsesAsAmbiguous(t *testing.T) {
	for _, shape := range []string{"object", "empty", "multiple"} {
		t.Run(shape, func(t *testing.T) {
			ctx := context.Background()
			bound, runner, reference := assignmentFixture(t, "original")
			before, err := bound.ObserveAssignment(ctx, reference)
			if err != nil {
				t.Fatal(err)
			}
			bound.adapter = newAdapter(&assignmentResponseShapeRunner{delegate: runner, shape: shape})
			request := tracker.ProjectAssignmentRequest{
				ExternalRef: reference, OperationID: "operation-shape", Assignee: "actor-a",
				ExpectedObservation: before.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded,
			}
			result, err := bound.ProjectAssignment(ctx, request)
			if !tracker.IsKind(err, tracker.ErrorAmbiguous) {
				t.Fatalf("%s response was not rejected as ambiguous: result=%+v err=%v", shape, result, err)
			}
			if !exactAssignment(result.Observation, request.Assignee, request.OperationID.String()) {
				t.Fatalf("%s response lost independently observed conflict evidence: %+v", shape, result)
			}
			if runner.mutationCount() != 2 { // fixture create plus exactly one assignment update
				t.Fatalf("%s response duplicated mutation: %d", shape, runner.mutationCount())
			}
		})
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

func TestAssignmentHumanChangesSurviveOneHundredIndependentRecoveryRaces(t *testing.T) {
	for repetition := 0; repetition < 100; repetition++ {
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
		locker := &countingAssignmentLocker{}
		bound.locker = locker
		baselineWrites, baselineLocks := runner.mutationCount(), locker.count()
		humanChanged := make(chan struct{})
		go func() {
			runner.mu.Lock()
			issue := runner.issues[reference.ExternalID]
			issue.Assignee = "human-owner"
			issue.UpdatedAt = runner.tick()
			runner.issues[reference.ExternalID] = issue
			runner.mu.Unlock()
			close(humanChanged)
		}()
		<-humanChanged // the direct write wins after Spec Kitty's applied observation and before recovery's fresh observation.
		result, compensateErr := bound.CompensateAssignment(ctx, tracker.CompensateAssignmentRequest{
			ExternalRef: reference, OperationID: "operation-human-recovery", ExpectedObservation: applied.Observation.Observation.Revision,
			ExpectedAssignee: "actor-a", ExpectedMarker: "operation-human-race", RestoreAssignee: "original",
		})
		if !tracker.IsKind(compensateErr, tracker.ErrorAttributionLost) || result.Observation.Assignee != "human-owner" {
			t.Fatalf("repetition %d lost human evidence: result=%+v err=%v", repetition, result, compensateErr)
		}
		if runner.mutationCount() != baselineWrites || locker.count() != baselineLocks {
			t.Fatalf("repetition %d recovery crossed mutation boundary: writes=%d/%d locks=%d/%d", repetition, runner.mutationCount(), baselineWrites, locker.count(), baselineLocks)
		}
	}
}

func TestSafeCompensationRefusalSensorsKillReadCompareUpdateMutant(t *testing.T) {
	ctx := context.Background()
	bound, runner, reference := assignmentFixture(t, "original")
	before, err := bound.ObserveAssignment(ctx, reference)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := bound.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{ExternalRef: reference, OperationID: "operation-mutant", Assignee: "actor-a", ExpectedObservation: before.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded})
	if err != nil {
		t.Fatal(err)
	}
	locker := &countingAssignmentLocker{}
	bound.locker = locker
	writes, locks := runner.mutationCount(), locker.count()
	request := tracker.CompensateAssignmentRequest{ExternalRef: reference, OperationID: "operation-mutant-recovery", ExpectedObservation: applied.Observation.Observation.Revision, ExpectedAssignee: "actor-a", ExpectedMarker: "operation-mutant", RestoreAssignee: "original"}
	if _, err := bound.CompensateAssignment(ctx, request); !tracker.IsKind(err, tracker.ErrorSafeCompensationUnsupported) {
		t.Fatalf("safe refusal = %v", err)
	}
	if runner.mutationCount() != writes || locker.count() != locks {
		t.Fatalf("safe refusal crossed sensors: writes=%d/%d locks=%d/%d", runner.mutationCount(), writes, locker.count(), locks)
	}
	if err := unsafeReadCompareUpdateMutant(ctx, bound, request); err != nil {
		t.Fatal(err)
	}
	if runner.mutationCount() == writes || locker.count() == locks {
		t.Fatalf("read-compare-update mutant survived sensors: writes=%d/%d locks=%d/%d", runner.mutationCount(), writes, locker.count(), locks)
	}
}

func unsafeReadCompareUpdateMutant(ctx context.Context, bound *BoundAdapter, request tracker.CompensateAssignmentRequest) error {
	actual, err := bound.ObserveAssignment(ctx, request.ExternalRef)
	if err != nil {
		return err
	}
	if actual.Observation.Revision != request.ExpectedObservation || actual.Assignee != request.ExpectedAssignee || actual.OperationMarker != request.ExpectedMarker {
		return nil
	}
	readiness, err := bound.Preflight(ctx)
	if err != nil {
		return err
	}
	returnValue := request.RestoreAssignee
	_, err = bound.withAssignmentMutation(ctx, readiness, func() (tracker.AssignmentMutation, error) {
		command, buildErr := newUpdateCommand(bound.commandBinding(), updateValues{id: request.ExternalRef.ExternalID, assignee: presentUpdateValue(returnValue)})
		if buildErr != nil {
			return tracker.AssignmentMutation{}, buildErr
		}
		_, executeErr := bound.adapter.execute(ctx, command)
		return tracker.AssignmentMutation{}, executeErr
	})
	return err
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

func preserveAssignmentForeignMetadata(t *testing.T, runner *catalogRunner, externalID string) {
	t.Helper()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	issue := runner.issues[externalID]
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(issue.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	metadata["foreign"] = json.RawMessage(`{"number":9007199254740993,"preserved":true}`)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	issue.Metadata = encoded
	issue.UpdatedAt = runner.tick()
	runner.issues[externalID] = issue
}

type assignmentResponseShapeRunner struct {
	delegate commandRunner
	shape    string
}

func (runner *assignmentResponseShapeRunner) run(ctx context.Context, request command) commandResult {
	result := runner.delegate.run(ctx, request)
	if request.kind != CommandUpdate || result.exitCode == nil || *result.exitCode != 0 {
		return result
	}
	var response struct {
		SchemaVersion uint16            `json:"schema_version"`
		Data          []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(result.stdout, &response); err != nil || len(response.Data) != 1 {
		return result
	}
	var data any
	switch runner.shape {
	case "object":
		data = response.Data[0]
	case "empty":
		data = []json.RawMessage{}
	case "multiple":
		data = []json.RawMessage{response.Data[0], response.Data[0]}
	default:
		return result
	}
	result.stdout, _ = json.Marshal(map[string]any{"schema_version": response.SchemaVersion, "data": data})
	return result
}

type countingAssignmentLocker struct {
	mu    sync.Mutex
	calls int
}

func (locker *countingAssignmentLocker) count() int {
	locker.mu.Lock()
	defer locker.mu.Unlock()
	return locker.calls
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
