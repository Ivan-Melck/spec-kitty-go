package conformance

import (
	"context"
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func graphCases() []Case {
	supported := func(c tracker.ProviderCapabilities) bool { return c.BlockingGraph == tracker.SupportSupported }
	return []Case{
		{ID: "graph.blocking.direction", Supported: supported, Exercise: exerciseGraphDirection},
		{ID: "graph.ready.diagnostic", Supported: supported, Exercise: exerciseGraphReady},
	}
}

func exerciseGraphDirection(ctx context.Context, fixture Fixture, supported bool) error {
	if fixture.Graph == nil {
		return fmt.Errorf("graph port is absent")
	}
	edge := tracker.DependencyRef{BlockedWorkID: fixture.Reference.WorkID, BlockingWorkID: ids.WorkID("work-conformance-blocker"), Kind: tracker.DependencyBlocks}
	request := tracker.DependencyRequest{ProjectID: fixture.Reference.ProjectID, OperationID: "operation-conformance-edge", Dependency: edge}
	put, err := fixture.Graph.PutBlockingDependency(ctx, request)
	if !supported {
		return expectKind(err, tracker.ErrorCapabilityUnsupported)
	}
	if err != nil {
		return err
	}
	listed, err := fixture.Graph.ListDependencies(ctx, fixture.Reference)
	if err != nil {
		return err
	}
	if put.Dependency.BlockedWorkID != edge.BlockedWorkID || put.Dependency.BlockingWorkID != edge.BlockingWorkID || len(listed) != 1 || listed[0].BlockedWorkID != edge.BlockedWorkID || listed[0].BlockingWorkID != edge.BlockingWorkID {
		return fmt.Errorf("blocking direction changed")
	}
	removed, err := fixture.Graph.RemoveBlockingDependency(ctx, request)
	if err != nil || removed.Dependency != put.Dependency {
		return fmt.Errorf("dependency removal: %v", err)
	}
	return nil
}

func exerciseGraphReady(ctx context.Context, fixture Fixture, supported bool) error {
	projection, err := fixture.Graph.ReadyProjection(ctx, tracker.ReadyRequest{ProjectID: fixture.Reference.ProjectID, PinnedReadyIDs: []ids.WorkID{fixture.Reference.WorkID}})
	if !supported {
		return expectKind(err, tracker.ErrorCapabilityUnsupported)
	}
	if err != nil {
		return err
	}
	if projection.Observation.Revision == 0 {
		return fmt.Errorf("readiness observation is absent")
	}
	return nil
}
