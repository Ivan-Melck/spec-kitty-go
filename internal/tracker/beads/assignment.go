package beads

import (
	"context"
	"errors"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

// ObserveAssignment returns one complete provider observation. Assignment is
// deliberately informational: this adapter has no access to execution leases,
// fencing tokens, or lease-scoped workspaces.
func (bound *BoundAdapter) ObserveAssignment(ctx context.Context, reference tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	if err := validateAssignmentReference(bound, reference); err != nil {
		return tracker.AssignmentObservation{}, err
	}
	record, err := bound.ReadWork(ctx, reference)
	if err != nil {
		return tracker.AssignmentObservation{}, err
	}
	return assignmentObservation(record), nil
}

// ProjectAssignment applies an explicitly authorized visible projection. Beads
// v1.2.2 can atomically claim for its own provider actor, but the portable
// request carries a mapped assignee and an operation marker. Because Beads
// cannot attach that marker in the atomic claim, the complete portable effect
// is available only as an explicitly requested unguarded update.
func (bound *BoundAdapter) ProjectAssignment(ctx context.Context, request tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	if err := validateProjectAssignment(bound, request); err != nil {
		return tracker.AssignmentMutation{}, err
	}
	if request.RequiredGuarantee == tracker.AssignmentAtomicClaim {
		return tracker.AssignmentMutation{}, &tracker.Error{
			Kind: tracker.ErrorGuardedUpdateUnsupported, Capability: tracker.CapabilityAssignment,
			RecoveryCode: "use_explicit_unguarded_assignment",
			Message:      "atomic claim cannot attach the requested operation marker as one provider effect",
		}
	}

	readiness, err := bound.Preflight(ctx)
	if err != nil {
		return tracker.AssignmentMutation{}, err
	}
	return bound.withAssignmentMutation(ctx, readiness, func() (tracker.AssignmentMutation, error) {
		before, issue, err := bound.observeAssignmentIssue(ctx, request.ExternalRef)
		if err != nil {
			return tracker.AssignmentMutation{}, err
		}
		if before.OperationMarker == request.OperationID.String() {
			if before.Assignee != request.Assignee {
				return tracker.AssignmentMutation{Observation: before}, assignmentConflict("assignment operation marker was reused for another assignee")
			}
			return tracker.AssignmentMutation{Observation: before, Guarantee: tracker.AssignmentUnguarded, Replayed: true, Noop: true}, nil
		}
		if before.Observation.Revision != request.ExpectedObservation {
			return tracker.AssignmentMutation{Observation: before}, &tracker.Error{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "observe_then_reconcile", Message: "assignment observation changed before projection"}
		}
		if issue.document.kitty.AssignmentMarker != "" {
			return tracker.AssignmentMutation{Observation: before}, assignmentConflict("another assignment operation marker is already visible")
		}

		desired := issue.document.kitty
		desired.LastOperation = request.OperationID
		desired.AssignmentMarker = request.OperationID
		metadata, err := mergeMetadata(issue.metadata, desired)
		if err != nil {
			return tracker.AssignmentMutation{}, err
		}
		command, err := newUpdateCommand(bound.commandBinding(), updateValues{
			id: request.ExternalRef.ExternalID, assignee: presentUpdateValue(request.Assignee), metadata: presentUpdateValue(string(metadata)),
		})
		if err != nil {
			return tracker.AssignmentMutation{}, err
		}
		response, executeErr := bound.adapter.execute(ctx, command)
		after, observeErr := bound.ObserveAssignment(ctx, request.ExternalRef)
		if observeErr == nil && exactAssignment(after, request.Assignee, request.OperationID.String()) {
			return tracker.AssignmentMutation{Observation: after, Guarantee: tracker.AssignmentUnguarded}, nil
		}
		if executeErr != nil {
			if !mutationCouldHaveApplied(response) && observeErr == nil && sameAssignmentObservation(before, after) {
				return tracker.AssignmentMutation{Observation: after, Guarantee: tracker.AssignmentUnguarded}, executeErr
			}
			return tracker.AssignmentMutation{Observation: after, Guarantee: tracker.AssignmentUnguarded}, ambiguousAssignment("assignment outcome could not be proven", executeErr)
		}
		if observeErr != nil {
			return tracker.AssignmentMutation{}, ambiguousAssignment("assignment post-observation is unavailable", observeErr)
		}
		return tracker.AssignmentMutation{Observation: after, Guarantee: tracker.AssignmentUnguarded}, ambiguousAssignment("assignment value and operation marker are only partially visible", nil)
	})
}

// CompensateAssignment never emulates provider CAS with a read followed by an
// update. A fresh observation is returned as reconciliation evidence, but
// Beads v1.2.2 has no primitive that enforces revision, assignee, and marker at
// its mutation boundary.
func (bound *BoundAdapter) CompensateAssignment(ctx context.Context, request tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	if err := validateCompensateAssignment(bound, request); err != nil {
		return tracker.AssignmentMutation{}, err
	}
	actual, err := bound.ObserveAssignment(ctx, request.ExternalRef)
	if err != nil {
		return tracker.AssignmentMutation{}, err
	}
	result := tracker.AssignmentMutation{Observation: actual, Guarantee: tracker.AssignmentUnguarded, Noop: true}
	if actual.Observation.Revision != request.ExpectedObservation || actual.Assignee != request.ExpectedAssignee || actual.OperationMarker != request.ExpectedMarker {
		return result, &tracker.Error{Kind: tracker.ErrorAttributionLost, Capability: tracker.CapabilitySafeCompensation, RecoveryCode: "preserve_and_reconcile", Message: "fresh assignment observation is no longer attributable to the operation"}
	}
	return result, &tracker.Error{Kind: tracker.ErrorSafeCompensationUnsupported, Capability: tracker.CapabilitySafeCompensation, RecoveryCode: "reconcile_without_mutation", Message: "provider cannot conditionally compensate assignment"}
}

func (bound *BoundAdapter) withAssignmentMutation(ctx context.Context, readiness Readiness, action func() (tracker.AssignmentMutation, error)) (tracker.AssignmentMutation, error) {
	if err := bound.validateReadiness(readiness); err != nil {
		return tracker.AssignmentMutation{}, err
	}
	if bound.binding.ConcurrencyMode == tracker.ConcurrencyServerMultiwriter {
		return action()
	}
	lock, err := bound.locker.acquire(ctx, bound.binding)
	if err != nil {
		return tracker.AssignmentMutation{}, err
	}
	defer lock.release()
	if err := bound.validateReadiness(readiness); err != nil {
		return tracker.AssignmentMutation{}, err
	}
	return action()
}

func (bound *BoundAdapter) observeAssignmentIssue(ctx context.Context, reference tracker.ExternalWorkRef) (tracker.AssignmentObservation, providerIssue, error) {
	issue, err := bound.readIssue(ctx, reference.ExternalID)
	if err != nil {
		return tracker.AssignmentObservation{}, providerIssue{}, err
	}
	if err := validateIssueMapping(issue, reference.ProjectID, reference.WorkID, reference.CreatedByOperation); err != nil {
		return tracker.AssignmentObservation{}, providerIssue{}, err
	}
	record, err := bound.issueRecord(issue)
	if err != nil {
		return tracker.AssignmentObservation{}, providerIssue{}, err
	}
	return assignmentObservation(record), issue, nil
}

func assignmentObservation(record tracker.WorkRecord) tracker.AssignmentObservation {
	return tracker.AssignmentObservation{ExternalRef: record.ExternalRef, Assignee: record.VisibleAssignee, OperationMarker: record.AssignmentMarker, Observation: record.Observation}
}

func validateAssignmentReference(bound *BoundAdapter, reference tracker.ExternalWorkRef) error {
	if bound == nil {
		return invalidCatalogRequest("assignment binding is missing", nil)
	}
	if err := reference.Validate(); err != nil {
		return invalidCatalogRequest("assignment reference is invalid", err)
	}
	if reference.ProjectID != bound.binding.ProjectID || reference.Provider != beadsProvider || reference.MetadataSchema != metadataSchemaV1 {
		return mappingConflict("assignment reference does not belong to this binding")
	}
	return validatePinnedBinding(bound.binding)
}

func validateProjectAssignment(bound *BoundAdapter, request tracker.ProjectAssignmentRequest) error {
	if err := validateAssignmentReference(bound, request.ExternalRef); err != nil {
		return err
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil || request.Assignee == "" || request.ExpectedObservation == 0 {
		return invalidCatalogRequest("operation, mapped assignee, and expected observation are required", err)
	}
	if request.RequiredGuarantee != tracker.AssignmentUnguarded && request.RequiredGuarantee != tracker.AssignmentAtomicClaim {
		return invalidCatalogRequest("assignment guarantee is invalid", nil)
	}
	return nil
}

func validateCompensateAssignment(bound *BoundAdapter, request tracker.CompensateAssignmentRequest) error {
	if err := validateAssignmentReference(bound, request.ExternalRef); err != nil {
		return err
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil || request.ExpectedObservation == 0 || request.ExpectedAssignee == "" || request.ExpectedMarker == "" {
		return invalidCatalogRequest("compensation operation and expected observation, assignee, and marker are required", err)
	}
	return nil
}

func exactAssignment(observation tracker.AssignmentObservation, assignee, marker string) bool {
	return observation.Assignee == assignee && observation.OperationMarker == marker
}

func sameAssignmentObservation(left, right tracker.AssignmentObservation) bool {
	return left.Observation.Revision == right.Observation.Revision && left.Assignee == right.Assignee && left.OperationMarker == right.OperationMarker
}

func assignmentConflict(message string) error {
	return &tracker.Error{Kind: tracker.ErrorRevisionConflict, RecoveryCode: "observe_then_reconcile", Message: message}
}

func ambiguousAssignment(message string, cause error) error {
	var portable *tracker.Error
	if errors.As(cause, &portable) && portable.Kind == tracker.ErrorUnavailable {
		return cause
	}
	return &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "observe_then_reconcile", Message: message, Cause: cause}
}

var _ tracker.AssignmentProjection = (*BoundAdapter)(nil)
