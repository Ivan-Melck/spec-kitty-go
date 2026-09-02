package gapdb

import (
	"bytes"
	"context"
	"errors"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type V2ReconcileDisposition string

const (
	V2ReconcileCommitted            V2ReconcileDisposition = "committed"
	V2ReconcileRetrySamePublication V2ReconcileDisposition = "retry_same_publication"
)

type V2ReconcileResult struct {
	disposition    V2ReconcileDisposition
	publicationID  journalv2.PublicationID
	revision       uint64
	operationState journalv2.OperationStateV2
	mutationCount  int
}

func (r V2ReconcileResult) Disposition() V2ReconcileDisposition        { return r.disposition }
func (r V2ReconcileResult) PublicationID() journalv2.PublicationID     { return r.publicationID }
func (r V2ReconcileResult) Revision() uint64                           { return r.revision }
func (r V2ReconcileResult) OperationState() journalv2.OperationStateV2 { return r.operationState }
func (r V2ReconcileResult) MutationCount() int                         { return r.mutationCount }

// ReconcileV2 performs bounded ordinary reads only. It proves either the exact
// complete publication at one provider revision or the still-current complete
// precondition set for retrying the identical sealed plan.
func (a *Adapter) ReconcileV2(ctx context.Context, plan journalv2.SealedCommitPlanV2) (V2ReconcileResult, error) {
	encoded, err := a.encodeV2Plan(plan)
	if err != nil {
		return V2ReconcileResult{}, err
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	committed, sharedMatch, precondition, moved := 0, 0, 0, 0
	var commonRevision uint64
	for index, key := range encoded.keys {
		record, readErr := a.client.Get(ctx, key.String())
		if readErr != nil {
			if v2NotFound(readErr) {
				if encoded.batch.Mutations[index].Condition.Kind != gapdbapi.ConditionAbsent {
					return V2ReconcileResult{}, corruptV2Publication("revision-conditioned record is absent", encoded.operationID)
				}
				precondition++
				continue
			}
			return V2ReconcileResult{}, translateError("v2_reconcile_read", string(encoded.operationID), readErr)
		}
		converted, convertErr := convertV2Record(record, key)
		if convertErr != nil || converted.expiresAt != nil {
			return V2ReconcileResult{}, corruptV2Publication("publication readback metadata is invalid", encoded.operationID)
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
			return V2ReconcileResult{}, corruptV2Publication("immutable publication value contradicts sealed plan", encoded.operationID)
		}
		if commonRevision == 0 {
			commonRevision = converted.revision
		} else if commonRevision != converted.revision {
			return V2ReconcileResult{}, corruptV2Publication("publication readback has mixed revisions", encoded.operationID)
		}
		if v2MovableConflictRole(encoded.roles[index], key) {
			sharedMatch++
		} else {
			committed++
		}
	}
	if committed+sharedMatch == len(encoded.keys) && precondition == 0 && moved == 0 && commonRevision > 0 {
		return V2ReconcileResult{disposition: V2ReconcileCommitted, publicationID: encoded.publicationID, revision: commonRevision,
			operationState: plan.OperationState(), mutationCount: len(encoded.keys)}, nil
	}
	if precondition == len(encoded.keys) && committed == 0 {
		return V2ReconcileResult{disposition: V2ReconcileRetrySamePublication, publicationID: encoded.publicationID,
			operationState: plan.OperationState(), mutationCount: len(encoded.keys)}, nil
	}
	if committed == 0 && moved+sharedMatch > 0 && moved+sharedMatch+precondition == len(encoded.keys) {
		return V2ReconcileResult{}, adapterError("v2_reconcile", string(encoded.operationID), OutcomeConflict, RecoveryRead, "sealed conditions moved before publication", nil)
	}
	return V2ReconcileResult{}, corruptV2Publication("publication membership is partial", encoded.operationID)
}

func v2MovableConflictRole(role journalv2.SealedRecordRole, key V2Key) bool {
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
