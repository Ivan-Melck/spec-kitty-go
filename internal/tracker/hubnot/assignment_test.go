package hubnot

import (
	"bytes"
	"context"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"testing"
)

func TestAssignmentOriginalRecoveryAndRefusals(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	created, err := b.CreateWork(ctx, simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	before, err := b.ObserveAssignment(ctx, created.Record.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	r := tracker.ProjectAssignmentRequest{ExternalRef: before.ExternalRef, OperationID: "assign-one", Assignee: "human", ExpectedObservation: before.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded}
	first, err := b.ProjectAssignment(ctx, r)
	if err != nil || first.Replayed || first.Observation.OperationMarker != "assign-one" {
		t.Fatalf("first: %+v %v", first, err)
	}
	seen, err := b.ObserveAssignment(ctx, r.ExternalRef)
	if err != nil || seen.OperationMarker != "assign-one" {
		t.Fatalf("observed: %+v %v", seen, err)
	}
	snapshot := refs()
	again, err := b.ProjectAssignment(ctx, r)
	if err != nil || !again.Replayed || !bytes.Equal(snapshot, refs()) {
		t.Fatalf("replay %+v %v", again, err)
	}
	r.RequiredGuarantee = tracker.AssignmentAtomicClaim
	if _, err := b.ProjectAssignment(ctx, r); !tracker.IsKind(err, tracker.ErrorGuardedUpdateUnsupported) || !bytes.Equal(snapshot, refs()) {
		t.Fatalf("atomic %v", err)
	}
	if _, err := b.CompensateAssignment(ctx, tracker.CompensateAssignmentRequest{ExternalRef: r.ExternalRef}); !tracker.IsKind(err, tracker.ErrorSafeCompensationUnsupported) || !bytes.Equal(snapshot, refs()) {
		t.Fatalf("compensation %v", err)
	}
}

func humanState(t *testing.T, b *BoundAdapter, ref tracker.ExternalWorkRef, key string, edit func(*issueState)) {
	t.Helper()
	budget := newObservationBudget()
	defer budget.close()
	o, err := b.observe(context.Background(), budget, ref)
	if err != nil {
		t.Fatal(err)
	}
	state := cloneIssueState(o.State)
	edit(&state)
	if _, _, err := b.mutate(context.Background(), budget, mutationRequest{intent: "revise", id: ref.ExternalID, key: key, parents: headIDs(o.Heads), state: &state}); err != nil {
		t.Fatal(err)
	}
}
func TestAssignmentAncestryPreservesOnlyContinuousOrigin(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	created, err := b.CreateWork(ctx, simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	ref := created.Record.ExternalRef
	r := tracker.ProjectAssignmentRequest{ExternalRef: ref, OperationID: "assignment-origin", Assignee: "A", RequiredGuarantee: tracker.AssignmentUnguarded}
	first, err := b.ProjectAssignment(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	humanState(t, b, ref, "native-unrelated", func(s *issueState) {
		s.Labels = append(s.Labels, "human-label")
		s.Metadata["foreign/note"] = "preserved"
	})
	o, err := b.ObserveAssignment(ctx, ref)
	if err != nil || o.OperationMarker != r.OperationID.String() {
		t.Fatalf("lost continuous origin %+v %v", o, err)
	}
	// Deliberately retain the copied assignment Operation on both native revisions.
	humanState(t, b, ref, "native-B", func(s *issueState) { s.Assignees = []string{"B"} })
	humanState(t, b, ref, "native-A", func(s *issueState) { s.Assignees = []string{"A"} })
	o, err = b.ObserveAssignment(ctx, ref)
	if err != nil || o.OperationMarker != "" || o.Assignee != "A" {
		t.Fatalf("copied stamp resurrected origin %+v %v", o, err)
	}
	before := refs()
	replay, err := b.ProjectAssignment(ctx, r)
	if err != nil || !replay.Replayed || replay.Observation.Observation.Digest != first.Observation.Observation.Digest || !bytes.Equal(before, refs()) {
		t.Fatalf("historical replay %+v %v", replay, err)
	}
	changed := r
	changed.OperationID = "another-assignment"
	changed.ExpectedObservation = 1
	if _, err := b.ProjectAssignment(ctx, changed); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || !bytes.Equal(before, refs()) {
		t.Fatalf("stale wrote %v", err)
	}
	changed = r
	changed.Assignee = "C"
	if _, err := b.ProjectAssignment(ctx, changed); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || !bytes.Equal(before, refs()) {
		t.Fatalf("divergent wrote %v", err)
	}
	humanState(t, b, ref, "native-multiple", func(s *issueState) { s.Assignees = []string{"A", "B"} })
	before = refs()
	if _, err := b.ObserveAssignment(ctx, ref); !tracker.IsKind(err, tracker.ErrorAmbiguous) || !bytes.Equal(before, refs()) {
		t.Fatalf("multiassignee read %v", err)
	}
	changed = r
	changed.OperationID = "multi-refusal"
	if _, err := b.ProjectAssignment(ctx, changed); !tracker.IsKind(err, tracker.ErrorAmbiguous) || !bytes.Equal(before, refs()) {
		t.Fatalf("multiassignee write %v", err)
	}
}
func TestAssignmentOriginMergeAndMissingAncestors(t *testing.T) {
	state := issueState{Assignees: []string{"A"}}
	row := func(id string, parents ...string) issueHistoryItem {
		return issueHistoryItem{ID: id, State: &state, Parents: parents}
	}
	nodes := map[string]issueHistoryItem{"root": row("root", "left", "right"), "left": row("left", "origin"), "right": row("right", "origin"), "origin": row("origin")}
	managed := func(r issueHistoryItem) (string, error) {
		if r.ID == "origin" || r.ID == "second" {
			return r.ID, nil
		}
		return "", nil
	}
	got, err := assignmentOrigin("root", []string{"A"}, nodes, managed)
	if err != nil || got != "origin" {
		t.Fatalf("convergent merge %q %v", got, err)
	}
	nodes["second"] = row("second")
	nodes["right"] = row("right", "second")
	if _, err := assignmentOrigin("root", []string{"A"}, nodes, managed); !tracker.IsKind(err, tracker.ErrorAttributionLost) {
		t.Fatalf("contradictory merge %v", err)
	}
	delete(nodes, "second")
	if _, err := assignmentOrigin("root", []string{"A"}, nodes, managed); err == nil {
		t.Fatal("missing ancestor accepted")
	}
	nodes["right"] = row("right", "root")
	if _, err := assignmentOrigin("root", []string{"A"}, nodes, managed); err == nil {
		t.Fatal("cycle accepted")
	}
}
func TestAssignmentLostResponsePreservesOriginal(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	created, err := b.CreateWork(ctx, simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	r := tracker.ProjectAssignmentRequest{ExternalRef: created.Record.ExternalRef, OperationID: "assignment-lost", Assignee: "A", RequiredGuarantee: tracker.AssignmentUnguarded}
	e, s := faultEngine(t, b)
	s.loseResponse = true
	s.loseLookup = true
	if _, err := e.projectAssignment(ctx, r); err == nil {
		t.Fatal("expected ambiguous response")
	}
	humanState(t, b, r.ExternalRef, "reassign-human", func(state *issueState) { state.Assignees = []string{"human"} })
	before := refs()
	retry, store := faultEngine(t, b)
	got, err := retry.projectAssignment(ctx, r)
	if err != nil || !got.Replayed || got.Observation.Assignee != "A" || store.reads != 0 || store.appendCalls != 0 || !bytes.Equal(before, refs()) {
		t.Fatalf("replay mutated human choice %+v %v", got, err)
	}
}
