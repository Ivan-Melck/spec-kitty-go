package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

func TestMissionActionPersistsExactOutcomeAndReceiptBeforeMutableReads(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	snapshot.CurrentStep = mission.StepInitialized
	seedPhase3Mission(t, store, snapshot)
	governance := testMissionActionGovernance(t, store, snapshot.MissionID, at)
	after := snapshot
	after.CurrentStep = mission.StepSpecifying
	after.StateVersion++
	after.UpdatedAt = at.Add(time.Second)
	commit := mission.ActionCommit{OperationID: "operation-action-01", MissionID: snapshot.MissionID, Expected: snapshot, ActionKey: "mission.specify", Successor: mission.StepSpecifying, GovernanceAction: "mission.specify", ActorID: "actor-01", ProcessID: "process-01", Governance: governance, RequestDigest: digest64("8"), Outcome: mission.ActionOutcome{Mission: after}, Receipt: mission.ActionReceipt(digest64("9")), OccurredAt: at.Add(time.Second)}
	first, err := store.CommitAction(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	rawBefore := append([]byte(nil), mustOperationResult(t, store, commit.OperationID)...)
	if _, err = store.db.Exec(`UPDATE phase3_missions SET updated_at=?`, formatTime(at.Add(10*time.Second))); err != nil {
		t.Fatal(err)
	}
	replayed, found, err := store.ReplayAction(ctx, snapshot.MissionID, commit.OperationID, commit.RequestDigest)
	if err != nil || !found {
		t.Fatalf("replay=%#v found=%v err=%v", replayed, found, err)
	}
	if replayed.Receipt != first.Receipt || !sameSnapshot(replayed.Outcome.Mission, after) || string(rawBefore) != string(mustOperationResult(t, store, commit.OperationID)) {
		t.Fatal("action replay was reconstructed from mutable state")
	}
}

func testMissionActionGovernance(t *testing.T, store *Store, missionID ids.MissionID, at time.Time) governancecontext.MissionAction {
	t.Helper()
	commitment, err := governancecontext.NewCommitment(governancecontext.AlgorithmSHA256, digest64("1"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := []governancecontext.EntryKind{governancecontext.KindConfig, governancecontext.KindDoctrine, governancecontext.KindProfile, governancecontext.KindMissionType}
	entries := make([]governancecontext.Entry, len(kinds))
	for i, k := range kinds {
		entries[i], err = governancecontext.NewEntry(k, string(k)+"-ref", "revision-01", commitment)
		if err != nil {
			t.Fatal(err)
		}
	}
	source, err := governancecontext.NewContext("governance-01", governancecontext.SchemaVersion, entries, at)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveGovernanceContextTx(context.Background(), tx, source, "operation-governance-01", at); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	value, err := governancecontext.NewMissionAction(governancecontext.MissionActionParams{Source: source, MissionID: missionID, StepKey: string(mission.StepInitialized), ActionKey: "mission.specify", ActorID: "actor-01", SelectedEntries: entries})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func mustOperationResult(t *testing.T, s *Store, op ids.OperationID) []byte {
	t.Helper()
	var raw []byte
	if err := s.db.QueryRow(`SELECT result_json FROM phase3_operations WHERE operation_id=?`, op).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}
