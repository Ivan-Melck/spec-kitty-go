package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	workgraphreadmodel "github.com/spec-kitty/go-kitty3/internal/workgraph/readmodel"
)

func TestMissionMethodObservationRestoresExactCurrentGeneration(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	snapshot.CurrentStep = mission.StepWorkFinalized
	snapshot.CurrentWorkRevisionID = "revision-query-01"
	snapshot.Lifecycle = mission.LifecycleTerminal
	snapshot.TerminalOutcome = mission.OutcomeWorkFinalized
	old := testRevisionV2(t, "revision-query-01", 1, "", "old title", at)
	seedPhase3MissionWithCurrent(t, store, snapshot, old)

	first, err := store.ObserveMissionMethod(ctx, snapshot.MissionID)
	if err != nil || first.Mission.CurrentWorkRevisionID() != old.ID() || first.Workgraph.Generation() != 1 || first.Workgraph.DriftStatus() != workgraphreadmodel.DriftNotChecked {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	newRevision := testRevisionV2(t, "revision-query-02", 2, old.ID(), "new title", at.Add(time.Second))
	drift, _ := workgraph.DetectDrift(old, newRevision, at.Add(time.Second))
	plan, err := workgraph.ReconcileMixed(workgraph.MixedReconcileRequest{ID: "reconciliation-query", Old: old, New: newRevision, Drift: drift, Statuses: []workgraph.WorkStatusAssignment{{WorkID: "work-01", Status: workgraph.StatusActive}}, Decisions: []workgraph.WorkDispositionDecisionParams{{OldWorkID: "work-01", Disposition: workgraph.Invalidate, Rationale: "replace changed work"}}, ActorID: "actor-01", Rationale: "semantic change", OccurredAt: at.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.PublishSuccessorAtomically(ctx, mission.SuccessorPublicationCommit{MissionID: snapshot.MissionID, ProjectID: snapshot.ProjectID, ExpectedMission: snapshot, ExpectedCurrentGeneration: 1, ExpectedOld: old, Candidate: newRevision, Drift: drift, ReconciliationID: "reconciliation-query", Reconciliation: plan, OperationID: "operation-query-successor", ActorID: "actor-01", GovernanceContextID: "governance-01", Rationale: "semantic change", OccurredAt: at.Add(time.Second), RequestDigest: digest64("q")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ObserveMissionMethod(ctx, snapshot.MissionID)
	if err != nil || second.Mission.StateVersion() != result.Mission.StateVersion || second.Mission.CurrentWorkRevisionID() != newRevision.ID() || second.Workgraph.Generation() != 2 || second.Workgraph.DriftStatus() != workgraphreadmodel.DriftChanged || len(second.Workgraph.Reconciliations()) != 1 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func TestMissionMethodObservationRejectsCrossGenerationCorruption(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	snapshot.CurrentStep, snapshot.CurrentWorkRevisionID, snapshot.Lifecycle, snapshot.TerminalOutcome = mission.StepWorkFinalized, "revision-query-corrupt", mission.LifecycleTerminal, mission.OutcomeWorkFinalized
	revision := testRevisionV2(t, snapshot.CurrentWorkRevisionID, 1, "", "title", at)
	seedPhase3MissionWithCurrent(t, store, snapshot, revision)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, `DROP TRIGGER immutable_phase3_work_revision_update`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, `UPDATE phase3_work_revisions_v2 SET generation=2 WHERE work_revision_id=?`, revision.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ObserveMissionMethod(ctx, snapshot.MissionID); err == nil {
		t.Fatal("cross-generation observation succeeded")
	}
}
