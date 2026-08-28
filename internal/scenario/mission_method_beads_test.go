package scenario

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/beads"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

// This portable campaign is the semantic control for every provider-specific
// qualification. It deliberately requires neither Beads nor provider
// environment variables, so an absent real provider cannot hide regressions.
func TestMissionMethodProviderParityPortableCampaign(t *testing.T) {
	result := runPortableMissionFixture(t, "provider-parity")
	if result.GenerationOne.Generation() != 1 || result.GenerationTwo.Generation() != 2 || result.GenerationTwo.PredecessorID() != result.GenerationOne.ID() {
		t.Fatalf("portable lineage one=%s two=%s predecessor=%s", result.GenerationOne.ID(), result.GenerationTwo.ID(), result.GenerationTwo.PredecessorID())
	}
	if result.MissionOne.TerminalOutcome != mission.OutcomeWorkFinalized || result.MissionTwo.TerminalOutcome != mission.OutcomeWorkFinalized {
		t.Fatalf("provider work changed mission authority: one=%s two=%s", result.MissionOne.TerminalOutcome, result.MissionTwo.TerminalOutcome)
	}
	if result.GenerationOne.GraphDigest() == result.GenerationTwo.GraphDigest() || result.GenerationTwo.Generation() != 2 {
		t.Fatal("semantic provider mutation did not require an explicit successor generation")
	}
	if result.GenerationTwo.WorkItems()[0].WorkID() != "work-a-v2" || result.MissionTwo.CurrentWorkRevisionID != result.GenerationTwo.ID() {
		t.Fatalf("explicit successor was not adopted: mission=%s revision=%s", result.MissionTwo.CurrentWorkRevisionID, result.GenerationTwo.ID())
	}
}

func TestMissionMethodBeadsDishonestProviderRefusesBeforePublication(t *testing.T) {
	executable := buildDishonestExecutable(t)
	cases := []struct {
		name     string
		steps    func(*testing.T, string, []dishonestStep) []dishonestStep
		wantKind mission.FinalizationErrorKind
		cancel   bool
	}{
		{"unavailable", dishonestFirstListUnavailable, mission.FinalizationProviderFailure, false},
		{"timeout", dishonestFirstListTimeout, mission.FinalizationProviderFailure, true},
		{"malformed", dishonestFirstListMalformed, mission.FinalizationProviderFailure, false},
		{"schema-skew", dishonestFirstListSchemaSkew, mission.FinalizationProviderFailure, false},
		{"wrong-identity", dishonestFirstListWrongIdentity, mission.FinalizationProviderFailure, false},
		{"omitted-work", dishonestSecondObservationOmitted, mission.FinalizationProviderChanged, false},
		{"additional-work", dishonestSecondObservationAdditional, mission.FinalizationProviderChanged, false},
		{"reversed-dependency", dishonestFirstObservationReversedDependency, mission.FinalizationProviderFailure, false},
		{"stale-content", dishonestSecondObservationStale, mission.FinalizationProviderChanged, false},
		{"between-observation-mutation", dishonestSecondObservationChanged, mission.FinalizationProviderChanged, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			valid := dishonestMissionObservationSteps(t, "clean", "")
			steps := test.steps(t, root, valid)
			fixture := newDishonestFixtureAt(t, root, executable, steps, nil)
			database, expected, command := prepareDishonestFinalizationMission(t, test.name)
			before := observeDishonestLocalAuthority(t, database, expected.MissionID)
			source := &dishonestMissionFinalizationSource{adapter: fixture.adapter, projectID: expected.ProjectID, missionID: expected.MissionID, planRevisionID: "artifact-plan-dishonest-" + test.name, planDigest: artifact.ContentCommitment([]byte("dishonest provider plan " + test.name))}
			serviceStore, err := sqlite.Open(context.Background(), database)
			if err != nil {
				t.Fatal(err)
			}
			service := mission.NewFinalizationService(source, serviceStore)
			var result mission.FinalizationResult
			if test.cancel {
				err = cancelAtDishonestBarrier(t, root, "mission-timeout", func(ctx context.Context) error {
					_, callErr := service.Finalize(ctx, command)
					return callErr
				})
				clean := append(dishonestStepsWithPrefix(valid, "retry-one"), dishonestStepsWithPrefix(valid, "retry-two")...)
				for index := range clean {
					clean[index].Workspace = root
					clean[index].RequiredEnv = safeDishonestEnvironment(root)
				}
				writeDishonestScenario(t, root, dishonestScenario{SchemaVersion: 1, Steps: clean})
			} else {
				result, err = service.Finalize(context.Background(), command)
			}
			var typed *mission.FinalizationError
			if !errors.As(err, &typed) || typed.Kind != test.wantKind || typed.Recovery != mission.FinalizationObserveAgain {
				t.Fatalf("result=%+v error=%T %v", result, err, err)
			}
			after := observeDishonestLocalAuthority(t, database, expected.MissionID)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("provider refusal changed local authority:\nbefore=%+v\nafter=%+v", before, after)
			}
			retry, retryErr := service.Finalize(context.Background(), command)
			if retryErr != nil || retry.Replayed || retry.Revision.Generation() != 1 || retry.Mission.CurrentWorkRevisionID != retry.Revision.ID() {
				runtime, runtimeErr := loadDishonestRuntime(root)
				t.Fatalf("clean retry=%+v error=%v runtime=%+v runtime_error=%v", retry, retryErr, runtime, runtimeErr)
			}
			if source.observations < 2 {
				t.Fatalf("clean retry did not observe afresh: observations=%d", source.observations)
			}
			if closeErr := serviceStore.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		})
	}
}

func TestMissionMethodBeadsSlowObservationCannotPublishBeforeResponse(t *testing.T) {
	executable := buildDishonestExecutable(t)
	root := t.TempDir()
	first := dishonestMissionObservationSteps(t, "slow-first", "")
	first[0].WaitFor = "mission-slow"
	first[0].WaitTiming = "after_apply"
	second := dishonestStepsWithPrefix(dishonestMissionObservationSteps(t, "clean", ""), "slow-second")
	fixture := newDishonestFixtureAt(t, root, executable, append(first, second...), nil)
	database, expected, command := prepareDishonestFinalizationMission(t, "slow")
	before := observeDishonestLocalAuthority(t, database, expected.MissionID)
	store, err := sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &dishonestMissionFinalizationSource{adapter: fixture.adapter, projectID: expected.ProjectID, missionID: expected.MissionID, planRevisionID: "artifact-plan-dishonest-slow", planDigest: artifact.ContentCommitment([]byte("dishonest provider plan slow"))}
	done := make(chan struct {
		result mission.FinalizationResult
		err    error
	}, 1)
	go func() {
		result, callErr := mission.NewFinalizationService(source, store).Finalize(context.Background(), command)
		done <- struct {
			result mission.FinalizationResult
			err    error
		}{result, callErr}
	}()
	waitForDishonestBarrier(t, root, "mission-slow")
	if during := observeDishonestLocalAuthority(t, database, expected.MissionID); !reflect.DeepEqual(before, during) {
		t.Fatalf("slow provider published before its response: before=%+v during=%+v", before, during)
	}
	if err = os.WriteFile(filepath.Join(root, "mission-slow"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-done:
		if outcome.err != nil || outcome.result.Revision.Generation() != 1 || outcome.result.Mission.CurrentWorkRevisionID != outcome.result.Revision.ID() {
			t.Fatalf("slow observation outcome=%+v error=%v", outcome.result, outcome.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("slow provider did not complete after explicit release")
	}
}

type dishonestMissionAuthority struct {
	Mission mission.MissionSnapshot
	Rows    methodRowCounts
}

func observeDishonestLocalAuthority(t *testing.T, database string, missionID ids.MissionID) dishonestMissionAuthority {
	t.Helper()
	store, err := sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.LoadMission(context.Background(), missionID)
	if err != nil {
		t.Fatal(err)
	}
	return dishonestMissionAuthority{Mission: snapshot, Rows: missionMethodRowCounts(t, database)}
}

type dishonestMissionFinalizationSource struct {
	adapter        *beads.BoundAdapter
	projectID      ids.ProjectID
	missionID      ids.MissionID
	planRevisionID string
	planDigest     string
	observations   int
}

func (source *dishonestMissionFinalizationSource) ObserveFinalization(ctx context.Context, missionID ids.MissionID) (workgraph.FinalizationObservation, error) {
	if missionID != source.missionID {
		return workgraph.FinalizationObservation{}, fmt.Errorf("mission scope changed")
	}
	observed, err := source.adapter.ObserveDefinitionCatalog(ctx, source.projectID)
	if err != nil {
		return workgraph.FinalizationObservation{}, err
	}
	source.observations++
	return dishonestFinalizationObservation(source.projectID, missionID, source.planRevisionID, source.planDigest, observed)
}

func dishonestFinalizationObservation(projectID ids.ProjectID, missionID ids.MissionID, planRevisionID, planDigest string, observations []tracker.WorkDefinitionObservation) (workgraph.FinalizationObservation, error) {
	work := make([]workgraph.WorkObservation, len(observations))
	evidence := make([]workgraph.ObservationEvidence, len(observations))
	for index, observation := range observations {
		definition := observation.Definition()
		criteria := make([]workgraph.CriterionObservation, len(definition.Criteria()))
		hints := make(map[string]string, len(criteria))
		for criterionIndex, criterion := range definition.Criteria() {
			requirement := workgraph.CriterionOptional
			if criterion.Required {
				requirement = workgraph.CriterionRequired
			}
			criteria[criterionIndex] = workgraph.CriterionObservation{ID: criterion.ID, Text: criterion.Text, Outcome: criterion.Outcome, Verifier: criterion.Verifier.String(), Requirement: requirement}
			hints[criterion.ID] = criterion.EvidenceHint
		}
		scopes := make([]workgraph.WriteScopeObservation, len(definition.WriteScopes()))
		for scopeIndex, scope := range definition.WriteScopes() {
			scopes[scopeIndex] = workgraph.WriteScopeObservation{Path: scope.Path, Domain: scope.Domain, Access: workgraph.ScopeAccess(scope.Access), SerializationKey: scope.SerializationKey}
		}
		provenance := make([]workgraph.ScopeReference, len(definition.Provenance()))
		for provenanceIndex, reference := range definition.Provenance() {
			provenance[provenanceIndex] = workgraph.ScopeReference{ID: reference.ID, Kind: workgraph.ScopeKind(reference.Kind)}
		}
		var err error
		work[index], err = workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: definition.ProjectID(), WorkID: definition.WorkID(), Title: definition.Title(), Description: definition.Description(), DesignContext: definition.DesignContext(), CrossDomainReason: definition.CrossDomainReason(), Criteria: criteria, Dependencies: definition.Dependencies(), WriteScopes: scopes, Provenance: provenance})
		if err != nil {
			return workgraph.FinalizationObservation{}, err
		}
		digest := sha256.Sum256(definition.CanonicalData())
		reference, provider := observation.ExternalRef(), observation.ProviderObservation()
		evidence[index] = workgraph.ObservationEvidence{WorkID: definition.WorkID(), Provider: reference.Provider, ExternalID: reference.ExternalID, Revision: fmt.Sprint(provider.Revision), MetadataSchema: reference.MetadataSchema, DefinitionDigest: fmt.Sprintf("%x", digest), CriterionEvidenceHints: hints}
	}
	draft, err := workgraph.NewDraftObservation(projectID, missionID, planRevisionID, planDigest, []workgraph.ApprovedScope{{ID: "plan-core", Kind: workgraph.ScopePlanConcern, MissionID: missionID}}, work)
	if err != nil {
		return workgraph.FinalizationObservation{}, err
	}
	return workgraph.NewFinalizationObservation(draft, evidence)
}

func prepareDishonestFinalizationMission(t *testing.T, suffix string) (string, mission.MissionSnapshot, mission.FinalizationCommand) {
	t.Helper()
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "control.db")
	at := time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC)
	definition, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	governance := portableGovernance(t, "governance-dishonest-"+suffix, at)
	store, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	created, err := mission.NewService(registry, store).Create(ctx, mission.CreateRequest{MissionID: ids.MissionID("mission-dishonest-" + suffix), ProjectID: "project-fault", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: ids.OperationID("operation-create-dishonest-" + suffix), ActorID: "actor-author", ProcessID: ids.ProcessID("process-dishonest-" + suffix), Governance: governance, OccurredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	current := created.Instance
	publish := func(kind artifact.Kind, revisionID, content string, when time.Time) artifactRevisionView {
		policy, policyErr := artifact.PolicyFor(kind)
		if policyErr != nil {
			t.Fatal(policyErr)
		}
		envelope := callMethod(t, ctx, "artifact", "publish", map[string]any{
			"database": database, "correlation_id": "correlation-publish-" + revisionID,
			"operation_id": "operation-publish-" + revisionID, "artifact_revision_id": revisionID,
			"mission_id": current.MissionID, "kind": kind, "source_ref": string(kind) + ".md",
			"source_revision": strings.Repeat("a", 40), "created_by": "actor-author", "created_at": when,
			"content": []byte(content), "present_concerns": policy.RequiredConcerns(), "validated_at": when,
			"expected_latest_revision_id": "",
		})
		var published struct {
			Revision artifactRevisionView `json:"revision"`
		}
		decodeMethodResult(t, envelope, &published)
		callMethod(t, ctx, "artifact", "approve", map[string]any{
			"database": database, "correlation_id": "correlation-approve-" + revisionID,
			"operation_id": "operation-approve-" + revisionID, "approval_id": "approval-" + revisionID,
			"artifact_revision_id": revisionID, "verdict": artifact.VerdictApproved, "actor_id": "actor-approver",
			"evidence_digest": artifact.ContentCommitment([]byte("approval:" + revisionID)), "governance_context_id": governance.ID(), "submitted_at": when.Add(time.Second),
		})
		return published.Revision
	}
	current = dishonestTransitionMission(t, ctx, database, definition, current, mission.StepSpecifying, governance.ID(), suffix+"-specifying", at.Add(time.Second))
	publish(artifact.KindSpecification, "artifact-spec-dishonest-"+suffix, "dishonest provider specification "+suffix, at.Add(2*time.Second))
	current = dishonestTransitionMission(t, ctx, database, definition, current, mission.StepSpecificationApproved, governance.ID(), suffix+"-spec-approved", at.Add(4*time.Second))
	current = dishonestTransitionMission(t, ctx, database, definition, current, mission.StepPlanning, governance.ID(), suffix+"-planning", at.Add(5*time.Second))
	planID := "artifact-plan-dishonest-" + suffix
	planContent := "dishonest provider plan " + suffix
	publish(artifact.KindPlan, planID, planContent, at.Add(6*time.Second))
	current = dishonestTransitionMission(t, ctx, database, definition, current, mission.StepPlanApproved, governance.ID(), suffix+"-plan-approved", at.Add(8*time.Second))
	current = dishonestTransitionMission(t, ctx, database, definition, current, mission.StepDecomposing, governance.ID(), suffix+"-decomposing", at.Add(9*time.Second))
	current = dishonestTransitionMission(t, ctx, database, definition, current, mission.StepWorkDraftReady, governance.ID(), suffix+"-draft-ready", at.Add(10*time.Second))
	command := mission.FinalizationCommand{MissionID: current.MissionID, ProjectID: current.ProjectID, ExpectedMission: current, ExpectedCurrentGeneration: 0, ProposedRevisionID: ids.WorkRevisionID("revision-dishonest-" + suffix), RequestedGeneration: 1, PlanningSourceCommit: strings.Repeat("b", 40), ActorID: "actor-finalizer", OperationID: ids.OperationID("operation-finalize-dishonest-" + suffix), GovernanceContextID: governance.ID(), OccurredAt: at.Add(11 * time.Second)}
	return database, current, command
}

func dishonestTransitionMission(t *testing.T, ctx context.Context, database string, definition mission.MissionDefinition, current mission.MissionSnapshot, successor mission.StepKey, governanceID ids.GovernanceContextID, suffix string, at time.Time) mission.MissionSnapshot {
	t.Helper()
	store, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
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
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	result, err := mission.NewServiceWithArtifactApprovals(registry, store, store).Transition(ctx, mission.TransitionRequest{MissionID: current.MissionID, DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), DefinitionCommitment: definition.Commitment(), ExpectedSnapshot: current, ExpectedCurrentStep: current.CurrentStep, ExpectedStateVersion: current.StateVersion, Successor: successor, EventID: ids.MissionEventID("event-" + suffix), OperationID: ids.OperationID("operation-transition-" + suffix), ActorID: "actor-author", GovernanceContextID: governanceID, OccurredAt: at, Evidence: evidence})
	if err != nil {
		t.Fatalf("transition %s to %s: %v", current.CurrentStep, successor, err)
	}
	return result.Instance
}

func dishonestMissionDefinition(t *testing.T, workID ids.WorkID, dependencies []ids.WorkID, description string) tracker.WorkDefinition {
	t.Helper()
	definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: "project-fault", WorkID: workID, Title: "Implement " + workID.String(), Description: description,
		DesignContext: "Preserve provider-neutral authority.",
		Criteria:      []tracker.WorkCriterion{{ID: "criterion-" + workID.String(), Text: "exercise the bounded mission behavior", Outcome: "the durable behavior is independently verified", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "go test ./..."}},
		Dependencies:  dependencies, WriteScopes: []tracker.WriteScope{{Path: "internal/" + workID.String(), Domain: workID.String(), Access: tracker.ScopeWriteExclusive}},
		Provenance: []tracker.ScopeRef{{ID: "plan-core", Kind: tracker.ScopePlanConcern}}, MetadataSchema: tracker.WorkDefinitionMetadataSchemaV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func dishonestMissionIssue(t *testing.T, externalID string, definition tracker.WorkDefinition, operation ids.OperationID, schema uint16, project ids.ProjectID) string {
	t.Helper()
	digest := sha256.Sum256(definition.CanonicalData())
	metadata := map[string]any{"spec_kitty": map[string]any{
		"schema_version": schema, "project_id": project, "work_id": definition.WorkID(),
		"created_by_operation": operation, "last_operation": operation,
		"criteria": definition.Criteria(), "design_context": definition.DesignContext(), "cross_domain_reason": definition.CrossDomainReason(),
		"dependencies": definition.Dependencies(), "write_scopes": definition.WriteScopes(), "provenance": definition.Provenance(),
		"content_digest": fmt.Sprintf("%x", digest),
	}}
	issue := map[string]any{"id": externalID, "title": definition.Title(), "description": definition.Description(), "acceptance_criteria": "presentation only", "metadata": metadata, "assignee": "", "updated_at": "2026-08-28T14:00:00Z", "revision": "provider-token-" + externalID}
	encoded, err := json.Marshal(issue)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func dishonestMissionObservationSteps(t *testing.T, prefix, change string) []dishonestStep {
	t.Helper()
	foundationDescription := "Deliver the bounded foundation outcome."
	if change == "foundation-description" {
		foundationDescription = "Deliver a changed bounded foundation outcome."
	}
	foundation := dishonestMissionDefinition(t, "work-foundation", nil, foundationDescription)
	feature := dishonestMissionDefinition(t, "work-feature", []ids.WorkID{"work-foundation"}, "Deliver the bounded feature outcome.")
	foundationIssue := dishonestMissionIssue(t, "bd-a", foundation, "operation-create-foundation", 1, "project-fault")
	featureIssue := dishonestMissionIssue(t, "bd-b", feature, "operation-create-feature", 1, "project-fault")
	return dishonestCatalogObservationSteps(prefix, []string{foundationIssue, featureIssue}, map[string][]string{"bd-b": {"bd-a"}})
}

func dishonestCatalogObservationSteps(prefix string, issues []string, dependencies map[string][]string) []dishonestStep {
	steps := []dishonestStep{{ID: prefix + ".list", Argv: []string{"list", "--json"}, Stdout: envelope("[" + strings.Join(issues, ",") + "]")}}
	for _, issue := range issues {
		var wire struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(issue), &wire)
		edges := make([]string, 0, len(dependencies[wire.ID]))
		for _, blocker := range dependencies[wire.ID] {
			edges = append(edges, fmt.Sprintf(`{"id":%q,"dependency_type":"blocks"}`, blocker))
		}
		steps = append(steps, dishonestStep{ID: prefix + ".dependencies." + wire.ID, Argv: []string{"dep", "list", wire.ID, "--json"}, Stdout: envelope("[" + strings.Join(edges, ",") + "]")})
	}
	return steps
}

func withCleanDishonestRetry(fault, valid []dishonestStep) []dishonestStep {
	result := append([]dishonestStep(nil), fault...)
	result = append(result, dishonestStepsWithPrefix(valid, "retry-one")...)
	return append(result, dishonestStepsWithPrefix(valid, "retry-two")...)
}

func dishonestStepsWithPrefix(steps []dishonestStep, prefix string) []dishonestStep {
	result := append([]dishonestStep(nil), steps...)
	for index := range result {
		result[index].ID = prefix + "." + result[index].ID
	}
	return result
}

func dishonestFirstListUnavailable(_ *testing.T, _ string, valid []dishonestStep) []dishonestStep {
	fault := []dishonestStep{{ID: "fault.unavailable", Argv: []string{"list", "--json"}, Stdout: `{"schema_version":1,"error":"provider unavailable","code":"database_locked"}`, ExitCode: 7}}
	return withCleanDishonestRetry(fault, valid)
}

func dishonestFirstListTimeout(_ *testing.T, _ string, valid []dishonestStep) []dishonestStep {
	fault := []dishonestStep{{ID: "fault.timeout", Argv: []string{"list", "--json"}, WaitFor: "mission-timeout", WaitTiming: "before_apply"}}
	return withCleanDishonestRetry(fault, valid)
}

func dishonestFirstListMalformed(_ *testing.T, _ string, valid []dishonestStep) []dishonestStep {
	return withCleanDishonestRetry([]dishonestStep{{ID: "fault.malformed", Argv: []string{"list", "--json"}, Stdout: `{"schema_version":1,"data":`}}, valid)
}

func dishonestFirstListSchemaSkew(t *testing.T, _ string, valid []dishonestStep) []dishonestStep {
	foundation := dishonestMissionDefinition(t, "work-foundation", nil, "Deliver the bounded foundation outcome.")
	issue := dishonestMissionIssue(t, "bd-a", foundation, "operation-create-foundation", 2, "project-fault")
	return withCleanDishonestRetry([]dishonestStep{{ID: "fault.schema-skew", Argv: []string{"list", "--json"}, Stdout: envelope("[" + issue + "]")}}, valid)
}

func dishonestFirstListWrongIdentity(t *testing.T, _ string, valid []dishonestStep) []dishonestStep {
	foundation := dishonestMissionDefinition(t, "work-foundation", nil, "Deliver the bounded foundation outcome.")
	issue := dishonestMissionIssue(t, "bd-a", foundation, "operation-create-foundation", 1, "project-other")
	return withCleanDishonestRetry([]dishonestStep{{ID: "fault.wrong-identity", Argv: []string{"list", "--json"}, Stdout: envelope("[" + issue + "]")}}, valid)
}

func dishonestSecondObservationOmitted(t *testing.T, _ string, valid []dishonestStep) []dishonestStep {
	foundation := dishonestMissionDefinition(t, "work-foundation", nil, "Deliver the bounded foundation outcome.")
	issue := dishonestMissionIssue(t, "bd-a", foundation, "operation-create-foundation", 1, "project-fault")
	fault := append(append([]dishonestStep(nil), valid...), dishonestCatalogObservationSteps("fault.omitted", []string{issue}, nil)...)
	return withCleanDishonestRetry(fault, valid)
}

func dishonestSecondObservationAdditional(t *testing.T, _ string, valid []dishonestStep) []dishonestStep {
	foundation := dishonestMissionDefinition(t, "work-foundation", nil, "Deliver the bounded foundation outcome.")
	feature := dishonestMissionDefinition(t, "work-feature", []ids.WorkID{"work-foundation"}, "Deliver the bounded feature outcome.")
	additional := dishonestMissionDefinition(t, "work-additional", nil, "Deliver an unexpected additional outcome.")
	issues := []string{
		dishonestMissionIssue(t, "bd-a", foundation, "operation-create-foundation", 1, "project-fault"),
		dishonestMissionIssue(t, "bd-b", feature, "operation-create-feature", 1, "project-fault"),
		dishonestMissionIssue(t, "bd-c", additional, "operation-create-additional", 1, "project-fault"),
	}
	fault := append(append([]dishonestStep(nil), valid...), dishonestCatalogObservationSteps("fault.additional", issues, map[string][]string{"bd-b": {"bd-a"}})...)
	return withCleanDishonestRetry(fault, valid)
}

func dishonestFirstObservationReversedDependency(t *testing.T, _ string, valid []dishonestStep) []dishonestStep {
	foundation := dishonestMissionDefinition(t, "work-foundation", nil, "Deliver the bounded foundation outcome.")
	feature := dishonestMissionDefinition(t, "work-feature", []ids.WorkID{"work-foundation"}, "Deliver the bounded feature outcome.")
	issues := []string{dishonestMissionIssue(t, "bd-a", foundation, "operation-create-foundation", 1, "project-fault"), dishonestMissionIssue(t, "bd-b", feature, "operation-create-feature", 1, "project-fault")}
	fault := dishonestCatalogObservationSteps("fault.reversed", issues, map[string][]string{"bd-a": {"bd-b"}})
	fault = fault[:2]
	return withCleanDishonestRetry(fault, valid)
}

func dishonestSecondObservationStale(t *testing.T, _ string, valid []dishonestStep) []dishonestStep {
	stale := dishonestMissionObservationSteps(t, "fault.stale", "foundation-description")
	fault := append(stale, valid...)
	return withCleanDishonestRetry(fault, valid)
}

func dishonestSecondObservationChanged(t *testing.T, _ string, valid []dishonestStep) []dishonestStep {
	changed := dishonestMissionObservationSteps(t, "fault.changed", "foundation-description")
	fault := append(append([]dishonestStep(nil), valid...), changed...)
	return withCleanDishonestRetry(fault, valid)
}

// The executable qualification itself lives beside the bounded Beads adapter,
// where it can reuse the established isolated workspace harness. This scenario
// guard makes its opt-in identity explicit and independently verifies the
// reviewed binary before the test selection is admitted.
func TestMissionMethodBeadsQualificationProfileIdentity(t *testing.T) {
	if os.Getenv("SPEC_KITTY_REAL_BEADS_QUALIFICATION") != "1" {
		t.Skip("real Beads mission-method qualification is explicit and is not portable-test success")
	}
	binary := os.Getenv("BEADS_BIN")
	want := os.Getenv("BEADS_DIGEST_SHA256")
	if !filepath.IsAbs(binary) || filepath.Clean(binary) != binary || len(want) != sha256.Size*2 {
		t.Fatal("qualified Beads profile requires canonical binary and reviewed SHA-256")
	}
	file, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, io.LimitReader(file, 1<<30))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("hash qualified Beads binary: copy=%v close=%v", copyErr, closeErr)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		t.Fatalf("qualified Beads digest mismatch: got=%s", got)
	}
}
