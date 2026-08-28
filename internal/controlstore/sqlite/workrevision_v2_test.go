package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestGenerationOneAtomicReplayAndExactReceipt(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	seedPhase3Mission(t, store, snapshot)
	revision := testRevisionV2(t, "revision-v2-01", 1, "", "title one", at.Add(time.Second))
	commit := mission.FinalizationCommit{ExpectedMission: snapshot, ExpectedCurrentGeneration: 0, OperationID: "operation-finalize-01", RequestDigest: digest64("d"), ExpectedReceipt: mission.FinalizationReceipt(digest64("e")), Candidate: revision, ActorID: "actor-01", GovernanceContextID: "governance-01", OccurredAt: at.Add(time.Second)}
	first, err := store.PublishGenerationOne(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.PublishGenerationOne(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || first.Receipt != commit.ExpectedReceipt || first.Mission.CurrentWorkRevisionID != revision.ID() {
		t.Fatalf("results %#v %#v", first, second)
	}
	changed := commit
	changed.RequestDigest = digest64("f")
	if _, err = store.PublishGenerationOne(ctx, changed); err == nil {
		t.Fatal("changed operation replay succeeded")
	}
}

func TestGenerationOneLateBoundaryFaultRollsBackMissionPointerAndRevision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	seedPhase3Mission(t, store, snapshot)
	revision := testRevisionV2(t, "revision-fault", 1, "", "fault title", at.Add(time.Second))
	if _, err = store.db.Exec(`CREATE TRIGGER fault_finalization_operation BEFORE INSERT ON phase3_operations WHEN NEW.operation_kind='finalize_generation_one' BEGIN SELECT RAISE(ABORT,'seeded late fault'); END`); err != nil {
		t.Fatal(err)
	}
	commit := mission.FinalizationCommit{ExpectedMission: snapshot, OperationID: "operation-finalize-fault", RequestDigest: digest64("4"), ExpectedReceipt: mission.FinalizationReceipt(digest64("5")), Candidate: revision, ActorID: "actor-01", GovernanceContextID: "governance-01", OccurredAt: at.Add(time.Second)}
	if _, err = store.PublishGenerationOne(ctx, commit); err == nil {
		t.Fatal("late boundary fault was hidden")
	}
	loaded, err := store.LoadMission(ctx, snapshot.MissionID)
	if err != nil || !sameSnapshot(loaded, snapshot) {
		t.Fatalf("mission advanced after fault: %#v err=%v", loaded, err)
	}
	var revisions, pointers, operations int
	if err = store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM phase3_work_revisions_v2),(SELECT COUNT(*) FROM phase3_current_work_revisions),(SELECT COUNT(*) FROM phase3_operations)`).Scan(&revisions, &pointers, &operations); err != nil || revisions+pointers+operations != 0 {
		t.Fatalf("partial authority revisions=%d pointers=%d operations=%d err=%v", revisions, pointers, operations, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err = store.LoadMission(ctx, snapshot.MissionID)
	if err != nil || !sameSnapshot(loaded, snapshot) {
		t.Fatalf("reopened mission advanced: %#v err=%v", loaded, err)
	}
}

func TestCurrentWorkRevisionPointerBindsMissionRevisionAndGeneration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	revision := testRevisionV2(t, "revision-pointer-01", 1, "", "pointer title", at)
	revisionBytes, _ := revision.Encode()
	snapshot.CurrentWorkRevisionID = revision.ID()
	snapshot.Lifecycle = mission.LifecycleTerminal
	snapshot.CurrentStep = mission.StepWorkFinalized
	snapshot.TerminalOutcome = mission.OutcomeWorkFinalized
	snapshotBytes, _ := encodeSnapshot(snapshot)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO phase3_work_revisions_v2(work_revision_id,mission_id,project_id,generation,predecessor_id,graph_digest,criteria_digest,encoded_revision) VALUES(?,?,?,?,?,?,?,?)`, revision.ID(), revision.MissionID(), revision.ProjectID(), revision.Generation(), nil, revision.GraphDigest(), revision.CriteriaDigest(), revisionBytes); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO phase3_missions(mission_id,project_id,definition_id,definition_version,definition_commitment,lifecycle,current_step,state_version,created_at,updated_at,snapshot_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, snapshot.MissionID, snapshot.ProjectID, snapshot.DefinitionID, snapshot.DefinitionVersion, snapshot.DefinitionCommitment, snapshot.Lifecycle, snapshot.CurrentStep, snapshot.StateVersion, formatTime(snapshot.CreatedAt), formatTime(snapshot.UpdatedAt), snapshotBytes); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO phase3_current_work_revisions(mission_id,work_revision_id,generation) VALUES(?,?,?)`, snapshot.MissionID, revision.ID(), revision.Generation()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	foreign := testRevisionV2(t, "revision-pointer-foreign", 2, revision.ID(), "foreign title", at.Add(time.Second))
	foreignBytes, _ := foreign.Encode()
	// A directly inserted revision with another mission is enough to attack the
	// pointer FK without relying on domain constructors to create bad state.
	if _, err = store.db.Exec(`INSERT INTO phase3_work_revisions_v2(work_revision_id,mission_id,project_id,generation,predecessor_id,graph_digest,criteria_digest,encoded_revision) VALUES(?,?,?,?,?,?,?,?)`, foreign.ID(), "mission-foreign", foreign.ProjectID(), foreign.Generation(), revision.ID(), foreign.GraphDigest(), foreign.CriteriaDigest(), foreignBytes); err != nil {
		t.Fatal(err)
	}
	mutants := []string{
		`UPDATE phase3_current_work_revisions SET generation=2 WHERE mission_id='mission-01'`,
		`UPDATE phase3_current_work_revisions SET work_revision_id='revision-pointer-foreign',generation=2 WHERE mission_id='mission-01'`,
		`UPDATE phase3_missions SET snapshot_json=json_set(snapshot_json,'$.CurrentWorkRevisionID','revision-pointer-foreign') WHERE mission_id='mission-01'`,
		`UPDATE phase3_missions SET snapshot_json=json_set(snapshot_json,'$.CurrentWorkRevisionID','revision-missing') WHERE mission_id='mission-01'`,
		`UPDATE phase3_missions SET snapshot_json=json_set(snapshot_json,'$.CurrentWorkRevisionID','') WHERE mission_id='mission-01'`,
		`DELETE FROM phase3_current_work_revisions WHERE mission_id='mission-01'`,
	}
	for _, statement := range mutants {
		if _, err = store.db.Exec(statement); err == nil {
			t.Fatalf("pointer mutant succeeded: %s", statement)
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
	var missionID, revisionID string
	var generation uint64
	if err = store.db.QueryRow(`SELECT mission_id,work_revision_id,generation FROM phase3_current_work_revisions WHERE mission_id='mission-01'`).Scan(&missionID, &revisionID, &generation); err != nil || missionID != snapshot.MissionID.String() || revisionID != revision.ID().String() || generation != 1 {
		t.Fatalf("reopened pointer mission=%q revision=%q generation=%d err=%v", missionID, revisionID, generation, err)
	}
}

func TestCurrentWorkRevisionPointerAndMissionSnapshotAreBothPresentOrAbsent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Date(2026, 8, 28, 10, 30, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	seedPhase3Mission(t, store, snapshot)
	revision := testRevisionV2(t, "revision-pointer-one-sided", 1, "", "one-sided title", at)
	revisionBytes, _ := revision.Encode()
	if _, err = store.db.Exec(`INSERT INTO phase3_work_revisions_v2(work_revision_id,mission_id,project_id,generation,predecessor_id,graph_digest,criteria_digest,encoded_revision) VALUES(?,?,?,?,?,?,?,?)`, revision.ID(), revision.MissionID(), revision.ProjectID(), revision.Generation(), nil, revision.GraphDigest(), revision.CriteriaDigest(), revisionBytes); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO phase3_current_work_revisions(mission_id,work_revision_id,generation) VALUES(?,?,1)`, snapshot.MissionID, revision.ID()); err == nil {
		t.Fatal("pointer row existed while mission snapshot had no current revision")
	}
	if _, err = store.db.Exec(`UPDATE phase3_missions SET snapshot_json=json_set(snapshot_json,'$.CurrentWorkRevisionID',?) WHERE mission_id=?`, revision.ID(), snapshot.MissionID); err == nil {
		t.Fatal("mission snapshot named a current revision without a pointer row")
	}
	var missionPointer any
	var pointerCount int
	if err = store.db.QueryRow(`SELECT current_work_revision_id,(SELECT COUNT(*) FROM phase3_current_work_revisions) FROM phase3_missions WHERE mission_id=?`, snapshot.MissionID).Scan(&missionPointer, &pointerCount); err != nil || missionPointer != nil || pointerCount != 0 {
		t.Fatalf("one-sided attempt persisted pointer=%v rows=%d err=%v", missionPointer, pointerCount, err)
	}
}

func TestGenerationOneHundredCallerTwoStoreRaceLeavesExactPointerPair(t *testing.T) {
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
	at := time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	seedPhase3Mission(t, left, snapshot)
	const callers = 100
	commits := make([]mission.FinalizationCommit, callers)
	for index := range callers {
		revisionID := ids.WorkRevisionID(fmt.Sprintf("revision-race-%03d", index))
		candidate := testRevisionV2(t, revisionID, 1, "", fmt.Sprintf("race title %03d", index), at.Add(time.Duration(index+1)*time.Second))
		commits[index] = mission.FinalizationCommit{ExpectedMission: snapshot, ExpectedCurrentGeneration: 0, OperationID: ids.OperationID(fmt.Sprintf("operation-race-%03d", index)), RequestDigest: digest64(fmt.Sprintf("%x", index%16)), ExpectedReceipt: mission.FinalizationReceipt(digest64(fmt.Sprintf("%x", (index+1)%16))), Candidate: candidate, ActorID: "actor-01", GovernanceContextID: "governance-01", OccurredAt: at.Add(time.Duration(index+1) * time.Second)}
	}
	start := make(chan struct{})
	results := make(chan error, callers)
	var wait sync.WaitGroup
	for index := range callers {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			store := left
			if index%2 == 1 {
				store = right
			}
			_, publishErr := store.PublishGenerationOne(ctx, commits[index])
			results <- publishErr
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	winners := 0
	for publishErr := range results {
		if publishErr == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("generation-one winners=%d", winners)
	}
	var missionPointer, rowPointer string
	var generation uint64
	var revisions, events, outbox int
	if err = left.db.QueryRow(`SELECT m.current_work_revision_id,p.work_revision_id,p.generation,
		(SELECT COUNT(*) FROM phase3_work_revisions_v2),(SELECT COUNT(*) FROM phase3_events WHERE event_kind='work_generation_finalized'),(SELECT COUNT(*) FROM phase3_outbox)
		FROM phase3_missions m JOIN phase3_current_work_revisions p ON p.mission_id=m.mission_id WHERE m.mission_id=?`, snapshot.MissionID).Scan(&missionPointer, &rowPointer, &generation, &revisions, &events, &outbox); err != nil || missionPointer != rowPointer || generation != 1 || revisions != 1 || events != 1 || outbox != 1 {
		t.Fatalf("mission=%q row=%q generation=%d revisions=%d events=%d outbox=%d err=%v", missionPointer, rowPointer, generation, revisions, events, outbox, err)
	}
	if err = left.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.db.QueryRow(`PRAGMA foreign_key_check`).Scan(new(string), new(int64), new(string), new(int64)); err != sql.ErrNoRows {
		t.Fatalf("reopened foreign-key violation: %v", err)
	}
}

func phase3MissionSnapshot(at time.Time) mission.MissionSnapshot {
	return mission.MissionSnapshot{MissionID: "mission-01", ProjectID: "project-01", DefinitionID: "software-development", DefinitionVersion: 1, DefinitionCommitment: mission.DefinitionCommitment(digest64("a")), Lifecycle: mission.LifecycleOpen, CurrentStep: mission.StepWorkDraftReady, StateVersion: 1, CreatedAt: at, UpdatedAt: at}
}
func seedPhase3Mission(t *testing.T, s *Store, snapshot mission.MissionSnapshot) {
	t.Helper()
	raw, err := encodeSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO phase3_missions(mission_id,project_id,definition_id,definition_version,definition_commitment,lifecycle,current_step,state_version,created_at,updated_at,snapshot_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, snapshot.MissionID, snapshot.ProjectID, snapshot.DefinitionID, snapshot.DefinitionVersion, snapshot.DefinitionCommitment, snapshot.Lifecycle, snapshot.CurrentStep, snapshot.StateVersion, formatTime(snapshot.CreatedAt), formatTime(snapshot.UpdatedAt), raw)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPhase3MissionWithCurrent(t *testing.T, s *Store, snapshot mission.MissionSnapshot, revision workgraph.RevisionV2) {
	t.Helper()
	snapshotBytes, err := encodeSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	revisionBytes, err := revision.Encode()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO phase3_work_revisions_v2(work_revision_id,mission_id,project_id,generation,predecessor_id,graph_digest,criteria_digest,encoded_revision) VALUES(?,?,?,?,?,?,?,?)`, revision.ID(), revision.MissionID(), revision.ProjectID(), revision.Generation(), nullString(revision.PredecessorID().String()), revision.GraphDigest(), revision.CriteriaDigest(), revisionBytes); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO phase3_missions(mission_id,project_id,definition_id,definition_version,definition_commitment,lifecycle,current_step,state_version,created_at,updated_at,snapshot_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, snapshot.MissionID, snapshot.ProjectID, snapshot.DefinitionID, snapshot.DefinitionVersion, snapshot.DefinitionCommitment, snapshot.Lifecycle, snapshot.CurrentStep, snapshot.StateVersion, formatTime(snapshot.CreatedAt), formatTime(snapshot.UpdatedAt), snapshotBytes); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO phase3_current_work_revisions(mission_id,work_revision_id,generation) VALUES(?,?,?)`, snapshot.MissionID, revision.ID(), revision.Generation()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func testRevisionV2(t *testing.T, id ids.WorkRevisionID, generation uint64, predecessor ids.WorkRevisionID, title string, at time.Time) workgraph.RevisionV2 {
	t.Helper()
	work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: "project-01", WorkID: "work-01", Title: title, Description: "bounded description", DesignContext: "bounded design", Criteria: []workgraph.CriterionObservation{{ID: "criterion-01", Text: "criterion text", Outcome: "verified", Verifier: "test", Requirement: workgraph.CriterionRequired}}, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/mission", Domain: "mission", Access: workgraph.ScopeWriteExclusive, SerializationKey: "mission"}}, Provenance: []workgraph.ScopeReference{{ID: "scope-01", Kind: workgraph.ScopeRequiredOutcome}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workgraph.NewDraftObservation("project-01", "mission-01", "plan-01", digest64("b"), []workgraph.ApprovedScope{{ID: "scope-01", Kind: workgraph.ScopeRequiredOutcome, MissionID: "mission-01"}}, []workgraph.WorkObservation{work})
	if err != nil {
		t.Fatal(err)
	}
	lanes, err := workgraph.CollapseLanes(draft)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.NewRevisionV2(workgraph.RevisionV2Params{ID: id, ProjectID: "project-01", MissionID: "mission-01", Generation: generation, PredecessorID: predecessor, PlanningSourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: digest64("a")}, DependencyPolicy: workgraph.DependencyApprovedOrDone, WorkItems: []workgraph.WorkItemV2Params{{WorkID: "work-01", Title: title, Description: "bounded description", DesignContext: "bounded design", Criteria: []workgraph.CriterionV2Params{{ID: "criterion-01", Text: "criterion text", VerifierKind: "test", Required: true, Outcome: "verified", EvidenceHint: "test evidence"}}, WriteScopes: []workgraph.NormalizedWriteScope{{Path: "internal/mission", Domain: "mission", Access: workgraph.ScopeWriteExclusive, SerializationKey: "mission"}}}}, Lanes: lanes, ProviderObservations: []workgraph.ProviderObservationV2{{WorkID: "work-01", Provider: "fake", ExternalID: "external-01", Revision: "provider-revision-01"}}, FinalizedBy: "actor-01", FinalizedAt: at, GovernanceContextID: "governance-01"})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
