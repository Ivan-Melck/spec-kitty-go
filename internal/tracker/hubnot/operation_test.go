package hubnot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type faultOperationStore struct {
	operationStore
	appendCalls   int
	failAppend    int
	loseResponse  bool
	loseLookup    bool
	lookupFailure error
	reads         int
}

func (s *faultOperationStore) lookup(ctx context.Context, b *observationBudget, key string) (issueOperation, error) {
	if s.lookupFailure != nil {
		return issueOperation{}, s.lookupFailure
	}
	if s.loseLookup && s.appendCalls > 0 {
		return issueOperation{}, &CommandError{Outcome: OutcomeUnstarted, Cause: failure(tracker.ErrorUnavailable, "bounded_retry", "lost observation", nil)}
	}
	return s.operationStore.lookup(ctx, b, key)
}
func (s *faultOperationStore) append(ctx context.Context, b *observationBudget, r mutationRequest) (issueMutation, MutationOutcome, error) {
	s.appendCalls++
	if s.appendCalls == s.failAppend {
		return issueMutation{}, OutcomeUnstarted, &CommandError{Outcome: OutcomeUnstarted, Cause: failure(tracker.ErrorUnavailable, "bounded_retry", "injected before append", nil)}
	}
	v, o, err := s.operationStore.append(ctx, b, r)
	if err == nil && s.loseResponse {
		return issueMutation{}, OutcomeAmbiguous, &CommandError{Outcome: OutcomeAmbiguous, Cause: failure(tracker.ErrorAmbiguous, "observe_then_reconcile", "response lost after append", nil)}
	}
	return v, o, err
}
func (s *faultOperationStore) inventory(ctx context.Context, b *observationBudget, p ids.ProjectID) (inventoryView, error) {
	s.reads++
	return s.operationStore.inventory(ctx, b, p)
}
func (s *faultOperationStore) observe(ctx context.Context, b *observationBudget, r tracker.ExternalWorkRef) (workObservation, error) {
	s.reads++
	return s.operationStore.observe(ctx, b, r)
}
func faultEngine(t *testing.T, b *BoundAdapter) (*operationEngine, *faultOperationStore) {
	t.Helper()
	e, err := b.operations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.budget.close)
	store := &faultOperationStore{operationStore: e.store}
	e.store = store
	return e, store
}
func TestOperationLostResponseRestartBeforeMutableGuards(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	e, store := faultEngine(t, b)
	store.loseResponse = true
	store.loseLookup = true
	request := simpleRequest()
	_, err := e.createWork(ctx, request)
	var oe *OperationError
	if !errors.As(err, &oe) || oe.Outcome != OutcomeAmbiguous || oe.PriorMutation {
		t.Fatalf("lost result: %#v %v", oe, err)
	}
	// New process/bound adapter, original key still resolves despite lost response.
	reopened, err := Open(ctx, b.config)
	if err != nil {
		t.Fatal(err)
	}
	budget := newObservationBudget()
	defer budget.close()
	v, err := collectInventory(ctx, reopened, budget, "project")
	if err != nil {
		t.Fatal(err)
	}
	node, ok := v.lookupWork("simple")
	if !ok {
		t.Fatal("applied opening lost")
	}
	observed, err := reopened.observe(ctx, budget, node.reference(0))
	if err != nil {
		t.Fatal(err)
	}
	c := cloneContent(observed.Content)
	c.Title = "Human changed title"
	state, err := encodeWorkState(observed.State, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reopened.mutate(ctx, budget, mutationRequest{intent: "revise", id: node.Detail.ID, key: "human", parents: headIDs(observed.Heads), state: &state}); err != nil {
		t.Fatal(err)
	}
	before := refs()
	retry, tracking := faultEngine(t, reopened)
	result, err := retry.createWork(ctx, request)
	if err != nil || !result.Replayed || result.Record.Title != request.Title || tracking.reads != 0 || tracking.appendCalls != 0 || !bytes.Equal(before, refs()) {
		t.Fatalf("recovery consulted mutable state or wrote: %+v %v reads=%d writes=%d", result, err, tracking.reads, tracking.appendCalls)
	}
}
func TestOperationAcknowledgedResponseLossRecoversOriginal(t *testing.T) {
	b, _ := operationFixture(t)
	e, store := faultEngine(t, b)
	store.loseResponse = true
	result, err := e.createWork(context.Background(), simpleRequest())
	if err != nil || result.Replayed || result.Record.ExternalRef.ExternalID == "" || store.appendCalls != 1 {
		t.Fatalf("observable lost response: %+v %v", result, err)
	}
}
func TestDefinitionPartialSagaPreservesPriorVersusFreshEffects(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	if _, err := b.CreateDefinition(ctx, definitionRequest(t, "supplier", nil)); err != nil {
		t.Fatal(err)
	}
	e, store := faultEngine(t, b)
	store.failAppend = 2
	request := definitionRequest(t, "target", []ids.WorkID{"supplier"})
	result, err := e.createDefinition(ctx, request)
	var oe *OperationError
	if !errors.As(err, &oe) || oe.Outcome != OutcomeApplied || oe.PriorMutation || !oe.AppliedThisCall || result.PriorMutation || result.Observation.ExternalRef().ExternalID == "" {
		t.Fatalf("fresh partial saga: %+v %#v %v", result, oe, err)
	}
	before := refs()
	retry, tracking := faultEngine(t, b)
	completed, err := retry.createDefinition(ctx, request)
	if err != nil || !completed.PriorMutation || completed.Replayed || tracking.appendCalls != 1 || bytes.Equal(before, refs()) {
		t.Fatalf("resume: %+v %v writes=%d", completed, err, tracking.appendCalls)
	}
	before = refs()
	again, err := b.CreateDefinition(ctx, request)
	if err != nil || !again.Replayed || !again.PriorMutation || !bytes.Equal(before, refs()) {
		t.Fatalf("completed replay: %+v %v", again, err)
	}
}
func TestOperationLookupFailureNeverReconstructsOrAppends(t *testing.T) {
	for _, code := range []string{"operation_conflict", "invalid_history", "repository_error"} {
		t.Run(code, func(t *testing.T) {
			b, refs := operationFixture(t)
			e, store := faultEngine(t, b)
			store.lookupFailure = &CommandError{Outcome: OutcomeUnstarted, ProviderCode: code, Cause: providerFailure(code, "incomplete original lookup")}
			before := refs()
			_, err := e.createWork(context.Background(), simpleRequest())
			if err == nil || store.reads != 0 || store.appendCalls != 0 || !bytes.Equal(before, refs()) {
				t.Fatal("uncertain lookup caused mutation")
			}
		})
	}
}
func TestRelationDistinctActionsAndPersistentNoopIntent(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	for _, work := range []string{"supplier", "target"} {
		r := simpleRequest()
		r.WorkID = ids.WorkID(work)
		r.OperationID = ids.OperationID("create-" + work)
		if _, err := b.CreateWork(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	e, _ := faultEngine(t, b)
	put, err := e.replaceBlockingRelation(ctx, "same-operation", "supplier", "target", true, "graph.put")
	if err != nil {
		t.Fatal(err)
	}
	remove, err := e.replaceBlockingRelation(ctx, "same-operation", "supplier", "target", false, "graph.remove")
	if err != nil || put.Event.Operation == remove.Event.Operation {
		t.Fatalf("distinct put/remove: %v", err)
	}
	before := refs()
	noop, err := e.replaceBlockingRelation(ctx, "new-noop", "supplier", "target", false, "graph.remove")
	if err != nil || noop.Replayed || !noop.Stamp.Noop || bytes.Equal(before, refs()) {
		t.Fatalf("durable noop: %+v %v", noop, err)
	}
	before = refs()
	again, err := e.replaceBlockingRelation(ctx, "new-noop", "supplier", "target", false, "graph.remove")
	if err != nil || !again.Replayed || !bytes.Equal(before, refs()) {
		t.Fatalf("noop replay: %+v %v", again, err)
	}
	if _, err := e.replaceBlockingRelation(ctx, "new-noop", "target", "supplier", false, "graph.remove"); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("divergent noop intent: %v", err)
	}
	if !bytes.Equal(before, refs()) {
		t.Fatal("divergent noop wrote")
	}
}
func TestOperationSemanticCriteriaOrderAndOpaquePrecision(t *testing.T) {
	c := richContent(t, "target")
	a, err := contentAction("project", "operation", "definition.create", c)
	if err != nil {
		t.Fatal(err)
	}
	c.Criteria[0], c.Criteria[1] = c.Criteria[1], c.Criteria[0]
	b, err := contentAction("project", "operation", "definition.create", c)
	if err != nil {
		t.Fatal(err)
	}
	if a.key != b.key || a.digest == b.digest {
		t.Fatal("ordered content missing from stable operation intent")
	}
	c.Opaque = []byte(`{"n":123456789012345678901234567891}`)
	d, err := contentAction("project", "operation", "definition.create", c)
	if err != nil || d.digest == b.digest {
		t.Fatal("opaque number precision lost")
	}
	if !strings.Contains(a.intent, "123456789012345678901234567890") {
		t.Fatal("opaque numeric identity missing")
	}
}

func TestActorLookupPreservesPinnedValidatorAndBinding(t *testing.T) {
	binary := testChild(t)
	b := testBound(t, binary)
	budget := newObservationBudget()
	defer budget.close()
	ctx := context.Background()
	actor := strings.Repeat("b", 64)
	key := "actor-operation"
	id := "sha256:" + strings.Repeat("1", 64)
	head := "sha256:" + strings.Repeat("2", 64)
	body := "exact native body"
	event := issueOperation{IssueID: id, EventID: head, Actor: actor, Operation: key, Intent: "comment", Kind: "issue.comment", Timestamp: "2026-09-09T00:00:00Z", Parents: []string{}, Body: &body, Request: id}
	raw := success(event, id)
	scenario(t, b, raw, 0)
	before := b.config
	got, env, err := b.lookupActorOperation(ctx, budget, actor, key)
	if err != nil || got.Actor != actor || env.Snapshot != id || b.config != before {
		t.Fatalf("supplied actor lookup: %+v %v", got, err)
	}
	start := calls(t, b)
	if _, _, err := b.lookupActorOperation(ctx, budget, "invalid", key); err == nil || calls(t, b) != start {
		t.Fatal("invalid actor dispatched")
	}
	event.Actor = b.config.ExpectedActor
	variants := []string{success(event, id), strings.Replace(success(event, id), `"body":"exact native body"`, `"body":"exact native body","state":null`, 1), strings.Replace(success(event, id), `"parents":[]`, `"parents":null`, 1), strings.Replace(success(event, id), `"intent":"comment"`, `"intent":"revise"`, 1), strings.Replace(success(event, id), `"actor":"`+event.Actor+`"`, `"actor":"`+actor+`"`, 1), strings.Replace(success(event, id), `"operation":"actor-operation"`, `"operation":"different"`, 1), strings.Replace(success(event, id), `"timestamp":"2026-09-09T00:00:00Z"`, `"timestamp":"bad"`, 1)}
	for i, raw := range variants {
		scenario(t, b, raw, 0)
		old, oldEnv, oldErr := b.lookupOperation(ctx, budget, key)
		fresh, freshEnv, freshErr := b.lookupActorOperation(ctx, budget, b.config.ExpectedActor, key)
		if (oldErr == nil) != (freshErr == nil) || !reflect.DeepEqual(old, fresh) || oldEnv.Snapshot != freshEnv.Snapshot {
			t.Fatalf("validator parity variant %d: old=%v new=%v", i, oldErr, freshErr)
		}
	}
}

type racingOperationStore struct {
	operationStore
	mode  string
	fired bool
}

func (s *racingOperationStore) append(ctx context.Context, b *observationBudget, r mutationRequest) (issueMutation, MutationOutcome, error) {
	if s.fired {
		return s.operationStore.append(ctx, b, r)
	}
	s.fired = true
	if s.mode == "preempt" {
		if _, _, err := s.operationStore.append(ctx, b, r); err != nil {
			return issueMutation{}, OutcomeUnstarted, err
		}
		return s.operationStore.append(ctx, b, r)
	}
	v, o, err := s.operationStore.append(ctx, b, r)
	if err != nil {
		return v, o, err
	}
	if s.mode == "duplicate" {
		r.key = "independent-racing-opening"
		if _, _, err := s.operationStore.append(ctx, b, r); err != nil {
			return v, o, err
		}
	}
	return v, o, nil
}
func TestOperationRegressionCatalogNativeReplayFlags(t *testing.T) {
	b, _ := operationFixture(t)
	e, err := b.operations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer e.budget.close()
	e.store = &racingOperationStore{operationStore: e.store, mode: "preempt"}
	got, err := e.createWork(context.Background(), simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Replayed || !got.Noop {
		t.Fatalf("verified original lookup after native replay returned fresh flags: replayed=%v noop=%v", got.Replayed, got.Noop)
	}
}
func TestOperationRegressionCatalogFreshDuplicateMapping(t *testing.T) {
	b, _ := operationFixture(t)
	e, err := b.operations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer e.budget.close()
	e.store = &racingOperationStore{operationStore: e.store, mode: "duplicate"}
	got, err := e.createWork(context.Background(), simpleRequest())
	if err == nil {
		_, readErr := b.Inventory(context.Background(), tracker.InventoryRequest{ProjectID: "project"})
		t.Fatalf("fresh catalog reported success despite accepted duplicate mapping; id=%s subsequent inventory=%v", got.Record.ExternalRef.ExternalID, readErr)
	}
	var oe *OperationError
	if !errors.As(err, &oe) || oe.Outcome != OutcomeApplied || !oe.AppliedThisCall {
		t.Fatalf("post-append conflict lost applied facts: %#v %v", oe, err)
	}
}
func TestOperationRegressionHistoricalSagaRejectsWrongTargetCreation(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	supplier, err := b.CreateDefinition(ctx, definitionRequest(t, "supplier", nil))
	if err != nil {
		t.Fatal(err)
	}
	request := definitionRequest(t, "target", []ids.WorkID{"supplier"})
	e, fault := faultEngine(t, b)
	fault.failAppend = 2
	partial, err := e.createDefinition(ctx, request)
	if err == nil {
		t.Fatal("expected partial fixture")
	}
	target := partial.Observation.ExternalRef()
	budget := newObservationBudget()
	defer budget.close()
	source, err := b.observe(ctx, budget, supplier.Observation.ExternalRef())
	if err != nil {
		t.Fatal(err)
	}
	a, err := relationAction("project", definitionChildID(request.OperationID, "supplier", "target"), "supplier", "target", true, "definition.edge")
	if err != nil {
		t.Fatal(err)
	}
	state := cloneIssueState(source.State)
	state.Relations = append(state.Relations, issueRelation{Kind: "blocks", Target: target.ExternalID})
	stamp := a.stamp(false)
	wrong := immutableReference(target)
	wrong.CreatedByOperation = "different-creation"
	stamp.Target = &wrong
	c := cloneContent(source.Content)
	c.Operation, err = json.Marshal(stamp)
	if err != nil {
		t.Fatal(err)
	}
	state, err = encodeWorkState(state, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.mutate(ctx, budget, mutationRequest{intent: "revise", id: source.Reference.ExternalID, key: a.key, parents: headIDs(source.Heads), state: &state}); err != nil {
		t.Fatal(err)
	}
	before := refs()
	got, err := b.CreateDefinition(ctx, request)
	if err == nil {
		t.Fatalf("accepted historically complete saga with target creation attribution contradicting its own original opening: replayed=%v", got.Replayed)
	}
	if !bytes.Equal(before, refs()) {
		t.Fatal("attribution refusal mutated refs")
	}
}

type replayWithoutCurrentRead struct {
	*racingOperationStore
	reads int
}

func (s *replayWithoutCurrentRead) observe(context.Context, *observationBudget, tracker.ExternalWorkRef) (workObservation, error) {
	s.reads++
	return workObservation{}, fmt.Errorf("current state unavailable after verified native replay")
}
func TestOperationAggregateDefinitionNativeReplayNeedsNoNewState(t *testing.T) {
	b, _ := operationFixture(t)
	e, err := b.operations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer e.budget.close()
	s := &replayWithoutCurrentRead{racingOperationStore: &racingOperationStore{operationStore: e.store, mode: "preempt"}}
	e.store = s
	got, err := e.createDefinition(context.Background(), definitionRequest(t, "target", nil))
	if err != nil || !got.Replayed || got.PriorMutation || s.reads != 0 {
		t.Fatalf("verified complete native replay consulted mutable state: replayed=%v prior=%v reads=%d err=%v", got.Replayed, got.PriorMutation, s.reads, err)
	}
}

type lateOriginalChild struct {
	operationStore
	key     string
	calls   int
	publish func()
}

func (s *lateOriginalChild) lookup(ctx context.Context, b *observationBudget, key string) (issueOperation, error) {
	if key == s.key {
		s.calls++
		if s.calls == 2 {
			s.publish()
		}
	}
	return s.operationStore.lookup(ctx, b, key)
}
func TestOperationAggregateSagaLateChildTargetStillChecked(t *testing.T) {
	b, _ := operationFixture(t)
	ctx := context.Background()
	supplier, err := b.CreateDefinition(ctx, definitionRequest(t, "supplier", nil))
	if err != nil {
		t.Fatal(err)
	}
	request := definitionRequest(t, "target", []ids.WorkID{"supplier"})
	e, fault := faultEngine(t, b)
	fault.failAppend = 2
	partial, err := e.createDefinition(ctx, request)
	if err == nil {
		t.Fatal("expected partial fixture")
	}
	target := partial.Observation.ExternalRef()
	a, err := relationAction("project", definitionChildID(request.OperationID, "supplier", "target"), "supplier", "target", true, "definition.edge")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := b.operations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer retry.budget.close()
	s := &lateOriginalChild{operationStore: retry.store, key: a.key}
	s.publish = func() {
		budget := newObservationBudget()
		defer budget.close()
		source, err := b.observe(ctx, budget, supplier.Observation.ExternalRef())
		if err != nil {
			t.Fatal(err)
		}
		state := cloneIssueState(source.State)
		state.Relations = append(state.Relations, issueRelation{Kind: "blocks", Target: target.ExternalID})
		stamp := a.stamp(false)
		wrong := immutableReference(target)
		wrong.CreatedByOperation = "different-creation"
		stamp.Target = &wrong
		c := cloneContent(source.Content)
		c.Operation, err = json.Marshal(stamp)
		if err != nil {
			t.Fatal(err)
		}
		state, err = encodeWorkState(state, c)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := b.mutate(ctx, budget, mutationRequest{intent: "revise", id: source.Reference.ExternalID, key: a.key, parents: headIDs(source.Heads), state: &state}); err != nil {
			t.Fatal(err)
		}
	}
	retry.store = s
	got, err := retry.createDefinition(ctx, request)
	if err == nil {
		t.Fatalf("child discovered after initial census bypassed original target mapping validation: replayed=%v prior=%v", got.Replayed, got.PriorMutation)
	}
}
