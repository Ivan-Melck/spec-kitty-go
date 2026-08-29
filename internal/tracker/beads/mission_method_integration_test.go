package beads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
	"github.com/spec-kitty/go-kitty3/internal/projection"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestMissionMethodFakeAndBeadsPreserveTheSameSemanticDefinitions(t *testing.T) {
	ctx := context.Background()
	beadsAdapter, _ := newCatalogTestAdapter(t)
	fakeStore, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	fakeAdapter := fake.NewProvider(fakeStore)
	definitions := []tracker.WorkDefinition{
		missionMethodDefinition(t, "project-1", "work-foundation", nil),
		missionMethodDefinition(t, "project-1", "work-feature", []ids.WorkID{"work-foundation"}),
	}
	beadsObservations := make([]tracker.WorkDefinitionObservation, 0, len(definitions))
	fakeObservations := make([]tracker.WorkDefinitionObservation, 0, len(definitions))
	for _, definition := range definitions {
		op := ids.OperationID("operation-" + definition.WorkID().String())
		beadsMutation, beadsErr := beadsAdapter.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: op, Definition: definition})
		fakeMutation, fakeErr := fakeAdapter.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: op, Definition: definition})
		if beadsErr != nil || fakeErr != nil {
			t.Fatalf("work=%s beads=%v fake=%v", definition.WorkID(), beadsErr, fakeErr)
		}
		if !bytes.Equal(beadsMutation.Observation.Definition().CanonicalData(), fakeMutation.Observation.Definition().CanonicalData()) {
			t.Fatalf("semantic definition differs for %s", definition.WorkID())
		}
		if beadsMutation.Observation.ExternalRef().Provider == fakeMutation.Observation.ExternalRef().Provider || beadsMutation.Observation.ProviderObservation().Revision == 0 || fakeMutation.Observation.ProviderObservation().Revision == 0 {
			t.Fatalf("provider-specific evidence was erased: beads=%+v fake=%+v", beadsMutation.Observation.ExternalRef(), fakeMutation.Observation.ExternalRef())
		}
		beadsObservations = append(beadsObservations, beadsMutation.Observation)
		fakeObservations = append(fakeObservations, fakeMutation.Observation)
	}
	observed, err := beadsAdapter.ObserveDefinitionCatalog(ctx, "project-1")
	if err != nil || len(observed) != 2 || observed[0].Definition().WorkID() != "work-feature" || observed[1].Definition().WorkID() != "work-foundation" {
		t.Fatalf("canonical catalog=%+v err=%v", observed, err)
	}
	definitionLaw, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	approved := mission.MissionSnapshot{MissionID: "mission-provider-parity", ProjectID: "project-1"}
	beadsFinalization := missionMethodFinalizationObservation(t, approved, beadsObservations)
	fakeFinalization := missionMethodFinalizationObservation(t, approved, fakeObservations)
	beadsFindings := workgraph.ValidateDraft(beadsFinalization.Draft(), workgraph.DefaultQualityPolicy())
	fakeFindings := workgraph.ValidateDraft(fakeFinalization.Draft(), workgraph.DefaultQualityPolicy())
	if len(beadsFindings) != 0 || len(fakeFindings) != 0 {
		t.Fatalf("provider-neutral coverage differs: beads=%+v fake=%+v", beadsFindings, fakeFindings)
	}
	at := time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC)
	beadsRevision := missionMethodRevision(t, approved, beadsObservations, 1, "", "revision-beads-parity", at, definitionLaw)
	fakeRevision := missionMethodRevision(t, approved, fakeObservations, 1, "", "revision-fake-parity", at, definitionLaw)
	beadsSemantic := missionMethodSemanticRevisionDigest(t, beadsFinalization.Draft(), beadsRevision)
	fakeSemantic := missionMethodSemanticRevisionDigest(t, fakeFinalization.Draft(), fakeRevision)
	if beadsSemantic != fakeSemantic || beadsRevision.CriteriaDigest() != fakeRevision.CriteriaDigest() {
		t.Fatalf("provider-neutral executable digest differs: beads=%s fake=%s beads_graph=%s fake_graph=%s", beadsSemantic, fakeSemantic, beadsRevision.GraphDigest(), fakeRevision.GraphDigest())
	}
	beadsEncoded, beadsEncodeErr := beadsRevision.Encode()
	fakeEncoded, fakeEncodeErr := fakeRevision.Encode()
	if beadsEncodeErr != nil || fakeEncodeErr != nil {
		t.Fatalf("encode parity revisions: beads=%v fake=%v", beadsEncodeErr, fakeEncodeErr)
	}
	if beadsRevision.GraphDigest() == fakeRevision.GraphDigest() || bytes.Equal(beadsEncoded, fakeEncoded) {
		t.Fatal("attributable provider mapping evidence was erased from WorkRevision")
	}
	mutatedDefinition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: definitions[0].ProjectID(), WorkID: definitions[0].WorkID(), Title: definitions[0].Title(),
		Description: "semantic parity mutant", DesignContext: definitions[0].DesignContext(), Criteria: definitions[0].Criteria(),
		Dependencies: definitions[0].Dependencies(), WriteScopes: definitions[0].WriteScopes(), Provenance: definitions[0].Provenance(), MetadataSchema: definitions[0].MetadataSchema(),
	})
	if err != nil {
		t.Fatal(err)
	}
	mutatedObservation, err := tracker.NewWorkDefinitionObservation(mutatedDefinition, fakeObservations[0].ExternalRef(), fakeObservations[0].ProviderObservation(), fakeObservations[0].ObservedAt())
	if err != nil {
		t.Fatal(err)
	}
	mutated := append([]tracker.WorkDefinitionObservation(nil), fakeObservations...)
	mutated[0] = mutatedObservation
	mutatedFinalization := missionMethodFinalizationObservation(t, approved, mutated)
	mutatedRevision := missionMethodRevision(t, approved, mutated, 1, "", "revision-fake-parity-mutant", at, definitionLaw)
	if missionMethodSemanticRevisionDigest(t, mutatedFinalization.Draft(), mutatedRevision) == fakeSemantic {
		t.Fatal("semantic-field mutant did not change the provider-neutral revision digest")
	}
}

func TestMissionMethodRealBeadsTwoItemQualification(t *testing.T) {
	// This selector deliberately remains outside the frozen Phase 2 eleven-case
	// qualification runner. WP12 owns Phase 3 campaign registration; review
	// invokes this exact selector in the pinned offline Beads environment.
	fixture := newRealBeadsFixture(t)
	bound := fixture.adapter(t)
	ctx := context.Background()
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	definitionLaw, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(t.TempDir(), "control.db")
	governance := missionMethodGovernance(t, "governance-real-beads", at)
	store, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	approvedPlan := missionMethodApprovedPlan(t, ctx, store, fixture.projectID, definitionLaw, governance, at)
	foundation := missionMethodDefinition(t, fixture.projectID, "work-foundation", nil)
	feature := missionMethodDefinition(t, fixture.projectID, "work-feature", []ids.WorkID{"work-foundation"})
	capabilities, err := bound.DefinitionCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	provider := &missionMethodCountingDefinitionPort{WorkDefinitionPort: bound}
	decompositionCommand := mission.DecompositionCommand{
		MissionID: approvedPlan.MissionID, ProjectID: fixture.projectID, Provider: beadsProvider,
		ExpectedMission: approvedPlan, PlanRevisionID: "artifact-plan-real-beads",
		PlanContentDigest: artifact.ContentCommitment([]byte("approved real Beads plan")),
		ApprovedScope:     []workgraph.ApprovedScope{{ID: "plan-core", Kind: workgraph.ScopePlanConcern, MissionID: approvedPlan.MissionID}},
		Work:              []mission.WorkProposal{{Definition: foundation}, {Definition: feature}},
		ActorID:           "actor-real-beads", OperationID: "operation-decompose-real-beads",
		GovernanceContextID: governance.ID(), OccurredAt: at.Add(9 * time.Second),
	}
	decomposed, err := mission.NewDecompositionService(store, store, provider, capabilities, store).Decompose(ctx, decompositionCommand)
	if err != nil || decomposed.Replayed || decomposed.Status != mission.DecompositionComplete || len(decomposed.Observations) != 2 || provider.freshMutations != 2 {
		t.Fatalf("durable decomposition=%+v provider=%+v err=%v", decomposed, provider, err)
	}
	for _, observation := range decomposed.Observations {
		want := missionMethodDerivedWorkOperation(decompositionCommand.OperationID, observation.Definition().WorkID())
		if observation.ExternalRef().CreatedByOperation != want {
			t.Fatalf("child operation for %s=%s want=%s", observation.Definition().WorkID(), observation.ExternalRef().CreatedByOperation, want)
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	freshBeforeReplay := provider.freshMutations
	mutationCallsBeforeReplay := provider.mutationCalls
	replayedDecomposition, err := mission.NewDecompositionService(store, store, provider, capabilities, store).Decompose(ctx, decompositionCommand)
	if err != nil || !replayedDecomposition.Replayed || replayedDecomposition.RequestDigest != decomposed.RequestDigest || len(replayedDecomposition.Observations) != 2 || provider.freshMutations != freshBeforeReplay || provider.mutationCalls != mutationCallsBeforeReplay+2 {
		t.Fatalf("reopened decomposition replay=%+v provider=%+v err=%v", replayedDecomposition, provider, err)
	}
	catalog, err := bound.ObserveDefinitionCatalog(ctx, fixture.projectID)
	if err != nil || len(catalog) != 2 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	draftReady := missionMethodTransition(t, ctx, store, definitionLaw, approvedPlan, mission.StepDecomposing, governance.ID(), "real-beads-decomposing", at.Add(10*time.Second))
	draftReady = missionMethodTransition(t, ctx, store, definitionLaw, draftReady, mission.StepWorkDraftReady, governance.ID(), "real-beads-draft-ready", at.Add(11*time.Second))
	source := &missionMethodBeadsFinalizationSource{adapter: bound, projectID: fixture.projectID, missionID: approvedPlan.MissionID, planRevisionID: "artifact-plan-real-beads", planDigest: artifact.ContentCommitment([]byte("approved real Beads plan"))}
	finalizedAt := at.Add(12 * time.Second)
	finalizationCommand := mission.FinalizationCommand{MissionID: approvedPlan.MissionID, ProjectID: fixture.projectID, ExpectedMission: draftReady, ExpectedCurrentGeneration: 0, ProposedRevisionID: "revision-real-beads-1", RequestedGeneration: 1, PlanningSourceCommit: strings.Repeat("a", 40), ActorID: "actor-real-beads", OperationID: "operation-finalize-real-beads", GovernanceContextID: governance.ID(), OccurredAt: finalizedAt}
	finalized, err := mission.NewFinalizationService(source, store).Finalize(ctx, finalizationCommand)
	if err != nil || source.observations != 2 || finalized.Revision.Generation() != 1 || finalized.Mission.CurrentWorkRevisionID != finalized.Revision.ID() {
		t.Fatalf("durable generation one=%+v observations=%d err=%v", finalized, source.observations, err)
	}
	replayedFinalization, err := mission.NewFinalizationService(source, store).Finalize(ctx, finalizationCommand)
	if err != nil || !replayedFinalization.Replayed || source.observations != 2 {
		t.Fatalf("finalization replay=%+v observations=%d err=%v", replayedFinalization, source.observations, err)
	}
	generationOne := finalized.Revision
	foundationObservation := definitionObservationByWork(t, catalog, "work-foundation")
	featureObservation := definitionObservationByWork(t, catalog, "work-feature")
	if _, _, err := fixture.runBD(t, "update", featureObservation.ExternalRef().ExternalID, "--status", "in_progress", "--json"); err != nil {
		t.Fatal(err)
	}
	statusCatalog, err := bound.ObserveDefinitionCatalog(ctx, fixture.projectID)
	if err != nil {
		t.Fatal(err)
	}
	statusCandidate := missionMethodRevision(t, approvedPlan, statusCatalog, 2, generationOne.ID(), "revision-real-beads-status", at.Add(13*time.Second), definitionLaw)
	statusDrift, err := workgraph.DetectDrift(generationOne, statusCandidate, at.Add(14*time.Second))
	if err != nil || statusDrift.SemanticStatus() != workgraph.SemanticObservationOnly {
		t.Fatalf("provider workflow status became executable authority: drift=%s changes=%+v old_graph=%s new_graph=%s err=%v", statusDrift.SemanticStatus(), statusDrift.Changes(), generationOne.GraphDigest(), statusCandidate.GraphDigest(), err)
	}
	if _, _, err := fixture.runBD(t, "update", foundationObservation.ExternalRef().ExternalID, "--description", "human semantic edit", "--json"); err != nil {
		t.Fatal(err)
	}
	changedCatalog, err := bound.ObserveDefinitionCatalog(ctx, fixture.projectID)
	if err != nil {
		t.Fatal(err)
	}
	publishedAt := at.Add(15 * time.Second)
	generationTwo := missionMethodRevision(t, approvedPlan, changedCatalog, 2, generationOne.ID(), "revision-real-beads-2", publishedAt, definitionLaw)
	drift, err := workgraph.DetectDrift(generationOne, generationTwo, publishedAt)
	if err != nil || drift.SemanticStatus() != workgraph.SemanticChanged {
		t.Fatalf("semantic provider mutation did not produce drift: status=%s err=%v", drift.SemanticStatus(), err)
	}
	statuses := make([]workgraph.WorkStatusAssignment, len(drift.AffectedOldWorkIDs()))
	decisions := make([]workgraph.WorkDispositionDecisionParams, len(drift.AffectedOldWorkIDs()))
	for index, workID := range drift.AffectedOldWorkIDs() {
		statuses[index] = workgraph.WorkStatusAssignment{WorkID: workID, Status: workgraph.StatusActive}
		decisions[index] = workgraph.WorkDispositionDecisionParams{OldWorkID: workID, Disposition: workgraph.Invalidate, Rationale: "replace semantic provider state explicitly"}
	}
	reconciliation, err := workgraph.ReconcileMixed(workgraph.MixedReconcileRequest{ID: "reconciliation-real-beads", Old: generationOne, New: generationTwo, Drift: drift, Statuses: statuses, Decisions: decisions, ActorID: "actor-real-beads", Rationale: "adopt the re-observed provider contract", OccurredAt: publishedAt})
	if err != nil || len(reconciliation.Decisions()) != len(drift.AffectedOldWorkIDs()) {
		t.Fatalf("reconciliation=%+v err=%v", reconciliation, err)
	}
	publicationCommand := mission.SuccessorPublicationCommand{MissionID: approvedPlan.MissionID, ProjectID: fixture.projectID, ExpectedMission: finalized.Mission, ExpectedCurrentGeneration: 1, ExpectedOld: generationOne, Candidate: generationTwo, Drift: drift, ReconciliationID: "reconciliation-real-beads", Reconciliation: reconciliation, OperationID: "operation-reconcile-real-beads", ActorID: "actor-real-beads", GovernanceContextID: governance.ID(), Rationale: "adopt the re-observed provider contract", OccurredAt: publishedAt}
	published, err := mission.NewSuccessorPublicationService(store).Publish(ctx, publicationCommand)
	if err != nil || published.Revision.ID() != generationTwo.ID() || published.Mission.CurrentWorkRevisionID != generationTwo.ID() {
		t.Fatalf("durable generation two=%+v err=%v", published, err)
	}
	replayedPublication, err := mission.NewSuccessorPublicationService(store).Publish(ctx, publicationCommand)
	if err != nil || !replayedPublication.Replayed {
		t.Fatalf("reconciliation replay=%+v err=%v", replayedPublication, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := reopened.ObserveMissionMethod(ctx, approvedPlan.MissionID)
	if err != nil || persisted.Workgraph.Generation() != 2 || persisted.Workgraph.RevisionID() != generationTwo.ID() || len(persisted.Workgraph.Reconciliations()) != len(reconciliation.Decisions()) {
		t.Fatalf("persisted generation=%+v err=%v", persisted, err)
	}
	view, err := projection.NewTasksView(persisted.Mission, persisted.Workgraph, at.Add(16*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	projectionBytes, err := projection.RenderTasks(view)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(projectionBytes, []byte("**Generation:** 2")) || !bytes.Contains(projectionBytes, []byte("human semantic edit")) || !bytes.Contains(projectionBytes, []byte("NON-AUTHORITATIVE")) {
		t.Fatalf("generation-two projection is incomplete: %s", projectionBytes)
	}
	t.Logf("qualified beads=%s binary_sha256=%s schema=%d generation_one=%s generation_two=%s drift=%s reconciliation=%s projection_bytes=%d", SupportedBeadsVersion, fixture.digest, SupportedProtocolSchema, generationOne.GraphDigest(), generationTwo.GraphDigest(), drift.SemanticStatus(), reconciliation.ID(), len(projectionBytes))
}

type missionMethodCountingDefinitionPort struct {
	tracker.WorkDefinitionPort
	mutationCalls  int
	freshMutations int
}

func (port *missionMethodCountingDefinitionPort) CreateDefinition(ctx context.Context, request tracker.CreateWorkDefinitionRequest) (tracker.WorkDefinitionMutation, error) {
	port.mutationCalls++
	result, err := port.WorkDefinitionPort.CreateDefinition(ctx, request)
	if err == nil && !result.Replayed {
		port.freshMutations++
	}
	return result, err
}

func (port *missionMethodCountingDefinitionPort) UpdateDefinition(ctx context.Context, request tracker.UpdateWorkDefinitionRequest) (tracker.WorkDefinitionMutation, error) {
	port.mutationCalls++
	result, err := port.WorkDefinitionPort.UpdateDefinition(ctx, request)
	if err == nil && !result.Replayed {
		port.freshMutations++
	}
	return result, err
}

func missionMethodDerivedWorkOperation(parent ids.OperationID, workID ids.WorkID) ids.OperationID {
	digest := sha256.Sum256([]byte(parent.String() + "\x00" + workID.String()))
	return ids.OperationID(fmt.Sprintf("decompose-%x", digest[:16]))
}

func missionMethodSemanticRevisionDigest(t *testing.T, draft workgraph.DraftObservation, revision workgraph.RevisionV2) string {
	t.Helper()
	type semanticWork struct {
		ProjectID         ids.ProjectID                     `json:"project_id"`
		WorkID            ids.WorkID                        `json:"work_id"`
		Title             string                            `json:"title"`
		Description       string                            `json:"description"`
		DesignContext     string                            `json:"design_context"`
		CrossDomainReason string                            `json:"cross_domain_reason,omitempty"`
		Criteria          []workgraph.CriterionObservation  `json:"criteria"`
		Dependencies      []ids.WorkID                      `json:"dependencies"`
		WriteScopes       []workgraph.WriteScopeObservation `json:"write_scopes"`
		Provenance        []workgraph.ScopeReference        `json:"provenance"`
	}
	work := draft.Work()
	semantic := make([]semanticWork, len(work))
	for index, item := range work {
		semantic[index] = semanticWork{
			ProjectID: item.ProjectID(), WorkID: item.WorkID(), Title: item.Title(), Description: item.Description(),
			DesignContext: item.DesignContext(), CrossDomainReason: item.CrossDomainReason(), Criteria: item.Criteria(),
			Dependencies: item.Dependencies(), WriteScopes: item.WriteScopes(), Provenance: item.Provenance(),
		}
	}
	revisionBytes, err := revision.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var revisionWire map[string]json.RawMessage
	if err := json.Unmarshal(revisionBytes, &revisionWire); err != nil {
		t.Fatal(err)
	}
	for _, auditOrProviderField := range []string{"work_revision_id", "generation", "predecessor_id", "provider_observations", "graph_digest", "finalized_by", "finalized_at", "governance_context_id"} {
		delete(revisionWire, auditOrProviderField)
	}
	encoded, err := json.Marshal(struct {
		Domain         string                     `json:"domain"`
		ProjectID      ids.ProjectID              `json:"project_id"`
		MissionID      ids.MissionID              `json:"mission_id"`
		PlanRevisionID string                     `json:"plan_revision_id"`
		PlanDigest     string                     `json:"plan_digest"`
		ApprovedScope  []workgraph.ApprovedScope  `json:"approved_scope"`
		Work           []semanticWork             `json:"work"`
		Revision       map[string]json.RawMessage `json:"revision"`
	}{"spec-kitty/provider-neutral-semantic-revision/v1", draft.ProjectID(), draft.MissionID(), draft.PlanRevisionID(), draft.PlanDigest(), draft.ApprovedScope(), semantic, revisionWire})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest)
}

type missionMethodBeadsFinalizationSource struct {
	adapter        *BoundAdapter
	projectID      ids.ProjectID
	missionID      ids.MissionID
	planRevisionID string
	planDigest     string
	observations   int
}

func (source *missionMethodBeadsFinalizationSource) ObserveFinalization(ctx context.Context, missionID ids.MissionID) (workgraph.FinalizationObservation, error) {
	if source == nil || missionID != source.missionID {
		return workgraph.FinalizationObservation{}, fmt.Errorf("finalization observation scope changed")
	}
	observations, err := source.adapter.ObserveDefinitionCatalog(ctx, source.projectID)
	if err != nil {
		return workgraph.FinalizationObservation{}, err
	}
	source.observations++
	return missionMethodFinalizationObservationFromPlan(source.projectID, source.missionID, source.planRevisionID, source.planDigest, observations)
}

func missionMethodGovernance(t *testing.T, id ids.GovernanceContextID, at time.Time) governancecontext.Context {
	t.Helper()
	commitment, err := governancecontext.NewCommitment(governancecontext.AlgorithmSHA256, strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := governancecontext.NewEntry(governancecontext.KindProfile, "implementer-ivan", "1", commitment)
	if err != nil {
		t.Fatal(err)
	}
	value, err := governancecontext.NewContext(id, governancecontext.SchemaVersion, []governancecontext.Entry{entry}, at)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func missionMethodApprovedPlan(t *testing.T, ctx context.Context, store *sqlite.Store, projectID ids.ProjectID, definition mission.MissionDefinition, governance governancecontext.Context, at time.Time) mission.MissionSnapshot {
	t.Helper()
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	created, err := mission.NewService(registry, store).Create(ctx, mission.CreateRequest{MissionID: "mission-real-beads", ProjectID: projectID, DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: "operation-real-beads-initialize", ActorID: "actor-real-beads", ProcessID: "process-real-beads", Governance: governance, OccurredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	current := missionMethodTransition(t, ctx, store, definition, created.Instance, mission.StepSpecifying, governance.ID(), "real-beads-specifying", at.Add(time.Second))
	missionMethodApprovedArtifact(t, ctx, store, created.Instance.MissionID, artifact.KindSpecification, "artifact-spec-real-beads", "approved real Beads specification", governance.ID(), at.Add(2*time.Second))
	current = missionMethodTransition(t, ctx, store, definition, current, mission.StepSpecificationApproved, governance.ID(), "real-beads-spec-approved", at.Add(4*time.Second))
	current = missionMethodTransition(t, ctx, store, definition, current, mission.StepPlanning, governance.ID(), "real-beads-planning", at.Add(5*time.Second))
	missionMethodApprovedArtifact(t, ctx, store, created.Instance.MissionID, artifact.KindPlan, "artifact-plan-real-beads", "approved real Beads plan", governance.ID(), at.Add(6*time.Second))
	return missionMethodTransition(t, ctx, store, definition, current, mission.StepPlanApproved, governance.ID(), "real-beads-plan-approved", at.Add(8*time.Second))
}

func missionMethodApprovedArtifact(t *testing.T, ctx context.Context, store *sqlite.Store, missionID ids.MissionID, kind artifact.Kind, revisionID, content string, governanceID ids.GovernanceContextID, at time.Time) {
	t.Helper()
	policy, err := artifact.PolicyFor(kind)
	if err != nil {
		t.Fatal(err)
	}
	validation, err := artifact.ValidateConcerns(revisionID, policy, policy.RequiredConcerns(), at)
	if err != nil {
		t.Fatal(err)
	}
	service, err := artifact.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, artifact.PublishCommand{OperationID: "operation-publish-" + revisionID, ArtifactRevisionID: revisionID, MissionID: missionID.String(), Kind: kind, SourceRef: string(kind) + ".md", SourceRevision: strings.Repeat("d", 40), CreatedBy: "actor-real-beads", CreatedAt: at, Content: []byte(content), Validation: validation}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Approve(ctx, artifact.ApproveCommand{OperationID: "operation-approve-" + revisionID, ApprovalID: "approval-" + revisionID, ArtifactRevisionID: revisionID, Verdict: artifact.VerdictApproved, ActorID: "actor-real-beads", EvidenceDigest: artifact.ContentCommitment([]byte("approval:" + revisionID)), GovernanceContextID: governanceID.String(), SubmittedAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
}

func missionMethodTransition(t *testing.T, ctx context.Context, store *sqlite.Store, definition mission.MissionDefinition, current mission.MissionSnapshot, successor mission.StepKey, governanceID ids.GovernanceContextID, suffix string, at time.Time) mission.MissionSnapshot {
	t.Helper()
	destination, ok := definition.Step(successor)
	if !ok {
		t.Fatalf("missing destination step %s", successor)
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
	result, err := mission.NewServiceWithArtifactApprovals(registry, store, store).Transition(ctx, mission.TransitionRequest{MissionID: current.MissionID, DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), DefinitionCommitment: definition.Commitment(), ExpectedSnapshot: current, ExpectedCurrentStep: current.CurrentStep, ExpectedStateVersion: current.StateVersion, Successor: successor, EventID: ids.MissionEventID("event-" + suffix), OperationID: ids.OperationID("operation-" + suffix), ActorID: "actor-real-beads", GovernanceContextID: governanceID, OccurredAt: at, Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	return result.Instance
}

func missionMethodDefinition(t *testing.T, projectID ids.ProjectID, workID ids.WorkID, dependencies []ids.WorkID) tracker.WorkDefinition {
	t.Helper()
	digest := sha256.Sum256([]byte(workID))
	definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: projectID, WorkID: workID, Title: "Implement " + workID.String(), Description: "Deliver the bounded mission outcome.", DesignContext: "Preserve provider-neutral authority.",
		Criteria:     []tracker.WorkCriterion{{ID: fmt.Sprintf("criterion-%x", digest[:4]), Text: "observable result", Outcome: "the result is durable and verified", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "go test ./..."}},
		Dependencies: dependencies, WriteScopes: []tracker.WriteScope{{Path: "internal/" + workID.String(), Domain: workID.String(), Access: tracker.ScopeWriteExclusive}},
		Provenance: []tracker.ScopeRef{{ID: "plan-core", Kind: tracker.ScopePlanConcern}}, MetadataSchema: tracker.WorkDefinitionMetadataSchemaV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func definitionObservationByWork(t *testing.T, observations []tracker.WorkDefinitionObservation, workID ids.WorkID) tracker.WorkDefinitionObservation {
	t.Helper()
	for _, observation := range observations {
		if observation.Definition().WorkID() == workID {
			return observation
		}
	}
	t.Fatalf("work observation %s is absent", workID)
	return tracker.WorkDefinitionObservation{}
}

func missionMethodFinalizationObservation(t *testing.T, approvedPlan mission.MissionSnapshot, observations []tracker.WorkDefinitionObservation) workgraph.FinalizationObservation {
	t.Helper()
	result, err := missionMethodFinalizationObservationFromPlan(approvedPlan.ProjectID, approvedPlan.MissionID, "plan-real-beads", strings.Repeat("b", 64), observations)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func missionMethodFinalizationObservationFromPlan(projectID ids.ProjectID, missionID ids.MissionID, planRevisionID, planDigest string, observations []tracker.WorkDefinitionObservation) (workgraph.FinalizationObservation, error) {
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
			if criterion.EvidenceHint != "" {
				hints[criterion.ID] = criterion.EvidenceHint
			}
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
	result, err := workgraph.NewFinalizationObservation(draft, evidence)
	if err != nil {
		return workgraph.FinalizationObservation{}, err
	}
	return result, nil
}

func missionMethodRevision(t *testing.T, approvedPlan mission.MissionSnapshot, observations []tracker.WorkDefinitionObservation, generation uint64, predecessor, id ids.WorkRevisionID, finalizedAt time.Time, definition mission.MissionDefinition) workgraph.RevisionV2 {
	t.Helper()
	finalization := missionMethodFinalizationObservation(t, approvedPlan, observations)
	draft := finalization.Draft()
	lanes, err := workgraph.CollapseLanes(draft)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]workgraph.WorkItemV2Params, len(observations))
	provider := make([]workgraph.ProviderObservationV2, len(observations))
	for index, observation := range observations {
		value := observation.Definition()
		criteria := make([]workgraph.CriterionV2Params, len(value.Criteria()))
		for criterionIndex, criterion := range value.Criteria() {
			criteria[criterionIndex] = workgraph.CriterionV2Params{ID: criterion.ID, Text: criterion.Text, VerifierKind: criterion.Verifier.String(), Required: criterion.Required, Outcome: criterion.Outcome, EvidenceHint: criterion.EvidenceHint}
		}
		scopes := make([]workgraph.NormalizedWriteScope, len(value.WriteScopes()))
		for scopeIndex, scope := range value.WriteScopes() {
			scopes[scopeIndex] = workgraph.NormalizedWriteScope{Path: scope.Path, Domain: scope.Domain, Access: workgraph.ScopeAccess(scope.Access), SerializationKey: scope.SerializationKey}
		}
		items[index] = workgraph.WorkItemV2Params{WorkID: value.WorkID(), Title: value.Title(), Description: value.Description(), DesignContext: value.DesignContext(), Criteria: criteria, Dependencies: value.Dependencies(), WriteScopes: scopes}
		reference, providerObservation := observation.ExternalRef(), observation.ProviderObservation()
		provider[index] = workgraph.ProviderObservationV2{WorkID: value.WorkID(), Provider: reference.Provider, ExternalID: reference.ExternalID, Revision: fmt.Sprint(providerObservation.Revision)}
	}
	result, err := workgraph.NewRevisionV2(workgraph.RevisionV2Params{ID: id, ProjectID: approvedPlan.ProjectID, MissionID: approvedPlan.MissionID, Generation: generation, PredecessorID: predecessor, PlanningSourceCommit: strings.Repeat("a", 40), MissionDefinition: workgraph.MissionDefinitionIdentity{Key: definition.ID().String(), Version: uint64(definition.Version()), Digest: definition.Commitment().String()}, DependencyPolicy: workgraph.DependencyApprovedOrDone, WorkItems: items, Lanes: lanes, ProviderObservations: provider, FinalizedBy: "actor-real-beads", FinalizedAt: finalizedAt, GovernanceContextID: "governance-real-beads"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMissionMethodDefinitionObservationDetectsEqualTimestampSemanticMutation(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	definition := missionMethodDefinition(t, "project-1", "work-equal-time", nil)
	created, err := bound.CreateDefinition(context.Background(), tracker.CreateWorkDefinitionRequest{OperationID: "operation-equal-time", Definition: definition})
	if err != nil {
		t.Fatal(err)
	}
	reference := created.Observation.ExternalRef()
	runner.mu.Lock()
	issue := runner.issues[reference.ExternalID]
	fixed := issue.UpdatedAt
	issue.Description = "changed at the same provider timestamp"
	issue.UpdatedAt = fixed
	runner.issues[reference.ExternalID] = issue
	runner.mu.Unlock()
	after, err := bound.ObserveDefinition(context.Background(), reference)
	if err != nil || after.ProviderObservation().Revision == created.Observation.ProviderObservation().Revision || after.ProviderObservation().ProviderUpdatedAt != created.Observation.ProviderObservation().ProviderUpdatedAt {
		t.Fatalf("equal-time mutation after=%+v err=%v", after.ProviderObservation(), err)
	}
}

func TestMissionMethodAdapterSourceKeepsProviderAuthorityBounded(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate adapter source")
	}
	for _, name := range []string{"catalog.go", "metadata.go", "graph.go"} {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), name))
		if err != nil {
			t.Fatal(err)
		}
		source := string(raw)
		for _, forbidden := range []string{"database/sql", "PrepareDefinitionOperation", "internal/mission", "internal/controlstore", "internal/workspace", "internal/review", "internal/integration"} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("%s contains forbidden authority/protocol dependency %q", name, forbidden)
			}
		}
	}
}
