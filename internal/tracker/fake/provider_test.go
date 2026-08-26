package fake_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

func TestProviderPersistsWorkAndCASMutations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tracker.json")
	store, err := fake.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Seed(ctx, fake.WorkSeed{
		Key: "TK-1", Title: "Keep the tracker stupid", Description: "external coordination only",
		Criteria: []string{"assignment is informational"}, Dependencies: []string{"TK-0"},
	}); err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(store)
	work, err := provider.Read(ctx, "TK-1")
	if err != nil || work.Revision != 1 || work.Title == "" || len(work.Criteria) != 1 || len(work.Dependencies) != 1 {
		t.Fatalf("seeded work = %+v, %v", work, err)
	}

	assignment, err := provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{
		WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-assign",
	})
	if err != nil || assignment.Revision != 2 || assignment.Assignee != "agent-a" || assignment.Marker != "operation-assign" {
		t.Fatalf("assignment = %+v, %v", assignment, err)
	}
	replayed, err := provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{
		WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-assign",
	})
	if err != nil || !replayed.Replayed || replayed.Revision != assignment.Revision {
		t.Fatalf("same-operation replay = %+v, %v", replayed, err)
	}
	comment, err := provider.CompareAndAddComment(ctx, tracker.CommentRequest{
		WorkKey: "TK-1", ExpectedRevision: 2, Author: "agent-a", Body: "observed", Marker: "operation-comment",
	})
	if err != nil || comment.Revision != 3 || comment.Marker != "operation-comment" {
		t.Fatalf("comment = %+v, %v", comment, err)
	}
	replayedComment, err := provider.CompareAndAddComment(ctx, tracker.CommentRequest{
		WorkKey: "TK-1", ExpectedRevision: 2, Author: "agent-a", Body: "observed", Marker: "operation-comment",
	})
	if err != nil || !replayedComment.Replayed || replayedComment.Marker != "operation-comment" || replayedComment.Revision != 3 {
		t.Fatalf("comment replay = %+v, %v", replayedComment, err)
	}

	store, err = fake.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	provider = fake.NewProvider(store)
	work, err = provider.Read(ctx, "TK-1")
	if err != nil || work.Revision != 3 || work.Assignee != "agent-a" || work.AssignmentMarker != "operation-assign" || len(work.Comments) != 1 || work.Comments[0].Author != "agent-a" {
		t.Fatalf("reopened work = %+v, %v", work, err)
	}
}

func TestProviderFaultsExposeAmbiguousReality(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		failure     fake.FailureMode
		wantKind    tracker.ErrorKind
		wantApplied bool
	}{
		{name: "unavailable", failure: fake.FailureUnavailable, wantKind: tracker.ErrorUnavailable},
		{name: "timeout before apply", failure: fake.FailureTimeoutBeforeApply, wantKind: tracker.ErrorAmbiguous},
		{name: "timeout after apply", failure: fake.FailureTimeoutAfterApply, wantKind: tracker.ErrorAmbiguous, wantApplied: true},
		{name: "explicit revision conflict", failure: fake.FailureRevisionConflict, wantKind: tracker.ErrorRevisionConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "fault target"}); err != nil {
				t.Fatal(err)
			}
			if err := store.Inject(ctx, test.failure); err != nil {
				t.Fatal(err)
			}
			provider := fake.NewProvider(store)
			_, err = provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-fault"})
			var providerError *tracker.Error
			if !errors.As(err, &providerError) || providerError.Kind != test.wantKind {
				t.Fatalf("error = %T %v, want %s", err, err, test.wantKind)
			}
			work, err := provider.Read(ctx, "TK-1")
			if err != nil {
				t.Fatal(err)
			}
			if got := work.Assignee == "agent-a"; got != test.wantApplied {
				t.Fatalf("applied = %v, want %v; work=%+v", got, test.wantApplied, work)
			}
			if !test.wantApplied {
				mutation, retryErr := provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-fault"})
				if retryErr != nil || mutation.Revision != 2 {
					t.Fatalf("one-shot failure was not consumed: mutation=%+v error=%v", mutation, retryErr)
				}
			}
		})
	}
}

func TestProviderCompareBeforeCompensatePreservesHumanAssignment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "compensation target", Assignee: "original"}); err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(store)
	applied, err := provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.HumanAssign(ctx, "TK-1", "human-owner"); err != nil {
		t.Fatal(err)
	}
	_, err = provider.CompareBeforeCompensate(ctx, tracker.CompensationRequest{
		WorkKey: "TK-1", ExpectedRevision: applied.Revision, ExpectedAssignee: "agent-a", Marker: "operation-a", RestoreAssignee: "original",
	})
	var providerError *tracker.Error
	if !errors.As(err, &providerError) || providerError.Kind != tracker.ErrorAttributionLost {
		t.Fatalf("compensation error = %T %v", err, err)
	}
	work, err := provider.Read(ctx, "TK-1")
	if err != nil || work.Assignee != "human-owner" {
		t.Fatalf("human assignment was not preserved: %+v, %v", work, err)
	}
}

func TestProviderSuccessfulCompensationIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "compensation target", Assignee: "original"}); err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(store)
	applied, err := provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-a"})
	if err != nil {
		t.Fatal(err)
	}
	request := tracker.CompensationRequest{WorkKey: "TK-1", ExpectedRevision: applied.Revision, ExpectedAssignee: "agent-a", Marker: "operation-a", RestoreAssignee: "original"}
	first, err := provider.CompareBeforeCompensate(ctx, request)
	if err != nil || first.Noop || first.Assignee != "original" {
		t.Fatalf("first compensation = %+v, %v", first, err)
	}
	second, err := provider.CompareBeforeCompensate(ctx, request)
	if err != nil || !second.Noop || second.Revision != first.Revision {
		t.Fatalf("repeated compensation = %+v, %v", second, err)
	}
}

func TestCycle5CompensationReplayReturnsOneHistoricalProviderState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "replay truth", Assignee: "original"}); err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(store)
	applied, err := provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-replay-truth"})
	if err != nil {
		t.Fatal(err)
	}
	request := tracker.CompensationRequest{WorkKey: "TK-1", ExpectedRevision: applied.Revision, ExpectedAssignee: "agent-a", Marker: "operation-replay-truth", RestoreAssignee: "original"}
	compensated, err := provider.CompareBeforeCompensate(ctx, request)
	if err != nil || compensated.Revision != 3 || compensated.Assignee != "original" || compensated.Marker != "" {
		t.Fatalf("initial compensation = %+v, %v", compensated, err)
	}
	if err := store.HumanAssign(ctx, "TK-1", "human-owner"); err != nil {
		t.Fatal(err)
	}
	replayed, err := provider.CompareBeforeCompensate(ctx, request)
	if err != nil || !replayed.Replayed || !replayed.Noop {
		t.Fatalf("compensation replay = %+v, %v", replayed, err)
	}
	if replayed.Revision != compensated.Revision || replayed.Assignee != compensated.Assignee || replayed.Marker != compensated.Marker {
		t.Fatalf("replay spliced historical and current provider states: first=%+v replay=%+v", compensated, replayed)
	}
	work, err := provider.Read(ctx, "TK-1")
	if err != nil || work.Revision != 4 || work.Assignee != "human-owner" || work.AssignmentMarker != "" {
		t.Fatalf("human assignment was not preserved: %+v, %v", work, err)
	}
}

func TestProviderRejectsOperationMarkerReuseForDifferentMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Seed(ctx, fake.WorkSeed{Key: "TK-1", Title: "marker target"}); err != nil {
		t.Fatal(err)
	}
	provider := fake.NewProvider(store)
	if _, err := provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-a"}); err != nil {
		t.Fatal(err)
	}
	_, err = provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: "TK-1", ExpectedRevision: 2, Assignee: "agent-b", Marker: "operation-a"})
	var providerError *tracker.Error
	if !errors.As(err, &providerError) || providerError.Kind != tracker.ErrorRevisionConflict {
		t.Fatalf("marker reuse error = %T %v", err, err)
	}
	work, err := provider.Read(ctx, "TK-1")
	if err != nil || work.Assignee != "agent-a" || work.Revision != 2 {
		t.Fatalf("marker reuse changed work: %+v, %v", work, err)
	}
}
