package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
)

func TestPhase3MissionCreateReplayConflictAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	service := mission.NewService(registry, store)
	at := time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)
	request := mission.CreateRequest{MissionID: ids.MissionID("mission-01"), ProjectID: ids.ProjectID("project-01"), DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: ids.OperationID("operation-create-01"), ActorID: ids.ActorID("actor-01"), OccurredAt: at}
	first, err := service.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || first.Instance.StateVersion != 1 {
		t.Fatalf("unexpected create results: %#v %#v", first, second)
	}
	changed := request
	changed.ProjectID = ids.ProjectID("project-02")
	if _, err = service.Create(ctx, changed); !mission.IsKind(err, mission.KindIdempotencyConflict) {
		t.Fatalf("changed operation replay: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.LoadMission(ctx, request.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ProjectID != request.ProjectID || loaded.StateVersion != 1 {
		t.Fatalf("restored mission mismatch: %#v", loaded)
	}
}

func TestPhase3OutboxDeliveryLifecycleSurvivesRestartAndRejectsMutants(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := softwaredev.Definition()
	registry, _ := mission.NewRegistry(definition)
	at := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)
	request := mission.CreateRequest{MissionID: "mission-outbox", ProjectID: "project-outbox", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: "operation-outbox", ActorID: "actor-outbox", OccurredAt: at}
	if _, err = mission.NewService(registry, store).Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	var available string
	if err = store.db.QueryRow(`SELECT delivery_status,attempt_count,available_at FROM phase3_outbox`).Scan(&status, &attempts, &available); err != nil || status != "pending" || attempts != 0 || available != formatTime(at) {
		t.Fatalf("initial status=%q attempts=%d available=%q err=%v", status, attempts, available, err)
	}
	claimed, found, err := store.ClaimOutbox(ctx, "claim-01", at, at.Add(time.Minute))
	if err != nil || !found || claimed.DeliveryStatus != controlstore.OutboxClaimed || claimed.AttemptCount != 1 || claimed.ClaimID != "claim-01" {
		t.Fatalf("claim=%#v found=%v err=%v", claimed, found, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	retryAt := at.Add(time.Minute)
	retry, err := store.RetryOutbox(ctx, claimed.Sequence, "claim-01", retryAt, at.Add(30*time.Second))
	if err != nil || retry.DeliveryStatus != controlstore.OutboxRetryable || retry.AttemptCount != 1 || !retry.AvailableAt.Equal(retryAt) {
		t.Fatalf("retry=%#v err=%v", retry, err)
	}
	if _, found, err = store.ClaimOutbox(ctx, "claim-too-early", retryAt.Add(-time.Second), retryAt.Add(time.Minute)); err != nil || found {
		t.Fatalf("early claim found=%v err=%v", found, err)
	}
	claimed, found, err = store.ClaimOutbox(ctx, "claim-02", retryAt, retryAt.Add(time.Minute))
	if err != nil || !found || claimed.AttemptCount != 2 {
		t.Fatalf("second claim=%#v found=%v err=%v", claimed, found, err)
	}
	delivered, err := store.DeliverOutbox(ctx, claimed.Sequence, "claim-02", retryAt.Add(time.Second))
	if err != nil || delivered.DeliveryStatus != controlstore.OutboxDelivered || delivered.AttemptCount != 2 || delivered.DeliveredAt.IsZero() {
		t.Fatalf("delivered=%#v err=%v", delivered, err)
	}
	mutants := []string{
		`UPDATE phase3_outbox SET event_id='substituted' WHERE sequence=1`,
		`UPDATE phase3_outbox SET delivery_status='claimed',claim_id='z',claimed_at=created_at,delivered_at=NULL,last_attempt_at=NULL WHERE sequence=1`,
		`DELETE FROM phase3_outbox WHERE sequence=1`,
	}
	for _, statement := range mutants {
		if _, err = store.db.Exec(statement); err == nil {
			t.Fatalf("outbox mutant succeeded: %s", statement)
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.db.QueryRow(`SELECT delivery_status,attempt_count FROM phase3_outbox`).Scan(&status, &attempts); err != nil || status != "delivered" || attempts != 2 {
		t.Fatalf("reopened status=%q attempts=%d err=%v", status, attempts, err)
	}
}

func TestPhase3OutboxClaimBoundaryFaultRollsBackAndReopensPending(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := softwaredev.Definition()
	registry, _ := mission.NewRegistry(definition)
	at := time.Date(2026, 8, 28, 3, 0, 0, 0, time.UTC)
	request := mission.CreateRequest{MissionID: "mission-outbox-fault", ProjectID: "project-outbox", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: "operation-outbox-fault", ActorID: "actor-outbox", OccurredAt: at}
	if _, err = mission.NewService(registry, store).Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`CREATE TRIGGER fault_outbox_claim BEFORE UPDATE ON phase3_outbox BEGIN SELECT RAISE(ABORT,'seeded claim fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.ClaimOutbox(ctx, "fault-claim", at, at.Add(time.Minute)); err == nil {
		t.Fatal("claim boundary fault was hidden")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var status string
	var attempts int
	if err = store.db.QueryRow(`SELECT delivery_status,attempt_count FROM phase3_outbox`).Scan(&status, &attempts); err != nil || status != "pending" || attempts != 0 {
		t.Fatalf("claim fault persisted status=%q attempts=%d err=%v", status, attempts, err)
	}
}

func TestPhase3OutboxExpiredClaimIsRecoveredByOneReplacement(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := softwaredev.Definition()
	registry, _ := mission.NewRegistry(definition)
	at := time.Date(2026, 8, 28, 3, 30, 0, 0, time.UTC)
	request := mission.CreateRequest{MissionID: "mission-outbox-recovery", ProjectID: "project-outbox", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: "operation-outbox-recovery", ActorID: "actor-outbox", OccurredAt: at}
	if _, err = mission.NewService(registry, store).Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	dead, found, err := store.ClaimOutbox(ctx, "claim-dead", at, at.Add(time.Minute))
	if err != nil || !found || dead.AttemptCount != 1 {
		t.Fatalf("dead claim=%#v found=%v err=%v", dead, found, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	left, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	if _, found, err = left.ClaimOutbox(ctx, "claim-premature", at.Add(59*time.Second), at.Add(2*time.Minute)); err != nil || found {
		t.Fatalf("pre-expiry recovery found=%v err=%v", found, err)
	}
	type claimResult struct {
		envelope controlstore.OutboxEnvelope
		found    bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	for index, candidate := range []struct {
		store *Store
		id    string
	}{{left, "claim-replacement-a"}, {right, "claim-replacement-b"}} {
		_ = index
		go func(candidate struct {
			store *Store
			id    string
		}) {
			<-start
			envelope, claimed, claimErr := candidate.store.ClaimOutbox(ctx, candidate.id, at.Add(time.Minute), at.Add(2*time.Minute))
			results <- claimResult{envelope, claimed, claimErr}
		}(candidate)
	}
	close(start)
	var winner controlstore.OutboxEnvelope
	winners := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.found {
			winner = result.envelope
			winners++
		}
	}
	if winners != 1 || winner.AttemptCount != 2 || winner.ClaimID == "claim-dead" || !winner.ClaimedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("winners=%d replacement=%#v", winners, winner)
	}
	if _, err = left.DeliverOutbox(ctx, dead.Sequence, "claim-dead", at.Add(70*time.Second)); err == nil {
		t.Fatal("expired claim retained delivery authority")
	}
	if _, err = left.RetryOutbox(ctx, dead.Sequence, "claim-dead", at.Add(80*time.Second), at.Add(70*time.Second)); err == nil {
		t.Fatal("expired claim retained retry authority")
	}
	delivered, err := left.DeliverOutbox(ctx, winner.Sequence, winner.ClaimID, at.Add(70*time.Second))
	if err != nil || delivered.DeliveryStatus != controlstore.OutboxDelivered || delivered.EventID != dead.EventID || delivered.PayloadDigest != dead.PayloadDigest {
		t.Fatalf("replacement delivery=%#v err=%v", delivered, err)
	}
}

func TestPhase3OutboxTemporalAndTransitionMutantsFailClosed(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	definition, _ := softwaredev.Definition()
	registry, _ := mission.NewRegistry(definition)
	at := time.Date(2026, 8, 28, 3, 45, 0, 0, time.UTC)
	request := mission.CreateRequest{MissionID: "mission-outbox-time", ProjectID: "project-outbox", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: "operation-outbox-time", ActorID: "actor-outbox", OccurredAt: at}
	if _, err = mission.NewService(registry, store).Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	assertUnchanged := func(t *testing.T, status string, attempts int) {
		t.Helper()
		var actualStatus string
		var actualAttempts int
		if err := store.db.QueryRow(`SELECT delivery_status,attempt_count FROM phase3_outbox`).Scan(&actualStatus, &actualAttempts); err != nil || actualStatus != status || actualAttempts != attempts {
			t.Fatalf("status=%q attempts=%d err=%v", actualStatus, actualAttempts, err)
		}
	}
	if _, _, err = store.ClaimOutbox(ctx, "claim-unbounded", at, at.Add(maxOutboxClaimLease+time.Second)); err == nil {
		t.Fatal("unbounded claim lease reached storage")
	}
	if _, _, err = store.ClaimOutbox(ctx, "claim-nonpositive", at, at); err == nil {
		t.Fatal("nonpositive claim lease reached storage")
	}
	assertUnchanged(t, "pending", 0)
	pendingMutants := []string{
		`UPDATE phase3_outbox SET delivery_status='claimed',attempt_count=1,claim_id='claim-bad-time',claimed_at='2026-08-27T03:45:00Z',claim_expires_at='2026-08-28T03:46:00Z'`,
		`UPDATE phase3_outbox SET delivery_status='claimed',attempt_count=1,claim_id='',claimed_at='2026-08-28T03:45:00Z',claim_expires_at='2026-08-28T03:46:00Z'`,
		`UPDATE phase3_outbox SET delivery_status='claimed',attempt_count=1,claim_id='claim-too-long',claimed_at='2026-08-28T03:45:00Z',claim_expires_at='2026-08-28T04:45:00Z'`,
		`UPDATE phase3_outbox SET delivery_status='claimed',attempt_count=2,claim_id='claim-jump',claimed_at='2026-08-28T03:45:00Z',claim_expires_at='2026-08-28T03:46:00Z'`,
		`UPDATE phase3_outbox SET delivery_status='delivered',attempt_count=1,last_attempt_at='2026-08-28T03:45:00Z',delivered_at='2026-08-28T03:45:00Z'`,
		`UPDATE phase3_outbox SET available_at='2026-08-28T03:46:00Z'`,
	}
	for _, statement := range pendingMutants {
		if _, err = store.db.Exec(statement); err == nil {
			t.Fatalf("pending mutant succeeded: %s", statement)
		}
		assertUnchanged(t, "pending", 0)
	}
	claimed, found, err := store.ClaimOutbox(ctx, "claim-time-01", at, at.Add(time.Minute))
	if err != nil || !found {
		t.Fatalf("claim=%#v found=%v err=%v", claimed, found, err)
	}
	claimedMutants := []string{
		`UPDATE phase3_outbox SET delivery_status='retryable',claim_id=NULL,last_attempt_at='2026-08-28T03:44:59Z',available_at='2026-08-28T03:46:00Z'`,
		`UPDATE phase3_outbox SET delivery_status='retryable',claim_id=NULL,last_attempt_at='2026-08-28T03:45:30Z',available_at='2026-08-28T03:45:29Z'`,
		`UPDATE phase3_outbox SET delivery_status='delivered',claim_id=NULL,last_attempt_at='2026-08-28T03:45:30Z',delivered_at='2026-08-28T03:45:29Z'`,
		`UPDATE phase3_outbox SET delivery_status='delivered',attempt_count=2,claim_id=NULL,last_attempt_at='2026-08-28T03:45:30Z',delivered_at='2026-08-28T03:45:30Z'`,
		`UPDATE phase3_outbox SET delivery_status='pending',attempt_count=0,claim_id=NULL,claimed_at=NULL,claim_expires_at=NULL`,
		`UPDATE phase3_outbox SET claim_id='claim-same-status'`,
	}
	for _, statement := range claimedMutants {
		if _, err = store.db.Exec(statement); err == nil {
			t.Fatalf("claimed mutant succeeded: %s", statement)
		}
		assertUnchanged(t, "claimed", 1)
	}
	retryAt := at.Add(30 * time.Second)
	if _, err = store.RetryOutbox(ctx, claimed.Sequence, claimed.ClaimID, at.Add(2*time.Minute), retryAt); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE phase3_outbox SET delivery_status='claimed',attempt_count=2,claim_id='claim-before-availability',claimed_at='2026-08-28T03:46:00Z',claim_expires_at='2026-08-28T03:47:00Z'`); err == nil {
		t.Fatal("pre-availability claim succeeded")
	}
	assertUnchanged(t, "retryable", 1)
}

func TestOperationIdentityCannotCrossPhase3AndLegacyNamespaces(t *testing.T) {
	ctx := context.Background()
	newMission := func(t *testing.T, store *Store, missionID, operationID string) error {
		t.Helper()
		definition, _ := softwaredev.Definition()
		registry, _ := mission.NewRegistry(definition)
		_, err := mission.NewService(registry, store).Create(ctx, mission.CreateRequest{MissionID: ids.MissionID(missionID), ProjectID: "project-global-op", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: ids.OperationID(operationID), ActorID: "actor-global-op", OccurredAt: time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)})
		return err
	}
	t.Run("phase3 then legacy", func(t *testing.T) {
		store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if err = newMission(t, store, "mission-global-a", "operation-global-a"); err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.Exec(`INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,created_at) VALUES('operation-global-a','claim','key','correlation','digest','completed','2026-08-28T04:00:00Z')`); err == nil {
			t.Fatal("legacy operation reused phase3 OperationID")
		}
		if _, err = store.db.Exec(`INSERT INTO work_graph_operations(operation_id,operation_kind,request_digest,schema_version,correlation_id,actor_id,process_id,occurred_at,result_work_revision_id) VALUES('operation-global-a','finalize_work_revision','digest',1,'correlation','actor','process','2026-08-28T04:00:00Z','revision')`); err == nil {
			t.Fatal("work graph operation reused phase3 OperationID")
		}
	})
	t.Run("legacy then phase3", func(t *testing.T) {
		store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if _, err = store.db.Exec(`INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,created_at) VALUES('operation-global-b','claim','key','correlation','digest','completed','2026-08-28T04:00:00Z')`); err != nil {
			t.Fatal(err)
		}
		if err = newMission(t, store, "mission-global-b", "operation-global-b"); !mission.IsKind(err, mission.KindIdempotencyConflict) {
			t.Fatalf("phase3 reuse error=%v", err)
		}
		var missions, operations int
		if err = store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM phase3_missions),(SELECT COUNT(*) FROM phase3_operations)`).Scan(&missions, &operations); err != nil || missions != 0 || operations != 0 {
			t.Fatalf("partial phase3 write missions=%d operations=%d err=%v", missions, operations, err)
		}
	})
	t.Run("work graph then phase3", func(t *testing.T) {
		store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if _, err = store.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.Exec(`INSERT INTO work_graph_operations(operation_id,operation_kind,request_digest,schema_version,correlation_id,actor_id,process_id,occurred_at,result_work_revision_id) VALUES('operation-global-c','finalize_work_revision','digest',1,'correlation','actor','process','2026-08-28T04:00:00Z','revision')`); err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
			t.Fatal(err)
		}
		if err = newMission(t, store, "mission-global-c", "operation-global-c"); !mission.IsKind(err, mission.KindIdempotencyConflict) {
			t.Fatalf("phase3 reuse error=%v", err)
		}
	})
	t.Run("legacy operations then work graph", func(t *testing.T) {
		store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		revision := storeRevision(t, "revision-global-d", 1, "work-global-d")
		operationID := "operation-finalize-" + revision.ID().String()
		if _, err = store.db.Exec(`INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,created_at) VALUES(?,?,?,?,?,?,?)`, operationID, "claim", "key-global-d", "correlation", "digest", "completed", "2026-08-28T04:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if err = store.finalizeWorkRevisionForTest(ctx, revision, 0); err == nil {
			t.Fatal("work graph operation reused legacy OperationID")
		}
		var workGraphOperations, revisions int
		if err = store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM work_graph_operations),(SELECT COUNT(*) FROM work_graph_revisions)`).Scan(&workGraphOperations, &revisions); err != nil || workGraphOperations != 0 || revisions != 0 {
			t.Fatalf("partial work graph operation=%d revisions=%d err=%v", workGraphOperations, revisions, err)
		}
	})
	t.Run("work graph then legacy operations", func(t *testing.T) {
		store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		revision := storeRevision(t, "revision-global-e", 1, "work-global-e")
		if err = store.finalizeWorkRevisionForTest(ctx, revision, 0); err != nil {
			t.Fatal(err)
		}
		operationID := "operation-finalize-" + revision.ID().String()
		if _, err = store.db.Exec(`INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,created_at) VALUES(?,?,?,?,?,?,?)`, operationID, "claim", "key-global-e", "correlation", "digest", "completed", "2026-08-28T04:00:00Z"); err == nil {
			t.Fatal("legacy operation reused work graph OperationID")
		}
		var legacyOperations int
		if err = store.db.QueryRow(`SELECT COUNT(*) FROM operations WHERE operation_id=?`, operationID).Scan(&legacyOperations); err != nil || legacyOperations != 0 {
			t.Fatalf("partial legacy operation=%d err=%v", legacyOperations, err)
		}
	})
}

func TestLegacyOperationNamespacesTwoStoreRaceHasOneGlobalWinner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	left, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	revision := storeRevision(t, "revision-global-race", 1, "work-global-race")
	operationID := "operation-finalize-" + revision.ID().String()
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, insertErr := left.db.Exec(`INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,created_at) VALUES(?,?,?,?,?,?,?)`, operationID, "claim", "key-global-race", "correlation", "digest", "completed", "2026-08-28T04:30:00Z")
		results <- insertErr
	}()
	go func() {
		<-start
		results <- right.finalizeWorkRevisionForTest(ctx, revision, 0)
	}()
	close(start)
	winners := 0
	for range 2 {
		if resultErr := <-results; resultErr == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("global OperationID winners=%d", winners)
	}
	var legacyOperations, workGraphOperations int
	if err = left.db.QueryRow(`SELECT (SELECT COUNT(*) FROM operations WHERE operation_id=?),(SELECT COUNT(*) FROM work_graph_operations WHERE operation_id=?)`, operationID, operationID).Scan(&legacyOperations, &workGraphOperations); err != nil || legacyOperations+workGraphOperations != 1 {
		t.Fatalf("legacy=%d work_graph=%d err=%v", legacyOperations, workGraphOperations, err)
	}
	if legacyOperations == 1 {
		var revisions int
		if err = left.db.QueryRow(`SELECT COUNT(*) FROM work_graph_revisions WHERE work_revision_id=?`, revision.ID()).Scan(&revisions); err != nil || revisions != 0 {
			t.Fatalf("losing work graph left revision=%d err=%v", revisions, err)
		}
	}
	if err = left.Close(); err != nil {
		t.Fatal(err)
	}
	if err = right.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.db.QueryRow(`SELECT (SELECT COUNT(*) FROM operations WHERE operation_id=?),(SELECT COUNT(*) FROM work_graph_operations WHERE operation_id=?)`, operationID, operationID).Scan(&legacyOperations, &workGraphOperations); err != nil || legacyOperations+workGraphOperations != 1 {
		t.Fatalf("reopened legacy=%d work_graph=%d err=%v", legacyOperations, workGraphOperations, err)
	}
}

func TestLegacyOperationIdentityMutationIsRejectedWithoutBlockingLegalResults(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := softwaredev.Definition()
	registry, _ := mission.NewRegistry(definition)
	phase3ID := "operation-global-mutation-phase3"
	if _, err = mission.NewService(registry, store).Create(ctx, mission.CreateRequest{MissionID: "mission-global-mutation", ProjectID: "project-global-mutation", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: ids.OperationID(phase3ID), ActorID: "actor-global-mutation", OccurredAt: time.Date(2026, 8, 28, 5, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	revision := storeRevision(t, "revision-global-mutation", 1, "work-global-mutation")
	if err = store.finalizeWorkRevisionForTest(ctx, revision, 0); err != nil {
		t.Fatal(err)
	}
	workGraphID := "operation-finalize-" + revision.ID().String()
	insertLegacy := func(operationID, key string) {
		t.Helper()
		if _, insertErr := store.db.Exec(`INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,created_at) VALUES(?,?,?,?,?,'started','2026-08-28T05:00:00Z')`, operationID, "start_run", key, "correlation-"+key, "digest-"+key); insertErr != nil {
			t.Fatal(insertErr)
		}
	}
	insertLegacy("operation-legacy-mutate-phase3", "key-mutate-phase3")
	insertLegacy("operation-legacy-mutate-workgraph", "key-mutate-workgraph")
	insertLegacy("operation-legacy-mutate-free", "key-mutate-free")
	insertLegacy("operation-legacy-legal-success", "key-legal-success")
	insertLegacy("operation-legacy-legal-failure", "key-legal-failure")
	counts := func() [8]int {
		t.Helper()
		var value [8]int
		query := `SELECT (SELECT COUNT(*) FROM operations),(SELECT COUNT(*) FROM work_graph_operations),(SELECT COUNT(*) FROM phase3_operations),(SELECT COUNT(*) FROM runs),(SELECT COUNT(*) FROM work_graph_revisions),(SELECT COUNT(*) FROM events),(SELECT COUNT(*) FROM phase3_events),(SELECT COUNT(*) FROM phase3_outbox)`
		if queryErr := store.db.QueryRow(query).Scan(&value[0], &value[1], &value[2], &value[3], &value[4], &value[5], &value[6], &value[7]); queryErr != nil {
			t.Fatal(queryErr)
		}
		return value
	}
	before := counts()
	mutants := []struct {
		source string
		target string
	}{
		{"operation-legacy-mutate-phase3", phase3ID},
		{"operation-legacy-mutate-workgraph", workGraphID},
		{"operation-legacy-mutate-free", "operation-legacy-unused-target"},
	}
	for _, mutant := range mutants {
		if _, err = store.db.Exec(`UPDATE operations SET operation_id=? WHERE operation_id=?`, mutant.target, mutant.source); err == nil {
			t.Fatalf("legacy identity mutation %q -> %q succeeded", mutant.source, mutant.target)
		}
		var status string
		if err = store.db.QueryRow(`SELECT status FROM operations WHERE operation_id=?`, mutant.source).Scan(&status); err != nil || status != "started" {
			t.Fatalf("source %q changed status=%q err=%v", mutant.source, status, err)
		}
	}
	if _, err = store.db.Exec(`UPDATE work_graph_operations SET operation_id='operation-workgraph-mutated' WHERE operation_id=?`, workGraphID); err == nil {
		t.Fatal("work graph operation identity mutation succeeded")
	}
	if after := counts(); after != before {
		t.Fatalf("failed identity mutations changed durable evidence: before=%v after=%v", before, after)
	}
	if _, err = store.db.Exec(`UPDATE operations SET status='succeeded',result_run_id='run-legal',result_lease_id='lease-legal',result_generation=1,result_deadline='2026-08-28T05:05:00Z' WHERE operation_id='operation-legacy-legal-success'`); err != nil {
		t.Fatalf("legal success update failed: %v", err)
	}
	if _, err = store.db.Exec(`UPDATE operations SET status='failed',result_error_kind='authority_conflict',result_error_payload='{}' WHERE operation_id='operation-legacy-legal-failure'`); err != nil {
		t.Fatalf("legal failure update failed: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, mutant := range mutants {
		var present int
		if err = store.db.QueryRow(`SELECT COUNT(*) FROM operations WHERE operation_id=?`, mutant.source).Scan(&present); err != nil || present != 1 {
			t.Fatalf("reopened source %q present=%d err=%v", mutant.source, present, err)
		}
	}
	var succeeded, failed string
	if err = store.db.QueryRow(`SELECT (SELECT status FROM operations WHERE operation_id='operation-legacy-legal-success'),(SELECT status FROM operations WHERE operation_id='operation-legacy-legal-failure')`).Scan(&succeeded, &failed); err != nil || succeeded != "succeeded" || failed != "failed" {
		t.Fatalf("reopened legal statuses succeeded=%q failed=%q err=%v", succeeded, failed, err)
	}
}

func TestPhase3MigrationIsAppendOnlyAndForeignKeysRemainEnabled(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int
	if err = store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 13 {
		t.Fatalf("schema=%d err=%v", version, err)
	}
	settings, err := store.Settings(context.Background())
	if err != nil || !settings.ForeignKeys || settings.JournalMode != "wal" {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
}

func TestMissionCreateHundredCallerTwoConnectionReplayRace(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	left, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	definition, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	request := mission.CreateRequest{MissionID: "mission-race-create", ProjectID: "project-race", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: "operation-race-create", ActorID: "actor-race", OccurredAt: time.Date(2026, 8, 28, 7, 0, 0, 0, time.UTC)}
	const callers = 100
	var wg sync.WaitGroup
	failures := make(chan error, callers)
	success := make(chan mission.CreateResult, callers)
	for i := 0; i < callers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			store := left
			if i%2 == 1 {
				store = right
			}
			result, e := mission.NewService(registry, store).Create(ctx, request)
			if e != nil {
				failures <- e
				return
			}
			success <- result
		}()
	}
	wg.Wait()
	close(failures)
	close(success)
	for e := range failures {
		t.Errorf("create race: %v", e)
	}
	count, replays := 0, 0
	for r := range success {
		count++
		if r.Replayed {
			replays++
		}
	}
	if count != callers || replays != callers-1 {
		t.Fatalf("success=%d replay=%d", count, replays)
	}
}
