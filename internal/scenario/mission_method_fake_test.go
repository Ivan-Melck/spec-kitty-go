package scenario

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

func TestMissionMethodFakeTwoItemDurableScenario(t *testing.T) {
	for _, suffix := range []string{"portable-one", "portable-two"} {
		t.Run(suffix, func(t *testing.T) {
			result := runPortableMissionFixture(t, suffix)
			if result.GenerationOne.Generation() != 1 || result.GenerationTwo.Generation() != 2 || result.GenerationTwo.PredecessorID() != result.GenerationOne.ID() {
				t.Fatalf("revision lineage one=%s two=%s predecessor=%s", result.GenerationOne.ID(), result.GenerationTwo.ID(), result.GenerationTwo.PredecessorID())
			}
			if result.MissionOne.CurrentWorkRevisionID != result.GenerationOne.ID() || result.MissionTwo.CurrentWorkRevisionID != result.GenerationTwo.ID() {
				t.Fatalf("mission pointers one=%s two=%s", result.MissionOne.CurrentWorkRevisionID, result.MissionTwo.CurrentWorkRevisionID)
			}
			store, err := sqlite.Open(context.Background(), result.Database)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if historical, err := store.WorkRevisionV2(context.Background(), result.GenerationOne.ID()); err != nil || historical.GraphDigest() != result.GenerationOne.GraphDigest() {
				t.Fatalf("historical generation changed: revision=%+v err=%v", historical, err)
			}
			observation, err := store.ObserveMissionMethod(context.Background(), result.MissionTwo.MissionID)
			if err != nil || observation.Workgraph.Generation() != 2 || len(observation.Workgraph.Reconciliations()) != 2 {
				t.Fatalf("reopened observation=%+v err=%v", observation, err)
			}
		})
	}
}

func TestMissionMethodHelperPureQueriesProjectionDamageAndStandaloneComprehension(t *testing.T) {
	result := runPortableMissionFixture(t, "portable-purity")
	before, err := os.ReadFile(result.Database)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 100; index++ {
		envelope := callMethod(t, context.Background(), "mission", "next", map[string]any{"database": result.Database, "correlation_id": "correlation-pure", "mission_id": result.MissionTwo.MissionID})
		var next mission.NextResult
		decodeMethodResult(t, envelope, &next)
		if next.Kind != mission.NextWorkReady || envelope.OperationID != "" {
			t.Fatalf("query %d next=%+v envelope=%+v", index, next, envelope)
		}
	}
	after, err := os.ReadFile(result.Database)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("100 pure next queries changed the authoritative database file")
	}

	path := filepath.Join(result.OutputRoot, "tasks.md")
	if err = os.WriteFile(path, []byte("corrupt derived output"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	projection := callMethod(t, context.Background(), "projection", "tasks", map[string]any{"database": result.Database, "correlation_id": "correlation-regenerate", "mission_id": result.MissionTwo.MissionID, "rendered_at": result.MissionTwo.UpdatedAt, "trusted_root": result.OutputRoot, "destination": "tasks.md"})
	var rendered struct {
		Content    string `json:"content"`
		Generation uint64 `json:"generation"`
	}
	decodeMethodResult(t, projection, &rendered)
	if rendered.Generation != 2 || !strings.Contains(rendered.Content, "Define the revised contract") || !strings.Contains(rendered.Content, "NON-AUTHORITATIVE") {
		t.Fatalf("regenerated projection=%+v", rendered)
	}
	unsafe := callMethodRaw(t, context.Background(), "projection", "tasks", map[string]any{"database": result.Database, "correlation_id": "correlation-unsafe", "mission_id": result.MissionTwo.MissionID, "rendered_at": result.MissionTwo.UpdatedAt, "trusted_root": result.OutputRoot, "destination": "../escape.md"})
	if unsafe.OK || unsafe.Error == nil {
		t.Fatalf("unsafe projection path succeeded: %+v", unsafe)
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(result.OutputRoot), "escape.md")); !os.IsNotExist(err) {
		t.Fatalf("unsafe projection created output: %v", err)
	}

	// Fresh comprehension uses only the public immutable revision and helper
	// projection. It identifies objective, criteria, generation, source, and
	// the next legal action without fixture prose or private package fields.
	items := result.GenerationTwo.WorkItems()
	if len(items) != 1 || items[0].Title() == "" || items[0].Description() == "" || len(items[0].Criteria()) == 0 || result.GenerationTwo.PlanningSourceCommit() == "" {
		t.Fatalf("public revision is not independently comprehensible: %+v", result.GenerationTwo)
	}
	next := callMethod(t, context.Background(), "mission", "next", map[string]any{"database": result.Database, "correlation_id": "correlation-comprehend", "mission_id": result.MissionTwo.MissionID})
	var nextResult mission.NextResult
	decodeMethodResult(t, next, &nextResult)
	if nextResult.Kind != mission.NextWorkReady {
		t.Fatalf("next legal action unavailable: %+v", nextResult)
	}
}
