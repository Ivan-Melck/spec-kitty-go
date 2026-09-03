package gapdb

import (
	"bytes"
	"context"
	"errors"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type v2ReconcileDisposition string

const (
	v2ReconcileCommitted            v2ReconcileDisposition = "committed"
	v2ReconcileRetrySamePublication v2ReconcileDisposition = "retry_same_publication"
)

type v2ReconcileResult struct {
	disposition    v2ReconcileDisposition
	observation    journalv2.CommitObservationV2
	operationState journalv2.OperationStateV2
}

// ReconcileV2 performs bounded ordinary reads only. It proves either the exact
// complete publication at one provider revision or the still-current complete
// precondition set for retrying the identical sealed plan.
func (a *Adapter) reconcileV2(ctx context.Context, plan journalv2.SealedCommitPlanV2) (v2ReconcileResult, error) {
	encoded, err := a.encodeV2Plan(plan)
	if err != nil {
		return v2ReconcileResult{}, err
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	exactMembers, precondition, moved := 0, 0, 0
	var commonRevision uint64
	for index, key := range encoded.keys {
		record, readErr := a.client.Get(ctx, key.String())
		if readErr != nil {
			if v2NotFound(readErr) {
				if encoded.batch.Mutations[index].Condition.Kind != gapdbapi.ConditionAbsent {
					return v2ReconcileResult{}, corruptV2Publication("revision-conditioned record is absent", encoded.operationID)
				}
				precondition++
				continue
			}
			return v2ReconcileResult{}, translateError("v2_reconcile_read", string(encoded.operationID), readErr)
		}
		converted, convertErr := convertV2ReadbackRecord(record, key)
		if convertErr != nil || converted.hasExpiry {
			return v2ReconcileResult{}, corruptV2Publication("publication readback metadata is invalid", encoded.operationID)
		}
		condition := encoded.batch.Mutations[index].Condition
		if condition.Kind == gapdbapi.ConditionRevision && converted.revision == uint64(condition.ExpectedRevision) {
			precondition++
			continue
		}
		if !bytes.Equal(converted.canonical, encoded.values[index]) {
			if condition.Kind == gapdbapi.ConditionRevision || v2MovableConflictRole(encoded.roles[index], key) {
				moved++
				continue
			}
			return v2ReconcileResult{}, corruptV2Publication("immutable publication value contradicts sealed plan", encoded.operationID)
		}
		if commonRevision == 0 {
			commonRevision = converted.revision
		} else if commonRevision != converted.revision {
			return v2ReconcileResult{}, corruptV2Publication("publication readback has mixed revisions", encoded.operationID)
		}
		exactMembers++
	}
	if exactMembers == len(encoded.keys) && precondition == 0 && moved == 0 && commonRevision > 0 {
		status, statusErr := a.client.Status(ctx)
		if statusErr != nil {
			return v2ReconcileResult{}, translateError("v2_reconcile_status", string(encoded.operationID), statusErr)
		}
		if uint64(status.CurrentRevision) < commonRevision || uint64(status.DurableThroughRevision) < commonRevision {
			return v2ReconcileResult{}, corruptV2Publication("publication durability cannot be proven", encoded.operationID)
		}
		observation, observationErr := journalv2.NewCommitObservationV2(encoded.publicationID, plan.Digest(), plan.RecordSetDigest(), commonRevision, uint64(status.DurableThroughRevision), len(encoded.keys))
		if observationErr != nil {
			return v2ReconcileResult{}, corruptV2Publication("publication observation is invalid", encoded.operationID)
		}
		return v2ReconcileResult{disposition: v2ReconcileCommitted, observation: observation, operationState: plan.OperationState()}, nil
	}
	if precondition == len(encoded.keys) && exactMembers == 0 {
		return v2ReconcileResult{disposition: v2ReconcileRetrySamePublication, operationState: plan.OperationState()}, nil
	}
	if exactMembers > 0 {
		return v2ReconcileResult{}, corruptV2Publication("publication membership is partial", encoded.operationID)
	}
	if moved > 0 && moved+precondition == len(encoded.keys) {
		return v2ReconcileResult{}, adapterError("v2_reconcile", string(encoded.operationID), OutcomeConflict, RecoveryRead, "sealed conditions moved before publication", nil)
	}
	return v2ReconcileResult{}, corruptV2Publication("publication membership is partial", encoded.operationID)
}

type v2ReadbackRecord struct {
	canonical []byte
	revision  uint64
	hasExpiry bool
}

func convertV2ReadbackRecord(record gapdbapi.Record, expected v2Key) (v2ReadbackRecord, error) {
	if record.Key != expected.String() || record.Revision == 0 || len(record.Value) == 0 || len(record.Value) > v2ValueMaximum(expected.Kind()) {
		return v2ReadbackRecord{}, corruptV2Publication("publication readback record is malformed", "")
	}
	parsed, err := parseV2Key(record.Key)
	if err != nil || parsed.String() != expected.String() || parsed.Kind() != expected.Kind() {
		return v2ReadbackRecord{}, corruptV2Publication("publication readback record is malformed", "")
	}
	return v2ReadbackRecord{canonical: append([]byte(nil), record.Value...), revision: uint64(record.Revision), hasExpiry: record.ExpiresAt != nil}, nil
}

func v2MovableConflictRole(role journalv2.SealedRecordRole, key v2Key) bool {
	return role == journalv2.RecordAggregateHead || role == journalv2.RecordEligibilityGuard || role == journalv2.RecordCheckpointSlot ||
		role == journalv2.RecordJournalEvent && key.Coordinate().Kind != journalv2.AggregateKind("operation")
}

func v2NotFound(err error) bool {
	var providerErr *gapdbapi.Error
	return errors.As(err, &providerErr) && providerErr.Code == gapdbapi.CodeNotFound
}

func corruptV2Publication(detail string, operationID journalv2.OperationID) *Error {
	return adapterError("v2_reconcile", string(operationID), OutcomeMalformedResponse, RecoveryOperator, detail, nil)
}
