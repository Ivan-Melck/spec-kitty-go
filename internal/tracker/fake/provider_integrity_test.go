package fake

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestReviewCycle7RejectsUnauthenticCompensationHistoryAcrossReopen(t *testing.T) {
	for _, test := range []struct {
		name    string
		corrupt func(*diskState, tracker.CompensationRequest)
	}{
		{
			name: "missing compensation history",
			corrupt: func(state *diskState, _ tracker.CompensationRequest) {
				state.History = cycle7WithoutCompensation(state.History)
			},
		},
		{
			name: "altered compensation value",
			corrupt: func(state *diskState, _ tracker.CompensationRequest) {
				for index := range state.History {
					if state.History[index].Kind == "compensation" {
						state.History[index].Value = "forged-owner"
					}
				}
			},
		},
		{
			name: "altered assignment evidence",
			corrupt: func(state *diskState, _ tracker.CompensationRequest) {
				for index := range state.History {
					if state.History[index].Kind == "assignment" {
						state.History[index].Value = "forged-agent"
					}
				}
			},
		},
		{
			name: "duplicate conflicting compensation",
			corrupt: func(state *diskState, request tracker.CompensationRequest) {
				revision := state.Compensated[request.WorkKey+"\x00"+request.Marker]
				state.History = append(state.History, HistoryEntry{Kind: "compensation", WorkKey: request.WorkKey, Value: "forged-owner", Marker: request.Marker, Revision: revision})
			},
		},
		{
			name: "inconsistent compensated revision",
			corrupt: func(state *diskState, request tracker.CompensationRequest) {
				state.Compensated[request.WorkKey+"\x00"+request.Marker]++
			},
		},
		{
			name: "conflicting event at compensation revision",
			corrupt: func(state *diskState, request tracker.CompensationRequest) {
				revision := state.Compensated[request.WorkKey+"\x00"+request.Marker]
				state.History = append(state.History, HistoryEntry{Kind: "human_assignment", WorkKey: request.WorkKey, Value: "forged-owner", Revision: revision})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			path, store, request := cycle7CompensatedProvider(t, ctx)
			if err := store.HumanAssign(ctx, request.WorkKey, "human-owner"); err != nil {
				t.Fatal(err)
			}
			if err := store.update(ctx, func(state *diskState) error {
				test.corrupt(state, request)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if mutation, err := NewProvider(reopened).CompareBeforeCompensate(ctx, request); !tracker.IsKind(err, tracker.ErrorAttributionLost) {
				t.Fatalf("unauthentic replay = %+v, %v", mutation, err)
			}
			work, err := NewProvider(reopened).Read(ctx, request.WorkKey)
			if err != nil || work.Revision != 4 || work.Assignee != "human-owner" || work.AssignmentMarker != "" {
				t.Fatalf("rejected replay changed current provider state: %+v, %v", work, err)
			}
		})
	}
}

func TestReviewCycle7AcceptsExactAndIdenticalCompensationHistoryAcrossReopen(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		name := "exact history"
		if duplicate {
			name = "identical duplicate record"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path, store, request := cycle7CompensatedProvider(t, ctx)
			if err := store.HumanAssign(ctx, request.WorkKey, "human-owner"); err != nil {
				t.Fatal(err)
			}
			if duplicate {
				if err := store.update(ctx, func(state *diskState) error {
					for _, entry := range state.History {
						if entry.Kind == "compensation" {
							state.History = append(state.History, entry)
							break
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			mutation, err := NewProvider(reopened).CompareBeforeCompensate(ctx, request)
			if err != nil || !mutation.Replayed || !mutation.Noop || mutation.Revision != 3 || mutation.Assignee != "original" || mutation.Marker != "" {
				t.Fatalf("authentic replay = %+v, %v", mutation, err)
			}
		})
	}
}

func TestReviewCycle7RejectsReplayRequestThatDisagreesWithDurableHistory(t *testing.T) {
	ctx := context.Background()
	path, _, request := cycle7CompensatedProvider(t, ctx)
	request.RestoreAssignee = "never-existed"
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if mutation, err := NewProvider(reopened).CompareBeforeCompensate(ctx, request); !tracker.IsKind(err, tracker.ErrorAttributionLost) {
		t.Fatalf("altered replay request = %+v, %v", mutation, err)
	}
}

func cycle7CompensatedProvider(t *testing.T, ctx context.Context) (string, *Store, tracker.CompensationRequest) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tracker.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Seed(ctx, WorkSeed{Key: "TK-1", Title: "history authenticity", Assignee: "original"}); err != nil {
		t.Fatal(err)
	}
	provider := NewProvider(store)
	applied, err := provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: "TK-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-cycle7"})
	if err != nil {
		t.Fatal(err)
	}
	request := tracker.CompensationRequest{WorkKey: "TK-1", ExpectedRevision: applied.Revision, ExpectedAssignee: "agent-a", Marker: "operation-cycle7", RestoreAssignee: "original"}
	if mutation, err := provider.CompareBeforeCompensate(ctx, request); err != nil || mutation.Revision != 3 {
		t.Fatalf("initial compensation = %+v, %v", mutation, err)
	}
	return path, store, request
}

func cycle7WithoutCompensation(history []HistoryEntry) []HistoryEntry {
	filtered := history[:0]
	for _, entry := range history {
		if entry.Kind != "compensation" {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
