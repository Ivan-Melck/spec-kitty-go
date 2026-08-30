package application

import (
	"bytes"
	"context"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

const maxReconcileAttemptsV1 = 16

type ReconcileConfigV1 struct {
	Attempts int
	Deadline time.Duration
}

// Reconcile performs only ordinary reads. It does not subscribe to Watch and
// does not resubmit a mutation. Applied is returned only when the complete
// operation-attributable batch is observable at one commit revision.
func (s *Service) Reconcile(ctx context.Context, batch ValidatedBatchV1, config ReconcileConfigV1) (ResultV1, error) {
	if s == nil || s.port == nil || !s.admission.valid() || !batch.valid() {
		return ResultV1{}, refusal(OutcomeRefused, "reconciliation requires admitted service and validated batch")
	}
	if batch.command.projectID != s.admission.projectID || batch.command.missionID != s.admission.missionID || batch.command.runtimeEpochID != s.admission.epochID {
		return ResultV1{Kind: OutcomeRuntimeEpochConflict, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, nil
	}
	if config.Attempts <= 0 || config.Attempts > maxReconcileAttemptsV1 || config.Deadline <= 0 {
		return ResultV1{}, refusal(OutcomeRefused, "reconciliation bounds are invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, config.Deadline)
	defer cancel()
	var last ResultV1
	for attempt := 0; attempt < config.Attempts; attempt++ {
		result, retry, err := s.reconcileOnce(ctx, batch)
		if err != nil {
			return ResultV1{}, err
		}
		last = result
		if !retry {
			return result, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	if last.OperationID == "" {
		last = ResultV1{Kind: OutcomeIndeterminate, OperationID: batch.command.operationID}
	}
	last.Kind = OutcomeIndeterminate
	last.CommittedRevision = 0
	last.CanonicalResult = nil
	last.ResultType = ""
	last.Recovery = RecoveryOperator
	return last, nil
}

func (s *Service) reconcileOnce(ctx context.Context, batch ValidatedBatchV1) (ResultV1, bool, error) {
	operation, err := s.port.Get(ctx, batch.product.Operation.Key)
	if err != nil {
		if storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
			return ResultV1{Kind: OutcomeProvenAbsent, OperationID: batch.command.operationID, Recovery: RecoveryRetrySameOperation}, false, nil
		}
		return ResultV1{Kind: OutcomeIndeterminate, OperationID: batch.command.operationID, Recovery: RecoveryReconcileSameOperation}, true, nil
	}
	stored, err := controljournal.DecodeOperationRecordV1(operation.Value)
	if err != nil {
		return ResultV1{Kind: OutcomeIndeterminate, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, false, nil
	}
	if !operationMatchesBatch(stored, batch) {
		return ResultV1{Kind: OutcomeOperationIdentityCollision, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, false, nil
	}
	if operation.Revision == 0 {
		return ResultV1{Kind: OutcomeIndeterminate, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, false, nil
	}
	for _, expected := range reconciliationMutations(batch.product) {
		record, readErr := s.port.Get(ctx, expected.Key)
		if readErr != nil {
			if storegapdb.IsKind(readErr, storegapdb.OutcomeNotFound) {
				return ResultV1{Kind: OutcomeIndeterminate, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, false, nil
			}
			return ResultV1{Kind: OutcomeIndeterminate, OperationID: batch.command.operationID, Recovery: RecoveryReconcileSameOperation}, true, nil
		}
		if record.Key != expected.Key || record.Revision != operation.Revision || !bytes.Equal(record.Value, expected.Value) {
			return ResultV1{Kind: OutcomeIndeterminate, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, false, nil
		}
	}
	return ResultV1{
		Kind: OutcomeAlreadyCommitted, OperationID: stored.OperationID,
		CommittedRevision: operation.Revision, ResultType: stored.ResultType,
		CanonicalResult: stored.CanonicalResult(), Recovery: RecoveryNone,
	}, false, nil
}

func reconciliationMutations(product storegapdb.ProductBatch) []storegapdb.Mutation {
	result := make([]storegapdb.Mutation, 0, len(product.Events)+len(product.AggregateHeads)+len(product.EffectIntents)+2)
	result = append(result, product.Events...)
	result = append(result, product.AggregateHeads...)
	if product.Checkpoint != nil {
		result = append(result, *product.Checkpoint, *product.CheckpointHead)
	}
	return append(result, product.EffectIntents...)
}
