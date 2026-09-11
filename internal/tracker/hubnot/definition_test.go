package hubnot

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func definitionRequest(t *testing.T, work string, deps []ids.WorkID) tracker.CreateWorkDefinitionRequest {
	t.Helper()
	c := richContent(t, work)
	d, err := c.definition(deps)
	if err != nil {
		t.Fatal(err)
	}
	return tracker.CreateWorkDefinitionRequest{OperationID: ids.OperationID("define-" + work), Definition: d}
}
func TestDefinitionHistoricalSagaReplayAndGuardRefusal(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	supplier, err := b.CreateDefinition(ctx, definitionRequest(t, "supplier", nil))
	if err != nil {
		t.Fatal(err)
	}
	request := definitionRequest(t, "target", []ids.WorkID{"supplier"})
	created, err := b.CreateDefinition(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := b.ObserveDefinition(ctx, created.Observation.ExternalRef())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observed.Definition().Criteria(), request.Definition.Criteria()) || !reflect.DeepEqual(observed.Definition().Dependencies(), request.Definition.Dependencies()) || !bytes.Equal(observed.Definition().ProviderMetadata().Bytes(), request.Definition.ProviderMetadata().Bytes()) {
		t.Fatal("definition field or real graph loss")
	}
	budget := newObservationBudget()
	defer budget.close()
	source, err := b.observe(ctx, budget, supplier.Observation.ExternalRef())
	if err != nil {
		t.Fatal(err)
	}
	state := cloneIssueState(source.State)
	state.Relations = []issueRelation{}
	if _, _, err := b.mutate(ctx, budget, mutationRequest{intent: "revise", id: source.Reference.ExternalID, key: "human-unlink", parents: []string{source.Heads[0].ID}, state: &state}); err != nil {
		t.Fatal(err)
	}
	before := refs()
	replayed, err := b.CreateDefinition(ctx, request)
	if err != nil || !replayed.Replayed || !replayed.Noop || !replayed.PriorMutation || !bytes.Equal(before, refs()) {
		t.Fatalf("completed saga replay: %+v %v", replayed, err)
	}
	if !reflect.DeepEqual(replayed.Observation.Definition().Dependencies(), []ids.WorkID{"supplier"}) {
		t.Fatal("replay lost original graph effect")
	}
	fresh, err := b.ObserveDefinition(ctx, created.Observation.ExternalRef())
	if err != nil || len(fresh.Definition().Dependencies()) != 0 {
		t.Fatalf("fresh graph: %v %v", fresh.Definition().Dependencies(), err)
	}
	reordered := richContent(t, "target")
	reordered.Criteria[0], reordered.Criteria[1] = reordered.Criteria[1], reordered.Criteria[0]
	d, err := reordered.definition([]ids.WorkID{"supplier"})
	if err != nil {
		t.Fatal(err)
	}
	request.Definition = d
	if _, err := b.CreateDefinition(ctx, request); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("criterion order reuse: %v", err)
	}
	if _, err := b.UpdateDefinition(ctx, tracker.UpdateWorkDefinitionRequest{}); !tracker.IsKind(err, tracker.ErrorGuardedUpdateUnsupported) {
		t.Fatalf("guard: %v", err)
	}
	if _, err := b.UpdateWork(ctx, tracker.UpdateWorkRequest{ExternalRef: created.Observation.ExternalRef(), OperationID: "bypass", RequiredGuard: tracker.UpdateUnguarded, Title: "bypass"}); !tracker.IsKind(err, tracker.ErrorGuardedUpdateUnsupported) {
		t.Fatalf("bypass: %v", err)
	}
	if !bytes.Equal(before, refs()) {
		t.Fatal("refusal wrote")
	}
	caps, err := b.DefinitionCapabilities(ctx)
	if err != nil || caps.GuardedUpdate || !caps.Create || !caps.BlockingGraph || caps.Validate() == nil || caps.Require(tracker.WorkDefinitionRequirements{Create: true, BlockingGraph: true}) != nil {
		t.Fatalf("capabilities: %+v %v", caps, err)
	}
}

func TestDefinitionNativeReplayRacePreservesAggregateFlags(t *testing.T) {
	b, _ := operationFixture(t)
	e, err := b.operations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer e.budget.close()
	e.store = &racingOperationStore{operationStore: e.store, mode: "preempt"}
	result, err := e.createDefinition(context.Background(), definitionRequest(t, "target", nil))
	if err != nil || !result.Replayed || !result.Noop || result.PriorMutation {
		t.Fatalf("native replay during call is not a fresh or pre-existing aggregate: %+v %v", result, err)
	}
}
