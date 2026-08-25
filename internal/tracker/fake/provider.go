package fake

import (
	"context"
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type Provider struct{ store *Store }

func NewProvider(store *Store) *Provider { return &Provider{store: store} }

var _ tracker.Provider = (*Provider)(nil)

func (p *Provider) Read(ctx context.Context, workKey string) (tracker.Work, error) {
	var result tracker.Work
	err := p.store.read(ctx, func(state diskState) error {
		work, ok := state.Works[workKey]
		if !ok {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work does not exist"}
		}
		result = publicWork(work)
		return nil
	})
	return result, err
}

func (p *Provider) CompareAndSetAssignment(ctx context.Context, request tracker.AssignmentRequest) (tracker.Mutation, error) {
	if request.WorkKey == "" || request.ExpectedRevision == 0 || request.Marker == "" {
		return tracker.Mutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "work key, expected revision, and marker are required"}
	}
	var result tracker.Mutation
	var outcome error
	err := p.store.update(ctx, func(state *diskState) error {
		work, ok := state.Works[request.WorkKey]
		if !ok {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work does not exist"}
		}
		if work.AssignmentMarker == request.Marker && work.Assignee == request.Assignee {
			result = mutation(work, true, false)
			return nil
		}
		if markerAlreadyUsed(state.History, request.WorkKey, request.Marker) {
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "operation marker was reused for a different mutation"}
		}
		failure := consumeFailure(state)
		if failure != "" && failure != FailureTimeoutAfterApply {
			outcome = failureError(failure)
			return nil
		}
		if work.Revision != request.ExpectedRevision {
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: fmt.Sprintf("expected revision %d, observed %d", request.ExpectedRevision, work.Revision)}
		}
		work.Revision++
		work.Assignee = request.Assignee
		work.AssignmentMarker = request.Marker
		state.History = append(state.History, HistoryEntry{Kind: "assignment", WorkKey: work.Key, Value: request.Assignee, Marker: request.Marker, Revision: work.Revision})
		result = mutation(work, false, false)
		if failure == FailureTimeoutAfterApply {
			outcome = failureError(failure)
		}
		return nil
	})
	if err != nil {
		return tracker.Mutation{}, err
	}
	if outcome != nil && result.Revision == 0 {
		return tracker.Mutation{}, outcome
	}
	return result, outcome
}

func (p *Provider) CompareAndAddComment(ctx context.Context, request tracker.CommentRequest) (tracker.Mutation, error) {
	if request.WorkKey == "" || request.ExpectedRevision == 0 || request.Marker == "" || request.Body == "" {
		return tracker.Mutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "work key, expected revision, body, and marker are required"}
	}
	var result tracker.Mutation
	var outcome error
	err := p.store.update(ctx, func(state *diskState) error {
		work, ok := state.Works[request.WorkKey]
		if !ok {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work does not exist"}
		}
		for _, comment := range work.Comments {
			if comment.Marker == request.Marker && comment.Body == request.Body {
				result = mutation(work, true, false)
				result.Marker = request.Marker
				return nil
			}
		}
		if markerAlreadyUsed(state.History, request.WorkKey, request.Marker) {
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "operation marker was reused for a different mutation"}
		}
		failure := consumeFailure(state)
		if failure != "" && failure != FailureTimeoutAfterApply {
			outcome = failureError(failure)
			return nil
		}
		if work.Revision != request.ExpectedRevision {
			return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: fmt.Sprintf("expected revision %d, observed %d", request.ExpectedRevision, work.Revision)}
		}
		work.Revision++
		work.Comments = append(work.Comments, tracker.Comment{Body: request.Body, Marker: request.Marker, Revision: work.Revision})
		state.History = append(state.History, HistoryEntry{Kind: "comment", WorkKey: work.Key, Value: request.Body, Marker: request.Marker, Revision: work.Revision})
		result = mutation(work, false, false)
		result.Marker = request.Marker
		if failure == FailureTimeoutAfterApply {
			outcome = failureError(failure)
		}
		return nil
	})
	if err != nil {
		return tracker.Mutation{}, err
	}
	if outcome != nil && result.Revision == 0 {
		return tracker.Mutation{}, outcome
	}
	return result, outcome
}

func (p *Provider) CompareBeforeCompensate(ctx context.Context, request tracker.CompensationRequest) (tracker.Mutation, error) {
	if request.WorkKey == "" || request.ExpectedRevision == 0 || request.Marker == "" {
		return tracker.Mutation{}, &tracker.Error{Kind: tracker.ErrorInvalidRequest, Message: "work key, expected revision, and marker are required"}
	}
	var result tracker.Mutation
	var outcome error
	err := p.store.update(ctx, func(state *diskState) error {
		work, ok := state.Works[request.WorkKey]
		if !ok {
			return &tracker.Error{Kind: tracker.ErrorNotFound, Message: "work does not exist"}
		}
		compensationKey := request.WorkKey + "\x00" + request.Marker
		if revision, compensated := state.Compensated[compensationKey]; compensated {
			result, ok = historicalCompensation(state.History, request, revision)
			if !ok {
				return &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "compensation record has no coherent provider history"}
			}
			return nil
		}
		failure := consumeFailure(state)
		if failure != "" && failure != FailureTimeoutAfterApply {
			outcome = failureError(failure)
			return nil
		}
		if work.Revision != request.ExpectedRevision || work.Assignee != request.ExpectedAssignee || work.AssignmentMarker != request.Marker {
			return &tracker.Error{Kind: tracker.ErrorAttributionLost, Message: "current revision, assignee, or operation marker no longer proves attribution"}
		}
		work.Revision++
		work.Assignee = request.RestoreAssignee
		work.AssignmentMarker = ""
		state.Compensated[compensationKey] = work.Revision
		state.History = append(state.History, HistoryEntry{Kind: "compensation", WorkKey: work.Key, Value: request.RestoreAssignee, Marker: request.Marker, Revision: work.Revision})
		result = mutation(work, false, false)
		if failure == FailureTimeoutAfterApply {
			outcome = failureError(failure)
		}
		return nil
	})
	if err != nil {
		return tracker.Mutation{}, err
	}
	if outcome != nil && result.Revision == 0 {
		return tracker.Mutation{}, outcome
	}
	return result, outcome
}

func publicWork(work *diskWork) tracker.Work {
	return tracker.Work{Key: work.Key, Title: work.Title, Description: work.Description, Criteria: clone(work.Criteria), Dependencies: clone(work.Dependencies), Assignee: work.Assignee, AssignmentMarker: work.AssignmentMarker, Comments: append([]tracker.Comment(nil), work.Comments...), Revision: work.Revision}
}

func mutation(work *diskWork, replayed, noop bool) tracker.Mutation {
	return tracker.Mutation{WorkKey: work.Key, Assignee: work.Assignee, Marker: work.AssignmentMarker, Revision: work.Revision, Replayed: replayed, Noop: noop}
}

func historicalCompensation(history []HistoryEntry, request tracker.CompensationRequest, revision tracker.Revision) (tracker.Mutation, bool) {
	if revision <= request.ExpectedRevision || revision-request.ExpectedRevision != 1 {
		return tracker.Mutation{}, false
	}
	assignmentFound, compensationFound := false, false
	for _, entry := range history {
		if entry.WorkKey != request.WorkKey {
			continue
		}
		if entry.Revision == request.ExpectedRevision {
			if entry.Kind != "assignment" || entry.Marker != request.Marker || entry.Value != request.ExpectedAssignee {
				return tracker.Mutation{}, false
			}
			assignmentFound = true
		}
		if entry.Revision == revision {
			if entry.Kind != "compensation" || entry.Marker != request.Marker || entry.Value != request.RestoreAssignee {
				return tracker.Mutation{}, false
			}
			compensationFound = true
		}
		if entry.Marker == request.Marker {
			switch entry.Kind {
			case "assignment":
				if entry.Revision != request.ExpectedRevision || entry.Value != request.ExpectedAssignee {
					return tracker.Mutation{}, false
				}
			case "compensation":
				if entry.Revision != revision || entry.Value != request.RestoreAssignee {
					return tracker.Mutation{}, false
				}
			default:
				return tracker.Mutation{}, false
			}
		}
	}
	if !assignmentFound || !compensationFound {
		return tracker.Mutation{}, false
	}
	return tracker.Mutation{WorkKey: request.WorkKey, Assignee: request.RestoreAssignee, Revision: revision, Replayed: true, Noop: true}, true
}

func consumeFailure(state *diskState) FailureMode {
	if len(state.Failures) == 0 {
		return ""
	}
	failure := state.Failures[0]
	state.Failures = state.Failures[1:]
	return failure
}

func failureError(failure FailureMode) error {
	switch failure {
	case FailureUnavailable:
		return &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "provider is unavailable"}
	case FailureTimeoutBeforeApply:
		return &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "request timed out before apply; caller must observe state"}
	case FailureTimeoutAfterApply:
		return &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "request timed out after apply; caller must observe state"}
	case FailureRevisionConflict:
		return &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "injected provider revision conflict"}
	default:
		return nil
	}
}

func markerAlreadyUsed(history []HistoryEntry, workKey, marker string) bool {
	for _, entry := range history {
		if entry.WorkKey == workKey && entry.Marker == marker {
			return true
		}
	}
	return false
}
