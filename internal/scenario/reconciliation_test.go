package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestReconciliationGenerationsRemainExplicitAndEnforceable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "control.db")
	store, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	oldRevision := scenarioRevision(t, "revision-1", 1, []ids.WorkID{"work-old"})
	if err := finalizeScenarioRevision(ctx, store, oldRevision, 0); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		disposition workgraph.Disposition
		newWorkIDs  []ids.WorkID
		forkEdges   []workgraph.ForkEdge
		assert      func(*testing.T, workgraph.Consequence)
	}{
		{workgraph.Continue, []ids.WorkID{"work-old"}, nil, func(t *testing.T, c workgraph.Consequence) {
			if !c.RunMayContinue || !c.HandoffEligible || !c.IntegrationEligible {
				t.Fatalf("continue = %+v", c)
			}
		}},
		{workgraph.Supersede, []ids.WorkID{"work-replacement"}, nil, func(t *testing.T, c workgraph.Consequence) {
			if !c.RunMayContinue || c.HandoffEligible || c.IntegrationEligible {
				t.Fatalf("supersede = %+v", c)
			}
		}},
		{workgraph.Invalidate, []ids.WorkID{"work-old"}, nil, func(t *testing.T, c workgraph.Consequence) {
			if !c.ExpireAuthority || !c.QuarantineWorkspace || c.RunMayContinue {
				t.Fatalf("invalidate = %+v", c)
			}
		}},
		{workgraph.Fork, []ids.WorkID{"work-left", "work-right"}, []workgraph.ForkEdge{{FromOldWorkID: "work-old", ToNewWorkID: "work-left"}, {FromOldWorkID: "work-old", ToNewWorkID: "work-right"}}, func(t *testing.T, c workgraph.Consequence) {
			if !c.RunMayContinue || !c.HandoffEligible || !c.IntegrationEligible {
				t.Fatalf("fork = %+v", c)
			}
		}},
	}
	previousNumber := int64(1)
	for index, test := range tests {
		newRevision := scenarioRevision(t, ids.WorkRevisionID("revision-"+string(rune('2'+index))), previousNumber+1, test.newWorkIDs)
		if err := finalizeScenarioRevision(ctx, store, newRevision, previousNumber); err != nil {
			t.Fatal(err)
		}
		reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
			ID: "reconciliation-" + string(test.disposition), Old: oldRevision, New: newRevision,
			Disposition: test.disposition, ActorID: "actor-owner", Rationale: "owner-selected " + string(test.disposition),
			OldWorkIDs: []ids.WorkID{"work-old"}, NewWorkIDs: test.newWorkIDs, ForkEdges: test.forkEdges, OccurredAt: time.Unix(int64(index+1), 0),
		})
		if err != nil {
			t.Fatalf("%s reconcile: %v", test.disposition, err)
		}
		if err := reconcileScenarioRevision(ctx, store, reconciliation); err != nil {
			t.Fatalf("%s save: %v", test.disposition, err)
		}
		consequence, err := store.ReconciliationConsequence(ctx, reconciliation.ID(), "work-old", workgraph.StatusActive)
		if err != nil || consequence.Reason == "" {
			t.Fatalf("%s query = %+v, %v", test.disposition, consequence, err)
		}
		test.assert(t, consequence)
		var stdout, stderr bytes.Buffer
		if code := helpercmd.RunWorkGraph(ctx, []string{"consequence", "--db", database, "--operation", "operation-inspect-" + string(test.disposition), "--correlation", "correlation-inspect-" + string(test.disposition), "--reconciliation", reconciliation.ID(), "--work", "work-old", "--status", "active"}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s helper exit=%d stderr=%s stdout=%s", test.disposition, code, stderr.String(), stdout.String())
		}
		var response helpercmd.WorkGraphResponse
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || !response.OK || response.Result == nil || response.Result.Consequence == nil || response.Result.Consequence.Reason != consequence.Reason {
			t.Fatalf("%s helper response=%+v decode=%v", test.disposition, response, err)
		}
		previousNumber++
	}
}

func TestReconciliationRejectsPostCollapseCycle(t *testing.T) {
	t.Parallel()
	draft := workgraph.NewDraft("project-1", "mission-1")
	for _, lane := range []workgraph.Lane{{ID: "lane-a"}, {ID: "lane-b"}} {
		if err := draft.AddLane(lane); err != nil {
			t.Fatal(err)
		}
	}
	items := []workgraph.WorkItem{
		{ID: "a1", LaneID: "lane-a", Criteria: []workgraph.Criterion{{ID: "a1", Text: "done"}}},
		{ID: "b1", LaneID: "lane-b", Criteria: []workgraph.Criterion{{ID: "b1", Text: "done"}}, Dependencies: []ids.WorkID{"a1"}},
		{ID: "b2", LaneID: "lane-b", Criteria: []workgraph.Criterion{{ID: "b2", Text: "done"}}},
		{ID: "a2", LaneID: "lane-a", Criteria: []workgraph.Criterion{{ID: "a2", Text: "done"}}, Dependencies: []ids.WorkID{"b2"}},
	}
	for _, item := range items {
		if err := draft.AddWork(item); err != nil {
			t.Fatal(err)
		}
	}
	_, err := draft.Finalize(workgraph.FinalizeMetadata{ID: "revision-cycle", Number: 1, ActorID: "actor-owner", FinalizedAt: time.Unix(1, 0)})
	if !workgraph.IsKind(err, workgraph.KindLaneCycle) {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestWorkGraphHelperBinaryFinalizesReconcilesAndReturnsTypedEnvelopes(t *testing.T) {
	t.Parallel()
	database := filepath.Join(t.TempDir(), "control.db")
	oldRevision := scenarioRevision(t, "revision-helper-1", 1, []ids.WorkID{"work-helper"})
	newRevision := scenarioRevision(t, "revision-helper-2", 2, []ids.WorkID{"work-helper"})
	oldJSON, err := oldRevision.Encode()
	if err != nil {
		t.Fatal(err)
	}
	newJSON, err := newRevision.Encode()
	if err != nil {
		t.Fatal(err)
	}

	oldFinalizeArgs := []string{"finalize", "--db", database, "--operation", "operation-finalize-1", "--correlation", "correlation-finalize-1", "--schema-version", "1", "--actor", "actor-owner", "--process", "process-finalize-1", "--at", oldRevision.FinalizedAt().Format(time.RFC3339Nano), "--expected-previous", "0", "--revision-json", string(oldJSON)}
	oldResult := runWorkGraphHelper(t, oldFinalizeArgs...)
	assertTypedResult(t, oldResult, true, "work_revision_finalized")
	assertWorkGraphCommandIdentity(t, oldResult, "actor-owner", "process-finalize-1")
	oldReplay := runWorkGraphHelper(t, oldFinalizeArgs...)
	if !reflect.DeepEqual(oldReplay, oldResult) {
		t.Fatalf("finalize replay=%+v, original=%+v", oldReplay, oldResult)
	}
	changedFinalizeProcessArgs := append([]string(nil), oldFinalizeArgs...)
	for index := range changedFinalizeProcessArgs {
		if changedFinalizeProcessArgs[index] == "process-finalize-1" {
			changedFinalizeProcessArgs[index] = "process-finalize-changed"
		}
	}
	changedFinalizeProcess := runWorkGraphHelper(t, changedFinalizeProcessArgs...)
	assertTypedResult(t, changedFinalizeProcess, false, "work_revision_conflict")
	assertWorkGraphCommandIdentity(t, changedFinalizeProcess, "actor-owner", "process-finalize-changed")
	newResult := runWorkGraphHelper(t, "finalize", "--db", database, "--operation", "operation-finalize-2", "--correlation", "correlation-finalize-2", "--schema-version", "1", "--actor", "actor-owner", "--process", "process-finalize-2", "--at", newRevision.FinalizedAt().Format(time.RFC3339Nano), "--expected-previous", "1", "--revision-json", string(newJSON))
	assertTypedResult(t, newResult, true, "work_revision_finalized")

	reconcileArgs := []string{"reconcile", "--db", database, "--operation", "operation-reconcile-1", "--correlation", "correlation-reconcile-1", "--schema-version", "1", "--old", "revision-helper-1", "--new", "revision-helper-2", "--disposition", "continue", "--actor", "actor-owner", "--process", "process-reconcile-1", "--rationale", "equivalent", "--old-work", "work-helper", "--new-work", "work-helper", "--at", time.Unix(2, 0).UTC().Format(time.RFC3339Nano)}
	reconciled := runWorkGraphHelper(t, reconcileArgs...)
	assertTypedResult(t, reconciled, true, "work_revision_reconciled")
	assertWorkGraphCommandIdentity(t, reconciled, "actor-owner", "process-reconcile-1")
	replayed := runWorkGraphHelper(t, reconcileArgs...)
	assertTypedResult(t, replayed, true, "work_revision_reconciled")
	consequence := runWorkGraphHelper(t, "consequence", "--db", database, "--operation", "operation-consequence-1", "--correlation", "correlation-consequence-1", "--reconciliation", "operation-reconcile-1", "--work", "work-helper", "--status", "active")
	assertTypedResult(t, consequence, true, "reconciliation_consequence")

	invalid := runWorkGraphHelper(t, "consequence", "--db", database, "--operation", "operation-invalid", "--correlation", "correlation-invalid", "--reconciliation", "operation-reconcile-1", "--work", "../unsafe", "--status", "active")
	assertTypedResult(t, invalid, false, "invalid_command")
	notFound := runWorkGraphHelper(t, "consequence", "--db", database, "--operation", "operation-not-found", "--correlation", "correlation-not-found", "--reconciliation", "missing-reconciliation", "--work", "work-helper", "--status", "active")
	assertTypedResult(t, notFound, false, "not_found")

	conflictingRevision := scenarioRevision(t, "revision-helper-conflict", 1, []ids.WorkID{"work-other"})
	conflictJSON, err := conflictingRevision.Encode()
	if err != nil {
		t.Fatal(err)
	}
	revisionConflict := runWorkGraphHelper(t, "finalize", "--db", database, "--operation", "operation-finalize-conflict", "--correlation", "correlation-finalize-conflict", "--schema-version", "1", "--actor", "actor-owner", "--process", "process-finalize-conflict", "--at", conflictingRevision.FinalizedAt().Format(time.RFC3339Nano), "--expected-previous", "0", "--revision-json", string(conflictJSON))
	assertTypedResult(t, revisionConflict, false, "work_revision_conflict")
	invalidSchema := runWorkGraphHelper(t, "finalize", "--db", database, "--operation", "operation-invalid-schema", "--correlation", "correlation-invalid-schema", "--schema-version", "2", "--actor", "actor-owner", "--process", "process-invalid-schema", "--at", conflictingRevision.FinalizedAt().Format(time.RFC3339Nano), "--expected-previous", "0", "--revision-json", string(conflictJSON))
	assertTypedResult(t, invalidSchema, false, "invalid_command")
	assertWorkGraphCommandIdentity(t, invalidSchema, "actor-owner", "process-invalid-schema")
	invalidProcess := runWorkGraphHelper(t, "reconcile", "--db", database, "--operation", "operation-invalid-process", "--correlation", "correlation-invalid-process", "--schema-version", "1", "--actor", "actor-owner", "--process", "../unsafe", "--at", time.Unix(2, 0).UTC().Format(time.RFC3339Nano))
	assertTypedResult(t, invalidProcess, false, "invalid_command")
	mismatchedActor := runWorkGraphHelper(t, "finalize", "--db", database, "--operation", "operation-mismatched-actor", "--correlation", "correlation-mismatched-actor", "--schema-version", "1", "--actor", "actor-other", "--process", "process-mismatched-actor", "--at", conflictingRevision.FinalizedAt().Format(time.RFC3339Nano), "--expected-previous", "0", "--revision-json", string(conflictJSON))
	assertTypedResult(t, mismatchedActor, false, "invalid_command")

	changedArgs := append([]string(nil), reconcileArgs...)
	for index := range changedArgs {
		if changedArgs[index] == "equivalent" {
			changedArgs[index] = "changed payload"
		}
	}
	reconciliationConflict := runWorkGraphHelper(t, changedArgs...)
	assertTypedResult(t, reconciliationConflict, false, "reconciliation_conflict")
	changedProcessArgs := append([]string(nil), reconcileArgs...)
	for index := range changedProcessArgs {
		if changedProcessArgs[index] == "process-reconcile-1" {
			changedProcessArgs[index] = "process-reconcile-changed"
		}
	}
	changedProcessConflict := runWorkGraphHelper(t, changedProcessArgs...)
	assertTypedResult(t, changedProcessConflict, false, "reconciliation_conflict")
}

func TestWorkGraphHelperConcurrentFinalizeReplayReturnsOneRecordedResult(t *testing.T) {
	t.Parallel()
	database := filepath.Join(t.TempDir(), "control.db")
	revision := scenarioRevision(t, "revision-helper-concurrent", 1, []ids.WorkID{"work-helper-concurrent"})
	revisionJSON, err := revision.Encode()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"finalize", "--db", database, "--operation", "operation-helper-concurrent", "--correlation", "correlation-helper-concurrent", "--schema-version", "1", "--actor", "actor-owner", "--process", "process-helper-concurrent", "--at", revision.FinalizedAt().Format(time.RFC3339Nano), "--expected-previous", "0", "--revision-json", string(revisionJSON)}
	type invocation struct {
		response map[string]any
		err      error
	}
	invocations := make(chan invocation, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			response, err := invokeWorkGraphHelper(args...)
			invocations <- invocation{response: response, err: err}
		}()
	}
	close(start)
	first, second := <-invocations, <-invocations
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent helper errors: first=%v second=%v", first.err, second.err)
	}
	assertTypedResult(t, first.response, true, "work_revision_finalized")
	assertTypedResult(t, second.response, true, "work_revision_finalized")
	if !reflect.DeepEqual(first.response, second.response) {
		t.Fatalf("concurrent results differ: first=%+v second=%+v", first.response, second.response)
	}
}

func runWorkGraphHelper(t *testing.T, args ...string) map[string]any {
	t.Helper()
	response, err := invokeWorkGraphHelper(args...)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func invokeWorkGraphHelper(args ...string) (map[string]any, error) {
	command := exec.Command(helperBinary, append([]string{"workgraph"}, args...)...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("workgraph helper %v: %w stderr=%s stdout=%s", args, err, stderr.String(), stdout.String())
	}
	var response map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("decode workgraph helper %v response %q: %w", args, stdout.String(), err)
	}
	return response, nil
}

func assertTypedResult(t *testing.T, response map[string]any, success bool, kind string) {
	t.Helper()
	if response["ok"] != success {
		t.Fatalf("response ok=%v, want %v: %+v", response["ok"], success, response)
	}
	key := "result"
	if !success {
		key = "error"
	}
	envelope, ok := response[key].(map[string]any)
	if !ok {
		t.Fatalf("response has no %s envelope: %+v", key, response)
	}
	required := []string{"schema_version", "kind", "correlation_id", "operation_id", "aggregate_ids", "observed_state", "expected_state", "retry_safety", "recovery_actions"}
	for _, field := range required {
		if _, exists := envelope[field]; !exists {
			t.Errorf("%s envelope missing %s: %+v", key, field, envelope)
		}
	}
	if got := fmt.Sprint(envelope["kind"]); got != kind {
		t.Errorf("kind=%q, want %q envelope=%+v", got, kind, envelope)
	}
}

func assertWorkGraphCommandIdentity(t *testing.T, response map[string]any, actor, process string) {
	t.Helper()
	key := "result"
	if response["ok"] != true {
		key = "error"
	}
	envelope, ok := response[key].(map[string]any)
	if !ok {
		t.Fatalf("response has no %s envelope: %+v", key, response)
	}
	aggregates, ok := envelope["aggregate_ids"].(map[string]any)
	if !ok || fmt.Sprint(aggregates["actor_id"]) != actor || fmt.Sprint(aggregates["process_id"]) != process {
		t.Fatalf("%s command identity actor=%q process=%q envelope=%+v", key, actor, process, envelope)
	}
	if fmt.Sprint(envelope["schema_version"]) != "1" {
		t.Fatalf("%s schema version envelope=%+v", key, envelope)
	}
}

func scenarioRevision(t *testing.T, revisionID ids.WorkRevisionID, number int64, workIDs []ids.WorkID) workgraph.Revision {
	t.Helper()
	draft := workgraph.NewDraft("project-1", "mission-1")
	if err := draft.AddLane(workgraph.Lane{ID: "lane-a"}); err != nil {
		t.Fatal(err)
	}
	for _, workID := range workIDs {
		if err := draft.AddWork(workgraph.WorkItem{ID: workID, LaneID: "lane-a", Criteria: []workgraph.Criterion{{ID: "criterion-" + workID.String(), Text: "done"}}}); err != nil {
			t.Fatal(err)
		}
	}
	revision, err := draft.Finalize(workgraph.FinalizeMetadata{ID: revisionID, Number: number, ActorID: "actor-owner", FinalizedAt: time.Unix(number, 0)})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func finalizeScenarioRevision(ctx context.Context, store *sqlite.Store, revision workgraph.Revision, expectedPreviousNumber int64) error {
	_, err := store.FinalizeWorkRevision(ctx, sqlite.FinalizeWorkRevisionRequest{
		SchemaVersion: 1, CorrelationID: "correlation-finalize-" + revision.ID().String(), OperationID: ids.OperationID("operation-finalize-" + revision.ID().String()),
		ActorID: revision.ActorID(), ProcessID: ids.ProcessID("process-finalize-" + revision.ID().String()), OccurredAt: revision.FinalizedAt(),
		Revision: revision, ExpectedPreviousNumber: expectedPreviousNumber,
	})
	return err
}

func reconcileScenarioRevision(ctx context.Context, store *sqlite.Store, reconciliation workgraph.Reconciliation) error {
	return store.ReconcileWorkRevision(ctx, sqlite.ReconcileWorkRevisionRequest{
		SchemaVersion: 1, CorrelationID: "correlation-" + reconciliation.ID(), OperationID: ids.OperationID(reconciliation.ID()),
		ActorID: reconciliation.ActorID(), ProcessID: ids.ProcessID("process-" + reconciliation.ID()), OccurredAt: reconciliation.OccurredAt(), Reconciliation: reconciliation,
	})
}
