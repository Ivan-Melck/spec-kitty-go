package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestSuccessorPublicationPersistsCompleteEligibility(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Date(2026, 8, 28, 3, 0, 0, 0, time.UTC)
	snapshot := phase3MissionSnapshot(at)
	snapshot.CurrentStep = mission.StepWorkFinalized
	snapshot.CurrentWorkRevisionID = "revision-v2-01"
	snapshot.Lifecycle = mission.LifecycleTerminal
	snapshot.TerminalOutcome = mission.OutcomeWorkFinalized
	old := testRevisionV2(t, "revision-v2-01", 1, "", "old title", at)
	newRevision := testRevisionV2(t, "revision-v2-02", 2, old.ID(), "new title", at.Add(time.Second))
	seedPhase3MissionWithCurrent(t, store, snapshot, old)
	drift, err := workgraph.DetectDrift(old, newRevision, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := workgraph.ReconcileMixed(workgraph.MixedReconcileRequest{ID: "reconciliation-01", Old: old, New: newRevision, Drift: drift, Statuses: []workgraph.WorkStatusAssignment{{WorkID: "work-01", Status: workgraph.StatusActive}}, Decisions: []workgraph.WorkDispositionDecisionParams{{OldWorkID: "work-01", Disposition: workgraph.Invalidate, Rationale: "replace changed work"}}, ActorID: "actor-01", Rationale: "semantic change", OccurredAt: at.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	commit := mission.SuccessorPublicationCommit{MissionID: snapshot.MissionID, ProjectID: snapshot.ProjectID, ExpectedMission: snapshot, ExpectedCurrentGeneration: 1, ExpectedOld: old, Candidate: newRevision, Drift: drift, ReconciliationID: "reconciliation-01", Reconciliation: plan, OperationID: "operation-successor-01", ActorID: "actor-01", GovernanceContextID: "governance-01", Rationale: "semantic change", OccurredAt: at.Add(time.Second), RequestDigest: digest64("7")}
	result, err := store.PublishSuccessorAtomically(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision.ID() != newRevision.ID() {
		t.Fatal("successor missing")
	}
	fact, err := store.WorkEligibilityFact(ctx, old.ID(), "work-01", workgraph.StatusActive, workgraph.EligibilityHandoff)
	if err != nil || !fact.Complete() || fact.NewRevisionID() != newRevision.ID() || fact.Permitted() {
		t.Fatalf("fact=%#v err=%v", fact, err)
	}
	if _, err = store.db.Exec(`DROP TRIGGER immutable_phase3_eligibility_update`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE phase3_eligibility_facts SET new_work_revision_id='revision-missing' WHERE reconciliation_id='reconciliation-01'`); err == nil {
		t.Fatal("eligibility revision substitution succeeded")
	}
	if _, err = store.db.Exec(`UPDATE phase3_eligibility_facts SET disposition='continue' WHERE reconciliation_id='reconciliation-01' AND work_status='active' AND operation='handoff'`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.WorkEligibilityFact(ctx, old.ID(), "work-01", workgraph.StatusActive, workgraph.EligibilityHandoff); err == nil {
		t.Fatal("substituted disposition restored as authority")
	}
	if _, err = store.db.Exec(`UPDATE phase3_eligibility_facts SET disposition='invalidate',work_status='approved',operation='review' WHERE reconciliation_id='reconciliation-01' AND work_status='active' AND operation='handoff'`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.WorkEligibilityFact(ctx, old.ID(), "work-01", workgraph.StatusApproved, workgraph.EligibilityReview); err == nil {
		t.Fatal("substituted status and operation restored as authority")
	}
	if _, err = store.db.Exec(`UPDATE phase3_eligibility_facts SET work_status='active',operation='handoff' WHERE reconciliation_id='reconciliation-01' AND work_status='approved' AND operation='review'`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DROP TRIGGER immutable_phase3_reconciliation_update`); err != nil {
		t.Fatal(err)
	}
	var canonicalMixed []byte
	if err = store.db.QueryRow(`SELECT encoded_reconciliation FROM phase3_mixed_reconciliations WHERE reconciliation_id='reconciliation-01'`).Scan(&canonicalMixed); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE phase3_mixed_reconciliations SET encoded_reconciliation='{}' WHERE reconciliation_id='reconciliation-01'`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.WorkEligibilityFact(ctx, old.ID(), "work-01", workgraph.StatusActive, workgraph.EligibilityHandoff); err == nil {
		t.Fatal("substituted reconciliation header restored as authority")
	}
	if _, err = store.db.Exec(`UPDATE phase3_mixed_reconciliations SET encoded_reconciliation=? WHERE reconciliation_id='reconciliation-01'`, canonicalMixed); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DROP TRIGGER immutable_phase3_work_revision_update`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE phase3_work_revisions_v2 SET encoded_revision='{}' WHERE work_revision_id=?`, newRevision.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.WorkEligibilityFact(ctx, old.ID(), "work-01", workgraph.StatusActive, workgraph.EligibilityHandoff); err == nil {
		t.Fatal("substituted successor revision restored as authority")
	}
}
