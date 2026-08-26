package conformance

import (
	"context"
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func discussionCases() []Case {
	return []Case{{ID: "discussion.append.order.replay", Supported: func(c tracker.ProviderCapabilities) bool { return c.Discussion != tracker.DiscussionUnsupported }, Exercise: exerciseDiscussion}}
}

func exerciseDiscussion(ctx context.Context, fixture Fixture, supported bool) error {
	if fixture.Discussion == nil {
		return fmt.Errorf("discussion port is absent")
	}
	record, _ := fixture.Catalog.ReadWork(ctx, fixture.Reference)
	request := tracker.AppendDiscussionRequest{ExternalRef: fixture.Reference, OperationID: "operation-conformance-discussion", Author: "actor-conformance", Body: "portable body", ExpectedObservation: record.Observation.Revision}
	first, err := fixture.Discussion.AppendDiscussion(ctx, request)
	if !supported {
		return expectKind(err, tracker.ErrorCapabilityUnsupported)
	}
	if err != nil {
		return err
	}
	second, err := fixture.Discussion.AppendDiscussion(ctx, request)
	if err != nil {
		return err
	}
	entries, err := fixture.Discussion.ListDiscussion(ctx, fixture.Reference)
	if err != nil {
		return err
	}
	if first.Entry.OperationMarker != request.OperationID.String() || first.Entry.Author != request.Author || first.Entry.Body != request.Body || !second.Replayed || len(entries) == 0 || entries[len(entries)-1].OperationMarker != request.OperationID.String() {
		return fmt.Errorf("discussion ordering, marker, or replay mismatch")
	}
	changed := request
	changed.Body = "changed"
	changed.ExpectedObservation = first.Observation.Revision
	if _, err := fixture.Discussion.AppendDiscussion(ctx, changed); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		return fmt.Errorf("marker ambiguity was not refused: %v", err)
	}
	return nil
}
