package scenario

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

var missionMethodFaultManifest = []string{
	"MM-ARTIFACT-APPROVAL-TX-ABORT",
	"MM-ARTIFACT-POSTCOMMIT-RESPONSE",
	"MM-DECOMPOSITION-APPLY-LOST-RESPONSE",
	"MM-FINALIZATION-PREWRITE-PROVIDER",
	"MM-FINALIZATION-TX-ABORT",
	"MM-FINALIZATION-POSTCOMMIT-RESPONSE",
	"MM-RECONCILIATION-DRIFT-TX-ABORT",
	"MM-RECONCILIATION-LATE-TX-ABORT",
	"MM-RECONCILIATION-POSTCOMMIT-RESPONSE",
}

const missionMethodFaultHelper = "SPEC_KITTY_MISSION_METHOD_FAULT_HELPER"

type missionMethodFaultHelperConfig struct {
	Kind            string                  `json:"kind"`
	Database        string                  `json:"database"`
	Barrier         string                  `json:"barrier"`
	Finalization    missionMethodRaceConfig `json:"finalization"`
	Approval        artifact.ApproveCommand `json:"approval"`
	SuccessorSuffix string                  `json:"successor_suffix"`
}

func TestMissionMethodCrashBoundaryCensus(t *testing.T) {
	if configPath := os.Getenv(missionMethodFaultHelper); configPath != "" {
		runMissionMethodFaultHelper(t, configPath)
		return
	}
	cases := map[string]func(*testing.T){
		"MM-ARTIFACT-APPROVAL-TX-ABORT":         missionMethodArtifactApprovalFault,
		"MM-ARTIFACT-POSTCOMMIT-RESPONSE":       missionMethodArtifactPostCommitFault,
		"MM-DECOMPOSITION-APPLY-LOST-RESPONSE":  missionMethodProviderSagaFault,
		"MM-FINALIZATION-PREWRITE-PROVIDER":     missionMethodFinalizationPrewriteFault,
		"MM-FINALIZATION-TX-ABORT":              missionMethodFinalizationFault,
		"MM-FINALIZATION-POSTCOMMIT-RESPONSE":   missionMethodFinalizationPostCommitFault,
		"MM-RECONCILIATION-DRIFT-TX-ABORT":      func(t *testing.T) { missionMethodReconciliationFault(t, "phase3_drift_reports") },
		"MM-RECONCILIATION-LATE-TX-ABORT":       func(t *testing.T) { missionMethodReconciliationFault(t, "phase3_operations") },
		"MM-RECONCILIATION-POSTCOMMIT-RESPONSE": missionMethodReconciliationPostCommitFault,
	}
	started := make(map[string]bool, len(cases))
	terminal := make(map[string]string, len(cases))
	for _, faultID := range missionMethodFaultManifest {
		run, exists := cases[faultID]
		if !exists {
			t.Fatalf("fault manifest entry %s is unreachable", faultID)
		}
		started[faultID] = true
		passed := t.Run(faultID, run)
		if passed {
			terminal[faultID] = "pass"
		} else {
			terminal[faultID] = "fail"
		}
	}
	if len(started) != len(missionMethodFaultManifest) || len(terminal) != len(missionMethodFaultManifest) {
		t.Fatalf("fault census incomplete expected=%d started=%d terminal=%d", len(missionMethodFaultManifest), len(started), len(terminal))
	}
	for _, faultID := range missionMethodFaultManifest {
		if terminal[faultID] != "pass" {
			t.Fatalf("fault %s terminal=%s", faultID, terminal[faultID])
		}
	}
	mutant := append([]string(nil), missionMethodFaultManifest[1:]...)
	if reflect.DeepEqual(mutant, missionMethodFaultManifest) || len(mutant) == len(cases) {
		t.Fatal("deleted fault-census mutant survived")
	}
}

func missionMethodArtifactApprovalFault(t *testing.T) {
	fixture := runPortableMissionFixture(t, "fault-artifact")
	at := fixture.MissionTwo.UpdatedAt.Add(time.Minute)
	policy, err := artifact.PolicyFor(artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	published := callMethod(t, context.Background(), "artifact", "publish", map[string]any{
		"database": fixture.Database, "correlation_id": "correlation-fault-artifact-publish",
		"operation_id": "operation-fault-artifact-publish", "artifact_revision_id": "artifact-plan-fault-successor",
		"mission_id": fixture.MissionTwo.MissionID, "kind": artifact.KindPlan, "source_ref": "plan.md", "source_revision": strings.Repeat("c", 40),
		"created_by": "actor-author", "created_at": at, "content": []byte("fault-qualified successor plan"),
		"present_concerns": policy.RequiredConcerns(), "validated_at": at, "expected_latest_revision_id": fixture.PlanPublic.ID,
	})
	var publication struct {
		Revision artifactRevisionView `json:"revision"`
	}
	decodeMethodResult(t, published, &publication)
	installMissionMethodTrigger(t, fixture.Database, `CREATE TRIGGER mission_fault_approval BEFORE INSERT ON phase3_artifact_approvals BEGIN SELECT RAISE(ABORT,'mission fault'); END`)
	before := missionMethodAuthorityCounts(t, fixture.Database)
	request := map[string]any{
		"database": fixture.Database, "correlation_id": "correlation-fault-artifact-approve",
		"operation_id": "operation-fault-artifact-approve", "approval_id": "approval-fault-artifact",
		"artifact_revision_id": publication.Revision.ID, "verdict": artifact.VerdictApproved, "actor_id": "actor-reviewer",
		"evidence_digest": artifact.ContentCommitment([]byte("fault approval")), "governance_context_id": "governance-fault-artifact", "submitted_at": at.Add(time.Second),
	}
	failed := callMethodRaw(t, context.Background(), "artifact", "approve", request)
	if failed.OK || failed.Error == nil || missionMethodAuthorityCounts(t, fixture.Database) != before {
		t.Fatalf("artifact approval boundary failed=%+v before=%+v after=%+v", failed, before, missionMethodAuthorityCounts(t, fixture.Database))
	}
	dropMissionMethodTrigger(t, fixture.Database, "mission_fault_approval")
	succeeded := callMethod(t, context.Background(), "artifact", "approve", request)
	replayed := callMethod(t, context.Background(), "artifact", "approve", request)
	if !succeeded.OK || !replayed.OK || missionMethodAuthorityCounts(t, fixture.Database).Operations != before.Operations+1 {
		t.Fatalf("artifact approval recovery fresh=%+v replay=%+v", succeeded, replayed)
	}
}

func missionMethodProviderSagaFault(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestBeadsDishonestCatalogCreateApplyTimeoutObservedBeforeRetry$", "-test.count=1")
	command.Env = os.Environ()
	if output, runErr := command.CombinedOutput(); runErr != nil {
		t.Fatalf("provider apply/lost-response recovery probe failed: %v output_bytes=%d", runErr, len(output))
	}
}

type missionMethodFailingObservation struct{}

func (missionMethodFailingObservation) ObserveFinalization(context.Context, ids.MissionID) (workgraph.FinalizationObservation, error) {
	return workgraph.FinalizationObservation{}, fmt.Errorf("bounded provider unavailable")
}

func missionMethodFinalizationPrewriteFault(t *testing.T) {
	database, expected, command := prepareDishonestFinalizationMission(t, "fault-prewrite")
	before := qualificationPersistenceSnapshotWithoutPointer(t, database, expected.MissionID)
	store, err := sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mission.NewFinalizationService(missionMethodFailingObservation{}, store).Finalize(context.Background(), command)
	store.Close()
	if err == nil || !reflect.DeepEqual(before, qualificationPersistenceSnapshotWithoutPointer(t, database, expected.MissionID)) {
		t.Fatalf("pre-write provider refusal err=%v", err)
	}
}

func missionMethodArtifactPostCommitFault(t *testing.T) {
	fixture := runPortableMissionFixture(t, "fault-artifact-post")
	at := fixture.MissionTwo.UpdatedAt.Add(time.Minute)
	policy, err := artifact.PolicyFor(artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	concerns := policy.RequiredConcerns()
	if concerns == nil {
		concerns = []artifact.Concern{}
	}
	published := callMethod(t, context.Background(), "artifact", "publish", map[string]any{
		"database": fixture.Database, "correlation_id": "correlation-post-artifact-publish",
		"operation_id": "operation-post-artifact-publish", "artifact_revision_id": "artifact-plan-post-successor",
		"mission_id": fixture.MissionTwo.MissionID, "kind": artifact.KindPlan, "source_ref": "plan.md", "source_revision": strings.Repeat("d", 40),
		"created_by": "actor-author", "created_at": at, "content": []byte("post-commit successor plan"),
		"present_concerns": concerns, "validated_at": at, "expected_latest_revision_id": fixture.PlanPublic.ID,
	})
	var publication struct {
		Revision artifactRevisionView `json:"revision"`
	}
	decodeMethodResult(t, published, &publication)
	command := artifact.ApproveCommand{OperationID: "operation-post-artifact-approve", ApprovalID: "approval-post-artifact", ArtifactRevisionID: publication.Revision.ID, Verdict: artifact.VerdictApproved, ActorID: "actor-reviewer", EvidenceDigest: string(artifact.ContentCommitment([]byte("post approval"))), GovernanceContextID: "governance-fault-artifact-post", SubmittedAt: at.Add(time.Second)}
	missionMethodKillAfterCommit(t, missionMethodFaultHelperConfig{Kind: "artifact", Database: fixture.Database, Approval: command})
	store, err := sqlite.Open(context.Background(), fixture.Database)
	if err != nil {
		t.Fatal(err)
	}
	service, err := artifact.NewService(store)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	replay, err := service.Approve(context.Background(), command)
	store.Close()
	if err != nil || !replay.Replayed {
		t.Fatalf("artifact lost-response replay=%+v err=%v", replay, err)
	}
}

func missionMethodFinalizationPostCommitFault(t *testing.T) {
	database, _, command := prepareDishonestFinalizationMission(t, "fault-finalization-post")
	config := missionMethodRaceConfig{Database: database, Command: command, PlanRevisionID: "artifact-plan-dishonest-fault-finalization-post", PlanDigest: string(artifact.ContentCommitment([]byte("dishonest provider plan fault-finalization-post")))}
	missionMethodKillAfterCommit(t, missionMethodFaultHelperConfig{Kind: "finalization", Database: database, Finalization: config})
	store, err := sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	observation := missionMethodRaceObservation(t, config)
	replay, err := mission.NewFinalizationService(missionMethodStaticObservation{value: observation}, store).Finalize(context.Background(), command)
	store.Close()
	if err != nil || !replay.Replayed || replay.Mission.CurrentWorkRevisionID != command.ProposedRevisionID {
		t.Fatalf("finalization lost-response replay=%+v err=%v", replay, err)
	}
}

func missionMethodReconciliationPostCommitFault(t *testing.T) {
	database, _, finalization := prepareDishonestFinalizationMission(t, "fault-reconciliation-post")
	config := missionMethodRaceConfig{Database: database, Command: finalization, PlanRevisionID: "artifact-plan-dishonest-fault-reconciliation-post", PlanDigest: string(artifact.ContentCommitment([]byte("dishonest provider plan fault-reconciliation-post")))}
	store, err := sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	observation := missionMethodRaceObservation(t, config)
	finalized, err := mission.NewFinalizationService(missionMethodStaticObservation{value: observation}, store).Finalize(context.Background(), finalization)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	command := missionMethodSuccessorCommand(t, finalized, "postcommit")
	store.Close()
	missionMethodKillAfterCommit(t, missionMethodFaultHelperConfig{Kind: "successor", Database: database, SuccessorSuffix: "postcommit"})
	store, err = sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := mission.NewSuccessorPublicationService(store).Publish(context.Background(), command)
	store.Close()
	if err != nil || !replay.Replayed || replay.Mission.CurrentWorkRevisionID != command.Candidate.ID() {
		t.Fatalf("successor lost-response replay=%+v err=%v", replay, err)
	}
}

func missionMethodKillAfterCommit(t *testing.T, config missionMethodFaultHelperConfig) {
	t.Helper()
	barrier := t.TempDir()
	config.Barrier = barrier
	if err := os.WriteFile(filepath.Join(barrier, "MM-POSTCOMMIT.action"), []byte("block\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(barrier, "config.json")
	writeMissionMethodRaceJSON(t, configPath, config)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestMissionMethodCrashBoundaryCensus$", "-test.count=1")
	command.Env = append(os.Environ(), missionMethodFaultHelper+"="+configPath)
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = waitForBoundaryReached(waitContext, barrier, "MM-POSTCOMMIT", 1)
	cancel()
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("post-commit boundary was not reached: %v", err)
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if waitErr := command.Wait(); waitErr == nil {
		t.Fatal("post-commit helper returned a response instead of being killed")
	}
}

type missionMethodPostCommitArtifactStore struct {
	artifact.Repository
	controller faults.FileController
}

func (store missionMethodPostCommitArtifactStore) ApproveAtomic(ctx context.Context, request artifact.AtomicApprovalRequest) (artifact.ApprovalResult, error) {
	result, err := store.Repository.ApproveAtomic(ctx, request)
	if err == nil {
		err = store.controller.Reach(ctx, "MM-POSTCOMMIT")
	}
	return result, err
}

type missionMethodPostCommitFinalizationStore struct {
	mission.FinalizationStore
	controller faults.FileController
}

func (store missionMethodPostCommitFinalizationStore) PublishGenerationOne(ctx context.Context, commit mission.FinalizationCommit) (mission.FinalizationResult, error) {
	result, err := store.FinalizationStore.PublishGenerationOne(ctx, commit)
	if err == nil {
		err = store.controller.Reach(ctx, "MM-POSTCOMMIT")
	}
	return result, err
}

type missionMethodPostCommitSuccessorStore struct {
	mission.SuccessorPublicationStore
	controller faults.FileController
}

func (store missionMethodPostCommitSuccessorStore) PublishSuccessorAtomically(ctx context.Context, commit mission.SuccessorPublicationCommit) (mission.SuccessorPublicationResult, error) {
	result, err := store.SuccessorPublicationStore.PublishSuccessorAtomically(ctx, commit)
	if err == nil {
		err = store.controller.Reach(ctx, "MM-POSTCOMMIT")
	}
	return result, err
}

func runMissionMethodFaultHelper(t *testing.T, configPath string) {
	var config missionMethodFaultHelperConfig
	readMissionMethodRaceJSON(t, configPath, &config)
	store, err := sqlite.Open(context.Background(), config.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	controller := faults.FileController{Directory: config.Barrier, Participant: "postcommit"}
	switch config.Kind {
	case "artifact":
		repository := missionMethodPostCommitArtifactStore{Repository: store, controller: controller}
		service, serviceErr := artifact.NewService(repository)
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}
		if _, err = service.Approve(context.Background(), config.Approval); err != nil {
			t.Fatal(err)
		}
	case "finalization":
		observation := missionMethodRaceObservation(t, config.Finalization)
		wrapped := missionMethodPostCommitFinalizationStore{FinalizationStore: store, controller: controller}
		if _, err = mission.NewFinalizationService(missionMethodStaticObservation{value: observation}, wrapped).Finalize(context.Background(), config.Finalization.Command); err != nil {
			t.Fatal(err)
		}
	case "successor":
		current, loadErr := store.LoadMission(context.Background(), ids.MissionID("mission-dishonest-fault-reconciliation-post"))
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		old, loadErr := store.WorkRevisionV2(context.Background(), current.CurrentWorkRevisionID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		command := missionMethodSuccessorCommand(t, mission.FinalizationResult{Revision: old, Mission: current}, config.SuccessorSuffix)
		wrapped := missionMethodPostCommitSuccessorStore{SuccessorPublicationStore: store, controller: controller}
		if _, err = mission.NewSuccessorPublicationService(wrapped).Publish(context.Background(), command); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unknown post-commit helper kind")
	}
}

func missionMethodFinalizationFault(t *testing.T) {
	database, expected, command := prepareDishonestFinalizationMission(t, "fault-finalization")
	config := missionMethodRaceConfig{Command: command, PlanRevisionID: "artifact-plan-dishonest-fault-finalization", PlanDigest: string(artifact.ContentCommitment([]byte("dishonest provider plan fault-finalization")))}
	observation := missionMethodRaceObservation(t, config)
	installMissionMethodTrigger(t, database, `CREATE TRIGGER mission_fault_finalization BEFORE INSERT ON phase3_operations WHEN NEW.operation_kind='finalize_generation_one' BEGIN SELECT RAISE(ABORT,'mission fault'); END`)
	before := qualificationPersistenceSnapshotWithoutPointer(t, database, expected.MissionID)
	store, err := sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mission.NewFinalizationService(missionMethodStaticObservation{value: observation}, store).Finalize(context.Background(), command)
	if err == nil {
		t.Fatal("finalization fault was hidden")
	}
	store.Close()
	if after := qualificationPersistenceSnapshotWithoutPointer(t, database, expected.MissionID); !reflect.DeepEqual(before, after) {
		t.Fatalf("late finalization fault left partial authority before=%+v after=%+v", before, after)
	}
	dropMissionMethodTrigger(t, database, "mission_fault_finalization")
	store, err = sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	service := mission.NewFinalizationService(missionMethodStaticObservation{value: observation}, store)
	result, err := service.Finalize(context.Background(), command)
	if err != nil || result.Replayed || result.Mission.CurrentWorkRevisionID != result.Revision.ID() {
		t.Fatalf("finalization recovery=%+v err=%v", result, err)
	}
	replay, err := service.Finalize(context.Background(), command)
	store.Close()
	if err != nil || !replay.Replayed {
		t.Fatalf("finalization replay=%+v err=%v", replay, err)
	}
}

func missionMethodReconciliationFault(t *testing.T, boundary string) {
	database, _, finalization := prepareDishonestFinalizationMission(t, "fault-reconciliation-"+strings.TrimPrefix(boundary, "phase3_"))
	config := missionMethodRaceConfig{Command: finalization, PlanRevisionID: "artifact-plan-dishonest-fault-reconciliation-" + strings.TrimPrefix(boundary, "phase3_"), PlanDigest: string(artifact.ContentCommitment([]byte("dishonest provider plan fault-reconciliation-" + strings.TrimPrefix(boundary, "phase3_"))))}
	observation := missionMethodRaceObservation(t, config)
	store, err := sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	finalized, err := mission.NewFinalizationService(missionMethodStaticObservation{value: observation}, store).Finalize(context.Background(), finalization)
	if err != nil {
		t.Fatal(err)
	}
	command := missionMethodSuccessorCommand(t, finalized, boundary)
	trigger := `CREATE TRIGGER mission_fault_reconciliation BEFORE INSERT ON phase3_drift_reports BEGIN SELECT RAISE(ABORT,'mission fault'); END`
	if boundary == "phase3_operations" {
		trigger = `CREATE TRIGGER mission_fault_reconciliation BEFORE INSERT ON phase3_operations WHEN NEW.operation_kind='publish_successor' BEGIN SELECT RAISE(ABORT,'mission fault'); END`
	}
	installMissionMethodTrigger(t, database, trigger)
	before := qualificationPersistenceSnapshot(t, database, finalized.Mission.MissionID)
	_, err = mission.NewSuccessorPublicationService(store).Publish(context.Background(), command)
	if err == nil {
		t.Fatal("reconciliation fault was hidden")
	}
	store.Close()
	if after := qualificationPersistenceSnapshot(t, database, finalized.Mission.MissionID); !reflect.DeepEqual(before, after) {
		t.Fatalf("reconciliation fault left partial authority before=%+v after=%+v", before, after)
	}
	dropMissionMethodTrigger(t, database, "mission_fault_reconciliation")
	store, err = sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	service := mission.NewSuccessorPublicationService(store)
	result, err := service.Publish(context.Background(), command)
	if err != nil || result.Replayed || result.Mission.CurrentWorkRevisionID != command.Candidate.ID() || len(result.Reconciliation.Decisions()) != len(command.Drift.AffectedOldWorkIDs()) {
		t.Fatalf("reconciliation recovery=%+v err=%v", result, err)
	}
	replay, err := service.Publish(context.Background(), command)
	store.Close()
	if err != nil || !replay.Replayed {
		t.Fatalf("reconciliation replay=%+v err=%v", replay, err)
	}
}

func missionMethodSuccessorCommand(t *testing.T, finalized mission.FinalizationResult, suffix string) mission.SuccessorPublicationCommand {
	t.Helper()
	at := finalized.Mission.UpdatedAt.Add(time.Minute)
	params := qualificationRevisionParams(finalized.Revision)
	params.ID = ids.WorkRevisionID("revision-successor-" + strings.TrimPrefix(suffix, "phase3_"))
	params.Generation = 2
	params.PredecessorID = finalized.Revision.ID()
	params.WorkItems[0].Description = "Semantically changed after provider observation."
	params.FinalizedAt = at
	params.FinalizedBy = "actor-finalizer"
	params.GovernanceContextID = finalized.Revision.GovernanceContextID()
	params.Lanes = qualificationLanes(t, params)
	candidate, err := workgraph.NewRevisionV2(params)
	if err != nil {
		t.Fatal(err)
	}
	drift, err := workgraph.DetectDrift(finalized.Revision, candidate, at)
	if err != nil {
		t.Fatal(err)
	}
	statuses := make([]workgraph.WorkStatusAssignment, 0, len(drift.AffectedOldWorkIDs()))
	decisions := make([]workgraph.WorkDispositionDecisionParams, 0, len(drift.AffectedOldWorkIDs()))
	for _, workID := range drift.AffectedOldWorkIDs() {
		statuses = append(statuses, workgraph.WorkStatusAssignment{WorkID: workID, Status: workgraph.StatusActive})
		decisions = append(decisions, workgraph.WorkDispositionDecisionParams{OldWorkID: workID, Disposition: workgraph.Invalidate, Rationale: "semantic provider change"})
	}
	reconciliationID := ids.OperationID("reconciliation-" + strings.TrimPrefix(suffix, "phase3_"))
	reconciliation, err := workgraph.ReconcileMixed(workgraph.MixedReconcileRequest{ID: reconciliationID.String(), Old: finalized.Revision, New: candidate, Drift: drift, Statuses: statuses, Decisions: decisions, ActorID: "actor-finalizer", Rationale: "semantic provider change", OccurredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return mission.SuccessorPublicationCommand{MissionID: finalized.Mission.MissionID, ProjectID: finalized.Mission.ProjectID, ExpectedMission: finalized.Mission, ExpectedCurrentGeneration: 1, ExpectedOld: finalized.Revision, Candidate: candidate, Drift: drift, ReconciliationID: reconciliationID, Reconciliation: reconciliation, OperationID: ids.OperationID("operation-successor-" + strings.TrimPrefix(suffix, "phase3_")), ActorID: "actor-finalizer", GovernanceContextID: finalized.Revision.GovernanceContextID(), Rationale: "semantic provider change", OccurredAt: at}
}

func installMissionMethodTrigger(t *testing.T, path, statement string) {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

func dropMissionMethodTrigger(t *testing.T, path, name string) {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.Exec(`DROP TRIGGER ` + name); err != nil {
		t.Fatal(err)
	}
}

func qualificationPersistenceSnapshotWithoutPointer(t *testing.T, path string, missionID ids.MissionID) qualificationPersistence {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var result qualificationPersistence
	if err = raw.QueryRow(`SELECT COALESCE(current_work_revision_id,'') FROM phase3_missions WHERE mission_id=?`, missionID).Scan(&result.MissionPointer); err != nil {
		t.Fatal(err)
	}
	rows, err := raw.Query(`SELECT work_revision_id,generation,graph_digest,criteria_digest,encoded_revision FROM phase3_work_revisions_v2 WHERE mission_id=? ORDER BY generation,work_revision_id`, missionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var row qualificationRevisionRow
		if err = rows.Scan(&row.ID, &row.Generation, &row.GraphDigest, &row.CriteriaDigest, &row.EncodedRevision); err != nil {
			t.Fatal(err)
		}
		result.Revisions = append(result.Revisions, row)
	}
	return result
}

func missionMethodAuthorityCounts(t *testing.T, path string) methodRowCounts {
	t.Helper()
	return missionMethodRowCounts(t, path)
}

// Keep fmt referenced in this evidence file so accidental replacement of
// bounded identities by raw provider output is caught by static compilation.
var _ = fmt.Sprintf
