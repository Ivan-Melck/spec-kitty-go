package beads

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const realV122CommentList = `[{"id":"comment-1","issue_id":"bd-discussion","author":"human","text":"human discussion","created_at":"2026-08-26T12:00:02Z"}]`

func TestRealV122CommentPayloadShape(t *testing.T) {
	var comments []json.RawMessage
	if err := json.Unmarshal([]byte(realV122CommentList), &comments); err != nil || len(comments) != 1 {
		t.Fatalf("comment fixture records=%d error=%v", len(comments), err)
	}
	entry, observation, err := decodeProviderComment(comments[0])
	if err != nil || entry.ExternalCommentID != "comment-1" || entry.Author != "human" || entry.Body != "human discussion" || observation.Body != "human discussion" {
		t.Fatalf("entry=%#v observation=%#v error=%v", entry, observation, err)
	}
	var skew map[string]json.RawMessage
	if err := json.Unmarshal(comments[0], &skew); err != nil {
		t.Fatal(err)
	}
	skew["future_optional"] = json.RawMessage(`{"ignored":true}`)
	skewPayload, err := json.Marshal(skew)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeProviderComment(skewPayload); err != nil {
		t.Fatalf("comment optional-field skew rejected: %v", err)
	}
	for name, payload := range map[string]string{
		"legacy body":       `{"id":"comment-1","author":"human","body":"human discussion"}`,
		"missing text":      `{"id":"comment-1","author":"human"}`,
		"non-string text":   `{"id":"comment-1","author":"human","text":7}`,
		"non-object record": `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeProviderComment([]byte(payload)); !tracker.IsKind(err, tracker.ErrorMalformedProtocol) {
				t.Fatalf("payload=%s error=%v", payload, err)
			}
		})
	}
}

func TestDiscussionMarkerRoundTripAndForgeryBoundaries(t *testing.T) {
	operationID := ids.OperationID("operation.comment:v1")
	validLine := strings.TrimSuffix(renderDiscussionBody(operationID, ""), "\n")
	tests := []struct {
		name, providerBody, wantBody, wantMarker string
	}{
		{name: "multiline unicode", providerBody: renderDiscussionBody(operationID, "first\n猫\nlast"), wantBody: "first\n猫\nlast", wantMarker: operationID.String()},
		{name: "empty semantic body", providerBody: renderDiscussionBody(operationID, ""), wantBody: "", wantMarker: operationID.String()},
		{name: "body marker cannot forge envelope", providerBody: renderDiscussionBody(operationID, validLine+"\nsecond"), wantBody: validLine + "\nsecond", wantMarker: operationID.String()},
		{name: "ordinary human", providerBody: "human prose", wantBody: "human prose"},
		{name: "human lookalike missing newline", providerBody: validLine, wantBody: validLine},
		{name: "human lookalike invalid token", providerBody: discussionMarkerPrefix + "%%%" + discussionMarkerSuffix + "body", wantBody: discussionMarkerPrefix + "%%%" + discussionMarkerSuffix + "body"},
		{name: "human lookalike noncanonical padding", providerBody: discussionMarkerPrefix + "b3BlcmF0aW9uLmNvbW1lbnQ6djE=" + discussionMarkerSuffix + "body", wantBody: discussionMarkerPrefix + "b3BlcmF0aW9uLmNvbW1lbnQ6djE=" + discussionMarkerSuffix + "body"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, marker, marked := parseDiscussionBody(test.providerBody)
			if body != test.wantBody || marker != test.wantMarker || marked != (test.wantMarker != "") {
				t.Fatalf("body=%q marker=%q marked=%t", body, marker, marked)
			}
		})
	}
}

func discussionFixture(t *testing.T) (*BoundAdapter, *wp06Runner, tracker.ExternalWorkRef, tracker.ProviderObservation) {
	t.Helper()
	bound, runner := newWP06Adapter(t)
	reference := addWP06Issue(t, runner, "project-1", "work-discussion", "bd-discussion")
	_, observation, err := bound.observeDiscussion(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	return bound, runner, reference, observation
}

func appendRequest(reference tracker.ExternalWorkRef, observation tracker.Revision, operation, body string) tracker.AppendDiscussionRequest {
	return tracker.AppendDiscussionRequest{
		ExternalRef: reference, OperationID: ids.OperationID(operation), Author: "agent-a", Body: body, ExpectedObservation: observation,
	}
}

func TestDiscussionListPreservesHumanAndAdapterOrder(t *testing.T) {
	bound, runner := newWP06Adapter(t)
	reference := addWP06Issue(t, runner, "project-1", "work-discussion", "bd-discussion")
	runner.mu.Lock()
	runner.issues[reference.ExternalID] = func(issue wp06Issue) wp06Issue {
		issue.Comments = []wp06Comment{
			{ID: "human-before", Author: "human", Body: "plain status=approved prose", CreatedAt: time.Unix(1_800_000_100, 0).UTC().Format(time.RFC3339Nano)},
			{ID: "adapter", Author: "agent-a", Body: renderDiscussionBody("operation-comment-1", "durable\n猫"), CreatedAt: time.Unix(1_800_000_101, 0).UTC().Format(time.RFC3339Nano)},
			{ID: "human-after", Author: "human", Body: discussionMarkerPrefix + "not-base64" + discussionMarkerSuffix + "lookalike", CreatedAt: time.Unix(1_800_000_102, 0).UTC().Format(time.RFC3339Nano)},
		}
		return issue
	}(runner.issues[reference.ExternalID])
	runner.mu.Unlock()
	entries, err := bound.ListDiscussion(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{entries[0].ExternalCommentID, entries[1].ExternalCommentID, entries[2].ExternalCommentID}; !reflect.DeepEqual(got, []string{"human-before", "adapter", "human-after"}) {
		t.Fatalf("order=%v", got)
	}
	if entries[0].OperationMarker != "" || entries[0].Body != "plain status=approved prose" || entries[1].OperationMarker != "operation-comment-1" || entries[1].Body != "durable\n猫" || entries[2].OperationMarker != "" {
		t.Fatalf("entries=%#v", entries)
	}
	if entries[0].Observation == 0 || entries[0].Observation != entries[2].Observation {
		t.Fatalf("observation revisions=%d/%d", entries[0].Observation, entries[2].Observation)
	}
}

func TestDiscussionAppendExactReplayAndMarkerConflict(t *testing.T) {
	bound, runner, reference, observation := discussionFixture(t)
	request := appendRequest(reference, observation.Revision, "operation-comment-1", "multiline\n猫")
	mutation, err := bound.AppendDiscussion(context.Background(), request)
	if err != nil || mutation.Replayed || mutation.Entry.Body != request.Body || mutation.Entry.Author != request.Author || mutation.Entry.OperationMarker != request.OperationID.String() {
		t.Fatalf("mutation=%#v error=%v", mutation, err)
	}
	replayed, err := bound.AppendDiscussion(context.Background(), request)
	if err != nil || !replayed.Replayed || !replayed.Noop || replayed.Entry.ExternalCommentID != mutation.Entry.ExternalCommentID {
		t.Fatalf("replay=%#v error=%v", replayed, err)
	}
	request.Body = "changed secret semantic body"
	if _, err := bound.AppendDiscussion(context.Background(), request); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || strings.Contains(err.Error(), request.Body) {
		t.Fatalf("changed replay error=%v", err)
	}
	request.Body = mutation.Entry.Body
	request.Author = "another-author"
	if _, err := bound.AppendDiscussion(context.Background(), request); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("author replay error=%v", err)
	}
	calls, _ := runner.callSnapshot()
	if countKind(calls, CommandCommentAppend) != 1 {
		t.Fatalf("duplicate append calls=%v", calls)
	}
}

func TestDiscussionEmptyUnicodeAndMarkerLikeBodiesRoundTrip(t *testing.T) {
	for index, body := range []string{"", "猫\nline two", renderDiscussionBody("operation-lookalike", "nested") + "\nend"} {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			bound, _, reference, observation := discussionFixture(t)
			request := appendRequest(reference, observation.Revision, "operation-body-boundary", body)
			mutation, err := bound.AppendDiscussion(context.Background(), request)
			if err != nil || mutation.Entry.Body != body {
				t.Fatalf("body=%q mutation=%#v error=%v", body, mutation, err)
			}
		})
	}
}

func TestDiscussionAmbiguousAppendObservesWithoutBlindRetry(t *testing.T) {
	for _, test := range []struct {
		name, body  string
		apply       bool
		unavailable bool
		wantSuccess bool
	}{
		{name: "timeout after apply", body: "applied", apply: true, wantSuccess: true},
		{name: "timeout before apply", body: "absent", apply: false},
		{name: "observation unavailable", body: "secret-body-sentinel", apply: true, unavailable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			bound, runner, reference, observation := discussionFixture(t)
			runner.ambiguousKind, runner.applyBeforeAmbiguous = CommandCommentAppend, test.apply
			runner.failCommentListAfterWrite = test.unavailable
			mutation, err := bound.AppendDiscussion(context.Background(), appendRequest(reference, observation.Revision, "operation-ambiguous", test.body))
			if test.wantSuccess && (err != nil || !mutation.Replayed) {
				t.Fatalf("mutation=%#v error=%v", mutation, err)
			}
			if !test.wantSuccess && !tracker.IsKind(err, tracker.ErrorAmbiguous) {
				t.Fatalf("error=%v", err)
			}
			if err != nil && strings.Contains(err.Error(), test.body) {
				t.Fatalf("discussion body leaked in error: %v", err)
			}
			calls, _ := runner.callSnapshot()
			if countKind(calls, CommandCommentAppend) != 1 {
				t.Fatalf("blind append retry calls=%v", calls)
			}
		})
	}
}

func TestDiscussionDuplicateMarkerAndRevisionDriftRefuseMutation(t *testing.T) {
	bound, runner, reference, observation := discussionFixture(t)
	request := appendRequest(reference, observation.Revision, "operation-duplicate", "body")
	rendered := renderDiscussionBody(request.OperationID, request.Body)
	runner.mu.Lock()
	issue := runner.issues[reference.ExternalID]
	issue.Comments = []wp06Comment{
		{ID: "one", Author: request.Author, Body: rendered, CreatedAt: time.Unix(1_800_000_200, 0).UTC().Format(time.RFC3339Nano)},
		{ID: "two", Author: request.Author, Body: rendered, CreatedAt: time.Unix(1_800_000_201, 0).UTC().Format(time.RFC3339Nano)},
	}
	runner.issues[reference.ExternalID] = issue
	runner.mu.Unlock()
	if _, err := bound.AppendDiscussion(context.Background(), request); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("duplicate marker error=%v", err)
	}

	bound, runner, reference, observation = discussionFixture(t)
	runner.mu.Lock()
	issue = runner.issues[reference.ExternalID]
	issue.Comments = append(issue.Comments, wp06Comment{ID: "human", Author: "human", Body: "changed", CreatedAt: time.Unix(1_800_000_202, 0).UTC().Format(time.RFC3339Nano)})
	runner.issues[reference.ExternalID] = issue
	runner.mu.Unlock()
	if _, err := bound.AppendDiscussion(context.Background(), appendRequest(reference, observation.Revision, "operation-stale", "body")); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("stale observation error=%v", err)
	}
	calls, _ := runner.callSnapshot()
	if countKind(calls, CommandCommentAppend) != 0 {
		t.Fatalf("revision drift started mutation calls=%v", calls)
	}
}

func TestDiscussionOperationCannotReplayAcrossWorkMappings(t *testing.T) {
	bound, runner, reference, observation := discussionFixture(t)
	other := addWP06Issue(t, runner, "project-1", "work-other", "bd-other")
	runner.mu.Lock()
	issue := runner.issues[other.ExternalID]
	issue.Comments = []wp06Comment{{
		ID: "other-comment", Author: "agent-a", Body: renderDiscussionBody("operation-cross-work", "body"),
		CreatedAt: time.Unix(1_800_000_300, 0).UTC().Format(time.RFC3339Nano),
	}}
	runner.issues[other.ExternalID] = issue
	runner.mu.Unlock()
	_, err := bound.AppendDiscussion(context.Background(), appendRequest(reference, observation.Revision, "operation-cross-work", "body"))
	if !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("cross-work marker error=%v", err)
	}
	calls, _ := runner.callSnapshot()
	if countKind(calls, CommandCommentAppend) != 0 {
		t.Fatalf("cross-work marker started append calls=%v", calls)
	}
}

func TestDiscussionMalformedProviderDataAndInvalidInputsAreRedacted(t *testing.T) {
	secret := "secret-comment-body-sentinel"
	bound, runner, reference, observation := discussionFixture(t)
	tests := []tracker.AppendDiscussionRequest{
		appendRequest(reference, 0, "operation-invalid", secret),
		appendRequest(reference, observation.Revision, "bad operation", secret),
		func() tracker.AppendDiscussionRequest {
			request := appendRequest(reference, observation.Revision, "operation-invalid", secret)
			request.Author = ""
			return request
		}(),
		func() tracker.AppendDiscussionRequest {
			request := appendRequest(reference, observation.Revision, "operation-invalid", secret+"\x00")
			return request
		}(),
	}
	for _, request := range tests {
		if _, err := bound.AppendDiscussion(context.Background(), request); !tracker.IsKind(err, tracker.ErrorInvalidRequest) || strings.Contains(err.Error(), secret) {
			t.Fatalf("invalid request error=%v", err)
		}
	}
	runner.malformedKind = CommandCommentList
	if _, err := bound.ListDiscussion(context.Background(), reference); !tracker.IsKind(err, tracker.ErrorMalformedProtocol) || strings.Contains(err.Error(), secret) {
		t.Fatalf("malformed error=%v", err)
	}
	calls, _ := runner.callSnapshot()
	if countKind(calls, CommandCommentAppend) != 0 {
		t.Fatalf("invalid input started append calls=%v", calls)
	}
}
