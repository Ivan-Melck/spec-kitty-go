package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/mission"
)

func TestDecompositionReservationIsDurableReplayableAndGloballyExclusive(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	seedPhase3Mission(t, store, snapshot)
	request := mission.DecompositionOperationRequest{MissionID: snapshot.MissionID, OperationID: "operation-decompose-root", RequestDigest: strings.Repeat("a", 64), OccurredAt: at}
	first, err := store.ReserveDecompositionOperation(ctx, request)
	if err != nil || first.Replayed {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := store.ReserveDecompositionOperation(ctx, request)
	if err != nil || !second.Replayed {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	changed := request
	changed.RequestDigest = strings.Repeat("b", 64)
	if _, err = store.ReserveDecompositionOperation(ctx, changed); !decompositionKind(err, mission.DecompositionOperationConflict) {
		t.Fatalf("changed replay=%v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if replay, err := store.ReserveDecompositionOperation(ctx, request); err != nil || !replay.Replayed {
		t.Fatalf("restart replay=%+v err=%v", replay, err)
	}
}

func TestDecompositionReservationRejectsLegacyAndCrossKindOperationIDs(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	seedPhase3Mission(t, store, snapshot)
	request := mission.DecompositionOperationRequest{MissionID: snapshot.MissionID, RequestDigest: strings.Repeat("a", 64), OccurredAt: at}

	request.OperationID = "operation-legacy-collision"
	if _, err := store.db.ExecContext(ctx, `INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,created_at) VALUES(?,?,?,?,?,?,?)`, request.OperationID, "claim", "decomposition-collision", "correlation-collision", strings.Repeat("c", 64), "started", formatTime(at)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveDecompositionOperation(ctx, request); !decompositionKind(err, mission.DecompositionOperationConflict) {
		t.Fatalf("legacy collision=%v", err)
	}
	request.OperationID = "operation-cross-kind"
	if _, err := store.db.ExecContext(ctx, `INSERT INTO phase3_operations(operation_id,operation_kind,mission_id,request_digest,result_json,occurred_at) VALUES(?,?,?,?,?,?)`, request.OperationID, "mission_action", snapshot.MissionID, request.RequestDigest, []byte(`{}`), formatTime(at)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveDecompositionOperation(ctx, request); !decompositionKind(err, mission.DecompositionOperationConflict) {
		t.Fatalf("cross-kind collision=%v", err)
	}
}

func TestDecompositionReservationConcurrentSameOperationConverges(t *testing.T) {
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
	at := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	seedPhase3Mission(t, first, snapshot)
	request := mission.DecompositionOperationRequest{MissionID: snapshot.MissionID, OperationID: "operation-decompose-race", RequestDigest: strings.Repeat("d", 64), OccurredAt: at}
	stores := []*Store{first, second}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 100)
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(store *Store) {
			defer wait.Done()
			_, err := store.ReserveDecompositionOperation(ctx, request)
			errorsSeen <- err
		}(stores[index%len(stores)])
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("same operation did not converge: %v", err)
		}
	}
	var count int
	if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM phase3_operations WHERE operation_id=?`, request.OperationID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("operation count=%d err=%v", count, err)
	}
}

func decompositionKind(err error, kind mission.DecompositionErrorKind) bool {
	var typed *mission.DecompositionError
	return errors.As(err, &typed) && typed.Kind == kind
}
