package productionruntime

import (
	"context"
	"errors"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type operationObservationReader interface {
	ReadV2(context.Context, journalv2.ReadRequestV2) (journalv2.RecoveryRecordV2, error)
	VerifyRevisionV2(context.Context, journalv2.StoreRevisionV2) error
}

// Operation aggregates are host-scoped for global idempotency. Their immutable
// bindings, not current mission heads or ID spelling, supply mission membership.
// This read-only query uses semantic addresses at one restored coordinate; it
// cannot allocate a workspace, publish an operation, or rotate an epoch.
func inspectOperation(ctx context.Context, reader operationObservationReader, captured journalv2.StoreRevisionV2, project journalv2.ProjectID, mission journalv2.MissionID, id string) (ProgressItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	operation, err := journalv2.NewOperationID(id)
	if err != nil {
		return ProgressItem{}, &ProgressError{Kind: ProgressInvalidQuery, Field: "operation_id"}
	}
	if _, err := journalv2.NewProjectID(string(project)); err != nil {
		return ProgressItem{}, &ProgressError{Kind: ProgressInvalidQuery, Field: "project_id"}
	}
	if _, err := journalv2.NewMissionID(string(mission)); err != nil {
		return ProgressItem{}, &ProgressError{Kind: ProgressInvalidQuery, Field: "mission_id"}
	}
	bindingAddress, _ := journalv2.NewOperationBindingAddressV2(operation)
	bindingRequest, err := journalv2.NewReadRequestV2(bindingAddress, captured)
	if err != nil || reader == nil {
		return ProgressItem{}, &ProgressError{Kind: ProgressIncomplete, Field: "operation_coordinate"}
	}
	bindingRecord, err := reader.ReadV2(ctx, bindingRequest)
	if err != nil {
		return ProgressItem{}, operationReadError(err)
	}
	if bindingRecord.Address() != bindingAddress || bindingRecord.RecordRevision() > captured.CurrentRevision() {
		return ProgressItem{}, &ProgressError{Kind: ProgressIncomplete, Field: "operation_binding_coordinate"}
	}
	binding, err := journalv2.DecodeOperationBindingRecordV2(bindingRecord)
	if err != nil || binding.OperationID() != operation {
		return ProgressItem{}, &ProgressError{Kind: ProgressIncomplete, Field: "operation_binding"}
	}
	if binding.Scope().ProjectID != project || binding.Scope().MissionID != mission {
		return ProgressItem{}, &ProgressError{Kind: ProgressNotFound, Field: "aggregate_id"}
	}
	stateAddress, _ := journalv2.NewOperationHeadAddressV2(operation)
	stateRequest, _ := journalv2.NewReadRequestV2(stateAddress, captured)
	stateRecord, err := reader.ReadV2(ctx, stateRequest)
	if err != nil {
		return ProgressItem{}, operationReadError(err)
	}
	if stateRecord.Address() != stateAddress || stateRecord.RecordRevision() > captured.CurrentRevision() {
		return ProgressItem{}, &ProgressError{Kind: ProgressIncomplete, Field: "operation_state_coordinate"}
	}
	state, err := journalv2.DecodeOperationStateRecordV2(stateRecord)
	if err != nil || state.OperationID() != operation {
		return ProgressItem{}, &ProgressError{Kind: ProgressIncomplete, Field: "operation_state"}
	}
	if err := reader.VerifyRevisionV2(ctx, captured); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ProgressItem{}, err
		}
		return ProgressItem{}, &ProgressError{Kind: ProgressResyncRequired, Field: "operation_revision"}
	}
	return ProgressItem{
		Family: "operation", AggregateID: id, LogicalVersion: uint64(state.LogicalVersion()),
		LastPublicationID: string(state.LastPublicationID()), AuthorityState: string(state.Status()),
		CanonicalState: stateRecord.CanonicalValue(),
	}, nil
}

func operationReadError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if journalv2.IsKind(err, journalv2.ErrorNotFound) {
		return &ProgressError{Kind: ProgressNotFound, Field: "aggregate_id"}
	}
	return &ProgressError{Kind: ProgressIncomplete, Field: "operation_read"}
}

func (o *Observation) InspectOperation(ctx context.Context, project journalv2.ProjectID, mission journalv2.MissionID, id string) (ProgressItem, error) {
	if o == nil || o.owner == nil || o.owner.adapter == nil {
		return ProgressItem{}, &ProgressError{Kind: ProgressIncomplete, Field: "operation_reader"}
	}
	return inspectOperation(ctx, o.owner.adapter, o.captured, project, mission, id)
}

type operationInspectionReader interface {
	InspectOperation(context.Context, journalv2.StoreRevisionV2, journalv2.ProjectID, journalv2.MissionID, string) (ProgressItem, error)
}

func (o *productionOwner) InspectOperation(ctx context.Context, captured journalv2.StoreRevisionV2, project journalv2.ProjectID, mission journalv2.MissionID, id string) (ProgressItem, error) {
	if o == nil || o.adapter == nil {
		return ProgressItem{}, &ProgressError{Kind: ProgressIncomplete, Field: "operation_reader"}
	}
	return inspectOperation(ctx, o.adapter, captured, project, mission, id)
}

func (h *Host) InspectOperation(ctx context.Context, project journalv2.ProjectID, mission journalv2.MissionID, id string) (ProgressItem, error) {
	if h == nil || h.projection == nil {
		return ProgressItem{}, &ProgressError{Kind: ProgressIncomplete, Field: "operation_projection"}
	}
	reader, ok := h.owner.(operationInspectionReader)
	if !ok {
		return ProgressItem{}, &ProgressError{Kind: ProgressIncomplete, Field: "operation_reader"}
	}
	snapshot := h.projection.Snapshot()
	if snapshot == nil {
		return ProgressItem{}, &ProgressError{Kind: ProgressIncomplete, Field: "operation_projection"}
	}
	captured, err := journalv2.NewStoreRevisionV2(h.databaseID, snapshot.Revision(), snapshot.DurableThroughRevision())
	if err != nil {
		return ProgressItem{}, err
	}
	return reader.InspectOperation(ctx, captured, project, mission, id)
}
