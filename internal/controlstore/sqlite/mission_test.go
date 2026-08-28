package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
