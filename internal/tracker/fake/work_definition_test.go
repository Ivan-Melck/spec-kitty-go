package fake_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

func TestWorkDefinitionCreateReplayUpdateAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tracker.json")
	store, err := fake.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(store)
	if capabilities, err := provider.DefinitionCapabilities(ctx); err != nil || capabilities.Validate() != nil {
		t.Fatalf("definition capabilities = %+v, %v", capabilities, err)
	}
	if _, err := provider.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: "operation-create-foundation", Definition: completeDefinition(t, "work-foundation", nil)}); err != nil {
		t.Fatal(err)
	}
	definition := completeDefinition(t, "work-build", []ids.WorkID{"work-foundation"})
	request := tracker.CreateWorkDefinitionRequest{OperationID: "operation-create-build", Definition: definition}
	created, err := provider.CreateDefinition(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := provider.CreateDefinition(ctx, request)
	if err != nil || !replayed.Replayed || !replayed.Noop || replayed.Observation.ProviderObservation().Revision != created.Observation.ProviderObservation().Revision {
		t.Fatalf("create replay = %+v, %v", replayed, err)
	}

	changed := completeDefinition(t, "work-build", []ids.WorkID{"work-foundation"})
	changed, err = tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: changed.ProjectID(), WorkID: changed.WorkID(), Title: "Build the portable boundary", Description: changed.Description(), DesignContext: changed.DesignContext(),
		Criteria: changed.Criteria(), Dependencies: changed.Dependencies(), WriteScopes: changed.WriteScopes(), Provenance: changed.Provenance(), MetadataSchema: changed.MetadataSchema(),
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := provider.UpdateDefinition(ctx, tracker.UpdateWorkDefinitionRequest{
		OperationID: "operation-update-build", ExternalRef: created.Observation.ExternalRef(),
		ExpectedObservation: created.Observation.ProviderObservation().Revision, Definition: changed,
	})
	if err != nil || updated.Observation.Definition().Title() != "Build the portable boundary" {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	updateReplay, err := provider.UpdateDefinition(ctx, tracker.UpdateWorkDefinitionRequest{
		OperationID: "operation-update-build", ExternalRef: updated.Observation.ExternalRef(),
		ExpectedObservation: updated.Observation.ProviderObservation().Revision, Definition: changed,
	})
	if err != nil || !updateReplay.Replayed || !updateReplay.Noop {
		t.Fatalf("update replay = %+v, %v", updateReplay, err)
	}

	reopened, err := fake.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := fake.NewProvider(reopened).ObserveDefinition(ctx, updated.Observation.ExternalRef())
	if err != nil {
		t.Fatal(err)
	}
	if observed.Definition().Title() != changed.Title() || len(observed.Definition().Criteria()) != 1 || len(observed.Definition().Dependencies()) != 1 || observed.Definition().Dependencies()[0] != "work-foundation" || len(observed.Definition().WriteScopes()) != 1 || len(observed.Definition().Provenance()) != 2 {
		t.Fatalf("reopened observation lost definition content: %+v", observed.Definition())
	}
}

func TestWorkDefinitionOperationConflictAndFailureBoundaries(t *testing.T) {
	ctx := context.Background()
	store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(store)
	request := tracker.CreateWorkDefinitionRequest{OperationID: "operation-create", Definition: completeDefinition(t, "work-one", nil)}
	if _, err := provider.CreateDefinition(ctx, request); err != nil {
		t.Fatal(err)
	}
	conflict := request
	conflict.Definition = completeDefinition(t, "work-two", nil)
	if _, err := provider.CreateDefinition(ctx, conflict); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("same operation/different request = %v", err)
	}

	timeoutStore, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := timeoutStore.Inject(ctx, fake.FailureTimeoutAfterApply); err != nil {
		t.Fatal(err)
	}
	timeoutProvider := fake.NewProvider(timeoutStore)
	mutation, err := timeoutProvider.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: "operation-ambiguous", Definition: completeDefinition(t, "work-ambiguous", nil)})
	if !tracker.IsKind(err, tracker.ErrorAmbiguous) || mutation.Observation.ProviderObservation().Revision == 0 {
		t.Fatalf("timeout-after-apply = %+v, %v", mutation, err)
	}
	if _, observeErr := timeoutProvider.ObserveDefinition(ctx, mutation.Observation.ExternalRef()); observeErr != nil {
		t.Fatalf("applied ambiguous result is not observable: %v", observeErr)
	}

	beforeStore, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := beforeStore.Inject(ctx, fake.FailureTimeoutBeforeApply); err != nil {
		t.Fatal(err)
	}
	beforeProvider := fake.NewProvider(beforeStore)
	mutation, err = beforeProvider.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: "operation-before", Definition: completeDefinition(t, "work-before", nil)})
	if !tracker.IsKind(err, tracker.ErrorAmbiguous) || mutation.Observation.ProviderObservation().Revision != 0 {
		t.Fatalf("timeout-before-apply = %+v, %v", mutation, err)
	}
}

func TestWorkDefinitionGuardRefusesConcurrentHumanEdit(t *testing.T) {
	ctx := context.Background()
	store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(store)
	created, err := provider.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: "operation-create", Definition: completeDefinition(t, "work-build", nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.HumanEditTitle(ctx, created.Observation.ExternalRef().ExternalID, "Human title"); err != nil {
		t.Fatal(err)
	}
	_, err = provider.UpdateDefinition(ctx, tracker.UpdateWorkDefinitionRequest{
		OperationID: "operation-stale-update", ExternalRef: created.Observation.ExternalRef(),
		ExpectedObservation: created.Observation.ProviderObservation().Revision, Definition: completeDefinition(t, "work-build", nil),
	})
	var typed *tracker.Error
	if !errors.As(err, &typed) || typed.Kind != tracker.ErrorRevisionConflict {
		t.Fatalf("stale guarded update = %T %v", err, err)
	}
	observed, err := provider.ObserveDefinition(ctx, created.Observation.ExternalRef())
	if err != nil || observed.Definition().Title() != "Human title" {
		t.Fatalf("human edit overwritten: title=%q error=%v", observed.Definition().Title(), err)
	}
}

func TestWorkDefinitionBoundaryRejectsForeignProviderReferences(t *testing.T) {
	ctx := context.Background()
	store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(store)
	definition := completeDefinition(t, "work-boundary", nil)
	created, err := provider.CreateDefinition(ctx, tracker.CreateWorkDefinitionRequest{OperationID: "operation-create-boundary", Definition: definition})
	if err != nil {
		t.Fatal(err)
	}
	foreign := created.Observation.ExternalRef()
	foreign.Provider = "foreign-provider"
	if _, err := provider.ObserveDefinition(ctx, foreign); !tracker.IsKind(err, tracker.ErrorInvalidRequest) {
		t.Fatalf("foreign-provider observation error = %v", err)
	}
	if _, err := provider.UpdateDefinition(ctx, tracker.UpdateWorkDefinitionRequest{OperationID: "operation-update-boundary", ExternalRef: foreign, ExpectedObservation: foreign.LastObservation, Definition: definition}); !tracker.IsKind(err, tracker.ErrorInvalidRequest) {
		t.Fatalf("foreign-provider update error = %v", err)
	}
	observed, err := provider.ObserveDefinition(ctx, created.Observation.ExternalRef())
	if err != nil || observed.ProviderObservation().Revision != created.Observation.ProviderObservation().Revision {
		t.Fatalf("foreign-provider request mutated local state: observation=%+v error=%v", observed, err)
	}
}

func completeDefinition(t *testing.T, workID ids.WorkID, dependencies []ids.WorkID) tracker.WorkDefinition {
	t.Helper()
	definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: "project-kitty", WorkID: workID, Title: "Implement " + workID.String(), Description: "Deliver a provider-neutral work definition.", DesignContext: "Stable internal identity remains authoritative.",
		Criteria:     []tracker.WorkCriterion{{ID: "criterion-result", Text: "Observe the result", Outcome: "the durable record round-trips", Verifier: tracker.VerifierTest, Required: true}},
		Dependencies: dependencies, WriteScopes: []tracker.WriteScope{{Path: "internal/" + workID.String(), Domain: "tracker", Access: tracker.ScopeWriteExclusive}},
		Provenance: []tracker.ScopeRef{{ID: "plan-provider", Kind: tracker.ScopePlanConcern}, {ID: "requirement-portable", Kind: tracker.ScopeRequiredOutcome}}, MetadataSchema: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}
