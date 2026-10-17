package productionruntime

import (
	"bytes"
	"context"
	"errors"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// AcknowledgedPublicationProof is owned verification data, not command admission
// or mission membership. HistoricalOperationRecord is the operation journal
// envelope, not the mutable operation head. CurrentState may be newer.
// Immutable journal values have copy-only accessors; CurrentProgressItem's bytes
// and JournalRecords' slice are separately owned by this return value.
type AcknowledgedPublicationProof struct {
	Binding                   journalv2.OperationBindingV2
	BindingRecord             journalv2.RecoveryRecordV2
	Witness                   journalv2.PublicationWitnessV2
	WitnessRecord             journalv2.RecoveryRecordV2
	HistoricalOperationState  journalv2.OperationStateV2
	HistoricalOperationRecord journalv2.RecoveryRecordV2
	JournalRecords            []journalv2.RecoveryRecordV2
	CurrentProgressItem       ProgressItem
	CurrentState              journalv2.OperationStateV2
	CapturedCoordinate        journalv2.StoreRevisionV2
	// Optional original permanent claim evidence, not workspace membership.
	WorkspacePathClaim  *journalv2.WorkspacePathClaimV2
	ClaimRecordRevision uint64
}

type acknowledgedWorkspacePathClaimReader interface {
	ObserveAcknowledgedWorkspacePathClaimV2(context.Context, journalv2.StoreRevisionV2, journalv2.AcknowledgedPublicationV2) (journalv2.WorkspacePathClaimV2, uint64, error)
}

type acknowledgedPublicationProofReader interface {
	verifyAcknowledgedPublication(context.Context, journalv2.StoreRevisionV2, journalv2.AcknowledgedPublicationV2, ...journalv2.AcknowledgedPublicationV2) (AcknowledgedPublicationProof, error)
}

// VerifyAcknowledgedPublication accepts only an actual opaque coordinator ACK.
// It cannot widen InspectOperation's scope, refresh a projection, or write data.
// A continuation requires exactly one collected initial ACK, independently
// verified at the same capture. Initial publications reject extraneous origins.
// The sealed plan validates its predecessor when constructed, but has no prior
// state getter: callers must still verify every collected ACK and compare the
// latest ACK to CurrentState; this proof does not attest an entire trace.
func (h *Host) VerifyAcknowledgedPublication(ctx context.Context, ack journalv2.AcknowledgedPublicationV2, origin ...journalv2.AcknowledgedPublicationV2) (AcknowledgedPublicationProof, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return AcknowledgedPublicationProof{}, err
	}
	if h == nil || h.projection == nil {
		return AcknowledgedPublicationProof{}, proofIncomplete("publication_projection")
	}
	reader, ok := h.owner.(acknowledgedPublicationProofReader)
	if !ok {
		return AcknowledgedPublicationProof{}, proofIncomplete("publication_reader")
	}
	snapshot := h.projection.Snapshot()
	if snapshot == nil || snapshot.DatabaseID() != h.databaseID {
		return AcknowledgedPublicationProof{}, proofIncomplete("publication_projection")
	}
	captured, err := journalv2.NewStoreRevisionV2(h.databaseID, snapshot.Revision(), snapshot.DurableThroughRevision())
	if err != nil {
		return AcknowledgedPublicationProof{}, proofIncomplete("publication_coordinate")
	}
	return reader.verifyAcknowledgedPublication(ctx, captured, ack, origin...)
}

func (o *productionOwner) verifyAcknowledgedPublication(ctx context.Context, captured journalv2.StoreRevisionV2, ack journalv2.AcknowledgedPublicationV2, origin ...journalv2.AcknowledgedPublicationV2) (AcknowledgedPublicationProof, error) {
	if o == nil || o.adapter == nil || o.database != captured.DatabaseID() {
		return AcknowledgedPublicationProof{}, proofIncomplete("publication_reader")
	}
	return verifyAcknowledgedPublication(ctx, o.adapter, captured, ack, origin...)
}

func proofIncomplete(field string) error {
	return &ProgressError{Kind: ProgressIncomplete, Field: field}
}

func proofReadError(ctx context.Context, err error, field string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return proofIncomplete(field)
}

func verifyAcknowledgedPublication(ctx context.Context, reader operationObservationReader, captured journalv2.StoreRevisionV2, ack journalv2.AcknowledgedPublicationV2, origin ...journalv2.AcknowledgedPublicationV2) (AcknowledgedPublicationProof, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return AcknowledgedPublicationProof{}, err
	}
	plan := ack.Plan()
	state := plan.OperationState()
	if reader == nil || ack.ProviderRevision() == 0 || ack.DurableThroughRevision() < ack.ProviderRevision() ||
		ack.PublicationID() != plan.PublicationID() || ack.PlanDigest().IsZero() || ack.PlanDigest() != plan.Digest() ||
		ack.RecordSetDigest().IsZero() || ack.RecordSetDigest() != plan.RecordSetDigest() ||
		ack.MutationCount() != plan.MutationCount() || ack.AssertionCount() != plan.AssertionCount() || plan.MutationCount() == 0 ||
		state.LastPublicationID() != ack.PublicationID() || state.LogicalVersion() == 0 {
		return AcknowledgedPublicationProof{}, proofIncomplete("acknowledged_publication")
	}
	if _, err := journalv2.NewStoreRevisionV2(captured.DatabaseID(), captured.CurrentRevision(), captured.DurableThroughRevision()); err != nil ||
		ack.ProviderRevision() > captured.CurrentRevision() || ack.DurableThroughRevision() > captured.DurableThroughRevision() {
		return AcknowledgedPublicationProof{}, proofIncomplete("publication_coordinate")
	}
	var claim *journalv2.WorkspacePathClaimV2
	var claimRevision uint64
	if expected := plan.WorkspacePathClaim(); expected != nil {
		closed, ok := reader.(acknowledgedWorkspacePathClaimReader)
		if !ok {
			return AcknowledgedPublicationProof{}, proofIncomplete("workspace_path_claim_reader")
		}
		observed, revision, err := closed.ObserveAcknowledgedWorkspacePathClaimV2(ctx, captured, ack)
		if err != nil {
			return AcknowledgedPublicationProof{}, proofReadError(ctx, err, "workspace_path_claim")
		}
		if observed.ClaimDigest() != expected.ClaimDigest() || revision != ack.ProviderRevision() || !bytes.Equal(observed.CanonicalValue(), expected.CanonicalValue()) {
			return AcknowledgedPublicationProof{}, proofIncomplete("workspace_path_claim")
		}
		owned, err := journalv2.NewWorkspacePathClaimV2(observed.ClaimDigest(), observed.CanonicalValue())
		if err != nil {
			return AcknowledgedPublicationProof{}, proofIncomplete("workspace_path_claim")
		}
		claim, claimRevision = &owned, revision
	}
	expectedBinding := plan.BindingCreate()
	bindingRevision := ack.ProviderRevision()
	initial := expectedBinding != nil
	if len(origin) > 1 || (initial && len(origin) != 0) {
		return AcknowledgedPublicationProof{}, proofIncomplete("publication_origin_count")
	}
	if initial {
		if state.LogicalVersion() != 1 {
			return AcknowledgedPublicationProof{}, proofIncomplete("operation_binding")
		}
	} else {
		if len(origin) != 1 {
			return AcknowledgedPublicationProof{}, proofIncomplete("continuation_requires_initial_ack")
		}
		root := origin[0]
		rootState := root.Plan().OperationState()
		if root.Plan().BindingCreate() == nil || rootState.LogicalVersion() != 1 || state.LogicalVersion() <= 1 ||
			rootState.OperationID() != state.OperationID() || root.ProviderRevision() >= ack.ProviderRevision() {
			return AcknowledgedPublicationProof{}, proofIncomplete("publication_origin_identity")
		}
		if _, err := verifyAcknowledgedPublication(ctx, reader, captured, root); err != nil {
			return AcknowledgedPublicationProof{}, err
		}
		expectedBinding = root.Plan().BindingCreate()
		bindingRevision = root.ProviderRevision()
	}
	read := func(address journalv2.RecordAddressV2, exactRevision uint64, field string) (journalv2.RecoveryRecordV2, error) {
		if err := ctx.Err(); err != nil {
			return journalv2.RecoveryRecordV2{}, err
		}
		request, err := journalv2.NewReadRequestV2(address, captured)
		if err != nil {
			return journalv2.RecoveryRecordV2{}, proofIncomplete(field)
		}
		record, err := reader.ReadV2(ctx, request)
		if err != nil {
			return journalv2.RecoveryRecordV2{}, proofReadError(ctx, err, field)
		}
		if record.Address() != address || record.RecordRevision() == 0 || record.RecordRevision() > captured.CurrentRevision() || record.ExpiresAt() != "" ||
			(exactRevision != 0 && record.RecordRevision() != exactRevision) {
			return journalv2.RecoveryRecordV2{}, proofIncomplete(field)
		}
		return record, nil
	}
	bindingAddress, err := journalv2.NewOperationBindingAddressV2(state.OperationID())
	if err != nil {
		return AcknowledgedPublicationProof{}, proofIncomplete("operation_binding")
	}
	bindingRecord, err := read(bindingAddress, bindingRevision, "operation_binding_coordinate")
	if err != nil {
		return AcknowledgedPublicationProof{}, err
	}
	binding, err := journalv2.DecodeOperationBindingRecordV2(bindingRecord)
	expectedBindingBytes, encodeErr := journalv2.EncodeOperationBindingV2(*expectedBinding)
	if err != nil || encodeErr != nil || binding.OperationID() != state.OperationID() || !bytes.Equal(bindingRecord.CanonicalValue(), expectedBindingBytes) {
		return AcknowledgedPublicationProof{}, proofIncomplete("operation_binding")
	}
	witnessAddress, err := ack.PublicationID().WitnessAddressV2()
	if err != nil {
		return AcknowledgedPublicationProof{}, proofIncomplete("publication_witness")
	}
	witnessRecord, err := read(witnessAddress, ack.ProviderRevision(), "publication_witness_coordinate")
	if err != nil {
		return AcknowledgedPublicationProof{}, err
	}
	witness, err := journalv2.DecodePublicationWitnessV2(witnessRecord.CanonicalValue())
	expectedWitness := plan.PublicationWitness()
	expectedWitnessBytes, encodeErr := journalv2.EncodePublicationWitnessV2(expectedWitness)
	if err != nil || encodeErr != nil || witness != expectedWitness || !bytes.Equal(witnessRecord.CanonicalValue(), expectedWitnessBytes) ||
		witness.PlanDigest() != ack.PlanDigest() || witness.AuthorityRecordSetDigest() != ack.RecordSetDigest() ||
		int(witness.AuthorityRecordCount())+1 != ack.MutationCount() || witness.OperationID() != state.OperationID() || witness.OperationLogicalVersion() != state.LogicalVersion() {
		return AcknowledgedPublicationProof{}, proofIncomplete("publication_witness")
	}
	var historical journalv2.OperationStateV2
	var historicalRecord journalv2.RecoveryRecordV2
	records := make([]journalv2.RecoveryRecordV2, 0, len(plan.Events()))
	operationEvents := 0
	registry := journalv2.DefaultEventRegistryV2()
	for _, sealedEvent := range plan.Events() {
		address, err := journalv2.NewJournalEventAddressV2(sealedEvent.Coordinate(), sealedEvent.LogicalVersion())
		if err != nil {
			return AcknowledgedPublicationProof{}, proofIncomplete("publication_event")
		}
		record, err := read(address, ack.ProviderRevision(), "publication_event_coordinate")
		if err != nil {
			return AcknowledgedPublicationProof{}, err
		}
		event, err := journalv2.DecodeEventEnvelopeRecordV2(record, registry)
		expectedBytes, encodeErr := journalv2.EncodeEventEnvelopeV2(sealedEvent)
		if err != nil || encodeErr != nil || !bytes.Equal(record.CanonicalValue(), expectedBytes) ||
			event.OperationID() != state.OperationID() || event.OperationVersion() != state.LogicalVersion() || event.PublicationID() != ack.PublicationID() {
			return AcknowledgedPublicationProof{}, proofIncomplete("publication_event")
		}
		records = append(records, record)
		if event.Family() != "operation" {
			continue
		}
		operationEvents++
		// Physical operation storage is host-only even for mission/work bindings.
		if event.Coordinate().Scope != (journalv2.Scope{Kind: journalv2.ScopeHost}) || event.Coordinate().ID != journalv2.AggregateID(binding.OperationID()) ||
			(initial && (event.ActorID() != binding.ActorID() || event.ProcessID() != binding.ProcessID() || event.CommittedRuntimeEpochID() != binding.OriginatingEpoch())) {
			return AcknowledgedPublicationProof{}, proofIncomplete("operation_event_binding")
		}
		historical, err = journalv2.ReconstructOperationStateFromEventV2(event)
		if err != nil || !journalv2.EqualOperationStatesV2(historical, state) {
			return AcknowledgedPublicationProof{}, proofIncomplete("historical_operation_state")
		}
		historicalRecord = record
	}
	if operationEvents != 1 {
		return AcknowledgedPublicationProof{}, proofIncomplete("historical_operation_event")
	}
	headAddress, err := journalv2.NewOperationHeadAddressV2(state.OperationID())
	if err != nil {
		return AcknowledgedPublicationProof{}, proofIncomplete("operation_state")
	}
	headRecord, err := read(headAddress, 0, "operation_state_coordinate")
	if err != nil {
		return AcknowledgedPublicationProof{}, err
	}
	current, err := journalv2.DecodeOperationStateRecordV2(headRecord)
	if err != nil || current.OperationID() != state.OperationID() || current.LogicalVersion() < state.LogicalVersion() || uint64(current.LogicalVersion()) > captured.CurrentRevision() || headRecord.RecordRevision() < ack.ProviderRevision() ||
		uint64(current.LogicalVersion()-state.LogicalVersion()) > headRecord.RecordRevision()-ack.ProviderRevision() ||
		(current.LogicalVersion() > state.LogicalVersion() && (headRecord.RecordRevision() <= ack.ProviderRevision() || current.LastPublicationID() == ack.PublicationID())) ||
		(current.LogicalVersion() == state.LogicalVersion() && (!journalv2.EqualOperationStatesV2(current, historical) || headRecord.RecordRevision() != ack.ProviderRevision())) {
		return AcknowledgedPublicationProof{}, proofIncomplete("current_operation_state")
	}
	if err := reader.VerifyRevisionV2(ctx, captured); err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return AcknowledgedPublicationProof{}, proofReadError(ctx, err, "publication_revision")
		}
		return AcknowledgedPublicationProof{}, &ProgressError{Kind: ProgressResyncRequired, Field: "publication_revision"}
	}
	if err := ctx.Err(); err != nil {
		return AcknowledgedPublicationProof{}, err
	}
	return AcknowledgedPublicationProof{
		Binding: binding, BindingRecord: bindingRecord, Witness: witness, WitnessRecord: witnessRecord,
		HistoricalOperationState: historical, HistoricalOperationRecord: historicalRecord, JournalRecords: records,
		CurrentState: current, CurrentProgressItem: ProgressItem{Family: "operation", AggregateID: string(current.OperationID()), LogicalVersion: uint64(current.LogicalVersion()),
			LastPublicationID: string(current.LastPublicationID()), AuthorityState: string(current.Status()), CanonicalState: headRecord.CanonicalValue()},
		CapturedCoordinate: captured, WorkspacePathClaim: claim, ClaimRecordRevision: claimRevision,
	}, nil
}
