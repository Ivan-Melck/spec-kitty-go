package conformance

import (
	"context"
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func assignmentCases() []Case {
	return []Case{
		{ID: "assignment.project.observe.replay", Supported: func(c tracker.ProviderCapabilities) bool { return c.Assignment != tracker.AssignmentUnsupported }, Exercise: exerciseAssignment},
		{ID: "assignment.compensation.attribution", Supported: func(c tracker.ProviderCapabilities) bool {
			return c.SafeCompensation == tracker.CompensationConditional
		}, Exercise: exerciseCompensation},
	}
}

func exerciseAssignment(ctx context.Context, fixture Fixture, supported bool) error {
	if fixture.Assignment == nil {
		return fmt.Errorf("assignment port is absent")
	}
	before, observeErr := fixture.Assignment.ObserveAssignment(ctx, fixture.Reference)
	if !supported {
		return expectKind(observeErr, tracker.ErrorCapabilityUnsupported)
	}
	if observeErr != nil {
		return observeErr
	}
	request := tracker.ProjectAssignmentRequest{ExternalRef: fixture.Reference, OperationID: "operation-conformance-assignment", Assignee: "actor-conformance", ExpectedObservation: before.Observation.Revision, RequiredGuarantee: fixture.Capabilities.Assignment}
	first, err := fixture.Assignment.ProjectAssignment(ctx, request)
	if err != nil {
		return err
	}
	second, err := fixture.Assignment.ProjectAssignment(ctx, request)
	if err != nil {
		return err
	}
	after, err := fixture.Assignment.ObserveAssignment(ctx, fixture.Reference)
	if err != nil {
		return err
	}
	if first.Observation.Assignee != request.Assignee || first.Observation.OperationMarker != request.OperationID.String() || after.Assignee != request.Assignee || !second.Replayed {
		return fmt.Errorf("assignment effect or replay mismatch")
	}
	return nil
}

func exerciseCompensation(ctx context.Context, fixture Fixture, supported bool) error {
	actual, err := fixture.Assignment.ObserveAssignment(ctx, fixture.Reference)
	if err != nil {
		return err
	}
	// Compensation is independently executable and does not rely on registry
	// ordering. Establish an attributable effect when the fixture starts with
	// no operation marker.
	if actual.OperationMarker == "" {
		projected, projectErr := fixture.Assignment.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{
			ExternalRef: fixture.Reference, OperationID: "operation-conformance-compensable",
			Assignee: "actor-compensable", ExpectedObservation: actual.Observation.Revision,
			RequiredGuarantee: fixture.Capabilities.Assignment,
		})
		if projectErr != nil {
			return projectErr
		}
		actual = projected.Observation
	}
	request := tracker.CompensateAssignmentRequest{ExternalRef: fixture.Reference, OperationID: "operation-conformance-compensation", ExpectedObservation: actual.Observation.Revision, ExpectedAssignee: actual.Assignee, ExpectedMarker: actual.OperationMarker, RestoreAssignee: "original"}
	mutation, err := fixture.Assignment.CompensateAssignment(ctx, request)
	if !supported {
		return expectKind(err, tracker.ErrorSafeCompensationUnsupported, tracker.ErrorAttributionLost)
	}
	if err != nil {
		return err
	}
	if mutation.Observation.Assignee != request.RestoreAssignee {
		return fmt.Errorf("compensation did not restore assignment")
	}
	return nil
}
