package hubnot

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func operationFixture(t *testing.T) (*BoundAdapter, func() []byte) {
	t.Helper()
	binary := os.Getenv("GO_KITTY_TEST_HUBNOT_BINARY")
	if binary == "" {
		t.Skip("set GO_KITTY_TEST_HUBNOT_BINARY to the admitted executable")
	}
	dir := t.TempDir()
	run := func(binary string, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Dir = dir
		cmd.Env = processEnvironment()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture %v: %v %s", args, err, out)
		}
		return out
	}
	run("/usr/bin/git", "init", "-q")
	run("/usr/bin/git", "-c", "user.name=Operation fixture", "-c", "user.email=fixture@test.invalid", "commit", "--allow-empty", "-q", "-m", "fixture")
	run(binary, "init", "--name", "Operation fixture")
	env, err := decodeEnvelope(run(binary, "identity", "show", "--json"), identitySchema, 0)
	if err != nil {
		t.Fatal(err)
	}
	var identity PublicIdentity
	if err := decodeStrict(env.Data, &identity); err != nil {
		t.Fatal(err)
	}
	b, err := Open(context.Background(), BindingConfig{Schema: bindingSchema, Provider: "hubnot", ProjectID: "project", WorkspacePath: dir, ExecutablePath: binary, ExpectedActor: identity.Actor, Identity: QualifiedProviderIdentity()})
	if err != nil {
		t.Fatal(err)
	}
	return b, func() []byte { return run("/usr/bin/git", "for-each-ref", "--format=%(refname) %(objectname)") }
}
func simpleRequest() tracker.CreateWorkRequest {
	return tracker.CreateWorkRequest{ProjectID: "project", WorkID: "simple", OperationID: "create-simple", Title: "Initial title", Description: "Original body", DesignContext: "Context", Criteria: []tracker.Criterion{{ID: "criterion", Text: "observable", Verifier: tracker.VerifierTest, Required: true}}, MetadataSchema: 1}
}
func TestCatalogOriginalReplayAndForeignPreservation(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	request := simpleRequest()
	created, err := b.CreateWork(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	read, err := b.ReadWork(ctx, created.Record.ExternalRef)
	if err != nil || read.Title != request.Title {
		t.Fatalf("read: %+v %v", read, err)
	}
	budget := newObservationBudget()
	defer budget.close()
	current, err := b.observe(ctx, budget, read.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	foreign := cloneIssueState(current.State)
	foreign.Labels = []string{"human"}
	foreign.Assignees = []string{"native-person"}
	foreign.Metadata["foreign/key"] = "kept"
	if _, _, err := b.mutate(ctx, budget, mutationRequest{intent: "revise", id: read.ExternalRef.ExternalID, key: "human-foreign", parents: []string{current.Heads[0].ID}, state: &foreign}); err != nil {
		t.Fatal(err)
	}
	before := refs()
	replayed, err := b.CreateWork(ctx, request)
	if err != nil || !replayed.Replayed || !replayed.Noop || replayed.Record.Title != request.Title || !bytes.Equal(before, refs()) {
		t.Fatalf("original create replay: %+v %v", replayed, err)
	}
	current, err = b.observe(ctx, budget, read.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	update := tracker.UpdateWorkRequest{ExternalRef: current.Reference, OperationID: "update-simple", ExpectedObservation: current.Provider.Revision, RequiredGuard: tracker.UpdateUnguarded, Title: "Updated title", Description: request.Description, DesignContext: request.DesignContext, Criteria: request.Criteria}
	changed, err := b.UpdateWork(ctx, update)
	if err != nil {
		t.Fatal(err)
	}
	current, err = b.observe(ctx, budget, read.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	if current.State.Metadata["foreign/key"] != "kept" || !reflect.DeepEqual(current.State.Labels, foreign.Labels) || !reflect.DeepEqual(current.State.Assignees, foreign.Assignees) {
		t.Fatal("foreign fields lost")
	}
	human := cloneContent(current.Content)
	human.Title = "Later human title"
	state, err := encodeWorkState(current.State, human)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.mutate(ctx, budget, mutationRequest{intent: "revise", id: read.ExternalRef.ExternalID, key: "later-human", parents: []string{current.Heads[0].ID}, state: &state}); err != nil {
		t.Fatal(err)
	}
	newer, err := b.ReadWork(ctx, read.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	update.ExternalRef = newer.ExternalRef
	update.ExpectedObservation = newer.Observation.Revision
	before = refs()
	again, err := b.UpdateWork(ctx, update)
	if err != nil || !again.Replayed || !again.Noop || again.Record.Title != changed.Record.Title || again.Record.Observation.Digest != changed.Record.Observation.Digest || !bytes.Equal(before, refs()) {
		t.Fatalf("update replay: %+v %v", again, err)
	}
	update.Title = "Divergent reuse"
	if _, err := b.UpdateWork(ctx, update); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("divergent reuse: %v", err)
	}
	if !bytes.Equal(before, refs()) {
		t.Fatal("divergent retry wrote")
	}
}

func TestCatalogCannotRemoveOpeningCompleteMarker(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	defined, err := b.CreateDefinition(ctx, definitionRequest(t, "target", nil))
	if err != nil {
		t.Fatal(err)
	}
	budget := newObservationBudget()
	defer budget.close()
	observed, err := b.observe(ctx, budget, defined.Observation.ExternalRef())
	if err != nil {
		t.Fatal(err)
	}
	// A collaborator rewrites both valid current mode and payload. The immutable
	// signed opening still proves this was a complete definition.
	c := cloneContent(observed.Content)
	c.Complete = false
	c.CrossDomainReason = ""
	c.CreateDependencies = nil
	c.WriteScopes = nil
	c.Provenance = nil
	for i := range c.Criteria {
		c.Criteria[i].Outcome = ""
	}
	state, err := encodeWorkState(observed.State, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.mutate(ctx, budget, mutationRequest{intent: "revise", id: observed.Reference.ExternalID, key: "human-strip-mode", parents: headIDs(observed.Heads), state: &state}); err != nil {
		t.Fatal(err)
	}
	before := refs()
	_, err = b.UpdateWork(ctx, tracker.UpdateWorkRequest{ExternalRef: observed.Reference, OperationID: "escape-guard", RequiredGuard: tracker.UpdateUnguarded, Title: "attempt", Description: "body"})
	if !tracker.IsKind(err, tracker.ErrorGuardedUpdateUnsupported) || !bytes.Equal(before, refs()) {
		t.Fatalf("opening complete marker bypass: %v", err)
	}
}

func TestCatalogEmptyCriteriaAndPrewriteRefusals(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	r := simpleRequest()
	r.Criteria = nil
	created, err := b.CreateWork(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	update := tracker.UpdateWorkRequest{ExternalRef: created.Record.ExternalRef, OperationID: "empty-update", RequiredGuard: tracker.UpdateUnguarded, Title: "No criteria", Description: "body"}
	if _, err := b.UpdateWork(ctx, update); err != nil {
		t.Fatalf("valid empty criteria: %v", err)
	}
	before := refs()
	bad := r
	bad.WorkID = "oversized"
	bad.OperationID = "oversized"
	bad.Title = string(bytes.Repeat([]byte("x"), 1025))
	if _, err := b.CreateWork(ctx, bad); !tracker.IsKind(err, tracker.ErrorInvalidRequest) {
		t.Fatalf("native bound: %v", err)
	}
	update.OperationID = "stale-update"
	update.ExpectedObservation = 1
	if _, err := b.UpdateWork(ctx, update); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("stale: %v", err)
	}
	update.ExternalRef.Provider = "beads"
	update.ExpectedObservation = 0
	if _, err := b.UpdateWork(ctx, update); !tracker.IsKind(err, tracker.ErrorInvalidRequest) {
		t.Fatalf("foreign binding: %v", err)
	}
	if !bytes.Equal(before, refs()) {
		t.Fatal("refusal changed refs")
	}
}

func TestCatalogEmptyCriteriaReplaySemantics(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	r := simpleRequest()
	r.Criteria = nil
	created, err := b.CreateWork(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	update := tracker.UpdateWorkRequest{ExternalRef: created.Record.ExternalRef, OperationID: "empty-semantic-update", Title: "updated title", Description: "body", RequiredGuard: tracker.UpdateUnguarded, Criteria: nil}
	first, err := b.UpdateWork(ctx, update)
	if err != nil {
		t.Fatal(err)
	}
	before := refs()
	update.Criteria = []tracker.Criterion{}
	again, err := b.UpdateWork(ctx, update)
	if err != nil || !again.Replayed || again.Record.Observation.Digest != first.Record.Observation.Digest || !bytes.Equal(before, refs()) {
		t.Fatalf("nil and empty criterion lists changed portable empty-list meaning: replayed=%v err=%v", again.Replayed, err)
	}
}
