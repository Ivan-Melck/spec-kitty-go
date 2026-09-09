package hubnot

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func graphRequest(operation, blocking, blocked string) tracker.DependencyRequest {
	return tracker.DependencyRequest{ProjectID: "project", OperationID: ids.OperationID(operation), Dependency: tracker.DependencyRef{BlockingWorkID: ids.WorkID(blocking), BlockedWorkID: ids.WorkID(blocked), Kind: tracker.DependencyBlocks}}
}
func graphCreate(t *testing.T, b *BoundAdapter, names ...string) map[string]tracker.ExternalWorkRef {
	t.Helper()
	result := map[string]tracker.ExternalWorkRef{}
	for _, name := range names {
		v, err := b.CreateDefinition(context.Background(), definitionRequest(t, name, nil))
		if err != nil {
			t.Fatal(err)
		}
		result[name] = v.Observation.ExternalRef()
	}
	return result
}
func graphObserve(t *testing.T, b *BoundAdapter, ref tracker.ExternalWorkRef) workObservation {
	t.Helper()
	budget := newObservationBudget()
	defer budget.close()
	v, err := b.observe(context.Background(), budget, ref)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func graphHumanState(t *testing.T, b *BoundAdapter, ref tracker.ExternalWorkRef, key string, change func(*issueState)) {
	t.Helper()
	observed := graphObserve(t, b, ref)
	state := cloneIssueState(observed.State)
	change(&state)
	budget := newObservationBudget()
	defer budget.close()
	if _, _, err := b.mutate(context.Background(), budget, mutationRequest{intent: "revise", id: ref.ExternalID, key: key, parents: headIDs(observed.Heads), state: &state}); err != nil {
		t.Fatal(err)
	}
}
func graphAssertContent(t *testing.T, b *BoundAdapter, ref tracker.ExternalWorkRef, want tracker.WorkDefinition, deps []ids.WorkID) {
	t.Helper()
	got, err := b.ObserveDefinition(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	d := got.Definition()
	if d.Title() != want.Title() || d.Description() != want.Description() || d.DesignContext() != want.DesignContext() || d.CrossDomainReason() != want.CrossDomainReason() || !reflect.DeepEqual(d.Criteria(), want.Criteria()) || !reflect.DeepEqual(d.WriteScopes(), want.WriteScopes()) || !reflect.DeepEqual(d.Provenance(), want.Provenance()) || !bytes.Equal(d.ProviderMetadata().Bytes(), want.ProviderMetadata().Bytes()) || !reflect.DeepEqual(d.Dependencies(), deps) {
		t.Fatalf("graph edit lost complete definition fields: %+v", d)
	}
}
func TestGraphPublicDirectionPreservationAndHistoricalReplay(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	mapped := graphCreate(t, b, "supplier", "target", "other")
	graphHumanState(t, b, mapped["supplier"], "human-foreign", func(s *issueState) {
		s.Labels = []string{"human-label"}
		s.Assignees = []string{"human"}
		s.Metadata["foreign/key"] = "retained"
		s.Relations = []issueRelation{{Kind: "parent", Target: mapped["other"].ExternalID}, {Kind: "related", Target: mapped["target"].ExternalID}}
	})
	beforeSource := graphObserve(t, b, mapped["supplier"])
	req := graphRequest("shared-operation", "supplier", "target")
	put, err := b.PutBlockingDependency(ctx, req)
	if err != nil || put.Replayed || put.Noop {
		t.Fatalf("put: %+v %v", put, err)
	}
	if put.Dependency.BlockingWorkID != "supplier" || put.Dependency.BlockedWorkID != "target" || put.Dependency.ExternalEdgeID != relationIdentity(mapped["supplier"].ExternalID, mapped["target"].ExternalID) || !bytes.Contains(put.Observation.CanonicalFields(), []byte("original-operation/1")) {
		t.Fatal("direction or original proof missing")
	}
	beforeReads := refs()
	incoming, err := b.ListDependencies(ctx, mapped["target"])
	if err != nil || len(incoming) != 1 || incoming[0] != put.Dependency {
		t.Fatalf("incoming: %+v %v", incoming, err)
	}
	outgoingAsDependencies, err := b.ListDependencies(ctx, mapped["supplier"])
	if err != nil || len(outgoingAsDependencies) != 0 {
		t.Fatalf("reversed dependencies: %+v %v", outgoingAsDependencies, err)
	}
	afterSource := graphObserve(t, b, mapped["supplier"])
	if !reflect.DeepEqual(afterSource.State.Labels, beforeSource.State.Labels) || !reflect.DeepEqual(afterSource.State.Assignees, beforeSource.State.Assignees) || afterSource.State.Metadata["foreign/key"] != "retained" || len(afterSource.State.Relations) != 3 {
		t.Fatal("foreign native fields lost")
	}
	if !bytes.Equal(beforeReads, refs()) {
		t.Fatal("graph read changed refs")
	}
	graphAssertContent(t, b, mapped["target"], definitionRequest(t, "target", nil).Definition, []ids.WorkID{"supplier"})
	graphAssertContent(t, b, mapped["supplier"], definitionRequest(t, "supplier", nil).Definition, nil)
	remove, err := b.RemoveBlockingDependency(ctx, req)
	if err != nil || remove.Replayed || remove.Noop || remove.Dependency != put.Dependency {
		t.Fatalf("remove identity: %+v %v", remove, err)
	}
	graphAssertContent(t, b, mapped["target"], definitionRequest(t, "target", nil).Definition, nil)
	graphHumanState(t, b, mapped["supplier"], "human-unrelated", func(s *issueState) {
		s.Relations = append(s.Relations, issueRelation{Kind: "blocks", Target: mapped["other"].ExternalID})
	})
	before := refs()
	reopened, err := Open(ctx, b.config)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := reopened.PutBlockingDependency(ctx, req)
	if err != nil || !replay.Replayed || !replay.Noop || replay.Dependency != put.Dependency || replay.Observation.Digest != put.Observation.Digest || !bytes.Equal(before, refs()) {
		t.Fatalf("historical replay rewrote current work: %+v %v", replay, err)
	}
	fresh := graphObserve(t, b, mapped["target"])
	if len(fresh.Dependencies) != 0 {
		t.Fatal("original link replay recreated human-removed edge")
	}
	other, err := b.ListDependencies(ctx, mapped["other"])
	if err != nil || len(other) != 1 || other[0].BlockingWorkID != "supplier" {
		t.Fatalf("human unrelated edge lost: %+v %v", other, err)
	}
}
func TestGraphSignedNoopAndDivergentReuse(t *testing.T) {
	b, refs := operationFixture(t)
	graphCreate(t, b, "supplier", "target")
	ctx := context.Background()
	req := graphRequest("noop-remove", "supplier", "target")
	before := refs()
	first, err := b.RemoveBlockingDependency(ctx, req)
	if err != nil || !first.Noop || first.Replayed || bytes.Equal(before, refs()) {
		t.Fatalf("new noop lacks signed operation: %+v %v", first, err)
	}
	before = refs()
	again, err := b.RemoveBlockingDependency(ctx, req)
	if err != nil || !again.Noop || !again.Replayed || again.Observation.Digest != first.Observation.Digest || !bytes.Equal(before, refs()) {
		t.Fatalf("noop replay: %+v %v", again, err)
	}
	changed := req
	changed.Dependency.BlockedWorkID, changed.Dependency.BlockingWorkID = changed.Dependency.BlockingWorkID, changed.Dependency.BlockedWorkID
	if _, err := b.RemoveBlockingDependency(ctx, changed); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || !bytes.Equal(before, refs()) {
		t.Fatalf("divergent operation reused: %v", err)
	}
}
func TestGraphReadyUsesOpenSuppliersAndStaysDiagnostic(t *testing.T) {
	f := &fixtureReader{rows: []issueDetail{fixtureDetail(t, "open-source", "1"), fixtureDetail(t, "blocked", "2"), fixtureDetail(t, "closed-source", "3"), fixtureDetail(t, "ready", "4")}}
	for i := range f.rows {
		f.rows[i].State.Status = "open"
	}
	f.rows[0].State.Relations = []issueRelation{{Kind: "blocks", Target: f.rows[1].ID}}
	f.rows[2].State.Status = "closed"
	f.rows[2].State.Relations = []issueRelation{{Kind: "blocks", Target: f.rows[3].ID}}
	request := tracker.ReadyRequest{ProjectID: "project", PinnedReadyIDs: []ids.WorkID{"blocked", "ready"}}
	budget := newObservationBudget()
	defer budget.close()
	got, err := readyProjection(context.Background(), f, budget, request)
	if err != nil || !reflect.DeepEqual(got.ProviderReadyIDs, []ids.WorkID{"open-source", "ready"}) || len(got.Diagnostics) != 2 || got.Observation.Revision == 0 || !bytes.Contains(got.Observation.CanonicalFields(), []byte("diagnostic-ready/1")) {
		t.Fatalf("ready: %+v %v", got, err)
	}
	for _, d := range got.Diagnostics {
		if d.RecoveryCode != "reconcile_pinned_graph" {
			t.Fatalf("unexpected diagnostic: %+v", d)
		}
	}
	second := request
	second.PinnedReadyIDs = []ids.WorkID{"ready", "blocked", "ready"}
	budget2 := newObservationBudget()
	defer budget2.close()
	again, err := readyProjection(context.Background(), f, budget2, second)
	if err != nil || again.Observation.Digest != got.Observation.Digest {
		t.Fatalf("pin set order changed proof: %v", err)
	}
	b := newObservationBudget()
	defer b.close()
	view, err := collectInventory(context.Background(), f, b, "project")
	if err != nil || !reflect.DeepEqual(view.currentDependencies("ready"), []ids.WorkID{"closed-source"}) {
		t.Fatalf("closed supplier relation erased: %v", err)
	}
}
func TestGraphInvalidAndHostileViewsNeverWrite(t *testing.T) {
	b, refs := operationFixture(t)
	mapped := graphCreate(t, b, "supplier", "target")
	ctx := context.Background()
	before := refs()
	for _, change := range []string{"project", "operation", "kind", "self", "missing"} {
		t.Run(change, func(t *testing.T) {
			req := graphRequest("invalid-"+change, "supplier", "target")
			switch change {
			case "project":
				req.ProjectID = "other"
			case "operation":
				req.OperationID = ""
			case "kind":
				req.Dependency.Kind = "related"
			case "self":
				req.Dependency.BlockedWorkID = "supplier"
			case "missing":
				req.Dependency.BlockingWorkID = "absent"
			}
			if _, err := b.PutBlockingDependency(ctx, req); err == nil || !bytes.Equal(before, refs()) {
				t.Fatalf("invalid graph wrote: %v", err)
			}
		})
	}
	bad := mapped["target"]
	bad.ProjectID = "other"
	if _, err := b.ListDependencies(ctx, bad); err == nil || !bytes.Equal(before, refs()) {
		t.Fatal("invalid graph reference wrote")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.ReadyProjection(ctx, tracker.ReadyRequest{ProjectID: "project"}); err == nil || !bytes.Equal(before, refs()) {
		t.Fatal("canceled graph read did not refuse")
	}
	for _, kind := range []string{"cycle", "missing", "unmapped", "conflict"} {
		t.Run(kind, func(t *testing.T) {
			f := &fixtureReader{rows: []issueDetail{fixtureDetail(t, "source", "1"), fixtureDetail(t, "target", "2")}}
			f.rows[0].State.Relations = []issueRelation{{Kind: "blocks", Target: f.rows[1].ID}}
			switch kind {
			case "cycle":
				f.rows[1].State.Relations = []issueRelation{{Kind: "blocks", Target: f.rows[0].ID}}
			case "missing":
				f.rows[0].State.Relations[0].Target = "sha256:" + strings.Repeat("f", 64)
			case "unmapped":
				f.rows[0].OpeningMetadata = map[string]string{}
				f.rows[0].State.Metadata = map[string]string{}
			case "conflict":
				f.rows[0].State = nil
				f.rows[0].Conflict = true
				f.rows[0].HeadCount = 2
			}
			budget := newObservationBudget()
			defer budget.close()
			projection, err := readyProjection(context.Background(), f, budget, tracker.ReadyRequest{ProjectID: "project"})
			if err != nil || len(projection.ProviderReadyIDs) != 0 || len(projection.Diagnostics) != 1 || projection.Observation.Revision != 0 || !bytes.Equal(before, refs()) {
				t.Fatalf("hostile view became ready: %+v %v", projection, err)
			}
		})
	}
}
func TestGraphSharedRecoveryRetainsAppliedAndPriorFacts(t *testing.T) {
	b, refs := operationFixture(t)
	mapped := graphCreate(t, b, "supplier", "target")
	ctx := context.Background()
	req := graphRequest("lost-link", "supplier", "target")
	e, s := faultEngine(t, b)
	s.loseResponse = true
	s.loseLookup = true
	_, err := mutateGraph(ctx, e, req, true)
	var oe *OperationError
	if !errors.As(err, &oe) || oe.Outcome != OutcomeAmbiguous || oe.PriorMutation || s.appendCalls != 1 {
		t.Fatalf("lost graph outcome: %#v %v", oe, err)
	}
	graphHumanState(t, b, mapped["supplier"], "human-after-lost-link", func(state *issueState) {
		state.Relations = append(state.Relations, issueRelation{Kind: "related", Target: mapped["target"].ExternalID})
	})
	before := refs()
	retry, tracking := faultEngine(t, b)
	result, err := mutateGraph(ctx, retry, req, true)
	if err != nil || !result.Replayed || !result.Noop || tracking.appendCalls != 0 || tracking.reads != 0 || !bytes.Equal(before, refs()) {
		t.Fatalf("graph retry consulted mutable state: %+v %v", result, err)
	}
	e, s = faultEngine(t, b)
	s.failAppend = 1
	before = refs()
	_, err = mutateGraph(ctx, e, graphRequest("before-append", "supplier", "target"), false)
	if !errors.As(err, &oe) || oe.Outcome != OutcomeUnstarted || oe.AppliedThisCall || !bytes.Equal(before, refs()) {
		t.Fatalf("prewrite graph interruption: %#v %v", oe, err)
	}
}

func TestGraphPublicCanonicalEdgesClosedReadinessAndCycleRefusal(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	mapped := graphCreate(t, b, "alpha", "zulu", "target")
	for _, source := range []string{"zulu", "alpha"} {
		if _, err := b.PutBlockingDependency(ctx, graphRequest("link-"+source, source, "target")); err != nil {
			t.Fatal(err)
		}
	}
	deps, err := b.ListDependencies(ctx, mapped["target"])
	if err != nil || len(deps) != 2 || deps[0].BlockingWorkID != "alpha" || deps[1].BlockingWorkID != "zulu" {
		t.Fatalf("dependency canonical order: %+v %v", deps, err)
	}
	for _, source := range []string{"alpha", "zulu"} {
		graphHumanState(t, b, mapped[source], "close-"+source, func(s *issueState) { s.Status = "closed" })
	}
	before := refs()
	ready, err := b.ReadyProjection(ctx, tracker.ReadyRequest{ProjectID: "project", PinnedReadyIDs: []ids.WorkID{"target"}})
	if err != nil || !reflect.DeepEqual(ready.ProviderReadyIDs, []ids.WorkID{"target"}) || len(ready.Diagnostics) != 0 || ready.Observation.Revision == 0 {
		t.Fatalf("closed blockers prevented diagnostic readiness: %+v %v", ready, err)
	}
	again, err := b.ListDependencies(ctx, mapped["target"])
	if err != nil || !reflect.DeepEqual(again, deps) || !bytes.Equal(before, refs()) {
		t.Fatalf("source revision changed edge identity or reads wrote: %+v %v", again, err)
	}
	graphAssertContent(t, b, mapped["target"], definitionRequest(t, "target", nil).Definition, []ids.WorkID{"alpha", "zulu"})
	if _, err := b.PutBlockingDependency(ctx, graphRequest("cycle-attempt", "target", "alpha")); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || !bytes.Equal(before, refs()) {
		t.Fatalf("prospective cycle wrote: %v", err)
	}

}

type graphSagaFault struct {
	operationStore
	calls, failAt, loseAt int
	lost                  bool
}

func (s *graphSagaFault) append(ctx context.Context, budget *observationBudget, r mutationRequest) (issueMutation, MutationOutcome, error) {
	s.calls++
	if s.calls == s.failAt {
		return issueMutation{}, OutcomeUnstarted, &CommandError{Outcome: OutcomeUnstarted, Cause: failure(tracker.ErrorUnavailable, "bounded_retry", "fixture interrupted before append", nil)}
	}
	v, outcome, err := s.operationStore.append(ctx, budget, r)
	if err == nil && s.calls == s.loseAt {
		s.lost = true
		return issueMutation{}, OutcomeAmbiguous, &CommandError{Outcome: OutcomeAmbiguous, Cause: failure(tracker.ErrorAmbiguous, "observe_then_reconcile", "fixture lost final edge response", nil)}
	}
	return v, outcome, err
}
func (s *graphSagaFault) lookup(ctx context.Context, budget *observationBudget, key string) (issueOperation, error) {
	if s.lost {
		return issueOperation{}, failure(tracker.ErrorUnavailable, "bounded_retry", "fixture lookup unavailable after final edge", nil)
	}
	return s.operationStore.lookup(ctx, budget, key)
}
func TestGraphDefinitionInterruptionAndCompletedSagaReplay(t *testing.T) {
	for _, stage := range []string{"before-opening", "between-opening-edge", "after-final-edge"} {
		t.Run(stage, func(t *testing.T) {
			b, refs := operationFixture(t)
			ctx := context.Background()
			mapped := graphCreate(t, b, "supplier")
			request := definitionRequest(t, "target", []ids.WorkID{"supplier"})
			e, err := b.operations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer e.budget.close()
			fault := &graphSagaFault{operationStore: e.store}
			e.store = fault
			switch stage {
			case "before-opening":
				fault.failAt = 1
			case "between-opening-edge":
				fault.failAt = 2
			case "after-final-edge":
				fault.loseAt = 2
			}
			before := refs()
			partial, err := e.createDefinition(ctx, request)
			var oe *OperationError
			if !errors.As(err, &oe) || oe.PriorMutation {
				t.Fatalf("first-call interruption accounting: %+v %#v %v", partial, oe, err)
			}
			if stage == "before-opening" {
				if oe.AppliedThisCall || !bytes.Equal(before, refs()) {
					t.Fatalf("opening interruption changed refs: %#v", oe)
				}
			} else if !oe.AppliedThisCall || bytes.Equal(before, refs()) {
				t.Fatalf("surviving saga effect lost: %#v", oe)
			}
			resumed, tracking := faultEngine(t, b)
			before = refs()
			complete, err := resumed.createDefinition(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			expectedAppends := 2
			if stage == "between-opening-edge" {
				expectedAppends = 1
			}
			if stage == "after-final-edge" {
				expectedAppends = 0
			}
			if tracking.appendCalls != expectedAppends || complete.PriorMutation != (stage != "before-opening") || complete.Replayed != (stage == "after-final-edge") {
				t.Fatalf("resume flags/census stage=%s result=%+v appends=%d", stage, complete, tracking.appendCalls)
			}
			if (expectedAppends == 0) != bytes.Equal(before, refs()) {
				t.Fatal("resume append/ref census disagrees")
			}
			deps, err := b.ListDependencies(ctx, complete.Observation.ExternalRef())
			if err != nil || len(deps) != 1 || deps[0].BlockingWorkID != "supplier" {
				t.Fatalf("saga graph incomplete: %+v %v", deps, err)
			}
			// New graph intent removes the edge. Replaying historical create must prove
			// original children, never silently recreate the now-removed dependency.
			if _, err := b.RemoveBlockingDependency(ctx, graphRequest("human-unlink", "supplier", "target")); err != nil {
				t.Fatal(err)
			}
			before = refs()
			replay, err := b.CreateDefinition(ctx, request)
			if err != nil || !replay.Replayed || !bytes.Equal(before, refs()) {
				t.Fatalf("completed saga replay changed graph: %+v %v", replay, err)
			}
			deps, err = b.ListDependencies(ctx, complete.Observation.ExternalRef())
			if err != nil || len(deps) != 0 {
				t.Fatalf("historical metadata became current edge: %+v %v", deps, err)
			}
			if mapped["supplier"].ExternalID == "" {
				t.Fatal("supplier missing")
			}
		})
	}
}

type graphUnavailableStore struct {
	operationStore
	reader         observationReader
	calls, appends int
	unavailable    bool
}

func (s *graphUnavailableStore) inventory(ctx context.Context, budget *observationBudget, project ids.ProjectID) (inventoryView, error) {
	s.calls++
	if s.unavailable {
		return collectInventory(ctx, s.reader, budget, project)
	}
	return s.operationStore.inventory(ctx, budget, project)
}
func (s *graphUnavailableStore) append(ctx context.Context, budget *observationBudget, request mutationRequest) (issueMutation, MutationOutcome, error) {
	s.appends++
	return s.operationStore.append(ctx, budget, request)
}
func TestGraphIncompleteAcceptedViewRefusesUntilSupplierAvailable(t *testing.T) {
	for _, kind := range []string{"cycle", "missing-supplier", "pending-admission"} {
		t.Run(kind, func(t *testing.T) {
			b, refs := operationFixture(t)
			graphCreate(t, b, "supplier", "target")
			ctx := context.Background()
			e, err := b.operations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer e.budget.close()
			f := &fixtureReader{rows: []issueDetail{fixtureDetail(t, "supplier", "1"), fixtureDetail(t, "target", "2")}}
			f.rows[0].State.Relations = []issueRelation{{Kind: "blocks", Target: f.rows[1].ID}}
			var reader observationReader = f
			switch kind {
			case "cycle":
				f.rows[1].State.Relations = []issueRelation{{Kind: "blocks", Target: f.rows[0].ID}}
			case "missing-supplier":
				f.rows = f.rows[:1]
			case "pending-admission":
				reader = graphPendingReader{observationReader: f}
			}
			store := &graphUnavailableStore{operationStore: e.store, reader: reader, unavailable: true}
			e.store = store
			before := refs()
			request := graphRequest("after-reconciliation", "supplier", "target")
			_, err = mutateGraph(ctx, e, request, false)
			if err == nil || store.appends != 0 || !bytes.Equal(before, refs()) {
				t.Fatalf("incomplete graph allowed unlink: %v", err)
			}
			if kind == "cycle" && !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
				t.Fatalf("cycle classification: %v", err)
			}
			if kind == "pending-admission" && !tracker.IsKind(err, tracker.ErrorMalformedProtocol) {
				t.Fatalf("admission classification: %v", err)
			}
			// The injected reader now exposes only the actual admitted public fixture.
			// This simulates admission visibility; it does not itself perform admission.
			store.unavailable = false
			result, err := mutateGraph(ctx, e, request, false)
			if err != nil || !result.Noop || result.Replayed || store.appends != 1 || bytes.Equal(before, refs()) {
				t.Fatalf("reconciled graph did not persist same original intent: %+v %v", result, err)
			}
		})
	}
}

type graphPendingReader struct{ observationReader }

func (graphPendingReader) listDetails(context.Context, *observationBudget, string) (page[issueDetail], envelope, error) {
	return page[issueDetail]{}, envelope{}, &CommandError{Outcome: OutcomeUnstarted, ProviderCode: "invalid_history", Cause: providerFailure("invalid_history", "supplier acceptance pending")}
}
