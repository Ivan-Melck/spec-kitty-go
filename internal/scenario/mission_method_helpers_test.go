package scenario

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type methodEnvelope struct {
	SchemaVersion uint16                      `json:"schema_version"`
	OK            bool                        `json:"ok"`
	Kind          string                      `json:"kind"`
	OperationID   ids.OperationID             `json:"operation_id,omitempty"`
	AggregateIDs  map[string]string           `json:"aggregate_ids"`
	RetrySafety   string                      `json:"retry_safety"`
	Recovery      string                      `json:"recovery"`
	Result        json.RawMessage             `json:"result,omitempty"`
	Error         *helpercmd.MethodDiagnostic `json:"error,omitempty"`
}

type portableMissionResult struct {
	Database      string
	TrackerStore  string
	OutputRoot    string
	GenerationOne workgraph.RevisionV2
	GenerationTwo workgraph.RevisionV2
	MissionOne    mission.MissionSnapshot
	MissionTwo    mission.MissionSnapshot
	FirstTasks    string
	SecondTasks   string
}

func runPortableMissionFixture(t *testing.T, suffix string) portableMissionResult {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	database := filepath.Join(root, "control.db")
	trackerStore := filepath.Join(root, "tracker.json")
	outputRoot := filepath.Join(root, "derived")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)
	definition, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	governance := portableGovernance(t, "governance-"+suffix, at)
	missionID := ids.MissionID("mission-" + suffix)
	projectID := ids.ProjectID("project-" + suffix)

	initialized := callMethod(t, ctx, "mission", "initialize", map[string]any{
		"database": database, "correlation_id": "correlation-initialize-" + suffix,
		"mission_id": missionID, "project_id": projectID, "definition_id": definition.ID(),
		"definition_version": definition.Version(), "operation_id": "operation-initialize-" + suffix,
		"actor_id": "actor-author", "process_id": "process-author-" + suffix,
		"governance_context": governance, "occurred_at": at,
	})
	var initializedResult struct {
		Mission mission.MissionSnapshot `json:"mission"`
	}
	decodeMethodResult(t, initialized, &initializedResult)
	current := initializedResult.Mission

	publishArtifact := func(kind artifact.Kind, id, source string, content []byte, when time.Time) artifactRevisionView {
		policy, policyErr := artifact.PolicyFor(kind)
		if policyErr != nil {
			t.Fatal(policyErr)
		}
		concerns := policy.RequiredConcerns()
		if concerns == nil {
			concerns = []artifact.Concern{}
		}
		request := map[string]any{
			"database": database, "correlation_id": "correlation-publish-" + id,
			"operation_id": "operation-publish-" + id, "artifact_revision_id": id,
			"mission_id": missionID, "kind": kind, "source_ref": source,
			"source_revision": strings.Repeat("a", 40), "created_by": "actor-author", "created_at": when,
			"content": content, "present_concerns": concerns, "validated_at": when,
			"expected_latest_revision_id": "",
		}
		envelope := callMethod(t, ctx, "artifact", "publish", request)
		var result struct {
			Revision artifactRevisionView `json:"revision"`
			Replayed bool                 `json:"replayed"`
		}
		decodeMethodResult(t, envelope, &result)
		replay := callMethod(t, ctx, "artifact", "publish", request)
		decodeMethodResult(t, replay, &result)
		if !result.Replayed {
			t.Fatalf("artifact publish lost-response replay was not reported: %s", id)
		}
		return result.Revision
	}
	approveArtifact := func(revision artifactRevisionView, when time.Time) {
		request := map[string]any{
			"database": database, "correlation_id": "correlation-approve-" + revision.ID,
			"operation_id": "operation-approve-" + revision.ID, "approval_id": "approval-" + revision.ID,
			"artifact_revision_id": revision.ID, "verdict": artifact.VerdictApproved,
			"actor_id": "actor-approver", "evidence_digest": artifact.ContentCommitment([]byte("approval:" + revision.ID)),
			"governance_context_id": governance.ID(), "submitted_at": when,
		}
		callMethod(t, ctx, "artifact", "approve", request)
		replay := callMethod(t, ctx, "artifact", "approve", request)
		var result struct {
			Replayed bool `json:"replayed"`
		}
		decodeMethodResult(t, replay, &result)
		if !result.Replayed {
			t.Fatalf("artifact approval lost-response replay was not reported: %s", revision.ID)
		}
	}

	_ = publishArtifact(artifact.KindIntent, "artifact-intent-"+suffix, "intent.md", []byte("mission intent"), at.Add(time.Second))
	current = transitionMission(t, ctx, database, definition, current, mission.StepSpecifying, governance.ID(), suffix+"-specifying", at.Add(2*time.Second))
	specification := publishArtifact(artifact.KindSpecification, "artifact-spec-"+suffix, "spec.md", []byte("complete specification"), at.Add(3*time.Second))
	approveArtifact(specification, at.Add(4*time.Second))
	current = transitionMission(t, ctx, database, definition, current, mission.StepSpecificationApproved, governance.ID(), suffix+"-spec-approved", at.Add(5*time.Second))
	current = transitionMission(t, ctx, database, definition, current, mission.StepPlanning, governance.ID(), suffix+"-planning", at.Add(6*time.Second))
	plan := publishArtifact(artifact.KindPlan, "artifact-plan-"+suffix, "plan.md", []byte("complete implementation plan"), at.Add(7*time.Second))
	approveArtifact(plan, at.Add(8*time.Second))
	current = transitionMission(t, ctx, database, definition, current, mission.StepPlanApproved, governance.ID(), suffix+"-plan-approved", at.Add(9*time.Second))

	approvedScope := []map[string]any{{"id": "plan-core", "kind": workgraph.ScopePlanConcern}}
	work := portableWorkInput()
	decompositionRequest := map[string]any{
		"database": database, "tracker_store": trackerStore, "correlation_id": "correlation-decompose-" + suffix,
		"mission_id": missionID, "project_id": projectID, "provider": "durable_fake", "expected_mission": current,
		"plan_revision_id": plan.ID, "plan_content_digest": plan.ContentDigest,
		"approved_scope": approvedScope, "work": work, "actor_id": "actor-author",
		"operation_id": "operation-decompose-" + suffix, "governance_context_id": governance.ID(), "occurred_at": at.Add(10 * time.Second),
	}
	decomposition := callMethod(t, ctx, "mission", "decompose", decompositionRequest)
	var decomposed struct {
		Status       mission.DecompositionStatus `json:"status"`
		Observations []portableObservation       `json:"observations"`
		Replayed     bool                        `json:"replayed"`
	}
	decodeMethodResult(t, decomposition, &decomposed)
	if decomposed.Status != mission.DecompositionComplete || len(decomposed.Observations) != 2 {
		t.Fatalf("decomposition=%+v", decomposed)
	}
	decompositionReplay := callMethod(t, ctx, "mission", "decompose", decompositionRequest)
	decodeMethodResult(t, decompositionReplay, &decomposed)
	if !decomposed.Replayed {
		t.Fatalf("decomposition lost-response replay was not reported: %+v", decomposed)
	}
	fakeStore, err := fake.Open(trackerStore)
	if err != nil {
		t.Fatal(err)
	}
	historyBeforeConflict, err := fakeStore.History(ctx)
	if err != nil {
		t.Fatal(err)
	}
	alteredDecomposition := cloneRequestMap(t, decompositionRequest)
	alteredWork := alteredDecomposition["work"].([]any)
	alteredWork[0].(map[string]any)["title"] = "Conflicting title"
	decompositionConflict := callMethodRaw(t, ctx, "mission", "decompose", alteredDecomposition)
	historyAfterConflict, historyErr := fakeStore.History(ctx)
	if decompositionConflict.OK || decompositionConflict.Error == nil || decompositionConflict.Error.Kind != "operation_conflict" || historyErr != nil || len(historyAfterConflict) != len(historyBeforeConflict) {
		t.Fatalf("decomposition conflict=%+v history before=%d after=%d err=%v", decompositionConflict, len(historyBeforeConflict), len(historyAfterConflict), historyErr)
	}
	control, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := control.LoadMission(ctx, missionID)
	_ = control.Close()
	if err != nil || unchanged.CurrentStep != current.CurrentStep || unchanged.StateVersion != current.StateVersion {
		t.Fatalf("decomposition mutated mission: before=%+v after=%+v err=%v", current, unchanged, err)
	}
	// Decomposition is intentionally provider work only; its caller submits
	// two distinct, ordinary mission transitions after complete observation.
	current = transitionMission(t, ctx, database, definition, current, mission.StepDecomposing, governance.ID(), suffix+"-decomposing", at.Add(11*time.Second))
	current = transitionMission(t, ctx, database, definition, current, mission.StepWorkDraftReady, governance.ID(), suffix+"-draft-ready", at.Add(12*time.Second))

	providerRefs := make([]map[string]any, 0, len(decomposed.Observations))
	for _, observation := range decomposed.Observations {
		providerRefs = append(providerRefs, map[string]any{
			"work_id": observation.WorkID, "external_id": observation.ExternalID,
			"metadata_schema": observation.MetadataSchema, "created_by_operation": observation.CreatedByOperation,
			"last_observation": observation.ProviderRevision,
		})
	}
	finalizationRequest := map[string]any{
		"database": database, "tracker_store": trackerStore, "correlation_id": "correlation-finalize-" + suffix,
		"mission_id": missionID, "project_id": projectID, "expected_mission": current,
		"expected_current_generation": 0, "proposed_revision_id": "revision-" + suffix + "-1",
		"planning_source_commit": strings.Repeat("b", 40), "plan_revision_id": plan.ID, "plan_digest": plan.ContentDigest,
		"approved_scope": approvedScope, "work": work, "provider_refs": providerRefs,
		"actor_id": "actor-author", "operation_id": "operation-finalize-" + suffix,
		"governance_context_id": governance.ID(), "occurred_at": at.Add(13 * time.Second),
	}
	finalized := callMethod(t, ctx, "work", "finalize", finalizationRequest)
	var finalizedResult struct {
		Revision json.RawMessage         `json:"revision"`
		Mission  mission.MissionSnapshot `json:"mission"`
		Replayed bool                    `json:"replayed"`
	}
	decodeMethodResult(t, finalized, &finalizedResult)
	finalizationReplay := callMethod(t, ctx, "work", "finalize", finalizationRequest)
	decodeMethodResult(t, finalizationReplay, &finalizedResult)
	if !finalizedResult.Replayed {
		t.Fatal("finalization lost-response replay was not reported")
	}
	rowsAfterFinalization := missionMethodRowCounts(t, database)
	alteredFinalization := cloneRequestMap(t, finalizationRequest)
	alteredFinalization["proposed_revision_id"] = "revision-" + suffix + "-conflict"
	finalizationConflict := callMethodRaw(t, ctx, "work", "finalize", alteredFinalization)
	if finalizationConflict.OK || finalizationConflict.Error == nil || finalizationConflict.Error.Kind != "operation_conflict" || missionMethodRowCounts(t, database) != rowsAfterFinalization {
		t.Fatalf("finalization conflict mutated authority: %+v", finalizationConflict)
	}
	generationOne, err := workgraph.DecodeRevisionV2(finalizedResult.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if generationOne.Generation() != 1 || generationOne.PredecessorID() != "" || len(generationOne.WorkItems()) != 2 || len(generationOne.Lanes()) != 2 {
		t.Fatalf("generation one=%+v", generationOne)
	}

	next := callMethod(t, ctx, "mission", "next", map[string]any{"database": database, "correlation_id": "correlation-next-" + suffix, "mission_id": missionID})
	if next.OperationID != "" {
		t.Fatalf("pure next carried operation identity: %+v", next)
	}
	var nextResult mission.NextResult
	decodeMethodResult(t, next, &nextResult)
	if nextResult.Kind != mission.NextWorkReady {
		t.Fatalf("next=%+v", nextResult)
	}
	firstProjection := callMethod(t, ctx, "projection", "tasks", map[string]any{
		"database": database, "correlation_id": "correlation-project-1-" + suffix, "mission_id": missionID,
		"rendered_at": at.Add(14 * time.Second), "trusted_root": outputRoot, "destination": "tasks.md",
	})
	var firstProjectionResult struct {
		Content    string `json:"content"`
		Generation uint64 `json:"generation"`
	}
	decodeMethodResult(t, firstProjection, &firstProjectionResult)
	if firstProjectionResult.Generation != 1 || !strings.Contains(firstProjectionResult.Content, "Define the contract") || !strings.Contains(firstProjectionResult.Content, "Exercise the contract") {
		t.Fatalf("generation-one projection=%+v", firstProjectionResult)
	}

	candidate, candidateRef := createReplacementCandidate(t, ctx, trackerStore, generationOne, plan, governance.ID(), at.Add(17*time.Second), suffix)
	oldEncoded, _ := generationOne.Encode()
	candidateEncoded, _ := candidate.Encode()
	databaseBeforeDrift, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	rowsBeforeDrift := missionMethodRowCounts(t, database)
	drift := callMethod(t, ctx, "work", "drift", map[string]any{
		"database": database, "tracker_store": trackerStore, "correlation_id": "correlation-drift-" + suffix,
		"mission_id": missionID, "old_revision": json.RawMessage(oldEncoded), "candidate_revision": json.RawMessage(candidateEncoded),
		"provider_refs": []map[string]any{candidateRef}, "observed_at": at.Add(16 * time.Second),
	})
	if drift.OperationID != "" {
		t.Fatalf("pure drift carried operation identity: %+v", drift)
	}
	var driftView struct {
		Status      workgraph.SemanticStatus `json:"semantic_status"`
		AffectedOld []ids.WorkID             `json:"affected_old_work_ids"`
		AffectedNew []ids.WorkID             `json:"affected_new_work_ids"`
	}
	decodeMethodResult(t, drift, &driftView)
	if driftView.Status != workgraph.SemanticChanged || len(driftView.AffectedOld) != 2 || len(driftView.AffectedNew) != 1 {
		t.Fatalf("drift=%+v raw=%s", driftView, drift.Result)
	}
	databaseAfterDrift, err := os.ReadFile(database)
	rowsAfterDrift := missionMethodRowCounts(t, database)
	if err != nil || !bytes.Equal(databaseBeforeDrift, databaseAfterDrift) || rowsAfterDrift != rowsBeforeDrift {
		t.Fatalf("pure drift changed local authority: err=%v", err)
	}

	incomplete := callMethodRaw(t, ctx, "work", "reconcile", map[string]any{
		"database": database, "correlation_id": "correlation-reconcile-incomplete-" + suffix,
		"mission_id": missionID, "project_id": projectID, "expected_mission": finalizedResult.Mission,
		"expected_current_generation": 1, "old_revision": json.RawMessage(oldEncoded), "candidate_revision": json.RawMessage(candidateEncoded),
		"drift_report": json.RawMessage(drift.Result), "reconciliation_id": "reconciliation-incomplete-" + suffix,
		"decisions":    []map[string]any{{"old_work_id": "work-a", "status": workgraph.StatusActive, "disposition": workgraph.Supersede, "new_work_ids": []ids.WorkID{"work-a-v2"}, "fork_edges": []workgraph.ForkEdge{}, "rationale": "replace changed contract"}},
		"operation_id": "operation-reconcile-incomplete-" + suffix, "actor_id": "actor-author",
		"governance_context_id": governance.ID(), "rationale": "incomplete mutant", "occurred_at": at.Add(17 * time.Second),
	})
	if incomplete.OK || incomplete.Error == nil {
		t.Fatalf("incomplete reconciliation succeeded: %+v", incomplete)
	}
	if rows := missionMethodRowCounts(t, database); rows != rowsBeforeDrift {
		t.Fatalf("incomplete reconciliation persisted partial rows: before=%+v after=%+v", rowsBeforeDrift, rows)
	}
	control, err = sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	stillOne, observeErr := control.ObserveMissionMethod(ctx, missionID)
	_ = control.Close()
	if observeErr != nil || stillOne.Workgraph.Generation() != 1 {
		t.Fatalf("incomplete reconciliation partially published: observation=%+v err=%v", stillOne, observeErr)
	}

	reconciliationRequest := map[string]any{
		"database": database, "correlation_id": "correlation-reconcile-" + suffix,
		"mission_id": missionID, "project_id": projectID, "expected_mission": finalizedResult.Mission,
		"expected_current_generation": 1, "old_revision": json.RawMessage(oldEncoded), "candidate_revision": json.RawMessage(candidateEncoded),
		"drift_report": json.RawMessage(drift.Result), "reconciliation_id": "reconciliation-" + suffix,
		"decisions": []map[string]any{
			{"old_work_id": "work-a", "status": workgraph.StatusActive, "disposition": workgraph.Supersede, "new_work_ids": []ids.WorkID{"work-a-v2"}, "fork_edges": []workgraph.ForkEdge{}, "rationale": "replace changed contract"},
			{"old_work_id": "work-b", "status": workgraph.StatusActive, "disposition": workgraph.Invalidate, "new_work_ids": []ids.WorkID{}, "fork_edges": []workgraph.ForkEdge{}, "rationale": "dependent exercise is no longer required"},
		},
		"operation_id": "operation-reconcile-" + suffix, "actor_id": "actor-author",
		"governance_context_id": governance.ID(), "rationale": "adopt explicit replacement scope", "occurred_at": at.Add(17 * time.Second),
	}
	reconciled := callMethod(t, ctx, "work", "reconcile", reconciliationRequest)
	var reconciledResult struct {
		Revision json.RawMessage         `json:"revision"`
		Mission  mission.MissionSnapshot `json:"mission"`
		Replayed bool                    `json:"replayed"`
	}
	decodeMethodResult(t, reconciled, &reconciledResult)
	reconciliationReplay := callMethod(t, ctx, "work", "reconcile", reconciliationRequest)
	decodeMethodResult(t, reconciliationReplay, &reconciledResult)
	if !reconciledResult.Replayed {
		t.Fatal("reconciliation lost-response replay was not reported")
	}
	rowsAfterReconciliation := missionMethodRowCounts(t, database)
	alteredReconciliation := cloneRequestMap(t, reconciliationRequest)
	alteredReconciliation["rationale"] = "conflicting reconciliation rationale"
	reconciliationConflict := callMethodRaw(t, ctx, "work", "reconcile", alteredReconciliation)
	if reconciliationConflict.OK || reconciliationConflict.Error == nil || missionMethodRowCounts(t, database) != rowsAfterReconciliation {
		t.Fatalf("reconciliation conflict mutated authority: %+v", reconciliationConflict)
	}
	generationTwo, err := workgraph.DecodeRevisionV2(reconciledResult.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if generationTwo.Generation() != 2 || generationTwo.PredecessorID() != generationOne.ID() || len(generationTwo.WorkItems()) != 1 || generationTwo.WorkItems()[0].WorkID() != "work-a-v2" {
		t.Fatalf("generation two=%+v", generationTwo)
	}
	secondProjection := callMethod(t, ctx, "projection", "tasks", map[string]any{
		"database": database, "correlation_id": "correlation-project-2-" + suffix, "mission_id": missionID,
		"rendered_at": at.Add(18 * time.Second), "trusted_root": outputRoot, "destination": "tasks.md",
	})
	var secondProjectionResult struct {
		Content    string `json:"content"`
		Generation uint64 `json:"generation"`
	}
	decodeMethodResult(t, secondProjection, &secondProjectionResult)
	if secondProjectionResult.Generation != 2 || !strings.Contains(secondProjectionResult.Content, "Define the revised contract") || strings.Contains(secondProjectionResult.Content, "Exercise the contract") {
		t.Fatalf("generation-two projection=%+v", secondProjectionResult)
	}
	return portableMissionResult{database, trackerStore, outputRoot, generationOne, generationTwo, finalizedResult.Mission, reconciledResult.Mission, firstProjectionResult.Content, secondProjectionResult.Content}
}

type artifactRevisionView struct {
	ID             string        `json:"artifact_revision_id"`
	Kind           artifact.Kind `json:"kind"`
	ContentDigest  string        `json:"content_digest"`
	RevisionNumber uint64        `json:"revision_number"`
}

type portableObservation struct {
	WorkID             ids.WorkID       `json:"work_id"`
	ExternalID         string           `json:"external_id"`
	ProviderRevision   tracker.Revision `json:"provider_revision"`
	MetadataSchema     uint16           `json:"metadata_schema"`
	CreatedByOperation ids.OperationID  `json:"created_by_operation"`
}

func transitionMission(t *testing.T, ctx context.Context, database string, definition mission.MissionDefinition, current mission.MissionSnapshot, successor mission.StepKey, governanceID ids.GovernanceContextID, suffix string, at time.Time) mission.MissionSnapshot {
	t.Helper()
	destination, ok := definition.Step(successor)
	if !ok {
		t.Fatalf("missing destination %s", successor)
	}
	evidence := make([]mission.EvidenceRef, 0, len(destination.Spec().RequiredEvidence))
	for index, kind := range destination.Spec().RequiredEvidence {
		if kind == mission.EvidenceSpecificationOK || kind == mission.EvidencePlanOK {
			continue
		}
		evidence = append(evidence, mission.EvidenceRef{ID: ids.EvidenceID(fmt.Sprintf("evidence-%s-%d", suffix, index)), Kind: kind, Status: mission.EvidenceAccepted, MissionID: current.MissionID, ForStep: current.CurrentStep, StateVersion: current.StateVersion})
	}
	request := map[string]any{
		"database": database, "correlation_id": "correlation-transition-" + suffix, "mission_id": current.MissionID,
		"definition_id": definition.ID(), "definition_version": definition.Version(), "definition_commitment": definition.Commitment(),
		"expected_snapshot": current, "expected_current_step": current.CurrentStep, "expected_state_version": current.StateVersion,
		"successor": successor, "event_id": "event-" + suffix, "operation_id": "operation-transition-" + suffix,
		"actor_id": "actor-author", "governance_context_id": governanceID, "occurred_at": at, "evidence": evidence,
		"work_revision_id": "",
	}
	envelope := callMethod(t, ctx, "mission", "transition", request)
	var result struct {
		Mission  mission.MissionSnapshot `json:"mission"`
		Replayed bool                    `json:"replayed"`
	}
	decodeMethodResult(t, envelope, &result)
	replay := callMethod(t, ctx, "mission", "transition", request)
	decodeMethodResult(t, replay, &result)
	if !result.Replayed {
		t.Fatalf("transition lost-response replay was not reported: %s", suffix)
	}
	return result.Mission
}

func callMethod(t *testing.T, ctx context.Context, root, action string, request any) methodEnvelope {
	t.Helper()
	envelope := callMethodRaw(t, ctx, root, action, request)
	if !envelope.OK {
		t.Fatalf("%s %s refused: %+v", root, action, envelope.Error)
	}
	return envelope
}

func callMethodRaw(t *testing.T, ctx context.Context, root, action string, request any) methodEnvelope {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := helpercmd.Run(ctx, []string{root, action, "--request-json", string(encoded)}, &stdout, &stderr); code != 0 {
		t.Fatalf("%s %s code=%d stdout=%q stderr=%q", root, action, code, stdout.String(), stderr.String())
	}
	var envelope methodEnvelope
	if err = json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("%s %s response %q: %v", root, action, stdout.String(), err)
	}
	if envelope.SchemaVersion != 1 || envelope.Kind == "" || envelope.AggregateIDs == nil || envelope.RetrySafety == "" || envelope.Recovery == "" {
		t.Fatalf("%s %s returned an incomplete typed envelope: %+v", root, action, envelope)
	}
	return envelope
}

func decodeMethodResult(t *testing.T, envelope methodEnvelope, target any) {
	t.Helper()
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		t.Fatalf("decode %s result %s: %v", envelope.Kind, envelope.Result, err)
	}
}

func cloneRequestMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err = decoder.Decode(&clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

type methodRowCounts struct {
	Operations      int
	DriftReports    int
	Reconciliations int
	Revisions       int
	Events          int
	Outbox          int
}

func missionMethodRowCounts(t *testing.T, path string) methodRowCounts {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var counts methodRowCounts
	if err = database.QueryRow(`SELECT
		(SELECT COUNT(*) FROM phase3_operations),
		(SELECT COUNT(*) FROM phase3_drift_reports),
		(SELECT COUNT(*) FROM phase3_mixed_reconciliations),
		(SELECT COUNT(*) FROM phase3_work_revisions_v2),
		(SELECT COUNT(*) FROM phase3_events),
		(SELECT COUNT(*) FROM phase3_outbox)`).Scan(&counts.Operations, &counts.DriftReports, &counts.Reconciliations, &counts.Revisions, &counts.Events, &counts.Outbox); err != nil {
		t.Fatal(err)
	}
	return counts
}

func portableGovernance(t *testing.T, id string, at time.Time) governancecontext.Context {
	t.Helper()
	commitment, err := governancecontext.NewCommitment(governancecontext.AlgorithmSHA256, strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := governancecontext.NewEntry(governancecontext.KindProfile, "implementer-ivan", "1", commitment)
	if err != nil {
		t.Fatal(err)
	}
	value, err := governancecontext.NewContext(ids.GovernanceContextID(id), governancecontext.SchemaVersion, []governancecontext.Entry{entry}, at)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func portableWorkInput() []map[string]any {
	criterionA := tracker.WorkCriterion{ID: "criterion-a", Text: "Define the portable contract", Outcome: "a deterministic contract is available", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "run contract tests"}
	criterionB := tracker.WorkCriterion{ID: "criterion-b", Text: "Exercise the portable contract", Outcome: "the complete scenario passes", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "run scenario tests"}
	return []map[string]any{
		{"work_id": "work-a", "title": "Define the contract", "description": "Define the standalone portable mission contract.", "design_context": "Use provider-neutral typed authority and deterministic results.", "cross_domain_reason": "", "criteria": []tracker.WorkCriterion{criterionA}, "dependencies": []ids.WorkID{}, "write_scopes": []tracker.WriteScope{{Path: "internal/contract", Domain: "contract", Access: tracker.ScopeWriteExclusive}}, "provenance": []tracker.ScopeRef{{ID: "plan-core", Kind: tracker.ScopePlanConcern}}},
		{"work_id": "work-b", "title": "Exercise the contract", "description": "Exercise the contract through the durable portable scenario.", "design_context": "Depend on the contract and validate public evidence only.", "cross_domain_reason": "", "criteria": []tracker.WorkCriterion{criterionB}, "dependencies": []ids.WorkID{"work-a"}, "write_scopes": []tracker.WriteScope{{Path: "internal/scenario", Domain: "scenario", Access: tracker.ScopeWriteExclusive}}, "provenance": []tracker.ScopeRef{{ID: "plan-core", Kind: tracker.ScopePlanConcern}}},
	}
}

func createReplacementCandidate(t *testing.T, ctx context.Context, trackerPath string, old workgraph.RevisionV2, plan artifactRevisionView, governanceID ids.GovernanceContextID, at time.Time, suffix string) (workgraph.RevisionV2, map[string]any) {
	t.Helper()
	metadata, _ := tracker.NewOpaqueMetadata([]byte(`{}`))
	criterion := tracker.WorkCriterion{ID: "criterion-a-v2", Text: "Define the revised portable contract", Outcome: "a revised deterministic contract is available", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "run revised contract tests"}
	definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{ProjectID: old.ProjectID(), WorkID: "work-a-v2", Title: "Define the revised contract", Description: "Replace the original contract with the explicitly revised portable contract.", DesignContext: "Preserve provider-neutral typed authority while changing acceptance semantics.", Criteria: []tracker.WorkCriterion{criterion}, WriteScopes: []tracker.WriteScope{{Path: "internal/contract-v2", Domain: "contract-v2", Access: tracker.ScopeWriteExclusive}}, Provenance: []tracker.ScopeRef{{ID: "plan-core", Kind: tracker.ScopePlanConcern}}, MetadataSchema: tracker.WorkDefinitionMetadataSchemaV1, ProviderMetadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	store, err := fake.Open(trackerPath)
	if err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(store)
	operation := ids.OperationID("operation-replacement-" + suffix)
	requestDigest := strings.Repeat("d", 64)
	if err = provider.PrepareDefinitionOperation(ctx, operation, requestDigest); err != nil {
		t.Fatal(err)
	}
	mutation, err := provider.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: operation, Definition: definition})
	if err != nil {
		t.Fatal(err)
	}
	ref := mutation.Observation.ExternalRef()
	providerObservation := mutation.Observation.ProviderObservation()
	workObservation, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: old.ProjectID(), WorkID: "work-a-v2", Title: definition.Title(), Description: definition.Description(), DesignContext: definition.DesignContext(), Criteria: []workgraph.CriterionObservation{{ID: criterion.ID, Text: criterion.Text, Outcome: criterion.Outcome, Verifier: string(criterion.Verifier), Requirement: workgraph.CriterionRequired}}, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/contract-v2", Domain: "contract-v2", Access: workgraph.ScopeWriteExclusive}}, Provenance: []workgraph.ScopeReference{{ID: "plan-core", Kind: workgraph.ScopePlanConcern}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workgraph.NewDraftObservation(old.ProjectID(), old.MissionID(), plan.ID, plan.ContentDigest, []workgraph.ApprovedScope{{ID: "plan-core", Kind: workgraph.ScopePlanConcern, MissionID: old.MissionID()}}, []workgraph.WorkObservation{workObservation})
	if err != nil {
		t.Fatal(err)
	}
	lanes, err := workgraph.CollapseLanes(draft)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := workgraph.NewRevisionV2(workgraph.RevisionV2Params{ID: ids.WorkRevisionID("revision-" + suffix + "-2"), ProjectID: old.ProjectID(), MissionID: old.MissionID(), Generation: 2, PredecessorID: old.ID(), PlanningSourceCommit: old.PlanningSourceCommit(), MissionDefinition: old.MissionDefinition(), DependencyPolicy: old.DependencyPolicy(), WorkItems: []workgraph.WorkItemV2Params{{WorkID: "work-a-v2", Title: definition.Title(), Description: definition.Description(), DesignContext: definition.DesignContext(), Criteria: []workgraph.CriterionV2Params{{ID: criterion.ID, Text: criterion.Text, VerifierKind: string(criterion.Verifier), Required: true, Outcome: criterion.Outcome, EvidenceHint: criterion.EvidenceHint}}, WriteScopes: []workgraph.NormalizedWriteScope{{Path: "internal/contract-v2", Domain: "contract-v2", Access: workgraph.ScopeWriteExclusive}}}}, Lanes: lanes, ProviderObservations: []workgraph.ProviderObservationV2{{WorkID: "work-a-v2", Provider: "durable_fake", ExternalID: ref.ExternalID, Revision: fmt.Sprint(providerObservation.Revision)}}, FinalizedBy: "actor-author", FinalizedAt: at, GovernanceContextID: governanceID})
	if err != nil {
		t.Fatal(err)
	}
	return candidate, map[string]any{"work_id": "work-a-v2", "external_id": ref.ExternalID, "metadata_schema": ref.MetadataSchema, "created_by_operation": ref.CreatedByOperation, "last_observation": providerObservation.Revision}
}
