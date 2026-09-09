package hubnot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"os/exec"
	"strings"
	"testing"
)

func TestDiscussionExactPairReplayAndLegacyProjection(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	created, err := b.CreateWork(ctx, simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	r := tracker.AppendDiscussionRequest{ExternalRef: created.Record.ExternalRef, OperationID: "comment-one", Author: "accountable author", Body: "  exact 😀\nbody\n", ExpectedObservation: created.Record.Observation.Revision}
	current, err := b.ReadWork(ctx, r.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedObservation = current.Observation.Revision
	first, err := b.AppendAttributedDiscussion(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if first.Entry.Entry().Body != r.Body || first.Entry.Entry().Author != r.Author || first.Entry.SigningActor() != b.config.ExpectedActor || first.Replayed {
		t.Fatalf("first %+v", first)
	}
	before := refs()
	second, err := b.AppendAttributedDiscussion(ctx, r)
	if err != nil || !second.Replayed || second.Entry.Entry() != first.Entry.Entry() || !bytes.Equal(before, refs()) {
		t.Fatalf("replay %+v %v", second, err)
	}
	legacy, err := b.AppendDiscussion(ctx, r)
	if err != nil || legacy.Entry != first.Entry.Entry() || !legacy.Replayed {
		t.Fatalf("legacy %+v %v", legacy, err)
	}
	observed, err := b.ObserveDiscussion(ctx, r.ExternalRef)
	if err != nil || len(observed.Entries()) != 1 || observed.Entries()[0].Entry().Body != r.Body || observed.CurrentWorkObservation().Revision == 0 {
		t.Fatalf("observe %+v %v", observed, err)
	}
	r.Author = "different"
	if _, err := b.AppendAttributedDiscussion(ctx, r); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || !bytes.Equal(before, refs()) {
		t.Fatalf("divergent %v", err)
	}
}
func TestDiscussionPartialSagaAndInvalidInputNoWrites(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	created, err := b.CreateWork(ctx, simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	r := tracker.AppendDiscussionRequest{ExternalRef: created.Record.ExternalRef, OperationID: "paired", Author: "author", Body: "body"}
	for _, body := range []string{"", strings.Repeat("x", 65537), "\xff", "null\x00"} {
		bad := r
		bad.Body = body
		before := refs()
		if _, err := b.AppendAttributedDiscussion(ctx, bad); err == nil || !bytes.Equal(before, refs()) {
			t.Fatalf("invalid body wrote: %v", err)
		}
	}
	e, store := faultEngine(t, b)
	store.failAppend = 2
	partial, err := e.appendDiscussion(ctx, r, b.identity.Name)
	var oe *OperationError
	if !errors.As(err, &oe) || oe.Outcome != OutcomeApplied || !oe.AppliedThisCall || oe.PriorMutation || partial.PriorMutation {
		t.Fatalf("partial %+v %#v %v", partial, oe, err)
	}
	retry, s := faultEngine(t, b)
	done, err := retry.appendDiscussion(ctx, r, b.identity.Name)
	if err != nil || !done.PriorMutation || done.Replayed || s.appendCalls != 1 {
		t.Fatalf("resume %+v %v writes=%d", done, err, s.appendCalls)
	}
}

type unreadableWorkStore struct{ operationStore }

func (s unreadableWorkStore) observe(context.Context, *observationBudget, tracker.ExternalWorkRef) (workObservation, error) {
	return workObservation{}, errors.New("mutable work unavailable")
}
func TestDiscussionOriginalPairAfterHumanEditAndLostResponse(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	created, err := b.CreateWork(ctx, simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	r := tracker.AppendDiscussionRequest{ExternalRef: created.Record.ExternalRef, OperationID: "recover-pair", Author: "accountable", Body: "exact original\n"}
	first, err := b.AppendAttributedDiscussion(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	humanState(t, b, r.ExternalRef, "human-after-pair", func(state *issueState) { state.Labels = append(state.Labels, "edited") })
	before := refs()
	e, err := b.operations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer e.budget.close()
	e.store = unreadableWorkStore{e.store}
	replay, err := e.appendDiscussion(ctx, r, b.identity.Name)
	if err != nil || !replay.Replayed || replay.Entry.Entry() != first.Entry.Entry() || replay.Entry.Proof().Digest != first.Entry.Proof().Digest || !bytes.Equal(before, refs()) {
		t.Fatalf("historical pair %+v %v", replay, err)
	}
	r.OperationID = "lost-comment"
	fault, store := faultEngine(t, b)
	store.loseResponse = true
	got, err := fault.appendDiscussion(ctx, r, b.identity.Name)
	if err != nil || got.Replayed || store.appendCalls != 2 {
		t.Fatalf("lost acknowledgment %+v %v", got, err)
	}
	r.OperationID = "lost-attribution"
	fault, store = faultEngine(t, b)
	store.loseResponse = true
	store.loseLookup = true
	_, err = fault.appendDiscussion(ctx, r, b.identity.Name)
	var oe *OperationError
	if !errors.As(err, &oe) || oe.Outcome != OutcomeAmbiguous || oe.PriorMutation {
		t.Fatalf("lost attribution %#v %v", oe, err)
	}
	resumed, err := b.AppendAttributedDiscussion(ctx, r)
	if err != nil || !resumed.PriorMutation || resumed.Replayed {
		t.Fatalf("resume %+v %v", resumed, err)
	}
}
func TestDiscussionAuthorAndNativeCapacityPreflight(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	created, err := b.CreateWork(ctx, simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	r := tracker.AppendDiscussionRequest{ExternalRef: created.Record.ExternalRef, OperationID: "preflight", Author: "author", Body: "body"}
	for _, author := range []string{"", strings.Repeat("a", 257), "\xff", "bad\x00"} {
		bad := r
		bad.Author = author
		before := refs()
		if _, err := b.AppendAttributedDiscussion(ctx, bad); err == nil || !bytes.Equal(before, refs()) {
			t.Fatalf("invalid author caused effect %v", err)
		}
	}
	before := refs()
	r.ExpectedObservation = 1
	if _, err := b.AppendAttributedDiscussion(ctx, r); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || !bytes.Equal(before, refs()) {
		t.Fatalf("stale caused effect %v", err)
	}
	r.ExpectedObservation = 0
	// Fill native metadata count with valid unrelated data. Adding a sufficiently
	// large attribution payload requires another owned chunk and must fail before
	// either child, rather than dropping foreign fields to fit.
	humanState(t, b, r.ExternalRef, "fill-native-metadata", func(s *issueState) {
		c, _, err := decodeWorkState(*s)
		if err != nil {
			t.Fatal(err)
		}
		c.Operation = nil
		c.DesignContext = strings.Repeat("x", 3000)
		encoded, err := encodeWorkState(*s, c)
		if err != nil {
			t.Fatal(err)
		}
		*s = encoded
		for i := len(s.Metadata); i < 64; i++ {
			s.Metadata[fmt.Sprintf("foreign/key-%d", i)] = "x"
		}
	})
	r.Author = strings.Repeat("a", 256)
	before = refs()
	e, store := faultEngine(t, b)
	_, err = e.appendDiscussion(ctx, r, b.identity.Name)
	if err == nil || store.appendCalls != 0 || !bytes.Equal(before, refs()) {
		t.Fatalf("native attribution capacity %v appends=%d", err, store.appendCalls)
	}
}
func syncDiscussionFixture(t *testing.T, destination, source *BoundAdapter, name string) {
	t.Helper()
	run := func(executable string, args ...string) {
		t.Helper()
		cmd := exec.Command(executable, args...)
		cmd.Dir = destination.binding.WorkspacePath
		cmd.Env = processEnvironment()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("sync fixture %v: %v %s", args, err, out)
		}
	}
	run("/usr/bin/git", "remote", "add", name, source.binding.WorkspacePath)
	run(destination.binding.ExecutablePath, "replication", "select", name, "--actor", source.config.ExpectedActor, "--max-events", "100", "--max-objects", "1000", "--max-object-bytes", "16777216", "--max-attachment-bytes", "1048576", "--max-total-bytes", "268435456")
	run(destination.binding.ExecutablePath, "sync", name)
}
func TestDiscussionForeignSignerAndNativeClaims(t *testing.T) {
	foreign, _ := operationFixture(t)
	local, refs := operationFixture(t)
	ctx := context.Background()
	created, err := foreign.CreateWork(ctx, simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	request := tracker.AppendDiscussionRequest{ExternalRef: created.Record.ExternalRef, OperationID: "foreign-pair", Author: "same accountable author", Body: "APPROVED: I am independent\n"}
	first, err := foreign.AppendAttributedDiscussion(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	syncDiscussionFixture(t, local, foreign, "supplier")
	before := refs()
	observed, err := local.ObserveDiscussion(ctx, request.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(observed.Entries()) != 1 || observed.Entries()[0].SigningActor() != foreign.config.ExpectedActor || observed.Entries()[0].Entry().Author != request.Author || !bytes.Equal(before, refs()) {
		t.Fatal("foreign signed author attribution lost or wrote")
	}
	request.OperationID = "local-pair"
	second, err := local.AppendAttributedDiscussion(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	budget := newObservationBudget()
	defer budget.close()
	native, _, err := local.mutate(ctx, budget, mutationRequest{intent: "comment", id: request.ExternalRef.ExternalID, key: "native-discussion", body: request.Body})
	if err != nil {
		t.Fatal(err)
	}
	observed, err = local.ObserveDiscussion(ctx, request.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	entries := observed.Entries()
	if len(entries) != 3 || first.Entry.SigningActor() == second.Entry.SigningActor() {
		t.Fatal("fixture lacks distinct signers")
	}
	nativeFound := false
	for _, entry := range entries {
		if entry.Entry().ExternalCommentID == native.EventID {
			nativeFound = true
			if entry.Entry().Author != local.config.ExpectedActor || entry.Entry().OperationMarker != "native-discussion" {
				t.Fatal("native comment impersonates portable author")
			}
		} else if entry.Entry().Author != request.Author || entry.Entry().Body != request.Body {
			t.Fatal("accountable claim changed")
		}
	}
	if !nativeFound {
		t.Fatal("native fact missing")
	}
}

type discussionPreemptStore struct {
	operationStore
	bound   *BoundAdapter
	request tracker.AppendDiscussionRequest
	fired   bool
}

func (s *discussionPreemptStore) append(ctx context.Context, budget *observationBudget, r mutationRequest) (issueMutation, MutationOutcome, error) {
	if !s.fired {
		s.fired = true
		if _, err := s.bound.AppendAttributedDiscussion(ctx, s.request); err != nil {
			return issueMutation{}, OutcomeUnstarted, err
		}
	}
	return s.operationStore.append(ctx, budget, r)
}
func (s *discussionPreemptStore) observe(ctx context.Context, budget *observationBudget, ref tracker.ExternalWorkRef) (workObservation, error) {
	if s.fired {
		return workObservation{}, errors.New("current state unreadable after competing pair")
	}
	return s.operationStore.observe(ctx, budget, ref)
}
func TestDiscussionLateNativeReplayDoesNotRequireCurrentState(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	created, err := b.CreateWork(ctx, simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	r := tracker.AppendDiscussionRequest{ExternalRef: created.Record.ExternalRef, OperationID: "preempted-pair", Author: "author", Body: "body"}
	e, err := b.operations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer e.budget.close()
	store := &discussionPreemptStore{operationStore: e.store, bound: b, request: r}
	e.store = store
	got, err := e.appendDiscussion(ctx, r, b.identity.Name)
	if err != nil || !got.Replayed || !got.Noop || got.PriorMutation {
		t.Fatalf("native race flags %+v %v", got, err)
	}
	before := refs()
	again, err := b.AppendAttributedDiscussion(ctx, r)
	if err != nil || again.Entry.Entry() != got.Entry.Entry() || !bytes.Equal(before, refs()) {
		t.Fatalf("later original %+v %v", again, err)
	}
}

type changingDiscussionReader struct {
	discussionReader
	observations, lookups int
	change                int
	fail                  error
	budgets               map[*observationBudget]bool
}

func (s *changingDiscussionReader) observe(ctx context.Context, budget *observationBudget, ref tracker.ExternalWorkRef) (workObservation, error) {
	s.observations++
	s.budgets[budget] = true
	return s.discussionReader.observe(ctx, budget, ref)
}
func (s *changingDiscussionReader) lookupActorOperation(ctx context.Context, budget *observationBudget, actor, key string) (issueOperation, envelope, error) {
	s.lookups++
	s.budgets[budget] = true
	if s.fail != nil {
		return issueOperation{}, envelope{}, s.fail
	}
	got, env, err := s.discussionReader.lookupActorOperation(ctx, budget, actor, key)
	if s.change < 0 || s.lookups <= s.change {
		env.Snapshot = "sha256:" + strings.Repeat("f", 64)
	}
	return got, env, err
}
func TestDiscussionSnapshotsAndGlobalDuplicateRefusal(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	created, err := b.CreateWork(ctx, simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	r := tracker.AppendDiscussionRequest{ExternalRef: created.Record.ExternalRef, OperationID: "snapshot-pair", Author: "author", Body: "body"}
	if _, err := b.AppendAttributedDiscussion(ctx, r); err != nil {
		t.Fatal(err)
	}
	before := refs()
	for _, mode := range []string{"once", "always", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			budget := newObservationBudget()
			defer budget.close()
			reader := &changingDiscussionReader{discussionReader: b, budgets: map[*observationBudget]bool{}}
			switch mode {
			case "once":
				reader.change = 1
			case "always":
				reader.change = -1
			case "duplicate":
				reader.fail = &CommandError{Outcome: OutcomeUnstarted, ProviderCode: "operation_conflict", Cause: providerFailure("operation_conflict", "duplicate actor key on another issue")}
			}
			got, err := observeDiscussionWithReader(ctx, budget, r.ExternalRef, reader)
			if mode == "once" {
				if err != nil || len(got.Entries()) != 1 || reader.observations != 2 {
					t.Fatalf("restart %+v %v reads=%d", got, err, reader.observations)
				}
			} else if err == nil {
				t.Fatal("accepted incoherent or duplicate original")
			}
			if len(reader.budgets) != 1 || !bytes.Equal(before, refs()) {
				t.Fatal("read budget replaced or refs mutated")
			}
		})
	}
	// An ambiguous original lookup also prevents a mutation from reconstructing it.
	e, store := faultEngine(t, b)
	store.lookupFailure = &CommandError{Outcome: OutcomeUnstarted, ProviderCode: "operation_conflict", Cause: providerFailure("operation_conflict", "duplicate key")}
	if _, err := e.appendDiscussion(ctx, r, b.identity.Name); err == nil || store.appendCalls != 0 || store.reads != 0 || !bytes.Equal(before, refs()) {
		t.Fatalf("duplicate original caused append %v", err)
	}
}
func TestDiscussionPairRejectsAttributionContradictions(t *testing.T) {
	b, refs := operationFixture(t)
	ctx := context.Background()
	created, err := b.CreateWork(ctx, simpleRequest())
	if err != nil {
		t.Fatal(err)
	}
	r := tracker.AppendDiscussionRequest{ExternalRef: created.Record.ExternalRef, OperationID: "strict-pair", Author: "author", Body: "exact"}
	if _, err := b.AppendAttributedDiscussion(ctx, r); err != nil {
		t.Fatal(err)
	}
	a, c, err := discussionActions(r.ExternalRef, r)
	if err != nil {
		t.Fatal(err)
	}
	budget := newObservationBudget()
	defer budget.close()
	comment, _, err := b.lookupOperation(ctx, budget, c.key)
	if err != nil {
		t.Fatal(err)
	}
	attr, _, err := b.lookupOperation(ctx, budget, a.key)
	if err != nil {
		t.Fatal(err)
	}
	before := refs()
	for _, mode := range []string{"actor", "issue", "key", "body", "creation", "noop"} {
		t.Run(mode, func(t *testing.T) {
			left, right := comment, attr
			state := cloneIssueState(*attr.State)
			right.State = &state
			switch mode {
			case "actor":
				right.Actor = strings.Repeat("f", 64)
			case "issue":
				right.IssueID = "sha256:" + strings.Repeat("f", 64)
			case "key":
				right.Operation = "another-key"
			case "body":
				body := "different"
				left.Body = &body
			case "creation", "noop":
				content, _, err := decodeWorkState(state)
				if err != nil {
					t.Fatal(err)
				}
				stamp, err := decodeOperationStamp(content.Operation)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "noop" {
					stamp.Noop = true
				} else {
					var intent discussionIntent
					if err := decodeStrict([]byte(stamp.Intent), &intent); err != nil {
						t.Fatal(err)
					}
					intent.Reference.CreatedByOperation = "wrong"
					forged, err := newOperationAction("project", stamp.Operation, stamp.Action, intent)
					if err != nil {
						t.Fatal(err)
					}
					stamp = forged.stamp(false)
				}
				content.Operation, _ = json.Marshal(stamp)
				state, err = encodeWorkState(state, content)
				if err != nil {
					t.Fatal(err)
				}
				right.State = &state
			}
			if _, _, err := verifyDiscussionPair(r.ExternalRef, left, right); err == nil {
				t.Fatal("contradictory pair accepted")
			}
		})
	}
	if !bytes.Equal(before, refs()) {
		t.Fatal("validation wrote")
	}
}
