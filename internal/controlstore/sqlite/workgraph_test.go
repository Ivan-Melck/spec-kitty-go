package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestFinalizeWorkRevisionOperationIsDurablyIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	revision := storeRevision(t, "revision-idempotent", 1, "work-a")
	request := FinalizeWorkRevisionRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-finalize", OperationID: "operation-finalize",
		ActorID: revision.ActorID(), ProcessID: "process-finalize", OccurredAt: revision.FinalizedAt(),
		Revision: revision, ExpectedPreviousNumber: 0,
	}
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.FinalizeWorkRevision(ctx, request)
	if err != nil {
		t.Fatalf("first finalize: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replay, err := reopened.FinalizeWorkRevision(ctx, request)
	if err != nil {
		t.Fatalf("reopen replay: %v", err)
	}
	if !reflect.DeepEqual(replay, first) {
		t.Fatalf("replay=%+v, first=%+v", replay, first)
	}
	var schemaVersion uint16
	var correlationID, actorID, processID, occurredAt string
	if err := reopened.db.QueryRowContext(ctx, `SELECT schema_version,correlation_id,actor_id,process_id,occurred_at FROM work_graph_operations WHERE operation_id=?`, request.OperationID).Scan(&schemaVersion, &correlationID, &actorID, &processID, &occurredAt); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != request.SchemaVersion || correlationID != request.CorrelationID || actorID != request.ActorID.String() || processID != request.ProcessID.String() || occurredAt != request.OccurredAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("stored command schema=%d correlation=%q actor=%q process=%q at=%q", schemaVersion, correlationID, actorID, processID, occurredAt)
	}
	if _, err := reopened.db.ExecContext(ctx, `UPDATE work_graph_operations SET request_digest='tampered' WHERE operation_id=?`, request.OperationID); err == nil {
		t.Fatal("finalization operation accepted result tampering")
	}
	if _, err := reopened.db.ExecContext(ctx, `DELETE FROM work_graph_operations WHERE operation_id=?`, request.OperationID); err == nil {
		t.Fatal("finalization operation accepted deletion")
	}
	changed := request
	changed.CorrelationID = "correlation-changed"
	if _, err := reopened.FinalizeWorkRevision(ctx, changed); !workgraph.IsKind(err, workgraph.KindRevisionConflict) {
		t.Fatalf("changed operation reuse error = %v", err)
	}
	changedProcess := request
	changedProcess.ProcessID = "process-finalize-changed"
	if _, err := reopened.FinalizeWorkRevision(ctx, changedProcess); !workgraph.IsKind(err, workgraph.KindRevisionConflict) {
		t.Fatalf("changed ProcessID operation reuse error = %v", err)
	}
}

func TestFinalizeWorkRevisionConcurrentIdenticalOperationHasOneRecordedResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	revision := storeRevision(t, "revision-concurrent", 1, "work-a")
	request := FinalizeWorkRevisionRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-concurrent", OperationID: "operation-concurrent",
		ActorID: revision.ActorID(), ProcessID: "process-concurrent", OccurredAt: revision.FinalizedAt(), Revision: revision,
	}
	stores := []*Store{first, second}
	results := make([]FinalizeWorkRevisionResult, 2)
	errorsByWriter := make([]error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range stores {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errorsByWriter[index] = stores[index].FinalizeWorkRevision(ctx, request)
		}(index)
	}
	close(start)
	wait.Wait()
	if errorsByWriter[0] != nil || errorsByWriter[1] != nil || !reflect.DeepEqual(results[0], results[1]) {
		t.Fatalf("results=%+v errors=%v", results, errorsByWriter)
	}
	var operations, revisions int
	if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_graph_operations WHERE operation_id=?`, request.OperationID).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_graph_revisions WHERE work_revision_id=?`, revision.ID()).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || revisions != 1 {
		t.Fatalf("operations=%d revisions=%d", operations, revisions)
	}
}

func TestWorkRevisionPersistenceIsNumberedImmutableAndRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openWorkGraphStore(t)
	revision := storeRevision(t, "revision-1", 1, "work-a")

	if err := store.finalizeWorkRevisionForTest(ctx, revision, 0); err != nil {
		t.Fatalf("save revision: %v", err)
	}
	loaded, err := store.WorkRevision(ctx, "revision-1")
	if err != nil {
		t.Fatalf("load revision: %v", err)
	}
	if loaded.Number() != 1 || loaded.GraphDigest() != revision.GraphDigest() || loaded.CriteriaDigest() != revision.CriteriaDigest() || len(loaded.WorkItems()) != 1 {
		t.Fatalf("loaded revision = number %d graph %q criteria %q items %+v", loaded.Number(), loaded.GraphDigest(), loaded.CriteriaDigest(), loaded.WorkItems())
	}
	if err := store.finalizeWorkRevisionForTest(ctx, revision, 0); err != nil {
		t.Fatalf("identical finalize replay error = %v", err)
	}
	if _, err := store.FinalizeWorkRevision(ctx, FinalizeWorkRevisionRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-finalize-duplicate", OperationID: "operation-finalize-duplicate",
		ActorID: revision.ActorID(), ProcessID: "process-finalize-duplicate", OccurredAt: revision.FinalizedAt(), Revision: revision, ExpectedPreviousNumber: 0,
	}); !workgraph.IsKind(err, workgraph.KindRevisionConflict) {
		t.Fatalf("new-operation duplicate revision error = %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `UPDATE work_graph_revisions SET graph_digest='rewritten' WHERE work_revision_id='revision-1'`); err == nil {
		t.Fatal("immutable graph row accepted UPDATE")
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM work_graph_revisions WHERE work_revision_id='revision-1'`); err == nil {
		t.Fatal("immutable graph row accepted DELETE")
	}
}

func TestCanonicalMutationCommandsAlwaysRecordDurableIdentity(t *testing.T) {
	t.Parallel()
	storeType := reflect.TypeOf(&Store{})
	for _, bypass := range []string{"SaveWorkRevision", "SaveReconciliation"} {
		if _, exists := storeType.MethodByName(bypass); exists {
			t.Fatalf("exported canonical mutation bypass %s remains", bypass)
		}
	}
	ctx := context.Background()
	store := openWorkGraphStore(t)
	oldRevision := storeRevision(t, "revision-command-old", 1, "work-command")
	newRevision := storeRevision(t, "revision-command-new", 2, "work-command")
	if err := store.finalizeWorkRevisionForTest(ctx, oldRevision, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.finalizeWorkRevisionForTest(ctx, newRevision, 1); err != nil {
		t.Fatal(err)
	}
	reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: "reconciliation-command", Old: oldRevision, New: newRevision, Disposition: workgraph.Continue,
		ActorID: "actor-owner", Rationale: "command-aware mutation", OldWorkIDs: []ids.WorkID{"work-command"}, NewWorkIDs: []ids.WorkID{"work-command"}, OccurredAt: time.Unix(2, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.reconcileWorkRevisionForTest(ctx, reconciliation); err != nil {
		t.Fatal(err)
	}
	var operations, missingRevisionOperations, missingReconciliationOperations int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_graph_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_graph_revisions g WHERE NOT EXISTS(SELECT 1 FROM work_graph_operations o WHERE o.result_work_revision_id=g.work_revision_id)`).Scan(&missingRevisionOperations); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reconciliations r WHERE NOT EXISTS(SELECT 1 FROM work_graph_operations o WHERE o.result_reconciliation_id=r.reconciliation_id)`).Scan(&missingReconciliationOperations); err != nil {
		t.Fatal(err)
	}
	if operations != 3 || missingRevisionOperations != 0 || missingReconciliationOperations != 0 {
		t.Fatalf("operations=%d missing revisions=%d reconciliations=%d", operations, missingRevisionOperations, missingReconciliationOperations)
	}
}

func TestWorkRevisionOptimisticNumberConflictHasOneWinner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	seed, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.finalizeWorkRevisionForTest(ctx, storeRevision(t, "revision-1", 1, "work-a"), 0); err != nil {
		t.Fatal(err)
	}
	_ = seed.Close()
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	revisions := []workgraph.Revision{storeRevision(t, "revision-2a", 2, "work-a"), storeRevision(t, "revision-2b", 2, "work-a")}
	stores := []*Store{first, second}
	errorsByWriter := make([]error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range 2 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			errorsByWriter[index] = stores[index].finalizeWorkRevisionForTest(ctx, revisions[index], 1)
		}(index)
	}
	close(start)
	wait.Wait()
	winners, conflicts := 0, 0
	for _, err := range errorsByWriter {
		switch {
		case err == nil:
			winners++
		case workgraph.IsKind(err, workgraph.KindRevisionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected writer error: %v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d errors=%v", winners, conflicts, errorsByWriter)
	}
}

func TestReconciliationPersistenceKeepsEvidenceAndQueryableConsequences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openWorkGraphStore(t)
	oldRevision := storeRevision(t, "revision-1", 1, "work-a")
	newRevision := storeRevision(t, "revision-2", 2, "work-a")
	if err := store.finalizeWorkRevisionForTest(ctx, oldRevision, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.finalizeWorkRevisionForTest(ctx, newRevision, 1); err != nil {
		t.Fatal(err)
	}
	reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: "reconciliation-1", Old: oldRevision, New: newRevision, Disposition: workgraph.Invalidate,
		ActorID: "actor-owner", Rationale: "criteria materially changed", OldWorkIDs: []ids.WorkID{"work-a"},
		NewWorkIDs: []ids.WorkID{"work-a"}, OccurredAt: time.Date(2026, 8, 23, 13, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.reconcileWorkRevisionForTest(ctx, reconciliation); err != nil {
		t.Fatalf("save reconciliation: %v", err)
	}
	evidence, err := store.Reconciliation(ctx, "reconciliation-1")
	if err != nil {
		t.Fatalf("load reconciliation: %v", err)
	}
	if evidence.OldGraphDigest != oldRevision.GraphDigest() || evidence.NewGraphDigest != newRevision.GraphDigest() || evidence.ActorID != "actor-owner" || evidence.Rationale == "" || evidence.Disposition != workgraph.Invalidate {
		t.Fatalf("evidence = %+v", evidence)
	}
	consequence, err := store.ReconciliationConsequence(ctx, "reconciliation-1", "work-a", workgraph.StatusActive)
	if err != nil {
		t.Fatalf("query consequence: %v", err)
	}
	if !consequence.ExpireAuthority || !consequence.QuarantineWorkspace || consequence.Reason == "" || consequence.HandoffEligible || consequence.IntegrationEligible {
		t.Fatalf("consequence = %+v", consequence)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE reconciliations SET rationale='rewritten' WHERE reconciliation_id='reconciliation-1'`); err == nil {
		t.Fatal("immutable reconciliation accepted UPDATE")
	}
	if err := store.reconcileWorkRevisionForTest(ctx, reconciliation); err != nil {
		t.Fatalf("identical reconciliation replay error = %v", err)
	}
}

func TestClaimEnforcesReconciliationDisposition(t *testing.T) {
	t.Parallel()
	for _, disposition := range []workgraph.Disposition{workgraph.Continue, workgraph.Supersede, workgraph.Invalidate, workgraph.Fork} {
		disposition := disposition
		t.Run(string(disposition), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := openWorkGraphStore(t)
			oldRevision := storeRevision(t, "revision-1", 1, "work-old")
			var newRevision workgraph.Revision
			request := workgraph.ReconcileRequest{
				ID: "reconciliation-" + string(disposition), Old: oldRevision, Disposition: disposition,
				ActorID: "actor-owner", Rationale: "explicit " + string(disposition), OldWorkIDs: []ids.WorkID{"work-old"}, OccurredAt: time.Unix(2, 0),
			}
			if disposition == workgraph.Fork {
				newRevision = storeRevisionMany(t, "revision-2", 2, []ids.WorkID{"work-left", "work-right"})
				request.NewWorkIDs = []ids.WorkID{"work-left", "work-right"}
				request.ForkEdges = []workgraph.ForkEdge{{FromOldWorkID: "work-old", ToNewWorkID: "work-left"}, {FromOldWorkID: "work-old", ToNewWorkID: "work-right"}}
			} else if disposition == workgraph.Supersede {
				newRevision = storeRevision(t, "revision-2", 2, "work-replacement")
				request.NewWorkIDs = []ids.WorkID{"work-replacement"}
			} else {
				newRevision = storeRevision(t, "revision-2", 2, "work-old")
				request.NewWorkIDs = []ids.WorkID{"work-old"}
			}
			request.New = newRevision
			if err := store.finalizeWorkRevisionForTest(ctx, oldRevision, 0); err != nil {
				t.Fatal(err)
			}
			if err := store.finalizeWorkRevisionForTest(ctx, newRevision, 1); err != nil {
				t.Fatal(err)
			}
			reconciliation, err := workgraph.Reconcile(request)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.reconcileWorkRevisionForTest(ctx, reconciliation); err != nil {
				t.Fatal(err)
			}
			for _, status := range []workgraph.WorkStatus{workgraph.StatusActive, workgraph.StatusHandedOff, workgraph.StatusApproved, workgraph.StatusIntegrated} {
				consequence, reconciliationID, err := store.WorkEligibility(ctx, "revision-1", "work-old", status)
				if err != nil || reconciliationID != reconciliation.ID() || consequence.Reason == "" {
					t.Fatalf("%s/%s eligibility=%+v reconciliation=%q error=%v", disposition, status, consequence, reconciliationID, err)
				}
				if status == workgraph.StatusIntegrated && !consequence.PreservesIntegratedHistory {
					t.Fatalf("%s rewrote integrated history: %+v", disposition, consequence)
				}
				if status != workgraph.StatusIntegrated && (disposition == workgraph.Supersede || disposition == workgraph.Invalidate) && (consequence.HandoffEligible || consequence.IntegrationEligible) {
					t.Fatalf("%s/%s publication remains eligible: %+v", disposition, status, consequence)
				}
			}

			_, err = startWork(ctx, store, "operation-after-"+string(disposition), "work-old", "revision-1")
			if disposition == workgraph.Supersede || disposition == workgraph.Invalidate {
				if !execution.IsKind(err, execution.KindAuthorityConflict) {
					t.Fatalf("%s old generation Start error = %v", disposition, err)
				}
			} else if err != nil {
				t.Fatalf("%s old generation should remain start-eligible: %v", disposition, err)
			}
		})
	}
}

func TestInvalidateExpiresExistingAuthorityAndPublishesQuarantineIntent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openWorkGraphStore(t)
	oldRevision := storeRevision(t, "revision-1", 1, "work-old")
	newRevision := storeRevision(t, "revision-2", 2, "work-old")
	if err := store.finalizeWorkRevisionForTest(ctx, oldRevision, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.finalizeWorkRevisionForTest(ctx, newRevision, 1); err != nil {
		t.Fatal(err)
	}
	service := execution.NewService(store, execution.RandomIdentities{}, execution.SystemClock{})
	startRequest := execution.StartRequest{
		OperationID: "operation-before-invalidate", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-old",
		ActorID: "actor-worker", ProcessID: "process-before-invalidate", Now: time.Unix(1, 0), LeaseTTL: time.Minute,
	}
	capsule, err := service.Start(ctx, startRequest)
	if err != nil {
		t.Fatal(err)
	}
	reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: "reconciliation-invalidate-active", Old: oldRevision, New: newRevision, Disposition: workgraph.Invalidate,
		ActorID: "actor-owner", Rationale: "material change", OldWorkIDs: []ids.WorkID{"work-old"}, NewWorkIDs: []ids.WorkID{"work-old"}, OccurredAt: time.Unix(3, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.reconcileWorkRevisionForTest(ctx, reconciliation); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.AuthoritySnapshot(ctx, "work-old", "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveLeases != 0 {
		t.Fatalf("invalidate left active authority: %+v", snapshot)
	}
	var intents int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_quarantine_intents WHERE lease_id=? AND reconciliation_id=?`, capsule.LeaseID, reconciliation.ID()).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if intents != 1 {
		t.Fatalf("quarantine intents = %d, want 1", intents)
	}
	if _, err := service.Start(ctx, startRequest); !execution.IsKind(err, execution.KindStaleAuthority) && !execution.IsKind(err, execution.KindAuthorityConflict) {
		t.Fatalf("invalidated successful-operation replay error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE leases SET status='active' WHERE lease_id=?`, capsule.LeaseID); err == nil {
		t.Fatal("direct SQL reactivated an invalidated lease")
	}
}

func TestSupersedePreservesExistingAuthorityButBlocksNewStarts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openWorkGraphStore(t)
	oldRevision := storeRevision(t, "revision-1", 1, "work-old")
	newRevision := storeRevision(t, "revision-2", 2, "work-replacement")
	if err := store.finalizeWorkRevisionForTest(ctx, oldRevision, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.finalizeWorkRevisionForTest(ctx, newRevision, 1); err != nil {
		t.Fatal(err)
	}
	service := execution.NewService(store, execution.RandomIdentities{}, execution.SystemClock{})
	request := execution.StartRequest{
		OperationID: "operation-before-supersede", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-old",
		ActorID: "actor-worker", ProcessID: "process-before-supersede", Now: time.Unix(1, 0), LeaseTTL: time.Minute,
	}
	original, err := service.Start(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: "reconciliation-supersede-active", Old: oldRevision, New: newRevision, Disposition: workgraph.Supersede,
		ActorID: "actor-owner", Rationale: "replacement", OldWorkIDs: []ids.WorkID{"work-old"}, NewWorkIDs: []ids.WorkID{"work-replacement"}, OccurredAt: time.Unix(2, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.reconcileWorkRevisionForTest(ctx, reconciliation); err != nil {
		t.Fatal(err)
	}
	replay, err := service.Start(ctx, request)
	if err != nil || replay.LeaseID != original.LeaseID || !replay.Replayed {
		t.Fatalf("existing superseded run replay=%+v error=%v", replay, err)
	}
	if _, err := startWork(ctx, store, "operation-new-after-supersede", "work-old", "revision-1"); !execution.IsKind(err, execution.KindAuthorityConflict) {
		t.Fatalf("new Start after supersede error=%v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE leases SET status='expired' WHERE lease_id=?`, original.LeaseID); err != nil {
		t.Fatalf("expire superseded authority: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE leases SET status='active' WHERE lease_id=?`, original.LeaseID); err == nil {
		t.Fatal("direct SQL reactivated an expired superseded lease")
	}
	if _, err := service.Start(ctx, request); !execution.IsKind(err, execution.KindStaleAuthority) {
		t.Fatalf("expired superseded Start replay error=%v", err)
	}
}

func TestCanonicalWorkStateCannotMoveFromTerminalToRunnable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openWorkGraphStore(t)
	revision := storeRevision(t, "revision-state", 1, "work-state")
	if err := store.finalizeWorkRevisionForTest(ctx, revision, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE work_items SET state='blocked' WHERE work_revision_id='revision-state' AND work_id='work-state'`); err != nil {
		t.Fatalf("ready to blocked: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE work_items SET state='ready' WHERE work_revision_id='revision-state' AND work_id='work-state'`); err != nil {
		t.Fatalf("blocked to ready: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE work_items SET state='done' WHERE work_revision_id='revision-state' AND work_id='work-state'`); err != nil {
		t.Fatalf("ready to done: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE work_items SET state='ready' WHERE work_revision_id='revision-state' AND work_id='work-state'`); err == nil {
		t.Fatal("terminal canonical work moved from done to ready")
	}
	if _, err := startWork(ctx, store, "operation-after-done", "work-state", "revision-state"); !execution.IsKind(err, execution.KindAuthorityConflict) {
		t.Fatalf("Start after terminal work error=%v", err)
	}
}

func TestSeedAndClaimFailClosedForBareNonFinalizedRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openWorkGraphStore(t)
	now := time.Unix(1, 0).UTC().Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO projects(project_id,created_at) VALUES('project-bare',?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO work_revisions(work_revision_id,project_id,mission_id,revision_number,finalized_at) VALUES('revision-bare','project-bare','mission-bare',1,?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO work_items(work_id,work_revision_id,state) VALUES('work-invented','revision-bare','ready')`); err != nil {
		t.Fatal(err)
	}
	seed := Seed{ProjectID: "project-bare", MissionID: "mission-bare", WorkRevisionID: "revision-bare", WorkID: "work-invented"}
	if err := store.SeedAuthority(ctx, seed); !execution.IsKind(err, execution.KindInvalidCommand) {
		t.Fatalf("bare SeedAuthority error=%v", err)
	}
	service := execution.NewService(store, execution.RandomIdentities{}, execution.SystemClock{})
	_, err := service.Start(ctx, execution.StartRequest{
		OperationID: "operation-bare", ProjectID: seed.ProjectID, MissionID: seed.MissionID,
		WorkRevisionID: seed.WorkRevisionID, WorkID: seed.WorkID, ActorID: "actor-bare", ProcessID: "process-bare", LeaseTTL: time.Minute,
	})
	if !execution.IsKind(err, execution.KindNotFound) {
		t.Fatalf("bare Claim error=%v", err)
	}
	missing := Seed{ProjectID: "project-missing", MissionID: "mission-missing", WorkRevisionID: "revision-missing", WorkID: "work-missing"}
	if err := store.SeedAuthority(ctx, missing); !execution.IsKind(err, execution.KindInvalidCommand) {
		t.Fatalf("missing SeedAuthority error=%v", err)
	}
}

func TestTerminalReconciliationCannotBeRevivedByLaterContinue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openWorkGraphStore(t)
	oldRevision := storeRevision(t, "revision-1", 1, "work-old")
	invalidatedRevision := storeRevision(t, "revision-2", 2, "work-old")
	laterRevision := storeRevision(t, "revision-3", 3, "work-old")
	for index, revision := range []workgraph.Revision{oldRevision, invalidatedRevision, laterRevision} {
		if err := store.finalizeWorkRevisionForTest(ctx, revision, int64(index)); err != nil {
			t.Fatal(err)
		}
	}
	invalidated, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: "reconciliation-terminal", Old: oldRevision, New: invalidatedRevision, Disposition: workgraph.Invalidate,
		ActorID: "actor-owner", Rationale: "material change", OldWorkIDs: []ids.WorkID{"work-old"}, NewWorkIDs: []ids.WorkID{"work-old"}, OccurredAt: time.Unix(2, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	continued, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: "reconciliation-later-continue", Old: oldRevision, New: laterRevision, Disposition: workgraph.Continue,
		ActorID: "actor-owner", Rationale: "attempted revival", OldWorkIDs: []ids.WorkID{"work-old"}, NewWorkIDs: []ids.WorkID{"work-old"}, OccurredAt: time.Unix(3, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.reconcileWorkRevisionForTest(ctx, invalidated); err != nil {
		t.Fatal(err)
	}
	if err := store.reconcileWorkRevisionForTest(ctx, continued); err != nil {
		t.Fatal(err)
	}
	eligibility, reconciliationID, err := store.WorkEligibility(ctx, "revision-1", "work-old", workgraph.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if reconciliationID != invalidated.ID() || eligibility.HandoffEligible || !eligibility.ExpireAuthority {
		t.Fatalf("terminal invalidation was revived: reconciliation=%q eligibility=%+v", reconciliationID, eligibility)
	}
	if _, err := startWork(ctx, store, "operation-after-attempted-revival", "work-old", "revision-1"); !execution.IsKind(err, execution.KindAuthorityConflict) {
		t.Fatalf("Start after attempted revival error = %v", err)
	}
}

func TestFinalizedGraphClosesSeedAndDirectSQLMutationPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openWorkGraphStore(t)
	revision := storeRevision(t, "revision-1", 1, "work-a")
	if err := store.finalizeWorkRevisionForTest(ctx, revision, 0); err != nil {
		t.Fatal(err)
	}
	secondRevision := storeRevision(t, "revision-2", 2, "work-b")
	if err := store.finalizeWorkRevisionForTest(ctx, secondRevision, 1); err != nil {
		t.Fatal(err)
	}

	mutations := map[string]string{
		"parent revision number": `UPDATE work_revisions SET revision_number=99 WHERE work_revision_id='revision-1'`,
		"parent delete":          `DELETE FROM work_revisions WHERE work_revision_id='revision-1'`,
		"invented authority row": `INSERT INTO work_items(work_id,work_revision_id,state) VALUES('work-invented','revision-1','ready')`,
		"membership update":      `UPDATE work_graph_items SET work_id='work-invented' WHERE work_revision_id='revision-1' AND work_id='work-a'`,
		"membership delete":      `DELETE FROM work_graph_items WHERE work_revision_id='revision-1' AND work_id='work-a'`,
		"membership append":      `INSERT INTO work_graph_items(work_revision_id,work_id) VALUES('revision-1','work-invented')`,
		"cross-generation row":   `INSERT INTO work_items(work_id,work_revision_id,state) VALUES('work-a','revision-2','ready')`,
		"cross-generation move":  `UPDATE work_items SET work_revision_id='revision-2' WHERE work_revision_id='revision-1' AND work_id='work-a'`,
	}
	for name, statement := range mutations {
		if _, err := store.db.ExecContext(ctx, statement); err == nil {
			t.Errorf("%s mutation succeeded", name)
		}
	}
	if err := store.SeedAuthority(ctx, Seed{ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-invented"}); !execution.IsKind(err, execution.KindInvalidCommand) {
		t.Fatalf("seed append error = %v", err)
	}
	if err := store.SeedAuthority(ctx, Seed{ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-2", WorkID: "work-a"}); !execution.IsKind(err, execution.KindInvalidCommand) {
		t.Fatalf("cross-generation seed error = %v", err)
	}
	if _, err := startWork(ctx, store, "operation-invented", "work-invented", "revision-1"); !execution.IsKind(err, execution.KindNotFound) {
		t.Fatalf("invented WorkID Start error = %v", err)
	}
}

func TestSealedReconciliationRejectsAppendOnlyChildTampering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openWorkGraphStore(t)
	oldRevision := storeRevision(t, "revision-1", 1, "work-a")
	newRevision := storeRevision(t, "revision-2", 2, "work-a")
	if err := store.finalizeWorkRevisionForTest(ctx, oldRevision, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.finalizeWorkRevisionForTest(ctx, newRevision, 1); err != nil {
		t.Fatal(err)
	}
	reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: "reconciliation-sealed", Old: oldRevision, New: newRevision, Disposition: workgraph.Continue,
		ActorID: "actor-owner", Rationale: "equivalent", OldWorkIDs: []ids.WorkID{"work-a"}, NewWorkIDs: []ids.WorkID{"work-a"}, OccurredAt: time.Unix(2, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.reconcileWorkRevisionForTest(ctx, reconciliation); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO reconciliation_fork_edges(reconciliation_id,from_old_work_id,to_new_work_id) VALUES('reconciliation-sealed','work-a','work-invented')`); err == nil {
		t.Fatal("sealed reconciliation accepted appended fork edge")
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO reconciliation_consequences(reconciliation_id,work_id,work_status,run_may_continue,handoff_eligible,integration_eligible,expire_authority,quarantine_workspace,preserves_integrated_history,reason) VALUES('reconciliation-sealed','work-invented','active',1,1,1,0,0,0,'invented')`); err == nil {
		t.Fatal("sealed reconciliation accepted appended consequence")
	}
}

func TestReconciliationReplayIsSemanticConcurrentAndRestartSafe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	seed, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	oldRevision := storeRevision(t, "revision-1", 1, "work-a")
	newRevision := storeRevision(t, "revision-2", 2, "work-a")
	if err := seed.finalizeWorkRevisionForTest(ctx, oldRevision, 0); err != nil {
		t.Fatal(err)
	}
	if err := seed.finalizeWorkRevisionForTest(ctx, newRevision, 1); err != nil {
		t.Fatal(err)
	}
	reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: "reconciliation-replay", Old: oldRevision, New: newRevision, Disposition: workgraph.Continue,
		ActorID: "actor-owner", Rationale: "equivalent", OldWorkIDs: []ids.WorkID{"work-a"}, NewWorkIDs: []ids.WorkID{"work-a"}, OccurredAt: time.Unix(2, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make([]error, 2)
	var wait sync.WaitGroup
	for index, store := range []*Store{first, second} {
		wait.Add(1)
		go func(index int, store *Store) {
			defer wait.Done()
			<-start
			errs[index] = store.reconcileWorkRevisionForTest(ctx, reconciliation)
		}(index, store)
	}
	close(start)
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent identical replay: %v", err)
		}
	}
	_ = first.Close()
	_ = second.Close()
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.reconcileWorkRevisionForTest(ctx, reconciliation); err != nil {
		t.Fatalf("restart replay: %v", err)
	}

	changed, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: "reconciliation-replay", Old: oldRevision, New: newRevision, Disposition: workgraph.Continue,
		ActorID: "actor-owner", Rationale: "different rationale", OldWorkIDs: []ids.WorkID{"work-a"}, NewWorkIDs: []ids.WorkID{"work-a"}, OccurredAt: time.Unix(2, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.reconcileWorkRevisionForTest(ctx, changed); !workgraph.IsKind(err, workgraph.KindReconciliationConflict) {
		t.Fatalf("changed replay error = %v", err)
	}
}

func openWorkGraphStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func (s *Store) finalizeWorkRevisionForTest(ctx context.Context, revision workgraph.Revision, expectedPreviousNumber int64) error {
	_, err := s.FinalizeWorkRevision(ctx, FinalizeWorkRevisionRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-finalize-" + revision.ID().String(), OperationID: ids.OperationID("operation-finalize-" + revision.ID().String()),
		ActorID: revision.ActorID(), ProcessID: ids.ProcessID("process-finalize-" + revision.ID().String()), OccurredAt: revision.FinalizedAt(),
		Revision: revision, ExpectedPreviousNumber: expectedPreviousNumber,
	})
	return err
}

func (s *Store) reconcileWorkRevisionForTest(ctx context.Context, reconciliation workgraph.Reconciliation) error {
	return s.ReconcileWorkRevision(ctx, ReconcileWorkRevisionRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-" + reconciliation.ID(), OperationID: ids.OperationID(reconciliation.ID()),
		ActorID: reconciliation.ActorID(), ProcessID: ids.ProcessID("process-" + reconciliation.ID()), OccurredAt: reconciliation.OccurredAt(), Reconciliation: reconciliation,
	})
}

func storeRevision(t *testing.T, revisionID ids.WorkRevisionID, number int64, workID ids.WorkID) workgraph.Revision {
	return storeRevisionMany(t, revisionID, number, []ids.WorkID{workID})
}

func storeRevisionMany(t *testing.T, revisionID ids.WorkRevisionID, number int64, workIDs []ids.WorkID) workgraph.Revision {
	t.Helper()
	draft := workgraph.NewDraft("project-1", "mission-1")
	if err := draft.AddLane(workgraph.Lane{ID: "lane-a"}); err != nil {
		t.Fatal(err)
	}
	for _, workID := range workIDs {
		if err := draft.AddWork(workgraph.WorkItem{ID: workID, Title: workID.String(), LaneID: "lane-a", Criteria: []workgraph.Criterion{{ID: "criterion-" + workID.String(), Text: "done"}}, SourceRevisions: []workgraph.SourceRevision{{Provider: "tracker", Revision: revisionID.String()}}}); err != nil {
			t.Fatal(err)
		}
	}
	revision, err := draft.Finalize(workgraph.FinalizeMetadata{ID: revisionID, Number: number, ActorID: "actor-owner", FinalizedAt: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func startWork(ctx context.Context, store *Store, operation, work, revision string) (execution.RunCapsule, error) {
	service := execution.NewService(store, execution.RandomIdentities{}, execution.SystemClock{})
	return service.Start(ctx, execution.StartRequest{
		OperationID: ids.OperationID(operation), ProjectID: "project-1", MissionID: "mission-1",
		WorkRevisionID: ids.WorkRevisionID(revision), WorkID: ids.WorkID(work), ActorID: "actor-worker", ProcessID: ids.ProcessID("process-" + operation), LeaseTTL: time.Minute,
	})
}

func TestWorkRevisionNotFoundIsTyped(t *testing.T) {
	t.Parallel()
	store := openWorkGraphStore(t)
	_, err := store.WorkRevision(context.Background(), "missing")
	if !workgraph.IsKind(err, workgraph.KindNotFound) || errors.Is(err, context.Canceled) {
		t.Fatalf("not found error = %v", err)
	}
}

func TestShippedWorkGraphMigrationV5DigestIsFrozen(t *testing.T) {
	digest := sha256.Sum256([]byte(workGraphMigrationV5))
	const shippedDigest = "7379fcf39fc07c12c2d4e35995a0cf0d99eb1f4fee60287c0d2b2567efca51f3"
	if got := fmt.Sprintf("%x", digest); got != shippedDigest {
		t.Fatalf("shipped v5 digest=%s", got)
	}
}

func TestFreshWorkGraphSchemaPreservesV6AndInstallsCurrentSchema(t *testing.T) {
	t.Parallel()
	store := openWorkGraphStore(t)
	var version int
	if err := store.db.QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("fresh schema version=%d error=%v", version, err)
	}
	for _, name := range []string{"work_graph_operations", "terminal_canonical_work_item_state", "prevent_terminal_lease_reactivation"} {
		var count int
		if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sqlite_master WHERE name=?`, name).Scan(&count); err != nil || count != 1 {
			t.Fatalf("fresh schema object %s count=%d error=%v", name, count, err)
		}
	}
}

func TestWorkGraphSchemaMigratesFromStableWP02V4ThroughCurrentSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for version := 0; version < 4; version++ {
		if _, err := raw.ExecContext(ctx, migrations[version]); err != nil {
			t.Fatalf("seed migration %d: %v", version+1, err)
		}
	}
	if _, err := raw.ExecContext(ctx, `PRAGMA user_version=4`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("upgrade v4 through current schema: %v", err)
	}
	defer store.Close()
	var version int
	if err := store.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version=%d, want %d", version, currentSchemaVersion)
	}
	for _, table := range []string{"external_effects", "work_graph_revisions", "work_graph_items", "work_graph_operations", "reconciliations", "reconciliation_consequences", "workspace_quarantine_intents"} {
		var count int
		if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("table %s count=%d", table, count)
		}
	}
}

func TestPopulatedShippedV5UpgradesThroughCurrentSchemaWithoutLosingAuthorityOrEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, oldRevision, newRevision := createPopulatedShippedV5(t)
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("upgrade populated shipped v5: %v", err)
	}
	defer store.Close()

	var version int
	if err := store.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("schema version=%d error=%v", version, err)
	}
	for _, object := range []struct{ kind, name string }{
		{"table", "work_graph_operations"},
		{"index", "one_finalize_operation_per_work_revision"},
		{"index", "one_reconcile_operation_per_reconciliation"},
		{"trigger", "terminal_canonical_work_item_state"},
		{"trigger", "prevent_terminal_lease_reactivation"},
		{"trigger", "enforce_finalized_work_graph_lease_insert"},
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type=? AND name=?`, object.kind, object.name).Scan(&count); err != nil || count != 1 {
			t.Fatalf("schema object %s/%s count=%d error=%v", object.kind, object.name, count, err)
		}
	}
	loadedOld, err := store.WorkRevision(ctx, oldRevision.ID())
	if err != nil || loadedOld.GraphDigest() != oldRevision.GraphDigest() || loadedOld.Number() != 1 {
		t.Fatalf("preserved old revision=%+v error=%v", loadedOld, err)
	}
	evidence, err := store.Reconciliation(ctx, "reconciliation-v5")
	if err != nil || evidence.OldRevisionID != oldRevision.ID() || evidence.NewRevisionID != newRevision.ID() || evidence.Disposition != workgraph.Supersede {
		t.Fatalf("preserved reconciliation=%+v error=%v", evidence, err)
	}
	var state, leaseStatus, effectMarker string
	if err := store.db.QueryRowContext(ctx, `SELECT state FROM work_items WHERE work_revision_id=? AND work_id='work-done'`, oldRevision.ID()).Scan(&state); err != nil || state != "done" {
		t.Fatalf("preserved terminal work state=%q error=%v", state, err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM leases WHERE lease_id='lease-v5'`).Scan(&leaseStatus); err != nil || leaseStatus != "active" {
		t.Fatalf("preserved lease status=%q error=%v", leaseStatus, err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT marker FROM external_effects WHERE operation_id='operation-effect-v5'`).Scan(&effectMarker); err != nil || effectMarker != "marker-v5" {
		t.Fatalf("preserved external effect marker=%q error=%v", effectMarker, err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE work_items SET state='ready' WHERE work_revision_id=? AND work_id='work-done'`, oldRevision.ID()); err == nil {
		t.Fatal("upgraded v5 allowed terminal work revival")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE leases SET status='expired' WHERE lease_id='lease-v5'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE leases SET status='active' WHERE lease_id='lease-v5'`); err == nil {
		t.Fatal("upgraded v5 allowed terminal lease reactivation")
	}

	thirdRevision := storeRevision(t, "revision-v6", 3, "work-new")
	if err := store.finalizeWorkRevisionForTest(ctx, thirdRevision, 2); err != nil {
		t.Fatalf("finalize after v5 upgrade: %v", err)
	}
	reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: "reconciliation-v6", Old: newRevision, New: thirdRevision, Disposition: workgraph.Continue,
		ActorID: "actor-owner", Rationale: "post-upgrade equivalent", OldWorkIDs: []ids.WorkID{"work-new"}, NewWorkIDs: []ids.WorkID{"work-new"}, OccurredAt: time.Unix(6, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.reconcileWorkRevisionForTest(ctx, reconciliation); err != nil {
		t.Fatalf("reconcile after v5 upgrade: %v", err)
	}
	var operations int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_graph_operations`).Scan(&operations); err != nil || operations != 2 {
		t.Fatalf("post-upgrade command operations=%d error=%v", operations, err)
	}
}

func createPopulatedShippedV5(t *testing.T) (string, workgraph.Revision, workgraph.Revision) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shipped-v5.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	for version := 0; version < 5; version++ {
		if _, err := db.ExecContext(ctx, migrations[version]); err != nil {
			t.Fatalf("install shipped migration %d: %v", version+1, err)
		}
	}
	oldRevision := storeRevisionMany(t, "revision-v5-old", 1, []ids.WorkID{"work-lease", "work-done"})
	newRevision := storeRevision(t, "revision-v5-new", 2, "work-new")
	reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: "reconciliation-v5", Old: oldRevision, New: newRevision, Disposition: workgraph.Supersede,
		ActorID: "actor-owner", Rationale: "shipped v5 evidence", OldWorkIDs: []ids.WorkID{"work-lease"}, NewWorkIDs: []ids.WorkID{"work-new"}, OccurredAt: time.Unix(5, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	now := time.Unix(5, 0).UTC().Format(time.RFC3339Nano)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO projects(project_id,created_at) VALUES('project-1',?)`, []any{now}},
		{`INSERT INTO actors(actor_id,created_at) VALUES('actor-owner',?),('actor-worker',?)`, []any{now, now}},
		{`INSERT INTO processes(process_id,actor_id,observed_at) VALUES('process-v5','actor-worker',?)`, []any{now}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, revision := range []workgraph.Revision{oldRevision, newRevision} {
		encoded, err := revision.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_revisions(work_revision_id,project_id,mission_id,revision_number,finalized_at) VALUES(?,?,?,?,?)`, revision.ID(), revision.ProjectID(), revision.MissionID(), revision.Number(), revision.FinalizedAt().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		for _, item := range revision.WorkItems() {
			if _, err := tx.ExecContext(ctx, `INSERT INTO work_graph_items(work_revision_id,work_id) VALUES(?,?)`, revision.ID(), item.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_graph_revisions(work_revision_id,graph_digest,criteria_digest,author_actor_id,encoded_revision) VALUES(?,?,?,?,?)`, revision.ID(), revision.GraphDigest(), revision.CriteriaDigest(), revision.ActorID(), encoded); err != nil {
			t.Fatal(err)
		}
		for _, item := range revision.WorkItems() {
			state := "ready"
			if item.ID == "work-done" {
				state = "done"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO work_items(work_id,work_revision_id,state) VALUES(?,?,?)`, item.ID, revision.ID(), state); err != nil {
				t.Fatal(err)
			}
		}
	}
	statuses := []workgraph.WorkStatus{workgraph.StatusActive, workgraph.StatusHandedOff, workgraph.StatusApproved, workgraph.StatusIntegrated}
	for _, status := range statuses {
		consequence, ok := reconciliation.Consequence("work-lease", status)
		if !ok {
			t.Fatal("missing shipped reconciliation consequence")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO reconciliation_consequences(reconciliation_id,work_id,work_status,run_may_continue,handoff_eligible,integration_eligible,expire_authority,quarantine_workspace,preserves_integrated_history,reason) VALUES(?,?,?,?,?,?,?,?,?,?)`, reconciliation.ID(), "work-lease", status, consequence.RunMayContinue, consequence.HandoffEligible, consequence.IntegrationEligible, consequence.ExpireAuthority, consequence.QuarantineWorkspace, consequence.PreservesIntegratedHistory, consequence.Reason); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Unix(65, 0).UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,result_run_id,result_lease_id,result_generation,result_deadline,created_at) VALUES('operation-run-v5','start_run','operation-run-v5','correlation-run-v5','digest-v5','succeeded','run-v5','lease-v5',1,?,?)`, deadline, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runs(run_id,operation_id,project_id,mission_id,work_id,work_revision_id,actor_id,process_id,generation,status,created_at) VALUES('run-v5','operation-run-v5','project-1','mission-1','work-lease',?,'actor-worker','process-v5',1,'active',?)`, oldRevision.ID(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO leases(lease_id,run_id,work_id,work_revision_id,generation,token_digest,status,heartbeat_at,deadline) VALUES('lease-v5','run-v5','work-lease',?,1,'token-digest-v5','active',?,?)`, oldRevision.ID(), now, deadline); err != nil {
		t.Fatal(err)
	}
	oldWorkIDs, _ := json.Marshal(reconciliation.OldWorkIDs())
	newWorkIDs, _ := json.Marshal(reconciliation.NewWorkIDs())
	if _, err := tx.ExecContext(ctx, `INSERT INTO reconciliations(reconciliation_id,request_digest,old_work_revision_id,new_work_revision_id,disposition,old_graph_digest,new_graph_digest,old_criteria_digest,new_criteria_digest,actor_id,rationale,old_work_ids,new_work_ids,occurred_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, reconciliation.ID(), reconciliation.RequestDigest(), reconciliation.OldRevisionID(), reconciliation.NewRevisionID(), reconciliation.Disposition(), reconciliation.OldGraphDigest(), reconciliation.NewGraphDigest(), reconciliation.OldCriteriaDigest(), reconciliation.NewCriteriaDigest(), reconciliation.ActorID(), reconciliation.Rationale(), oldWorkIDs, newWorkIDs, reconciliation.OccurredAt().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO external_effects(operation_id,schema_version,correlation_id,actor_id,process_id,provider,work_key,marker,prior_revision,prior_value,applied_revision,applied_value,response_status,compensation_status,compensation_revision,conflict_revision,conflict_value,conflict_marker,observed_at) VALUES('operation-effect-v5',1,'correlation-effect-v5','actor-worker','process-v5','tracker','work-lease','marker-v5',1,'old',2,'new','applied','none',0,0,'','',?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version=5`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateTokenKey(ctx, path, true); err != nil {
		t.Fatal(err)
	}
	return path, oldRevision, newRevision
}
