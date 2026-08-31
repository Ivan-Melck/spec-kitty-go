package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	journalcheckpoint "github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

type restoreReadPortV1 interface {
	Get(context.Context, string) (storegapdb.Record, error)
	Scan(context.Context, storegapdb.ScanRequest) (storegapdb.ScanPage, error)
}

// NewRestoreReaderV1 translates the admitted external adapter's ordinary
// Get/Scan surface into the frozen three-method checkpoint restore seam. It
// has no mutation or watch capability.
func NewRestoreReaderV1(port restoreReadPortV1, projectID controljournal.ProjectID, missionID controljournal.MissionID) (journalcheckpoint.RestoreReaderV1, error) {
	if port == nil {
		return nil, refusal(OutcomeRefused, "restore read port is absent")
	}
	if _, err := controljournal.NewProjectID(string(projectID)); err != nil {
		return nil, refusal(OutcomeRefused, "restore project identity is invalid")
	}
	if _, err := controljournal.NewMissionID(string(missionID)); err != nil {
		return nil, refusal(OutcomeRefused, "restore mission identity is invalid")
	}
	return &adapterRestoreReaderV1{port: port, projectID: projectID, prefix: canonicalPrefix(projectID, missionID)}, nil
}

type adapterRestoreReaderV1 struct {
	mu        sync.Mutex
	port      restoreReadPortV1
	projectID controljournal.ProjectID
	prefix    string
	records   []controljournal.StoredEventV1
	revision  uint64
	loaded    bool
	firstPage storegapdb.ScanPage
}

func (reader *adapterRestoreReaderV1) CheckpointHeadsV1(ctx context.Context) ([]journalcheckpoint.CheckpointHeadV1, error) {
	record, err := reader.port.Get(ctx, reader.prefix+"checkpoint-head")
	if storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
		return nil, nil
	}
	if err != nil || record.Key != reader.prefix+"checkpoint-head" || record.Revision == 0 || record.ExpiresAt != nil {
		return nil, fmt.Errorf("checkpoint selector read failed: %w", err)
	}
	head, err := journalcheckpoint.DecodeHeadV1(record.Value)
	if err != nil {
		return nil, err
	}
	return []journalcheckpoint.CheckpointHeadV1{head}, nil
}

func (reader *adapterRestoreReaderV1) CheckpointBlobV1(ctx context.Context, id journalcheckpoint.CheckpointID) ([]byte, error) {
	if id == "" {
		return nil, errors.New("checkpoint identity is absent")
	}
	key := reader.prefix + "checkpoints/" + string(id)
	record, err := reader.port.Get(ctx, key)
	if err != nil || record.Key != key || record.Revision == 0 || record.ExpiresAt != nil {
		return nil, fmt.Errorf("checkpoint record read failed: %w", err)
	}
	return append([]byte(nil), record.Value...), nil
}

func (reader *adapterRestoreReaderV1) JournalPageV1(ctx context.Context, after journalcheckpoint.WatermarkV1, cursor string) (journalcheckpoint.JournalScanPageV1, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	start := 0
	if cursor == "" {
		if err := reader.refreshJournalV1(ctx, after); err != nil {
			return journalcheckpoint.JournalScanPageV1{}, err
		}
	} else {
		if !strings.HasPrefix(cursor, "restore-v1-") {
			return journalcheckpoint.JournalScanPageV1{}, errors.New("restore cursor is invalid")
		}
		var err error
		start, err = strconv.Atoi(strings.TrimPrefix(cursor, "restore-v1-"))
		if err != nil || start <= 0 || start >= len(reader.records) {
			return journalcheckpoint.JournalScanPageV1{}, errors.New("restore cursor is invalid")
		}
	}
	end := min(start+journalcheckpoint.MaxScanPageRecords, len(reader.records))
	page := journalcheckpoint.JournalScanPageV1{ObservedRevision: reader.revision, RecordCount: uint16(end - start)}
	for index := start; index < end; index++ {
		record := reader.records[index]
		page.Records[index-start] = record
		encoded, err := controljournal.EncodeEventEnvelopeV1(record.Envelope)
		if err != nil {
			return journalcheckpoint.JournalScanPageV1{}, err
		}
		page.RawBytes += uint64(len(encoded))
	}
	if end < len(reader.records) {
		page.Truncated = true
		page.Cursor = fmt.Sprintf("restore-v1-%d", end)
	}
	return page, nil
}

func (reader *adapterRestoreReaderV1) refreshJournalV1(ctx context.Context, after journalcheckpoint.WatermarkV1) error {
	if after.ThroughRecordRevision != 0 {
		return journalcheckpoint.NotReadyErrorV1{
			Kind: journalcheckpoint.NotReadyReplayBound, Field: "gapdb_after_revision_scan",
			Recovery: "keep admission closed until GapDB exposes a revision-ordered after-watermark scan",
		}
	}
	providerCursor := ""
	var observed uint64
	var rawBytes uint64
	providerPages := 0
	records := make([]controljournal.StoredEventV1, 0, 512)
	seen := make(map[controljournal.EventID]struct{}, 512)
	var firstPage storegapdb.ScanPage
	for {
		providerPages++
		if providerPages > journalcheckpoint.MaxRestorePages {
			return errors.New("restore provider page budget exceeded")
		}
		page, err := reader.port.Scan(ctx, storegapdb.ScanRequest{
			Prefix: reader.prefix + "journal/", Limit: storegapdb.MaxScanRecords,
			Cursor: providerCursor, MaxBytes: storegapdb.MaxScanBytes,
		})
		if err != nil {
			return err
		}
		if providerPages == 1 {
			if reader.loaded && reader.revision != 0 {
				if page.ObservedRevision == 0 || page.ObservedRevision < reader.revision {
					return journalcheckpoint.ErrStaleScanV1
				}
				if page.ObservedRevision == reader.revision {
					if !sameRestoreProbePageV1(page, reader.firstPage) {
						return journalcheckpoint.ErrStaleScanV1
					}
					return nil
				}
			}
			firstPage = cloneRestoreProbePageV1(page)
		}
		if page.ObservedRevision == 0 && (providerPages != 1 || len(page.Records) != 0 || page.Truncated || page.Cursor != "") ||
			observed != 0 && observed != page.ObservedRevision {
			return journalcheckpoint.ErrStaleScanV1
		}
		observed = page.ObservedRevision
		for _, record := range page.Records {
			event, err := controljournal.DecodeEventEnvelopeV1(record.Value)
			expectedKey := reader.prefix + "journal/" + string(event.EventID)
			_, duplicate := seen[event.EventID]
			if err != nil || record.Revision == 0 || record.Revision > observed || record.ExpiresAt != nil || record.Key != expectedKey || event.ProjectID != reader.projectID || duplicate {
				return errors.New("stored journal record is invalid")
			}
			seen[event.EventID] = struct{}{}
			if record.Revision > after.ThroughRecordRevision {
				records = append(records, controljournal.StoredEventV1{RecordRevision: record.Revision, Envelope: event})
				rawBytes += uint64(len(record.Value))
				if len(records) > journalcheckpoint.MaxRestoreEvents || rawBytes > journalcheckpoint.MaxRestoreBytes {
					reader.installJournalSnapshotV1(records, observed, firstPage, false)
					return nil
				}
			}
		}
		if !page.Truncated {
			if page.Cursor != "" {
				return errors.New("terminal provider scan returned a cursor")
			}
			break
		}
		if page.Cursor == "" || page.Cursor == providerCursor {
			return errors.New("provider scan cursor made no progress")
		}
		providerCursor = page.Cursor
	}
	reader.installJournalSnapshotV1(records, observed, firstPage, true)
	return nil
}

func (reader *adapterRestoreReaderV1) installJournalSnapshotV1(records []controljournal.StoredEventV1, observed uint64, firstPage storegapdb.ScanPage, complete bool) {
	sort.Slice(records, func(i, j int) bool { return records[i].RecordRevision < records[j].RecordRevision })
	reader.records = records
	reader.revision = observed
	reader.loaded = complete
	reader.firstPage = cloneRestoreProbePageV1(firstPage)
}

func cloneRestoreProbePageV1(page storegapdb.ScanPage) storegapdb.ScanPage {
	clone := page
	clone.Records = make([]storegapdb.Record, len(page.Records))
	for index, record := range page.Records {
		clone.Records[index] = record
		clone.Records[index].Value = append([]byte(nil), record.Value...)
		if record.ExpiresAt != nil {
			expires := *record.ExpiresAt
			clone.Records[index].ExpiresAt = &expires
		}
	}
	return clone
}

func sameRestoreProbePageV1(got, want storegapdb.ScanPage) bool {
	if got.ObservedRevision == 0 || got.ObservedRevision != want.ObservedRevision || got.Cursor != want.Cursor || got.Truncated != want.Truncated || len(got.Records) != len(want.Records) {
		return false
	}
	for index := range got.Records {
		left, right := got.Records[index], want.Records[index]
		if left.Key != right.Key || left.Revision != right.Revision || !bytes.Equal(left.Value, right.Value) || !sameExpiryV1(left.ExpiresAt, right.ExpiresAt) {
			return false
		}
	}
	return true
}

func sameExpiryV1(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

const maxReconcileAttemptsV1 = 16

type ReconcileConfigV1 struct {
	Attempts int
	Deadline time.Duration
}

// Reconcile performs only ordinary reads. It does not subscribe to Watch and
// does not resubmit a mutation. Applied is returned only when the complete
// operation-attributable batch is observable at one commit revision.
func (s *Service) Reconcile(ctx context.Context, batch ValidatedBatchV1, config ReconcileConfigV1) (ResultV1, error) {
	if s == nil || s.port == nil || !batch.valid() {
		return ResultV1{}, refusal(OutcomeRefused, "reconciliation requires admitted service and validated batch")
	}
	release, valid := s.admission.operationGuard()
	if !valid {
		return ResultV1{Kind: OutcomeRuntimeEpochConflict, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, nil
	}
	defer release()
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
