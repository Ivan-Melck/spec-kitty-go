package scenario

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

const missionMethodRaceHelper = "SPEC_KITTY_MISSION_METHOD_RACE_HELPER"

type missionMethodRaceConfig struct {
	Database       string                      `json:"database"`
	Barrier        string                      `json:"barrier"`
	Participant    string                      `json:"participant"`
	ResultPath     string                      `json:"result_path"`
	PlanRevisionID string                      `json:"plan_revision_id"`
	PlanDigest     string                      `json:"plan_digest"`
	Command        mission.FinalizationCommand `json:"command"`
}

type missionMethodRaceResult struct {
	OK         bool   `json:"ok"`
	Replayed   bool   `json:"replayed"`
	Kind       string `json:"kind,omitempty"`
	RevisionID string `json:"revision_id,omitempty"`
}

func TestMissionMethodFinalizationProcessRaces100(t *testing.T) {
	if configPath := os.Getenv(missionMethodRaceHelper); configPath != "" {
		runMissionMethodRaceHelper(t, configPath)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 100; iteration++ {
		suffix := fmt.Sprintf("race-%03d", iteration)
		database, expected, command := prepareDishonestFinalizationMission(t, suffix)
		barrier := t.TempDir()
		if err = os.WriteFile(filepath.Join(barrier, "MF-PUBLISH.action"), []byte("block\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		commands := []mission.FinalizationCommand{command, command}
		if iteration%2 == 1 {
			commands[1].OperationID = ids.OperationID(fmt.Sprintf("operation-finalize-race-loser-%03d", iteration))
			commands[1].ProposedRevisionID = ids.WorkRevisionID(fmt.Sprintf("revision-race-loser-%03d", iteration))
		}
		processes := make([]*exec.Cmd, 2)
		resultPaths := make([]string, 2)
		for contender := range processes {
			resultPaths[contender] = filepath.Join(barrier, fmt.Sprintf("result-%d.json", contender))
			config := missionMethodRaceConfig{
				Database: database, Barrier: barrier, Participant: fmt.Sprintf("contender-%d", contender), ResultPath: resultPaths[contender],
				PlanRevisionID: "artifact-plan-dishonest-" + suffix,
				PlanDigest:     string(artifact.ContentCommitment([]byte("dishonest provider plan " + suffix))), Command: commands[contender],
			}
			configPath := filepath.Join(barrier, fmt.Sprintf("config-%d.json", contender))
			writeMissionMethodRaceJSON(t, configPath, config)
			processes[contender] = exec.Command(executable, "-test.run=^TestMissionMethodFinalizationProcessRaces100$", "-test.count=1")
			processes[contender].Env = append(os.Environ(), missionMethodRaceHelper+"="+configPath)
			if err = processes[contender].Start(); err != nil {
				t.Fatal(err)
			}
		}
		waitContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = waitForBoundaryReached(waitContext, barrier, "MF-PUBLISH", 2)
		cancel()
		if err != nil {
			t.Fatalf("race=%d deterministic barrier: %v", iteration, err)
		}
		if err = os.WriteFile(filepath.Join(barrier, "MF-PUBLISH.release"), []byte("release\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		results := make([]missionMethodRaceResult, 2)
		for contender, process := range processes {
			if waitErr := process.Wait(); waitErr != nil {
				t.Fatalf("race=%d contender=%d process: %v", iteration, contender, waitErr)
			}
			readMissionMethodRaceJSON(t, resultPaths[contender], &results[contender])
		}
		assertMissionMethodRaceOutcomes(t, iteration, results)
		assertMissionMethodRacePersistence(t, database, expected.MissionID)
	}
}

func runMissionMethodRaceHelper(t *testing.T, configPath string) {
	var config missionMethodRaceConfig
	readMissionMethodRaceJSON(t, configPath, &config)
	observation := missionMethodRaceObservation(t, config)
	store, err := sqlite.Open(context.Background(), config.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	controlled := missionMethodBarrierStore{Store: store, controller: faults.FileController{Directory: config.Barrier, Participant: config.Participant}}
	result, callErr := mission.NewFinalizationService(missionMethodStaticObservation{value: observation}, controlled).Finalize(context.Background(), config.Command)
	outcome := missionMethodRaceResult{OK: callErr == nil, Replayed: result.Replayed, RevisionID: result.Revision.ID().String()}
	if callErr != nil {
		var typed *mission.FinalizationError
		if errors.As(callErr, &typed) {
			outcome.Kind = string(typed.Kind)
		} else {
			outcome.Kind = "unexpected"
		}
	}
	writeMissionMethodRaceJSON(t, config.ResultPath, outcome)
}

type missionMethodStaticObservation struct {
	value workgraph.FinalizationObservation
}

func (source missionMethodStaticObservation) ObserveFinalization(context.Context, ids.MissionID) (workgraph.FinalizationObservation, error) {
	return source.value, nil
}

type missionMethodBarrierStore struct {
	*sqlite.Store
	controller faults.FileController
}

func (store missionMethodBarrierStore) PublishGenerationOne(ctx context.Context, commit mission.FinalizationCommit) (mission.FinalizationResult, error) {
	if err := store.controller.Reach(ctx, "MF-PUBLISH"); err != nil {
		return mission.FinalizationResult{}, err
	}
	return store.Store.PublishGenerationOne(ctx, commit)
}

func missionMethodRaceObservation(t *testing.T, config missionMethodRaceConfig) workgraph.FinalizationObservation {
	t.Helper()
	definitions := []tracker.WorkDefinition{
		dishonestMissionDefinition(t, "work-a", nil, "Define the race authority."),
		dishonestMissionDefinition(t, "work-b", []ids.WorkID{"work-a"}, "Consume the race authority."),
	}
	observations := make([]tracker.WorkDefinitionObservation, 0, len(definitions))
	for index, definition := range definitions {
		canonical := definition.CanonicalData()
		digest := sha256.Sum256(canonical)
		provider, err := tracker.NewProviderObservation(fmt.Sprintf("external-race-%d", index), canonical, tracker.Revision(index+1), fmt.Sprintf("%x", digest), fmt.Sprintf("race-token-%d", index), config.Command.OccurredAt.Add(-time.Second))
		if err != nil {
			t.Fatal(err)
		}
		reference := tracker.ExternalWorkRef{ProjectID: config.Command.ProjectID, WorkID: definition.WorkID(), Provider: "durable_fake", ExternalID: fmt.Sprintf("external-race-%d", index), MetadataSchema: tracker.WorkDefinitionMetadataSchemaV1, CreatedByOperation: ids.OperationID(fmt.Sprintf("operation-create-race-%d", index)), LastObservation: provider.Revision}
		observed, err := tracker.NewWorkDefinitionObservation(definition, reference, provider, provider.ObservedAt)
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, observed)
	}
	result, err := dishonestFinalizationObservation(config.Command.ProjectID, config.Command.MissionID, config.PlanRevisionID, config.PlanDigest, observations)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertMissionMethodRaceOutcomes(t *testing.T, iteration int, results []missionMethodRaceResult) {
	t.Helper()
	fresh, replayed, conflicts := 0, 0, 0
	for _, result := range results {
		switch {
		case result.OK && result.Replayed:
			replayed++
		case result.OK:
			fresh++
		case result.Kind == string(mission.FinalizationCurrentConflict):
			conflicts++
		default:
			t.Fatalf("race=%d unexpected bounded outcome=%+v", iteration, result)
		}
	}
	if iteration%2 == 0 {
		if fresh != 1 || replayed != 1 || conflicts != 0 {
			t.Fatalf("same-operation race=%d fresh=%d replayed=%d conflicts=%d", iteration, fresh, replayed, conflicts)
		}
		return
	}
	if fresh != 1 || replayed != 0 || conflicts != 1 {
		t.Fatalf("distinct-operation race=%d fresh=%d replayed=%d conflicts=%d", iteration, fresh, replayed, conflicts)
	}
}

func assertMissionMethodRacePersistence(t *testing.T, path string, missionID ids.MissionID) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	missionSnapshot, err := store.LoadMission(context.Background(), missionID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var pointer string
	var generation uint64
	var revisions, operations, events, outbox int
	if err = raw.QueryRow(`SELECT work_revision_id,generation,
		(SELECT COUNT(*) FROM phase3_work_revisions_v2 WHERE mission_id=?),
		(SELECT COUNT(*) FROM phase3_operations WHERE mission_id=? AND operation_kind='finalize_generation_one'),
		(SELECT COUNT(*) FROM phase3_events WHERE mission_id=? AND event_kind='work_generation_finalized'),
		(SELECT COUNT(*) FROM phase3_outbox o JOIN phase3_events e ON e.event_id=o.event_id WHERE e.mission_id=? AND e.event_kind='work_generation_finalized')
		FROM phase3_current_work_revisions WHERE mission_id=?`, missionID, missionID, missionID, missionID, missionID).Scan(&pointer, &generation, &revisions, &operations, &events, &outbox); err != nil {
		t.Fatal(err)
	}
	if pointer != missionSnapshot.CurrentWorkRevisionID.String() || generation != 1 || revisions != 1 || operations != 1 || events != 1 || outbox != 1 {
		t.Fatalf("restart audit mission=%s pointer=%s/%s generation=%d revisions=%d operations=%d events=%d outbox=%d", missionID, pointer, missionSnapshot.CurrentWorkRevisionID, generation, revisions, operations, events, outbox)
	}
	var encoded []byte
	if err = raw.QueryRow(`SELECT encoded_revision FROM phase3_work_revisions_v2 WHERE work_revision_id=?`, pointer).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.DecodeRevisionV2(encoded)
	if err != nil || revision.Generation() != 1 || len(revision.WorkItems()) != 2 || len(revision.ProviderObservations()) != 2 || len(revision.Lanes()) == 0 || revision.FinalizedBy() == "" || revision.GovernanceContextID() == "" {
		t.Fatalf("restart revision is partial: revision=%s generation=%d work=%d provider=%d lanes=%d err=%v", revision.ID(), revision.Generation(), len(revision.WorkItems()), len(revision.ProviderObservations()), len(revision.Lanes()), err)
	}
}

func writeMissionMethodRaceJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readMissionMethodRaceJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		t.Fatal(err)
	}
}
