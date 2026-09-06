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

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
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
	approvedPlan := mission.MissionSnapshot{MissionID: "mission-real-beads", ProjectID: fixture.projectID}
	foundation := missionMethodDefinition(t, fixture.projectID, "work-foundation", nil)
	feature := missionMethodDefinition(t, fixture.projectID, "work-feature", []ids.WorkID{"work-foundation"})
	provider := &missionMethodCountingDefinitionPort{WorkDefinitionPort: bound}
	parentOperation := ids.OperationID("operation-decompose-real-beads")
	definitions := []tracker.WorkDefinition{foundation, feature}
	for _, definition := range definitions {
		operation := missionMethodDerivedWorkOperation(parentOperation, definition.WorkID())
		mutation, createErr := provider.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: operation, Definition: definition})
		if createErr != nil || mutation.Replayed {
			t.Fatalf("create provider definition %s: mutation=%+v err=%v", definition.WorkID(), mutation, createErr)
		}
		if mutation.Observation.ExternalRef().CreatedByOperation != operation {
			t.Fatalf("child operation for %s=%s want=%s", definition.WorkID(), mutation.Observation.ExternalRef().CreatedByOperation, operation)
		}
	}
	if provider.freshMutations != 2 {
		t.Fatalf("fresh provider mutation census=%d want=2", provider.freshMutations)
	}
	freshBeforeReplay := provider.freshMutations
	mutationCallsBeforeReplay := provider.mutationCalls
	for _, definition := range definitions {
		operation := missionMethodDerivedWorkOperation(parentOperation, definition.WorkID())
		mutation, replayErr := provider.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: operation, Definition: definition})
		if replayErr != nil || !mutation.Replayed {
			t.Fatalf("provider replay %s: mutation=%+v err=%v", definition.WorkID(), mutation, replayErr)
		}
	}
	if provider.freshMutations != freshBeforeReplay || provider.mutationCalls != mutationCallsBeforeReplay+2 {
		t.Fatalf("provider replay mutated twice: provider=%+v", provider)
	}
	catalog, err := bound.ObserveDefinitionCatalog(ctx, fixture.projectID)
	if err != nil || len(catalog) != 2 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	generationOne := missionMethodRevision(t, approvedPlan, catalog, 1, "", "revision-real-beads-1", at.Add(12*time.Second), definitionLaw)
	encodedOne, err := generationOne.Encode()
	if err != nil {
		t.Fatal(err)
	}
	restoredOne, err := workgraph.DecodeRevisionV2(encodedOne)
	if err != nil || restoredOne.ContentDigest() != generationOne.ContentDigest() {
		t.Fatalf("generation-one canonical replay: restored=%+v err=%v", restoredOne, err)
	}
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
	encodedTwo, err := generationTwo.Encode()
	if err != nil {
		t.Fatal(err)
	}
	restoredTwo, err := workgraph.DecodeRevisionV2(encodedTwo)
	if err != nil || restoredTwo.Generation() != 2 || restoredTwo.PredecessorID() != generationOne.ID() || len(restoredTwo.WorkItems()) != 2 || len(reconciliation.Decisions()) != len(drift.AffectedOldWorkIDs()) {
		t.Fatalf("generation-two canonical replay: restored=%+v reconciliation=%+v err=%v", restoredTwo, reconciliation, err)
	}
	if restoredTwo.WorkItems()[1].Description() != "human semantic edit" && restoredTwo.WorkItems()[0].Description() != "human semantic edit" {
		t.Fatalf("generation-two replay omitted the provider semantic edit: %+v", restoredTwo.WorkItems())
	}
	// GapDB durability/restart, reconciliation publication, progress projection,
	// and stale-writer fencing are executed by the production-authority proof
	// selectors. Keeping those facts in their owning package avoids rebuilding
	// the retired cross-domain SQLite test store here.
	runPortableAuthorityProof(t, "TestTwoItemMissionRestartAfterEachDurableBoundary")
	t.Logf("qualified beads=%s binary_sha256=%s schema=%d generation_one=%s generation_two=%s drift=%s reconciliation=%s authority=gapdb-production", SupportedBeadsVersion, fixture.digest, SupportedProtocolSchema, generationOne.GraphDigest(), generationTwo.GraphDigest(), drift.SemanticStatus(), reconciliation.ID())
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
