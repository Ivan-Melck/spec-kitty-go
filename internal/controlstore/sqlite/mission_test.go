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
	claimed, found, err := store.ClaimOutbox(ctx, "claim-01", at)
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
	if _, found, err = store.ClaimOutbox(ctx, "claim-too-early", retryAt.Add(-time.Second)); err != nil || found {
		t.Fatalf("early claim found=%v err=%v", found, err)
	}
	claimed, found, err = store.ClaimOutbox(ctx, "claim-02", retryAt)
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
	if _, _, err = store.ClaimOutbox(ctx, "fault-claim", at); err == nil {
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
