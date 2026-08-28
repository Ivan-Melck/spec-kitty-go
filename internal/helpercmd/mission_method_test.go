package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestMissionInitializePersistsExactCreationAuthorityAndReplaysAfterReopen(t *testing.T) {
	definition, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	request := initializeRequest{
		Database: filepath.Join(t.TempDir(), "control.db"), CorrelationID: "correlation-initialize",
		MissionID: "mission-method-initialize", ProjectID: "project-method", DefinitionID: definition.ID(),
		DefinitionVersion: definition.Version(), OperationID: "operation-method-initialize", ActorID: "actor-method",
		ProcessID: "process-method", Governance: methodGovernance(t, "governance-method", at), OccurredAt: at,
	}
	first := invokeMethod(t, RunMission, []string{"initialize", "--request-json", marshalMethod(t, request)})
	second := invokeMethod(t, RunMission, []string{"initialize", "--request-json", marshalMethod(t, request)})
	if !first.OK || first.Kind != "mission_initialized" || first.RetrySafety != "replay_same_operation" || first.Recovery != "none" || !second.OK || second.Kind != first.Kind {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	firstResult := resultObject(t, first)
	secondResult := resultObject(t, second)
	if firstResult["actor_id"] != "actor-method" || firstResult["process_id"] != "process-method" || firstResult["governance_context_id"] != "governance-method" || secondResult["replayed"] != true {
		t.Fatalf("first result=%v second result=%v", firstResult, secondResult)
	}

	changed := request
	changed.ProcessID = "process-method-other"
	conflict := invokeMethod(t, RunMission, []string{"initialize", "--request-json", marshalMethod(t, changed)})
	if conflict.OK || conflict.Error == nil || conflict.Error.Kind != "idempotency_conflict" {
		t.Fatalf("changed replay=%+v", conflict)
	}
}

func TestStrictMethodJSONRejectsUnknownDuplicateNullTrailingAndLeftover(t *testing.T) {
	cases := map[string][]string{
		"unknown":   {"next", "--request-json", `{"database":"db","correlation_id":"c","mission_id":"mission-01","extra":1}`},
		"duplicate": {"next", "--request-json", `{"database":"db","database":"other","correlation_id":"c","mission_id":"mission-01"}`},
		"null":      {"next", "--request-json", `{"database":"db","correlation_id":null,"mission_id":"mission-01"}`},
		"trailing":  {"next", "--request-json", `{"database":"db","correlation_id":"c","mission_id":"mission-01"} []`},
		"leftover":  {"next", "--request-json", `{"database":"db","correlation_id":"c","mission_id":"mission-01"}`, "unexpected"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			response := invokeMethodUsage(t, RunMission, args)
			if response.OK || response.Kind != "invalid_invocation" || response.Error == nil || response.Error.Message == "" || len(response.Error.Message) > 512 || response.RetrySafety == "" || response.Recovery == "" {
				t.Fatalf("response=%+v", response)
			}
		})
	}
}

func TestMissionInitializeMissingCreationProvenanceFailsBeforeStoreOpen(t *testing.T) {
	definition, _ := softwaredev.Definition()
	at := time.Date(2026, 8, 28, 15, 30, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*initializeRequest){
		"process":    func(request *initializeRequest) { request.ProcessID = "" },
		"governance": func(request *initializeRequest) { request.Governance = governancecontext.Context{} },
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.db")
			request := initializeRequest{Database: path, CorrelationID: "correlation-missing-" + name, MissionID: ids.MissionID("mission-missing-" + name), ProjectID: "project-missing", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: ids.OperationID("operation-missing-" + name), ActorID: "actor-method", ProcessID: "process-method", Governance: methodGovernance(t, "governance-missing-"+name, at), OccurredAt: at}
			mutate(&request)
			args := []string{"initialize", "--request-json", marshalMethod(t, request)}
			response := MethodResponse{}
			if name == "governance" {
				response = invokeMethodUsage(t, RunMission, args)
			} else {
				response = invokeMethod(t, RunMission, args)
			}
			if response.OK || response.Error == nil {
				t.Fatalf("response=%+v", response)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("store opened for missing provenance: %v", err)
			}
		})
	}
}

func TestMutatingMissionMethodCommandsValidateBeforeOpeningAnyPath(t *testing.T) {
	definition, _ := softwaredev.Definition()
	at := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	missionAt := func(step mission.StepKey, version uint64) mission.MissionSnapshot {
		return mission.MissionSnapshot{MissionID: "mission-preopen", ProjectID: "project-preopen", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), DefinitionCommitment: definition.Commitment(), Lifecycle: mission.LifecycleOpen, CurrentStep: step, StateVersion: version, CreatedAt: at, UpdatedAt: at}
	}
	work := []workDefinitionInput{{WorkID: "work-preopen", Title: "Validate before opening", Description: "Reject malformed transport values before durable composition.", DesignContext: "The helper is a transport boundary.", Criteria: []tracker.WorkCriterion{{ID: "criterion-preopen", Text: "No path is created", Outcome: "invalid input leaves paths absent", Verifier: tracker.VerifierTest, Required: true}}, WriteScopes: []tracker.WriteScope{{Path: "internal/preopen", Domain: "preopen", Access: tracker.ScopeWriteExclusive}}, Provenance: []tracker.ScopeRef{{ID: "plan-preopen", Kind: tracker.ScopePlanConcern}}}}
	approved := []approvedScopeInput{{ID: "plan-preopen", Kind: workgraph.ScopePlanConcern}}

	for _, test := range []struct {
		name    string
		invoke  func(database, trackerPath string) MethodResponse
		tracker bool
	}{
		{name: "transition", invoke: func(database, _ string) MethodResponse {
			current := missionAt(mission.StepInitialized, 1)
			return invokeMethod(t, func(ctx context.Context, _ []string, stdout, _ io.Writer) int {
				return runMissionTransition(ctx, transitionRequest{Database: database, CorrelationID: "correlation-preopen-transition", MissionID: current.MissionID, DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), DefinitionCommitment: definition.Commitment(), ExpectedSnapshot: current, ExpectedCurrentStep: current.CurrentStep, ExpectedStateVersion: current.StateVersion, Successor: mission.StepSpecifying, EventID: "event-preopen", OperationID: "operation-preopen-transition", ActorID: "actor-preopen", GovernanceContextID: "governance-preopen", OccurredAt: time.Time{}}, stdout)
			}, nil)
		}},
		{name: "decompose", tracker: true, invoke: func(database, trackerPath string) MethodResponse {
			return invokeMethod(t, func(ctx context.Context, _ []string, stdout, _ io.Writer) int {
				return runMissionDecompose(ctx, decomposeRequest{Database: database, TrackerStore: trackerPath, CorrelationID: "correlation-preopen-decompose", MissionID: "mission-preopen", ProjectID: "project-preopen", Provider: "durable_fake", ExpectedMission: missionAt(mission.StepPlanApproved, 2), PlanRevisionID: "plan-preopen", PlanContentDigest: strings.Repeat("a", 64), ApprovedScope: approved, Work: work, ActorID: "actor-preopen", OperationID: "operation-preopen-decompose", GovernanceContextID: "governance-preopen", OccurredAt: time.Time{}}, stdout)
			}, nil)
		}},
		{name: "finalize", tracker: true, invoke: func(database, trackerPath string) MethodResponse {
			return invokeMethod(t, func(ctx context.Context, _ []string, stdout, _ io.Writer) int {
				return runWorkFinalize(ctx, finalizeRequest{Database: database, TrackerStore: trackerPath, CorrelationID: "correlation-preopen-finalize", MissionID: "mission-preopen", ProjectID: "project-preopen", ExpectedMission: missionAt(mission.StepWorkDraftReady, 3), ProposedRevisionID: "revision-preopen", PlanningSourceCommit: strings.Repeat("b", 40), PlanRevisionID: "plan-preopen", PlanDigest: strings.Repeat("a", 64), ApprovedScope: approved, Work: work, ProviderRefs: []providerRefInput{{WorkID: "work-preopen", ExternalID: "external-preopen", MetadataSchema: tracker.WorkDefinitionMetadataSchemaV1, CreatedByOperation: "operation-provider-preopen", LastObservation: 1}}, ActorID: "actor-preopen", OperationID: "operation-preopen-finalize", GovernanceContextID: "governance-preopen", OccurredAt: time.Time{}}, stdout)
			}, nil)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			database, trackerPath := filepath.Join(root, "absent", "control.db"), filepath.Join(root, "absent-provider", "tracker.json")
			response := test.invoke(database, trackerPath)
			if response.OK || response.Error == nil {
				t.Fatalf("response=%+v", response)
			}
			for _, path := range []string{database, trackerPath} {
				if !test.tracker && path == trackerPath {
					continue
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("path %q opened for malformed request: %v", path, err)
				}
			}
		})
	}
}

func TestProviderVerifiedDriftRejectsSemanticSubstitutionAndAcceptsRevisionNoise(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)
	metadata, _ := tracker.NewOpaqueMetadata([]byte(`{}`))
	criterion := tracker.WorkCriterion{ID: "criterion-provider", Text: "Observe provider truth", Outcome: "provider semantics are exact", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "run provider checks"}
	definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{ProjectID: "project-provider", WorkID: "work-provider", Title: "Provider title", Description: "Provider-owned semantic description.", DesignContext: "The durable provider is observed directly.", Criteria: []tracker.WorkCriterion{criterion}, WriteScopes: []tracker.WriteScope{{Path: "internal/provider", Domain: "provider", Access: tracker.ScopeWriteExclusive}}, Provenance: []tracker.ScopeRef{{ID: "plan-provider", Kind: tracker.ScopePlanConcern}}, MetadataSchema: tracker.WorkDefinitionMetadataSchemaV1, ProviderMetadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	providerStore, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(providerStore)
	mutation, err := provider.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: "operation-provider-create", Definition: definition})
	if err != nil {
		t.Fatal(err)
	}
	ref := mutation.Observation.ExternalRef()
	observation := mutation.Observation.ProviderObservation()
	semantic, _ := semanticWorkObservation(definition)
	draft, err := workgraph.NewDraftObservation("project-provider", "mission-provider", "plan-provider", strings.Repeat("a", 64), []workgraph.ApprovedScope{{ID: "plan-provider", Kind: workgraph.ScopePlanConcern, MissionID: "mission-provider"}}, []workgraph.WorkObservation{semantic})
	if err != nil {
		t.Fatal(err)
	}
	lanes, err := workgraph.CollapseLanes(draft)
	if err != nil {
		t.Fatal(err)
	}
	baseWork := workItemV2ParamsFromDefinition(definition)
	newCandidate := func(id ids.WorkRevisionID, generation uint64, predecessor ids.WorkRevisionID, revision tracker.Revision, work workgraph.WorkItemV2Params) workgraph.RevisionV2 {
		value, buildErr := workgraph.NewRevisionV2(workgraph.RevisionV2Params{ID: id, ProjectID: "project-provider", MissionID: "mission-provider", Generation: generation, PredecessorID: predecessor, PlanningSourceCommit: strings.Repeat("b", 40), MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("c", 64)}, DependencyPolicy: workgraph.DependencyApprovedOrDone, WorkItems: []workgraph.WorkItemV2Params{work}, Lanes: lanes, ProviderObservations: []workgraph.ProviderObservationV2{{WorkID: "work-provider", Provider: "durable_fake", ExternalID: ref.ExternalID, Revision: fmt.Sprint(revision)}}, FinalizedBy: "actor-provider", FinalizedAt: at, GovernanceContextID: "governance-provider"})
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		return value
	}
	old := newCandidate("revision-provider-1", 1, "", observation.Revision, baseWork)
	if err = providerStore.HumanAssign(ctx, ref.ExternalID, "human-owner"); err != nil {
		t.Fatal(err)
	}
	current := observation.Revision + 1
	input := []providerRefInput{{WorkID: ref.WorkID, ExternalID: ref.ExternalID, MetadataSchema: ref.MetadataSchema, CreatedByOperation: ref.CreatedByOperation, LastObservation: current}}
	noise := newCandidate("revision-provider-2", 2, old.ID(), current, baseWork)
	if err = verifyProviderCandidate(ctx, noise, input, provider); err != nil {
		t.Fatalf("revision-only provider noise refused: %v", err)
	}
	report, err := workgraph.DetectDrift(old, noise, at.Add(time.Second))
	if err != nil || report.SemanticStatus() != workgraph.SemanticObservationOnly {
		t.Fatalf("noise report=%+v err=%v", report, err)
	}

	mutations := map[string]func(*workgraph.WorkItemV2Params){
		"title":          func(item *workgraph.WorkItemV2Params) { item.Title = "Caller substituted title" },
		"description":    func(item *workgraph.WorkItemV2Params) { item.Description = "Caller substituted description." },
		"design_context": func(item *workgraph.WorkItemV2Params) { item.DesignContext = "Caller substituted design context." },
		"criterion_text": func(item *workgraph.WorkItemV2Params) { item.Criteria[0].Text = "Caller substituted criterion" },
		"criterion_outcome": func(item *workgraph.WorkItemV2Params) {
			item.Criteria[0].Outcome = "caller substituted outcome"
		},
		"criterion_required": func(item *workgraph.WorkItemV2Params) { item.Criteria[0].Required = false },
		"criterion_verifier": func(item *workgraph.WorkItemV2Params) {
			item.Criteria[0].VerifierKind = tracker.VerifierReview.String()
		},
		"criterion_evidence": func(item *workgraph.WorkItemV2Params) { item.Criteria[0].EvidenceHint = "caller hint" },
		"criterion_metadata": func(item *workgraph.WorkItemV2Params) { item.Criteria[0].Metadata = map[string]any{"caller": "value"} },
		"scope_path":         func(item *workgraph.WorkItemV2Params) { item.WriteScopes[0].Path = "internal/substituted" },
		"scope_domain":       func(item *workgraph.WorkItemV2Params) { item.WriteScopes[0].Domain = "substituted" },
		"scope_access":       func(item *workgraph.WorkItemV2Params) { item.WriteScopes[0].Access = workgraph.ScopeReadOnly },
		"scope_serialization": func(item *workgraph.WorkItemV2Params) {
			item.WriteScopes[0].SerializationKey = "substituted"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := baseWork
			changed.Criteria = append([]workgraph.CriterionV2Params(nil), baseWork.Criteria...)
			changed.WriteScopes = append([]workgraph.NormalizedWriteScope(nil), baseWork.WriteScopes...)
			mutate(&changed)
			candidate := newCandidate(ids.WorkRevisionID("revision-provider-mutant-"+name), 2, old.ID(), current, changed)
			if err := verifyProviderCandidate(ctx, candidate, input, provider); err == nil {
				t.Fatal("caller-authored semantic substitution was accepted")
			}
		})
	}
}

func TestMissionMethodRefusalsExposeBoundedStateAndStableAdapterClassification(t *testing.T) {
	definition, _ := softwaredev.Definition()
	at := time.Date(2026, 8, 28, 18, 0, 0, 0, time.UTC)
	unsupported := initializeRequest{Database: "unused", CorrelationID: "correlation-state", MissionID: "mission-state", ProjectID: "project-state", DefinitionID: "unknown-definition", DefinitionVersion: 9, OperationID: "operation-state", ActorID: "actor-state", ProcessID: "process-state", Governance: methodGovernance(t, "governance-state", at), OccurredAt: at}
	response := invokeMethod(t, RunMission, []string{"initialize", "--request-json", marshalMethod(t, unsupported)})
	if response.OK || response.Error == nil || response.Error.Kind != "unsupported_definition" || response.Error.Observed["definition_id"] != "unknown-definition" || response.Error.Expected["definition_id"] != definition.ID().String() {
		t.Fatalf("unsupported response=%+v", response)
	}

	root := t.TempDir()
	blockingPath := filepath.Join(root, "provider-secret-output")
	if err := os.WriteFile(blockingPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(blockingPath, "credential-control.db")
	response = invokeMethod(t, RunMission, []string{"next", "--request-json", marshalMethod(t, nextRequest{Database: database, CorrelationID: "correlation-adapter", MissionID: "mission-adapter"})})
	encoded, _ := json.Marshal(response)
	if response.OK || response.Error == nil || response.Error.Kind != "adapter_unavailable" || response.Error.Recovery != "retry_same_operation" || !response.Error.RetrySafe || strings.Contains(string(encoded), database) || strings.Contains(string(encoded), "credential-control") {
		t.Fatalf("adapter response=%s", encoded)
	}

	secret := "provider transcript secret\nraw output"
	diagnostic := boundedMethodDiagnostic(methodStateError("provider_failure", "observe_again", "provider", "bounded refusal", map[string]string{"state": secret}, map[string]string{"state": "current"}, true))
	encoded, _ = json.Marshal(diagnostic)
	if strings.Contains(string(encoded), secret) || diagnostic.Observed != nil || diagnostic.Expected["state"] != "current" {
		t.Fatalf("unbounded diagnostic=%s", encoded)
	}
}

func invokeMethod(t *testing.T, run func(context.Context, []string, io.Writer, io.Writer) int, args []string) MethodResponse {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response MethodResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", stdout.String(), err)
	}
	return response
}

func invokeMethodUsage(t *testing.T, run func(context.Context, []string, io.Writer, io.Writer) int, args []string) MethodResponse {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response MethodResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", stdout.String(), err)
	}
	return response
}

func marshalMethod(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func resultObject(t *testing.T, response MethodResponse) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func methodGovernance(t *testing.T, id string, at time.Time) governancecontext.Context {
	t.Helper()
	commitment, err := governancecontext.NewCommitment(governancecontext.AlgorithmSHA256, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := governancecontext.NewEntry(governancecontext.KindProfile, "implementer-ivan", "1", commitment)
	if err != nil {
		t.Fatal(err)
	}
	context, err := governancecontext.NewContext(ids.GovernanceContextID(id), governancecontext.SchemaVersion, []governancecontext.Entry{entry}, at)
	if err != nil {
		t.Fatal(err)
	}
	return context
}
