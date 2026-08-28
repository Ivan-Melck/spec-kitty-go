package scenario

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
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
	trackerBefore, err := os.ReadFile(result.TrackerStore)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(result.OutputRoot, "tasks.md")
	projectionBefore, err := os.ReadFile(path)
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
	trackerAfter, trackerErr := os.ReadFile(result.TrackerStore)
	projectionAfter, projectionErr := os.ReadFile(path)
	if trackerErr != nil || projectionErr != nil || !bytes.Equal(trackerBefore, trackerAfter) || !bytes.Equal(projectionBefore, projectionAfter) {
		t.Fatalf("100 pure next queries changed provider/projection state: tracker_err=%v projection_err=%v tracker_equal=%v projection_equal=%v", trackerErr, projectionErr, bytes.Equal(trackerBefore, trackerAfter), bytes.Equal(projectionBefore, projectionAfter))
	}

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

	// Fresh comprehension receives only exact public helper results. It does
	// not receive fixture prose, package-private state, or the setup transcript.
	one, err := summarizePublicMission(result.GenerationOnePublic, result.PlanPublic, result.NextOnePublic)
	if err != nil || len(one.Work) != 2 {
		t.Fatalf("generation-one public summary=%+v err=%v", one, err)
	}
	two, err := summarizePublicMission(result.GenerationTwoPublic, result.PlanPublic, result.NextTwoPublic)
	if err != nil || len(two.Work) != 1 || two.Generation != 2 || two.NextAction != mission.NextWorkReady || two.SourcePlanRevision != result.PlanPublic.ID {
		t.Fatalf("generation-two public summary=%+v err=%v", two, err)
	}

	for name, mutate := range publicSummaryOmissionMutants() {
		t.Run("fresh-reader-omits-"+name, func(t *testing.T) {
			revision := append(json.RawMessage(nil), result.GenerationTwoPublic...)
			plan := result.PlanPublic
			next := append(json.RawMessage(nil), result.NextTwoPublic...)
			mutate(t, &revision, &plan, &next)
			if summary, err := summarizePublicMission(revision, plan, next); err == nil {
				t.Fatalf("omission accepted: %+v", summary)
			}
		})
	}

	assertMissionMethodNegativeMatrix(t, result, unsafe)
}

func assertMissionMethodNegativeMatrix(t *testing.T, result portableMissionResult, unsafe methodEnvelope) {
	t.Helper()
	ctx := context.Background()
	responses := make(map[string]methodEnvelope, len(result.NegativeResponses)+8)
	for name, response := range result.NegativeResponses {
		responses[name] = response
	}
	responses["unsafe-output"] = unsafe
	definition, _ := softwaredev.Definition()

	invalidLifecycle := callMethodRaw(t, ctx, "mission", "transition", map[string]any{
		"database": result.Database, "correlation_id": "correlation-invalid-lifecycle", "mission_id": result.MissionTwo.MissionID,
		"definition_id": definition.ID(), "definition_version": definition.Version(), "definition_commitment": definition.Commitment(),
		"expected_snapshot": result.MissionTwo, "expected_current_step": result.MissionTwo.CurrentStep, "expected_state_version": result.MissionTwo.StateVersion,
		"successor": mission.StepSpecifying, "event_id": "event-invalid-lifecycle", "operation_id": "operation-invalid-lifecycle",
		"actor_id": "actor-author", "governance_context_id": "governance-portable-purity", "occurred_at": result.MissionTwo.UpdatedAt.Add(time.Second), "evidence": []mission.EvidenceRef{}, "work_revision_id": "",
	})
	if invalidLifecycle.OK || invalidLifecycle.Error == nil {
		t.Fatalf("invalid lifecycle succeeded: %+v", invalidLifecycle)
	}
	responses["invalid-lifecycle"] = invalidLifecycle

	ambiguousRef := cloneRequestMap(t, result.GenerationTwoRef)
	providerAmbiguity := callMethodRaw(t, ctx, "work", "drift", map[string]any{
		"database": result.Database, "tracker_store": result.TrackerStore, "correlation_id": "correlation-provider-ambiguity",
		"mission_id": result.MissionTwo.MissionID, "old_revision": result.GenerationTwoPublic, "candidate_revision": result.GenerationTwoPublic,
		"provider_refs": []map[string]any{ambiguousRef, cloneRequestMap(t, ambiguousRef)}, "observed_at": result.MissionTwo.UpdatedAt.Add(time.Second),
	})
	if providerAmbiguity.OK || providerAmbiguity.Error == nil {
		t.Fatalf("provider ambiguity succeeded: %+v", providerAmbiguity)
	}
	responses["provider-ambiguity"] = providerAmbiguity

	staleGenerationInput := cloneRequestMap(t, result.ReconciliationInput)
	staleGeneration := callMethodRaw(t, ctx, "work", "drift", map[string]any{
		"database": staleGenerationInput["database"], "tracker_store": result.TrackerStore,
		"correlation_id": "correlation-stale-generation", "mission_id": result.MissionTwo.MissionID,
		"old_revision": staleGenerationInput["old_revision"], "candidate_revision": staleGenerationInput["candidate_revision"],
		"provider_refs": []map[string]any{cloneRequestMap(t, result.GenerationTwoRef)}, "observed_at": result.MissionTwo.UpdatedAt.Add(time.Second),
	})
	if staleGeneration.OK || staleGeneration.Error == nil {
		t.Fatalf("stale generation succeeded: %+v", staleGeneration)
	}
	responses["stale-generation"] = staleGeneration

	at := result.MissionTwo.UpdatedAt.Add(2 * time.Second)
	unknownDefinition := callMethodRaw(t, ctx, "mission", "initialize", map[string]any{
		"database": filepath.Join(t.TempDir(), "must-not-open.db"), "correlation_id": "correlation-unknown-definition",
		"mission_id": "mission-unknown-definition", "project_id": "project-unknown-definition", "definition_id": "unknown-definition", "definition_version": 1,
		"operation_id": "operation-unknown-definition", "actor_id": "actor-unknown", "process_id": "process-unknown", "governance_context": portableGovernance(t, "governance-unknown", at), "occurred_at": at,
	})
	if unknownDefinition.OK || unknownDefinition.Error == nil {
		t.Fatalf("unknown definition succeeded: %+v", unknownDefinition)
	}
	responses["unknown-definition"] = unknownDefinition

	corruptPath := filepath.Join(t.TempDir(), "corrupt.db")
	source, err := sql.Open("sqlite", result.Database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		source.Close()
		t.Fatal(err)
	}
	if _, err = source.Exec(`VACUUM INTO ?`, corruptPath); err != nil {
		source.Close()
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", corruptPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`UPDATE phase3_missions SET snapshot_json=json_set(snapshot_json,'$.StateVersion',0) WHERE mission_id=?`, result.MissionTwo.MissionID); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	corrupt := callMethodRaw(t, ctx, "projection", "tasks", map[string]any{"database": corruptPath, "correlation_id": "correlation-corrupt", "mission_id": result.MissionTwo.MissionID, "rendered_at": at, "trusted_root": "", "destination": ""})
	if corrupt.OK || corrupt.Error == nil {
		t.Fatalf("corrupt snapshot succeeded: %+v", corrupt)
	}
	responses["corrupt-snapshot"] = corrupt

	expected := map[string]string{
		"invalid-lifecycle":         "terminal_mission",
		"stale-approval":            "missing_evidence",
		"missing-coverage":          "invalid_work",
		"bad-dependency":            "invalid_work",
		"provider-ambiguity":        "provider_observation_conflict",
		"stale-generation":          "current_generation_conflict",
		"incomplete-reconciliation": "invalid_reconciliation",
		"unsafe-output":             "unsafe_output_path",
		"unknown-definition":        "unsupported_definition",
		"corrupt-snapshot":          "observation_corrupt",
	}
	for name, want := range expected {
		response := responses[name]
		if response.Error == nil || response.Error.Kind != want {
			t.Errorf("negative %s response=%+v want kind=%q", name, response, want)
			continue
		}
		assertBoundedRefusalEvidence(t, name, response)
		if len(response.AggregateIDs) == 0 {
			t.Errorf("negative %s omitted affected aggregate identities", name)
		}
	}
	for name, response := range responses {
		if response.Error == nil || len(response.Error.Observed) == 0 || len(response.Error.Expected) == 0 {
			t.Errorf("negative %s omitted safe observed/expected state: %+v", name, response.Error)
		}
	}

	// Provider revision-only noise, human status, and assignment are explicit
	// non-authority controls. They may change provider observation identity but
	// cannot change the mission aggregate, its current revision, or Next.
	control, err := sqlite.Open(ctx, result.Database)
	if err != nil {
		t.Fatal(err)
	}
	missionBefore, err := control.LoadMission(ctx, result.MissionTwo.MissionID)
	if err != nil {
		control.Close()
		t.Fatal(err)
	}
	revisionBefore, err := control.WorkRevisionV2(ctx, result.GenerationTwo.ID())
	_ = control.Close()
	if err != nil {
		t.Fatal(err)
	}
	revisionBeforeBytes, _ := revisionBefore.Encode()
	store, err := fake.Open(result.TrackerStore)
	if err != nil {
		t.Fatal(err)
	}
	ref := cloneRequestMap(t, result.GenerationTwoRef)
	if _, err = store.HumanSetStatus(ctx, ref["external_id"].(string), "in_progress"); err != nil {
		t.Fatal(err)
	}
	if err = store.HumanAssign(ctx, ref["external_id"].(string), "human-owner"); err != nil {
		t.Fatal(err)
	}
	reopenedProvider, err := fake.Open(result.TrackerStore)
	if err != nil {
		t.Fatal(err)
	}
	providerState, err := reopenedProvider.ObserveHumanState(ctx, ref["external_id"].(string))
	if err != nil || providerState.Status != "in_progress" || providerState.Assignee != "human-owner" || providerState.Revision == 0 {
		t.Fatalf("durable provider status/assignment state=%+v err=%v", providerState, err)
	}
	noisyCandidate := append(json.RawMessage(nil), result.GenerationTwoPublic...)
	mutateRevisionNoise(t, &noisyCandidate, result.GenerationTwo.ID().String(), "revision-provider-noise", fmt.Sprint(providerState.Revision))
	ref["last_observation"] = providerState.Revision
	noise := callMethodRaw(t, ctx, "work", "drift", map[string]any{"database": result.Database, "tracker_store": result.TrackerStore, "correlation_id": "correlation-provider-noise", "mission_id": result.MissionTwo.MissionID, "old_revision": result.GenerationTwoPublic, "candidate_revision": noisyCandidate, "provider_refs": []map[string]any{ref}, "observed_at": at})
	var noiseResult struct {
		Status workgraph.SemanticStatus `json:"semantic_status"`
	}
	if !noise.OK {
		t.Fatalf("provider noise refused: %+v", noise)
	}
	decodeMethodResult(t, noise, &noiseResult)
	if noiseResult.Status != workgraph.SemanticObservationOnly {
		t.Fatalf("provider noise status=%s", noiseResult.Status)
	}
	control, err = sqlite.Open(ctx, result.Database)
	if err != nil {
		t.Fatal(err)
	}
	missionAfter, err := control.LoadMission(ctx, result.MissionTwo.MissionID)
	if err != nil {
		control.Close()
		t.Fatal(err)
	}
	revisionAfter, err := control.WorkRevisionV2(ctx, result.GenerationTwo.ID())
	_ = control.Close()
	if err != nil {
		t.Fatal(err)
	}
	revisionAfterBytes, _ := revisionAfter.Encode()
	if !reflect.DeepEqual(missionBefore, missionAfter) || missionAfter.StateVersion != missionBefore.StateVersion || missionAfter.CurrentWorkRevisionID != result.GenerationTwo.ID() || !bytes.Equal(revisionBeforeBytes, revisionAfterBytes) {
		t.Fatalf("provider status/assignment changed authority: mission before=%+v after=%+v revision_equal=%v", missionBefore, missionAfter, bytes.Equal(revisionBeforeBytes, revisionAfterBytes))
	}
	afterAssignment := callMethod(t, ctx, "mission", "next", map[string]any{"database": result.Database, "correlation_id": "correlation-after-assignment", "mission_id": result.MissionTwo.MissionID})
	if !jsonEqual(afterAssignment.Result, result.NextTwoPublic) {
		t.Fatalf("tracker assignment changed next: before=%s after=%s", result.NextTwoPublic, afterAssignment.Result)
	}
}

func assertBoundedRefusalEvidence(t *testing.T, name string, response methodEnvelope) {
	t.Helper()
	if response.OK || response.Error == nil || response.RetrySafety == "" || len(response.RetrySafety) > 64 || response.Recovery == "" || len(response.Recovery) > 128 || response.Error.Recovery != response.Recovery || response.Error.Message == "" || len(response.Error.Message) > 512 || len(response.Error.Field) > 128 {
		t.Errorf("negative %s has incomplete or unbounded refusal evidence: %+v", name, response)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"credential", "password", "private transcript", "provider raw output", "stderr", "environment="} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("negative %s leaked forbidden diagnostic content %q", name, forbidden)
		}
	}
	for _, states := range []map[string]string{response.Error.Observed, response.Error.Expected} {
		for key, value := range states {
			if key == "" || len(key) > 128 || value == "" || len(value) > 128 || strings.ContainsAny(key+value, "\r\n\x00") {
				t.Errorf("negative %s has unbounded state evidence %q=%q", name, key, value)
			}
		}
	}
}

func mutateRevisionNoise(t *testing.T, revision *json.RawMessage, predecessor, id, providerRevision string) {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(*revision, &object); err != nil {
		t.Fatal(err)
	}
	object["work_revision_id"], object["generation"], object["predecessor_id"] = id, float64(3), predecessor
	observations := object["provider_observations"].([]any)
	observations[0].(map[string]any)["revision"] = providerRevision
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	*revision = encoded
}

func jsonEqual(left, right []byte) bool {
	var l, r any
	return json.Unmarshal(left, &l) == nil && json.Unmarshal(right, &r) == nil && reflect.DeepEqual(l, r)
}

type freshWorkSummary struct {
	WorkID, Objective, Description, DesignContext string
	Dependencies                                  []string
	Criteria                                      []string
}

type freshMissionSummary struct {
	Generation         uint64
	WorkRevisionID     string
	PlanningSource     string
	SourcePlanRevision string
	SourcePlanDigest   string
	NextAction         mission.NextKind
	ReadyWork          []string
	Work               []freshWorkSummary
}

func summarizePublicMission(revisionJSON json.RawMessage, plan artifactRevisionView, nextJSON json.RawMessage) (freshMissionSummary, error) {
	revision, err := workgraph.DecodeRevisionV2(revisionJSON)
	if err != nil {
		return freshMissionSummary{}, err
	}
	if plan.ID == "" || plan.Kind != "plan" || plan.RevisionNumber == 0 || len(plan.ContentDigest) != 64 || revision.Generation() == 0 || revision.PlanningSourceCommit() == "" {
		return freshMissionSummary{}, fmt.Errorf("public source identity is incomplete")
	}
	var next mission.NextResult
	if err = json.Unmarshal(nextJSON, &next); err != nil || next.Validate() != nil || next.WorkRevisionID != revision.ID() {
		return freshMissionSummary{}, fmt.Errorf("public next result is incomplete")
	}
	result := freshMissionSummary{Generation: revision.Generation(), WorkRevisionID: revision.ID().String(), PlanningSource: revision.PlanningSourceCommit(), SourcePlanRevision: plan.ID, SourcePlanDigest: plan.ContentDigest, NextAction: next.Kind}
	for _, ready := range next.ReadyWorkIDs {
		result.ReadyWork = append(result.ReadyWork, ready.String())
	}
	for _, item := range revision.WorkItems() {
		if item.WorkID() == "" || item.Title() == "" || item.Description() == "" || item.DesignContext() == "" || len(item.Criteria()) == 0 {
			return freshMissionSummary{}, fmt.Errorf("public work context is incomplete")
		}
		work := freshWorkSummary{WorkID: item.WorkID().String(), Objective: item.Title(), Description: item.Description(), DesignContext: item.DesignContext()}
		for _, dependency := range item.Dependencies() {
			work.Dependencies = append(work.Dependencies, dependency.String())
		}
		for _, criterion := range item.Criteria() {
			metadata := criterion.Metadata()
			outcome, outcomeOK := metadata[workgraph.CriterionMetadataOutcome].(string)
			if criterion.ID() == "" || criterion.Text() == "" || criterion.VerifierKind() == "" || outcome == "" || !outcomeOK {
				return freshMissionSummary{}, fmt.Errorf("public criterion context is incomplete")
			}
			work.Criteria = append(work.Criteria, criterion.ID()+":"+criterion.Text()+":"+criterion.VerifierKind()+":"+outcome)
		}
		result.Work = append(result.Work, work)
	}
	return result, nil
}

type publicSummaryMutant func(*testing.T, *json.RawMessage, *artifactRevisionView, *json.RawMessage)

func publicSummaryOmissionMutants() map[string]publicSummaryMutant {
	mutateRevision := func(path ...string) publicSummaryMutant {
		return func(t *testing.T, revision *json.RawMessage, _ *artifactRevisionView, _ *json.RawMessage) {
			mutatePublicObject(t, revision, path...)
		}
	}
	mutateNext := func(field string) publicSummaryMutant {
		return func(t *testing.T, _ *json.RawMessage, _ *artifactRevisionView, next *json.RawMessage) {
			mutatePublicObject(t, next, field)
		}
	}
	return map[string]publicSummaryMutant{
		"generation":           mutateRevision("generation"),
		"work-revision":        mutateRevision("work_revision_id"),
		"planning-source":      mutateRevision("planning_source_commit"),
		"work-items":           mutateRevision("work_items"),
		"objective":            mutateRevision("work_items", "0", "title"),
		"description":          mutateRevision("work_items", "0", "description"),
		"design-context":       mutateRevision("work_items", "0", "design_context"),
		"dependencies":         mutateRevision("work_items", "0", "dependencies"),
		"criteria":             mutateRevision("work_items", "0", "criteria"),
		"criterion-text":       mutateRevision("work_items", "0", "criteria", "0", "text"),
		"criterion-verifier":   mutateRevision("work_items", "0", "criteria", "0", "verifier_kind"),
		"criterion-outcome":    mutateRevision("work_items", "0", "criteria", "0", "metadata"),
		"source-plan-revision": func(_ *testing.T, _ *json.RawMessage, plan *artifactRevisionView, _ *json.RawMessage) { plan.ID = "" },
		"source-plan-digest": func(_ *testing.T, _ *json.RawMessage, plan *artifactRevisionView, _ *json.RawMessage) {
			plan.ContentDigest = ""
		},
		"source-plan-generation": func(_ *testing.T, _ *json.RawMessage, plan *artifactRevisionView, _ *json.RawMessage) {
			plan.RevisionNumber = 0
		},
		"next-kind":       mutateNext("kind"),
		"next-revision":   mutateNext("work_revision_id"),
		"next-ready-work": mutateNext("ready_work_ids"),
	}
}

func mutatePublicObject(t *testing.T, raw *json.RawMessage, path ...string) {
	t.Helper()
	var value any
	if err := json.Unmarshal(*raw, &value); err != nil {
		t.Fatal(err)
	}
	current := value
	for _, component := range path[:len(path)-1] {
		switch typed := current.(type) {
		case map[string]any:
			current = typed[component]
		case []any:
			if component != "0" || len(typed) == 0 {
				t.Fatalf("unsupported mutant path %v", path)
			}
			current = typed[0]
		default:
			t.Fatalf("invalid mutant path %v", path)
		}
	}
	object, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("mutant target %v is not an object", path)
	}
	delete(object, path[len(path)-1])
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	*raw = encoded
}
