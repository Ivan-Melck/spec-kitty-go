package gapdb

import (
	"context"
	"encoding/json"
	"errors"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type v2EncodedPlan struct {
	publicationID journalv2.PublicationID
	operationID   journalv2.OperationID
	keys          []v2Key
	roles         []journalv2.SealedRecordRole
	values        [][]byte
	batch         gapdbapi.Batch
	rawBytes      int
	wireBytes     int
}

var _ journalv2.DurablePublicationPortV2 = (*Adapter)(nil)

// Commit is the durable foundation port. CommitV2 is retained as the adapter's
// explicit versioned entry point; both return the provider-neutral observation
// that only the foundation coordinator may acknowledge.
func (a *Adapter) Commit(ctx context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	return a.CommitV2(ctx, plan)
}

// CommitV2 translates the immutable WP02 record projection to one durable
// provider transaction. It does not parse capability values or retry.
func (a *Adapter) CommitV2(ctx context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	encoded, err := a.encodeV2Plan(plan)
	if err != nil {
		return journalv2.CommitObservationV2{}, err
	}
	if ctx != nil && ctx.Err() != nil {
		return journalv2.CommitObservationV2{}, adapterError("v2_commit", string(encoded.operationID), OutcomeCanceled, RecoveryRetrySameOperation, "caller canceled before delivery", ctx.Err())
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	ctx = gapdbapi.WithRequestID(ctx, string(encoded.publicationID))
	result, callErr := a.client.AtomicBatch(ctx, encoded.batch)
	if callErr != nil {
		if v2DefiniteConflict(callErr) {
			reconciled, reconcileErr := a.reconcileV2(ctx, plan)
			if reconcileErr == nil && reconciled.disposition == v2ReconcileCommitted {
				return reconciled.observation, nil
			}
			if reconcileErr != nil && !IsKind(reconcileErr, OutcomeConflict) {
				return journalv2.CommitObservationV2{}, reconcileErr
			}
			return journalv2.CommitObservationV2{}, adapterError("v2_commit", string(encoded.operationID), OutcomeConflict, RecoveryRead, "conditional authority changed after same-publication resolution", callErr)
		}
		return journalv2.CommitObservationV2{}, translateV2MutationError("v2_commit", encoded.operationID, callErr)
	}
	if result.Revision == 0 || result.Ack != gapdbapi.AckDurable || result.DurableThroughRevision < result.Revision || result.MutationCount != len(encoded.batch.Mutations) {
		return journalv2.CommitObservationV2{}, ambiguousV2Error("provider acknowledgement cannot prove durable publication", encoded.operationID, nil)
	}
	observation, err := journalv2.NewCommitObservationV2(encoded.publicationID, plan.Digest(), plan.RecordSetDigest(), uint64(result.Revision), uint64(result.DurableThroughRevision), result.MutationCount)
	if err != nil {
		return journalv2.CommitObservationV2{}, ambiguousV2Error("provider acknowledgement cannot form a sealed observation", encoded.operationID, err)
	}
	return observation, nil
}

func v2DefiniteConflict(err error) bool {
	var providerErr *gapdbapi.Error
	if !errors.As(err, &providerErr) || providerErr.OperationApplied {
		return false
	}
	switch providerErr.Code {
	case gapdbapi.CodeAlreadyExists, gapdbapi.CodeRevisionMismatch, gapdbapi.CodeConditionFailed:
		return true
	default:
		return false
	}
}

func (a *Adapter) preflightCommitV2(plan journalv2.SealedCommitPlanV2) (BatchMeasurement, error) {
	encoded, err := a.encodeV2Plan(plan)
	if err != nil {
		return BatchMeasurement{}, err
	}
	return BatchMeasurement{MutationCount: len(encoded.batch.Mutations), RawBytes: encoded.rawBytes, WireBytes: encoded.wireBytes}, nil
}

func (a *Adapter) encodeV2Plan(plan journalv2.SealedCommitPlanV2) (v2EncodedPlan, error) {
	publicationID, err := journalv2.DecodePublicationID(string(plan.PublicationID()))
	evidence := plan.CanonicalBytes()
	if err != nil || len(evidence) == 0 || len(evidence) > MaxWireFrameBytes {
		return v2EncodedPlan{}, invalidV2Plan("sealed plan identity or evidence budget")
	}
	operationID := plan.OperationState().OperationID()
	if _, err := journalv2.NewOperationID(string(operationID)); err != nil || plan.OperationState().LastPublicationID() != publicationID {
		return v2EncodedPlan{}, invalidV2Plan("sealed operation membership")
	}
	records := plan.Records()
	if len(records) == 0 || len(records) > MaxMutationsPerBatch {
		return v2EncodedPlan{}, invalidV2Plan("mutation count")
	}
	encoded := v2EncodedPlan{
		publicationID: publicationID,
		operationID:   operationID,
		keys:          make([]v2Key, len(records)),
		roles:         make([]journalv2.SealedRecordRole, len(records)),
		values:        make([][]byte, len(records)),
		batch:         gapdbapi.Batch{Ack: gapdbapi.AckDurable, Mutations: make([]gapdbapi.Mutation, len(records))},
	}
	counts := make(map[journalv2.SealedRecordRole]int, 9)
	seen := make(map[string]struct{}, len(records))
	for index, record := range records {
		key, err := v2KeyForSealedRecord(record)
		if err != nil {
			return v2EncodedPlan{}, err
		}
		if record.Role() == journalv2.RecordPublicationWitness && key.PublicationID() != publicationID {
			return v2EncodedPlan{}, invalidV2Plan("publication witness identity")
		}
		if _, duplicate := seen[key.String()]; duplicate {
			return v2EncodedPlan{}, invalidV2Plan("duplicate projected key")
		}
		seen[key.String()] = struct{}{}
		value := record.CanonicalValue()
		if (record.Role() == journalv2.RecordCheckpointRecord) != (record.ValueClass() == journalv2.RecordValueCheckpoint) {
			return v2EncodedPlan{}, invalidV2Plan("record value class")
		}
		if record.ValueClass() != journalv2.RecordValueOrdinary && record.ValueClass() != journalv2.RecordValueCheckpoint {
			return v2EncodedPlan{}, invalidV2Plan("record value class")
		}
		maximum := MaxOrdinaryValueBytes
		if record.ValueClass() == journalv2.RecordValueCheckpoint {
			maximum = MaxCheckpointValueBytes
		}
		if len(value) == 0 || len(value) > maximum || len(key.String()) > MaxKeyBytes {
			return v2EncodedPlan{}, invalidV2Plan("projected record budget")
		}
		if len(key.String()) > MaxRawBatchBytes-encoded.rawBytes || len(value) > MaxRawBatchBytes-encoded.rawBytes-len(key.String()) {
			return v2EncodedPlan{}, invalidV2Plan("raw batch budget")
		}
		encoded.rawBytes += len(key.String()) + len(value)
		condition, err := v2ProviderCondition(record)
		if err != nil {
			return v2EncodedPlan{}, err
		}
		encoded.keys[index] = key
		encoded.roles[index] = record.Role()
		encoded.values[index] = append([]byte(nil), value...)
		encoded.batch.Mutations[index] = gapdbapi.NewPutMutation(key.String(), value, condition, nil)
		counts[record.Role()]++
	}
	if counts[journalv2.RecordJournalEvent] < 1 || counts[journalv2.RecordJournalEvent] > MaxEventsPerCommand ||
		counts[journalv2.RecordOperationHead] != 1 || counts[journalv2.RecordOperationHead]+counts[journalv2.RecordAggregateHead] > MaxHeadsPerCommand ||
		counts[journalv2.RecordEffectIntent] > MaxIntentsPerCommand || counts[journalv2.RecordOperationBinding] > 1 ||
		counts[journalv2.RecordCheckpointRecord] != counts[journalv2.RecordCheckpointSlot] || counts[journalv2.RecordPublicationWitness] != 1 {
		return v2EncodedPlan{}, invalidV2Plan("role cardinality")
	}
	wire := struct {
		SchemaVersion uint64         `json:"schema_version"`
		RequestID     string         `json:"request_id"`
		Operation     string         `json:"operation"`
		Arguments     gapdbapi.Batch `json:"arguments"`
	}{AdmittedWireVersion, string(publicationID), "atomic_batch", encoded.batch}
	wireBytes, err := json.Marshal(wire)
	if err != nil {
		return v2EncodedPlan{}, invalidV2Plan("wire encoding")
	}
	encoded.wireBytes = a.wireSize(wireBytes)
	if encoded.wireBytes <= 0 || encoded.wireBytes > MaxWireFrameBytes {
		return v2EncodedPlan{}, invalidV2Plan("wire frame budget")
	}
	return encoded, nil
}

func v2KeyForSealedRecord(record journalv2.SealedRecordV2) (v2Key, error) {
	target := record.Target()
	emptyCoordinate := target.Coordinate() == (journalv2.AggregateCoordinate{})
	var key v2Key
	var err error
	switch record.Role() {
	case journalv2.RecordOperationBinding, journalv2.RecordOperationHead:
		if target.OperationID() == "" || target.PublicationID() != "" || target.EffectID() != "" || !emptyCoordinate || target.LogicalVersion() != 0 || target.MissionID() != "" || target.ShardID() != "" || target.CheckpointID() != "" || target.CheckpointSlotID() != "" {
			return v2Key{}, invalidV2Plan("operation target")
		}
		if record.Role() == journalv2.RecordOperationBinding {
			key, err = v2OperationBindingKey(target.OperationID())
		} else {
			key, err = v2OperationHeadKey(target.OperationID())
		}
	case journalv2.RecordJournalEvent:
		if target.OperationID() != "" || target.PublicationID() != "" || target.EffectID() != "" || emptyCoordinate || target.LogicalVersion() == 0 || target.MissionID() != "" || target.ShardID() != "" || target.CheckpointID() != "" || target.CheckpointSlotID() != "" {
			return v2Key{}, invalidV2Plan("journal target")
		}
		key, err = v2JournalEventKey(target.Coordinate(), target.LogicalVersion())
	case journalv2.RecordAggregateHead:
		if target.OperationID() != "" || target.PublicationID() != "" || target.EffectID() != "" || emptyCoordinate || target.LogicalVersion() != 0 || target.MissionID() != "" || target.ShardID() != "" || target.CheckpointID() != "" || target.CheckpointSlotID() != "" {
			return v2Key{}, invalidV2Plan("aggregate head target")
		}
		key, err = v2AggregateHeadKey(target.Coordinate())
	case journalv2.RecordEffectIntent:
		if target.EffectID() == "" {
			return v2Key{}, invalidV2Plan("effect target")
		}
		if target.OperationID() != "" || target.PublicationID() != "" || !emptyCoordinate || target.LogicalVersion() != 0 || target.MissionID() != "" || target.ShardID() != "" || target.CheckpointID() != "" || target.CheckpointSlotID() != "" {
			return v2Key{}, invalidV2Plan("effect target")
		}
		key, err = v2EffectIntentKey(target.EffectID())
	case journalv2.RecordEligibilityGuard:
		if target.MissionID() == "" || target.ShardID() == "" || target.OperationID() != "" || target.PublicationID() != "" || target.EffectID() != "" || !emptyCoordinate || target.LogicalVersion() != 0 || target.CheckpointID() != "" || target.CheckpointSlotID() != "" {
			return v2Key{}, invalidV2Plan("eligibility target")
		}
		key, err = v2EligibilityGuardKey(target.MissionID(), target.ShardID())
	case journalv2.RecordCheckpointRecord:
		if target.CheckpointID() == "" || target.OperationID() != "" || target.PublicationID() != "" || target.EffectID() != "" || !emptyCoordinate || target.LogicalVersion() != 0 || target.MissionID() != "" || target.ShardID() != "" || target.CheckpointSlotID() != "" {
			return v2Key{}, invalidV2Plan("checkpoint record target")
		}
		key, err = v2CheckpointRecordKey(target.CheckpointID())
	case journalv2.RecordCheckpointSlot:
		if target.CheckpointSlotID() == "" || target.OperationID() != "" || target.PublicationID() != "" || target.EffectID() != "" || !emptyCoordinate || target.LogicalVersion() != 0 || target.MissionID() != "" || target.ShardID() != "" || target.CheckpointID() != "" {
			return v2Key{}, invalidV2Plan("checkpoint slot target")
		}
		key, err = v2CheckpointSlotKey(target.CheckpointSlotID())
	case journalv2.RecordPublicationWitness:
		if target.PublicationID() == "" || target.OperationID() != "" || target.EffectID() != "" || !emptyCoordinate || target.LogicalVersion() != 0 || target.MissionID() != "" || target.ShardID() != "" || target.CheckpointID() != "" || target.CheckpointSlotID() != "" {
			return v2Key{}, invalidV2Plan("publication witness target")
		}
		key, err = v2PublicationWitnessKey(target.PublicationID())
	default:
		return v2Key{}, invalidV2Plan("unknown record role")
	}
	if err != nil {
		return v2Key{}, invalidV2Plan("record target key")
	}
	return key, nil
}

func v2ProviderCondition(record journalv2.SealedRecordV2) (gapdbapi.Condition, error) {
	immutable := record.Role() == journalv2.RecordOperationBinding || record.Role() == journalv2.RecordJournalEvent ||
		record.Role() == journalv2.RecordEffectIntent || record.Role() == journalv2.RecordCheckpointRecord ||
		record.Role() == journalv2.RecordPublicationWitness
	condition := record.Condition()
	switch condition.Kind() {
	case journalv2.ConditionAbsent:
		if condition.ObservedRevision() != 0 {
			return gapdbapi.Condition{}, invalidV2Plan("absent condition revision")
		}
		return gapdbapi.Condition{Kind: gapdbapi.ConditionAbsent}, nil
	case journalv2.ConditionRecordRevision:
		if immutable || condition.ObservedRevision() == 0 {
			return gapdbapi.Condition{}, invalidV2Plan("revision condition")
		}
		return gapdbapi.Condition{Kind: gapdbapi.ConditionRevision, ExpectedRevision: gapdbapi.Revision(condition.ObservedRevision())}, nil
	default:
		return gapdbapi.Condition{}, invalidV2Plan("condition kind")
	}
}

func invalidV2Plan(detail string) *Error {
	return adapterError("v2_commit", "", OutcomeInvalid, RecoveryFixRequest, detail, nil)
}

func translateV2MutationError(operation string, operationID journalv2.OperationID, err error) error {
	var transport *gapdbapi.TransportError
	if errors.As(err, &transport) {
		if transport.Ambiguous {
			return ambiguousV2Error("delivery outcome is unknown", operationID, err)
		}
		return adapterError(operation, string(operationID), OutcomeUnavailable, RecoveryRetrySameOperation, "request was not delivered", err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ambiguousV2Error("delivery outcome requires reconciliation", operationID, err)
	}
	var providerErr *gapdbapi.Error
	if errors.As(err, &providerErr) {
		if providerErr.OperationApplied || providerErr.Code == gapdbapi.CodeAuditFailedAfterApply || providerErr.Code == gapdbapi.CodeDeadlineExceeded {
			return ambiguousV2Error("provider reports a may-have-applied outcome", operationID, err)
		}
		switch providerErr.Code {
		case gapdbapi.CodeAlreadyExists, gapdbapi.CodeRevisionMismatch, gapdbapi.CodeConditionFailed:
			return adapterError(operation, string(operationID), OutcomeConflict, RecoveryRead, "conditional authority changed", err)
		case gapdbapi.CodeServerUnavailable, gapdbapi.CodeServerBusy, gapdbapi.CodeServerShuttingDown, gapdbapi.CodeStorageDegraded:
			return adapterError(operation, string(operationID), OutcomeUnavailable, RecoveryRetrySameOperation, "provider refused before apply", err)
		case gapdbapi.CodeUnsupportedVersion, gapdbapi.CodeUnknownFormat:
			return adapterError(operation, string(operationID), OutcomeUnsupportedVersion, RecoveryUpgradeProvider, "provider version is unsupported", err)
		default:
			return adapterError(operation, string(operationID), OutcomeInvalid, RecoveryOperator, "provider refused publication", err)
		}
	}
	return ambiguousV2Error("unclassified publication failure", operationID, err)
}

func ambiguousV2Error(detail string, operationID journalv2.OperationID, cause error) *Error {
	return adapterError("v2_commit", string(operationID), OutcomeAmbiguous, RecoveryReconcile, detail, cause)
}
